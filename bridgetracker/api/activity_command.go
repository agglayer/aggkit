package api

import (
	"net/http"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
)

// compile-time check: activityCommand fulfils the command interface
var _ command = (*activityCommand)(nil)

// activityCommand answers GET /activity/from/{from_address}: it scans every configured bridge
// service for bridges sent by from_address and reports their claim (and, optionally, tracking)
// state
type activityCommand struct {
	querier domain.ActivityQuerier
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
	// Claimed is the tri-state result of the destination bridge contract's isClaimed() call
	// the last time it was checked: "false" (confirmed unclaimed), "true" (claimed), or
	// "error" if the check itself failed (e.g. no bridge contract address configured for the
	// destination network) — callers must not read "error" as "false"
	Claimed string `json:"claimed"`
	// ClaimNetworkID is the network whose bridge service reported Claim (the bridge's
	// destination network); only present alongside Claim
	ClaimNetworkID uint32 `json:"claim_network_id,omitempty"`
	// Claim is the raw claim record, exactly as returned by the destination network's bridge
	// service, unmodified, once Claimed is true and the indexer has recorded it
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
	// keyed by which check it was — currently only "claim", present when Claimed is "error"
	Errors map[string]string `json:"errors,omitempty"`
}

// ActivityResponse is the body of GET /activity/from/{from_address}
type ActivityResponse struct {
	// FromAddress is the address requested
	FromAddress common.Address `json:"from_address"`
	// Bridges holds every bridge found for FromAddress across every configured bridge service
	Bridges []ActivityItem `json:"bridges"`
}

// Execute implements command: it scans every configured bridge service for bridges sent by the
// from_address path parameter, and reports each one's claim state. Passing
// ?includeTracking=true additionally registers every still-unclaimed bridge found with the
// bridge tracker (same effect as calling GetTxStatus for it) and includes its current tracking
// snapshot. ?filterBridges=claimed|pending|error restricts the result to only bridges with that
// claim state (default "all"); a claimed bridge excluded by "pending"/"error" never has its
// claim record fetched, so switching back to "all"/"claimed" later fetches it then.
// 200 OK unless: invalid from_address/filterBridges (ErrorData/400), or the scan itself failed
// (ErrorData/500)
//
// @Summary Get bridge activity by sender address
// @Description Scans every bridge service the tracker knows about for bridges sent by
// @Description from_address and reports each one's claim state, exactly as the bridge service
// @Description reported it. Results are cached: a bridge already known to be claimed, with its
// @Description claim record already fetched, is not rechecked on a later call. Passing
// @Description includeTracking=true additionally registers every still-unclaimed bridge with
// @Description the bridge tracker and includes its current tracking snapshot. filterBridges
// @Description restricts the result to bridges with only that claim state (claimed / still
// @Description pending / errored while checking).
// @Tags bridge-tracker
// @Produce json
// @Param from_address path string true "Address that sent the bridges to look up"
// @Param includeTracking query bool false "Register still-unclaimed bridges with the tracker"
// @Param filterBridges query string false "Which bridges to return" Enums(all, claimed, pending, error) default(all)
// @Success 200 {object} ActivityResponse
// @Failure 400 {object} types.ErrorData "Invalid from_address or filterBridges"
// @Failure 500 {object} types.ErrorData "Scanning the configured bridge services failed"
// @Router /activity/from/{from_address} [get]
func (cmd *activityCommand) Execute(c *gin.Context) (int, any, *types.ErrorData) {
	addrStr := c.Param(fromAddressParam)
	if !common.IsHexAddress(addrStr) {
		return 0, nil, &types.ErrorData{Code: http.StatusBadRequest, Message: "invalid from_address parameter"}
	}
	fromAddress := common.HexToAddress(addrStr)
	includeTracking := c.Query(includeTrackingQueryParam) == "true"

	filter, err := types.ParseActivityFilter(c.Query(filterBridgesQueryParam))
	if err != nil {
		return 0, nil, &types.ErrorData{Code: http.StatusBadRequest, Message: err.Error()}
	}

	entries, err := cmd.querier.GetActivity(c.Request.Context(), fromAddress, includeTracking, filter)
	if err != nil {
		return 0, nil, &types.ErrorData{Code: http.StatusInternalServerError, Message: err.Error()}
	}

	return http.StatusOK, ActivityResponse{FromAddress: fromAddress, Bridges: newActivityItems(entries)}, nil
}

// newActivityItems builds the wire ActivityItems from the resolved activity entries
func newActivityItems(entries []*domain.ActivityEntry) []ActivityItem {
	items := make([]ActivityItem, 0, len(entries))
	for _, e := range entries {
		item := ActivityItem{
			Bridge:               e.Bridge,
			BridgeNetworkID:      e.BridgeNetworkID,
			Claimed:              e.ClaimStatus.String(),
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
