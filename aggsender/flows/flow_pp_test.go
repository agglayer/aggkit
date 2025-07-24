package flows

import (
	"errors"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/certificatebuild"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_PPFlow_GetCertificateBuildParams(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name               string
		mockFn             func(*mocks.CommonCertParamsBuilder, *mocks.CommonCertParamsVerifier)
		forceOneBridgeExit bool
		expectedParams     *types.CertificateBuildParams
		expectedError      string
	}{
		{
			name: "error getting common certificate build params",
			mockFn: func(mockBuilder *mocks.CommonCertParamsBuilder,
				mockVerifier *mocks.CommonCertParamsVerifier) {
				mockBuilder.EXPECT().GetCommonCertificateBuildParams(t.Context(), types.CertificateTypePP).Return(nil, errors.New("some error")).Once()
			},
			expectedError: "some error",
		},
		{
			name: "no new blocks to send a certificate",
			mockFn: func(mockBuilder *mocks.CommonCertParamsBuilder,
				mockVerifier *mocks.CommonCertParamsVerifier) {
				mockBuilder.EXPECT().GetCommonCertificateBuildParams(t.Context(), types.CertificateTypePP).Return(nil, certificatebuild.ErrNoNewBlocks).Once()
			},
			expectedParams: nil,
		},
		{
			name:               "no bridges when forceOneBridgeExit is true",
			forceOneBridgeExit: true,
			mockFn: func(mockBuilder *mocks.CommonCertParamsBuilder,
				mockVerifier *mocks.CommonCertParamsVerifier) {
				mockBuilder.EXPECT().GetCommonCertificateBuildParams(t.Context(), types.CertificateTypePP).Return(&types.CertificateBuildParams{}, nil).Once()
			},
			expectedParams: nil,
		},
		{
			name:               "no bridges and claims when forceOneBridgeExit is false",
			forceOneBridgeExit: false,
			mockFn: func(mockBuilder *mocks.CommonCertParamsBuilder,
				mockVerifier *mocks.CommonCertParamsVerifier) {
				mockBuilder.EXPECT().GetCommonCertificateBuildParams(t.Context(), types.CertificateTypePP).Return(&types.CertificateBuildParams{}, nil).Once()
			},
			expectedParams: nil,
		},
		{
			name:               "no bridges when forceOneBridgeExit is false, but has claims",
			forceOneBridgeExit: false,
			mockFn: func(mockBuilder *mocks.CommonCertParamsBuilder,
				mockVerifier *mocks.CommonCertParamsVerifier) {
				params := &types.CertificateBuildParams{
					FromBlock: 0,
					ToBlock:   10,
					Claims:    []bridgesync.Claim{{BlockNum: 10}},
				}
				mockBuilder.EXPECT().GetCommonCertificateBuildParams(t.Context(), types.CertificateTypePP).Return(params, nil).Once()
				mockVerifier.EXPECT().VerifyBuildParams(t.Context(), params).Return(nil).Once()
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock: 0,
				ToBlock:   10,
				Claims:    []bridgesync.Claim{{BlockNum: 10}},
			},
		},
		{
			name: "error on verifying build params",
			mockFn: func(mockBuilder *mocks.CommonCertParamsBuilder,
				mockVerifier *mocks.CommonCertParamsVerifier) {
				params := &types.CertificateBuildParams{
					FromBlock: 0,
					ToBlock:   10,
					Bridges:   []bridgesync.Bridge{{BlockNum: 5}},
					Claims:    []bridgesync.Claim{{BlockNum: 10}},
				}
				mockBuilder.EXPECT().GetCommonCertificateBuildParams(t.Context(), types.CertificateTypePP).
					Return(params, nil).Once()
				mockVerifier.EXPECT().VerifyBuildParams(t.Context(), params).Return(errors.New("verification error")).Once()
			},
			expectedError: "ppFlow - error verifying build params",
		},
		{
			name: "success - has bridges and claims",
			mockFn: func(mockBuilder *mocks.CommonCertParamsBuilder,
				mockVerifier *mocks.CommonCertParamsVerifier) {
				params := &types.CertificateBuildParams{
					FromBlock: 0,
					ToBlock:   10,
					Bridges:   []bridgesync.Bridge{{BlockNum: 5}},
					Claims:    []bridgesync.Claim{{BlockNum: 10}},
				}
				mockBuilder.EXPECT().GetCommonCertificateBuildParams(t.Context(), types.CertificateTypePP).Return(params, nil).Once()
				mockVerifier.EXPECT().VerifyBuildParams(t.Context(), params).Return(nil).Once()
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock: 0,
				ToBlock:   10,
				Bridges:   []bridgesync.Bridge{{BlockNum: 5}},
				Claims:    []bridgesync.Claim{{BlockNum: 10}},
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mockParamsBuilder := mocks.NewCommonCertParamsBuilder(t)
			mockParamsVerifier := mocks.NewCommonCertParamsVerifier(t)

			logger := log.WithFields("test", "Test_PPFlow_GetCertificateBuildParams")
			ppFlow := NewPPFlow(
				logger,
				mockParamsBuilder,
				mockParamsVerifier,
				nil, // mockStorage
				nil, // mockL1InfoTreeQuerier
				nil, // mockL2BridgeQuerier
				nil, // mockSigner
				tc.forceOneBridgeExit,
				0,
			)

			tc.mockFn(mockParamsBuilder, mockParamsVerifier)

			params, err := ppFlow.GetCertificateBuildParams(t.Context())
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedParams, params)
			}

			mockParamsBuilder.AssertExpectations(t)
			mockParamsVerifier.AssertExpectations(t)
		})
	}
}

