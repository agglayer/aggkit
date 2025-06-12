package flows

import (
	"context"
	"errors"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/certificatebuild"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/log"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestBuildCertificate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name          string
		buildParams   *types.CertificateBuildParams
		mockFn        func(*mocks.CertificateBuilder, *mocks.Signer)
		expectedError string
	}{
		{
			name: "successfully builds certificate",
			buildParams: &types.CertificateBuildParams{
				FromBlock:           0,
				ToBlock:             10,
				RetryCount:          0,
				L1InfoTreeLeafCount: 1,
				CertificateType:     types.CertificateTypePP,
				LastSentCertificate: &types.CertificateHeader{ToBlock: 5},
				Bridges:             []bridgesync.Bridge{{}},
			},
			mockFn: func(mockCertBuilder *mocks.CertificateBuilder, mockSigner *mocks.Signer) {
				mockCertBuilder.EXPECT().BuildCertificate(ctx, mock.Anything, &types.CertificateHeader{ToBlock: 5}, false).Return(&agglayertypes.Certificate{
					NewLocalExitRoot: common.HexToHash("0x456"),
				}, nil).Once()
				mockSigner.EXPECT().SignHash(mock.Anything, mock.Anything).Return([]byte("mock_signature"), nil).Once()
				mockSigner.EXPECT().PublicAddress().Return(common.HexToAddress("0x123")).Once()
			},
		},
		{
			name: "error building certificate",
			buildParams: &types.CertificateBuildParams{
				FromBlock:           10,
				ToBlock:             20,
				RetryCount:          1,
				L1InfoTreeLeafCount: 23,
				CertificateType:     types.CertificateTypePP,
				LastSentCertificate: &types.CertificateHeader{ToBlock: 9},
				Bridges:             []bridgesync.Bridge{{}},
			},
			mockFn: func(mockCertBuilder *mocks.CertificateBuilder, mockSigner *mocks.Signer) {
				mockCertBuilder.EXPECT().BuildCertificate(ctx, mock.Anything, &types.CertificateHeader{ToBlock: 9}, false).Return(nil, errors.New("build error")).Once()
			},
			expectedError: "ppFlow - error building certificate",
		},
		{
			name: "error signing certificate",
			buildParams: &types.CertificateBuildParams{
				FromBlock:           20,
				ToBlock:             30,
				RetryCount:          2,
				L1InfoTreeLeafCount: 45,
				CertificateType:     types.CertificateTypePP,
				LastSentCertificate: &types.CertificateHeader{ToBlock: 19},
				Bridges:             []bridgesync.Bridge{{}},
			},
			mockFn: func(mockCertBuilder *mocks.CertificateBuilder, mockSigner *mocks.Signer) {
				mockCertBuilder.EXPECT().BuildCertificate(ctx, mock.Anything, &types.CertificateHeader{ToBlock: 19}, false).Return(&agglayertypes.Certificate{
					NewLocalExitRoot: common.HexToHash("0x789"),
				}, nil).Once()
				mockSigner.EXPECT().SignHash(mock.Anything, mock.Anything).Return(nil, errors.New("sign error")).Once()
			},
			expectedError: "ppFlow - error signing certificate",
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockCertBuilder := mocks.NewCertificateBuilder(t)
			mockSigner := mocks.NewSigner(t)
			if tt.mockFn != nil {
				tt.mockFn(mockCertBuilder, mockSigner)
			}

			flow := &PPFlow{
				log:                log.WithFields("aggsender-test", "PPFlow_buildCertificate"),
				certificateBuilder: mockCertBuilder,
				signer:             mockSigner,
			}

			cert, err := flow.BuildCertificate(ctx, tt.buildParams)
			if tt.expectedError != "" {
				require.ErrorContains(t, err, tt.expectedError)
				require.Nil(t, cert)
			} else {
				require.NoError(t, err)
				require.NotNil(t, cert)

				require.IsType(t, &agglayertypes.AggchainDataSignature{}, cert.AggchainData)
			}
		})
	}
}

