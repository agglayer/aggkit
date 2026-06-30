package flows

import (
	"context"
	"errors"
	"math/big"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/query"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	"github.com/agglayer/aggkit/log"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func Test_baseFlow_AdjustBlockRange_NilBuildParams(t *testing.T) {
	t.Parallel()

	f := &baseFlow{}

	result, err := f.AdjustBlockRange(context.Background(), nil, types.BlockRangeAdjustmentOptions{})

	require.ErrorIs(t, err, ErrBuildParamsIsNil)
	require.Nil(t, result)
}

func Test_baseFlow_adjustMaxL2BlockNumber(t *testing.T) {
	t.Parallel()

	lastSentCert := &types.CertificateHeader{CertificateID: common.HexToHash("0x1")}
	aggchainProof := &types.AggchainProof{EndBlock: 10}

	tests := []struct {
		name          string
		buildParams   *types.CertificateBuildParams
		options       types.BlockRangeAdjustmentOptions
		checkResult   func(t *testing.T, result *types.CertificateBuildParams)
		expectedError error
		errorContains string
	}{
		{
			name: "no max l2 block configured keeps original certificate",
			buildParams: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   10,
			},
			options: types.BlockRangeAdjustmentOptions{},
			checkResult: func(t *testing.T, result *types.CertificateBuildParams) {
				t.Helper()
				require.Equal(t, uint64(1), result.FromBlock)
				require.Equal(t, uint64(10), result.ToBlock)
			},
		},
		{
			name: "range already within limit keeps original certificate",
			buildParams: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   5,
			},
			options: types.BlockRangeAdjustmentOptions{MaxL2BlockNumber: 5},
			checkResult: func(t *testing.T, result *types.CertificateBuildParams) {
				t.Helper()
				require.Equal(t, uint64(1), result.FromBlock)
				require.Equal(t, uint64(5), result.ToBlock)
			},
		},
		{
			name: "retry certificate cannot be resized when disabled",
			buildParams: &types.CertificateBuildParams{
				FromBlock:                      1,
				ToBlock:                        10,
				RetryCount:                     1,
				LastSentCertificate:            lastSentCert,
				CertificateType:                types.CertificateTypePP,
				L1InfoTreeLeafCount:            5,
				AggchainProof:                  aggchainProof,
				ExtraData:                      "keep-me",
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0x11"),
			},
			options:       types.BlockRangeAdjustmentOptions{MaxL2BlockNumber: 5},
			expectedError: ErrMaxL2BlockNumberExceededInARetryCert,
		},
		{
			name: "upcoming next range returns complete",
			buildParams: &types.CertificateBuildParams{
				FromBlock: 6,
				ToBlock:   10,
			},
			options:       types.BlockRangeAdjustmentOptions{MaxL2BlockNumber: 5},
			expectedError: ErrComplete,
		},
		{
			name: "range starting far beyond max returns complete",
			buildParams: &types.CertificateBuildParams{
				FromBlock: 8,
				ToBlock:   10,
			},
			options:       types.BlockRangeAdjustmentOptions{MaxL2BlockNumber: 5},
			expectedError: ErrComplete,
		},
		{
			name: "shrinks range and preserves metadata",
			buildParams: &types.CertificateBuildParams{
				FromBlock:                      1,
				ToBlock:                        10,
				Bridges:                        []bridgesync.Bridge{{BlockNum: 4, DepositCount: 1}, {BlockNum: 9, DepositCount: 2}, {BlockNum: 10, DepositCount: 3}},
				Claims:                         []claimsynctypes.Claim{{BlockNum: 8}, {BlockNum: 10}},
				Unclaims:                       []claimsynctypes.Unclaim{{BlockNumber: 7}, {BlockNumber: 10}},
				CreatedAt:                      99,
				RetryCount:                     0,
				LastSentCertificate:            lastSentCert,
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0x22"),
				L1InfoTreeLeafCount:            12,
				AggchainProof:                  aggchainProof,
				CertificateType:                types.CertificateTypeFEP,
				ExtraData:                      "extra",
			},
			options: types.BlockRangeAdjustmentOptions{MaxL2BlockNumber: 9},
			checkResult: func(t *testing.T, result *types.CertificateBuildParams) {
				t.Helper()
				require.Equal(t, uint64(1), result.FromBlock)
				require.Equal(t, uint64(9), result.ToBlock)
				require.Len(t, result.Bridges, 2)
				require.Len(t, result.Claims, 1)
				require.Len(t, result.Unclaims, 1)
				require.Equal(t, uint32(99), result.CreatedAt)
				require.Zero(t, result.RetryCount)
				require.Same(t, lastSentCert, result.LastSentCertificate)
				require.Equal(t, common.HexToHash("0x22"), result.L1InfoTreeRootFromWhichToProve)
				require.Equal(t, uint32(12), result.L1InfoTreeLeafCount)
				require.Same(t, aggchainProof, result.AggchainProof)
				require.Equal(t, types.CertificateTypeFEP, result.CertificateType)
				require.Equal(t, "extra", result.ExtraData)
			},
		},
		{
			name: "allow empty certificate when bridges are not required",
			buildParams: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   10,
				Bridges:   []bridgesync.Bridge{{BlockNum: 10}},
			},
			options: types.BlockRangeAdjustmentOptions{MaxL2BlockNumber: 9},
			checkResult: func(t *testing.T, result *types.CertificateBuildParams) {
				t.Helper()
				require.Equal(t, uint64(1), result.FromBlock)
				require.Equal(t, uint64(9), result.ToBlock)
				require.Empty(t, result.Bridges)
				require.Empty(t, result.Claims)
			},
		},
		{
			name: "required bridge with remaining claims returns error",
			buildParams: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   10,
				Bridges:   []bridgesync.Bridge{{BlockNum: 10}},
				Claims:    []claimsynctypes.Claim{{BlockNum: 9}},
			},
			options:       types.BlockRangeAdjustmentOptions{MaxL2BlockNumber: 9, RequireOneBridgeInCertificate: true},
			errorContains: "has no bridges but has 1 imported bridges",
		},
		{
			name: "required bridge with empty adjusted certificate returns complete",
			buildParams: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   10,
				Bridges:   []bridgesync.Bridge{{BlockNum: 10}},
			},
			options:       types.BlockRangeAdjustmentOptions{MaxL2BlockNumber: 9, RequireOneBridgeInCertificate: true},
			expectedError: ErrComplete,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := &baseFlow{log: log.WithFields("test", t.Name())}

			result, err := f.adjustMaxL2BlockNumber(tc.buildParams, tc.options)

			if tc.expectedError != nil {
				require.ErrorIs(t, err, tc.expectedError)
				require.Nil(t, result)
				return
			}
			if tc.errorContains != "" {
				require.ErrorContains(t, err, tc.errorContains)
				require.Nil(t, result)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, result)
			tc.checkResult(t, result)
		})
	}
}

