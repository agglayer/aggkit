package aggsender

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"testing"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/grpc"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestIsValidSignature(t *testing.T) {
	vp := &validatorPoller{}

	s := aggkitcommon.SignatureSize
	less := s - 1
	greater := s + 1

	tests := []struct {
		name string
		sig  []byte
		want bool
	}{
		{"nil signature", nil, s == 0},
		{"empty signature", []byte{}, s == 0},
		{"exact size", make([]byte, s), true},
		{"smaller than size", make([]byte, less), false},
		{"larger than size", make([]byte, greater), false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := vp.isValidSignature(tt.sig)
			if got != tt.want {
				t.Fatalf("isValidSignature(%d) = %v, want %v", len(tt.sig), got, tt.want)
			}
		})
	}
}

func TestIsThresholdReached(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		multisig    *agglayertypes.Multisig
		cert        *agglayertypes.Certificate
		threshold   *big.Int
		errs        []error
		expectedErr string
	}{
		{
			name: "threshold reached - only proposer",
			multisig: &agglayertypes.Multisig{
				Signatures: []agglayertypes.ECDSAMultisigEntry{
					{Index: 0, Signature: make([]byte, aggkitcommon.SignatureSize)},
				},
			},
			cert:      &agglayertypes.Certificate{},
			threshold: big.NewInt(1),
		},
		{
			name: "threshold reached - multiple signers",
			multisig: &agglayertypes.Multisig{
				Signatures: []agglayertypes.ECDSAMultisigEntry{
					{Index: 0, Signature: make([]byte, aggkitcommon.SignatureSize)},
					{Index: 1, Signature: make([]byte, aggkitcommon.SignatureSize)},
					{Index: 2, Signature: make([]byte, aggkitcommon.SignatureSize)},
				},
			},
			cert:      &agglayertypes.Certificate{},
			threshold: big.NewInt(2),
		},
		{
			name: "threshold not reached",
			multisig: &agglayertypes.Multisig{
				Signatures: []agglayertypes.ECDSAMultisigEntry{
					{Index: 0, Signature: make([]byte, aggkitcommon.SignatureSize)},
				},
			},
			cert:        &agglayertypes.Certificate{},
			threshold:   big.NewInt(2),
			expectedErr: "threshold not reached",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			vp := &validatorPoller{log: log.WithFields("test", tc.name)}

			result, err := vp.isThresholdReached(tc.multisig, tc.cert, tc.threshold.Uint64(), tc.errs)
			if tc.expectedErr == "" {
				require.NoError(t, err)
				require.Equal(t, tc.multisig, result)
			} else {
				require.ErrorContains(t, err, tc.expectedErr)
			}
		})
	}
}

func TestSignCertificateForMultisigAsProposer(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		mockFn      func(*mocks.Signer)
		cert        *agglayertypes.Certificate
		expectedSig []byte
		expectedErr string
	}{
		{
			name: "successful signing",
			mockFn: func(mockSigner *mocks.Signer) {
				mockSigner.EXPECT().SignHash(t.Context(), mock.Anything).Return([]byte("sig"), nil).Once()
			},
			cert:        &agglayertypes.Certificate{},
			expectedSig: []byte("sig"),
		},
		{
			name: "signing error",
			mockFn: func(mockSigner *mocks.Signer) {
				mockSigner.EXPECT().SignHash(t.Context(), mock.Anything).Return(nil, errors.New("test error")).Once()
			},
			cert:        &agglayertypes.Certificate{},
			expectedErr: "test error",
		},
		{
			name:        "invalid certificate",
			cert:        nil,
			expectedErr: "failed to hash certificate for proposer signing: certificate is nil",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockSigner := mocks.NewSigner(t)
			if tc.mockFn != nil {
				tc.mockFn(mockSigner)
			}

			vp := &validatorPoller{
				log:            log.WithFields("test", tc.name),
				proposerSigner: mockSigner,
			}

			sig, err := vp.signCertificateForMultisigAsProposer(t.Context(), tc.cert)
			if tc.expectedErr != "" {
				require.ErrorContains(t, err, tc.expectedErr)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedSig, sig)
			}

			mockSigner.AssertExpectations(t)
		})
	}
}

