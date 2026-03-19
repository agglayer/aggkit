package claimsync

import (
	"math/big"
	"testing"

	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// --- Constants ---

func TestClaimTypeConstants(t *testing.T) {
	t.Parallel()
	require.Equal(t, ClaimType("ClaimEvent"), ClaimEvent)
	require.Equal(t, ClaimType("DetailedClaimEvent"), DetailedClaimEvent)
	require.NotEqual(t, ClaimEvent, DetailedClaimEvent)
}

func TestClaimTypeConstants_MatchUnderlyingPackage(t *testing.T) {
	t.Parallel()
	require.Equal(t, claimsynctypes.ClaimEvent, ClaimEvent)
	require.Equal(t, claimsynctypes.DetailedClaimEvent, DetailedClaimEvent)
}

// --- Type aliases ---
// These tests verify that the aliases are truly interchangeable with their
// underlying types by passing claimsynctypes values to functions that accept
// the alias types — a compile error here would mean the alias is broken.

func requireClaim(t *testing.T, c Claim, blockNum uint64, claimType ClaimType) {
	t.Helper()
	require.Equal(t, blockNum, c.BlockNum)
	require.Equal(t, claimType, c.Type)
}

func requireUnsetClaim(t *testing.T, u UnsetClaim, blockNum uint64) {
	t.Helper()
	require.Equal(t, blockNum, u.BlockNum)
}

func requireSetClaim(t *testing.T, s SetClaim, blockNum uint64) {
	t.Helper()
	require.Equal(t, blockNum, s.BlockNum)
}

func TestClaimAlias_AssignableFromUnderlying(t *testing.T) {
	t.Parallel()
	c := claimsynctypes.Claim{
		BlockNum:    1,
		GlobalIndex: big.NewInt(42),
		TxHash:      common.HexToHash("0xdeadbeef"),
		Type:        claimsynctypes.ClaimEvent,
	}
	// passing claimsynctypes.Claim where Claim is expected proves alias identity
	requireClaim(t, c, 1, ClaimEvent)
}

func TestUnsetClaimAlias_AssignableFromUnderlying(t *testing.T) {
	t.Parallel()
	u := claimsynctypes.UnsetClaim{
		BlockNum:    2,
		GlobalIndex: big.NewInt(10),
		TxHash:      common.HexToHash("0xabc"),
	}
	requireUnsetClaim(t, u, 2)
}

func TestSetClaimAlias_AssignableFromUnderlying(t *testing.T) {
	t.Parallel()
	s := claimsynctypes.SetClaim{
		BlockNum:    3,
		GlobalIndex: big.NewInt(20),
		TxHash:      common.HexToHash("0x123"),
	}
	requireSetClaim(t, s, 3)
}
