package sources

import (
	"context"
	"fmt"
	"sync"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	"github.com/agglayer/aggkit/bridgetracker"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

// claimChecker is the minimal bridge contract surface needed to check a bridge's on-chain claim
// state; *agglayerbridgel2.Agglayerbridgel2 satisfies it. Shared by ActivitySource (the activity
// endpoint) and ClaimChecker (the tracker engine's StepWaitingClaim) via contractClaimCheckers —
// both resolve the exact same isClaimed() call, just off differently-shaped bridge inputs
type claimChecker interface {
	IsClaimed(opts *bind.CallOpts, leafIndex uint32, sourceBridgeNetwork uint32) (bool, error)
}

// contractClaimCheckers resolves and caches, per destination network, the claim-checking
// contract binding used to call isClaimed() on-chain. Factored out of ActivitySource so
// ClaimChecker (below) reuses the same binding/cache logic instead of duplicating it
type contractClaimCheckers struct {
	finder     NetworkLister
	ethClients EthClientResolver
	// newContract builds the claim-checking contract binding for a destination network,
	// injectable for tests. Defaults to agglayerbridgel2.NewAgglayerbridgel2
	newContract func(addr common.Address, c aggkittypes.BaseEthereumClienter) (claimChecker, error)

	mu        sync.Mutex
	contracts map[uint32]claimChecker // destination networkID -> bound contract, built lazily
}

// newContractClaimCheckers returns a contractClaimCheckers resolving bridge contract
// addresses/clients through finder/ethClients
func newContractClaimCheckers(finder NetworkLister, ethClients EthClientResolver) *contractClaimCheckers {
	return &contractClaimCheckers{
		finder:     finder,
		ethClients: ethClients,
		newContract: func(addr common.Address, c aggkittypes.BaseEthereumClienter) (claimChecker, error) {
			return agglayerbridgel2.NewAgglayerbridgel2(addr, c)
		},
		contracts: make(map[uint32]claimChecker),
	}
}

// isClaimed calls isClaimed() on destinationNetwork's bridge contract, for the leaf identified
// by depositCount within sourceNetworkID's exit tree
func (c *contractClaimCheckers) isClaimed(
	ctx context.Context, destinationNetwork, depositCount, sourceNetworkID uint32,
) (bool, error) {
	contract, err := c.claimCheckerFor(ctx, destinationNetwork)
	if err != nil {
		return false, err
	}
	return contract.IsClaimed(&bind.CallOpts{Context: ctx}, depositCount, sourceNetworkID)
}

// claimCheckerFor returns (building and caching if necessary) the claim-checking contract
// binding for the given destination network
func (c *contractClaimCheckers) claimCheckerFor(ctx context.Context, networkID uint32) (claimChecker, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if cc, ok := c.contracts[networkID]; ok {
		return cc, nil
	}

	addr, err := c.finder.BridgeAddress(ctx, networkID)
	if err != nil {
		return nil, fmt.Errorf("resolving bridge contract address for network %d: %w", networkID, err)
	}
	rpcClient, err := c.ethClients.RPCClientFor(ctx, networkID)
	if err != nil {
		return nil, fmt.Errorf("resolving JSON-RPC client for network %d: %w", networkID, err)
	}
	contract, err := c.newContract(addr, rpcClient)
	if err != nil {
		return nil, fmt.Errorf("binding bridge contract %s on network %d: %w", addr, networkID, err)
	}

	c.contracts[networkID] = contract
	return contract, nil
}

// ClaimChecker implements bridgetracker.ClaimChecker over StepWaitingClaim: whether a bridge has
// been claimed on its destination network, per isClaimed() on the destination bridge contract —
// the same on-chain check ActivitySource uses for the activity endpoint, applied here to a
// resolved bridgetracker.BridgeInfo instead of a domain.ScannedBridge
type ClaimChecker struct {
	*contractClaimCheckers
}

// NewClaimChecker returns a ClaimChecker resolving bridge contract addresses/clients through
// finder/ethClients
func NewClaimChecker(finder NetworkLister, ethClients EthClientResolver) *ClaimChecker {
	return &ClaimChecker{contractClaimCheckers: newContractClaimCheckers(finder, ethClients)}
}

// IsClaimed implements bridgetracker.ClaimChecker: it calls isClaimed() on bridge's destination
// bridge contract. The on-chain sourceBridgeNetwork argument is bridge.NetworkID — the network
// the bridge-creating tx was actually sent to
func (c *ClaimChecker) IsClaimed(ctx context.Context, bridge *bridgetracker.BridgeInfo) (bool, error) {
	return c.isClaimed(ctx, bridge.DestinationNetwork, bridge.DepositCount, bridge.NetworkID)
}
