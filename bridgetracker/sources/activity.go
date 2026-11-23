package sources

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/agglayer/aggkit/bridgeservice/client"
	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgetracker"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/metrics"
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
	BridgesFrom(ctx context.Context, networkID uint32, fromAddress common.Address) (activityRPCScanResult, error)
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
	// scanBridgeService)
	pageSize uint32
	// rpc is the RPC-based fallback scanner (see agglayer/aggkit#1837); nil when
	// bridgetracker.ActivitySourceRPCConfig.Enabled is false, in which case BridgesFrom behaves
	// exactly as it did before this fallback existed. Interface (*activityRPCScanner satisfies
	// it) so tests can exercise BridgesFrom's merge logic with a stub instead of a full RPC mock
	// (see activity_test.go)
	rpc activityBridgeRPCScanner
	// maxConcurrentNetworkScans bounds how many networks BridgesFrom scans at once (see
	// bridgetracker.ActivitySourceBridgeServiceConfig.MaxConcurrentNetworkScans)
	maxConcurrentNetworkScans int
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
	timeout := bridgeServiceCfg.Timeout.Duration
	if timeout <= 0 {
		timeout = bridgetracker.DefaultActivitySourceBridgeServiceTimeout.Duration
	}
	maxConcurrentNetworkScans := bridgeServiceCfg.MaxConcurrentNetworkScans
	if maxConcurrentNetworkScans <= 0 {
		maxConcurrentNetworkScans = bridgetracker.DefaultActivitySourceBridgeServiceMaxConcurrentNetworkScans
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
		logger:                    logger,
		services:                  newBridgeServiceClients(finder, timeout),
		finder:                    finder,
		pageSize:                  pageSize,
		rpc:                       rpc,
		maxConcurrentNetworkScans: maxConcurrentNetworkScans,
		contractClaimCheckers:     newContractClaimCheckers(finder, ethClients),
	}, nil
}

// networkScanResult holds one network's concurrently-fetched bridge-service and RPC-fallback
// results (see ActivitySource.scanNetwork/BridgesFrom)
type networkScanResult struct {
	rest    bridgeServiceScan
	restErr error
	// syncStatus is networkID's own bridge service's GET /bridge/v1/sync-status result, fetched
	// best-effort right after rest (nil on any failure — see isNetworkSynced)
	syncStatus *bridgeservicetypes.SyncStatus
	rpcBridges []*domain.ScannedBridge
	// rpcFromBlock/rpcToBlock are the block range the RPC fallback actually resolved and scanned
	// this call (see activityRPCScanResult) — only meaningful when rpcErr is nil; used to decide
	// which known bridges should have shown up in rpcBridges again but did not (see
	// invalidatedBridges)
	rpcFromBlock, rpcToBlock uint64
	rpcErr                   error
}

