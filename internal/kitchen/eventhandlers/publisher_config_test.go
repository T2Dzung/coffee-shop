package eventhandlers

import (
	"context"
	"testing"

	"github.com/thangchung/go-coffeeshop/pkg/rabbitmq/publisher"
)

type configureProbe struct {
	calls  int
	result publisher.EventPublisher
}

func (p *configureProbe) Configure(opts ...publisher.Option) publisher.EventPublisher {
	p.calls++
	if len(opts) != 3 {
		panic("expected exchange, binding and message type options")
	}
	return p.result
}
func (*configureProbe) Publish(context.Context, []byte, string) error { return nil }

func TestConstructorUsesConfiguredCounterPublisher(t *testing.T) {
	configured := &configureProbe{}
	base := &configureProbe{result: configured}
	handler := NewKitchenOrderedEventHandler(nil, base).(*kitchenOrderedEventHandler)
	if base.calls != 1 || handler.counterPub != configured {
		t.Fatal("handler must retain the configured completion-event publisher")
	}
}
