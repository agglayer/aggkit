package domain

import (
	"math/big"
	"math/rand/v2"
	"slices"
	"strconv"
	"testing"
	"time"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/stretchr/testify/require"
)

// TestGlobalIndexSortKeyOrdersNumerically verifies ordering the sort keys as text is ordering the
// global indexes numerically, above 2^64 and with leading zeros included, and that a value that is
// not a non-negative decimal sorts as 0 — as the big.Int comparator it replaces read it
func TestGlobalIndexSortKeyOrdersNumerically(t *testing.T) {
	values := []string{
		"0", "7", "007", "9", "10", "4294967296", "18446744073709551615", "18446744073709551616",
		"36893488147419103231", "+5",
	}
	for _, a := range values {
		for _, b := range values {
			na, _ := new(big.Int).SetString(a, 10)
			nb, _ := new(big.Int).SetString(b, 10)
			ka := GlobalIndexSortKey(bridgeservicetypes.BigIntString(a))
			kb := GlobalIndexSortKey(bridgeservicetypes.BigIntString(b))
			require.Len(t, ka, GlobalIndexSortWidth)
			require.Equal(t, sign(na.Cmp(nb)), sign(compareStrings(ka, kb)), "%s vs %s", a, b)
			require.Equal(t, sign(na.Cmp(nb)),
				sign(compareGlobalIndexes(bridgeservicetypes.BigIntString(a), bridgeservicetypes.BigIntString(b))))
		}
	}
	zero := GlobalIndexSortKey("0")
	for _, invalid := range []string{"", "abc", "-5", "0x10"} {
		require.Equal(t, zero, GlobalIndexSortKey(bridgeservicetypes.BigIntString(invalid)), invalid)
	}
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func sign(c int) int {
	switch {
	case c < 0:
		return -1
	case c > 0:
		return 1
	}
	return 0
}

// TestActivityPageOffset verifies the offset is computed without overflow
func TestActivityPageOffset(t *testing.T) {
	require.Equal(t, uint64(0), ActivityPage{PageNumber: 1, PageSize: 20}.Offset())
	require.Equal(t, uint64(40), ActivityPage{PageNumber: 3, PageSize: 20}.Offset())
	require.Equal(t, uint64(0), ActivityPage{}.Offset())
	require.Equal(t, uint64(1<<32-2)*200, ActivityPage{PageNumber: 1<<32 - 1, PageSize: 200}.Offset())
}

// TestSelectActivityPageMatchesAFullSort verifies the bounded selection serves exactly the page a
// full sort would, for every page including a partial last one and one past the end (empty, with
// the real total), on entries with tied CreatedAt, tied global indexes and global indexes above 2^64
func TestSelectActivityPageMatchesAFullSort(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2)) // deterministic test data
	base := time.Unix(1_700_000_000, 0)
	mainnet := new(big.Int).Lsh(big.NewInt(1), 64)
	entries := make([]*ActivityEntry, 0, 500)
	for i := range 500 {
		gi := big.NewInt(int64(rng.IntN(40)))
		if rng.IntN(2) == 0 {
			gi.Add(gi, mainnet)
		}
		entries = append(entries, &ActivityEntry{
			Bridge: &bridgeservicetypes.BridgeResponse{
				GlobalIndex: bridgeservicetypes.BigIntString(gi.String()), DepositCount: uint32(i),
			},
			BridgeNetworkID: uint32(rng.IntN(3)),
			CreatedAt:       base.Add(time.Duration(rng.IntN(10)) * time.Second),
		})
	}
	sorted := slices.Clone(entries)
	slices.SortFunc(sorted, CompareActivityEntries)
	for i := 1; i < len(sorted); i++ {
		require.Negative(t, CompareActivityEntries(sorted[i-1], sorted[i]), "the order is total")
	}

	for _, size := range []uint32{1, 7, 20, 200} {
		for number := uint32(1); uint64(number-1)*uint64(size) <= uint64(len(entries)); number++ {
			page := ActivityPage{PageNumber: number, PageSize: size}
			got, total := SelectActivityPage(slices.Clone(entries), page)
			require.Equal(t, len(entries), total)
			start := min(page.Offset(), uint64(len(sorted)))
			end := min(start+uint64(size), uint64(len(sorted)))
			require.Equal(t, sorted[start:end], got, "page %d of %d", number, size)
		}
	}
	got, total := SelectActivityPage(entries, ActivityPage{PageNumber: 1000, PageSize: 200})
	require.NotNil(t, got)
	require.Empty(t, got)
	require.Equal(t, len(entries), total)
}

// TestCompareActivityEntriesOrder pins the order: CreatedAt descending, then global index
// descending numerically, then bridge key descending
func TestCompareActivityEntriesOrder(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	entry := func(gi string, network uint32, createdAt time.Time) *ActivityEntry {
		return &ActivityEntry{
			Bridge:          &bridgeservicetypes.BridgeResponse{GlobalIndex: bridgeservicetypes.BigIntString(gi)},
			BridgeNetworkID: network, CreatedAt: createdAt,
		}
	}
	ordered := []*ActivityEntry{
		entry("1", 0, at.Add(time.Second)),
		entry("18446744073709551616", 0, at),
		entry("10", 1, at),
		entry("10", 0, at), // the legacy collision: same CreatedAt and global index, lower key
		entry("9", 0, at),
	}
	shuffled := slices.Clone(ordered)
	slices.Reverse(shuffled)
	slices.SortFunc(shuffled, CompareActivityEntries)
	require.Equal(t, ordered, shuffled)
	for i, e := range ordered {
		require.Zero(t, CompareActivityEntries(e, e), strconv.Itoa(i))
	}
}
