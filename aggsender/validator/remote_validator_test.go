package validator

import (
	"errors"
	"testing"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const VALIDATOR_SK = "4c0883a69102937d6231471b5dbb6204fe5129617082799a9f2201a2ad8d9c32"
const VALIDATOR_ADDR = "0x9D36c7795cC5F041010F1eb74f3e70306Ab9Ede5"

func signCertificate(t *testing.T, certificate *agglayertypes.Certificate) []byte {
	t.Helper()
	certificateHash, err := HashCertificateToSign(certificate)
	require.NoError(t, err)
	privKey, err := crypto.HexToECDSA(VALIDATOR_SK)
	require.NoError(t, err)
	signature, err := crypto.Sign(certificateHash.Bytes(), privKey)
	require.NoError(t, err)
	return signature
}

func TestRemoteClient_ValidateCertificate(t *testing.T) {
	t.Parallel()

	certAtHeight0 := agglayertypes.Certificate{
		Height: 0,
	}
	certAtHeight0Sig := signCertificate(t, &certAtHeight0)
	certAtHeight11 := agglayertypes.Certificate{
		Height: 11,
	}
	certAtHeight11Sig := signCertificate(t, &certAtHeight11)

	ctx := t.Context()
	testCases := []struct {
		name          string
		certificate   *agglayertypes.Certificate
		mockFn        func(mockClient *mocks.ValidatorClient, mockStorage *mocks.AggSenderStorage, mockSigner *mocks.Signer)
		expectedSig   []byte
		expectedError string
	}{
		{
			name:        "storage returns an error",
			certificate: &certAtHeight11,
			mockFn: func(mockClient *mocks.ValidatorClient, mockStorage *mocks.AggSenderStorage, mockSigner *mocks.Signer) {
				mockStorage.EXPECT().GetCertificateHeaderByHeight(uint64(10)).Return(nil, errors.New("storage error"))
			},
			expectedError: "error getting previous certificate header by height 10: storage error",
		},
		{
			name:        "client returns an error",
			certificate: &certAtHeight0,
			mockFn: func(mockClient *mocks.ValidatorClient, mockStorage *mocks.AggSenderStorage, mockSigner *mocks.Signer) {
				var previousCertificateID *common.Hash
				mockClient.EXPECT().ValidateCertificate(
					ctx,
					previousCertificateID,
					&certAtHeight0,
					mock.Anything,
					mock.Anything,
					mock.Anything,
				).Return(nil, errors.New("client error"))
			},
			expectedError: "error validating certificate on aggsender validator service: client error",
		},
		{
			name:        "success, no previous certificate",
			certificate: &certAtHeight0,
			mockFn: func(mockClient *mocks.ValidatorClient, mockStorage *mocks.AggSenderStorage, mockSigner *mocks.Signer) {
				var previousCertificateID *common.Hash
				mockClient.EXPECT().ValidateCertificate(
					ctx,
					previousCertificateID,
					&certAtHeight0,
					mock.Anything,
					mock.Anything,
					mock.Anything,
				).Return(certAtHeight0Sig, nil)
			},
			expectedSig: certAtHeight0Sig,
		},
		{
			name:        "fail, invalid signature",
			certificate: &certAtHeight0,
			mockFn: func(mockClient *mocks.ValidatorClient, mockStorage *mocks.AggSenderStorage, mockSigner *mocks.Signer) {
				var previousCertificateID *common.Hash
				mockClient.EXPECT().ValidateCertificate(
					ctx,
					previousCertificateID,
					&certAtHeight0,
					mock.Anything,
					mock.Anything,
					mock.Anything,
				).Return(certAtHeight11Sig, nil)
			},
			expectedError: "error validating remote validator signature, mismatch expected:0x9D36c7795cC5F041010F1eb74f3e70306Ab9Ede5 current:0x38996100B11d9637C61c2d903A8dE79F26B01A9a",
		},
		{
			name:        "fail, empty signature",
			certificate: &certAtHeight0,
			mockFn: func(mockClient *mocks.ValidatorClient, mockStorage *mocks.AggSenderStorage, mockSigner *mocks.Signer) {
				var previousCertificateID *common.Hash
				mockClient.EXPECT().ValidateCertificate(
					ctx,
					previousCertificateID,
					&certAtHeight0,
					mock.Anything,
					mock.Anything,
					mock.Anything,
				).Return(make([]byte, crypto.SignatureLength), nil)
			},
			expectedError: "error validating remote validator signature: recovery failed",
		},
		{
			name:        "success, with previous certificate",
			certificate: &certAtHeight11,
			mockFn: func(mockClient *mocks.ValidatorClient, mockStorage *mocks.AggSenderStorage, mockSigner *mocks.Signer) {
				previousCertID := common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
				mockStorage.EXPECT().GetCertificateHeaderByHeight(uint64(10)).Return(
					&types.CertificateHeader{
						CertificateID: previousCertID,
					}, nil,
				)
				mockClient.EXPECT().ValidateCertificate(
					ctx,
					&previousCertID,
					&certAtHeight11,
					mock.Anything,
					mock.Anything,
					mock.Anything,
				).Return(certAtHeight11Sig, nil)
			},
			expectedSig: certAtHeight11Sig,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockClient := mocks.NewValidatorClient(t)
			mockStorage := mocks.NewAggSenderStorage(t)
			mockSigner := mocks.NewSigner(t)
			mockSigner.EXPECT().SignHash(mock.Anything, mock.Anything).Return(certAtHeight0Sig, nil).Maybe()

			if tc.mockFn != nil {
				tc.mockFn(mockClient, mockStorage, mockSigner)
			}

			remoteValidator := &RemoteValidator{
				client:  mockClient,
				storage: mockStorage,
				address: common.HexToAddress(VALIDATOR_ADDR),
				signer:  mockSigner,
				now:     time.Now,
			}

			signature, err := remoteValidator.ValidateAndSignCertificate(ctx, tc.certificate, 0)

			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, signature, tc.expectedSig)
			}

			mockClient.AssertExpectations(t)
			mockStorage.AssertExpectations(t)
		})
	}
}