func Test_baseFlow_adjustMaxL2BlockRange(t *testing.T) {
	t.Parallel()

	lastSentCert := &types.CertificateHeader{CertificateID: common.HexToHash("0x1")}
	aggchainProof := &types.AggchainProof{EndBlock: 12}

	tests := []struct {
		name          string
		buildParams   *types.CertificateBuildParams
		options       types.BlockRangeAdjustmentOptions
		checkResult   func(t *testing.T, result *types.CertificateBuildParams)
		expectedError error
		errorContains string
	}{
		{
			name: "no max l2 block range configured keeps original certificate",
			buildParams: &types.CertificateBuildParams{
				FromBlock: 5,
				ToBlock:   12,
			},
			options: types.BlockRangeAdjustmentOptions{},
			checkResult: func(t *testing.T, result *types.CertificateBuildParams) {
				t.Helper()
				require.Equal(t, uint64(5), result.FromBlock)
				require.Equal(t, uint64(12), result.ToBlock)
			},
		},
		{
			name: "range already within limit keeps original certificate",
			buildParams: &types.CertificateBuildParams{
				FromBlock: 5,
				ToBlock:   8,
			},
			options: types.BlockRangeAdjustmentOptions{MaxL2BlockRange: 3},
			checkResult: func(t *testing.T, result *types.CertificateBuildParams) {
				t.Helper()
				require.Equal(t, uint64(5), result.FromBlock)
				require.Equal(t, uint64(8), result.ToBlock)
			},
		},
		{
			name: "single block range is always within limit",
			buildParams: &types.CertificateBuildParams{
				FromBlock: 5,
				ToBlock:   5,
			},
			options: types.BlockRangeAdjustmentOptions{MaxL2BlockRange: 1},
			checkResult: func(t *testing.T, result *types.CertificateBuildParams) {
				t.Helper()
				require.Equal(t, uint64(5), result.FromBlock)
				require.Equal(t, uint64(5), result.ToBlock)
			},
		},
		{
			name: "retry certificate cannot be resized when disabled",
			buildParams: &types.CertificateBuildParams{
				FromBlock:                      5,
				ToBlock:                        12,
				RetryCount:                     1,
				LastSentCertificate:            lastSentCert,
				CertificateType:                types.CertificateTypeFEP,
				L1InfoTreeLeafCount:            5,
				AggchainProof:                  aggchainProof,
				ExtraData:                      "keep-me",
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0x11"),
			},
			options:       types.BlockRangeAdjustmentOptions{MaxL2BlockRange: 3},
			expectedError: ErrMaxL2BlockRangeExceededInARetryCert,
		},
		{
			name: "shrinks range and preserves metadata",
			buildParams: &types.CertificateBuildParams{
				FromBlock:                      5,
				ToBlock:                        12,
				Bridges:                        []bridgesync.Bridge{{BlockNum: 5, DepositCount: 1}, {BlockNum: 8, DepositCount: 2}, {BlockNum: 9, DepositCount: 3}},
				Claims:                         []claimsynctypes.Claim{{BlockNum: 7}, {BlockNum: 9}},
				Unclaims:                       []claimsynctypes.Unclaim{{BlockNumber: 8}, {BlockNumber: 10}},
				CreatedAt:                      99,
				RetryCount:                     1,
				LastSentCertificate:            lastSentCert,
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0x22"),
				L1InfoTreeLeafCount:            12,
				AggchainProof:                  aggchainProof,
				CertificateType:                types.CertificateTypePP,
				ExtraData:                      "extra",
			},
			options: types.BlockRangeAdjustmentOptions{MaxL2BlockRange: 3, AllowResizeRetryCert: true},
			checkResult: func(t *testing.T, result *types.CertificateBuildParams) {
				t.Helper()
				require.Equal(t, uint64(5), result.FromBlock)
				require.Equal(t, uint64(8), result.ToBlock)
				require.Len(t, result.Bridges, 2)
				require.Len(t, result.Claims, 1)
				require.Len(t, result.Unclaims, 1)
				require.Equal(t, uint32(99), result.CreatedAt)
				require.Equal(t, 1, result.RetryCount)
				require.Same(t, lastSentCert, result.LastSentCertificate)
				require.Equal(t, common.HexToHash("0x22"), result.L1InfoTreeRootFromWhichToProve)
				require.Equal(t, uint32(12), result.L1InfoTreeLeafCount)
				require.Same(t, aggchainProof, result.AggchainProof)
				require.Equal(t, types.CertificateTypePP, result.CertificateType)
				require.Equal(t, "extra", result.ExtraData)
			},
		},
		{
			name: "allow empty certificate when bridges are not required",
			buildParams: &types.CertificateBuildParams{
				FromBlock: 5,
				ToBlock:   12,
				Bridges:   []bridgesync.Bridge{{BlockNum: 12}},
			},
			options: types.BlockRangeAdjustmentOptions{MaxL2BlockRange: 3},
			checkResult: func(t *testing.T, result *types.CertificateBuildParams) {
				t.Helper()
				require.Equal(t, uint64(5), result.FromBlock)
				require.Equal(t, uint64(8), result.ToBlock)
				require.Empty(t, result.Bridges)
				require.Empty(t, result.Claims)
			},
		},
		{
			name: "required bridge with remaining claims returns error",
			buildParams: &types.CertificateBuildParams{
				FromBlock: 5,
				ToBlock:   12,
				Bridges:   []bridgesync.Bridge{{BlockNum: 12}},
				Claims:    []claimsynctypes.Claim{{BlockNum: 7}},
			},
			options:       types.BlockRangeAdjustmentOptions{MaxL2BlockRange: 3, RequireOneBridgeInCertificate: true},
			errorContains: "has no bridges but has 1 imported bridges",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := &baseFlow{log: log.WithFields("test", t.Name())}

			result, err := f.adjustMaxL2BlockRange(tc.buildParams, tc.options)

			if tc.expectedError != nil {
				require.ErrorIs(t, err, tc.expectedError)
				require.Nil(t, result)
				return
			}
			if tc.errorContains != "" {
				require.ErrorContains(t, err, tc.errorContains)
				require.Nil(t, result)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, result)
			tc.checkResult(t, result)
		})
	}
}

