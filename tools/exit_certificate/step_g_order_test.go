package exit_certificate

import (
	"math/big"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/tools/exit_certificate/bridgesyncerlite"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func erc20Exit(originNet uint32, originAddr, dest common.Address, amount int64) *agglayertypes.BridgeExit {
	return &agglayertypes.BridgeExit{
		TokenInfo:          &agglayertypes.TokenInfo{OriginNetwork: originNet, OriginTokenAddress: originAddr},
		DestinationNetwork: 0,
		DestinationAddress: dest,
		Amount:             big.NewInt(amount),
	}
}

func nativeExit(dest common.Address, amount int64) *agglayertypes.BridgeExit {
	return &agglayertypes.BridgeExit{
		TokenInfo:          nil,
		DestinationNetwork: 0,
		DestinationAddress: dest,
		Amount:             big.NewInt(amount),
	}
}

// leafWithDepositCount builds a replayed BridgeLeaf carrying the given deposit count, as it would be
// captured from the replay of the matching certificate exit.
func leafWithDepositCount(depositCount uint32) bridgesyncerlite.BridgeLeaf {
	return bridgesyncerlite.BridgeLeaf{DepositCount: depositCount}
}

func TestDepositOrderedExits(t *testing.T) {
	t.Parallel()

	tokenA := common.HexToAddress("0xaaaa")
	destA := common.HexToAddress("0x1111")
	destB := common.HexToAddress("0x2222")
	destC := common.HexToAddress("0x3333")

	// Certificate order: [A, B, C], generated metadata tagged by original index.
	exits := []*agglayertypes.BridgeExit{
		erc20Exit(1, tokenA, destA, 100),
		nativeExit(destB, 200),
		erc20Exit(1, tokenA, destC, 300),
	}
	generatedMetadata := [][]byte{{0xA}, {0xB}, {0xC}}

	// Replay assigned deposit counts C(0), A(1), B(2) — leaves are indexed by the original exit
	// position, so leaves[0] is A's, leaves[1] is B's, leaves[2] is C's.
	leaves := []bridgesyncerlite.BridgeLeaf{
		leafWithDepositCount(1),
		leafWithDepositCount(2),
		leafWithDepositCount(0),
	}

	orderedExits, orderedMeta, err := depositOrderedExits(exits, generatedMetadata, leaves)
	require.NoError(t, err)

	// The sorted copy follows the replay deposit order and keeps the metadata aligned to it.
	require.Equal(t, destC, orderedExits[0].DestinationAddress)
	require.Equal(t, destA, orderedExits[1].DestinationAddress)
	require.Equal(t, destB, orderedExits[2].DestinationAddress)
	require.Equal(t, [][]byte{{0xC}, {0xA}, {0xB}}, orderedMeta)

	// The input slices are untouched: the certificate keeps its deterministic order.
	require.Equal(t, destA, exits[0].DestinationAddress)
	require.Equal(t, destB, exits[1].DestinationAddress)
	require.Equal(t, destC, exits[2].DestinationAddress)
	require.Equal(t, [][]byte{{0xA}, {0xB}, {0xC}}, generatedMetadata)
}

func TestDepositOrderedExitsCountMismatch(t *testing.T) {
	t.Parallel()

	exits := []*agglayertypes.BridgeExit{nativeExit(common.HexToAddress("0x1111"), 100)}

	_, _, err := depositOrderedExits(exits, [][]byte{{0x1}}, nil)
	require.ErrorContains(t, err, "!= certificate bridge exit count")

	_, _, err = depositOrderedExits(exits, nil, []bridgesyncerlite.BridgeLeaf{leafWithDepositCount(0)})
	require.ErrorContains(t, err, "generated metadata count")
}