// scanNetwork runs the bridge-service scan and the RPC-based fallback scan for networkID
// concurrently (see BridgesFrom) — the acceptance criterion behind agglayer/aggkit#1837 that the
// two sources are queried in parallel.
func (s *ActivitySource) scanNetwork(
	ctx context.Context, networkID uint32, addr string, fromAddress common.Address,
	known map[string]domain.KnownBridge,
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
		res.rest, res.restErr = scanBridgeService(ctx, svc, networkID, addr, s.pageSize, known, s.logger)
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
		var result activityRPCScanResult
		result, res.rpcErr = s.rpc.BridgesFrom(ctx, networkID, fromAddress)
		res.rpcBridges, res.rpcFromBlock, res.rpcToBlock = result.Bridges, result.FromBlock, result.ToBlock
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

// appendRPCBridges appends to all every bridge the RPC fallback found (candidates) that the
// bridge service does not already account for: a bridge the bridge service listed this call
// (listedByBridgeService) or that is cached from the bridge service (known Source ==
// domain.ActivitySourceBridgeService) is ignored — the bridge service is the source of record,
// and while it cannot be scanned its entries are kept as they are. Of the rest, a bridge already
// known at the same location is skipped too (nothing to do, and so no claim/readiness re-check
// triggered by it), while one a reorg moved to another block/tx gets through so the cache is
// corrected instead of trusting stale data forever (see agglayer/aggkit#1837's reorg-risk
// discussion). It reports how many bridges it appended and how many of those are corrections.
func appendRPCBridges(
	all []*domain.ScannedBridge, candidates []*domain.ScannedBridge, known map[string]domain.KnownBridge,
	listedByBridgeService map[string]struct{},
) ([]*domain.ScannedBridge, int, int) {
	var added, corrected int
	for _, b := range candidates {
		key := string(b.Bridge.GlobalIndex)
		if _, ok := listedByBridgeService[key]; ok {
			continue
		}
		if prev, ok := known[key]; ok {
			if prev.Source == domain.ActivitySourceBridgeService || prev.SameLocation(b.Bridge) {
				continue
			}
			corrected++
		}
		all = append(all, b)
		added++
	}
	return all, added, corrected
}

// invalidatedBridges returns the GlobalIndex (decimal string, see domain.KnownBridge) of every
// entry in known that: belongs to networkID (see KnownBridge.NetworkID), was last cached from
// the RPC fallback (Source == domain.ActivitySourceRPC — bridge-service-sourced entries are
// checked against the bridge service's own listing instead, see scanBridgeService), has a
// BlockNum still inside [rpcFromBlock, the window's own upper bound] — i.e. it has not simply
// aged out of the range this call scanned, which would explain its absence with nothing wrong —
// yet is not among foundThisCall, this call's full set of bridges actually found (by any source)
// for networkID. That combination means a reorg has dropped it and nothing has re-included it
// (yet); see domain.ActivityBridgeScanner.BridgesFrom's invalidated return for what a caller must
// do with the result.
func invalidatedBridges(
	networkID uint32, known map[string]domain.KnownBridge, rpcFromBlock uint64, foundThisCall map[string]struct{},
) []string {
	var invalidated []string
	for key, k := range known {
		if k.NetworkID != networkID || k.Source != domain.ActivitySourceRPC || k.BlockNum < rpcFromBlock {
			continue
		}
		if _, ok := foundThisCall[key]; !ok {
			invalidated = append(invalidated, key)
		}
	}
	return invalidated
}

// BridgesFrom implements bridgetracker.ActivityBridgeScanner: it scans every network the finder
// currently knows about concurrently — up to maxConcurrentNetworkScans of them at once (see
// bridgetracker.ActivitySourceBridgeServiceConfig.MaxConcurrentNetworkScans), so however many
// networks the finder reports, the number of networks in flight together — and so the number of
// simultaneous outbound calls scanNetwork itself fans out to below — stays bounded. Each network
// runs scanNetwork, which reads that network's own bridge service GET /bridge/v1/bridges filtered
// by from_address — its non-final head, down to the first known final bridge (see
// scanBridgeService) — concurrently with the RPC-based fallback scan (see rpc,
// agglayer/aggkit#1837) over a small, recent block window on that network's own bridge contract
// — a safety net for a bridge that bridge service has not indexed yet. Once every network's scan
// has returned, results are merged sequentially (fast, in-memory only — no further I/O) in a
// second pass, in the same order s.finder.NetworkIDs() returned them, so the outcome does not
// depend on which network happened to answer first. The two results are merged per network:
//   - the bridge service failing (unreachable, any transient error, or a 503 while it resolves a
//     reorg) is never read as its bridges being gone: nothing of that network is invalidated
//     from the bridge-service side, its cached entries are kept as they are, a warning is added
//     and the next refresh retries. The RPC result (if the fallback is enabled and succeeded) is
//     still merged, minus what is cached from the bridge service;
//   - the bridge service succeeding returns its scan (new bridges, a fresh read of every
//     non-final known one, corrections, and the non-final entries it proves gone as
//     invalidated), plus whatever bridge the RPC fallback found that the bridge service does not
//     account for (see appendRPCBridges). A warning is added only when the RPC fallback added
//     something and networkID's own bridge service is not fully caught up with its network (see
//     isNetworkSynced) — a fully synced bridge service silently absorbs it, since that would then
//     be expected (recent or reorg-affected) rather than a sign the bridge service is behind;
//   - the RPC fallback failing, finding nothing new or changed, or being disabled entirely never
//     changes or degrades the bridge-service result.
//
// Independent of the above, whenever the RPC fallback itself succeeds, any bridge it previously
// reported (Source == domain.ActivitySourceRPC in known) whose BlockNum is still within the
// window just scanned but that no longer shows up there (nor in the bridge service's listing) is
// reported back via invalidated too (see invalidatedBridges).
//
// Every network whose scan detected a reorg (a known bridge corrected or invalidated) counts it
// in the activity reorg metrics (see bridgetracker/metrics).
func (s *ActivitySource) BridgesFrom(
	ctx context.Context, fromAddress common.Address, known map[string]domain.KnownBridge,
) ([]*domain.ScannedBridge, []string, []domain.ActivityWarning, error) {
	addr := fromAddress.Hex()

	networkIDs := s.finder.NetworkIDs()
	results := make([]networkScanResult, len(networkIDs))
	maxConcurrentNetworkScans := s.maxConcurrentNetworkScans
	if maxConcurrentNetworkScans <= 0 {
		maxConcurrentNetworkScans = bridgetracker.DefaultActivitySourceBridgeServiceMaxConcurrentNetworkScans
	}
	sem := make(chan struct{}, maxConcurrentNetworkScans)
	var wg sync.WaitGroup
	wg.Add(len(networkIDs))
	for i, networkID := range networkIDs {
		sem <- struct{}{}
		go func(i int, networkID uint32) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = s.scanNetwork(ctx, networkID, addr, fromAddress, known)
		}(i, networkID)
	}
	wg.Wait()

	var all []*domain.ScannedBridge
	var invalidated []string
	var warnings []domain.ActivityWarning
	for i, networkID := range networkIDs {
		res := results[i]
		var found []*domain.ScannedBridge
		var removed []string
		corrected := 0

		if res.rpcErr == nil && s.rpc != nil {
			foundThisCall := make(map[string]struct{}, len(res.rest.listed)+len(res.rpcBridges))
			if res.restErr == nil {
				for key := range res.rest.listed {
					foundThisCall[key] = struct{}{}
				}
			}
			for _, b := range res.rpcBridges {
				foundThisCall[string(b.Bridge.GlobalIndex)] = struct{}{}
			}
			removed = append(removed, invalidatedBridges(networkID, known, res.rpcFromBlock, foundThisCall)...)
		}

		if res.restErr != nil {
			if s.rpc != nil && res.rpcErr == nil {
				found, _, corrected = appendRPCBridges(nil, res.rpcBridges, known, nil)
			}
			warnings = append(warnings, s.bridgeServiceWarning(networkID, fromAddress, res.restErr,
				s.rpc != nil && res.rpcErr == nil))
		} else {
			found = res.rest.bridges
			removed = append(removed, res.rest.removed...)
			corrected = res.rest.corrected
			if res.rpcErr == nil && len(res.rpcBridges) > 0 {
				var added, rpcCorrected int
				found, added, rpcCorrected = appendRPCBridges(found, res.rpcBridges, known, res.rest.listed)
				corrected += rpcCorrected
				if added > 0 && !isNetworkSynced(networkID, res.syncStatus) {
					warnings = append(warnings, s.warnf(networkID,
						"bridge service for network %d is not fully synchronized, some bridges may "+
							"still be missing", networkID))
				}
			}
		}

		removed = removedNotFound(removed, found)
		recordReorgMetrics(corrected, len(removed))
		all = append(all, found...)
		invalidated = append(invalidated, removed...)
	}
	return all, invalidated, warnings, nil
}