func Test_PPFlow_CheckInitialStatus(t *testing.T) {
	sut := &PPFlow{}
	require.Nil(t, sut.CheckInitialStatus(t.Context()))
}

func Test_PPFlow_BuildCertificate(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		buildParams   *types.CertificateBuildParams
		mockFn        func(*mocks.CommonCertParamsBuilder, *mocks.Signer)
		expectedCert  *agglayertypes.Certificate
		expectedError string
	}{
		{
			name: "error building common certificate",
			buildParams: &types.CertificateBuildParams{
				FromBlock:           0,
				ToBlock:             10,
				Bridges:             []bridgesync.Bridge{{BlockNum: 5}},
				Claims:              []bridgesync.Claim{{BlockNum: 10}},
				LastSentCertificate: nil,
			},
			mockFn: func(mockBuilder *mocks.CommonCertParamsBuilder, mockSigner *mocks.Signer) {
				mockBuilder.EXPECT().BuildCertificate(t.Context(), mock.Anything, (*types.CertificateHeader)(nil), false).
					Return(nil, errors.New("build error")).Once()
			},
			expectedError: "ppFlow - error building certificate: build error",
		},
		{
			name: "error signing certificate",
			buildParams: &types.CertificateBuildParams{
				FromBlock:           0,
				ToBlock:             10,
				Bridges:             []bridgesync.Bridge{{BlockNum: 5}},
				Claims:              []bridgesync.Claim{{BlockNum: 10}},
				LastSentCertificate: nil,
			},
			mockFn: func(mockBuilder *mocks.CommonCertParamsBuilder, mockSigner *mocks.Signer) {
				mockBuilder.EXPECT().BuildCertificate(t.Context(), mock.Anything, (*types.CertificateHeader)(nil), false).
					Return(&agglayertypes.Certificate{NewLocalExitRoot: common.HexToHash("0x456")}, nil).Once()
				mockSigner.EXPECT().SignHash(t.Context(), mock.Anything).Return(nil, errors.New("signing error")).Once()
			},
			expectedError: "ppFlow - error signing certificate: signing error",
		},
		{
			name: "successfully builds and signs certificate",
			buildParams: &types.CertificateBuildParams{
				FromBlock: 0,
				ToBlock:   9,
				Bridges:   []bridgesync.Bridge{{BlockNum: 5}},
				Claims:    []bridgesync.Claim{{BlockNum: 8}},
			},
			mockFn: func(mockBuilder *mocks.CommonCertParamsBuilder, mockSigner *mocks.Signer) {
				mockBuilder.EXPECT().BuildCertificate(t.Context(), mock.Anything, (*types.CertificateHeader)(nil), false).
					Return(&agglayertypes.Certificate{NewLocalExitRoot: common.HexToHash("0x456")}, nil).Once()
				mockSigner.EXPECT().SignHash(t.Context(), mock.Anything).Return([]byte("mock_signature"), nil).Once()
				mockSigner.EXPECT().PublicAddress().Return(common.HexToAddress("0x123")).Once()
			},
			expectedCert: &agglayertypes.Certificate{
				NewLocalExitRoot: common.HexToHash("0x456"),
				AggchainData: &agglayertypes.AggchainDataSignature{
					Signature: []byte("mock_signature"),
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockParamsBuilder := mocks.NewCommonCertParamsBuilder(t)
			mockSigner := mocks.NewSigner(t)

			if tc.mockFn != nil {
				tc.mockFn(mockParamsBuilder, mockSigner)
			}

			logger := log.WithFields("test", "Test_PPFlow_BuildCertificate")
			ppFlow := NewPPFlow(
				logger,
				mockParamsBuilder,
				nil, // commonParamsVerifier
				nil, // storage
				nil, // l1InfoTreeQuerier
				nil, // l2BridgeQuerier
				mockSigner,
				false, // forceOneBridgeExit
				0,     // maxL2BlockNumber
			)

			cert, err := ppFlow.BuildCertificate(t.Context(), tc.buildParams)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.NotNil(t, cert)
			}

			mockParamsBuilder.AssertExpectations(t)
			mockSigner.AssertExpectations(t)
		})
	}
}

