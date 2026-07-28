package api

import (
	"net/http"

	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/gin-gonic/gin"
)

// compile-time check: getTxStatusCommand fulfils the command interface
var _ command = (*getTxStatusCommand)(nil)

// getTxStatusCommand registers (or looks up) the bridge identified by the request's network id
// and transaction hash in the supervised registry.
type getTxStatusCommand struct {
	supervised domain.SupervisedRegistry
}

// Execute implements command: it parses the network_id/tx_hash path params, registers the
// bridge in the supervised registry, and reports its TrackingData — BridgeStatus nil until
// the tracker resolves the bridge, or Error set if it gave up trying to resolve it at all.
// Always 200 OK; only invalid path parameters produce an ErrorData/400
func (cmd *getTxStatusCommand) Execute(c *gin.Context) (int, any, *types.ErrorData) {
	req, err := parseBridgeRequest(c)
	if err != nil {
		return 0, nil, &types.ErrorData{Code: http.StatusBadRequest, Message: err.Error()}
	}

	tracking, err := cmd.supervised.Get(domain.TrackingID{NetworkID: req.NetworkID, TxHash: req.TxHash}, true)
	if err != nil {
		return 0, nil, &types.ErrorData{Code: http.StatusInternalServerError, Message: err.Error()}
	}

	return http.StatusOK, trackingDataFrom(tracking), nil
}
