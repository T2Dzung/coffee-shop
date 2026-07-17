package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/thangchung/go-coffeeshop/cmd/counter/config"
	"github.com/thangchung/go-coffeeshop/internal/counter/app"
	"github.com/thangchung/go-coffeeshop/pkg/logger"
	"github.com/thangchung/go-coffeeshop/pkg/postgres"
	"github.com/thangchung/go-coffeeshop/pkg/rabbitmq"
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

	ctx, cancel := context.WithCancel(context.Background())

	cfg, err := config.NewConfig()
	if err != nil {
		slog.Error("failed get config", "error", err)
		return
	}
	logger.SetDefault(logger.Config{Service: cfg.Name, Environment: logger.Environment(), Version: cfg.Version, Level: cfg.Log.Level})
	slog.Info("app initialized")

	server := grpc.NewServer()

	go func() {
		defer server.GracefulStop()
		<-ctx.Done()
	}()

	cleanup := prepareApp(ctx, cancel, cfg, server)

	// gRPC Server.
	address := fmt.Sprintf("%s:%d", cfg.HTTP.Host, cfg.HTTP.Port)
	network := "tcp"

	l, err := net.Listen(network, address)
	if err != nil {
		slog.Error("failed to listen to address", "error", err, "network", network, "address", address)
		cancel()
		<-ctx.Done()
	}

	slog.Info("🌏 start server...", "address", address)

	defer func() {
		if err1 := l.Close(); err != nil {
			slog.Error("failed to close", "error", err1, "network", network, "address", address)
			<-ctx.Done()
		}
	}()

	err = server.Serve(l)
	if err != nil {
		slog.Error("failed start gRPC server", "error", err, "network", network, "address", address)
		cancel()
		<-ctx.Done()
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	select {
	case v := <-quit:
		cleanup()
		slog.Info("shutdown signal received", "signal", v.String())
	case done := <-ctx.Done():
		cleanup()
		slog.Info("application context done", "error", done)
	}
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
