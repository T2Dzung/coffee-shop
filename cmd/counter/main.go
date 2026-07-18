package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/thangchung/go-coffeeshop/cmd/counter/config"
	"github.com/thangchung/go-coffeeshop/internal/counter/app"
	"github.com/thangchung/go-coffeeshop/pkg/logger"
	"github.com/thangchung/go-coffeeshop/pkg/postgres"
	"github.com/thangchung/go-coffeeshop/pkg/rabbitmq"
	"github.com/thangchung/go-coffeeshop/pkg/telemetry"
	"go.uber.org/automaxprocs/maxprocs"
	"google.golang.org/grpc"
	"log/slog"

	pkgConsumer "github.com/thangchung/go-coffeeshop/pkg/rabbitmq/consumer"
	pkgPublisher "github.com/thangchung/go-coffeeshop/pkg/rabbitmq/publisher"

	_ "github.com/lib/pq"
)

func main() {
	logger.SetDefault(logger.Config{Service: "counter", Environment: logger.Environment(), Level: os.Getenv("LOG_LEVEL")})

	// set GOMAXPROCS
	_, err := maxprocs.Set()
	if err != nil {
		slog.Error("failed set max procs", "error", err)
	}

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

	server := grpc.NewServer()

	go func() {
		defer server.GracefulStop()
		<-ctx.Done()
	}()

	cleanup := prepareApp(ctx, stop, cfg, server)

	// gRPC Server.
	address := fmt.Sprintf("%s:%d", cfg.HTTP.Host, cfg.HTTP.Port)
	network := "tcp"

	l, err := net.Listen(network, address)
	if err != nil {
		slog.Error("failed to listen to address", "error", err, "network", network, "address", address)
		return
	}

	slog.Info("🌏 start server...", "address", address)

	defer func() {
		if err1 := l.Close(); err != nil && !errors.Is(err1, net.ErrClosed) {
			slog.Error("failed to close", "error", err1, "network", network, "address", address)
		}
	}()

	err = server.Serve(l)
	if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		slog.Error("failed start gRPC server", "error", err, "network", network, "address", address)
	}

	cleanup()
}

func prepareApp(ctx context.Context, cancel context.CancelFunc, cfg *config.Config, server *grpc.Server) func() {
	a, cleanup, err := app.InitApp(cfg, postgres.DBConnString(cfg.PG.DsnURL), rabbitmq.RabbitMQConnStr(cfg.RabbitMQ.URL), server)
	if err != nil {
		slog.ErrorContext(ctx, "failed init app", "error", err)
		cancel()
		<-ctx.Done()
	}

	a.BaristaOrderPub.Configure(
		pkgPublisher.ExchangeName("barista-order-exchange"),
		pkgPublisher.BindingKey("barista-order-routing-key"),
		pkgPublisher.MessageTypeName("barista-order-created"),
	)

	a.KitchenOrderPub.Configure(
		pkgPublisher.ExchangeName("kitchen-order-exchange"),
		pkgPublisher.BindingKey("kitchen-order-routing-key"),
		pkgPublisher.MessageTypeName("kitchen-order-created"),
	)

	a.Consumer.Configure(
		pkgConsumer.ExchangeName("counter-order-exchange"),
		pkgConsumer.QueueName("counter-order-queue"),
		pkgConsumer.BindingKey("counter-order-routing-key"),
		pkgConsumer.ConsumerTag("counter-order-consumer"),
	)

	go func() {
		err1 := a.Consumer.StartConsumer(a.Worker)
		if err1 != nil {
			slog.ErrorContext(ctx, "failed to start consumer", "error", err1)
			cancel()
			<-ctx.Done()
		}
	}()

	return cleanup
}