func Test_baseFlow_validateRootToProveIsFinalized_Errors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	finalizedRoot := common.HexToHash("0x123")

	tests := []struct {
		name          string
		buildParams   *types.CertificateBuildParams
		mockFn        func(*mocks.L1InfoTreeDataQuerier)
		errorContains string
	}{
		{
			name:          "leaf count must be positive",
			buildParams:   &types.CertificateBuildParams{L1InfoTreeLeafCount: 0},
			errorContains: "L1InfoTreeLeafCount must be greater than 0",
		},
		{
			name: "error getting root by leaf index",
			buildParams: &types.CertificateBuildParams{
				L1InfoTreeLeafCount:            3,
				L1InfoTreeRootFromWhichToProve: finalizedRoot,
			},
			mockFn: func(mockQuerier *mocks.L1InfoTreeDataQuerier) {
				mockQuerier.EXPECT().GetL1InfoRootByLeafIndex(ctx, uint32(2)).
					Return(nil, errors.New("lookup failed")).Once()
			},
			errorContains: "error getting L1 info tree root by leaf count 3: lookup failed",
		},
		{
			name: "root mismatch",
			buildParams: &types.CertificateBuildParams{
				L1InfoTreeLeafCount:            3,
				L1InfoTreeRootFromWhichToProve: finalizedRoot,
			},
			mockFn: func(mockQuerier *mocks.L1InfoTreeDataQuerier) {
				mockQuerier.EXPECT().GetL1InfoRootByLeafIndex(ctx, uint32(2)).
					Return(&treetypes.Root{Hash: common.HexToHash("0x999"), Index: 2}, nil).Once()
			},
			errorContains: "does not match root",
		},
		{
			name: "error getting latest finalized root",
			buildParams: &types.CertificateBuildParams{
				L1InfoTreeLeafCount:            3,
				L1InfoTreeRootFromWhichToProve: finalizedRoot,
			},
			mockFn: func(mockQuerier *mocks.L1InfoTreeDataQuerier) {
				mockQuerier.EXPECT().GetL1InfoRootByLeafIndex(ctx, uint32(2)).
					Return(&treetypes.Root{Hash: finalizedRoot, Index: 2}, nil).Once()
				mockQuerier.EXPECT().GetTargetL1InfoRoot(ctx).Return(nil, nil, errors.New("target failed")).Once()
			},
			errorContains: "error getting latest finalized L1 info root: target failed",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockQuerier := mocks.NewL1InfoTreeDataQuerier(t)
			if tc.mockFn != nil {
				tc.mockFn(mockQuerier)
			}

			f := &baseFlow{
				l1InfoTreeDataQuerier: mockQuerier,
				log:                   log.WithFields("test", t.Name()),
			}

			err := f.validateRootToProveIsFinalized(ctx, tc.buildParams)
			require.ErrorContains(t, err, tc.errorContains)
		})
	}
}

