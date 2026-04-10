package flows

import (
	"context"
	"errors"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/log"
	"github.com/stretchr/testify/require"
)

func Test_PPFlow_GenerateBuildParams(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	preParams := &types.CertificatePreBuildParams{
		BlockRange:      common.NewBlockRange(1, 10),
		CertificateType: types.CertificateTypePP,
	}
	generatedParams := &types.CertificateBuildParams{FromBlock: 1, ToBlock: 10}
	adjustedParams := &types.CertificateBuildParams{FromBlock: 1, ToBlock: 8}
	expectedOptions := types.BlockRangeAdjustmentOptions{
		MaxL2BlockNumber:              8,
		AllowResizeRetryCert:          true,
		RequireOneBridgeInCertificate: true,
		ValidateRootToProve:           true,
		DisableSizeLimit:              true,
	}

	tests := []struct {
		name           string
		preParams      *types.CertificatePreBuildParams
		mockFn         func(*mocks.AggsenderFlowBaser)
		expectedParams *types.CertificateBuildParams
		expectedError  string
	}{
		{
			name:          "nil pre params",
			preParams:     nil,
			expectedError: "ppFlow - preParams is nil",
		},
		{
			name:      "generate build params error",
			preParams: preParams,
			mockFn: func(mockBaseFlow *mocks.AggsenderFlowBaser) {
				mockBaseFlow.EXPECT().GenerateBuildParams(ctx, *preParams).
					Return(nil, errors.New("generate failed")).Once()
			},
			expectedError: "ppFlow - error generating build params: generate failed",
		},
		{
			name:      "adjust block range error",
			preParams: preParams,
			mockFn: func(mockBaseFlow *mocks.AggsenderFlowBaser) {
				mockBaseFlow.EXPECT().GenerateBuildParams(ctx, *preParams).
					Return(generatedParams, nil).Once()
				mockBaseFlow.EXPECT().AdjustBlockRange(ctx, generatedParams, expectedOptions).
					Return(nil, errors.New("adjust failed")).Once()
			},
			expectedError: "ppFlow - error adjusting block range: adjust failed",
		},
		{
			name:      "verify build params error",
			preParams: preParams,
			mockFn: func(mockBaseFlow *mocks.AggsenderFlowBaser) {
				mockBaseFlow.EXPECT().GenerateBuildParams(ctx, *preParams).
					Return(generatedParams, nil).Once()
				mockBaseFlow.EXPECT().AdjustBlockRange(ctx, generatedParams, expectedOptions).
					Return(adjustedParams, nil).Once()
				mockBaseFlow.EXPECT().VerifyBuildParams(ctx, adjustedParams).
					Return(errors.New("verify failed")).Once()
			},
			expectedError: "ppFlow - error verifying build params: verify failed",
		},
		{
			name:      "success",
			preParams: preParams,
			mockFn: func(mockBaseFlow *mocks.AggsenderFlowBaser) {
				mockBaseFlow.EXPECT().GenerateBuildParams(ctx, *preParams).
					Return(generatedParams, nil).Once()
				mockBaseFlow.EXPECT().AdjustBlockRange(ctx, generatedParams, expectedOptions).
					Return(adjustedParams, nil).Once()
				mockBaseFlow.EXPECT().VerifyBuildParams(ctx, adjustedParams).Return(nil).Once()
			},
			expectedParams: adjustedParams,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockBaseFlow := mocks.NewAggsenderFlowBaser(t)
			if tc.mockFn != nil {
				tc.mockFn(mockBaseFlow)
			}

			sut := &PPBuilderFlow{
				baseFlow:           mockBaseFlow,
				log:                log.WithFields("test", t.Name()),
				forceOneBridgeExit: true,
				maxL2BlockNumber:   8,
			}

			result, err := sut.GenerateBuildParams(ctx, tc.preParams)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
				require.Nil(t, result)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.expectedParams, result)
		})
	}
}

func Test_PPFlow_BuildCertificate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	lastSent := &types.CertificateHeader{ToBlock: 9}
	buildParams := &types.CertificateBuildParams{LastSentCertificate: lastSent}
	expectedCert := &agglayertypes.Certificate{Height: 5}

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		mockBaseFlow := mocks.NewAggsenderFlowBaser(t)
		mockBaseFlow.EXPECT().BuildCertificate(ctx, buildParams, lastSent, false).
			Return(expectedCert, nil).Once()

		sut := &PPBuilderFlow{baseFlow: mockBaseFlow}

		result, err := sut.BuildCertificate(ctx, buildParams)

		require.NoError(t, err)
		require.Equal(t, expectedCert, result)
	})

	t.Run("wrapped error", func(t *testing.T) {
		t.Parallel()

		mockBaseFlow := mocks.NewAggsenderFlowBaser(t)
		mockBaseFlow.EXPECT().BuildCertificate(ctx, buildParams, lastSent, false).
			Return(nil, errors.New("build failed")).Once()

		sut := &PPBuilderFlow{baseFlow: mockBaseFlow}

		result, err := sut.BuildCertificate(ctx, buildParams)

		require.ErrorContains(t, err, "ppFlow - error building certificate: build failed")
		require.Nil(t, result)
	})
}

func Test_PPFlow_Signer(t *testing.T) {
	t.Parallel()

	mockSigner := mocks.NewSigner(t)
	sut := &PPBuilderFlow{certificateSigner: mockSigner}

	require.Same(t, mockSigner, sut.Signer())
}
