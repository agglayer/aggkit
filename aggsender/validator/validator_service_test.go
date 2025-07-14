package validator

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"testing"

	nodev1 "buf.build/gen/go/agglayer/agglayer/protocolbuffers/go/agglayer/node/types/v1"
	typesv1 "buf.build/gen/go/agglayer/interop/protocolbuffers/go/agglayer/interop/types/v1"
	"github.com/agglayer/aggkit/aggsender/mocks"
	validatormocks "github.com/agglayer/aggkit/aggsender/validator/mocks"
	v1 "github.com/agglayer/aggkit/aggsender/validator/proto/v1"
	"github.com/agglayer/aggkit/grpc"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
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
	mockValidator := mocks.NewCertificateValidator(t)
	mockAgglayerClient := validatormocks.NewAgglayerClientInterface(t)
	svc := NewValidatorService(mockValidator, mockAgglayerClient)
	l1InfoTreeLeafCount := uint32(123)
	req := &v1.ValidateCertificateRequest{
		Certificate: &nodev1.Certificate{
			Height:              42,
			NewLocalExitRoot:    &typesv1.FixedBytes32{},
			PrevLocalExitRoot:   &typesv1.FixedBytes32{},
			Metadata:            &typesv1.FixedBytes32{},
			L1InfoTreeLeafCount: &l1InfoTreeLeafCount,
		},
	}
	mockValidator.EXPECT().ValidateCertificate(mock.Anything, mock.Anything).Return(nil).Once()
	resp, err := svc.ValidateCertificate(t.Context(), req)
	require.NoError(t, err)
	require.IsType(t, &v1.ValidateCertificateResponse{}, resp)
}