func Test_baseFlow_adjustInvalidClaimsAreNotUnclaimed(t *testing.T) {
	t.Parallel()

	globalIndex := big.NewInt(7)
	ger := common.HexToHash("0x77")

	tests := []struct {
		name          string
		buildParams   *types.CertificateBuildParams
		mockFn        func(*mocks.L1InfoTreeDataQuerier)
		checkResult   func(t *testing.T, result *types.CertificateBuildParams)
		errorContains string
	}{
		{
			name: "claim on l1 keeps certificate",
			buildParams: &types.CertificateBuildParams{
				FromBlock: 5,
				ToBlock:   9,
				Claims:    []claimsynctypes.Claim{{BlockNum: 7, GlobalExitRoot: ger, GlobalIndex: globalIndex}},
			},
			mockFn: func(mockQuerier *mocks.L1InfoTreeDataQuerier) {
				mockQuerier.EXPECT().DoesGERExistsOnL1(ger).Return(true, nil).Once()
			},
			checkResult: func(t *testing.T, result *types.CertificateBuildParams) {
				t.Helper()
				require.Equal(t, uint64(9), result.ToBlock)
				require.Len(t, result.Claims, 1)
			},
		},
		{
			name: "posterior unclaim keeps certificate",
			buildParams: &types.CertificateBuildParams{
				FromBlock: 5,
				ToBlock:   9,
				Claims:    []claimsynctypes.Claim{{BlockNum: 7, GlobalExitRoot: ger, GlobalIndex: globalIndex}},
				Unclaims:  []claimsynctypes.Unclaim{{BlockNumber: 8, GlobalIndex: globalIndex}},
			},
			mockFn: func(mockQuerier *mocks.L1InfoTreeDataQuerier) {
				mockQuerier.EXPECT().DoesGERExistsOnL1(ger).Return(false, nil).Once()
			},
			checkResult: func(t *testing.T, result *types.CertificateBuildParams) {
				t.Helper()
				require.Equal(t, uint64(9), result.ToBlock)
				require.Len(t, result.Claims, 1)
			},
		},
		{
			name: "later invalid claim forces earlier dependent claim out of final range",
			buildParams: &types.CertificateBuildParams{
				FromBlock: 5,
				ToBlock:   10,
				Claims: []claimsynctypes.Claim{
					{BlockNum: 6, GlobalExitRoot: ger, GlobalIndex: big.NewInt(1)},
					{BlockNum: 8, GlobalExitRoot: ger, GlobalIndex: big.NewInt(2)},
				},
				Unclaims: []claimsynctypes.Unclaim{
					{BlockNumber: 10, GlobalIndex: big.NewInt(1)},
				},
			},
			mockFn: func(mockQuerier *mocks.L1InfoTreeDataQuerier) {
				mockQuerier.EXPECT().DoesGERExistsOnL1(ger).Return(false, nil).Once()
			},
			checkResult: func(t *testing.T, result *types.CertificateBuildParams) {
				t.Helper()
				require.Equal(t, uint64(5), result.ToBlock)
				require.Empty(t, result.Claims)
				require.Empty(t, result.Unclaims)
			},
		},
		{
			name: "same block later unclaim keeps claim",
			buildParams: &types.CertificateBuildParams{
				FromBlock: 5,
				ToBlock:   8,
				Claims: []claimsynctypes.Claim{
					{BlockNum: 7, BlockPos: 10, GlobalExitRoot: ger, GlobalIndex: globalIndex},
				},
				Unclaims: []claimsynctypes.Unclaim{
					{BlockNumber: 7, LogIndex: 11, GlobalIndex: globalIndex},
				},
			},
			mockFn: func(mockQuerier *mocks.L1InfoTreeDataQuerier) {
				mockQuerier.EXPECT().DoesGERExistsOnL1(ger).Return(false, nil).Once()
			},
			checkResult: func(t *testing.T, result *types.CertificateBuildParams) {
				t.Helper()
				require.Equal(t, uint64(8), result.ToBlock)
				require.Len(t, result.Claims, 1)
			},
		},
		{
			name: "same block earlier unclaim is not posterior",
			buildParams: &types.CertificateBuildParams{
				FromBlock: 5,
				ToBlock:   8,
				Claims: []claimsynctypes.Claim{
					{BlockNum: 7, BlockPos: 10, GlobalExitRoot: ger, GlobalIndex: globalIndex},
				},
				Unclaims: []claimsynctypes.Unclaim{
					{BlockNumber: 7, LogIndex: 9, GlobalIndex: globalIndex},
				},
			},
			mockFn: func(mockQuerier *mocks.L1InfoTreeDataQuerier) {
				mockQuerier.EXPECT().DoesGERExistsOnL1(ger).Return(false, nil).Once()
			},
			checkResult: func(t *testing.T, result *types.CertificateBuildParams) {
				t.Helper()
				require.Equal(t, uint64(6), result.ToBlock)
				require.Empty(t, result.Claims)
			},
		},
		{
			name: "same block same index is not posterior",
			buildParams: &types.CertificateBuildParams{
				FromBlock: 5,
				ToBlock:   8,
				Claims: []claimsynctypes.Claim{
					{BlockNum: 7, BlockPos: 10, GlobalExitRoot: ger, GlobalIndex: globalIndex},
				},
				Unclaims: []claimsynctypes.Unclaim{
					{BlockNumber: 7, LogIndex: 10, GlobalIndex: globalIndex},
				},
			},
			mockFn: func(mockQuerier *mocks.L1InfoTreeDataQuerier) {
				mockQuerier.EXPECT().DoesGERExistsOnL1(ger).Return(false, nil).Once()
			},
			checkResult: func(t *testing.T, result *types.CertificateBuildParams) {
				t.Helper()
				require.Equal(t, uint64(6), result.ToBlock)
				require.Empty(t, result.Claims)
			},
		},
		{
			name: "invalid claim at start block returns error",
			buildParams: &types.CertificateBuildParams{
				FromBlock: 5,
				ToBlock:   9,
				Claims:    []claimsynctypes.Claim{{BlockNum: 5, GlobalExitRoot: ger, GlobalIndex: globalIndex}},
			},
			mockFn: func(mockQuerier *mocks.L1InfoTreeDataQuerier) {
				mockQuerier.EXPECT().DoesGERExistsOnL1(ger).Return(false, nil).Once()
			},
			errorContains: "invalid claim at block 5",
		},
		{
			name: "invalid claim trims certificate",
			buildParams: &types.CertificateBuildParams{
				FromBlock: 5,
				ToBlock:   9,
				Claims: []claimsynctypes.Claim{
					{BlockNum: 6, GlobalExitRoot: ger, GlobalIndex: globalIndex},
					{BlockNum: 8, GlobalExitRoot: ger, GlobalIndex: big.NewInt(8)},
				},
			},
			mockFn: func(mockQuerier *mocks.L1InfoTreeDataQuerier) {
				mockQuerier.EXPECT().DoesGERExistsOnL1(ger).Return(false, nil).Once()
			},
			checkResult: func(t *testing.T, result *types.CertificateBuildParams) {
				t.Helper()
				require.Equal(t, uint64(5), result.ToBlock)
				require.Empty(t, result.Claims)
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockQuerier := mocks.NewL1InfoTreeDataQuerier(t)
			tc.mockFn(mockQuerier)

			f := &baseFlow{
				l1InfoTreeDataQuerier: mockQuerier,
				log:                   log.WithFields("test", t.Name()),
			}

			result, err := f.adjustInvalidClaimsAreNotUnclaimed(tc.buildParams, newGERValidationCache())
			if tc.errorContains != "" {
				require.ErrorContains(t, err, tc.errorContains)
				require.Nil(t, result)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, result)
			tc.checkResult(t, result)
		})
	}
}

