package sources

import (
	"context"
	"fmt"
	"sync"

	"github.com/agglayer/aggkit/bridgeservice/client"
	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgetracker"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
)

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

// activityBridgeRPCScanner is the RPC-based fallback surface ActivitySource.BridgesFrom needs;
// *activityRPCScanner satisfies it. Interface only so tests can inject a stub (see
// activity_test.go) instead of a full RPC mock — activity_rpc_test.go already covers
// *activityRPCScanner's own scanning logic directly.
type activityBridgeRPCScanner interface {
	BridgesFrom(ctx context.Context, networkID uint32, fromAddress common.Address) ([]*domain.ScannedBridge, error)
}

// ActivitySource implements bridgetracker.ActivityBridgeScanner and ActivityClaimChecker: it
// scans every network the finder currently knows about for bridges sent by a given address (via
// each network's own bridge service, plus an optional RPC-based fallback for bridges that
// service has not indexed yet — see rpc, agglayer/aggkit#1837), and resolves a bridge's claim
// state on its destination network — isClaimed() on the destination bridge contract as the
// source of truth, then the destination bridge service's own claim record once claimed.
type ActivitySource struct {
	logger   aggkitcommon.Logger
	services *bridgeServiceClients
	finder   NetworkLister
	// pageSize is the page size used to page through a network's GET /bridge/v1/bridges (see
	// fetchNewBridgesFrom)
	pageSize uint32
	// rpc is the RPC-based fallback scanner (see agglayer/aggkit#1837); nil when
	// bridgetracker.ActivitySourceRPCConfig.Enabled is false, in which case BridgesFrom behaves
	// exactly as it did before this fallback existed. Interface (*activityRPCScanner satisfies
	// it) so tests can exercise BridgesFrom's merge logic with a stub instead of a full RPC mock
	// (see activity_test.go)
	rpc activityBridgeRPCScanner
	// contractClaimCheckers resolves/caches the on-chain isClaimed() binding per destination
	// network; embedded so tests can still reach newContract directly (see claim_checker.go,
	// shared with ClaimChecker so the binding/cache logic isn't duplicated between them)
	*contractClaimCheckers
}

// NewActivitySource returns an ActivitySource resolving bridge services, JSON-RPC clients and
// destination bridge contract addresses through finder/ethClients (see
// bridgeservicefinder.Finder.BridgeAddress for how a destination network's contract address is
// resolved and overridden). bridgeServiceCfg/rpcCfg configure the two ActivityBridgeScanner
// sources BridgesFrom queries in parallel — the existing bridge-service-backed one and the
// RPC-based fallback (see bridgetracker.Config.ActivitySourceBridgeService/ActivitySourceRPC);
// rpcCfg.Enabled false disables the RPC-based fallback entirely, leaving BridgesFrom's behavior
// unchanged from before it existed.
func NewActivitySource(
	finder NetworkLister, ethClients EthClientResolver, logger aggkitcommon.Logger,
	bridgeServiceCfg bridgetracker.ActivitySourceBridgeServiceConfig,
	rpcCfg bridgetracker.ActivitySourceRPCConfig,
) (*ActivitySource, error) {
	pageSize := bridgeServiceCfg.PageSize
	if pageSize == 0 {
		pageSize = bridgetracker.DefaultActivitySourceBridgeServicePageSize
	}

	// rpc is declared as the activityBridgeRPCScanner interface, not *activityRPCScanner, and
	// left completely unassigned (a true nil interface, not a nil pointer wrapped in a non-nil
	// interface) when the fallback is disabled — the "s.rpc == nil" checks in scanNetwork rely
	// on that.
	var rpc activityBridgeRPCScanner
	if rpcCfg.Enabled {
		fromBlock, toBlock := rpcCfg.RangeFromBlock, rpcCfg.RangeToBlock
		if fromBlock.IsEmpty() {
			fromBlock = bridgetracker.DefaultActivitySourceRPCRangeFromBlock
		}
		if toBlock.IsEmpty() {
			toBlock = bridgetracker.DefaultActivitySourceRPCRangeToBlock
		}
		concreteRPC, err := newActivityRPCScanner(ethClients, finder, fromBlock, toBlock, logger)
		if err != nil {
			return nil, fmt.Errorf("creating RPC-based activity fallback scanner: %w", err)
		}
		rpc = concreteRPC
	}

	return &ActivitySource{
		logger:                logger,
		services:              newBridgeServiceClients(finder),
		finder:                finder,
		pageSize:              pageSize,
		rpc:                   rpc,
		contractClaimCheckers: newContractClaimCheckers(finder, ethClients),
	}, nil
}

