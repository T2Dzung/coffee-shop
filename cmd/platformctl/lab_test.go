package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLabCLIRejectsAmbiguousOrDestructiveRequests(t *testing.T) {
	for _, args := range [][]string{{}, {"iximiuz", "destroy"}, {"iximiuz", "setup"}, {"iximiuz", "resume"}, {"iximiuz", "stop", "--timeout=0"}} {
		var out bytes.Buffer
		require.Error(t, runLab(context.Background(), args, &out, &out))
	}
}
