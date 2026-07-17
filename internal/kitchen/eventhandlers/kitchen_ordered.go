package eventhandlers

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/google/wire"
	"github.com/pkg/errors"
	"github.com/thangchung/go-coffeeshop/internal/kitchen/domain"
	"github.com/thangchung/go-coffeeshop/internal/kitchen/infras/postgresql"
	"github.com/thangchung/go-coffeeshop/internal/pkg/event"
	"github.com/thangchung/go-coffeeshop/pkg/postgres"
	pkgPublisher "github.com/thangchung/go-coffeeshop/pkg/rabbitmq/publisher"
	"log/slog"
)

type kitchenOrderedEventHandler struct {
	pg         postgres.DBEngine
	counterPub pkgPublisher.EventPublisher
}

var _ KitchenOrderedEventHandler = (*kitchenOrderedEventHandler)(nil)

var KitchenOrderedEventHandlerSet = wire.NewSet(NewKitchenOrderedEventHandler)

func NewKitchenOrderedEventHandler(
	pg postgres.DBEngine,
	counterPub pkgPublisher.EventPublisher,
) KitchenOrderedEventHandler {
	return &kitchenOrderedEventHandler{
		pg:         pg,
		counterPub: counterPub,
	}
}

func (h *kitchenOrderedEventHandler) Handle(ctx context.Context, e event.KitchenOrdered) error {
	slog.InfoContext(ctx, "kitchen order event received", "order_id", e.OrderID.String(), "item_line_id", e.ItemLineID.String())

	order := domain.NewKitchenOrder(e)

	db := h.pg.GetDB()
	querier := postgresql.New(db)

	tx, err := db.Begin()
	if err != nil {
		return errors.Wrap(err, "kitchenOrderedEventHandler.Handle")
	}

	qtx := querier.WithTx(tx)

	_, err = qtx.CreateOrder(ctx, postgresql.CreateOrderParams{
		ID:       order.ID,
		OrderID:  e.OrderID,
		ItemType: int32(order.ItemType),
		ItemName: order.ItemName,
		TimeUp:   order.TimeUp,
		Created:  order.Created,
		Updated: sql.NullTime{
			Time:  order.Updated,
			Valid: true,
		},
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to create kitchen order", "error", err, "order_id", e.OrderID.String())

		return errors.Wrap(err, "kitchenOrderedEventHandler-querier.CreateOrder")
	}

	// todo: it might cause dual-write problem, but we accept it temporary
	for _, event := range order.DomainEvents() {
		eventBytes, err := json.Marshal(event)
		if err != nil {
			return errors.Wrap(err, "json.Marshal[event]")
		}

		if err := h.counterPub.Publish(ctx, eventBytes, "text/plain"); err != nil {
			return errors.Wrap(err, "counterPub.Publish")
		}
	}

	return tx.Commit()
}
