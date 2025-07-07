package verifier

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"testing"

	nodev1 "buf.build/gen/go/agglayer/agglayer/protocolbuffers/go/agglayer/node/types/v1"
	v1 "github.com/agglayer/aggkit/aggsender/verifier/proto/v1"
	"github.com/agglayer/aggkit/grpc"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
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

func TestVerifierService_VerifyCertificate(t *testing.T) {
	svc := &VerifierService{}
	cert := &nodev1.Certificate{
		Height: 42,
	}

	resp, err := svc.VerifyCertificate(context.Background(), cert)
	require.NoError(t, err)
	require.IsType(t, &emptypb.Empty{}, resp)
}
