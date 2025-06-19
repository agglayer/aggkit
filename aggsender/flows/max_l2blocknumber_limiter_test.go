package flows

import (
	"testing"

	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/log"
	"github.com/stretchr/testify/require"
)

func TestAdaptCertificateForFEP(t *testing.T) {
	tests := []struct {
		name                 string
		maxL2BlockNumber     uint64
		buildParams          *types.CertificateBuildParams
		expectedBuildParams  *types.CertificateBuildParams
		expectedError        error
		expectedErrorContain string
	}{
		{
			name:             "Feature disabled",
			maxL2BlockNumber: 0,
			buildParams: &types.CertificateBuildParams{
				FromBlock: 100,
				ToBlock:   200,
			},
			expectedBuildParams: &types.CertificateBuildParams{
				FromBlock: 100,
				ToBlock:   200,
			},
			expectedError: nil,
		},
		{
			name:                "BuildParams is nil",
			maxL2BlockNumber:    100,
			buildParams:         nil,
			expectedBuildParams: nil,
			expectedError:       ErrBuildParamsIsNil,
		},
		{
			name:             "Allowed block number",
			maxL2BlockNumber: 200,
			buildParams: &types.CertificateBuildParams{
				FromBlock: 100,
				ToBlock:   150,
			},
			expectedBuildParams: &types.CertificateBuildParams{
				FromBlock: 100,
				ToBlock:   150,
			},
			expectedError: nil,
		},
		{
			name:             "Retry certificate not allowed to resize",
			maxL2BlockNumber: 150,
			buildParams: &types.CertificateBuildParams{
				FromBlock:  100,
				ToBlock:    200,
				RetryCount: 1,
			},
			expectedBuildParams: nil,
			expectedError:       ErrMaxL2BlockNumberExceededInARetryCert,
		},
		{
			name:             "Upcoming next range",
			maxL2BlockNumber: 150,
			buildParams: &types.CertificateBuildParams{
				FromBlock: 151,
				ToBlock:   200,
			},
			expectedBuildParams:  nil,
			expectedError:        ErrComplete,
			expectedErrorContain: "upcoming next range",
		},
		{
			name:             "exceeded the maximum block",
			maxL2BlockNumber: 150,
			buildParams: &types.CertificateBuildParams{
				FromBlock: 251,
				ToBlock:   300,
			},
			expectedBuildParams:  nil,
			expectedError:        ErrComplete,
			expectedErrorContain: "exceeded the maximum block",
		},
		{
			name:             "Adjust range successfully",
			maxL2BlockNumber: 150,
			buildParams: &types.CertificateBuildParams{
				FromBlock: 100,
				ToBlock:   200,
			},
			expectedBuildParams: &types.CertificateBuildParams{
				FromBlock: 100,
				ToBlock:   150,
			},
			expectedError: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sut := NewMaxL2BlockNumberLimiter(
				tt.maxL2BlockNumber,
				log.WithFields("module", "feature_maxl2blocknumber_test"),
				false,
				false,
			)
			result, err := sut.AdaptCertificate(tt.buildParams)
			log.Infof("Test [%s]: maxL2BlockNumber: %d, buildParam:%+v, Result: %+v, Error: %v",
				tt.name,
				tt.maxL2BlockNumber,
				tt.buildParams,
				result,
				err,
			)
			if tt.expectedBuildParams == nil {
				require.Nil(t, result)
			} else {
				require.NotNil(t, result)
				require.Equal(t, tt.expectedBuildParams.FromBlock, result.FromBlock)
				require.Equal(t, tt.expectedBuildParams.ToBlock, result.ToBlock)
			}
			if tt.expectedError == nil && tt.expectedErrorContain == "" {
				require.NoError(t, err)
			} else {
				if tt.expectedError != nil {
					require.Error(t, err)
					require.Contains(t, err.Error(), tt.expectedError.Error())
				}
				if tt.expectedErrorContain != "" {
					require.Error(t, err)
					require.Contains(t, err.Error(), tt.expectedErrorContain)
				}
			}
		})
	}
}