func TestGetSignatureFromValidator(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		lastL2      uint64
		setupMock   func(*mocks.Signer, *mocks.CertificateValidateAndSigner)
		expectedSig []byte
		expectedErr string
	}{
		{
			name:   "self-signing (proposer signs)",
			lastL2: 0,
			setupMock: func(mockProposerSigner *mocks.Signer, mockValidator *mocks.CertificateValidateAndSigner) {
				// proposer address that matches validator address
				mockProposerSigner.EXPECT().PublicAddress().Return(common.HexToAddress("0xdeadbeef")).Once()
				mockValidator.EXPECT().Address().Return(common.HexToAddress("0xdeadbeef")).Once()

				// expect proposer to sign the certificate
				mockProposerSigner.EXPECT().SignHash(t.Context(), mock.Anything).Return([]byte("sig"), nil).Once()
			},
			expectedSig: []byte("sig"),
		},
		{
			name:   "remote validation (validator signs remotely)",
			lastL2: 123,
			setupMock: func(mockProposerSigner *mocks.Signer, mockValidator *mocks.CertificateValidateAndSigner) {
				// proposer address different from validator address
				mockProposerSigner.EXPECT().PublicAddress().Return(common.HexToAddress("0xdeadbeef")).Once()
				mockValidator.EXPECT().Address().Return(common.HexToAddress("0x12341213132")).Once()

				// expect remote validator to be called
				mockValidator.EXPECT().
					ValidateAndSignCertificate(t.Context(), mock.Anything, uint64(123)).
					Return([]byte("remote-sig"), nil).
					Once()
			},
			expectedSig: []byte("remote-sig"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockSigner := mocks.NewSigner(t)
			mockValidator := mocks.NewCertificateValidateAndSigner(t)

			if tc.setupMock != nil {
				tc.setupMock(mockSigner, mockValidator)
			}

			cert := &agglayertypes.Certificate{}

			vp := &validatorPoller{
				log:            log.WithFields("test", tc.name),
				proposerSigner: mockSigner,
			}

			sig, err := vp.getSignatureFromValidator(t.Context(), mockValidator, cert, tc.lastL2)
			if tc.expectedErr != "" {
				require.ErrorContains(t, err, tc.expectedErr)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedSig, sig)
			}

			mockSigner.AssertExpectations(t)
			mockValidator.AssertExpectations(t)
		})
	}
}

func TestValidateRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		req         *types.ValidationRequest
		expectedErr string
	}{
		{
			name:        "nil request",
			req:         nil,
			expectedErr: "validation request cannot be nil",
		},
		{
			name:        "nil certificate",
			req:         &types.ValidationRequest{Certificate: nil},
			expectedErr: "certificate cannot be nil",
		},
		{
			name:        "zero last L2 block in certificate",
			req:         &types.ValidationRequest{Certificate: &agglayertypes.Certificate{}, LastL2BlockInCert: 0},
			expectedErr: "last L2 block in certificate cannot be zero",
		},
		{
			name:        "valid request",
			req:         &types.ValidationRequest{Certificate: &agglayertypes.Certificate{}, LastL2BlockInCert: 10},
			expectedErr: "",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			vp := &validatorPoller{}
			err := vp.validateRequest(tc.req)
			if tc.expectedErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.expectedErr)
			}
		})
	}
}

func TestPollValidators(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		setupMocks      func(*mocks.MultisigQuerier)
		req             *types.ValidationRequest
		expectedMinSigs int
		expectedErr     string
	}{
		{
			name:        "invalid request",
			req:         nil,
			expectedErr: "validation request cannot be nil",
		},
		{
			name: "no validators configured",
			req:  &types.ValidationRequest{Certificate: &agglayertypes.Certificate{}, LastL2BlockInCert: 10},
			setupMocks: func(m *mocks.MultisigQuerier) {
				m.EXPECT().
					GetMultisigCommittee(mock.Anything, mock.Anything).
					Return(&types.MultisigCommittee{}, nil).
					Once()
			},
			expectedMinSigs: 0,
			expectedErr:     "no validators available in the committee",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockMultisigQuerier := mocks.NewMultisigQuerier(t)

			if tc.setupMocks != nil {
				tc.setupMocks(mockMultisigQuerier)
			}

			poller := NewValidatorPoller(
				log.WithFields("test", tc.name),
				nil,
				nil,
				mockMultisigQuerier,
				&grpc.ClientConfig{},
			)

			result, err := poller.PollValidators(t.Context(), tc.req)

			if tc.expectedErr != "" {
				require.ErrorContains(t, err, tc.expectedErr)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				require.GreaterOrEqual(t, len(result.Signatures), tc.expectedMinSigs)
			}

			mockMultisigQuerier.AssertExpectations(t)
		})
	}
}

