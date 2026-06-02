package exit_certificate

import (
	"context"
	"fmt"
	"math/big"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
)

// Deposit-order recovery mechanisms (selected via Options.DepositOrderSource). After the parallel
// replay the certificate's bridge exits must be reordered to match the actual exit-tree deposit
// order; these are the two interchangeable ways to recover that order from the Anvil shadow-fork.
const (
	// DepositOrderEvents reads BridgeEvent logs directly from the shadow-fork (only the replayed
	// blocks). Lightweight — it does not sync the full L2 history.
	DepositOrderEvents = "events"
	// DepositOrderBridgesync reuses the production bridgesync component, syncing all L2 bridges
	// from genesis and filtering the replayed ones.
	DepositOrderBridgesync = "bridgesync"
	// DefaultDepositOrderSource is used when Options.DepositOrderSource is empty.
	DefaultDepositOrderSource = DepositOrderEvents
)

// shadowForkBridge is a bridge exit recovered from the shadow-fork, carrying the on-chain
// DepositCount that defines its position in the exit tree. Both recovery mechanisms (events and
// bridgesync) produce this common shape so the reorder logic is shared.
type shadowForkBridge struct {
	BlockNum           uint64
	OriginNetwork      uint32
	OriginAddress      common.Address
	DestinationNetwork uint32
	DestinationAddress common.Address
	Amount             *big.Int
	DepositCount       uint32
}

// recoverShadowForkDepositOrder returns the replayed bridge exits ordered by DepositCount, using
// the mechanism selected in cfg.Options.DepositOrderSource. The returned slice contains only the
// bridges produced by the replay (BlockNum >= shadowForkFirstBlock).
func recoverShadowForkDepositOrder(
	ctx context.Context, cfg *Config, anvilURL string, shadowForkFirstBlock uint64,
) ([]shadowForkBridge, error) {
	source := cfg.Options.DepositOrderSource
	if source == "" {
		source = DefaultDepositOrderSource
	}
	switch source {
	case DepositOrderEvents:
		log.Infof("Recovering deposit order via shadow-fork BridgeEvent logs (from block %d)",
			shadowForkFirstBlock)
		return readShadowForkBridges(ctx, anvilURL, cfg.L2BridgeAddress, shadowForkFirstBlock)
	case DepositOrderBridgesync:
		log.Info("Recovering deposit order via bridgesync (syncing all L2 bridges from genesis)")
		all, err := syncShadowForkBridges(ctx, anvilURL, cfg.L2BridgeAddress, cfg.L2NetworkID)
		if err != nil {
			return nil, err
		}
		return bridgesFromBlock(all, shadowForkFirstBlock), nil
	default:
		return nil, fmt.Errorf("unknown depositOrderSource %q (expected %q or %q)",
			source, DepositOrderEvents, DepositOrderBridgesync)
	}
}

// bridgesFromBlock returns the subset of bridges with BlockNum >= fromBlock, preserving order.
// Used to isolate the bridges produced by the replay (BlockNum >= shadowForkFirstBlock) from the
// historical L2 bridges synced from genesis by the bridgesync mechanism.
func bridgesFromBlock(bridges []shadowForkBridge, fromBlock uint64) []shadowForkBridge {
	out := make([]shadowForkBridge, 0, len(bridges))
	for _, b := range bridges {
		if b.BlockNum >= fromBlock {
			out = append(out, b)
		}
	}
	return out
}

// bridgeMatchKey identifies a bridge exit by its on-chain leaf content, used to map a replayed
// BridgeEvent back to the certificate exit that produced it.
type bridgeMatchKey struct {
	originNetwork uint32
	originAddr    common.Address
	destNetwork   uint32
	destAddr      common.Address
	amount        string
}

func bigIntKey(v *big.Int) string {
	if v == nil {
		return "0"
	}
	return v.String()
}

// exitMatchKey builds the content key for a certificate bridge exit. For native exits (no token
// info) the bridge emits the gas token origin — standard ETH is (network 0, address 0x0).
func exitMatchKey(
	be *agglayertypes.BridgeExit, gasTokenNetwork uint32, gasTokenAddress common.Address,
) bridgeMatchKey {
	originNetwork := gasTokenNetwork
	originAddr := gasTokenAddress
	if be.TokenInfo != nil && be.TokenInfo.OriginTokenAddress != (common.Address{}) {
		originNetwork = be.TokenInfo.OriginNetwork
		originAddr = be.TokenInfo.OriginTokenAddress
	}
	return bridgeMatchKey{
		originNetwork: originNetwork,
		originAddr:    originAddr,
		destNetwork:   be.DestinationNetwork,
		destAddr:      be.DestinationAddress,
		amount:        bigIntKey(be.Amount),
	}
}

// reorderCertificateExits reorders certificate.BridgeExits (and the parallel metadatas slice) to
// follow the deposit order observed on the shadow-fork. The parallel replay assigns depositCounts
// non-deterministically, so the certificate's exit order must be aligned with the actual exit-tree
// leaf order for the certificate to be consistent with the computed NewLocalExitRoot (agglayer
// recomputes the LER by inserting the certificate's bridge exits in order).
//
// replayedBridges must be the bridges produced by the replay, sorted by DepositCount. Each is
// matched back to a certificate exit by leaf content; identical exits produce identical leaves, so
// their relative order is irrelevant. Returns the metadatas reordered to match the new order.
func reorderCertificateExits(
	certificate *agglayertypes.Certificate, metadatas [][]byte, replayedBridges []shadowForkBridge,
	gasTokenNetwork uint32, gasTokenAddress common.Address,
) ([][]byte, error) {
	exits := certificate.BridgeExits
	if len(replayedBridges) != len(exits) {
		return nil, fmt.Errorf("replayed bridge count %d != certificate bridge exit count %d",
			len(replayedBridges), len(exits))
	}

	// Map content key -> queue of original exit indices (a multimap handles duplicate exits).
	indexByKey := make(map[bridgeMatchKey][]int, len(exits))
	for i, be := range exits {
		key := exitMatchKey(be, gasTokenNetwork, gasTokenAddress)
		indexByKey[key] = append(indexByKey[key], i)
	}

	newExits := make([]*agglayertypes.BridgeExit, len(exits))
	newMetadatas := make([][]byte, len(exits))
	for pos, b := range replayedBridges {
		key := bridgeMatchKey{
			originNetwork: b.OriginNetwork,
			originAddr:    b.OriginAddress,
			destNetwork:   b.DestinationNetwork,
			destAddr:      b.DestinationAddress,
			amount:        bigIntKey(b.Amount),
		}
		queue := indexByKey[key]
		if len(queue) == 0 {
			return nil, fmt.Errorf("no certificate bridge exit matches replayed bridge depositCount=%d "+
				"(origin net=%d addr=%s dest net=%d addr=%s amount=%s)",
				b.DepositCount, b.OriginNetwork, b.OriginAddress.Hex(),
				b.DestinationNetwork, b.DestinationAddress.Hex(), bigIntKey(b.Amount))
		}
		idx := queue[0]
		indexByKey[key] = queue[1:]
		newExits[pos] = exits[idx]
		if idx < len(metadatas) {
			newMetadatas[pos] = metadatas[idx]
		}
	}

	certificate.BridgeExits = newExits
	return newMetadatas, nil
}
