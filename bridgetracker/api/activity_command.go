package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
)

// compile-time check: activityCommand fulfils the command interface
var _ command = (*activityCommand)(nil)

// retryAfterHeader is the standard HTTP header telling the client how long to wait before
// retrying a 503 response (RFC 9110 §10.2.3), expressed here in whole seconds
const retryAfterHeader = "Retry-After"

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
	// pollInterval is the activity engine's own background refresh cadence (see
	// bridgetracker.ActivityEngineConfig.PollInterval), reported as the Retry-After value of a
	// 503 response for an address whose first refresh has not completed yet — the client's next
	// request has a real chance of finding it ready by then, instead of retrying blindly
	pollInterval time.Duration
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

// MarshalJSON is the implementation of the json.Marshaler interface. It redacts any URL,
// host:port, IP address or DNS name from every Errors value defensively — domain.ActivityEntry.
// Errors is already redacted at construction (see ActivityCache.refresh), this is the last line
// of defense for any caller that does not
func (i ActivityItem) MarshalJSON() ([]byte, error) {
	i.Errors = redactActivityErrors(i.Errors)
	type activityItemAlias ActivityItem
	return json.Marshal(activityItemAlias(i))
}

// redactActivityErrors returns a new map with aggkitcommon.RedactSensitive applied to every
// value of errs, or nil when errs is nil (so ActivityItem.Errors stays omitted from the wire
// response when there is nothing to report — see its "omitempty" tag). It never mutates errs.
func redactActivityErrors(errs map[string]string) map[string]string {
	if errs == nil {
		return nil
	}
	redacted := make(map[string]string, len(errs))
	for k, v := range errs {
		redacted[k] = aggkitcommon.RedactSensitive(v)
	}
	return redacted
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

// MarshalJSON is the implementation of the json.Marshaler interface. It redacts any URL,
// host:port, IP address or DNS name from Message defensively — domain.ActivityWarning.Message is
// already redacted at construction (see sources.ActivitySource.warnf), this is the last line of
// defense for any caller that does not
func (w ActivityWarningItem) MarshalJSON() ([]byte, error) {
	w.Message = aggkitcommon.RedactSensitive(w.Message)
	type activityWarningItemAlias ActivityWarningItem
	return json.Marshal(activityWarningItemAlias(w))
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
// gives a freshly registered tx. If that first refresh has still not completed by the time the
// wait ends (a slow scan, or the engine falling behind), Execute answers 503 with a Retry-After
// header set to the engine's own poll interval, instead of a 200 with an empty result that would
// be indistinguishable from "this address genuinely has no activity" — the client is expected to
// retry after that interval, by which point the engine's regular cadence should have caught up
// even if the initial wait did not. Once ready, it reports whatever is currently cached for
// from_address. Passing ?includeTracking=true additionally marks the address as wanting tracking
// enrichment: every still-unclaimed bridge found for it is registered with the bridge tracker
// (same effect as calling GetTxStatus for it) by a following background refresh, and its current
// tracking snapshot included once that has happened. ?filterBridges=claimed|pending|readyToClaim|
// error restricts the result to only bridges with that claim state (default "all"); the
// background refresh always fetches a claimed bridge's claim record regardless of this filter,
// so switching to "all"/"claimed" later never needs a fresh fetch for it. ?flush_cache=true
// discards whatever is already cached for from_address, so the next background refresh rechecks
// everything from scratch instead of reusing cached state — the response to the very same
// request that set it goes through the same not-ready-yet 503 path as a first-time registration,
// since flushing resets from_address's readiness exactly like never having been requested
// before. A network whose bridge service could not be scanned never fails the request: it is
// skipped and reported in the "warnings" field instead, so Bridges is still whatever every other
// network reported. 200 OK unless: invalid from_address/filterBridges (ErrorData/400), the
// address registry is at capacity or from_address is not ready yet (ErrorData/503, the latter
// with Retry-After), or registering otherwise failed (ErrorData/500)
//
// @Summary Get bridge activity by sender address
// @Description Registers from_address as supervised and reports whatever the activity engine's
// @Description background refresh has cached for it so far — on a first-time address, this
// @Description request waits briefly for that first refresh before answering, and answers 503
// @Description with a Retry-After header (the engine's poll interval) if it is still not ready
// @Description by then, instead of an empty 200 indistinguishable from "no activity" (see
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
// @Failure 503 {object} types.ErrorData "Registry at capacity, or from_address not ready yet (see Retry-After)"
// @Router /activity/from/{from_address} [get]
func (cmd *activityCommand) Execute(c *gin.Context) (int, any, *types.ErrorData) {
	addrStr := c.Param(fromAddressParam)
	if !common.IsHexAddress(addrStr) {
		return 0, nil, &types.ErrorData{Code: http.StatusBadRequest, Message: "invalid from_address parameter"}
	}
	fromAddress := common.HexToAddress(addrStr)
	includeTracking := c.Query(includeTrackingQueryParam) == queryValueTrue

	// Validate before mutating anything: a bad filterBridges value must 400 without having
	// already discarded the cache below (flush_cache=true&filterBridges=typo used to flush first
	// and reject after, forcing the follow-up request through the same not-ready-yet path as a
	// first-time registration just to fix a client-side typo)
	filter, err := types.ParseActivityFilter(c.Query(filterBridgesQueryParam))
	if err != nil {
		return 0, nil, &types.ErrorData{Code: http.StatusBadRequest, Message: aggkitcommon.RedactError(err)}
	}

	if c.Query(flushCacheQueryParam) == queryValueTrue {
		cmd.registry.FlushActivity(fromAddress)
	}

	// includeTracking is threaded into RegisterAndAwait itself, not only the GetActivity call
	// below: RegisterAndAwait sets the sticky flag before signalling the engine's trigger, so
	// even this very first refresh enriches tracking — see domain.ActivitySupervisedStore.
	// RegisterAndAwait's doc. Without this, a client polling with both includeTracking=true and
	// flush_cache=true (the natural "give me fresh data including tracking" call) could never
	// observe Tracking: the flush above resets the flag, and GetActivity only sets it again after
	// this refresh has already run with tracking disabled.
	ready, err := cmd.registry.RegisterAndAwait(fromAddress, includeTracking, cmd.resolveTimeout)
	if err != nil {
		if errors.Is(err, domain.ErrActivityRegistryFull) {
			return 0, nil, &types.ErrorData{
				Code: http.StatusServiceUnavailable, Message: aggkitcommon.RedactError(err),
			}
		}
		return 0, nil, &types.ErrorData{Code: http.StatusInternalServerError, Message: aggkitcommon.RedactError(err)}
	}
	if !ready {
		// from_address was only just registered and its first background refresh has not
		// completed yet (see domain.ActivitySupervisedStore.RegisterAndAwait) — answering with
		// an empty ActivityResponse here would be indistinguishable from "this address genuinely
		// has no activity", so report 503 instead, with Retry-After set to the engine's own
		// refresh cadence: the next request has a real chance of landing after that refresh
		c.Header(retryAfterHeader, strconv.Itoa(int(cmd.pollInterval.Round(time.Second).Seconds())))
		return 0, nil, &types.ErrorData{
			Code:    http.StatusServiceUnavailable,
			Message: "activity for this address is not ready yet, retry after " + cmd.pollInterval.String(),
		}
	}

	entries, warnings, err := cmd.registry.GetActivity(c.Request.Context(), fromAddress, includeTracking, filter)
	if err != nil {
		return 0, nil, &types.ErrorData{Code: http.StatusInternalServerError, Message: aggkitcommon.RedactError(err)}
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
