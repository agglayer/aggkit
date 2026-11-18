package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"sort"
	"strconv"
	"strings"
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

// HTTP conditional request support of the activity endpoint (RFC 9110 §8.8.3, §13.1.2): the
// address's revision is also sent as the ETag header, and a request whose If-None-Match holds
// it is answered 304 Not Modified with no body (see activityCommand.Execute)
const (
	etagHeader        = "ETag"
	ifNoneMatchHeader = "If-None-Match"
	// cacheControlHeader is sent as cacheControlNoStore next to every ETag. The revision does
	// not cover everything in the body (finalized, last_updated_timestamp, tracking, warnings),
	// so a 304 means "nothing the revision covers changed", not "the body is byte for byte the
	// same". no-store keeps an HTTP cache (a browser's, mainly) from storing the 200 and then
	// revalidating it on its own: it would serve its stored body for every 304, and a client that
	// never asked for conditional requests would stop seeing those fields change. A client that
	// sends If-None-Match itself gets the 304 and decides what to do with it
	cacheControlHeader  = "Cache-Control"
	cacheControlNoStore = "no-store"
	// ifNoneMatchAny is the If-None-Match value matching any current representation
	ifNoneMatchAny = "*"
	// weakETagPrefix marks a weak entity tag; If-None-Match compares weakly, so it is ignored
	weakETagPrefix = "W/"
)

// Pagination defaults/limits for the activity endpoint, matching the bridge service's own
// paginated endpoints (see bridgeservice.DefaultPage/DefaultPageSize/MaxPageSize) — same
// numbers, same 1-based page numbering, so a client already paging bridge service endpoints
// needs no new mental model for this one.
const (
	defaultActivityPage     = uint32(1)
	defaultActivityPageSize = uint32(20)
	maxActivityPageSize     = uint32(200)
)

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
	// "claimed", "claimed_finalized" or "error" — the same vocabulary and field name as
	// TrackingData.ClaimStatus (see domain.TrackingData.ClaimStatus for exactly how it is
	// derived). "claimed_finalized" is the only final state: "claimed" can still go back to
	// "readyToClaim" or "pending" if a reorg removes the claim. "error" reports the destination
	// bridge contract's isClaimed() call itself failing (e.g. no bridge contract address
	// configured for the destination network) — callers must not read it as "pending". While
	// unclaimed, "readyToClaim" vs "pending" is resolved from the tracker's own snapshot when
	// Tracking is present, or directly against the bridge-service endpoints otherwise (see
	// domain.ActivityClaimChecker.IsReadyToClaim). "claimed_finalized" is "claimed" once the
	// bridge's and the claim's blocks are both final (see Finalized)
	ClaimStatus string `json:"claim_status" enums:"pending,readyToClaim,claimed,claimed_finalized,error"`
	// Finalized is true when this item can no longer be changed by a reorg: its bridge is final
	// and, if it is claimed, its claim is final too (claim_status "claimed_finalized"). Always
	// serialized. Going from false to true does not change the response's Revision. An unclaimed
	// item with a final bridge is finalized too, and goes back to false if it gets claimed, until
	// that claim is final
	Finalized bool `json:"finalized"`
	// ClaimNetworkID is the network whose bridge service reported Claim (the bridge's
	// destination network); only present alongside Claim
	ClaimNetworkID uint32 `json:"claim_network_id,omitempty"`
	// Claim is the raw claim record, exactly as returned by the destination network's bridge
	// service, unmodified, once ClaimStatus is "claimed" and the indexer has recorded it
	Claim *bridgeservicetypes.ClaimResponse `json:"claim,omitempty"`
	// CreationTimestamp is when this bridge was created (unix seconds): its origin deposit block
	// timestamp, or when the activity endpoint first cached it if the bridge service had not
	// populated that timestamp yet; it only changes again if a reorg moves the bridge to another
	// block or transaction
	CreationTimestamp uint64 `json:"creation_timestamp"`
	// LastUpdatedTimestamp is when this item's claim/tracking state was last (re)checked (unix
	// seconds), whether or not anything about it actually changed. Stops advancing once the item
	// is settled (claim_status "claimed_finalized") — nothing left to recheck
	LastUpdatedTimestamp uint64 `json:"last_updated_timestamp"`
	// Tracking is the bridge tracker's current status for this bridge; only present when the
	// request set includeTracking=true and the bridge is still unclaimed
	Tracking *TrackingData `json:"tracking,omitempty"`
	// Errors holds the message of whatever check failed the last time this item was refreshed,
	// keyed by which check it was — currently only "claim", present when ClaimStatus is "error"
	Errors map[string]string `json:"errors,omitempty"`
}

