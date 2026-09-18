package sources

import (
	"context"
	"fmt"
	"math/big"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/agglayer/aggkit/bridgetracker"
	trackertypes "github.com/agglayer/aggkit/bridgetracker/types"
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
	// resolver resolves the canonical bridge contract address for the bridge's origin network.
	// Required (fail closed): OriginLER never reads GetRoot() from an attacker-controlled
	// address — it only ever binds to the resolver's answer, never to anything derived from the
	// bridge's own (already-verified-by-BridgeEventSource) log.
	resolver bridgeAddressResolver
}

// NewLERSource returns a LERSource resolving per-network JSON-RPC clients through clients and
// the origin network's canonical bridge contract address through resolver. Returns an error if
// resolver is nil (fail closed — there is no permissive fallback for locating the bridge
// contract).
func NewLERSource(clients EthClientResolver, resolver bridgeAddressResolver) (*LERSource, error) {
	if resolver == nil {
		return nil, fmt.Errorf("bridge address resolver is required")
	}
	return &LERSource{clients: clients, resolver: resolver}, nil
}

// OriginLER implements bridgetracker.LERSource. It never actually returns nil (see the type
// doc): the origin network's local exit tree always covers its own deposit by the time the
// BridgeEvent exists, so this resolves the bridge contract's canonical address and reads
// GetRoot() at that exact block
func (s *LERSource) OriginLER(
	ctx context.Context, bridge *bridgetracker.BridgeInfo,
) (*trackertypes.LERUpdateResult, error) {
	client, err := s.clients.RPCClientFor(ctx, bridge.NetworkID)
	if err != nil {
		return nil, err // transient: URL resolution failure, retried by the engine
	}

	bridgeAddr, err := s.resolver.BridgeAddress(ctx, bridge.NetworkID)
	if err != nil {
		return nil, fmt.Errorf("resolving canonical bridge address for network %d: %w", bridge.NetworkID, err)
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
