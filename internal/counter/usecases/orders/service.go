package orders

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/wire"
	"github.com/pkg/errors"
	"github.com/thangchung/go-coffeeshop/internal/counter/domain"
	"github.com/thangchung/go-coffeeshop/pkg/logger"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"log/slog"
)

const ordersTracerName = "github.com/thangchung/go-coffeeshop/internal/counter/usecases/orders"

type usecase struct {
	orderRepo        OrderRepo
	productDomainSvc domain.ProductDomainService
	baristaEventPub  BaristaEventPublisher
	kitchenEventPub  KitchenEventPublisher
}

var _ UseCase = (*usecase)(nil)

var UseCaseSet = wire.NewSet(NewUseCase)

func NewUseCase(
	orderRepo OrderRepo,
	productDomainSvc domain.ProductDomainService,
	baristaEventPub BaristaEventPublisher,
	kitchenEventPub KitchenEventPublisher,
) UseCase {
	return &usecase{
		orderRepo:        orderRepo,
		productDomainSvc: productDomainSvc,
		baristaEventPub:  baristaEventPub,
		kitchenEventPub:  kitchenEventPub,
	}
}

func (uc *usecase) GetListOrderFulfillment(ctx context.Context) ([]*domain.Order, error) {
	entities, err := uc.orderRepo.GetAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("orderRepo.GetAll: %w", err)
	}

	return entities, nil
}

func (uc *usecase) PlaceOrder(ctx context.Context, model *domain.PlaceOrderModel) error {
	productCtx, productSpan := otel.Tracer(ordersTracerName).Start(ctx, "order.product_lookup")
	order, err := domain.CreateOrderFrom(productCtx, model, uc.productDomainSvc)
	if err != nil {
		logger.ErrorContext(productCtx, "product lookup failed", "error", err)
	}
	finishSpan(productSpan, err)
	if err != nil {
		return errors.Wrap(err, "domain.CreateOrderFrom")
	}

	dbCtx, dbSpan := otel.Tracer(ordersTracerName).Start(ctx, "db.order.create",
		trace.WithAttributes(
			attribute.String("db.system.name", "postgresql"),
			attribute.String("db.namespace", "order"),
			attribute.String("db.operation.name", "INSERT"),
		),
	)
	err = uc.orderRepo.Create(dbCtx, order)
	if err != nil {
		logger.ErrorContext(dbCtx, "order transaction failed", "error", err)
	}
	finishSpan(dbSpan, err)
	if err != nil {
		return errors.Wrap(err, "orderRepo.Create")
	}

	slog.DebugContext(ctx, "order created", "order_id", order.ID.String(), "line_item_count", len(order.LineItems))

	// The database transaction is already committed at this point. Returning a
	// publish error makes the partial side effect visible, but does not make the
	// DB + RabbitMQ dual write atomic. A transactional outbox remains deferred.
	publishedEventCount := 0
	for _, event := range order.DomainEvents() {
		if event.Identity() == "BaristaOrdered" {
			eventBytes, err := json.Marshal(event)
			if err != nil {
				logger.ErrorContext(ctx, "barista event marshal failed", "error", err)
				return errors.Wrap(err, "json.Marshal[event]")
			}

			err = publishEvent(ctx, uc.baristaEventPub, eventBytes, "barista-order", publishedEventCount > 0)
			if err != nil {
				return errors.Wrap(err, "baristaEventPub.Publish")
			}
			publishedEventCount++
		}

		if event.Identity() == "KitchenOrdered" {
			eventBytes, err := json.Marshal(event)
			if err != nil {
				logger.ErrorContext(ctx, "kitchen event marshal failed", "error", err)
				return errors.Wrap(err, "json.Marshal[event]")
			}

			err = publishEvent(ctx, uc.kitchenEventPub, eventBytes, "kitchen-order", publishedEventCount > 0)
			if err != nil {
				return errors.Wrap(err, "kitchenEventPub.Publish")
			}
			publishedEventCount++
		}
	}

	return nil
}

type eventPublisher interface {
	Publish(context.Context, []byte, string) error
}

func publishEvent(
	ctx context.Context,
	publisher eventPublisher,
	body []byte,
	destination string,
	partialSideEffect bool,
) error {
	publishCtx, span := otel.Tracer(ordersTracerName).Start(ctx, "messaging.publish",
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String("messaging.system", "rabbitmq"),
			attribute.String("messaging.destination.name", destination),
			attribute.String("messaging.operation.name", "publish"),
		),
	)
	err := publisher.Publish(publishCtx, body, "text/plain")
	partialFailure := err != nil && partialSideEffect
	span.SetAttributes(attribute.Bool("order.publish.partial_side_effect", partialFailure))
	if err != nil {
		logger.ErrorContext(publishCtx, "message publish failed",
			"error", err,
			"messaging_destination", destination,
			"partial_side_effect", partialFailure,
		)
	}
	finishSpan(span, err)
	return err
}

func finishSpan(span trace.Span, err error) {
	defer span.End()
	if err == nil {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}