// MarshalJSON is the implementation of the json.Marshaler interface. It redacts any URL,
// host:port, IP address or DNS name from every Errors value. domain.ActivityEntry.Errors holds
// the raw error strings on purpose (they are what the refresh loop logs, where operators need the
// real endpoint); this is the layer that makes them client-safe
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
// host:port, IP address or DNS name from Message. domain.ActivityWarning.Message holds the raw
// message on purpose (sources.ActivitySource.warnf logs the same string verbatim, where operators
// need the real endpoint); this is the layer that makes it client-safe
func (w ActivityWarningItem) MarshalJSON() ([]byte, error) {
	w.Message = aggkitcommon.RedactSensitive(w.Message)
	type activityWarningItemAlias ActivityWarningItem
	return json.Marshal(activityWarningItemAlias(w))
}

// ActivityResponse is the body of GET /activity/from/{from_address}
type ActivityResponse struct {
	// FromAddress is the address requested
	FromAddress common.Address `json:"from_address"`
	// Bridges holds this page of the bridges found for FromAddress across every configured
	// bridge service, most recent first (see CreationTimestamp), restricted to filterBridges
	// and sliced to page_number/page_size
	Bridges []ActivityItem `json:"bridges"`
	// Count is how many bridges matched filterBridges in total, across every page — not just
	// len(Bridges) — mirroring bridgeservice's BridgesResult/ClaimsResult "count" field
	Count int `json:"count"`
	// Revision is FromAddress's revision (see domain.ActivityRevisionReader), an opaque string
	// that is a hash of the address's activity (e.g. "v1:" followed by hex), present in every
	// response whatever filterBridges/page_number/page_size say: it covers all of the address's
	// bridges, not just this page. It changes when a bridge is added or removed, changes
	// claim_status or is corrected after a reorg — wherever it sits in the sorted result — and it
	// does NOT change when an item only goes from finalized=false to finalized=true. It is the
	// same after a restart and on every instance holding the same data. A client polls it with a
	// cheap request (page_size=1), reloads the pages it has loaded when it differs from the one
	// it holds, and checks it again after the last page; compare it for equality only. Always
	// serialized; the empty-activity value "v1:" followed by 64 zeros for an address with no
	// bridges
	Revision string `json:"revision" example:"v1:0000000000000000000000000000000000000000000000000000000000000000"`
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
// with Retry-After), or registering otherwise failed (ErrorData/500).
//
// Conditional requests: the response's revision is also sent as its ETag header (the revision
// between double quotes, a strong entity tag). A request carrying it in If-None-Match (or "*") is
// answered 304 Not Modified with the ETag and no body, once from_address is ready, without
// calling GetActivity (RegisterAndAwait has already counted the request as an access). Like the revision, the ETag is the same whatever filterBridges/
// page_number/page_size say, and a 304 only tells that nothing the revision covers changed (see
// cacheControlHeader). Everything before that point (validation, flush_cache, registering
// from_address with includeTracking) runs exactly as for a 200
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
// @Description whole request. Bridges are sorted most recent first and paginated via
// @Description page_number/page_size (same names and 1-based numbering as the bridge service's
// @Description own paginated endpoints); "count" reports how many bridges matched in total,
// @Description across every page. "revision" is an opaque string, a hash of the address's
// @Description activity: it changes when a bridge is added or removed, changes claim_status or is
// @Description corrected after a reorg, wherever it sits in the sorted result, and does not change
// @Description when an item only becomes finalized. It is the same after a restart and on every
// @Description instance holding the same data. Poll it with a cheap request (page_size=1), reload
// @Description the loaded pages when it changed, and check it again after the last page. Compare
// @Description it only for equality. The same value is sent as the ETag response header (between
// @Description double quotes): a request carrying it in If-None-Match is answered 304 Not Modified
// @Description with no body while the revision is unchanged, whatever the filter or the page. A 304
// @Description does not tell whether finalized, last_updated_timestamp, tracking or warnings changed.
// @Tags bridge-tracker
// @Produce json
// @Param from_address path string true "Address that sent the bridges to look up"
// @Param includeTracking query bool false "Register still-unclaimed bridges with the tracker"
// @Param filterBridges query string false "Claim filter" Enums(all, claimed, pending, readyToClaim, error) default(all)
// @Param flush_cache query bool false "Discard cached activity for from_address before answering"
// @Param page_number query uint32 false "Page number, 1-based (default 1)"
// @Param page_size query uint32 false "Page size (default 20, max 200)"
// @Param If-None-Match header string false "ETag of a previous response (the revision between double quotes)"
// @Success 200 {object} ActivityResponse
// @Success 304 "Not Modified: the revision is the one sent in If-None-Match, no body"
// @Header 200,304 {string} ETag "The revision between double quotes (strong entity tag)"
// @Header 200,304 {string} Cache-Control "no-store"
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

	// Validate before mutating anything: a bad filterBridges/page_number/page_size value must
	// 400 without having already discarded the cache below (flush_cache=true&filterBridges=typo
	// used to flush first and reject after, forcing the follow-up request through the same
	// not-ready-yet path as a first-time registration just to fix a client-side typo)
	filter, err := types.ParseActivityFilter(c.Query(filterBridgesQueryParam))
	if err != nil {
		return 0, nil, &types.ErrorData{Code: http.StatusBadRequest, Message: aggkitcommon.RedactError(err)}
	}

	pageNumber, pageSize, err := parseActivityPageParams(c)
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

	// the revision is read BEFORE the entries (see domain.ActivityRevisionReader): a refresh landing
	// in between can only make the entries newer than the revision reported, never older
	revision, hasRevision, err := cmd.activityRevision(c.Request.Context(), fromAddress)
	if err != nil {
		return 0, nil, &types.ErrorData{Code: http.StatusInternalServerError, Message: aggkitcommon.RedactError(err)}
	}
	// without a real revision (a registry that keeps none) there is no validator to offer: the
	// constant empty revision would answer 304 forever
	if hasRevision && ifNoneMatchHolds(c.Request.Header.Values(ifNoneMatchHeader), activityETag(revision)) {
		setActivityValidatorHeaders(c, revision)
		return http.StatusNotModified, nil, nil
	}

	entries, warnings, err := cmd.registry.GetActivity(c.Request.Context(), fromAddress, includeTracking, filter)
	if err != nil {
		return 0, nil, &types.ErrorData{Code: http.StatusInternalServerError, Message: aggkitcommon.RedactError(err)}
	}

	// only now: an error response must not carry the ETag of a body the client never received
	if hasRevision {
		setActivityValidatorHeaders(c, revision)
	}
	return http.StatusOK, ActivityResponse{
		FromAddress: fromAddress,
		Bridges:     newActivityItems(paginateActivityEntries(entries, pageNumber, pageSize)),
		Count:       len(entries),
		Revision:    revision,
		Warnings:    newActivityWarningItems(warnings),
	}, nil
}