// removedNotFound deduplicates removed and drops every key found again this call (found wins:
// the bridge was re-included), so each removal is reported and counted once
func removedNotFound(removed []string, found []*domain.ScannedBridge) []string {
	if len(removed) == 0 {
		return nil
	}
	skip := make(map[string]struct{}, len(found)+len(removed))
	for _, b := range found {
		skip[string(b.Bridge.GlobalIndex)] = struct{}{}
	}
	out := make([]string, 0, len(removed))
	for _, key := range removed {
		if _, ok := skip[key]; ok {
			continue
		}
		skip[key] = struct{}{}
		out = append(out, key)
	}
	slices.Sort(out)
	return out
}

// recordReorgMetrics counts one network scan's reorg evidence (see bridgetracker/metrics)
func recordReorgMetrics(corrected, removed int) {
	if corrected == 0 && removed == 0 {
		return
	}
	metrics.IncActivityReorgsDetected()
	if corrected > 0 {
		metrics.AddActivityEntriesCorrected(corrected)
	}
	if removed > 0 {
		metrics.AddActivityEntriesRemoved(removed)
	}
}

// bridgeServiceWarning is the warning BridgesFrom reports for a network whose bridge service
// could not be scanned (see BridgesFrom): a 503 says it is temporarily unavailable — typically
// resolving a reorg — and that the last known activity is kept; otherwise the error itself, noting
// when the RPC fallback stood in for it (rpcUsed)
func (s *ActivitySource) bridgeServiceWarning(
	networkID uint32, fromAddress common.Address, err error, rpcUsed bool,
) domain.ActivityWarning {
	switch {
	case isServiceUnavailable(err):
		return s.warnf(networkID,
			"bridge service for network %d is temporarily unavailable (it may be resolving a reorg): "+
				"keeping its last known activity, retrying on the next refresh: %v", networkID, err)
	case rpcUsed:
		return s.warnf(networkID,
			"bridge service unavailable for network %d, historical activity may not be "+
				"available: %v", networkID, err)
	default:
		return s.warnf(networkID, "fetching bridges from %s on network %d: %v", fromAddress, networkID, err)
	}
}

