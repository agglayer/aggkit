package types

import (
	"math/big"
	"testing"

	"github.com/agglayer/aggkit/bridgesync"
	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	"github.com/stretchr/testify/require"
)

func TestCertificateBuildParamsHelpers(t *testing.T) {
	t.Parallel()

	t.Run("nil params", func(t *testing.T) {
		t.Parallel()

		var params *CertificateBuildParams

		require.Equal(t, 0, params.NumberOfBridges())
		require.Equal(t, 0, params.NumberOfClaims())
		require.Equal(t, 0, params.NumberOfUnclaims())
		require.Equal(t, uint(0), params.EstimatedSize())
		require.True(t, params.IsEmpty())
		require.False(t, params.IsARetry())
		require.Equal(t, uint32(0), params.MaxDepositCount())
	})

	t.Run("non empty pessimistic certificate", func(t *testing.T) {
		t.Parallel()

		params := &CertificateBuildParams{
			Bridges:         []bridgesync.Bridge{{DepositCount: 1}, {DepositCount: 3}},
			Claims:          []claimsynctypes.Claim{{}, {}},
			Unclaims:        []claimsynctypes.Unclaim{{}},
			CertificateType: CertificateTypePP,
		}

		require.Equal(t, 2, params.NumberOfBridges())
		require.Equal(t, 2, params.NumberOfClaims())
		require.Equal(t, 1, params.NumberOfUnclaims())
		require.False(t, params.IsEmpty())
		require.False(t, params.IsARetry())
		require.Equal(t, uint32(3), params.MaxDepositCount())
		require.NotZero(t, params.EstimatedSize())
	})

	t.Run("retry requires previous certificate", func(t *testing.T) {
		t.Parallel()

		params := &CertificateBuildParams{RetryCount: 2}
		require.False(t, params.IsARetry())

		params.LastSentCertificate = &CertificateHeader{}
		require.True(t, params.IsARetry())
	})

	t.Run("fep certificate accounts for proof growth", func(t *testing.T) {
		t.Parallel()

		ppParams := &CertificateBuildParams{
			Claims:          []claimsynctypes.Claim{{}, {}},
			CertificateType: CertificateTypePP,
		}
		fepParams := &CertificateBuildParams{
			Claims:          []claimsynctypes.Claim{{}, {}},
			CertificateType: CertificateTypeFEP,
		}

		require.Greater(t, fepParams.EstimatedSize(), ppParams.EstimatedSize())
	})
}

func TestGetClaimsFilteringUnclaims_NoUnclaimsReturnsCopy(t *testing.T) {
	t.Parallel()

	globalIndex1 := bridgesync.GenerateGlobalIndex(true, 1, 1)
	globalIndex2 := bridgesync.GenerateGlobalIndex(true, 1, 2)
	params := &CertificateBuildParams{
		Claims: []claimsynctypes.Claim{
			{GlobalIndex: globalIndex1, BlockNum: 10},
			{GlobalIndex: globalIndex2, BlockNum: 11},
		},
	}

	filtered := params.GetClaimsFilteringUnclaims()

	require.Equal(t, params.Claims, filtered)
	filtered[0].BlockNum = 99
	require.Equal(t, uint64(10), params.Claims[0].BlockNum)
}

func TestGetClaimsFilteringUnclaims_IgnoresNilGlobalIndexes(t *testing.T) {
	t.Parallel()

	matchingIndex := big.NewInt(7)
	otherIndex := big.NewInt(8)
	params := &CertificateBuildParams{
		Claims: []claimsynctypes.Claim{
			{GlobalIndex: nil, BlockNum: 10},
			{GlobalIndex: matchingIndex, BlockNum: 11},
			{GlobalIndex: otherIndex, BlockNum: 12},
		},
		Unclaims: []claimsynctypes.Unclaim{
			{GlobalIndex: nil, BlockNumber: 20},
			{GlobalIndex: matchingIndex, BlockNumber: 21},
		},
	}

	require.NotPanics(t, func() {
		filtered := params.GetClaimsFilteringUnclaims()

		require.Len(t, filtered, 2)
		require.Nil(t, filtered[0].GlobalIndex)
		require.Equal(t, uint64(10), filtered[0].BlockNum)
		require.Equal(t, otherIndex, filtered[1].GlobalIndex)
		require.Equal(t, uint64(12), filtered[1].BlockNum)
	})
}