func TestRemoteClient_ValidateCertificate_SignsTheRequestDigest(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cert := &agglayertypes.Certificate{Height: 11}
	certSig := signCertificate(t, cert)
	certHash, err := HashCertificateToSign(cert)
	require.NoError(t, err)

	previousCertID := common.HexToHash("0xabcdef")
	validatorAddr := common.HexToAddress(VALIDATOR_ADDR)
	now := time.Unix(1_700_000_000, 0)
	const lastL2Block = uint64(42)
	expectedDigest := ProposerRequestDigest(certHash, &previousCertID, lastL2Block, validatorAddr, uint64(now.Unix()))
	require.NotEqual(t, certHash, expectedDigest, "the proposer must not sign the committee hash")
	proposerSig := []byte("proposer-signature")

	mockClient := mocks.NewValidatorClient(t)
	mockStorage := mocks.NewAggSenderStorage(t)
	mockSigner := mocks.NewSigner(t)
	mockStorage.EXPECT().GetCertificateHeaderByHeight(uint64(10)).Return(
		&types.CertificateHeader{CertificateID: previousCertID}, nil)
	mockSigner.EXPECT().SignHash(mock.Anything, expectedDigest).Return(proposerSig, nil)
	mockClient.EXPECT().ValidateCertificate(ctx, &previousCertID, cert, lastL2Block, proposerSig,
		uint64(now.Unix())).Return(certSig, nil)

	remoteValidator := &RemoteValidator{
		client:  mockClient,
		storage: mockStorage,
		address: validatorAddr,
		signer:  mockSigner,
		now:     func() time.Time { return now },
	}

	signature, err := remoteValidator.ValidateAndSignCertificate(ctx, cert, lastL2Block)
	require.NoError(t, err)
	require.Equal(t, certSig, signature)
}