// isServiceUnavailable reports whether err is a bridge service answering 503 — what it does while
// a syncer is temporarily inconsistent (a reorg being resolved) or unavailable
func isServiceUnavailable(err error) bool {
	var statusErr *client.HTTPStatusError
	return errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusServiceUnavailable
}

// warnf logs msg (formatted per fmt.Sprintf's rules on format/args) and turns it into the
// domain.ActivityWarning BridgesFrom reports back for networkID. Both carry the message verbatim,
// backend URLs included: operators need the real endpoint to debug, and the value only becomes
// client-facing once it is marshalled as a wire ActivityWarningItem, which is where it is redacted
// (see api.ActivityWarningItem.MarshalJSON)
func (s *ActivitySource) warnf(networkID uint32, format string, args ...any) domain.ActivityWarning {
	message := fmt.Sprintf(format, args...)
	s.logger.Warnf("activity: %s", message)
	return domain.ActivityWarning{NetworkID: networkID, Message: message}
}

// errBridgeListShifted is the scan error when a network's bridge listing kept changing while it was
// paged, in both passes of scanBridgeService
var errBridgeListShifted = errors.New(
	"the bridge listing kept changing while it was read: keeping the last known activity, retrying on the next refresh")

// bridgeServiceScan is what scanBridgeService read from one network's bridge service
type bridgeServiceScan struct {
	// bridges is every bridge to hand back to the caller: new ones, a fresh read of every known
	// non-final one (relabeled domain.ActivitySourceBridgeService, which upgrades an entry found
	// through the RPC fallback), and a known final one reported at another location (a reorg
	// deeper than the finality in use). One per GlobalIndex, newest first
	bridges []*domain.ScannedBridge
	// listed is the GlobalIndex of every bridge the bridge service listed in the pages read
	listed map[string]struct{}
	// removed is every known non-final entry of this network the listing proves gone (see
	// bridgeListScan.removedEntries)
	removed []string
	// corrected is how many known entries were re-read at a different location
	corrected int
}

