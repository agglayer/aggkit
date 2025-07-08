package grpc

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestServer(t *testing.T) {
	cfg := ServerConfig{
		Host:             "127.0.0.1",
		Port:             1111,
		EnableReflection: true,
	}

	s, err := NewServer(cfg)
	require.NoError(t, err, "Failed to create gRPC server")
	require.NotNil(t, s.GRPC())
	require.Equal(t, fmt.Sprintf("%s:%d", cfg.Host, cfg.Port), s.Addr(), "Server address should match config")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	// Start the server in a goroutine
	go func() {
		s.Start(ctx)
		close(done)
	}()

	// Wait for the server to start
	time.Sleep(200 * time.Millisecond)

	cancel()

	select {
	case <-done:
		// Success: server stopped after context cancel
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop after context cancellation")
	}
}
