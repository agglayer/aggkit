package sources

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/bridgeservice/client"
	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/ethereum/go-ethereum/common"
)

// activityPageSize is the page size used to page through a network's GET /bridge/v1/bridges
// while scanning for a given from_address (see ActivitySource.BridgesFrom)
const activityPageSize = uint32(100)

// NetworkLister widens NetworkURLResolver with network enumeration and bridge contract address
// resolution: it is the slice of bridgeservicefinder.Finder ActivitySource needs on top of the
// per-network URL lookup every other source already uses, so it knows which bridge services to
// scan for a given address (without a fixed config list) and which contract to check
// isClaimed() against. bridgeservicefinder.Finder satisfies it.
type NetworkLister interface {
	NetworkURLResolver
	// NetworkIDs returns the networkIDs of every network currently resolved
	NetworkIDs() []uint32
	// BridgeAddress returns the bridge contract address for networkID (see
	// bridgeservicefinder.Finder.BridgeAddress for the resolution/override rules)
	BridgeAddress(ctx context.Context, networkID uint32) (common.Address, error)
}

// ActivitySource implements bridgetracker.ActivityBridgeScanner and ActivityClaimChecker: it
// scans every network the finder currently knows about for bridges sent by a given address
// (via each network's own bridge service), and resolves a bridge's claim state on its
// destination network — isClaimed() on the destination bridge contract as the source of truth,
// then the destination bridge service's own claim record once claimed.
type ActivitySource struct {
	services *bridgeServiceClients
	finder   NetworkLister
	// contractClaimCheckers resolves/caches the on-chain isClaimed() binding per destination
	// network; embedded so tests can still reach newContract directly (see claim_checker.go,
	// shared with ClaimChecker so the binding/cache logic isn't duplicated between them)
	*contractClaimCheckers
}

// NewActivitySource returns an ActivitySource resolving bridge services, JSON-RPC clients and
// destination bridge contract addresses through finder/ethClients (see
// bridgeservicefinder.Finder.BridgeAddress for how a destination network's contract address is
// resolved and overridden)
func NewActivitySource(finder NetworkLister, ethClients EthClientResolver) *ActivitySource {
	return &ActivitySource{
		services:              newBridgeServiceClients(finder),
		finder:                finder,
		contractClaimCheckers: newContractClaimCheckers(finder, ethClients),
	}
}

// BridgesFrom implements bridgetracker.ActivityBridgeScanner: it queries every network's own
// bridge service GET /bridge/v1/bridges filtered by from_address, paging until either a short
// page or an already-known bridge is reached (see fetchNewBridgesFrom — this relies on the
// bridge service reporting bridges newest-first). A network that cannot be reached is logged and
// skipped rather than failing the whole scan, so one misbehaving bridge service does not hide
// every other network's activity.
func (s *ActivitySource) BridgesFrom(
	ctx context.Context, fromAddress common.Address, known map[string]struct{},
) ([]*domain.ScannedBridge, error) {
	addr := fromAddress.Hex()

	var all []*domain.ScannedBridge
	for _, networkID := range s.finder.NetworkIDs() {
		svc, err := s.services.aggkitBridgeClientFor(networkID)
		if err != nil {
			return nil, fmt.Errorf("resolving bridge service client for network %d: %w", networkID, err)
		}

		items, err := fetchNewBridgesFrom(ctx, svc, networkID, addr, activityPageSize, known)
		if err != nil {
			return nil, fmt.Errorf("fetching bridges from %s on network %d: %w", fromAddress, networkID, err)
		}
		all = append(all, items...)
	}
	return all, nil
}

// fetchNewBridgesFrom pages through networkID's GET /bridge/v1/bridges filtered by fromAddress,
// newest bridge first (the bridge service's own order, by descending deposit_count), stopping as
// soon as either a page shorter than pageSize is returned (no more data) or a bridge already in
// known is reached. The latter is safe because the feed is append-only and strictly ordered:
// once a known bridge is seen, every bridge after it (same page or later pages) is guaranteed
// already known too, so nothing new is missed by stopping there. Each returned bridge is paired
// with networkID — the network whose bridge service reported it — via domain.ScannedBridge,
// since that is NOT always the same as the bridge's own OriginNetwork field (see ScannedBridge).
func fetchNewBridgesFrom(
	ctx context.Context, svc *client.Client, networkID uint32, fromAddress string, pageSize uint32,
	known map[string]struct{},
) ([]*domain.ScannedBridge, error) {
	var out []*domain.ScannedBridge
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
		for _, b := range res.Bridges {
			if _, ok := known[b.GlobalIndex.String()]; ok {
				return out, nil
			}
			out = append(out, &domain.ScannedBridge{Bridge: b, NetworkID: networkID})
		}
		if uint32(len(res.Bridges)) < pageSize {
			return out, nil
		}
	}
}

// IsClaimed implements bridgetracker.ActivityClaimChecker: it calls isClaimed() on bridge's
// destination bridge contract. The on-chain sourceBridgeNetwork argument is bridge.NetworkID —
// the network the bridge-creating tx was actually sent to — never bridge.Bridge.OriginNetwork,
// which can differ for a re-bridged asset (see domain.ScannedBridge)
func (s *ActivitySource) IsClaimed(ctx context.Context, bridge *domain.ScannedBridge) (bool, error) {
	return s.isClaimed(ctx, bridge.Bridge.DestinationNetwork, bridge.Bridge.DepositCount, bridge.NetworkID)
}

// ClaimInfo implements bridgetracker.ActivityClaimChecker: it asks bridge's destination
// network's bridge service for the claim record matching bridge's global index
func (s *ActivitySource) ClaimInfo(
	ctx context.Context, bridge *domain.ScannedBridge,
) (*bridgeservicetypes.ClaimResponse, error) {
	svc, err := s.services.aggkitBridgeClientFor(bridge.Bridge.DestinationNetwork)
	if err != nil {
		return nil, err
	}

	res, err := svc.GetClaims(ctx, client.GetClaimsParams{
		NetworkID:   bridge.Bridge.DestinationNetwork,
		GlobalIndex: bridge.Bridge.GlobalIndex,
	})
	if isNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fetching claim of global index %s on network %d: %w",
			bridge.Bridge.GlobalIndex, bridge.Bridge.DestinationNetwork, err)
	}
	if res.Count == 0 || len(res.Claims) == 0 {
		return nil, nil
	}
	return res.Claims[0], nil
}