// scanBridgeService reads networkID's GET /bridge/v1/bridges filtered by fromAddress. The
// listing is ordered by deposit_count descending and finality is monotonic, so only its
// non-final head can still change: the scan reads page after page until it reaches a bridge that
// is known AND final (see domain.KnownBridge.Final) with every non-final known bridge of this
// network already behind it — deeper pages are never read again — or the end of the listing.
// Every known non-final bridge is re-read (and returned with the fresh data), so the caller can
// correct it if a reorg moved it, and remove it if the listing went past its position without
// it. When more than one page was read, page 1 is read again at the end, to pick up bridges
// added while the scan paged. When the listing changed while it was paged (its count differs
// between the pages read, page 1 re-read shows a bridge the pass had not listed or listed at
// another location, or the scan read the whole listing but saw fewer bridges than it counts), a
// bridge may have shifted onto a page already read and been missed — and a missed
// non-final bridge would be read as removed — so the scan is run once more. If the listing still
// changed during that second pass, the scan fails with errBridgeListShifted: a partial result could
// remove a bridge that still exists, or return a newer final bridge while an older one was skipped,
// which the next refresh would then stop at and never find the skipped one.
//
// Any error fails the whole scan (nothing partial is returned): the caller must then keep this
// network's entries as they are, never read the failure as the bridges being gone
func scanBridgeService(
	ctx context.Context, svc *client.Client, networkID uint32, fromAddress string, pageSize uint32,
	known map[string]domain.KnownBridge, logger aggkitcommon.Logger,
) (bridgeServiceScan, error) {
	scan, complete, err := scanBridgeServiceOnce(ctx, svc, networkID, fromAddress, pageSize, known, logger)
	if err != nil || complete {
		return scan, err
	}
	logger.Infof("activity: the bridges of %s on network %d shifted while they were read, scanning again",
		fromAddress, networkID)
	scan, complete, err = scanBridgeServiceOnce(ctx, svc, networkID, fromAddress, pageSize, known, logger)
	if err != nil {
		return bridgeServiceScan{}, err
	}
	if !complete {
		return bridgeServiceScan{}, fmt.Errorf("bridges of %s on network %d: %w", fromAddress, networkID,
			errBridgeListShifted)
	}
	return scan, nil
}

