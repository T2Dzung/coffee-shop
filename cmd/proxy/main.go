package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	gwruntime "github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/thangchung/go-coffeeshop/cmd/proxy/config"
	"github.com/thangchung/go-coffeeshop/pkg/logger"
	"github.com/thangchung/go-coffeeshop/pkg/telemetry"
	gen "github.com/thangchung/go-coffeeshop/proto/gen"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"log/slog"
)

func newGateway(
	ctx context.Context,
	cfg *config.Config,
	opts []gwruntime.ServeMuxOption,
) (http.Handler, error) {
	productEndpoint := fmt.Sprintf("%s:%d", cfg.ProductHost, cfg.ProductPort)
	counterEndpoint := fmt.Sprintf("%s:%d", cfg.CounterHost, cfg.CounterPort)

	mux := gwruntime.NewServeMux(opts...)
	dialOpts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithStatsHandler(otelgrpc.NewClientHandler())}

	err := gen.RegisterProductServiceHandlerFromEndpoint(ctx, mux, productEndpoint, dialOpts)
	if err != nil {
		return nil, err
	}

	err = gen.RegisterCounterServiceHandlerFromEndpoint(ctx, mux, counterEndpoint, dialOpts)
	if err != nil {
		return nil, err
	}

	return mux, nil
}

func allowCORS(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			if r.Method == "OPTIONS" && r.Header.Get("Access-Control-Request-Method") != "" {
				preflightHandler(w, r)

				return
			}
		}
		h.ServeHTTP(w, r)
	})
}

func preflightHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	headers := []string{"*"}
	w.Header().Set("Access-Control-Allow-Headers", strings.Join(headers, ","))

	methods := []string{"GET", "HEAD", "POST", "PUT", "DELETE"}
	w.Header().Set("Access-Control-Allow-Methods", strings.Join(methods, ","))

	logger.InfoContext(r.Context(), "preflight request", "http_path", r.URL.Path)
}

func withLogger(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.InfoContext(r.Context(), "http request", "http_method", r.Method, "http_path", r.URL.Path)

		h.ServeHTTP(w, r)
	})
}

func main() {
	logger.SetDefault(logger.Config{Service: "proxy", Environment: logger.Environment(), Level: os.Getenv("LOG_LEVEL")})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.NewConfig()
	if err != nil {
		slog.Error("failed get config", "error", err)
		return
	}
	logger.SetDefault(logger.Config{Service: cfg.Name, Environment: logger.Environment(), Version: cfg.Version, Level: cfg.Log.Level})
	slog.Info("app initialized")

	telCfg, err := telemetry.ParseConfig(cfg.Name, logger.Environment(), cfg.Version)
	if err != nil {
		slog.Error("failed to parse telemetry config", "error", err)
		return
	}
	telemetryShutdown, err := telemetry.New(telCfg)
	if err != nil {
		slog.Error("failed to init telemetry", "error", err)
		return
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := telemetryShutdown(shutdownCtx); err != nil {
			slog.Error("failed to shutdown telemetry", "error", err)
		}
	}()

	mux := http.NewServeMux()

	gw, err := newGateway(ctx, cfg, nil)
	if err != nil {
		slog.ErrorContext(ctx, "failed to create a new gateway", "error", err)
		return
	}

	mux.Handle("/", gw)

	s := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Handler: otelhttp.NewHandler(allowCORS(withLogger(mux)), "HTTP /"),
	}

	go func() {
		<-ctx.Done()
		slog.Info("shutting down the http server")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := s.Shutdown(shutdownCtx); err != nil {
			slog.ErrorContext(ctx, "failed to shutdown http server", "error", err)
		}
	}()

	slog.Info("start listening...", "address", fmt.Sprintf("%s:%d", cfg.Host, cfg.Port))

	if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.ErrorContext(ctx, "failed to listen and serve", "error", err)
	}
}