// networkScanResult holds one network's concurrently-fetched bridge-service and RPC-fallback
// results (see ActivitySource.scanNetwork/BridgesFrom)
type networkScanResult struct {
	restBridges []*domain.ScannedBridge
	restErr     error
	// syncStatus is networkID's own bridge service's GET /bridge/v1/sync-status result, fetched
	// best-effort right after restBridges (nil on any failure — see isNetworkSynced)
	syncStatus *bridgeservicetypes.SyncStatus
	rpcBridges []*domain.ScannedBridge
	rpcErr     error
}

// scanNetwork runs the bridge-service fetch and the RPC-based fallback scan for networkID
// concurrently (see BridgesFrom) — the acceptance criterion behind agglayer/aggkit#1837 that the
// two sources are queried in parallel.
func (s *ActivitySource) scanNetwork(
	ctx context.Context, networkID uint32, addr string, fromAddress common.Address, known map[string]struct{},
) networkScanResult {
	var res networkScanResult
	var wg sync.WaitGroup
	wg.Add(2) //nolint:mnd // two concurrent fetches: the bridge-service call and the RPC fallback
	go func() {
		defer wg.Done()
		svc, err := s.services.aggkitBridgeClientFor(networkID)
		if err != nil {
			res.restErr = err
			return
		}
		res.restBridges, res.restErr = fetchNewBridgesFrom(ctx, svc, networkID, addr, s.pageSize, known)
		if res.restErr == nil && s.rpc != nil {
			// only needed to decide the "not fully synchronized" warning below, which only ever
			// applies when the RPC fallback is actually enabled — skipped entirely otherwise, so
			// a disabled fallback never adds this extra round trip (and never waits on it) on top
			// of every activity request.
			//
			// best-effort: a failure here just skips that warning, it never turns a successful
			// bridge fetch into an error
			res.syncStatus, _ = svc.GetSyncStatus(ctx)
		}
	}()
	go func() {
		defer wg.Done()
		if s.rpc == nil {
			return
		}
		res.rpcBridges, res.rpcErr = s.rpc.BridgesFrom(ctx, networkID, fromAddress)
	}()
	wg.Wait()
	return res
}

// isNetworkSynced reports whether networkID's own bridge service is fully caught up with its
// network, per GET /bridge/v1/sync-status: network 0 (mainnet) reads L1Info, any other network
// reads L2Info — each instance's own side of that pair (see GetSyncStatusHandler). A nil status
// (the best-effort GetSyncStatus call in scanNetwork failed, or never ran) is conservatively
// treated as "not synced": a missing status is as much a reason to warn as a stale one.
func isNetworkSynced(networkID uint32, status *bridgeservicetypes.SyncStatus) bool {
	if status == nil {
		return false
	}
	if networkID == MainnetNetworkID {
		return status.L1Info != nil && status.L1Info.IsSynced
	}
	return status.L2Info != nil && status.L2Info.IsSynced
}

// appendNewBridges appends to all every bridge in candidates whose GlobalIndex is not already in
// known, reporting whether anything was actually appended. Shared by BridgesFrom's two merge
// paths (bridge-service reachable and bridge-service unreachable) so an RPC-only bridge is never
// re-added — and so never re-triggers its claim/readiness checks — regardless of which path
// found it.
func appendNewBridges(
	all []*domain.ScannedBridge, candidates []*domain.ScannedBridge, known map[string]struct{},
) ([]*domain.ScannedBridge, bool) {
	var addedAny bool
	for _, b := range candidates {
		if _, dup := known[string(b.Bridge.GlobalIndex)]; dup {
			continue
		}
		all = append(all, b)
		addedAny = true
	}
	return all, addedAny
}

