package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/thangchung/go-coffeeshop/cmd/product/config"
	"github.com/thangchung/go-coffeeshop/internal/product/app"
	"github.com/thangchung/go-coffeeshop/pkg/logger"
	"go.uber.org/automaxprocs/maxprocs"
	"google.golang.org/grpc"
	"log/slog"
)

func main() {
	logger.SetDefault(logger.Config{Service: "product", Environment: logger.Environment(), Level: os.Getenv("LOG_LEVEL")})

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

	_, err = app.InitApp(cfg, server)
	if err != nil {
		slog.Error("failed init app", "error", err)
		cancel()
	}

	// gRPC Server.
	address := fmt.Sprintf("%s:%d", cfg.HTTP.Host, cfg.HTTP.Port)
	network := "tcp"

	l, err := net.Listen(network, address)
	if err != nil {
		slog.Error("failed to listen to address", "error", err, "network", network, "address", address)
		cancel()
	}

	slog.Info("🌏 start server...", "address", address)

	defer func() {
		if err1 := l.Close(); err != nil {
			slog.Error("failed to close", "error", err1, "network", network, "address", address)
		}
	}()

	err = server.Serve(l)
	if err != nil {
		slog.Error("failed start gRPC server", "error", err, "network", network, "address", address)
		cancel()
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	select {
	case v := <-quit:
		slog.Info("shutdown signal received", "signal", v.String())
	case done := <-ctx.Done():
		slog.Info("application context done", "error", done)
	}
}
