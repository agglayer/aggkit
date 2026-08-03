package sources

import (
	"context"
	"errors"
	"fmt"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/agglayer/aggkit/bridgetracker"
	trackertypes "github.com/agglayer/aggkit/bridgetracker/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// bridgeEventSignature is the topic0 of the bridge contract's BridgeEvent (same event on L1
// and L2 bridges)
var bridgeEventSignature = crypto.Keccak256Hash([]byte(
	"BridgeEvent(uint8,uint32,address,uint32,address,uint256,bytes,uint32)",
))

// BridgeEventSource implements bridgetracker.BridgeEventSource over the origin network's
// JSON-RPC endpoint: it fetches the tx receipt and parses the BridgeEvent log
type BridgeEventSource struct {
	clients EthClientResolver
	// parser is the bridge contract binding used only for ABI log unpacking (no backend calls)
	parser *agglayerbridge.Agglayerbridge
	// l1Finality is the block finality an L1 (network 0) receipt must reach before it is
	// accepted (see FindBridge): a resolved bridge is never re-checked (TrackingBridgeTx.IsDone),
	// so accepting a receipt that later gets reorged out would otherwise be permanent
	l1Finality aggkittypes.BlockNumberFinality
	// l2Finality is the block finality an L2 (non-zero network) receipt must reach before it
	// is accepted; see l1Finality for the reasoning
	l2Finality aggkittypes.BlockNumberFinality
	// bridgeAddrs is the per-network canonical bridge contract address a BridgeEvent log's
	// emitter must match to be accepted (see FindBridge). A network absent from this map has
	// no configured address yet, so its logs are still matched on the event signature alone.
	bridgeAddrs map[uint32]common.Address
}

// NewBridgeEventSource returns a BridgeEventSource resolving per-network JSON-RPC clients
// through the given resolver, accepting a tx's receipt only once it reaches l1Finality (for
// network 0) or l2Finality (for any other network). bridgeAddrs is the static
// networkID -> canonical bridge contract address map used to reject a BridgeEvent log emitted
// by an unrelated or malicious contract; a network absent from it (or a nil map) keeps matching
// logs on the event signature alone.
func NewBridgeEventSource(
	clients EthClientResolver, l1Finality, l2Finality aggkittypes.BlockNumberFinality,
	bridgeAddrs map[uint32]common.Address,
) (*BridgeEventSource, error) {
	parser, err := agglayerbridge.NewAgglayerbridge(common.Address{}, nil)
	if err != nil {
		return nil, fmt.Errorf("creating bridge contract parser: %w", err)
	}
	return &BridgeEventSource{
		clients: clients, parser: parser, l1Finality: l1Finality, l2Finality: l2Finality, bridgeAddrs: bridgeAddrs,
	}, nil
}

// finalityFor returns the block finality a receipt on networkID must reach before it is
// accepted: l1Finality for L1 (network 0), l2Finality for any other network.
func (s *BridgeEventSource) finalityFor(networkID uint32) aggkittypes.BlockNumberFinality {
	if networkID == 0 {
		return s.l1Finality
	}
	return s.l2Finality
}

// FindBridge implements bridgetracker.BridgeEventSource: it resolves the receipt of the tx
// on its origin network and extracts the BridgeEvent facts. A missing tx, or one whose receipt
// has not reached finality yet, is ErrBridgeTxNotFound (the engine retries — the tx may simply
// not be mined, or not final, yet); a reverted tx or a mined receipt without BridgeEvent logs
// are both ErrBridgeTxNotABridge, since neither can ever change on retry (the engine fails the
// bridge immediately for those). Finality is checked before either of those outcomes is decided:
// a reverted or eventless receipt can be reorged away just as much as a successful one, and a
// resolved bridge is never re-checked afterward (see TrackingBridgeTx.IsDone), so accepting any
// outcome from a non-final receipt could leave the tracker permanently wrong
func (s *BridgeEventSource) FindBridge(
	ctx context.Context, id bridgetracker.TrackingID,
) (*bridgetracker.BridgeInfo, error) {
	client, err := s.clients.RPCClientFor(ctx, id.NetworkID)
	if err != nil {
		return nil, fmt.Errorf("resolving JSON-RPC client for network %d: %w", id.NetworkID, err)
	}

	receipt, err := client.TransactionReceipt(ctx, id.TxHash)
	if errors.Is(err, ethereum.NotFound) {
		return nil, fmt.Errorf("%s not found: %w", id, bridgetracker.ErrBridgeTxNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("fetching receipt of %s: %w", id, err)
	}

	finality := s.finalityFor(id.NetworkID)
	finalized, err := client.CustomHeaderByNumber(ctx, &finality)
	if err != nil {
		return nil, fmt.Errorf("fetching %s header for network %d: %w", finality.String(), id.NetworkID, err)
	}
	if receipt.BlockNumber == nil || receipt.BlockNumber.Uint64() > finalized.Number {
		return nil, fmt.Errorf("%s not yet %s: %w", id, finality.String(), bridgetracker.ErrBridgeTxNotFound)
	}

	if receipt.Status != gethtypes.ReceiptStatusSuccessful {
		return nil, fmt.Errorf("%s reverted: %w", id, bridgetracker.ErrBridgeTxNotABridge)
	}

	wantAddr, checkAddr := s.bridgeAddrs[id.NetworkID]

	for _, l := range receipt.Logs {
		if len(l.Topics) == 0 || l.Topics[0] != bridgeEventSignature {
			continue
		}
		if checkAddr && l.Address != wantAddr {
			continue
		}
		event, err := s.parser.ParseBridgeEvent(*l)
		if err != nil {
			return nil, fmt.Errorf("parsing BridgeEvent log of %s: %w", id, err)
		}
		return &bridgetracker.BridgeInfo{
			NetworkID:          id.NetworkID,
			LeafType:           trackertypes.BridgeLeafType(event.LeafType),
			DestinationNetwork: event.DestinationNetwork,
			DepositCount:       event.DepositCount,
			BlockNumber:        l.BlockNumber,
			LogIndex:           uint32(l.Index),
		}, nil
	}

	return nil, fmt.Errorf("%s emitted no BridgeEvent: %w", id, bridgetracker.ErrBridgeTxNotABridge)
}
