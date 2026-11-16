package validator

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"os"
	"os/signal"
	"syscall"
	"testing"

	nodev1 "buf.build/gen/go/agglayer/agglayer/protocolbuffers/go/agglayer/node/types/v1"
	typesv1 "buf.build/gen/go/agglayer/interop/protocolbuffers/go/agglayer/interop/types/v1"
	agglayergrpc "github.com/agglayer/aggkit/agglayer/grpc"
	"github.com/agglayer/aggkit/aggsender/mocks"
	validatormocks "github.com/agglayer/aggkit/aggsender/validator/mocks"
	v1 "github.com/agglayer/aggkit/aggsender/validator/proto/v1"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/grpc"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var (
	signature = &typesv1.AggchainData_Signature{
		Signature: &typesv1.FixedBytes65{Value: []byte("test_signature")},
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
		req := testData.signedRequest(t, &v1.ValidateCertificateRequest{
			Certificate: newGRPCCertificateForTest(t),
		})
		testData.mockValidator.EXPECT().ValidateCertificate(mock.Anything, mock.Anything).Return(nil).Once()
		testData.mockSigner.EXPECT().SignHash(mock.Anything, mock.Anything).Return([]byte("signature"), nil).Once()
		resp, err := testData.sut.ValidateCertificate(t.Context(), req)
		require.NoError(t, err)
		require.IsType(t, &v1.ValidateCertificateResponse{}, resp)
		require.Equal(t, resp.Signature.Value, []byte("signature"), "Expected signature to match")
	})

	t.Run("PreviousCertificateId, fail to retrieve it", func(t *testing.T) {
		testData := newValidatorServiceTestData(t)
		req := testData.signedRequest(t, &v1.ValidateCertificateRequest{
			Certificate: newGRPCCertificateForTest(t),
			PreviousCertificateId: &nodev1.CertificateId{
				Value: &typesv1.FixedBytes32{Value: common.HexToHash("0xbeef").Bytes()},
			},
		})
		testData.mockAgglayerClient.EXPECT().GetCertificateHeader(mock.Anything, common.HexToHash("0xbeef")).Return(nil, errTestGenericError)
		_, err := testData.sut.ValidateCertificate(t.Context(), req)
		require.ErrorContains(t, err, "fails to request certificate header to agglayer")
	})
	t.Run("PreviousCertificateId, retrieved but is nil", func(t *testing.T) {
		testData := newValidatorServiceTestData(t)
		req := testData.signedRequest(t, &v1.ValidateCertificateRequest{
			Certificate: newGRPCCertificateForTest(t),
			PreviousCertificateId: &nodev1.CertificateId{
				Value: &typesv1.FixedBytes32{Value: common.HexToHash("0xbeef").Bytes()},
			},
		})
		testData.mockAgglayerClient.EXPECT().GetCertificateHeader(mock.Anything, common.HexToHash("0xbeef")).Return(nil, nil)
		_, err := testData.sut.ValidateCertificate(t.Context(), req)
		require.ErrorContains(t, err, "Certificate header is nil in agglayer")
	})

	t.Run("fails to convert certificate", func(t *testing.T) {
		testData := newValidatorServiceTestData(t)
		cert := newGRPCCertificateForTest(t)
		cert.NewLocalExitRoot = nil
		req := &v1.ValidateCertificateRequest{
			Certificate: cert,
		}
		_, err := testData.sut.ValidateCertificate(t.Context(), req)
		require.ErrorContains(t, err, "Invalid certificate conversion")
	})
	t.Run("converted certificate with bridgeExits", func(t *testing.T) {
		testData := newValidatorServiceTestData(t)
		cert := newGRPCCertificateForTest(t)
		cert.BridgeExits = []*typesv1.BridgeExit{
			{
				LeafType: 1,
				TokenInfo: &typesv1.TokenInfo{
					OriginTokenAddress: &typesv1.FixedBytes20{},
				},
				DestAddress: &typesv1.FixedBytes20{},
			},
		}
		testData.mockValidator.EXPECT().ValidateCertificate(mock.Anything, mock.Anything).Return(nil).Once()
		testData.mockSigner.EXPECT().SignHash(mock.Anything, mock.Anything).Return([]byte("signature"), nil).Once()

		req := testData.signedRequest(t, &v1.ValidateCertificateRequest{
			Certificate: cert,
		})
		_, err := testData.sut.ValidateCertificate(t.Context(), req)
		require.NoError(t, err)
	})
	t.Run("fails validate certificate", func(t *testing.T) {
		testData := newValidatorServiceTestData(t)
		req := testData.signedRequest(t, &v1.ValidateCertificateRequest{
			Certificate: newGRPCCertificateForTest(t),
		})
		testData.mockValidator.EXPECT().ValidateCertificate(mock.Anything, mock.Anything).Return(errTestGenericError).Once()
		_, err := testData.sut.ValidateCertificate(t.Context(), req)
		require.ErrorContains(t, err, "Certificate validation failed")
	})
	t.Run("rejects a request with no proposer signature", func(t *testing.T) {
		testData := newValidatorServiceTestData(t)
		req := &v1.ValidateCertificateRequest{
			Certificate: newGRPCCertificateForTest(t),
			PreviousCertificateId: &nodev1.CertificateId{
				Value: &typesv1.FixedBytes32{Value: common.HexToHash("0xbeef").Bytes()},
			},
		}
		_, err := testData.sut.ValidateCertificate(t.Context(), req)
		require.ErrorContains(t, err, "Code: Unauthenticated")
	})
	t.Run("rejects a request with a short proposer signature", func(t *testing.T) {
		testData := newValidatorServiceTestData(t)
		req := &v1.ValidateCertificateRequest{
			Certificate:       newGRPCCertificateForTest(t),
			ProposerSignature: &typesv1.FixedBytes65{Value: []byte("short")},
		}
		_, err := testData.sut.ValidateCertificate(t.Context(), req)
		require.ErrorContains(t, err, "Code: Unauthenticated")
	})
	t.Run("rejects a request signed by another key", func(t *testing.T) {
		testData := newValidatorServiceTestData(t)
		otherKey, err := crypto.GenerateKey()
		require.NoError(t, err)
		req := testData.signedRequestWithKey(t, &v1.ValidateCertificateRequest{
			Certificate: newGRPCCertificateForTest(t),
		}, otherKey)
		_, err = testData.sut.ValidateCertificate(t.Context(), req)
		require.ErrorContains(t, err, "Code: Unauthenticated")
	})
	t.Run("rejects when the proposer address cannot be resolved", func(t *testing.T) {
		testData := newValidatorServiceTestData(t)
		testData.proposer.err = errTestGenericError
		req := testData.signedRequest(t, &v1.ValidateCertificateRequest{
			Certificate: newGRPCCertificateForTest(t),
		})
		_, err := testData.sut.ValidateCertificate(t.Context(), req)
		require.ErrorContains(t, err, "Code: Unavailable")
	})
	t.Run("an invalid certificate fails conversion before authentication", func(t *testing.T) {
		testData := newValidatorServiceTestData(t)
		cert := newGRPCCertificateForTest(t)
		cert.NewLocalExitRoot = nil
		req := &v1.ValidateCertificateRequest{
			Certificate:       cert,
			ProposerSignature: &typesv1.FixedBytes65{Value: make([]byte, aggkitcommon.SignatureSize)},
		}
		_, err := testData.sut.ValidateCertificate(t.Context(), req)
		require.ErrorContains(t, err, "Invalid certificate conversion")
	})
}

