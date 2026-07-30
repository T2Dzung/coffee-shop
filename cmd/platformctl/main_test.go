package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestProdRestoreDrillRoutesNestedCommandBeforeProdActionParsing(t *testing.T) {
	t.Parallel()
	err := runProd(context.Background(), []string{"restore-drill", "unknown"}, strings.NewReader(""),
		&bytes.Buffer{}, &bytes.Buffer{}, "")
	if err == nil || !strings.Contains(err.Error(), "restore-drill <run|status|cleanup>") {
		t.Fatalf("runProd() error = %v, want nested restore-drill usage", err)
	}
}

func TestProdRestoreDrillStatusRequiresExplicitState(t *testing.T) {
	t.Parallel()
	err := runProd(context.Background(), []string{"restore-drill", "status"}, strings.NewReader(""),
		&bytes.Buffer{}, &bytes.Buffer{}, "")
	if err == nil || !strings.Contains(err.Error(), "--state is required") {
		t.Fatalf("runProd() error = %v, want explicit state failure", err)
	}
}
