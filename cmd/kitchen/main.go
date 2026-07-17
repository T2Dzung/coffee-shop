package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/thangchung/go-coffeeshop/cmd/kitchen/config"
	"github.com/thangchung/go-coffeeshop/internal/kitchen/app"
	"github.com/thangchung/go-coffeeshop/pkg/logger"
	"github.com/thangchung/go-coffeeshop/pkg/postgres"
	"github.com/thangchung/go-coffeeshop/pkg/rabbitmq"
	"go.uber.org/automaxprocs/maxprocs"
	"log/slog"

	pkgConsumer "github.com/thangchung/go-coffeeshop/pkg/rabbitmq/consumer"
	pkgPublisher "github.com/thangchung/go-coffeeshop/pkg/rabbitmq/publisher"

	_ "github.com/lib/pq"
)

func main() {
	logger.SetDefault(logger.Config{Service: "kitchen", Environment: logger.Environment(), Level: os.Getenv("LOG_LEVEL")})

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

	a, cleanup, err := app.InitApp(cfg, postgres.DBConnString(cfg.PG.DsnURL), rabbitmq.RabbitMQConnStr(cfg.RabbitMQ.URL))
	if err != nil {
		slog.ErrorContext(ctx, "failed init app", "error", err)
		cancel()
	}

	a.CounterOrderPub.Configure(
		pkgPublisher.ExchangeName("counter-order-exchange"),
		pkgPublisher.BindingKey("counter-order-routing-key"),
		pkgPublisher.MessageTypeName("kitchen-order-updated"),
	)

	a.Consumer.Configure(
		pkgConsumer.ExchangeName("kitchen-order-exchange"),
		pkgConsumer.QueueName("kitchen-order-queue"),
		pkgConsumer.BindingKey("kitchen-order-routing-key"),
		pkgConsumer.ConsumerTag("kitchen-order-consumer"),
	)

	slog.Info("🌏 start server...", "address", fmt.Sprintf("%s:%d", cfg.HTTP.Host, cfg.HTTP.Port))

	go func() {
		err := a.Consumer.StartConsumer(a.Worker)
		if err != nil {
			slog.ErrorContext(ctx, "failed to start consumer", "error", err)
			cancel()
		}
	}()

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