// activityRevision is fromAddress's revision as served by the registry when it implements
// domain.ActivityRevisionReader (kept true), and domain.EmptyActivityRevision with kept false
// when it does not: that constant says nothing about the address's activity
func (cmd *activityCommand) activityRevision(
	ctx context.Context, fromAddress common.Address,
) (revision string, kept bool, err error) {
	reader, ok := cmd.registry.(domain.ActivityRevisionReader)
	if !ok {
		return domain.EmptyActivityRevision, false, nil
	}
	revision, err = reader.GetActivityRevision(ctx, fromAddress)
	return revision, true, err
}

// activityETag is revision as an entity tag: the opaque string between double quotes, a strong
// validator in RFC 9110 terms (see cacheControlHeader for what it does and does not cover)
func activityETag(revision string) string {
	return `"` + revision + `"`
}

// setActivityValidatorHeaders sets the headers a 200 and a 304 of the activity endpoint share:
// revision as the ETag, and Cache-Control: no-store (see cacheControlHeader)
func setActivityValidatorHeaders(c *gin.Context, revision string) {
	c.Header(etagHeader, activityETag(revision))
	c.Header(cacheControlHeader, cacheControlNoStore)
}

// ifNoneMatchHolds reports whether the If-None-Match header lines of a request hold etag, so the
// request is answered 304: any of the listed entity tags is etag, compared weakly as RFC 9110
// §13.1.2 mandates for this header (a W/ prefix is ignored), or the header is "*" (any current
// representation; there always is one at this point). No header, or only other tags, is false
func ifNoneMatchHolds(headerLines []string, etag string) bool {
	for _, line := range headerLines {
		// an entity tag of this endpoint has no comma, so splitting the list on commas cannot
		// make a foreign tag look like etag
		for _, candidate := range strings.Split(line, ",") {
			candidate = strings.TrimSpace(candidate)
			if candidate == ifNoneMatchAny || strings.TrimPrefix(candidate, weakETagPrefix) == etag {
				return true
			}
		}
	}
	return false
}

