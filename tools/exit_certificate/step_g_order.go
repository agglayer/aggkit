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

// reorderCertificateByDepositCount reorders certificate.BridgeExits to the canonical exit-tree order
// and returns the replay's on-chain metadata aligned to the reordered exits, so the caller can
// cross-check it against each exit's own metadata. leaves[i] is the BridgeEvent the replay of
// certificate.BridgeExits[i] emitted; its DepositCount is the on-chain leaf index. The parallel
// replay assigns deposit counts non-deterministically across exits, so the exits must be sorted by
// that count for the certificate to be consistent with the computed NewLocalExitRoot (agglayer
// rebuilds the LER by inserting the bridge exits in order). Because each exit maps directly to one
// replayed leaf by index, no content matching is needed and duplicate exits are handled trivially.
func reorderCertificateByDepositCount(
	certificate *agglayertypes.Certificate, leaves []bridgesyncerlite.BridgeLeaf,
) ([][]byte, error) {
	exits := certificate.BridgeExits
	if len(leaves) != len(exits) {
		return nil, fmt.Errorf("replayed leaf count %d != certificate bridge exit count %d",
			len(leaves), len(exits))
	}

	order := make([]int, len(exits))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool {
		return leaves[order[a]].DepositCount < leaves[order[b]].DepositCount
	})

	newExits := make([]*agglayertypes.BridgeExit, len(exits))
	onChainMetadata := make([][]byte, len(exits))
	for pos, idx := range order {
		newExits[pos] = exits[idx]
		onChainMetadata[pos] = leaves[idx].Metadata
	}

	certificate.BridgeExits = newExits
	return onChainMetadata, nil
}
