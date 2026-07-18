package publisher

import "testing"

func TestConfigureReturnsIndependentPublisher(t *testing.T) {
	base := &publisher{
		exchangeName:    _exchangeName,
		bindingKey:      _bindingKey,
		messageTypeName: _messageTypeName,
	}

	barista := base.Configure(
		ExchangeName("barista-order-exchange"),
		BindingKey("barista-order-routing-key"),
		MessageTypeName("barista-order-created"),
	).(*publisher)
	kitchen := base.Configure(
		ExchangeName("kitchen-order-exchange"),
		BindingKey("kitchen-order-routing-key"),
		MessageTypeName("kitchen-order-created"),
	).(*publisher)

	if barista == kitchen || barista == base || kitchen == base {
		t.Fatal("Configure must return independent publisher values")
	}
	if barista.exchangeName != "barista-order-exchange" {
		t.Fatalf("barista exchange = %q", barista.exchangeName)
	}
	if kitchen.exchangeName != "kitchen-order-exchange" {
		t.Fatalf("kitchen exchange = %q", kitchen.exchangeName)
	}
	if base.exchangeName != _exchangeName {
		t.Fatalf("base publisher mutated to %q", base.exchangeName)
	}
}