// parseActivityPageParams parses and validates the activity endpoint's page_number/page_size
// query parameters, same names, defaults and bounds as the bridge service's own paginated
// endpoints (see bridgeservice.parseUintQuery/validatePaginationParams): page_number defaults to
// 1 and must be > 0; page_size defaults to 20 and must be in (0, 200]
func parseActivityPageParams(c *gin.Context) (pageNumber, pageSize uint32, err error) {
	pageNumber, err = parseActivityUintQuery(c, pageNumberQueryParam, defaultActivityPage)
	if err != nil {
		return 0, 0, err
	}
	if pageNumber == 0 {
		return 0, 0, fmt.Errorf("invalid %s parameter: must be greater than 0", pageNumberQueryParam)
	}

	pageSize, err = parseActivityUintQuery(c, pageSizeQueryParam, defaultActivityPageSize)
	if err != nil {
		return 0, 0, err
	}
	if pageSize == 0 || pageSize > maxActivityPageSize {
		return 0, 0, fmt.Errorf(
			"invalid %s parameter: must be greater than 0 and less than or equal to %d", pageSizeQueryParam, maxActivityPageSize)
	}

	return pageNumber, pageSize, nil
}

// parseActivityUintQuery parses key as a uint32 query parameter, returning defaultVal if it is
// absent
func parseActivityUintQuery(c *gin.Context, key string, defaultVal uint32) (uint32, error) {
	paramStr := c.Query(key)
	if paramStr == "" {
		return defaultVal, nil
	}
	val, err := strconv.ParseUint(paramStr, decimalBase, uint32BitSize)
	if err != nil {
		return 0, fmt.Errorf("invalid %s parameter: %w", key, err)
	}
	return uint32(val), nil
}

// paginateActivityEntries sorts entries most recent first (by CreatedAt, descending — see
// domain.ActivityEntry.CreatedAt — then by global index, descending, so entries sharing a
// CreatedAt keep the same relative order across requests) and returns the pageNumber-th page
// of pageSize entries (both 1-based/positive, as validated by parseActivityPageParams). A
// pageNumber past the end of entries returns an empty, non-nil slice rather than an error: the
// caller already has the true total in ActivityResponse.Count to tell "no more pages" from "no
// activity at all"
func paginateActivityEntries(entries []*domain.ActivityEntry, pageNumber, pageSize uint32) []*domain.ActivityEntry {
	sortActivityEntries(entries)

	offset := uint64(pageNumber-1) * uint64(pageSize)
	if offset >= uint64(len(entries)) {
		return []*domain.ActivityEntry{}
	}
	end := min(offset+uint64(pageSize), uint64(len(entries)))
	return entries[offset:end]
}

// sortActivityEntries sorts entries in place by CreatedAt descending, breaking ties by bridge
// global index descending — compared numerically, since it can exceed 2^64 — so entries sharing
// a CreatedAt have the same relative order on every request. Each global index is parsed once,
// before sorting, rather than on every comparison
func sortActivityEntries(entries []*domain.ActivityEntry) {
	type keyed struct {
		entry       *domain.ActivityEntry
		globalIndex *big.Int
	}
	keyedEntries := make([]keyed, len(entries))
	for i, e := range entries {
		gi, ok := new(big.Int).SetString(string(e.Bridge.GlobalIndex), decimalBase)
		if !ok {
			gi = new(big.Int)
		}
		keyedEntries[i] = keyed{entry: e, globalIndex: gi}
	}
	sort.Slice(keyedEntries, func(i, j int) bool {
		a, b := keyedEntries[i], keyedEntries[j]
		if !a.entry.CreatedAt.Equal(b.entry.CreatedAt) {
			return a.entry.CreatedAt.After(b.entry.CreatedAt)
		}
		return a.globalIndex.Cmp(b.globalIndex) > 0
	})
	for i, k := range keyedEntries {
		entries[i] = k.entry
	}
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
			Finalized:            e.Finalized,
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
