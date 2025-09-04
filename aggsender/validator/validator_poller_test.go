package validator

import (
	"context"
	"errors"
	"fmt"
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
		threshold   uint32
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
			threshold: 1,
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
			threshold: 2,
		},
		{
			name: "threshold not reached",
			multisig: &agglayertypes.Multisig{
				Signatures: []agglayertypes.ECDSAMultisigEntry{
					{Index: 0, Signature: make([]byte, aggkitcommon.SignatureSize)},
				},
			},
			cert:        &agglayertypes.Certificate{},
			threshold:   2,
			expectedErr: "threshold not reached",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			vp := &validatorPoller{log: log.WithFields("test", tc.name)}

			result, err := vp.isThresholdReached(tc.multisig, tc.cert, tc.threshold, tc.errs)
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

func TestGetValidators(t *testing.T) {
	allSigners := []*types.SignerInfo{
		types.NewSignerInfo("http://localhost:8001", common.HexToAddress("0x1")),
		types.NewSignerInfo("http://localhost:8002", common.HexToAddress("0x2")),
		types.NewSignerInfo("http://localhost:8003", common.HexToAddress("0x3")),
		types.NewSignerInfo("http://localhost:8004", common.HexToAddress("0x4")),
		types.NewSignerInfo("http://localhost:8005", common.HexToAddress("0x5")),
		types.NewSignerInfo("http://localhost:8006", common.HexToAddress("0x6")),
	}

	testCases := []struct {
		name                 string
		signers              []*types.SignerInfo
		expectedValidatorsFn func(*testing.T, []*types.SignerInfo) []types.CertificateValidateAndSigner
		expectedThreshold    uint32
		expectedError        string
	}{
		{
			name:              "successful return of committee validators",
			signers:           allSigners[:len(allSigners)/2],
			expectedThreshold: uint32(len(allSigners) / 2),
			expectedValidatorsFn: func(t *testing.T,
				signers []*types.SignerInfo) []types.CertificateValidateAndSigner {
				t.Helper()

				validators := make([]types.CertificateValidateAndSigner, 0, len(signers))
				for i, signer := range signers {
					validator, err := NewRemoteValidator(&grpc.ClientConfig{URL: signer.URL}, nil, signer.Address, uint32(i))
					require.NoError(t, err)
					validators = append(validators, validator)
				}
				return validators
			},
		},
		{
			name:          "failed to query the committee",
			expectedError: "invalid parameters",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			multisigQuerierMock := mocks.NewMultisigQuerier(t)

			if tc.expectedError == "" {
				committee, err := types.NewMultisigCommittee(tc.signers, uint32(len(tc.signers)))
				require.NoError(t, err)
				multisigQuerierMock.EXPECT().
					GetMultisigCommittee(mock.Anything, mock.Anything).
					Return(committee, nil)
			} else {
				multisigQuerierMock.EXPECT().
					GetMultisigCommittee(mock.Anything, mock.Anything).
					Return(nil, errors.New(tc.expectedError))
			}

			poller := &validatorPoller{
				log:                log.WithFields("test", tc.name),
				validatorClientCfg: &grpc.ClientConfig{},
				multisigQuerier:    multisigQuerierMock,
			}

			validators, threshold, err := poller.getValidators(t.Context(), 10)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				expectedValidators := tc.expectedValidatorsFn(t, tc.signers)
				require.Len(t, validators, len(tc.signers))
				for i, v := range expectedValidators {
					require.Equal(t, v.URL(), validators[i].URL())
				}
				require.Equal(t, tc.expectedThreshold, threshold)
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
		tc := tc
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

func TestExecuteRequest(t *testing.T) {
	t.Parallel()

	certificate := &agglayertypes.Certificate{
		NetworkID: 1,
		Height:    1,
	}

	tests := []struct {
		name               string
		setupMocks         func(*mocks.Signer) ([]types.CertificateValidateAndSigner, uint32)
		expectedMinSigs    int
		expectErrSubstring string
	}{
		{
			name: "single healthy validator returns valid signature",
			setupMocks: func(mockSigner *mocks.Signer) ([]types.CertificateValidateAndSigner, uint32) {
				mockSigner.EXPECT().PublicAddress().Return(common.HexToAddress("0xdeadbeef")).Once()

				mockValidator := mocks.NewCertificateValidateAndSigner(t)
				mockValidator.EXPECT().Address().Return(common.HexToAddress("0x2")).Once()
				mockValidator.EXPECT().Index().Return(uint32(1))
				mockValidator.EXPECT().
					ValidateAndSignCertificate(mock.Anything, mock.Anything, mock.Anything).
					Return(make([]byte, aggkitcommon.SignatureSize), nil).
					Once()
				return []types.CertificateValidateAndSigner{mockValidator}, 1
			},
			expectedMinSigs: 1,
		},
		{
			name: "multiple validators reach threshold",
			setupMocks: func(mockSigner *mocks.Signer) ([]types.CertificateValidateAndSigner, uint32) {
				mockSigner.EXPECT().PublicAddress().Return(common.HexToAddress("0xdeadbeef"))

				v1 := mocks.NewCertificateValidateAndSigner(t)
				v2 := mocks.NewCertificateValidateAndSigner(t)
				v3 := mocks.NewCertificateValidateAndSigner(t)

				for i, v := range [](*mocks.CertificateValidateAndSigner){v1, v2, v3} {
					v.EXPECT().Index().Return(uint32(i)).Maybe()
					v.EXPECT().Address().Return(common.HexToAddress(fmt.Sprintf("0x%d", i+1))).Maybe()
					v.EXPECT().
						ValidateAndSignCertificate(mock.Anything, mock.Anything, mock.Anything).
						Return(make([]byte, aggkitcommon.SignatureSize), nil).Maybe()
				}

				validators := []types.CertificateValidateAndSigner{v1, v2, v3}
				return validators, 2
			},
			expectedMinSigs: 2,
		},
		{
			name: "threshold not reached",
			setupMocks: func(mockSigner *mocks.Signer) ([]types.CertificateValidateAndSigner, uint32) {
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
				return validators, 2
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
				threshold  uint32
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
			}, threshold, validators)

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
