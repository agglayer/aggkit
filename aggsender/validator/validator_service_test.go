package validator

import (
	"context"
	"errors"
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
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var (
	testL1InfoTreeLeafCount = uint32(123)
	testCertificate1        = nodev1.Certificate{
		Height:              42,
		NewLocalExitRoot:    &typesv1.FixedBytes32{},
		PrevLocalExitRoot:   &typesv1.FixedBytes32{},
		Metadata:            &typesv1.FixedBytes32{},
		L1InfoTreeLeafCount: &testL1InfoTreeLeafCount,
	}
	errTestGenericError = errors.New("generic error")
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
	t.Run("nil request", func(t *testing.T) {
		testData := newValidatorServiceTestData(t)
		_, err := testData.sut.ValidateCertificate(t.Context(), nil)
		require.Error(t, err)
	})

	t.Run("ok", func(t *testing.T) {
		testData := newValidatorServiceTestData(t)
		req := &v1.ValidateCertificateRequest{
			Certificate: &testCertificate1,
		}
		testData.mockValidator.EXPECT().ValidateCertificate(mock.Anything, mock.Anything).Return(nil).Once()
		resp, err := testData.sut.ValidateCertificate(t.Context(), req)
		require.NoError(t, err)
		require.IsType(t, &v1.ValidateCertificateResponse{}, resp)
	})

	t.Run("PreviousCertificateId, fail to retrieve it", func(t *testing.T) {
		testData := newValidatorServiceTestData(t)
		req := &v1.ValidateCertificateRequest{
			Certificate: &testCertificate1,
			PreviousCertificateId: &nodev1.CertificateId{
				Value: &typesv1.FixedBytes32{Value: common.HexToHash("0xbeef").Bytes()},
			},
		}
		testData.mockAgglayerClient.EXPECT().GetCertificateHeader(mock.Anything, common.HexToHash("0xbeef")).Return(nil, errTestGenericError)
		_, err := testData.sut.ValidateCertificate(t.Context(), req)
		require.ErrorContains(t, err, "fail to request certificate header to agglayer: generic error")
	})
	t.Run("PreviousCertificateId, retrieved but is nil", func(t *testing.T) {
		testData := newValidatorServiceTestData(t)
		req := &v1.ValidateCertificateRequest{
			Certificate: &testCertificate1,
			PreviousCertificateId: &nodev1.CertificateId{
				Value: &typesv1.FixedBytes32{Value: common.HexToHash("0xbeef").Bytes()},
			},
		}
		testData.mockAgglayerClient.EXPECT().GetCertificateHeader(mock.Anything, common.HexToHash("0xbeef")).Return(nil, nil)
		_, err := testData.sut.ValidateCertificate(t.Context(), req)
		require.ErrorContains(t, err, "Certificate header is nil in agglayer")
	})

	t.Run("fails conversion certificate", func(t *testing.T) {
		testData := newValidatorServiceTestData(t)
		cert := testCertificate1
		cert.NewLocalExitRoot = nil
		req := &v1.ValidateCertificateRequest{
			Certificate: &cert,
		}
		_, err := testData.sut.ValidateCertificate(t.Context(), req)
		require.ErrorContains(t, err, "Error converting certificate")
	})
	t.Run("fails validate certificate", func(t *testing.T) {
		testData := newValidatorServiceTestData(t)
		req := &v1.ValidateCertificateRequest{
			Certificate: &testCertificate1,
		}
		testData.mockValidator.EXPECT().ValidateCertificate(mock.Anything, mock.Anything).Return(errTestGenericError).Once()
		_, err := testData.sut.ValidateCertificate(t.Context(), req)
		require.ErrorContains(t, err, "Certificate validation failed")
	})
}

type testValidatorServiceData struct {
	mockValidator      *mocks.CertificateValidator
	mockAgglayerClient *validatormocks.AgglayerClientInterface
	sut                *ValidatorService
}

func newValidatorServiceTestData(t *testing.T) *testValidatorServiceData {
	t.Helper()
	mockValidator := mocks.NewCertificateValidator(t)
	mockAgglayerClient := validatormocks.NewAgglayerClientInterface(t)
	sut := NewValidatorService(mockValidator, mockAgglayerClient)
	return &testValidatorServiceData{
		mockValidator:      mockValidator,
		mockAgglayerClient: mockAgglayerClient,
		sut:                sut,
	}
}
