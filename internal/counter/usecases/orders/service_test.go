package orders

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/thangchung/go-coffeeshop/internal/counter/domain"
	shared "github.com/thangchung/go-coffeeshop/internal/pkg/shared_kernel"
	"github.com/thangchung/go-coffeeshop/pkg/rabbitmq/publisher"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type fakeProductService struct {
	calls *[]string
	err   error
}

func (f *fakeProductService) GetItemsByType(_ context.Context, _ *domain.PlaceOrderModel, barista bool) ([]*domain.ItemModel, error) {
	name := "product:kitchen"
	itemType := shared.ItemTypeCakePop
	if barista {
		name = "product:barista"
		itemType = shared.ItemTypeCappuccino
	}
	*f.calls = append(*f.calls, name)
	if f.err != nil {
		return nil, f.err
	}
	return []*domain.ItemModel{{ItemType: itemType, Price: 3.5}}, nil
}

type fakeOrderRepo struct {
	calls *[]string
	err   error
}

func (*fakeOrderRepo) GetAll(context.Context) ([]*domain.Order, error) { return nil, nil }
func (*fakeOrderRepo) GetByID(context.Context, uuid.UUID) (*domain.Order, error) {
	return nil, nil
}
func (f *fakeOrderRepo) Create(context.Context, *domain.Order) error {
	*f.calls = append(*f.calls, "db:create")
	return f.err
}
func (*fakeOrderRepo) Update(context.Context, *domain.Order) (*domain.Order, error) {
	return nil, nil
}

type fakeEventPublisher struct {
	name  string
	calls *[]string
	err   error
}

func (*fakeEventPublisher) Configure(...publisher.Option) {}
func (f *fakeEventPublisher) Publish(ctx context.Context, _ []byte, _ string) error {
	*f.calls = append(*f.calls, f.name)
	if f.err != nil {
		return f.err
	}
	return ctx.Err()
}

func TestPlaceOrderFailureMatrix(t *testing.T) {
	tests := []struct {
		name            string
		productErr      error
		dbErr           error
		baristaErr      error
		kitchenErr      error
		cancelContext   bool
		wantErr         string
		wantCalls       []string
		wantErrorSpan   string
		wantDestination string
		wantPartial     bool
	}{
		{
			name: "product lookup error stops DB and publish", productErr: errors.New("product unavailable"),
			wantErr: "domain.CreateOrderFrom", wantCalls: []string{"product:barista"}, wantErrorSpan: "order.product_lookup",
		},
		{
			name: "DB error stops publish", dbErr: errors.New("commit failed"),
			wantErr: "orderRepo.Create", wantCalls: []string{"product:barista", "product:kitchen", "db:create"}, wantErrorSpan: "db.order.create",
		},
		{
			name: "barista publish error stops kitchen", baristaErr: errors.New("barista nack"),
			wantErr: "baristaEventPub.Publish", wantCalls: []string{"product:barista", "product:kitchen", "db:create", "publish:barista"},
			wantErrorSpan: "messaging.publish", wantDestination: "barista-order",
		},
		{
			name: "kitchen publish error exposes partial side effect", kitchenErr: errors.New("kitchen timeout"),
			wantErr: "kitchenEventPub.Publish", wantCalls: []string{"product:barista", "product:kitchen", "db:create", "publish:barista", "publish:kitchen"},
			wantErrorSpan: "messaging.publish", wantDestination: "kitchen-order", wantPartial: true,
		},
		{
			name: "canceled context propagates from publish", cancelContext: true,
			wantErr: "context canceled", wantCalls: []string{"product:barista", "product:kitchen", "db:create", "publish:barista"},
			wantErrorSpan: "messaging.publish", wantDestination: "barista-order",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := []string{}
			recorder, restore := installSpanRecorder(t)
			defer restore()

			ctx := context.Background()
			if tt.cancelContext {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			uc := NewUseCase(
				&fakeOrderRepo{calls: &calls, err: tt.dbErr},
				&fakeProductService{calls: &calls, err: tt.productErr},
				&fakeEventPublisher{name: "publish:barista", calls: &calls, err: tt.baristaErr},
				&fakeEventPublisher{name: "publish:kitchen", calls: &calls, err: tt.kitchenErr},
			)

			err := uc.PlaceOrder(ctx, validPlaceOrderModel())
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("PlaceOrder() error = %v, want substring %q", err, tt.wantErr)
			}
			assertCalls(t, calls, tt.wantCalls)
			assertErrorSpan(t, recorder.Ended(), tt.wantErrorSpan, tt.wantDestination, tt.wantPartial)
		})
	}
}

func TestPlaceOrderSuccessPublishesBothEvents(t *testing.T) {
	calls := []string{}
	recorder, restore := installSpanRecorder(t)
	defer restore()
	uc := NewUseCase(
		&fakeOrderRepo{calls: &calls},
		&fakeProductService{calls: &calls},
		&fakeEventPublisher{name: "publish:barista", calls: &calls},
		&fakeEventPublisher{name: "publish:kitchen", calls: &calls},
	)

	if err := uc.PlaceOrder(context.Background(), validPlaceOrderModel()); err != nil {
		t.Fatalf("PlaceOrder() error = %v", err)
	}
	assertCalls(t, calls, []string{"product:barista", "product:kitchen", "db:create", "publish:barista", "publish:kitchen"})

	spans := recorder.Ended()
	counts := map[string]int{}
	for _, span := range spans {
		counts[span.Name()]++
		if span.Status().Code == codes.Error {
			t.Fatalf("successful span %q has error status", span.Name())
		}
	}
	if counts["order.product_lookup"] != 1 || counts["db.order.create"] != 1 || counts["messaging.publish"] != 2 {
		t.Fatalf("unexpected span counts: %#v", counts)
	}
}

func validPlaceOrderModel() *domain.PlaceOrderModel {
	return &domain.PlaceOrderModel{
		OrderSource:     shared.OrderSourceWeb,
		Location:        shared.LocationAtlanta,
		LoyaltyMemberID: uuid.MustParse("01234567-89ab-cdef-0123-456789abcdef"),
		BaristaItems:    []*domain.OrderItemModel{{ItemType: shared.ItemTypeCappuccino}},
		KitchenItems:    []*domain.OrderItemModel{{ItemType: shared.ItemTypeCakePop}},
	}
}

func installSpanRecorder(t *testing.T) (*tracetest.SpanRecorder, func()) {
	t.Helper()
	previous := otel.GetTracerProvider()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(provider)
	return recorder, func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(previous)
	}
}

func assertCalls(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("call order = %v, want %v", got, want)
	}
}

func assertErrorSpan(t *testing.T, spans []sdktrace.ReadOnlySpan, name, destination string, partial bool) {
	t.Helper()
	for _, span := range spans {
		if span.Name() == name && span.Status().Code == codes.Error {
			if destination != "" {
				attrs := spanAttributes(span.Attributes())
				if attrs["messaging.destination.name"].AsString() != destination {
					t.Fatalf("error span destination = %q, want %q", attrs["messaging.destination.name"].AsString(), destination)
				}
				if attrs["order.publish.partial_side_effect"].AsBool() != partial {
					t.Fatalf("partial_side_effect = %v, want %v", attrs["order.publish.partial_side_effect"].AsBool(), partial)
				}
			}
			return
		}
	}
	t.Fatalf("no error span named %q in %d ended spans", name, len(spans))
}

func spanAttributes(attrs []attribute.KeyValue) map[string]attribute.Value {
	result := make(map[string]attribute.Value, len(attrs))
	for _, attr := range attrs {
		result[string(attr.Key)] = attr.Value
	}
	return result
}
