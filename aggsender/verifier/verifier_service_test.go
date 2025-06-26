package verifier

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"testing"

	v1 "github.com/agglayer/aggkit/aggsender/verifier/proto/v1"
	"github.com/agglayer/aggkit/grpc"
	"github.com/stretchr/testify/require"
)

func TestVerifierService(t *testing.T) {
	t.Skip("Skipping test for VerifierService, this is only for debugging purposes")

	cfg := grpc.ServerConfig{
		Host:             "localhost",
		Port:             9090,
		EnableReflection: true,
	}

	// Create the server
	server, err := grpc.NewServer(cfg)
	require.NoError(t, err, "Failed to create gRPC server")

	// Register the Verifier service
	v1.RegisterAggsenderVerifierServer(server.GRPC(), &VerifierService{})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
		<-c
		t.Log("Received shutdown signal, stopping server...")
		cancel()
	}()

	server.Start(ctx)
}