func TestGetValidators(t *testing.T) {
	t.Parallel()

	// Sample addresses for testing
	proposerAddr := common.HexToAddress("0x1")
	validator2Addr := common.HexToAddress("0x2")
	validator3Addr := common.HexToAddress("0x3")

	testCases := []struct {
		name              string
		setupMocks        func(*mocks.MultisigQuerier, *mocks.Signer)
		expectedCount     int
		expectedThreshold uint64
		expectedErr       string
	}{
		{
			name: "successful retrieval with single validator (proposer only)",
			setupMocks: func(mockQuerier *mocks.MultisigQuerier, mockSigner *mocks.Signer) {
				signers := []types.SignerInfo{
					{Address: proposerAddr, URL: "http://validator1:8001"},
				}
				committee, err := types.NewMultisigCommittee([]*types.SignerInfo{&signers[0]}, 1)
				require.NoError(t, err)

				mockQuerier.EXPECT().
					GetMultisigCommittee(mock.Anything, mock.Anything).
					Return(committee, nil).
					Once()

				// Mock proposer address check
				mockSigner.EXPECT().PublicAddress().Return(proposerAddr).Once()
			},
			expectedCount:     1,
			expectedThreshold: 1,
		},
		{
			name: "successful retrieval with multiple validators",
			setupMocks: func(mockQuerier *mocks.MultisigQuerier, mockSigner *mocks.Signer) {
				// Create committee with multiple validators (proposer first)
				signers := []types.SignerInfo{
					{Address: proposerAddr, URL: "http://validator1:8001"},
					{Address: validator2Addr, URL: "http://validator2:8002"},
					{Address: validator3Addr, URL: "http://validator3:8003"},
				}
				signersPtr := []*types.SignerInfo{&signers[0], &signers[1], &signers[2]}
				committee, err := types.NewMultisigCommittee(signersPtr, 2)
				require.NoError(t, err)

				mockQuerier.EXPECT().
					GetMultisigCommittee(mock.Anything, mock.Anything).
					Return(committee, nil).
					Once()

				mockSigner.EXPECT().PublicAddress().Return(proposerAddr).Once()
			},
			expectedCount:     3,
			expectedThreshold: 2,
		},
		{
			name: "multisig querier fails",
			setupMocks: func(mockQuerier *mocks.MultisigQuerier, mockSigner *mocks.Signer) {
				mockQuerier.EXPECT().
					GetMultisigCommittee(mock.Anything, mock.Anything).
					Return(nil, errors.New("blockchain connection error")).
					Once()
			},
			expectedErr: "failed to retrieve the latest multisig committee: blockchain connection error",
		},
		{
			name: "empty committee",
			setupMocks: func(mockQuerier *mocks.MultisigQuerier, mockSigner *mocks.Signer) {
				mockQuerier.EXPECT().
					GetMultisigCommittee(mock.Anything, mock.Anything).
					Return(&types.MultisigCommittee{}, nil).
					Once()
			},
			expectedErr: "no validators available in the committee",
		},
		{
			name: "proposer not first in committee",
			setupMocks: func(mockQuerier *mocks.MultisigQuerier, mockSigner *mocks.Signer) {
				// Create committee where proposer is NOT the first validator
				signers := []types.SignerInfo{
					{Address: validator2Addr, URL: "http://validator2:8002"}, // Different validator first
					{Address: proposerAddr, URL: "http://validator1:8001"},   // Proposer second
				}
				signersPtr := []*types.SignerInfo{&signers[0], &signers[1]}
				committee, err := types.NewMultisigCommittee(signersPtr, 1)
				require.NoError(t, err)

				mockQuerier.EXPECT().
					GetMultisigCommittee(mock.Anything, mock.Anything).
					Return(committee, nil).
					Once()

				mockSigner.EXPECT().PublicAddress().Return(proposerAddr).Once()
			},
			expectedErr: "expected proposer 0x0000000000000000000000000000000000000001 to be the first member of the validator committee, got 0x0000000000000000000000000000000000000002",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockQuerier := mocks.NewMultisigQuerier(t)
			mockSigner := mocks.NewSigner(t)

			if tc.setupMocks != nil {
				tc.setupMocks(mockQuerier, mockSigner)
			}

			clientCfg := &grpc.ClientConfig{
				URL: "http://base-url:8000",
			}

			vp := NewValidatorPoller(
				log.WithFields("test", tc.name),
				nil, // storage
				mockSigner,
				mockQuerier,
				clientCfg,
			)

			validators, threshold, err := vp.getValidators(context.Background())

			if tc.expectedErr != "" {
				require.ErrorContains(t, err, tc.expectedErr)
				require.Nil(t, validators)
			} else {
				require.NoError(t, err)
				require.NotNil(t, validators)
				require.Len(t, validators, tc.expectedCount)
				require.Equal(t, tc.expectedThreshold, threshold)
			}

			mockQuerier.AssertExpectations(t)
			mockSigner.AssertExpectations(t)
		})
	}
}