func Test_PPFlow_GetCertificateBuildParams(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	testCases := []struct {
		name               string
		forceOneBridgeExit bool
		mockFn             func(*mocks.CertificateBuilder, *mocks.CertificateBuildVerifier, *mocks.L1InfoTreeDataQuerier)
		expectedParams     *types.CertificateBuildParams
		expectedError      string
	}{
		{
			name: "error getting certificate build params",
			mockFn: func(mockCertBuilder *mocks.CertificateBuilder,
				mockCertVerifier *mocks.CertificateBuildVerifier,
				mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				mockCertBuilder.EXPECT().GetCertificateBuildParams(ctx, types.CertificateTypePP).Return(nil, errors.New("some error"))
			},
			expectedError: "some error",
		},
		{
			name: "no new blocks to send a certificate",
			mockFn: func(mockCertBuilder *mocks.CertificateBuilder,
				mockCertVerifier *mocks.CertificateBuildVerifier,
				mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				mockCertBuilder.EXPECT().GetCertificateBuildParams(ctx, types.CertificateTypePP).Return(nil, certificatebuild.ErrNoNewBlocks)
			},
			expectedError: "",
		},
		{
			name: "no bridges - force one bridge exit is true",
			mockFn: func(mockCertBuilder *mocks.CertificateBuilder,
				mockCertVerifier *mocks.CertificateBuildVerifier,
				mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				mockCertBuilder.EXPECT().GetCertificateBuildParams(ctx, types.CertificateTypePP).Return(&types.CertificateBuildParams{}, nil)
			},
			forceOneBridgeExit: true,
			expectedParams:     nil,
		},
		{
			name: "no bridges - force one bridge exit is false",
			mockFn: func(mockCertBuilder *mocks.CertificateBuilder,
				mockCertVerifier *mocks.CertificateBuildVerifier,
				mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				mockCertBuilder.EXPECT().GetCertificateBuildParams(ctx, types.CertificateTypePP).Return(&types.CertificateBuildParams{
					Claims: []bridgesync.Claim{{}},
				}, nil)
				mockCertVerifier.EXPECT().VerifyBuildParams(mock.Anything).Return(nil)
				mockL1InfoTreeQuerier.EXPECT().GetLatestFinalizedL1InfoRoot(ctx).Return(
					&treetypes.Root{Hash: common.HexToHash("0x123"), BlockNum: 10, Index: 1}, nil, nil)
			},
			forceOneBridgeExit: false,
			expectedParams: &types.CertificateBuildParams{
				Claims:                         []bridgesync.Claim{{}},
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0x123"),
				L1InfoTreeLeafCount:            2,
			},
		},
		{
			name: "no bridges and claims - force one bridge exit is false",
			mockFn: func(mockCertBuilder *mocks.CertificateBuilder,
				mockCertVerifier *mocks.CertificateBuildVerifier,
				mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				mockCertBuilder.EXPECT().GetCertificateBuildParams(ctx, types.CertificateTypePP).Return(&types.CertificateBuildParams{}, nil)
			},
			expectedParams: nil,
		},
		{
			name: "error verifying build params",
			mockFn: func(mockCertBuilder *mocks.CertificateBuilder,
				mockCertVerifier *mocks.CertificateBuildVerifier,
				mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				mockCertBuilder.EXPECT().GetCertificateBuildParams(ctx, types.CertificateTypePP).Return(&types.CertificateBuildParams{Bridges: []bridgesync.Bridge{{}}}, nil)
				mockCertVerifier.EXPECT().VerifyBuildParams(&types.CertificateBuildParams{Bridges: []bridgesync.Bridge{{}}}).Return(errors.New("verification error"))
			},
			expectedError: "ppFlow - error verifying build params: verification error",
		},
		{
			name: "error GetLatestFinalizedL1InfoRoot",
			mockFn: func(mockCertBuilder *mocks.CertificateBuilder,
				mockCertVerifier *mocks.CertificateBuildVerifier,
				mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				mockCertBuilder.EXPECT().GetCertificateBuildParams(ctx, types.CertificateTypePP).Return(&types.CertificateBuildParams{Bridges: []bridgesync.Bridge{{}}}, nil)
				mockCertVerifier.EXPECT().VerifyBuildParams(&types.CertificateBuildParams{Bridges: []bridgesync.Bridge{{}}}).Return(nil)
				mockL1InfoTreeQuerier.On("GetLatestFinalizedL1InfoRoot", ctx).Return(nil, nil, errors.New("some error"))
			},
			expectedError: "ppFlow - error getting latest finalized L1 info root: some error",
		},
		{
			name: "success",
			mockFn: func(mockCertBuilder *mocks.CertificateBuilder,
				mockCertVerifier *mocks.CertificateBuildVerifier,
				mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				mockCertBuilder.EXPECT().GetCertificateBuildParams(ctx, types.CertificateTypePP).Return(&types.CertificateBuildParams{
					FromBlock:           6,
					ToBlock:             10,
					RetryCount:          0,
					L1InfoTreeLeafCount: 1,
					CertificateType:     types.CertificateTypePP,
					LastSentCertificate: &types.CertificateHeader{ToBlock: 5},
					Bridges:             []bridgesync.Bridge{{}},
					Claims:              []bridgesync.Claim{{}},
				}, nil)
				mockCertVerifier.EXPECT().VerifyBuildParams(mock.Anything).Return(nil)
				mockL1InfoTreeQuerier.EXPECT().GetLatestFinalizedL1InfoRoot(ctx).Return(
					&treetypes.Root{Hash: common.HexToHash("0x123"), BlockNum: 10}, nil, nil)
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:                      6,
				ToBlock:                        10,
				RetryCount:                     0,
				L1InfoTreeLeafCount:            1,
				CertificateType:                types.CertificateTypePP,
				LastSentCertificate:            &types.CertificateHeader{ToBlock: 5},
				Bridges:                        []bridgesync.Bridge{{}},
				Claims:                         []bridgesync.Claim{{}},
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0x123"),
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mockCertBuilder := mocks.NewCertificateBuilder(t)
			mockCertVerifier := mocks.NewCertificateBuildVerifier(t)
			mockL1InfoTreeQuerier := mocks.NewL1InfoTreeDataQuerier(t)
			logger := log.WithFields("test", "Test_PPFlow_GetCertificateBuildParams")
			ppFlow := NewPPFlow(
				logger,
				nil, // storage
				mockL1InfoTreeQuerier,
				nil, // l2BridgeQuerier
				mockCertBuilder,
				mockCertVerifier,
				nil, // signer
				tc.forceOneBridgeExit,
			)

			tc.mockFn(mockCertBuilder, mockCertVerifier, mockL1InfoTreeQuerier)

			params, err := ppFlow.GetCertificateBuildParams(ctx)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedParams, params)
			}
		})
	}
}

func Test_PPFlow_CheckInitialStatus(t *testing.T) {
	sut := &PPFlow{}
	require.Nil(t, sut.CheckInitialStatus(context.TODO()))
}

func Test_PPFlow_SignCertificate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

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
				mockSigner.EXPECT().SignHash(ctx, mock.Anything).Return([]byte("mock_signature"), nil)
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
				mockSigner.EXPECT().SignHash(ctx, mock.Anything).Return(nil, errors.New("signing error"))
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

			ppFlow := &PPFlow{
				log:    logger,
				signer: mockSigner,
			}

			signedCert, err := ppFlow.signCertificate(ctx, tt.certificate)

			if tt.expectedError != "" {
				require.ErrorContains(t, err, tt.expectedError)
				require.Nil(t, signedCert)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedCert, signedCert)
			}
		})
	}
}