// BridgesFrom implements bridgetracker.ActivityBridgeScanner: for every network the finder
// currently knows about, it queries that network's own bridge service GET /bridge/v1/bridges
// filtered by from_address (paging until either a short page or an already-known bridge is
// reached — see fetchNewBridgesFrom, this relies on the bridge service reporting bridges
// newest-first) concurrently with the RPC-based fallback scan (see rpc, agglayer/aggkit#1837)
// over a small, recent block window on that network's own bridge contract — a safety net for a
// bridge that bridge service has not indexed yet. The two results are merged per network:
//   - the bridge service failing entirely falls back to the RPC result alone (if the fallback is
//     enabled and it itself succeeded), with a warning that historical activity may not be
//     available for that network;
//   - the bridge service succeeding keeps its own result, plus whatever new bridge the RPC
//     fallback found that it did not already report (deduplicated by GlobalIndex, same as
//     known); a warning is added only when doing so and networkID's own bridge service is not
//     fully caught up with its network (see isNetworkSynced) — a fully synced bridge service
//     silently absorbs the RPC-only bridges, since finding any there at all would then be
//     unexpected rather than simply "not indexed yet";
//   - the RPC fallback failing, finding nothing new, or being disabled entirely never changes or
//     degrades the bridge-service result.
//
// A network whose bridge service cannot be reached at all, and whose RPC fallback cannot help
// either, is skipped and reported back as a domain.ActivityWarning instead of failing the whole
// scan, so one misbehaving network never hides every other network's activity.
func (s *ActivitySource) BridgesFrom(
	ctx context.Context, fromAddress common.Address, known map[string]struct{},
) ([]*domain.ScannedBridge, []domain.ActivityWarning, error) {
	addr := fromAddress.Hex()

	var all []*domain.ScannedBridge
	var warnings []domain.ActivityWarning
	for _, networkID := range s.finder.NetworkIDs() {
		res := s.scanNetwork(ctx, networkID, addr, fromAddress, known)

		if res.restErr != nil {
			if s.rpc != nil && res.rpcErr == nil {
				all, _ = appendNewBridges(all, res.rpcBridges, known)
				warnings = append(warnings, s.warnf(networkID,
					"bridge service unavailable for network %d, historical activity may not be "+
						"available: %v", networkID, res.restErr))
			} else {
				warnings = append(warnings, s.warnf(networkID,
					"fetching bridges from %s on network %d: %v", fromAddress, networkID, res.restErr))
			}
			continue
		}
		all = append(all, res.restBridges...)

		if res.rpcErr != nil || len(res.rpcBridges) == 0 {
			continue
		}
		// res.restBridges was not yet known to the RPC scan (they ran concurrently), so it must
		// be folded into the dedup set here before deciding what the RPC scan actually adds
		localKnown := make(map[string]struct{}, len(known)+len(res.restBridges))
		for k := range known {
			localKnown[k] = struct{}{}
		}
		for _, b := range res.restBridges {
			localKnown[string(b.Bridge.GlobalIndex)] = struct{}{}
		}

		var addedAny bool
		all, addedAny = appendNewBridges(all, res.rpcBridges, localKnown)
		if addedAny && !isNetworkSynced(networkID, res.syncStatus) {
			warnings = append(warnings, s.warnf(networkID,
				"bridge service for network %d is not fully synchronized, some bridges may "+
					"still be missing", networkID))
		}
	}
	return all, warnings, nil
}

// warnf logs msg (formatted per fmt.Sprintf's rules on format/args) and turns it into the
// domain.ActivityWarning BridgesFrom reports back for networkID
func (s *ActivitySource) warnf(networkID uint32, format string, args ...any) domain.ActivityWarning {
	message := fmt.Sprintf(format, args...)
	s.logger.Warnf("activity: %s", message)
	return domain.ActivityWarning{NetworkID: networkID, Message: message}
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
			if _, ok := known[string(b.GlobalIndex)]; ok {
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
		GlobalIndex: bridge.Bridge.GlobalIndex.ToBigInt(),
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

// IsReadyToClaim implements bridgetracker.ActivityClaimChecker: it resolves the L1 info tree
// leaf covering bridge's own origin deposit (GET /bridge/v1/l1-info-tree-index, queried per
// bridgeServiceClients.l1InfoTreeIndexClientFor's routing rule — the same one
// GERSource.L1InfoTreeIndexForBridge uses to build the claim proof), then whether that leaf has
// already been injected on the destination (GET /bridge/v1/injected-l1-info-leaf, always asked
// of the destination's own instance). Both must resolve for the bridge to be ready to claim;
// either one still pending (a 404) reports false, not an error
func (s *ActivitySource) IsReadyToClaim(ctx context.Context, bridge *domain.ScannedBridge) (bool, error) {
	originSvc, err := s.services.l1InfoTreeIndexClientFor(bridge.NetworkID, bridge.Bridge.DestinationNetwork)
	if err != nil {
		return false, err
	}

	leafIndex, err := originSvc.GetL1InfoTreeIndex(ctx, int(bridge.NetworkID), int(bridge.Bridge.DepositCount))
	if isNotFound(err) {
		return false, nil // not covered by any L1 info tree leaf yet
	}
	if err != nil {
		return false, fmt.Errorf("fetching l1 info tree index for network %d deposit %d: %w",
			bridge.NetworkID, bridge.Bridge.DepositCount, err)
	}

	destSvc, err := s.services.aggkitBridgeClientFor(bridge.Bridge.DestinationNetwork)
	if err != nil {
		return false, err
	}
	_, err = destSvc.GetInjectedL1InfoLeaf(ctx, int(bridge.Bridge.DestinationNetwork), int(leafIndex))
	if isNotFound(err) {
		return false, nil // covering leaf not injected on the destination yet
	}
	if err != nil {
		return false, fmt.Errorf("fetching injected l1 info leaf %d on network %d: %w",
			leafIndex, bridge.Bridge.DestinationNetwork, err)
	}
	return true, nil
}
