package validator

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

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
	testValidatorAddr   = common.HexToAddress("0x00000000000000000000000000000000000000aa")
	testNow             = time.Unix(1_700_000_000, 0)
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
	t.Run("rejects the proposer's committee signature of the certificate hash", func(t *testing.T) {
		testData := newValidatorServiceTestData(t)
		req := &v1.ValidateCertificateRequest{
			Certificate:      newGRPCCertificateForTest(t),
			RequestTimestamp: uint64(testNow.Unix()),
		}
		cert, err := agglayergrpc.ConvertProtoCertToAgglayer(req.Certificate)
		require.NoError(t, err)
		certificateHash, err := HashCertificateToSign(cert)
		require.NoError(t, err)
		committeeSignature, err := crypto.Sign(certificateHash.Bytes(), testData.proposerKey)
		require.NoError(t, err)
		req.ProposerSignature = &typesv1.FixedBytes65{Value: committeeSignature}
		_, err = testData.sut.ValidateCertificate(t.Context(), req)
		require.ErrorContains(t, err, "Code: Unauthenticated")
	})
	t.Run("rejects a request addressed to another validator", func(t *testing.T) {
		testData := newValidatorServiceTestData(t)
		req := signRequest(t, &v1.ValidateCertificateRequest{
			Certificate: newGRPCCertificateForTest(t),
		}, testData.proposerKey, common.HexToAddress("0xbb"))
		_, err := testData.sut.ValidateCertificate(t.Context(), req)
		require.ErrorContains(t, err, "Code: Unauthenticated")
	})
	t.Run("rejects a request whose fields changed after signing", func(t *testing.T) {
		testData := newValidatorServiceTestData(t)
		req := testData.signedRequest(t, &v1.ValidateCertificateRequest{
			Certificate:       newGRPCCertificateForTest(t),
			LastL2BlockInCert: 10,
		})
		req.LastL2BlockInCert = 11
		_, err := testData.sut.ValidateCertificate(t.Context(), req)
		require.ErrorContains(t, err, "Code: Unauthenticated")
	})
	t.Run("rejects a request with no timestamp", func(t *testing.T) {
		testData := newValidatorServiceTestData(t)
		req := testData.signedRequest(t, &v1.ValidateCertificateRequest{
			Certificate: newGRPCCertificateForTest(t),
		})
		req.RequestTimestamp = 0
		_, err := testData.sut.ValidateCertificate(t.Context(), req)
		require.ErrorContains(t, err, "Code: Unauthenticated")
	})
	t.Run("rejects a stale request", func(t *testing.T) {
		testData := newValidatorServiceTestData(t)
		req := testData.signedRequest(t, &v1.ValidateCertificateRequest{
			Certificate:      newGRPCCertificateForTest(t),
			RequestTimestamp: uint64(testNow.Add(-2 * DefaultProposerRequestMaxClockSkew).Unix()),
		})
		_, err := testData.sut.ValidateCertificate(t.Context(), req)
		require.ErrorContains(t, err, "outside the allowed window")
	})
	t.Run("rejects a request from the future", func(t *testing.T) {
		testData := newValidatorServiceTestData(t)
		req := testData.signedRequest(t, &v1.ValidateCertificateRequest{
			Certificate:      newGRPCCertificateForTest(t),
			RequestTimestamp: uint64(testNow.Add(2 * DefaultProposerRequestMaxClockSkew).Unix()),
		})
		_, err := testData.sut.ValidateCertificate(t.Context(), req)
		require.ErrorContains(t, err, "outside the allowed window")
	})
	t.Run("accepts a request within the clock skew", func(t *testing.T) {
		testData := newValidatorServiceTestData(t)
		req := testData.signedRequest(t, &v1.ValidateCertificateRequest{
			Certificate:      newGRPCCertificateForTest(t),
			RequestTimestamp: uint64(testNow.Add(-DefaultProposerRequestMaxClockSkew / 2).Unix()),
		})
		testData.mockValidator.EXPECT().ValidateCertificate(mock.Anything, mock.Anything).Return(nil).Once()
		testData.mockSigner.EXPECT().SignHash(mock.Anything, mock.Anything).Return([]byte("signature"), nil).Once()
		_, err := testData.sut.ValidateCertificate(t.Context(), req)
		require.NoError(t, err)
	})
	t.Run("accepts a signature with a legacy recovery id", func(t *testing.T) {
		testData := newValidatorServiceTestData(t)
		req := testData.signedRequest(t, &v1.ValidateCertificateRequest{
			Certificate: newGRPCCertificateForTest(t),
		})
		req.ProposerSignature.Value[crypto.RecoveryIDOffset] += 27
		testData.mockValidator.EXPECT().ValidateCertificate(mock.Anything, mock.Anything).Return(nil).Once()
		testData.mockSigner.EXPECT().SignHash(mock.Anything, mock.Anything).Return([]byte("signature"), nil).Once()
		_, err := testData.sut.ValidateCertificate(t.Context(), req)
		require.NoError(t, err)
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

	mockSigner.EXPECT().PublicAddress().Return(testValidatorAddr).Maybe()

	sut := NewValidatorService(
		log.WithFields("module", "aggsender-validator"),
		mockValidator, mockAgglayerClient, mockSigner, proposer, 0)
	sut.now = func() time.Time { return testNow }
	return &testValidatorServiceData{
		mockValidator:      mockValidator,
		mockAgglayerClient: mockAgglayerClient,
		mockSigner:         mockSigner,
		proposerKey:        proposerKey,
		proposer:           proposer,
		sut:                sut,
	}
}

// signedRequest adds the signature of the test proposer to the request, addressed to the
// test validator and timestamped now unless the request already carries a timestamp.
func (d *testValidatorServiceData) signedRequest(
	t *testing.T, req *v1.ValidateCertificateRequest) *v1.ValidateCertificateRequest {
	t.Helper()
	return d.signedRequestWithKey(t, req, d.proposerKey)
}

func (d *testValidatorServiceData) signedRequestWithKey(
	t *testing.T, req *v1.ValidateCertificateRequest, key *ecdsa.PrivateKey) *v1.ValidateCertificateRequest {
	t.Helper()
	return signRequest(t, req, key, testValidatorAddr)
}

func signRequest(t *testing.T, req *v1.ValidateCertificateRequest, key *ecdsa.PrivateKey,
	validatorAddr common.Address) *v1.ValidateCertificateRequest {
	t.Helper()
	if req.RequestTimestamp == 0 {
		req.RequestTimestamp = uint64(testNow.Unix())
	}
	cert, err := agglayergrpc.ConvertProtoCertToAgglayer(req.Certificate)
	require.NoError(t, err)
	certificateHash, err := HashCertificateToSign(cert)
	require.NoError(t, err)
	var previousCertificateID *common.Hash
	if req.PreviousCertificateId != nil {
		id := common.BytesToHash(req.PreviousCertificateId.Value.Value)
		previousCertificateID = &id
	}
	digest := ProposerRequestDigest(certificateHash, previousCertificateID, req.LastL2BlockInCert,
		validatorAddr, req.RequestTimestamp)
	signature, err := crypto.Sign(digest.Bytes(), key)
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
