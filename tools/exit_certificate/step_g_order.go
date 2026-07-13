package exit_certificate

import (
	"fmt"
	"math/big"
	"sort"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/tools/exit_certificate/bridgesyncerlite"
)

func bigIntKey(v *big.Int) string {
	if v == nil {
		return "0"
	}
	return v.String()
}

// depositOrderedExits returns copies of the certificate exits and their generated metadata sorted
// by the replay's on-chain deposit count, leaving the certificate itself untouched. leaves[i] is
// the BridgeEvent the replay of exits[i] emitted; its DepositCount is the on-chain leaf index. The
// parallel replay assigns deposit counts non-deterministically across exits, so the sorted copy
// reflects the order the forked contract inserted the leaves in — the order needed to rebuild the
// contract's getRoot() and cross-check the off-chain leaf encoding against it. The certificate
// keeps its own (deterministic) order; its NewLocalExitRoot is computed from that order instead.
// Because each exit maps directly to one replayed leaf by index, no content matching is needed and
// duplicate exits are handled trivially.
func depositOrderedExits(
	exits []*agglayertypes.BridgeExit, generatedMetadata [][]byte, leaves []bridgesyncerlite.BridgeLeaf,
) ([]*agglayertypes.BridgeExit, [][]byte, error) {
	if len(leaves) != len(exits) {
		return nil, nil, fmt.Errorf("replayed leaf count %d != certificate bridge exit count %d",
			len(leaves), len(exits))
	}
	if len(generatedMetadata) != len(exits) {
		return nil, nil, fmt.Errorf("generated metadata count %d != certificate bridge exit count %d",
			len(generatedMetadata), len(exits))
	}

	order := make([]int, len(exits))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool {
		return leaves[order[a]].DepositCount < leaves[order[b]].DepositCount
	})

	orderedExits := make([]*agglayertypes.BridgeExit, len(exits))
	orderedMetadata := make([][]byte, len(exits))
	for pos, idx := range order {
		orderedExits[pos] = exits[idx]
		orderedMetadata[pos] = generatedMetadata[idx]
	}
	return orderedExits, orderedMetadata, nil
}
