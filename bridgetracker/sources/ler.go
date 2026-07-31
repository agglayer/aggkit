package sources

import (
	"context"
	"fmt"
	"math/big"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/agglayer/aggkit/bridgetracker"
	trackertypes "github.com/agglayer/aggkit/bridgetracker/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

// LERSource implements bridgetracker.LERSource over the origin network's JSON-RPC endpoint.
// Unlike the Global Exit Root (a separate on-chain structure updated asynchronously from the
// deposit, see GERSource), the bridge contract's Local Exit Root is recomputed synchronously
// in the very same transaction as the deposit that feeds it (docs/bridgetracker/README.md's
// L2->Lx sequence diagrams: "BridgeEvent / LER updated"). So the covering LER is simply the
// contract's GetRoot() read back at the block the BridgeEvent was emitted in: by the time
// BridgeEventSource has resolved (and finality-checked) the bridge, its LER is already final too
type LERSource struct {
	clients EthClientResolver
}

// NewLERSource returns a LERSource resolving per-network JSON-RPC clients through the given
// resolver
func NewLERSource(clients EthClientResolver) *LERSource {
	return &LERSource{clients: clients}
}

// OriginLER implements bridgetracker.LERSource. It never actually returns nil (see the type
// doc): the origin network's local exit tree always covers its own deposit by the time the
// BridgeEvent exists, so this locates the bridge contract from the BridgeEvent log itself and
// reads GetRoot() at that exact block
func (s *LERSource) OriginLER(
	ctx context.Context, bridge *bridgetracker.BridgeInfo,
) (*trackertypes.LERUpdateResult, error) {
	client, err := s.clients.RPCClientFor(ctx, bridge.NetworkID)
	if err != nil {
		return nil, err // transient: URL resolution failure, retried by the engine
	}

	bridgeAddr, err := s.bridgeContractAddress(ctx, client, bridge)
	if err != nil {
		return nil, err
	}

	contract, err := agglayerbridge.NewAgglayerbridgeCaller(bridgeAddr, client)
	if err != nil {
		return nil, fmt.Errorf("binding bridge contract at %s: %w", bridgeAddr, err)
	}

	ler, err := contract.GetRoot(&bind.CallOpts{
		Context:     ctx,
		BlockNumber: new(big.Int).SetUint64(bridge.BlockNumber),
	})
	if err != nil {
		return nil, fmt.Errorf("reading local exit root of network %d at block %d: %w",
			bridge.NetworkID, bridge.BlockNumber, err)
	}

	return &trackertypes.LERUpdateResult{
		NetworkID:   bridge.NetworkID,
		LER:         common.Hash(ler),
		BlockNumber: bridge.BlockNumber,
	}, nil
}

// bridgeContractAddress locates the bridge contract's address off the BridgeEvent log itself:
// re-fetching the log at bridge.BlockNumber/LogIndex (both already resolved by
// BridgeEventSource) and taking its emitting address avoids requiring a separate, per-network
// bridge contract address configuration
func (s *LERSource) bridgeContractAddress(
	ctx context.Context, client aggkittypes.BaseEthereumClienter, bridge *bridgetracker.BridgeInfo,
) (common.Address, error) {
	blockNumber := new(big.Int).SetUint64(bridge.BlockNumber)
	logs, err := client.FilterLogs(ctx, ethereum.FilterQuery{
		FromBlock: blockNumber,
		ToBlock:   blockNumber,
		Topics:    [][]common.Hash{{bridgeEventSignature}},
	})
	if err != nil {
		return common.Address{}, fmt.Errorf("fetching BridgeEvent logs of network %d at block %d: %w",
			bridge.NetworkID, bridge.BlockNumber, err)
	}

	for _, l := range logs {
		if uint32(l.Index) == bridge.LogIndex {
			return l.Address, nil
		}
	}
	return common.Address{}, fmt.Errorf("BridgeEvent log %d not found in network %d block %d",
		bridge.LogIndex, bridge.NetworkID, bridge.BlockNumber)
}
