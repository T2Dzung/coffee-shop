package controller_test

import (
	"testing"
	"time"

	"github.com/T2Dzung/coffee-shop/platform-ownership-guard/internal/controller"
)

func TestDefaultJitterBounds(t *testing.T) {
	if got := controller.DefaultJitter(0); got != 0 {
		t.Fatalf("zero interval must stay zero, got %s", got)
	}
	if got := controller.DefaultJitter(24 * time.Hour); got != 24*time.Hour {
		t.Fatalf("24h interval must remain within the public upper bound, got %s", got)
	}

	base := 10 * time.Minute
	upper := base + base/10
	for i := 0; i < 100; i++ {
		got := controller.DefaultJitter(base)
		if got < base || got >= upper {
			t.Fatalf("jittered interval %s outside [%s, %s)", got, base, upper)
		}
	}

	nearLimit := 23*time.Hour + 59*time.Minute
	if got := controller.DefaultJitter(nearLimit); got < nearLimit || got > 24*time.Hour {
		t.Fatalf("near-limit jitter %s outside [%s, 24h]", got, nearLimit)
	}
}
