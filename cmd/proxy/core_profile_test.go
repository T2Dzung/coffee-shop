package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	productconfig "github.com/thangchung/go-coffeeshop/cmd/product/config"
	proxyconfig "github.com/thangchung/go-coffeeshop/cmd/proxy/config"
	productapp "github.com/thangchung/go-coffeeshop/internal/product/app"
	gen "github.com/thangchung/go-coffeeshop/proto/gen"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
)

// The iximiuz core profile needs a real menu flow, not just /healthz. Exercise
// the actual product repository/router through gRPC without a counter, DB or queue.
// This is loopback application integration, not Kubernetes/Cilium runtime evidence.
func TestCoreProfileMenuWithoutCounter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server := grpc.NewServer()
	defer server.Stop()
	_, err := productapp.InitApp(&productconfig.Config{}, server)
	require.NoError(t, err)
	listener := listenLoopback(t)
	defer listener.Close()
	go serveGRPC(server, listener)
	host, port := splitAddress(t, listener.Addr())
	gateway, err := newGateway(ctx, &proxyconfig.Config{
		GRPC: proxyconfig.GRPC{
			ProductHost: host, ProductPort: port,
			CounterHost: "127.0.0.1", CounterPort: 1, // deliberately no counter server
		},
	}, nil)
	require.NoError(t, err)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/api/item-types", nil).WithContext(ctx)
	newHTTPHandler(gateway).ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var menu gen.GetItemTypesResponse
	require.NoError(t, protojson.Unmarshal(response.Body.Bytes(), &menu))
	require.NotEmpty(t, menu.ItemTypes)
	for _, item := range menu.ItemTypes {
		require.NotEmpty(t, item.Name)
	}
}
