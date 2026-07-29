package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	counterconfig "github.com/thangchung/go-coffeeshop/cmd/counter/config"
	proxyconfig "github.com/thangchung/go-coffeeshop/cmd/proxy/config"
	counterrouter "github.com/thangchung/go-coffeeshop/internal/counter/app/router"
	"github.com/thangchung/go-coffeeshop/internal/counter/domain"
	"github.com/thangchung/go-coffeeshop/pkg/telemetry"
	gen "github.com/thangchung/go-coffeeshop/proto/gen"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type testSpanExporter struct {
	mu    sync.Mutex
	spans []sdktrace.ReadOnlySpan
}

func (e *testSpanExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.spans = append(e.spans, spans...)
	return nil
}

func (*testSpanExporter) Shutdown(context.Context) error { return nil }

func (e *testSpanExporter) getSpans() []sdktrace.ReadOnlySpan {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]sdktrace.ReadOnlySpan(nil), e.spans...)
}

type testProductServer struct {
	gen.UnimplementedProductServiceServer
}

func (*testProductServer) GetItemsByType(context.Context, *gen.GetItemsByTypeRequest) (*gen.GetItemsByTypeResponse, error) {
	return &gen.GetItemsByTypeResponse{Items: []*gen.ItemDto{{Type: 0, Price: 3.5}}}, nil
}

type tracingOrderUseCase struct {
	product gen.ProductServiceClient
	called  bool
}

func (*tracingOrderUseCase) GetListOrderFulfillment(context.Context) ([]*domain.Order, error) {
	return nil, nil
}

func (u *tracingOrderUseCase) PlaceOrder(ctx context.Context, _ *domain.PlaceOrderModel) error {
	u.called = true
	_, err := u.product.GetItemsByType(ctx, &gen.GetItemsByTypeRequest{ItemTypes: "0"})
	return err
}

type propagationHarness struct {
	handler  http.Handler
	useCase  *tracingOrderUseCase
	exporter *testSpanExporter
	shutdown func()
}

func newPropagationHarness(t *testing.T) *propagationHarness {
	t.Helper()

	oldTracerProvider := otel.GetTracerProvider()
	oldPropagator := otel.GetTextMapPropagator()
	exporter := &testSpanExporter{}
	shutdownTelemetry, err := telemetry.New(telemetry.Config{
		Endpoint:    "unused:4317",
		Service:     "propagation-test",
		Environment: "test",
		Version:     "1",
		SampleRatio: 1,
	}, telemetry.WithExporter(exporter))
	if err != nil {
		t.Fatalf("telemetry.New() error = %v", err)
	}

	productServer := grpc.NewServer(grpc.StatsHandler(otelgrpc.NewServerHandler()))
	gen.RegisterProductServiceServer(productServer, &testProductServer{})
	productListener := listenLoopback(t)
	go serveGRPC(productServer, productListener)

	productConn, err := grpc.NewClient(
		productListener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		t.Fatalf("create Product client: %v", err)
	}
	useCase := &tracingOrderUseCase{product: gen.NewProductServiceClient(productConn)}

	counterServer := grpc.NewServer(grpc.StatsHandler(otelgrpc.NewServerHandler()))
	counterrouter.NewGRPCCounterServer(counterServer, &counterconfig.Config{}, useCase)
	counterListener := listenLoopback(t)
	go serveGRPC(counterServer, counterListener)

	ctx, cancelGateway := context.WithCancel(context.Background())
	productHost, productPort := splitAddress(t, productListener.Addr())
	counterHost, counterPort := splitAddress(t, counterListener.Addr())
	gw, err := newGateway(ctx, &proxyconfig.Config{GRPC: proxyconfig.GRPC{
		ProductHost: productHost,
		ProductPort: productPort,
		CounterHost: counterHost,
		CounterPort: counterPort,
	}}, nil)
	if err != nil {
		t.Fatalf("newGateway() error = %v", err)
	}

	return &propagationHarness{
		handler:  otelhttp.NewHandler(allowCORS(withLogger(gw)), "HTTP /"),
		useCase:  useCase,
		exporter: exporter,
		shutdown: func() {
			cancelGateway()
			counterServer.Stop()
			productServer.Stop()
			_ = productConn.Close()
			_ = counterListener.Close()
			_ = productListener.Close()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := shutdownTelemetry(shutdownCtx); err != nil {
				t.Errorf("telemetry shutdown: %v", err)
			}
			otel.SetTracerProvider(oldTracerProvider)
			otel.SetTextMapPropagator(oldPropagator)
		},
	}
}

func listenLoopback(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return listener
}

func serveGRPC(server *grpc.Server, listener net.Listener) {
	_ = server.Serve(listener)
}

func splitAddress(t *testing.T, address net.Addr) (string, int) {
	t.Helper()
	host, portText, err := net.SplitHostPort(address.String())
	if err != nil {
		t.Fatalf("split address %q: %v", address, err)
	}
	var port int
	if _, err := fmt.Sscanf(portText, "%d", &port); err != nil {
		t.Fatalf("parse port %q: %v", portText, err)
	}
	return host, port
}