func Test_claimHasPosteriorUnclaim(t *testing.T) {
	t.Parallel()

	globalIndex := big.NewInt(42)

	t.Run("handles nil claim global index", func(t *testing.T) {
		t.Parallel()

		require.False(t, claimHasPosteriorUnclaim(
			claimsynctypes.Claim{BlockNum: 5},
			[]claimsynctypes.Unclaim{{BlockNumber: 6, GlobalIndex: globalIndex}},
			[]bool{false},
		))
	})

	t.Run("requires later matching unclaim and marks it used", func(t *testing.T) {
		t.Parallel()

		usedUnclaims := []bool{false, false, true}
		unclaims := []claimsynctypes.Unclaim{
			{BlockNumber: 5, GlobalIndex: globalIndex},
			{BlockNumber: 7, GlobalIndex: globalIndex},
			{BlockNumber: 8, GlobalIndex: big.NewInt(99)},
		}

		ok := claimHasPosteriorUnclaim(
			claimsynctypes.Claim{BlockNum: 6, GlobalIndex: globalIndex},
			unclaims,
			usedUnclaims,
		)

		require.True(t, ok)
		require.Equal(t, []bool{false, true, true}, usedUnclaims)
	})

	t.Run("uses same block ordering via intra block index", func(t *testing.T) {
		t.Parallel()

		usedUnclaims := []bool{false, false, false}
		unclaims := []claimsynctypes.Unclaim{
			{BlockNumber: 6, LogIndex: 1, GlobalIndex: globalIndex},
			{BlockNumber: 6, LogIndex: 2, GlobalIndex: globalIndex},
			{BlockNumber: 7, LogIndex: 0, GlobalIndex: globalIndex},
		}

		ok := claimHasPosteriorUnclaim(
			claimsynctypes.Claim{BlockNum: 6, BlockPos: 1, GlobalIndex: globalIndex},
			unclaims,
			usedUnclaims,
		)

		require.True(t, ok)
		require.Equal(t, []bool{false, true, false}, usedUnclaims)
	})

	t.Run("ignores nil or already used unclaims", func(t *testing.T) {
		t.Parallel()

		usedUnclaims := []bool{false, true}
		unclaims := []claimsynctypes.Unclaim{
			{BlockNumber: 8, GlobalIndex: nil},
			{BlockNumber: 9, GlobalIndex: globalIndex},
		}

		ok := claimHasPosteriorUnclaim(
			claimsynctypes.Claim{BlockNum: 6, GlobalIndex: globalIndex},
			unclaims,
			usedUnclaims,
		)

		require.False(t, ok)
		require.Equal(t, []bool{false, true}, usedUnclaims)
	})
}