type testValidatorServiceData struct {
	mockValidator      *mocks.CertificateValidator
	mockAgglayerClient *validatormocks.AgglayerClientInterface
	mockSigner         *mocks.Signer
	proposerKey        *ecdsa.PrivateKey
	proposer           *stubProposerAddressProvider
	sut                *ValidatorService
}

// stubProposerAddressProvider answers the proposer address the test configures.
type stubProposerAddressProvider struct {
	address common.Address
	err     error
}

func (s *stubProposerAddressProvider) GetProposerAddress(context.Context) (common.Address, error) {
	return s.address, s.err
}

func newValidatorServiceTestData(t *testing.T) *testValidatorServiceData {
	t.Helper()
	mockValidator := mocks.NewCertificateValidator(t)
	mockAgglayerClient := validatormocks.NewAgglayerClientInterface(t)
	mockSigner := mocks.NewSigner(t)

	proposerKey, err := crypto.GenerateKey()
	require.NoError(t, err)
	proposer := &stubProposerAddressProvider{address: crypto.PubkeyToAddress(proposerKey.PublicKey)}

	sut := NewValidatorService(
		log.WithFields("module", "aggsender-validator"),
		mockValidator, mockAgglayerClient, mockSigner, proposer)
	return &testValidatorServiceData{
		mockValidator:      mockValidator,
		mockAgglayerClient: mockAgglayerClient,
		mockSigner:         mockSigner,
		proposerKey:        proposerKey,
		proposer:           proposer,
		sut:                sut,
	}
}

// signedRequest adds the signature of the test proposer to the request.
func (d *testValidatorServiceData) signedRequest(
	t *testing.T, req *v1.ValidateCertificateRequest) *v1.ValidateCertificateRequest {
	t.Helper()
	return d.signedRequestWithKey(t, req, d.proposerKey)
}

func (d *testValidatorServiceData) signedRequestWithKey(
	t *testing.T, req *v1.ValidateCertificateRequest, key *ecdsa.PrivateKey) *v1.ValidateCertificateRequest {
	t.Helper()
	cert, err := agglayergrpc.ConvertProtoCertToAgglayer(req.Certificate)
	require.NoError(t, err)
	hashToSign, err := HashCertificateToSign(cert)
	require.NoError(t, err)
	signature, err := crypto.Sign(hashToSign.Bytes(), key)
	require.NoError(t, err)
	req.ProposerSignature = &typesv1.FixedBytes65{Value: signature}

	return req
}

func newGRPCCertificateForTest(t *testing.T) *nodev1.Certificate {
	t.Helper()
	testL1InfoTreeLeafCount := uint32(123)
	return &nodev1.Certificate{
		Height:              42,
		NewLocalExitRoot:    &typesv1.FixedBytes32{},
		PrevLocalExitRoot:   &typesv1.FixedBytes32{},
		Metadata:            &typesv1.FixedBytes32{},
		L1InfoTreeLeafCount: &testL1InfoTreeLeafCount,
		AggchainData:        &typesv1.AggchainData{Data: signature},
	}
}