func TestExecuteRequest(t *testing.T) {
	t.Parallel()

	certificate := &agglayertypes.Certificate{
		NetworkID: 1,
		Height:    1,
	}

	tests := []struct {
		name               string
		setupMocks         func(*mocks.Signer) ([]types.CertificateValidateAndSigner, *big.Int)
		expectedMinSigs    int
		expectErrSubstring string
	}{
		{
			name: "single healthy validator returns valid signature",
			setupMocks: func(mockSigner *mocks.Signer) ([]types.CertificateValidateAndSigner, *big.Int) {
				mockSigner.EXPECT().PublicAddress().Return(common.HexToAddress("0xdeadbeef")).Once()

				mockValidator := mocks.NewCertificateValidateAndSigner(t)
				mockValidator.EXPECT().String().Return("validator-1").Maybe()
				mockValidator.EXPECT().Address().Return(common.HexToAddress("0x2")).Once()
				mockValidator.EXPECT().Index().Return(uint32(1))
				mockValidator.EXPECT().
					ValidateAndSignCertificate(mock.Anything, mock.Anything, mock.Anything).
					Return(make([]byte, aggkitcommon.SignatureSize), nil).
					Once()
				return []types.CertificateValidateAndSigner{mockValidator}, big.NewInt(1)
			},
			expectedMinSigs: 1,
		},
		{
			name: "multiple validators reach threshold",
			setupMocks: func(mockSigner *mocks.Signer) ([]types.CertificateValidateAndSigner, *big.Int) {
				mockSigner.EXPECT().PublicAddress().Return(common.HexToAddress("0xdeadbeef"))

				v1 := mocks.NewCertificateValidateAndSigner(t)
				v2 := mocks.NewCertificateValidateAndSigner(t)
				v3 := mocks.NewCertificateValidateAndSigner(t)

				for i, v := range [](*mocks.CertificateValidateAndSigner){v1, v2, v3} {
					v.EXPECT().String().Return(fmt.Sprintf("validator-%d", i)).Maybe()
					v.EXPECT().Index().Return(uint32(i)).Maybe()
					v.EXPECT().Address().Return(common.HexToAddress(fmt.Sprintf("0x%d", i+1))).Maybe()
					v.EXPECT().
						ValidateAndSignCertificate(mock.Anything, mock.Anything, mock.Anything).
						Return(make([]byte, aggkitcommon.SignatureSize), nil).Maybe()
				}

				validators := []types.CertificateValidateAndSigner{v1, v2, v3}
				return validators, big.NewInt(2)
			},
			expectedMinSigs: 2,
		},
		{
			name: "threshold not reached",
			setupMocks: func(mockSigner *mocks.Signer) ([]types.CertificateValidateAndSigner, *big.Int) {
				mockSigner.EXPECT().PublicAddress().Return(common.HexToAddress("0xdeadbeef"))

				v1 := mocks.NewCertificateValidateAndSigner(t)
				v2 := mocks.NewCertificateValidateAndSigner(t)
				v3 := mocks.NewCertificateValidateAndSigner(t)

				for i, v := range [](*mocks.CertificateValidateAndSigner){v1, v2, v3} {
					v.EXPECT().String().Return(fmt.Sprintf("validator-%d", i))
					v.EXPECT().Address().Return(common.HexToAddress(fmt.Sprintf("0x%d", i+1)))
					v.EXPECT().
						ValidateAndSignCertificate(mock.Anything, mock.Anything, mock.Anything).
						Return(nil, errors.New("validation failed")).
						Times(1)
				}

				validators := []types.CertificateValidateAndSigner{v1, v2, v3}
				return validators, big.NewInt(2)
			},
			expectErrSubstring: "threshold not reached",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockProposerSigner := mocks.NewSigner(t)

			var (
				validators []types.CertificateValidateAndSigner
				threshold  *big.Int
			)

			if tc.setupMocks != nil {
				validators, threshold = tc.setupMocks(mockProposerSigner)
			}

			poller := NewValidatorPoller(
				log.WithFields("test", tc.name),
				nil,
				mockProposerSigner,
				nil,
				&grpc.ClientConfig{},
			)

			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()

			result, err := poller.executeRequest(ctx, &types.ValidationRequest{
				Certificate:       certificate,
				LastL2BlockInCert: 10,
			}, threshold.Uint64(), validators)

			if tc.expectErrSubstring != "" {
				require.ErrorContains(t, err, tc.expectErrSubstring)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				require.GreaterOrEqual(t, len(result.Signatures), tc.expectedMinSigs)
			}

			mockProposerSigner.AssertExpectations(t)
		})
	}
}
