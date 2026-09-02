package api

import (
	"net/http"
	"strconv"

	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
)

// compile-time check: bridgeAddressCommand fulfils the command interface
var _ command = (*bridgeAddressCommand)(nil)

// bridgeAddressCommand answers GET /bridge-address and GET /bridge-address/{network_id}: with
// no network_id it reports the bridge contract address of every network the resolver currently
// knows about, and with one it reports only that network's
type bridgeAddressCommand struct {
	resolver domain.BridgeAddressResolver
}

// BridgeAddressItem is the bridge contract address of one network
type BridgeAddressItem struct {
	// NetworkID is the network BridgeAddress belongs to
	NetworkID uint32 `json:"network_id"`
	// BridgeAddress is the bridge contract address on NetworkID
	BridgeAddress common.Address `json:"bridge_address"`
}

// BridgeAddressResponse is the body of GET /bridge-address
type BridgeAddressResponse struct {
	// Bridges holds the bridge contract address of every network currently known
	Bridges []BridgeAddressItem `json:"bridges"`
}

// Execute implements command: with no network_id path parameter it resolves the bridge
// contract address of every network the resolver currently knows about (BridgeAddressResponse);
// with one it resolves only that network's (BridgeAddressItem). 200 OK unless: invalid
// network_id (ErrorData/400), or resolving the address failed (ErrorData/500)
//
// @Summary Get the bridge contract address of one network, or every network
// @Description With no network_id, reports the bridge contract address of every network the
// @Description tracker currently knows about (via the bridge service finder). With network_id,
// @Description reports only that network's.
// @Tags bridge-tracker
// @Produce json
// @Param network_id path int false "Network to look up; omit to get every network"
// @Success 200 {object} BridgeAddressResponse "Body when network_id is omitted"
// @Success 200 {object} BridgeAddressItem "Body when network_id is set"
// @Failure 400 {object} types.ErrorData "Invalid network_id"
// @Failure 500 {object} types.ErrorData "Resolving the bridge contract address failed"
// @Router /bridge-address [get]
// @Router /bridge-address/{network_id} [get]
func (cmd *bridgeAddressCommand) Execute(c *gin.Context) (int, any, *types.ErrorData) {
	networkIDStr := c.Param(networkIDParam)
	if networkIDStr == "" {
		return cmd.executeAll(c)
	}

	networkID, err := strconv.ParseUint(networkIDStr, decimalBase, uint32BitSize)
	if err != nil {
		return 0, nil, &types.ErrorData{Code: http.StatusBadRequest, Message: "invalid network_id parameter"}
	}

	addr, err := cmd.resolver.BridgeAddress(c.Request.Context(), uint32(networkID))
	if err != nil {
		return 0, nil, &types.ErrorData{Code: http.StatusInternalServerError, Message: err.Error()}
	}

	return http.StatusOK, BridgeAddressItem{NetworkID: uint32(networkID), BridgeAddress: addr}, nil
}

// executeAll resolves the bridge contract address of every network cmd.resolver currently
// knows about (see domain.BridgeAddressResolver.NetworkIDs)
func (cmd *bridgeAddressCommand) executeAll(c *gin.Context) (int, any, *types.ErrorData) {
	networkIDs := cmd.resolver.NetworkIDs()
	items := make([]BridgeAddressItem, 0, len(networkIDs))
	for _, networkID := range networkIDs {
		addr, err := cmd.resolver.BridgeAddress(c.Request.Context(), networkID)
		if err != nil {
			return 0, nil, &types.ErrorData{Code: http.StatusInternalServerError, Message: err.Error()}
		}
		items = append(items, BridgeAddressItem{NetworkID: networkID, BridgeAddress: addr})
	}

	return http.StatusOK, BridgeAddressResponse{Bridges: items}, nil
}