func Test_trimCertificateToBlock(t *testing.T) {
	t.Parallel()

	buildParams := &types.CertificateBuildParams{
		FromBlock: 5,
		ToBlock:   10,
		Bridges:   []bridgesync.Bridge{{BlockNum: 5}, {BlockNum: 7}, {BlockNum: 10}},
		Claims:    []claimsynctypes.Claim{{BlockNum: 6}, {BlockNum: 10}},
		Unclaims:  []claimsynctypes.Unclaim{{BlockNumber: 6}, {BlockNumber: 9}},
	}

	t.Run("rejects larger end block", func(t *testing.T) {
		t.Parallel()

		result, err := trimCertificateToBlock(buildParams, 11)

		require.ErrorContains(t, err, "cannot adjust toBlock to a higher value")
		require.Nil(t, result)
	})

	t.Run("returns same params for same end block", func(t *testing.T) {
		t.Parallel()

		result, err := trimCertificateToBlock(buildParams, 10)

		require.NoError(t, err)
		require.Same(t, buildParams, result)
	})

	t.Run("rejects trim before start block", func(t *testing.T) {
		t.Parallel()

		result, err := trimCertificateToBlock(buildParams, 4)

		require.ErrorContains(t, err, "would move before start block 5")
		require.Nil(t, result)
	})

	t.Run("trims contents to new end block", func(t *testing.T) {
		t.Parallel()

		result, err := trimCertificateToBlock(buildParams, 7)

		require.NoError(t, err)
		require.Equal(t, uint64(7), result.ToBlock)
		require.Len(t, result.Bridges, 2)
		require.Len(t, result.Claims, 1)
		require.Len(t, result.Unclaims, 1)
	})
}

func Test_cloneCertificateBuildParamsWithRange(t *testing.T) {
	t.Parallel()

	buildParams := &types.CertificateBuildParams{
		FromBlock:                      5,
		ToBlock:                        10,
		Bridges:                        []bridgesync.Bridge{{BlockNum: 5}, {BlockNum: 8}, {BlockNum: 10}},
		Claims:                         []claimsynctypes.Claim{{BlockNum: 6}, {BlockNum: 10}},
		Unclaims:                       []claimsynctypes.Unclaim{{BlockNumber: 7}, {BlockNumber: 10}},
		CreatedAt:                      55,
		RetryCount:                     3,
		LastSentCertificate:            &types.CertificateHeader{Status: agglayertypes.InError},
		L1InfoTreeRootFromWhichToProve: common.HexToHash("0xabc"),
		L1InfoTreeLeafCount:            9,
		AggchainProof:                  &types.AggchainProof{EndBlock: 9},
		CertificateType:                types.CertificateTypeFEP,
		ExtraData:                      "extra",
	}

	t.Run("nil params", func(t *testing.T) {
		t.Parallel()

		result, err := cloneCertificateBuildParamsWithRange(nil, 1, 1)

		require.ErrorIs(t, err, ErrBuildParamsIsNil)
		require.Nil(t, result)
	})

	t.Run("range outside bounds", func(t *testing.T) {
		t.Parallel()

		result, err := cloneCertificateBuildParamsWithRange(buildParams, 4, 10)

		require.ErrorContains(t, err, "are not within the certificate range")
		require.Nil(t, result)
	})

	t.Run("invalid descending range", func(t *testing.T) {
		t.Parallel()

		result, err := cloneCertificateBuildParamsWithRange(buildParams, 8, 7)

		require.ErrorContains(t, err, "FromBlock 8 is greater than toBlock 7")
		require.Nil(t, result)
	})

	t.Run("same range returns original", func(t *testing.T) {
		t.Parallel()

		result, err := cloneCertificateBuildParamsWithRange(buildParams, 5, 10)

		require.NoError(t, err)
		require.Same(t, buildParams, result)
	})

	t.Run("clones requested subset", func(t *testing.T) {
		t.Parallel()

		result, err := cloneCertificateBuildParamsWithRange(buildParams, 6, 8)

		require.NoError(t, err)
		require.NotSame(t, buildParams, result)
		require.Equal(t, uint64(6), result.FromBlock)
		require.Equal(t, uint64(8), result.ToBlock)
		require.Len(t, result.Bridges, 1)
		require.Len(t, result.Claims, 1)
		require.Len(t, result.Unclaims, 1)
		require.Equal(t, uint32(55), result.CreatedAt)
		require.Equal(t, 3, result.RetryCount)
		require.Same(t, buildParams.LastSentCertificate, result.LastSentCertificate)
		require.Equal(t, common.HexToHash("0xabc"), result.L1InfoTreeRootFromWhichToProve)
		require.Equal(t, uint32(9), result.L1InfoTreeLeafCount)
		require.Same(t, buildParams.AggchainProof, result.AggchainProof)
		require.Equal(t, types.CertificateTypeFEP, result.CertificateType)
		require.Equal(t, "extra", result.ExtraData)
	})
}

