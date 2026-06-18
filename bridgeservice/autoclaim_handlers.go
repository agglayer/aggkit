package bridgeservice

import (
	"context"
	"errors"
	"net/http"

	"github.com/agglayer/aggkit/autoclaim/apitypes"
	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	"github.com/agglayer/aggkit/db"
	"github.com/gin-gonic/gin"
)

// GetAutoClaimBridgesHandler lists Auto Claim bridge requests.
//
// @Summary List Auto Claim bridge requests
// @Description Returns tracked Auto Claim requests with optional filters and pagination. Available
// @Description only when the autoclaim component is running.
// @Tags autoclaim
// @Param origin_network query uint32 false "Filter by origin network ID"
// @Param destination_network query uint32 false "Filter by destination network ID"
// @Param status query string false "Filter by request status"
// @Param policy_status query string false "Filter by policy result"
// @Param policy_result query string false "Alias for policy_status"
// @Param bridge_tx_hash query string false "Filter by 0x-prefixed bridge transaction hash"
// @Param claim_tx_hash query string false "Filter by 0x-prefixed claim transaction hash"
// @Param from_block query uint64 false "Filter by minimum bridge block number"
// @Param to_block query uint64 false "Filter by maximum bridge block number"
// @Param page_number query uint32 false "Page number (default 0)"
// @Param page_size query uint32 false "Page size (default 100, max 1000)"
// @Produce json
// @Success 200 {object} apitypes.ListResponse
// @Failure 400 {object} apitypes.ErrorResponse "Bad Request"
// @Failure 500 {object} apitypes.ErrorResponse "Internal Server Error"
// @Router /autoclaim/bridges [get]
func (b *BridgeService) GetAutoClaimBridgesHandler(c *gin.Context) {
	filter, err := apitypes.ParseRequestFilter(c)
	if err != nil {
		writeAutoClaimError(c, http.StatusBadRequest, err)
		return
	}

	ctx, cancel := context.WithTimeout(c, b.readTimeout)
	defer cancel()

	page, err := b.autoClaimQuerier.ListRequests(ctx, filter)
	if err != nil {
		writeAutoClaimError(c, http.StatusInternalServerError, err)
		return
	}

	bridges := make([]apitypes.RequestResponse, 0, len(page.Requests))
	for _, request := range page.Requests {
		if request == nil {
			continue
		}
		bridges = append(bridges, apitypes.NewRequestResponse(*request))
	}
	c.JSON(http.StatusOK, apitypes.ListResponse{
		Bridges:    bridges,
		Count:      page.Count,
		PageNumber: filter.PageNumber,
		PageSize:   apitypes.EffectivePageSize(filter.PageSize),
	})
}

// GetAutoClaimBridgeHandler returns one Auto Claim bridge request by ID.
//
// @Summary Get Auto Claim bridge request
// @Description Returns one tracked Auto Claim request by request ID. Available only when the
// @Description autoclaim component is running.
// @Tags autoclaim
// @Param id path string true "Auto Claim request ID"
// @Produce json
// @Success 200 {object} apitypes.RequestResponse
// @Failure 404 {object} apitypes.ErrorResponse "Not Found"
// @Failure 500 {object} apitypes.ErrorResponse "Internal Server Error"
// @Router /autoclaim/bridges/{id} [get]
func (b *BridgeService) GetAutoClaimBridgeHandler(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c, b.readTimeout)
	defer cancel()

	key := autoclaimtypes.RequestKey(c.Param("id"))
	request, err := b.autoClaimQuerier.GetRequest(ctx, key)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeAutoClaimError(c, http.StatusNotFound, err)
			return
		}
		writeAutoClaimError(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, apitypes.NewRequestResponse(*request))
}

func writeAutoClaimError(c *gin.Context, status int, err error) {
	c.JSON(status, apitypes.ErrorResponse{Error: err.Error()})
}
