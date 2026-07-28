package sources

import (
	"context"
	"errors"
	"fmt"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/agglayer/aggkit/bridgetracker"
	trackertypes "github.com/agglayer/aggkit/bridgetracker/types"
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
}

// NewBridgeEventSource returns a BridgeEventSource resolving per-network JSON-RPC clients
// through the given resolver
func NewBridgeEventSource(clients EthClientResolver) (*BridgeEventSource, error) {
	parser, err := agglayerbridge.NewAgglayerbridge(common.Address{}, nil)
	if err != nil {
		return nil, fmt.Errorf("creating bridge contract parser: %w", err)
	}
	return &BridgeEventSource{clients: clients, parser: parser}, nil
}

// FindBridge implements bridgetracker.BridgeEventSource: it resolves the receipt of the tx
// on its origin network and extracts the BridgeEvent facts. A missing tx is ErrBridgeTxNotFound
// (it may simply not be mined yet, so the engine retries); a reverted tx or a mined receipt
// without BridgeEvent logs are both ErrBridgeTxNotABridge, since neither can ever change on
// retry (the engine fails the bridge immediately for those)
func (s *BridgeEventSource) FindBridge(
	ctx context.Context, id bridgetracker.TrackingID,
) (*bridgetracker.BridgeInfo, error) {
	client, err := s.clients.ClientFor(ctx, id.NetworkID)
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

	if receipt.Status != gethtypes.ReceiptStatusSuccessful {
		return nil, fmt.Errorf("%s reverted: %w", id, bridgetracker.ErrBridgeTxNotABridge)
	}

	for _, l := range receipt.Logs {
		if len(l.Topics) == 0 || l.Topics[0] != bridgeEventSignature {
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