func Test_isUpcomingNextRange(t *testing.T) {
	t.Parallel()

	require.False(t, isUpcomingNextRange(0, 1, 5))
	require.False(t, isUpcomingNextRange(5, 7, 7))
	require.True(t, isUpcomingNextRange(5, 6, 9))
}

func Test_baseFlow_adjustClaimsNotProvableAgainstRoot_IgnoresMissingGERNotOnL1(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	finalizedRoot := common.HexToHash("0x123")
	gerMissing := common.HexToHash("0x2")

	mockQuerier := mocks.NewL1InfoTreeDataQuerier(t)
	mockQuerier.EXPECT().GetProofForGER(ctx, gerMissing, finalizedRoot).
		Return(nil, treetypes.Proof{}, errors.New("not provable")).Once()
	mockQuerier.EXPECT().DoesGERExistsOnL1(gerMissing).Return(false, nil).Once()

	f := &baseFlow{
		l1InfoTreeDataQuerier: mockQuerier,
		log:                   log.WithFields("test", t.Name()),
	}
	buildParams := &types.CertificateBuildParams{
		FromBlock:                      5,
		ToBlock:                        12,
		L1InfoTreeRootFromWhichToProve: finalizedRoot,
		Claims: []claimsynctypes.Claim{
			{BlockNum: 8, GlobalExitRoot: gerMissing},
		},
	}

	result, err := f.adjustClaimsNotProvableAgainstRoot(ctx, buildParams, newGERValidationCache())

	require.NoError(t, err)
	require.Same(t, buildParams, result)
}

func Test_baseFlow_validateRootToProveIsFinalized_Success(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	finalizedRoot := common.HexToHash("0x123")
	mockQuerier := mocks.NewL1InfoTreeDataQuerier(t)
	mockQuerier.EXPECT().GetL1InfoRootByLeafIndex(ctx, uint32(10)).
		Return(&treetypes.Root{Hash: finalizedRoot, Index: 10}, nil).Once()
	mockQuerier.EXPECT().GetTargetL1InfoRoot(ctx).
		Return(&treetypes.Root{Hash: common.HexToHash("0x999"), Index: 20}, nil, nil).Once()

	f := &baseFlow{
		l1InfoTreeDataQuerier: mockQuerier,
		log:                   log.WithFields("test", t.Name()),
	}

	err := f.validateRootToProveIsFinalized(ctx, &types.CertificateBuildParams{
		L1InfoTreeLeafCount:            11,
		L1InfoTreeRootFromWhichToProve: finalizedRoot,
	})

	require.NoError(t, err)
}

func Test_baseFlow_adjustClaimsNotProvableAgainstRoot_FailsForExistingGERNotProvableAgainstRoot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	finalizedRoot := common.HexToHash("0x123")
	gerProvable := common.HexToHash("0x1")

	mockQuerier := mocks.NewL1InfoTreeDataQuerier(t)
	mockQuerier.EXPECT().GetProofForGER(ctx, gerProvable, finalizedRoot).
		Return(nil, treetypes.Proof{}, query.ErrGERNotProvableAgainstRoot).Once()
	mockQuerier.EXPECT().DoesGERExistsOnL1(gerProvable).Return(true, nil).Once()

	f := &baseFlow{
		l1InfoTreeDataQuerier: mockQuerier,
		log:                   log.WithFields("test", t.Name()),
	}
	buildParams := &types.CertificateBuildParams{
		FromBlock:                      5,
		ToBlock:                        12,
		L1InfoTreeRootFromWhichToProve: finalizedRoot,
		Claims: []claimsynctypes.Claim{
			{BlockNum: 9, GlobalExitRoot: gerProvable},
		},
	}

	result, err := f.adjustClaimsNotProvableAgainstRoot(ctx, buildParams, newGERValidationCache())

	require.ErrorContains(t, err, "exists on L1 but cannot be proved against selected root")
	require.Nil(t, result)
}

