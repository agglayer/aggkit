package api

import (
	"errors"
	"net/http"
	"time"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
)

// compile-time check: activityCommand fulfils the command interface
var _ command = (*activityCommand)(nil)

// activityCommand answers GET /activity/from/{from_address}: it registers from_address as
// supervised (see domain.ActivitySupervisedStore.RegisterAndAwait), then reports whatever is
// cached for it — claim state and, optionally, tracking state — same idea as
// getTxStatusCommand, just over from_addresses instead of tracked tx IDs
type activityCommand struct {
	registry domain.ActivityRegistry
	// resolveTimeout is how long Execute waits, the first time a from_address is registered,
	// for the activity engine's immediate refresh attempt to complete before answering (see
	// domain.ActivitySupervisedStore.RegisterAndAwait); <= 0 disables the wait
	resolveTimeout time.Duration
}

// ActivityItem is one bridge found for the requested from_address. Bridge and Claim are the
// bridge service's own response shapes (see bridgeservice/types), reported exactly as-is
// rather than remapped into a bespoke model; BridgeNetworkID/ClaimNetworkID sit alongside them
// (not inside) since the caller needs to know which bridge service produced each one
type ActivityItem struct {
	// Bridge is the raw bridge event, exactly as returned by the origin network's bridge
	// service, unmodified
	Bridge *bridgeservicetypes.BridgeResponse `json:"bridge"`
	// BridgeNetworkID is the network whose bridge service reported Bridge — not necessarily
	// Bridge.OriginNetwork, which is the origin network of the bridged asset and can differ for
	// a re-bridged asset (see domain.ScannedBridge)
	BridgeNetworkID uint32 `json:"bridge_network_id"`
	// Source is which system supplied Bridge as of the last time it was (re)scanned: "bridge"
	// (the network's own bridge service — the default, and the source of record whenever it is
	// available) or "rpc" (the RPC-based fallback, only ever used while the bridge service had
	// not indexed this bridge yet, or could not be reached — see domain.ActivitySourceKind)
	Source string `json:"source"`
	// ClaimStatus is a simplified claim-readiness summary, one of "pending", "readyToClaim",
	// "claimed" or "error" — the same vocabulary and field name as TrackingData.ClaimStatus (see
	// domain.TrackingData.ClaimStatus for exactly how it is derived). "error" reports the
	// destination bridge contract's isClaimed() call itself failing (e.g. no bridge contract
	// address configured for the destination network) — callers must not read it as "pending".
	// While unclaimed, "readyToClaim" vs "pending" is resolved from the tracker's own snapshot
	// when Tracking is present, or directly against the bridge-service endpoints otherwise (see
	// domain.ActivityClaimChecker.IsReadyToClaim)
	ClaimStatus string `json:"claim_status"`
	// ClaimNetworkID is the network whose bridge service reported Claim (the bridge's
	// destination network); only present alongside Claim
	ClaimNetworkID uint32 `json:"claim_network_id,omitempty"`
	// Claim is the raw claim record, exactly as returned by the destination network's bridge
	// service, unmodified, once ClaimStatus is "claimed" and the indexer has recorded it
	Claim *bridgeservicetypes.ClaimResponse `json:"claim,omitempty"`
	// CreationTimestamp is when this bridge was first cached by the activity endpoint (unix
	// seconds); it never changes after that
	CreationTimestamp uint64 `json:"creation_timestamp"`
	// LastUpdatedTimestamp is when this item's claim/tracking state was last (re)checked (unix
	// seconds), whether or not anything about it actually changed. Stops advancing once the
	// bridge is claimed with its claim record fetched — nothing left to recheck
	LastUpdatedTimestamp uint64 `json:"last_updated_timestamp"`
	// Tracking is the bridge tracker's current status for this bridge; only present when the
	// request set includeTracking=true and the bridge is still unclaimed
	Tracking *TrackingData `json:"tracking,omitempty"`
	// Errors holds the message of whatever check failed the last time this item was refreshed,
	// keyed by which check it was — currently only "claim", present when ClaimStatus is "error"
	Errors map[string]string `json:"errors,omitempty"`
}

// ActivityWarningItem reports one network's bridge service that could not be scanned while
// building this response — Bridges is still whatever every other network reported, just
// possibly incomplete for the networks listed here
type ActivityWarningItem struct {
	// NetworkID is the network whose bridge service could not be scanned
	NetworkID uint32 `json:"network_id"`
	// Message is the error encountered while scanning NetworkID
	Message string `json:"message"`
}

// ActivityResponse is the body of GET /activity/from/{from_address}
type ActivityResponse struct {
	// FromAddress is the address requested
	FromAddress common.Address `json:"from_address"`
	// Bridges holds every bridge found for FromAddress across every configured bridge service
	Bridges []ActivityItem `json:"bridges"`
	// Warnings lists every network whose bridge service could not be scanned this call; absent
	// when every configured network was scanned successfully. Bridges may be incomplete for the
	// networks listed here, but is still valid for every other network
	Warnings []ActivityWarningItem `json:"warnings,omitempty"`
}

