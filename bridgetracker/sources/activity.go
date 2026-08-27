package sources

import (
	"context"
	"fmt"
	"sync"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	"github.com/agglayer/aggkit/bridgeservice/client"
	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

// activityPageSize is the page size used to page through a network's GET /bridge/v1/bridges
// while scanning for a given from_address (see ActivitySource.BridgesFrom)
const activityPageSize = uint32(100)

// NetworkLister widens NetworkURLResolver with network enumeration: it is the slice of
// bridgeservicefinder.Finder ActivitySource needs on top of the per-network URL lookup every
// other source already uses, so it knows which bridge services to scan for a given address
// without a fixed config list. bridgeservicefinder.Finder satisfies it.
type NetworkLister interface {
	NetworkURLResolver
	// NetworkIDs returns the networkIDs of every network currently resolved
	NetworkIDs() []uint32
}

// claimChecker is the minimal bridge contract surface ActivitySource needs to check a bridge's
// on-chain claim state; *agglayerbridgel2.Agglayerbridgel2 satisfies it
type claimChecker interface {
	IsClaimed(opts *bind.CallOpts, leafIndex uint32, sourceBridgeNetwork uint32) (bool, error)
}

// ActivitySource implements bridgetracker.ActivityBridgeScanner and ActivityClaimChecker: it
// scans every network the finder currently knows about for bridges sent by a given address
// (via each network's own bridge service), and resolves a bridge's claim state on its
// destination network — isClaimed() on the destination bridge contract as the source of truth,
// then the destination bridge service's own claim record once claimed.
type ActivitySource struct {
	services    *bridgeServiceClients
	finder      NetworkLister
	ethClients  EthClientResolver
	bridgeAddrs map[uint32]common.Address
	// newContract builds the claim-checking contract binding for a destination network,
	// injectable for tests. Defaults to agglayerbridgel2.NewAgglayerbridgel2
	newContract func(addr common.Address, c aggkittypes.BaseEthereumClienter) (claimChecker, error)

	mu        sync.Mutex
	contracts map[uint32]claimChecker // destination networkID -> bound contract, built lazily
}

// NewActivitySource returns an ActivitySource resolving bridge services and JSON-RPC clients
// through finder/ethClients, and destination bridge contract addresses through bridgeAddrs (see
// Config.BridgeAddrs) — a destination network absent from bridgeAddrs cannot be claim-checked
// (IsClaimed errors for it, see claimCheckerFor)
func NewActivitySource(
	finder NetworkLister, ethClients EthClientResolver, bridgeAddrs map[uint32]common.Address,
) *ActivitySource {
	return &ActivitySource{
		services:    newBridgeServiceClients(finder),
		finder:      finder,
		ethClients:  ethClients,
		bridgeAddrs: bridgeAddrs,
		newContract: func(addr common.Address, c aggkittypes.BaseEthereumClienter) (claimChecker, error) {
			return agglayerbridgel2.NewAgglayerbridgel2(addr, c)
		},
		contracts: make(map[uint32]claimChecker),
	}
}

// BridgesFrom implements bridgetracker.ActivityBridgeScanner: it queries every network's own
// bridge service GET /bridge/v1/bridges filtered by from_address, paging until a short page. A
// network that cannot be reached is logged and skipped rather than failing the whole scan, so
// one misbehaving bridge service does not hide every other network's activity.
func (s *ActivitySource) BridgesFrom(
	ctx context.Context, fromAddress common.Address,
) ([]*bridgeservicetypes.BridgeResponse, error) {
	addr := fromAddress.Hex()

	var all []*bridgeservicetypes.BridgeResponse
	for _, networkID := range s.finder.NetworkIDs() {
		svc, err := s.services.aggkitBridgeClientFor(networkID)
		if err != nil {
			return nil, fmt.Errorf("resolving bridge service client for network %d: %w", networkID, err)
		}

		items, err := fetchAllBridgesFrom(ctx, svc, networkID, addr, activityPageSize)
		if err != nil {
			return nil, fmt.Errorf("fetching bridges from %s on network %d: %w", fromAddress, networkID, err)
		}
		all = append(all, items...)
	}
	return all, nil
}

// fetchAllBridgesFrom pages through networkID's GET /bridge/v1/bridges filtered by fromAddress
// until a page shorter than pageSize is returned
func fetchAllBridgesFrom(
	ctx context.Context, svc *client.Client, networkID uint32, fromAddress string, pageSize uint32,
) ([]*bridgeservicetypes.BridgeResponse, error) {
	var out []*bridgeservicetypes.BridgeResponse
	for page := uint32(1); ; page++ {
		res, err := svc.GetBridges(ctx, client.GetBridgesParams{
			NetworkID:   networkID,
			FromAddress: &fromAddress,
			PageNumber:  &page,
			PageSize:    &pageSize,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, res.Bridges...)
		if uint32(len(res.Bridges)) < pageSize {
			return out, nil
		}
	}
}

// IsClaimed implements bridgetracker.ActivityClaimChecker: it calls isClaimed() on bridge's
// destination bridge contract
func (s *ActivitySource) IsClaimed(ctx context.Context, bridge *bridgeservicetypes.BridgeResponse) (bool, error) {
	contract, err := s.claimCheckerFor(ctx, bridge.DestinationNetwork)
	if err != nil {
		return false, err
	}
	return contract.IsClaimed(&bind.CallOpts{Context: ctx}, bridge.DepositCount, bridge.OriginNetwork)
}

// ClaimInfo implements bridgetracker.ActivityClaimChecker: it asks bridge's destination
// network's bridge service for the claim record matching bridge's global index
func (s *ActivitySource) ClaimInfo(
	ctx context.Context, bridge *bridgeservicetypes.BridgeResponse,
) (*bridgeservicetypes.ClaimResponse, error) {
	svc, err := s.services.aggkitBridgeClientFor(bridge.DestinationNetwork)
	if err != nil {
		return nil, err
	}

	res, err := svc.GetClaims(ctx, client.GetClaimsParams{
		NetworkID:   bridge.DestinationNetwork,
		GlobalIndex: bridge.GlobalIndex,
	})
	if isNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fetching claim of global index %s on network %d: %w",
			bridge.GlobalIndex, bridge.DestinationNetwork, err)
	}
	if res.Count == 0 || len(res.Claims) == 0 {
		return nil, nil
	}
	return res.Claims[0], nil
}

// claimCheckerFor returns (building and caching if necessary) the claim-checking contract
// binding for the given destination network
func (s *ActivitySource) claimCheckerFor(ctx context.Context, networkID uint32) (claimChecker, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if c, ok := s.contracts[networkID]; ok {
		return c, nil
	}

	addr, ok := s.bridgeAddrs[networkID]
	if !ok {
		return nil, fmt.Errorf("no bridge contract address configured for network %d (see [Tracker].BridgeAddrs)",
			networkID)
	}
	rpcClient, err := s.ethClients.RPCClientFor(ctx, networkID)
	if err != nil {
		return nil, fmt.Errorf("resolving JSON-RPC client for network %d: %w", networkID, err)
	}
	contract, err := s.newContract(addr, rpcClient)
	if err != nil {
		return nil, fmt.Errorf("binding bridge contract %s on network %d: %w", addr, networkID, err)
	}

	s.contracts[networkID] = contract
	return contract, nil
}