func Test_baseFlow_adjustClaimsNotProvableAgainstRoot_FailsHardForLookupErrorsOnL1GER(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	finalizedRoot := common.HexToHash("0x123")
	gerProvable := common.HexToHash("0x1")

	mockQuerier := mocks.NewL1InfoTreeDataQuerier(t)
	mockQuerier.EXPECT().GetProofForGER(ctx, gerProvable, finalizedRoot).
		Return(nil, treetypes.Proof{}, errors.New("syncer unavailable")).Once()
	mockQuerier.EXPECT().DoesGERExistsOnL1(gerProvable).Return(true, nil).Once()

	f := &baseFlow{
		l1InfoTreeDataQuerier: mockQuerier,
		log:                   log.WithFields("test", t.Name()),
	}
	buildParams := &types.CertificateBuildParams{
		FromBlock:                      5,
		ToBlock:                        12,
		L1InfoTreeRootFromWhichToProve: finalizedRoot,
		Claims: []claimsynctypes.Claim{
			{BlockNum: 9, GlobalExitRoot: gerProvable},
		},
	}

	result, err := f.adjustClaimsNotProvableAgainstRoot(ctx, buildParams, newGERValidationCache())

	require.ErrorContains(t, err, "proof lookup failed for GER")
	require.Nil(t, result)
}

func Test_baseFlow_adjustClaimsNotProvableAgainstRoot_UsesGERCache(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	finalizedRoot := common.HexToHash("0x123")
	gerMissing := common.HexToHash("0x2")

	mockQuerier := mocks.NewL1InfoTreeDataQuerier(t)
	mockQuerier.EXPECT().GetProofForGER(ctx, gerMissing, finalizedRoot).
		Return(nil, treetypes.Proof{}, errors.New("not provable")).Twice()
	mockQuerier.EXPECT().DoesGERExistsOnL1(gerMissing).Return(false, nil).Once()

	f := &baseFlow{
		l1InfoTreeDataQuerier: mockQuerier,
		log:                   log.WithFields("test", t.Name()),
	}
	buildParams := &types.CertificateBuildParams{
		FromBlock:                      5,
		ToBlock:                        12,
		L1InfoTreeRootFromWhichToProve: finalizedRoot,
		Claims: []claimsynctypes.Claim{
			{BlockNum: 8, GlobalExitRoot: gerMissing},
			{BlockNum: 9, GlobalExitRoot: gerMissing},
		},
	}

	result, err := f.adjustClaimsNotProvableAgainstRoot(ctx, buildParams, newGERValidationCache())

	require.NoError(t, err)
	require.Same(t, buildParams, result)
}

func Test_baseFlow_AdjustBlockRange_DisableSizeLimit(t *testing.T) {
	t.Parallel()

	f := &baseFlow{
		cfg: BaseFlowConfig{MaxCertSize: 1},
		log: log.WithFields("test", t.Name()),
	}
	buildParams := &types.CertificateBuildParams{
		FromBlock:       1,
		ToBlock:         3,
		CertificateType: types.CertificateTypePP,
		Bridges:         []bridgesync.Bridge{{BlockNum: 1}, {BlockNum: 2}, {BlockNum: 3}},
	}

	result, err := f.AdjustBlockRange(context.Background(), buildParams, types.BlockRangeAdjustmentOptions{
		DisableSizeLimit: true,
	})

	require.NoError(t, err)
	require.Same(t, buildParams, result)
}

func Test_baseFlow_AdjustBlockRange_RevalidatesMissingGERClaimsAfterRangeTrim(t *testing.T) {
	t.Parallel()

	ger := common.HexToHash("0x77")
	mockQuerier := mocks.NewL1InfoTreeDataQuerier(t)
	mockQuerier.EXPECT().GetProofForGER(context.Background(), ger, common.Hash{}).
		Return(nil, treetypes.Proof{}, errors.New("missing")).Once()
	mockQuerier.EXPECT().DoesGERExistsOnL1(ger).Return(false, nil).Once()

	f := &baseFlow{
		l1InfoTreeDataQuerier: mockQuerier,
		log:                   log.WithFields("test", t.Name()),
	}
	buildParams := &types.CertificateBuildParams{
		FromBlock: 5,
		ToBlock:   10,
		Claims: []claimsynctypes.Claim{
			{BlockNum: 6, GlobalExitRoot: ger, GlobalIndex: big.NewInt(1)},
		},
		Unclaims: []claimsynctypes.Unclaim{
			{BlockNumber: 9, GlobalIndex: big.NewInt(1)},
		},
	}

	result, err := f.AdjustBlockRange(context.Background(), buildParams, types.BlockRangeAdjustmentOptions{
		MaxL2BlockNumber: 8,
	})

	require.NoError(t, err)
	require.Equal(t, uint64(5), result.ToBlock)
	require.Empty(t, result.Claims)
	require.Empty(t, result.Unclaims)
}
