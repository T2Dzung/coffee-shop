package app

import (
	"context"
	"encoding/json"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/thangchung/go-coffeeshop/cmd/counter/config"
	"github.com/thangchung/go-coffeeshop/internal/counter/domain"
	"github.com/thangchung/go-coffeeshop/internal/counter/events"
	ordersUC "github.com/thangchung/go-coffeeshop/internal/counter/usecases/orders"
	shared "github.com/thangchung/go-coffeeshop/internal/pkg/event"
	"github.com/thangchung/go-coffeeshop/pkg/postgres"
	pkgConsumer "github.com/thangchung/go-coffeeshop/pkg/rabbitmq/consumer"
	pkgPublisher "github.com/thangchung/go-coffeeshop/pkg/rabbitmq/publisher"
	"github.com/thangchung/go-coffeeshop/proto/gen"
	"log/slog"
)

type App struct {
	Cfg       *config.Config
	PG        postgres.DBEngine
	AMQPConn  *amqp.Connection
	Publisher pkgPublisher.EventPublisher
	Consumer  pkgConsumer.EventConsumer

	BaristaOrderPub ordersUC.BaristaEventPublisher
	KitchenOrderPub ordersUC.KitchenEventPublisher

	ProductDomainSvc  domain.ProductDomainService
	UC                ordersUC.UseCase
	CounterGRPCServer gen.CounterServiceServer

	baristaHandler events.BaristaOrderUpdatedEventHandler
	kitchenHandler events.KitchenOrderUpdatedEventHandler
}

func New(
	cfg *config.Config,
	pg postgres.DBEngine,
	amqpConn *amqp.Connection,
	publisher pkgPublisher.EventPublisher,
	consumer pkgConsumer.EventConsumer,

	baristaOrderPub ordersUC.BaristaEventPublisher,
	kitchenOrderPub ordersUC.KitchenEventPublisher,
	productDomainSvc domain.ProductDomainService,
	uc ordersUC.UseCase,
	counterGRPCServer gen.CounterServiceServer,

	baristaHandler events.BaristaOrderUpdatedEventHandler,
	kitchenHandler events.KitchenOrderUpdatedEventHandler,
) *App {
	return &App{
		Cfg: cfg,

		PG:        pg,
		AMQPConn:  amqpConn,
		Publisher: publisher,
		Consumer:  consumer,

		BaristaOrderPub: baristaOrderPub,
		KitchenOrderPub: kitchenOrderPub,

		ProductDomainSvc:  productDomainSvc,
		UC:                uc,
		CounterGRPCServer: counterGRPCServer,

		baristaHandler: baristaHandler,
		kitchenHandler: kitchenHandler,
	}
}

func (a *App) Worker(ctx context.Context, messages <-chan amqp.Delivery) {
	for delivery := range messages {
		slog.InfoContext(ctx, "processing delivery", "delivery_tag", delivery.DeliveryTag, "delivery_type", delivery.Type)

		switch delivery.Type {
		case "barista-order-updated":
			var payload shared.BaristaOrderUpdated

			err := json.Unmarshal(delivery.Body, &payload)
			if err != nil {
				slog.ErrorContext(ctx, "failed to unmarshal delivery", "error", err)
			}

			err = a.baristaHandler.Handle(ctx, &payload)

			if err != nil {
				if err = delivery.Reject(false); err != nil {
					slog.ErrorContext(ctx, "failed to reject delivery", "error", err)
				}

				slog.ErrorContext(ctx, "failed to process delivery", "error", err)
			} else {
				err = delivery.Ack(false)
				if err != nil {
					slog.ErrorContext(ctx, "failed to acknowledge delivery", "error", err)
				}
			}
		case "kitchen-order-updated":
			var payload shared.KitchenOrderUpdated

			err := json.Unmarshal(delivery.Body, &payload)
			if err != nil {
				slog.ErrorContext(ctx, "failed to unmarshal delivery", "error", err)
			}

			err = a.kitchenHandler.Handle(ctx, &payload)

			if err != nil {
				if err = delivery.Reject(false); err != nil {
					slog.ErrorContext(ctx, "failed to reject delivery", "error", err)
				}

				slog.ErrorContext(ctx, "failed to process delivery", "error", err)
			} else {
				err = delivery.Ack(false)
				if err != nil {
					slog.ErrorContext(ctx, "failed to acknowledge delivery", "error", err)
				}
			}
		default:
			slog.WarnContext(ctx, "unsupported delivery type", "delivery_type", delivery.Type)
		}
	}

	slog.InfoContext(ctx, "deliveries channel closed")
}
