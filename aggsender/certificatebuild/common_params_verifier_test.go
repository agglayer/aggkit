package certificatebuild

import (
	"context"
	"errors"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func Test_baseFlow_VerifyBuildParams(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	testCases := []struct {
		name          string
		buildParams   *types.CertificateBuildParams
		mockFn        func(*mocks.BridgeQuerier)
		expectedError string
	}{
		{
			name: "invalid retry starting block",
			buildParams: &types.CertificateBuildParams{
				FromBlock:  10,
				ToBlock:    15,
				RetryCount: 1,
				LastSentCertificate: &types.CertificateHeader{
					Height:    1,
					Status:    agglayertypes.InError,
					FromBlock: 5,
					ToBlock:   10,
				},
			},
			expectedError: "retry certificate fromBlock 10 != last sent certificate fromBlock 5",
		},
		{
			name: "invalid claim GER",
			buildParams: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   10,
				Claims: []bridgesync.Claim{
					{GlobalExitRoot: common.HexToHash("0x123"), MainnetExitRoot: common.HexToHash("0x456"), RollupExitRoot: common.HexToHash("0x789")},
				},
			},
			expectedError: "GER mismatch",
		},
		{
			name: "success",
			buildParams: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   10,
			},
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mockL2BridgeQuerier := mocks.NewBridgeQuerier(t)
			if tc.mockFn != nil {
				tc.mockFn(mockL2BridgeQuerier)
			}

			verifier := NewCommonParamsVerifier(mockL2BridgeQuerier, false)

			err := verifier.VerifyBuildParams(ctx, tc.buildParams)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func Test_VerifyBlockRangeGaps(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	type args struct {
		lastSentCertificate *types.CertificateHeader
		newFromBlock        uint64
		newToBlock          uint64
	}
	tests := []struct {
		name            string
		args            args
		requireNoFEPGap bool
		mockFn          func(mockL2BridgeQuerier *mocks.BridgeQuerier)
		expectedError   string
	}{
		{
			name: "lastSentCertificate is nil",
			args: args{
				lastSentCertificate: nil,
				newFromBlock:        10,
				newToBlock:          20,
			},
		},
		{
			name: "no gap between certificates",
			args: args{
				lastSentCertificate: &types.CertificateHeader{
					Status:    agglayertypes.Settled,
					FromBlock: 10,
					ToBlock:   20,
				},
				newFromBlock: 21,
				newToBlock:   30,
			},
		},
		{
			name: "gap exists but no bridges or claims in gap",
			args: args{
				lastSentCertificate: &types.CertificateHeader{
					Status:    agglayertypes.Settled,
					FromBlock: 10,
					ToBlock:   15,
				},
				newFromBlock: 17,
				newToBlock:   20,
			},
			mockFn: func(mockL2BridgeQuerier *mocks.BridgeQuerier) {
				// gap is [16,16]
				mockL2BridgeQuerier.EXPECT().
					GetBridgesAndClaims(ctx, uint64(16), uint64(16)).
					Return([]bridgesync.Bridge{}, []bridgesync.Claim{}, nil)
			},
		},
		{
			name: "gap exists and bridges in gap returns error",
			args: args{
				lastSentCertificate: &types.CertificateHeader{
					Status:    agglayertypes.Settled,
					FromBlock: 10,
					ToBlock:   15,
				},
				newFromBlock: 17,
				newToBlock:   20,
			},
			mockFn: func(mockL2BridgeQuerier *mocks.BridgeQuerier) {
				mockL2BridgeQuerier.EXPECT().
					GetBridgesAndClaims(ctx, uint64(16), uint64(16)).
					Return([]bridgesync.Bridge{{}}, []bridgesync.Claim{}, nil)
			},
			expectedError: "there are new bridges or claims in the gap",
		},
		{
			name: "gap exists and claims in gap returns error",
			args: args{
				lastSentCertificate: &types.CertificateHeader{
					Status:    agglayertypes.Settled,
					FromBlock: 10,
					ToBlock:   15,
				},
				newFromBlock: 17,
				newToBlock:   20,
			},
			mockFn: func(mockL2BridgeQuerier *mocks.BridgeQuerier) {
				mockL2BridgeQuerier.EXPECT().
					GetBridgesAndClaims(ctx, uint64(16), uint64(16)).
					Return([]bridgesync.Bridge{}, []bridgesync.Claim{{}}, nil)
			},
			expectedError: "there are new bridges or claims in the gap",
		},
		{
			name: "gap exists, no bridges/claims, RequireNoFEPBlockGap true returns error",
			args: args{
				lastSentCertificate: &types.CertificateHeader{
					Status:    agglayertypes.Settled,
					FromBlock: 10,
					ToBlock:   15,
				},
				newFromBlock: 17,
				newToBlock:   20,
			},
			requireNoFEPGap: true,
			mockFn: func(mockL2BridgeQuerier *mocks.BridgeQuerier) {
				mockL2BridgeQuerier.EXPECT().
					GetBridgesAndClaims(ctx, uint64(16), uint64(16)).
					Return([]bridgesync.Bridge{}, []bridgesync.Claim{}, nil)
			},
			expectedError: "block gap detected",
		},
		{
			name: "gap exists, GetBridgesAndClaims returns error",
			args: args{
				lastSentCertificate: &types.CertificateHeader{
					Status:    agglayertypes.Settled,
					FromBlock: 10,
					ToBlock:   15,
				},
				newFromBlock: 17,
				newToBlock:   20,
			},
			mockFn: func(mockL2BridgeQuerier *mocks.BridgeQuerier) {
				mockL2BridgeQuerier.EXPECT().
					GetBridgesAndClaims(ctx, uint64(16), uint64(16)).
					Return(nil, nil, errors.New("db error"))
			},
			expectedError: "error getting bridges and claims in the gap",
		},
		{
			name: "lastSentCertificate is InError, gap logic uses FromBlock-1",
			args: args{
				lastSentCertificate: &types.CertificateHeader{
					Status:    agglayertypes.InError,
					FromBlock: 5,
					ToBlock:   10,
				},
				newFromBlock: 7,
				newToBlock:   10,
			},
			mockFn: func(mockL2BridgeQuerier *mocks.BridgeQuerier) {
				// lastSettledToBlock = 4, so gap is [5,6]
				mockL2BridgeQuerier.EXPECT().
					GetBridgesAndClaims(ctx, uint64(5), uint64(6)).
					Return([]bridgesync.Bridge{}, []bridgesync.Claim{}, nil)
			},
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockL2BridgeQuerier := mocks.NewBridgeQuerier(t)
			if tt.mockFn != nil {
				tt.mockFn(mockL2BridgeQuerier)
			}

			verifier := NewCommonParamsVerifier(mockL2BridgeQuerier, tt.requireNoFEPGap)

			err := verifier.VerifyBlockRangeGaps(ctx, tt.args.lastSentCertificate, tt.args.newFromBlock, tt.args.newToBlock)
			if tt.expectedError != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.expectedError)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