func TestAdaptCertificateForPP(t *testing.T) {
	tests := []struct {
		name                 string
		maxL2BlockNumber     uint64
		buildParams          *types.CertificateBuildParams
		expectedBuildParams  *types.CertificateBuildParams
		expectedError        error
		expectetErrorContain string
	}{
		{
			name:             "PP:Retry certificate allowed to resize",
			maxL2BlockNumber: 150,
			buildParams: &types.CertificateBuildParams{
				FromBlock:  100,
				ToBlock:    200,
				RetryCount: 1,
				Bridges: []bridgesync.Bridge{
					{
						BlockNum: 120,
					},
				},
				Claims: []bridgesync.Claim{
					{
						BlockNum: 120,
					},
				},
			},
			expectedBuildParams: &types.CertificateBuildParams{
				FromBlock: 100,
				ToBlock:   150,
			},
			expectedError: nil,
		},
		{
			name:             "PP:Upcoming next range",
			maxL2BlockNumber: 150,
			buildParams: &types.CertificateBuildParams{
				FromBlock: 151,
				ToBlock:   200,
			},
			expectedBuildParams:  nil,
			expectedError:        ErrComplete,
			expectetErrorContain: "upcoming next range",
		},
		{
			name:             "PP:Adjust range successfully empty not allowed",
			maxL2BlockNumber: 150,
			buildParams: &types.CertificateBuildParams{
				FromBlock: 100,
				ToBlock:   200,
				Claims: []bridgesync.Claim{
					{
						BlockNum: 120,
					},
				},
			},
			expectedBuildParams:  nil,
			expectedError:        nil,
			expectetErrorContain: "has no bridges",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sut := NewMaxL2BlockNumberLimiter(
				tt.maxL2BlockNumber,
				log.WithFields("module", "feature_maxl2blocknumber_test"),
				true,
				true,
			)
			result, err := sut.AdaptCertificate(tt.buildParams)
			log.Infof("Test [%s]: maxL2BlockNumber: %d, buildParam:%+v, Result: %+v, Error: %v",
				tt.name,
				tt.maxL2BlockNumber,
				tt.buildParams,
				result,
				err,
			)
			if tt.expectedBuildParams == nil {
				require.Nil(t, result)
			} else {
				require.NotNil(t, result, "expected build params to be not nil")
				require.Equal(t, tt.expectedBuildParams.FromBlock, result.FromBlock)
				require.Equal(t, tt.expectedBuildParams.ToBlock, result.ToBlock)
			}
			if tt.expectedError == nil && tt.expectetErrorContain == "" {
				require.NoError(t, err)
			} else {
				if tt.expectedError != nil {
					require.Error(t, err)
					require.Contains(t, err.Error(), tt.expectedError.Error())
				}
				if tt.expectetErrorContain != "" {
					require.Error(t, err)
					require.Contains(t, err.Error(), tt.expectetErrorContain)
				}
			}
		})
	}
}

func TestIsUpcomingNextRange(t *testing.T) {
	sut := NewMaxL2BlockNumberLimiter(
		100,
		log.WithFields("module", "feature_maxl2blocknumber_test"),
		false,
		false,
	)

	require.True(t, sut.isUpcomingNextRange(101, 102))
	require.False(t, sut.isUpcomingNextRange(100, 102))
	sutDisabled := NewMaxL2BlockNumberLimiter(
		0,
		log.WithFields("module", "feature_maxl2blocknumber_test"),
		false,
		false,
	)
	require.False(t, sutDisabled.isUpcomingNextRange(1, 2))
}

func TestIsAllowedBlockNumber(t *testing.T) {
	sut := NewMaxL2BlockNumberLimiter(
		100,
		log.WithFields("module", "feature_maxl2blocknumber_test"),
		false,
		false,
	)
	require.True(t, sut.IsAllowedBlockNumber(100))
	require.True(t, sut.IsAllowedBlockNumber(50))
	require.False(t, sut.IsAllowedBlockNumber(101))
	sutDisabled := NewMaxL2BlockNumberLimiter(
		0,
		log.WithFields("module", "feature_maxl2blocknumber_test"),
		false,
		false,
	)
	require.True(t, sutDisabled.IsAllowedBlockNumber(1))
}