func Test_PPFlow_SignCertificate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		mockSignerFn  func(*mocks.Signer)
		certificate   *agglayertypes.Certificate
		expectedCert  *agglayertypes.Certificate
		expectedError string
	}{
		{
			name: "successfully signs certificate",
			mockSignerFn: func(mockSigner *mocks.Signer) {
				mockSigner.EXPECT().SignHash(t.Context(), mock.Anything).Return([]byte("mock_signature"), nil)
				mockSigner.EXPECT().PublicAddress().Return(common.HexToAddress("0x123"))
			},
			certificate: &agglayertypes.Certificate{
				NewLocalExitRoot: common.HexToHash("0x456"),
			},
			expectedCert: &agglayertypes.Certificate{
				NewLocalExitRoot: common.HexToHash("0x456"),
				AggchainData: &agglayertypes.AggchainDataSignature{
					Signature: []byte("mock_signature"),
				},
			},
		},
		{
			name: "error signing certificate",
			mockSignerFn: func(mockSigner *mocks.Signer) {
				mockSigner.EXPECT().SignHash(t.Context(), mock.Anything).Return(nil, errors.New("signing error"))
			},
			certificate: &agglayertypes.Certificate{
				NewLocalExitRoot: common.HexToHash("0x456"),
			},
			expectedError: "signing error",
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockSigner := mocks.NewSigner(t)
			if tt.mockSignerFn != nil {
				tt.mockSignerFn(mockSigner)
			}
			logger := log.WithFields("test", "Test_PPFlow_SignCertificate")

			ppFlow := NewPPFlow(
				logger,
				nil, // commonParamsBuilder
				nil, // commonParamsVerifier
				nil, // storage
				nil, // l1InfoTreeDataQuerier
				nil, // l2BridgeQuerier
				mockSigner,
				false, // forceOneBridgeExit
				0,     // maxL2BlockNumber
			)

			signedCert, err := ppFlow.signCertificate(t.Context(), tt.certificate)

			if tt.expectedError != "" {
				require.ErrorContains(t, err, tt.expectedError)
				require.Nil(t, signedCert)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedCert, signedCert)
			}

			mockSigner.AssertExpectations(t)
		})
	}
}
