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
// on its origin network and extracts the BridgeEvent facts. A missing tx, a reverted tx or
// a receipt without BridgeEvent logs are all ErrBridgeTxNotFound (the engine retries a few
// polls before failing the bridge, covering the not-yet-mined window)
func (s *BridgeEventSource) FindBridge(
	ctx context.Context, networkID uint32, txHash common.Hash,
) (*bridgetracker.BridgeInfo, error) {
	client, err := s.clients.ClientFor(ctx, networkID)
	if err != nil {
		return nil, fmt.Errorf("resolving JSON-RPC client for network %d: %w", networkID, err)
	}

	receipt, err := client.TransactionReceipt(ctx, txHash)
	if errors.Is(err, ethereum.NotFound) {
		return nil, fmt.Errorf("tx %s not found on network %d: %w",
			txHash, networkID, bridgetracker.ErrBridgeTxNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("fetching receipt of tx %s on network %d: %w", txHash, networkID, err)
	}

	if receipt.Status != gethtypes.ReceiptStatusSuccessful {
		return nil, fmt.Errorf("tx %s reverted on network %d: %w",
			txHash, networkID, bridgetracker.ErrBridgeTxNotFound)
	}

	for _, l := range receipt.Logs {
		if len(l.Topics) == 0 || l.Topics[0] != bridgeEventSignature {
			continue
		}
		event, err := s.parser.ParseBridgeEvent(*l)
		if err != nil {
			return nil, fmt.Errorf("parsing BridgeEvent log of tx %s: %w", txHash, err)
		}
		return &bridgetracker.BridgeInfo{
			Key:                bridgetracker.BridgeKey{NetworkID: networkID, TxHash: txHash},
			LeafType:           trackertypes.BridgeLeafType(event.LeafType),
			DestinationNetwork: event.DestinationNetwork,
			DepositCount:       event.DepositCount,
			BlockNumber:        l.BlockNumber,
			LogIndex:           uint32(l.Index),
		}, nil
	}

	return nil, fmt.Errorf("tx %s on network %d emitted no BridgeEvent: %w",
		txHash, networkID, bridgetracker.ErrBridgeTxNotFound)
}