// Execute implements command: it registers the from_address path parameter as supervised (see
// domain.ActivitySupervisedStore.RegisterAndAwait) — on a first-time address, this waits up to
// the configured resolveTimeout for the activity engine's background scan of every configured
// bridge service to complete before answering, the same head-start idea getTxStatusCommand
// gives a freshly registered tx — then reports whatever is currently cached for it. Passing
// ?includeTracking=true additionally marks the address as wanting tracking enrichment: every
// still-unclaimed bridge found for it is registered with the bridge tracker (same effect as
// calling GetTxStatus for it) by a following background refresh, and its current tracking
// snapshot included once that has happened. ?filterBridges=claimed|pending|readyToClaim|error
// restricts the result to only bridges with that claim state (default "all"); the background
// refresh always fetches a claimed bridge's claim record regardless of this filter, so switching
// to "all"/"claimed" later never needs a fresh fetch for it. ?flush_cache=true discards whatever
// is already cached for from_address, so the next background refresh rechecks everything from
// scratch instead of reusing cached state — the response to the very same request that set it is
// still whatever (if anything) is cached at that point, exactly like a first-time registration
// waiting out resolveTimeout with nothing yet to show. A network whose bridge service could not
// be scanned never fails the request: it is skipped and reported in the "warnings" field instead,
// so Bridges is still whatever every other network reported. 200 OK unless: invalid
// from_address/filterBridges (ErrorData/400), the address registry is at capacity
// (ErrorData/503), or registering otherwise failed (ErrorData/500)
//
// @Summary Get bridge activity by sender address
// @Description Registers from_address as supervised and reports whatever the activity engine's
// @Description background refresh has cached for it so far — on a first-time address, this
// @Description request waits briefly for that first refresh before answering (see
// @Description RegisterResolveTimeout-equivalent config). Results are cached: a bridge already
// @Description known to be claimed, with its claim record already fetched, is not rechecked on a
// @Description later refresh. Passing includeTracking=true additionally marks the address so a
// @Description following background refresh registers every still-unclaimed bridge with the
// @Description bridge tracker and includes its current tracking snapshot. filterBridges
// @Description restricts the result to bridges with only that claim state (claimed / still
// @Description pending / ready to claim / errored while checking). flush_cache=true discards
// @Description whatever is already cached for from_address, so the next background refresh
// @Description rechecks everything from scratch. A network whose bridge service could not be
// @Description scanned is skipped and reported in the "warnings" field instead of failing the
// @Description whole request.
// @Tags bridge-tracker
// @Produce json
// @Param from_address path string true "Address that sent the bridges to look up"
// @Param includeTracking query bool false "Register still-unclaimed bridges with the tracker"
// @Param filterBridges query string false "Claim filter" Enums(all, claimed, pending, readyToClaim, error) default(all)
// @Param flush_cache query bool false "Discard cached activity for from_address before answering"
// @Success 200 {object} ActivityResponse
// @Failure 400 {object} types.ErrorData "Invalid from_address or filterBridges"
// @Failure 500 {object} types.ErrorData "Registering from_address failed"
// @Failure 503 {object} types.ErrorData "The supervised address registry is at capacity"
// @Router /activity/from/{from_address} [get]
func (cmd *activityCommand) Execute(c *gin.Context) (int, any, *types.ErrorData) {
	addrStr := c.Param(fromAddressParam)
	if !common.IsHexAddress(addrStr) {
		return 0, nil, &types.ErrorData{Code: http.StatusBadRequest, Message: "invalid from_address parameter"}
	}
	fromAddress := common.HexToAddress(addrStr)
	includeTracking := c.Query(includeTrackingQueryParam) == queryValueTrue

	if c.Query(flushCacheQueryParam) == queryValueTrue {
		cmd.registry.FlushActivity(fromAddress)
	}

	filter, err := types.ParseActivityFilter(c.Query(filterBridgesQueryParam))
	if err != nil {
		return 0, nil, &types.ErrorData{Code: http.StatusBadRequest, Message: err.Error()}
	}

	if err := cmd.registry.RegisterAndAwait(fromAddress, cmd.resolveTimeout); err != nil {
		if errors.Is(err, domain.ErrActivityRegistryFull) {
			return 0, nil, &types.ErrorData{Code: http.StatusServiceUnavailable, Message: err.Error()}
		}
		return 0, nil, &types.ErrorData{Code: http.StatusInternalServerError, Message: err.Error()}
	}

	entries, warnings, err := cmd.registry.GetActivity(c.Request.Context(), fromAddress, includeTracking, filter)
	if err != nil {
		return 0, nil, &types.ErrorData{Code: http.StatusInternalServerError, Message: err.Error()}
	}

	return http.StatusOK, ActivityResponse{
		FromAddress: fromAddress,
		Bridges:     newActivityItems(entries),
		Warnings:    newActivityWarningItems(warnings),
	}, nil
}

// newActivityItems builds the wire ActivityItems from the resolved activity entries
func newActivityItems(entries []*domain.ActivityEntry) []ActivityItem {
	items := make([]ActivityItem, 0, len(entries))
	for _, e := range entries {
		item := ActivityItem{
			Bridge:               e.Bridge,
			BridgeNetworkID:      e.BridgeNetworkID,
			Source:               string(e.Source),
			ClaimStatus:          e.TrackerClaimStatus.String(),
			Errors:               e.Errors,
			CreationTimestamp:    uint64(e.CreatedAt.Unix()),
			LastUpdatedTimestamp: uint64(e.UpdatedAt.Unix()),
		}
		if e.Claim != nil {
			item.Claim = e.Claim
			item.ClaimNetworkID = e.Bridge.DestinationNetwork
		}
		if e.Tracking != nil {
			tracking := trackingDataFrom(e.Tracking)
			item.Tracking = &tracking
		}
		items = append(items, item)
	}
	return items
}

// newActivityWarningItems builds the wire ActivityWarningItems from the scan's warnings; nil in
// (every network scanned fine) yields nil out, so Warnings is omitted from the response entirely
func newActivityWarningItems(warnings []domain.ActivityWarning) []ActivityWarningItem {
	if len(warnings) == 0 {
		return nil
	}
	items := make([]ActivityWarningItem, 0, len(warnings))
	for _, w := range warnings {
		items = append(items, ActivityWarningItem{NetworkID: w.NetworkID, Message: w.Message})
	}
	return items
}