// scanBridgeServiceOnce is one pass of scanBridgeService; complete is false when the listing
// changed while it was paged (see scanBridgeService)
func scanBridgeServiceOnce(
	ctx context.Context, svc *client.Client, networkID uint32, fromAddress string, pageSize uint32,
	known map[string]domain.KnownBridge, logger aggkitcommon.Logger,
) (bridgeServiceScan, bool, error) {
	if pageSize == 0 {
		pageSize = bridgetracker.DefaultActivitySourceBridgeServicePageSize
	}
	getPage := func(page uint32) (*bridgeservicetypes.BridgesResult, error) {
		res, err := svc.GetBridges(ctx, client.GetBridgesParams{
			NetworkID:   networkID,
			FromAddress: &fromAddress,
			PageNumber:  &page,
			PageSize:    &pageSize,
		})
		if errors.Is(err, client.ErrInvalidResponse) {
			return nil, fmt.Errorf("network %d page %d: %w", networkID, page, err)
		}
		if err != nil {
			return nil, err
		}
		// the client already rejects a null entry: checked again so a page is only ever read
		// whole and valid, whatever produced it
		if err := checkBridgesPage(networkID, page, res.Bridges); err != nil {
			return nil, err
		}
		return res, nil
	}

	list := newBridgeListScan(networkID, known, logger)
	pages, reachedEnd, count, countChanged := 0, false, 0, false
	observeCount := func(c int) {
		if pages > 1 && c != count {
			countChanged = true
		}
		count = c
	}
	for page := uint32(1); ; page++ {
		res, err := getPage(page)
		if err != nil {
			return bridgeServiceScan{}, false, err
		}
		pages++
		observeCount(res.Count)
		stopped := false
		for _, b := range res.Bridges {
			if list.visit(b) {
				stopped = true
				break
			}
		}
		if stopped {
			break
		}
		if uint32(len(res.Bridges)) < pageSize {
			reachedEnd = true
			break
		}
	}
	headChanged := false
	if pages > 1 {
		// bridges added while the scan paged land on page 1, which was read first
		res, err := getPage(1)
		if err != nil {
			return bridgeServiceScan{}, false, err
		}
		observeCount(res.Count)
		for _, b := range res.Bridges {
			if list.changedSinceListed(b) {
				headChanged = true
			}
			list.visit(b)
		}
	}

	// A reorg rewrites the newest bridges first, so one that replaced bridges while the scan paged
	// shows on page 1 even when the count did not change: a bridge may then have shifted onto a
	// page already read
	complete := !countChanged && !headChanged && (!reachedEnd || len(list.listed) >= count)
	return bridgeServiceScan{
		bridges: list.bridges, listed: list.listed, removed: list.removedEntries(), corrected: list.corrected,
	}, complete, nil
}

// checkBridgesPage rejects a page of a network's bridge listing holding a null entry, so the scan
// never reads, nor returns, a partially valid page
func checkBridgesPage(networkID, page uint32, bridges []*bridgeservicetypes.BridgeResponse) error {
	for i, b := range bridges {
		if b == nil {
			return fmt.Errorf("network %d page %d entry %d: %w: the bridge is null",
				networkID, page, i, client.ErrInvalidResponse)
		}
	}
	return nil
}

// bridgeListScan is the state of one pass over a network's bridge listing (see
// scanBridgeServiceOnce)
type bridgeListScan struct {
	networkID uint32
	known     map[string]domain.KnownBridge
	logger    aggkitcommon.Logger
	// pending is every known non-final entry of this network not listed yet
	pending map[string]domain.KnownBridge
	// listed is every GlobalIndex listed so far
	listed map[string]struct{}
	// emitted maps a GlobalIndex to its position in bridges
	emitted   map[string]int
	bridges   []*domain.ScannedBridge
	corrected int
	// maxDepositCount is the highest deposit count listed so far (valid when anyListed)
	maxDepositCount uint32
	anyListed       bool
}

func newBridgeListScan(
	networkID uint32, known map[string]domain.KnownBridge, logger aggkitcommon.Logger,
) *bridgeListScan {
	pending := make(map[string]domain.KnownBridge)
	for key, k := range known {
		if k.NetworkID == networkID && !k.Final {
			pending[key] = k
		}
	}
	return &bridgeListScan{
		networkID: networkID, known: known, logger: logger, pending: pending,
		listed: make(map[string]struct{}), emitted: make(map[string]int),
	}
}