func TestCreateOrderPropagatesOneTraceAcrossHTTPAndGRPCHops(t *testing.T) {
	h := newPropagationHarness(t)

	request := httptest.NewRequest(http.MethodPost, "/v1/api/orders", bytes.NewBufferString(`{
		"loyaltyMemberId":"01234567-89ab-cdef-0123-456789abcdef",
		"timestamp":"2026-07-18T00:00:00Z",
		"baristaItems":[{"itemType":0}]
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	h.handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	if !h.useCase.called {
		t.Fatal("Counter use case was not called")
	}

	h.shutdown()
	assertLinearTrace(t, h.exporter.getSpans(), []trace.SpanKind{
		trace.SpanKindServer,
		trace.SpanKindClient,
		trace.SpanKindServer,
		trace.SpanKindClient,
		trace.SpanKindServer,
	})
}

func TestInvalidUUIDPreservesAPIErrorAndRecordsErrorSpan(t *testing.T) {
	h := newPropagationHarness(t)

	request := httptest.NewRequest(http.MethodPost, "/v1/api/orders", strings.NewReader(`{
		"loyaltyMemberId":"not-a-uuid",
		"timestamp":"2026-07-18T00:00:00Z"
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	h.handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want current API status 500; body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "uuid.Parse") {
		t.Fatalf("response no longer exposes the current UUID parse error: %s", response.Body.String())
	}
	if h.useCase.called {
		t.Fatal("invalid UUID must fail before the use case or Product call")
	}

	h.shutdown()
	spans := h.exporter.getSpans()
	assertLinearTrace(t, spans, []trace.SpanKind{
		trace.SpanKindServer,
		trace.SpanKindClient,
		trace.SpanKindServer,
	})
	var errorSpanFound bool
	for _, span := range spans {
		if span.SpanKind() == trace.SpanKindServer && strings.Contains(span.Name(), "PlaceOrder") {
			errorSpanFound = span.Status().Code == codes.Error
		}
	}
	if !errorSpanFound {
		t.Fatal("Counter PlaceOrder server span did not record error status")
	}
}

func TestAllowCORSPreservesPreflightBehavior(t *testing.T) {
	downstreamCalled := false
	handler := allowCORS(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		downstreamCalled = true
	}))
	request := httptest.NewRequest(http.MethodOptions, "/v1/api/orders", nil)
	request.Header.Set("Origin", "https://example.test")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if downstreamCalled {
		t.Fatal("preflight request unexpectedly reached the gateway")
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want *", got)
	}
	if got := response.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, http.MethodPost) {
		t.Fatalf("Access-Control-Allow-Methods = %q, want POST", got)
	}
}

func TestHealthzIsShallowAndDoesNotReachGateway(t *testing.T) {
	t.Parallel()
	gatewayCalled := false
	handler := newHTTPHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		gatewayCalled = true
	}))

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if response.Body.String() != "ok\n" {
		t.Fatalf("body = %q, want %q", response.Body.String(), "ok\n")
	}
	if gatewayCalled {
		t.Fatal("health check unexpectedly reached the gRPC gateway")
	}
}

func TestHealthzRejectsMutationMethodsWithoutReachingGateway(t *testing.T) {
	t.Parallel()
	gatewayCalled := false
	handler := newHTTPHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		gatewayCalled = true
	}))

	request := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", response.Code)
	}
	if response.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("Allow = %q, want %q", response.Header().Get("Allow"), "GET, HEAD")
	}
	if gatewayCalled {
		t.Fatal("mutation request to health check unexpectedly reached the gRPC gateway")
	}
}

func TestHTTPHandlerPreservesGatewayRouting(t *testing.T) {
	t.Parallel()
	var observedPath string
	handler := newHTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observedPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/api/v1/api/item-types", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", response.Code)
	}
	if observedPath != "/v1/api/item-types" {
		t.Fatalf("gateway path = %q, want %q", observedPath, "/v1/api/item-types")
	}
}

func assertLinearTrace(t *testing.T, spans []sdktrace.ReadOnlySpan, wantKinds []trace.SpanKind) {
	t.Helper()
	if len(spans) != len(wantKinds) {
		names := make([]string, 0, len(spans))
		for _, span := range spans {
			names = append(names, fmt.Sprintf("%s(%s)", span.Name(), span.SpanKind()))
		}
		t.Fatalf("span count = %d, want %d: %v", len(spans), len(wantKinds), names)
	}

	byParent := make(map[trace.SpanID][]sdktrace.ReadOnlySpan, len(spans))
	var current sdktrace.ReadOnlySpan
	for _, span := range spans {
		byParent[span.Parent().SpanID()] = append(byParent[span.Parent().SpanID()], span)
		if !span.Parent().IsValid() {
			if current != nil {
				t.Fatal("trace contains more than one root span")
			}
			current = span
		}
	}
	if current == nil {
		t.Fatal("trace has no root span")
	}
	traceID := current.SpanContext().TraceID()
	for index, wantKind := range wantKinds {
		if current.SpanKind() != wantKind {
			t.Fatalf("hop %d kind = %s, want %s (span %q)", index, current.SpanKind(), wantKind, current.Name())
		}
		if current.SpanContext().TraceID() != traceID {
			t.Fatalf("hop %d has a different trace ID", index)
		}
		if index == len(wantKinds)-1 {
			break
		}
		children := byParent[current.SpanContext().SpanID()]
		if len(children) != 1 {
			t.Fatalf("span %q has %d children, want 1", current.Name(), len(children))
		}
		current = children[0]
	}
}
