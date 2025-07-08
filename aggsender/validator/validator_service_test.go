package validator

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"testing"

	nodev1 "buf.build/gen/go/agglayer/agglayer/protocolbuffers/go/agglayer/node/types/v1"
	v1 "github.com/agglayer/aggkit/aggsender/validator/proto/v1"
	"github.com/agglayer/aggkit/grpc"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestValidatorService(t *testing.T) {
	t.Skip("Skipping test for ValidatorService, this is only for debugging purposes")

	cfg := grpc.ServerConfig{
		Host:             "localhost",
		Port:             9090,
		EnableReflection: true,
	}

	// Create the server
	server, err := grpc.NewServer(cfg)
	require.NoError(t, err, "Failed to create gRPC server")

	// Register the Validator service
	v1.RegisterAggsenderValidatorServer(server.GRPC(), &ValidatorService{})

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

func TestValidatorService_ValidateCertificate(t *testing.T) {
	svc := &ValidatorService{}
	req := &v1.ValidateCertificateRequest{
		Certificate: &nodev1.Certificate{
			Height: 42,
		},
	}

	resp, err := svc.ValidateCertificate(context.Background(), req)
	require.NoError(t, err)
	require.IsType(t, &emptypb.Empty{}, resp)
}