// visit processes one listed bridge and reports whether the scan can stop there: b is known and
// final at the same location, and no non-final known bridge of this network is still to be
// reached further down the listing
func (l *bridgeListScan) visit(b *bridgeservicetypes.BridgeResponse) bool {
	key := string(b.GlobalIndex)
	scanned := &domain.ScannedBridge{Bridge: b, NetworkID: l.networkID, Source: domain.ActivitySourceBridgeService}
	if i, ok := l.emitted[key]; ok {
		l.bridges[i] = scanned // listed again (page 1 re-read, or pages shifted): keep the freshest read
		return false
	}
	if _, ok := l.listed[key]; ok {
		return false
	}
	l.listed[key] = struct{}{}
	if !l.anyListed || b.DepositCount > l.maxDepositCount {
		l.maxDepositCount, l.anyListed = b.DepositCount, true
	}

	prev, isKnown := l.known[key]
	if isKnown && prev.Final && prev.SameLocation(b) {
		return !l.pendingBelow(b.DepositCount)
	}
	delete(l.pending, key)
	if isKnown && !prev.SameLocation(b) {
		l.corrected++
		if prev.Final {
			l.logger.Warnf("activity: bridge with global index %s on network %d was final at tx %s block %d "+
				"but is now listed at tx %s block %d (a reorg deeper than the finality in use?): correcting it",
				key, l.networkID, prev.TxHash, prev.BlockNum, b.TxHash, b.BlockNum)
		}
	}
	l.emitted[key] = len(l.bridges)
	l.bridges = append(l.bridges, scanned)
	return false
}

// changedSinceListed reports whether b, read again on page 1 at the end of a pass, was not listed
// by the pass yet or was listed at another location (tx_hash / block_num): the listing changed
// while the pass paged. Call it before visit(b)
func (l *bridgeListScan) changedSinceListed(b *bridgeservicetypes.BridgeResponse) bool {
	key := string(b.GlobalIndex)
	if _, ok := l.listed[key]; !ok {
		return true
	}
	i, ok := l.emitted[key]
	if !ok {
		return false // a known final bridge the pass stopped at: not re-read
	}
	prev := l.bridges[i].Bridge
	return !strings.EqualFold(string(prev.TxHash), string(b.TxHash)) || prev.BlockNum != b.BlockNum
}

// pendingBelow reports whether a non-final known bridge of this network that has not been listed
// yet sits further down the listing than depositCount
func (l *bridgeListScan) pendingBelow(depositCount uint32) bool {
	for _, k := range l.pending {
		if k.DepositCount < depositCount {
			return true
		}
	}
	return false
}

// removedEntries is every known non-final entry of this network the listing proves gone. The
// pass either reached the end of the listing or stopped at a final bridge below every pending
// one, so it went past the position of every pending entry still unlisted:
//   - an entry cached from the bridge service is gone (reorged out, or its deposit count taken
//     by another sender's bridge);
//   - an entry only found through the RPC fallback is gone only once the bridge service has
//     indexed past it — it listed a later deposit of this sender on this network — since until
//     then it may simply not be indexed yet (the RPC window check, see invalidatedBridges,
//     covers it meanwhile)
func (l *bridgeListScan) removedEntries() []string {
	var removed []string
	for key, k := range l.pending {
		if k.Source == domain.ActivitySourceRPC && (!l.anyListed || l.maxDepositCount <= k.DepositCount) {
			continue
		}
		removed = append(removed, key)
	}
	slices.Sort(removed)
	return removed
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

	globalIndex, ok := parseGlobalIndex(bridge.Bridge.GlobalIndex)
	if !ok {
		return nil, fmt.Errorf("bridge tx=%s has an invalid global index %q",
			bridge.Bridge.TxHash, bridge.Bridge.GlobalIndex)
	}
	res, err := svc.GetClaims(ctx, client.GetClaimsParams{
		NetworkID:   bridge.Bridge.DestinationNetwork,
		GlobalIndex: globalIndex,
	})
	if isNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fetching claim of global index %s on network %d: %w",
			bridge.Bridge.GlobalIndex, bridge.Bridge.DestinationNetwork, err)
	}
	claim, err := selectClaim(res, claimQuery{
		globalIndex:        globalIndex,
		networkID:          bridge.NetworkID,
		destinationNetwork: bridge.Bridge.DestinationNetwork,
		depositCount:       bridge.Bridge.DepositCount,
	})
	if err != nil {
		return nil, fmt.Errorf("claims of global index %s on network %d: %w",
			bridge.Bridge.GlobalIndex, bridge.Bridge.DestinationNetwork, err)
	}
	return claim, nil
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
