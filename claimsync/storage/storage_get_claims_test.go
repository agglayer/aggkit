package storage

import (
	"context"
	"fmt"
	"math/big"
	"testing"

	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// getClaimsAllRankedSQL is the claims query that ranked every claim of the table
// instead of only the global indexes present in the requested range
const getClaimsAllRankedSQL = `
	WITH all_claims_ranked AS (
		SELECT
			*,
			ROW_NUMBER() OVER (PARTITION BY global_index ORDER BY block_num ASC, block_pos ASC) AS rn_oldest_global,
			ROW_NUMBER() OVER (PARTITION BY global_index ORDER BY block_num DESC, block_pos DESC) AS rn_newest_global
		FROM claim
	),
	claims_in_range AS (
		SELECT * FROM all_claims_ranked WHERE block_num >= $1 AND block_num <= $2
	),
	claims_with_unset AS (
		SELECT c.%s
		FROM claims_in_range c
		WHERE EXISTS (SELECT 1 FROM unset_claim uc WHERE uc.global_index = c.global_index)
	),
	compactable_claims AS (
		SELECT %s
		FROM claims_in_range o
		JOIN claims_in_range n ON o.global_index = n.global_index AND n.rn_newest_global = 1
		WHERE o.rn_oldest_global = 1
		AND NOT EXISTS (SELECT 1 FROM unset_claim uc WHERE uc.global_index = o.global_index)
	)
	SELECT * FROM claims_with_unset
	UNION ALL
	SELECT * FROM compactable_claims
	ORDER BY block_num ASC, block_pos ASC;
	`

// seedClaimsFixture stores claims with global indexes that are claimed inside and outside of several
// ranges, plus global indexes that have an unset claim, and returns how many claims were inserted
func seedClaimsFixture(t *testing.T, ctx context.Context, s claimsynctypes.ClaimStorager) int {
	t.Helper()

	type entry struct {
		block, pos  uint64
		globalIndex int64
	}
	entries := []entry{
		{1, 0, 100}, {2, 0, 100}, {3, 1, 100}, // claimed before, inside and after most ranges
		{2, 1, 200}, {5, 0, 200}, // one inside, one outside
		{2, 2, 300}, {3, 0, 300}, // only inside [2,3]
		{6, 0, 400},                           // only outside
		{2, 3, 500}, {4, 0, 500}, {5, 1, 500}, // has an unset claim: returned uncompacted
		{3, 2, 600}, {3, 3, 600}, // same block, unset claim
		{7, 0, 700},
	}
	blocks := map[uint64]bool{}
	for i, e := range entries {
		if !blocks[e.block] {
			require.NoError(t, s.InsertBlock(ctx, nil, e.block, common.BigToHash(big.NewInt(int64(e.block)))))
			blocks[e.block] = true
		}
		require.NoError(t, s.InsertClaim(ctx, nil, claimsynctypes.Claim{
			BlockNum:       e.block,
			BlockPos:       e.pos,
			TxHash:         common.BigToHash(big.NewInt(int64(i + 1))),
			GlobalIndex:    big.NewInt(e.globalIndex),
			Amount:         big.NewInt(int64(i + 1)),
			Metadata:       []byte(fmt.Sprintf("metadata-%d", i)),
			GlobalExitRoot: common.BigToHash(big.NewInt(int64(1000 + i))),
			Type:           claimsynctypes.ClaimEvent,
		}))
	}
	// unset claims are stored on blocks that may be outside the claimed ranges
	for _, u := range []struct {
		block       uint64
		globalIndex int64
	}{{8, 500}, {4, 600}} {
		if !blocks[u.block] {
			require.NoError(t, s.InsertBlock(ctx, nil, u.block, common.BigToHash(big.NewInt(int64(u.block)))))
			blocks[u.block] = true
		}
		require.NoError(t, s.InsertUnsetClaim(ctx, nil, claimsynctypes.UnsetClaim{
			BlockNum:    u.block,
			TxHash:      common.BigToHash(big.NewInt(int64(5000 + u.block))),
			GlobalIndex: big.NewInt(u.globalIndex),
		}))
	}
	return len(entries)
}

func TestGetClaims_SameResultsAsRankingTheWholeTable(t *testing.T) {
	s, rawDB := newTestStorage(t)
	ctx := context.Background()
	seedClaimsFixture(t, ctx, s)

	legacyQuery := fmt.Sprintf(getClaimsAllRankedSQL, claimColumnsSQL, compactedClaimsSelectSQL)
	nonEmptyRanges := 0
	for from := uint64(0); from <= 9; from++ {
		for to := from; to <= 9; to++ {
			rows, err := rawDB.QueryContext(ctx, legacyQuery, from, to)
			require.NoError(t, err)
			expected, err := scanClaims(rows)
			require.NoError(t, err)
			require.NoError(t, rows.Close())

			got, err := s.GetClaims(ctx, nil, from, to)
			require.NoError(t, err)
			require.Equal(t, expected, got, "range [%d, %d]", from, to)
			if len(got) > 0 {
				nonEmptyRanges++
			}
		}
	}
	require.NotZero(t, nonEmptyRanges)
}

func TestGetClaims_FixtureContent(t *testing.T) {
	s, _ := newTestStorage(t)
	ctx := context.Background()
	seedClaimsFixture(t, ctx, s)

	// [2,3]: gi=100 and gi=200 have claims outside the range and none of them is the compacted one;
	// gi=300 is compacted into its oldest claim; gi=500 and gi=600 have an unset claim, so the claims
	// they have in the range are returned as stored
	got, err := s.GetClaims(ctx, nil, 2, 3)
	require.NoError(t, err)
	type key struct{ block, pos uint64 }
	gotKeys := []key{}
	for _, c := range got {
		gotKeys = append(gotKeys, key{c.BlockNum, c.BlockPos})
	}
	require.Equal(t, []key{{2, 2}, {2, 3}, {3, 2}, {3, 3}}, gotKeys)
}

func TestGetClaimsWithLimit(t *testing.T) {
	s, _ := newTestStorage(t)
	ctx := context.Background()
	seedClaimsFixture(t, ctx, s)

	all, err := s.GetClaims(ctx, nil, 0, 9)
	require.NoError(t, err)
	require.Greater(t, len(all), 3)

	t.Run("zero means no limit", func(t *testing.T) {
		got, err := s.GetClaimsWithLimit(ctx, nil, 0, 9, 0)
		require.NoError(t, err)
		require.Equal(t, all, got)
	})
	t.Run("limit below the result size returns the oldest claims", func(t *testing.T) {
		got, err := s.GetClaimsWithLimit(ctx, nil, 0, 9, 3)
		require.NoError(t, err)
		require.Equal(t, all[:3], got)
	})
	t.Run("limit equal to the result size", func(t *testing.T) {
		got, err := s.GetClaimsWithLimit(ctx, nil, 0, 9, uint64(len(all)))
		require.NoError(t, err)
		require.Equal(t, all, got)
	})
	t.Run("limit above the result size", func(t *testing.T) {
		got, err := s.GetClaimsWithLimit(ctx, nil, 0, 9, uint64(len(all))+10)
		require.NoError(t, err)
		require.Equal(t, all, got)
	})
}
