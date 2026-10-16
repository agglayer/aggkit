package bridgeservice

import (
	"context"
	"fmt"
	"net/http"
	"time"

	// Both packages are referenced by the swagger annotations below only; swag resolves
	// annotation types through blank imports too (packages.findPackagePathFromImports).
	_ "github.com/agglayer/aggkit/bridgeservice/feeestimate"
	_ "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
)

const (
	// originNetworkParam is the query parameter naming the network a claimed token is bridged from
	originNetworkParam = "origin_network"
	// tokenAddressParam is the query parameter naming the token a bridge fee estimate is about
	tokenAddressParam = "token_address"

	// errFeeEstimationNotConfigured is the 503 body for a network this instance serves but has no
	// fee estimator for (fail closed)
	errFeeEstimationNotConfigured = "fee estimation is not configured for network %d"

	// Handler ids the fee estimate handlers report to request metrics. They live here (not in the
	// metrics package) so the fee estimate endpoints need no edit outside their own file.
	getClaimFeeEstimateReq  = "get_claim_fee_estimate"
	getBridgeFeeEstimateReq = "get_bridge_fee_estimate"
)

// parseAddressQuery parses a mandatory hex address query parameter from the request context.
// It returns an error when the parameter is absent or not a 0x-prefixed hex address.
func parseAddressQuery(c *gin.Context, key string) (common.Address, error) {
	paramStr := c.Query(key)
	if paramStr == "" {
		return common.Address{}, fmt.Errorf("%s is mandatory", key)
	}

	if !common.IsHexAddress(paramStr) {
		return common.Address{}, fmt.Errorf("invalid %s parameter, it should be a hex address", key)
	}

	return common.HexToAddress(paramStr), nil
}

// GetClaimFeeEstimateHandler returns the estimated cost of claiming a token on the queried
// destination network.
//
// @Summary Get claim fee estimate
// @Description Returns the estimated cost of claimAsset on the destination network, covering only
// @Description what the user or sponsor pays: execution gas times the gas price of the destination
// @Description network (chains served by this endpoint do not charge senders an L1 data fee). It
// @Description gives separate first-claim (wrapper deployment) and repeat-claim estimates; exactly
// @Description one of them applies to the request (chosen by wrapper_exists) and the other is null
// @Description with its reason in unavailable. The estimate fails closed: with no estimator
// @Description configured for the network, a required RPC call failing or no samples for the
// @Description applicable bucket it answers 503, never a default value. No server-side margin is
// @Description added: callers buffer for gas-price movement themselves. Message claims
// @Description (claimMessage) are not estimated. A request whose origin_network equals the
// @Description destination network while origin_token_address is 0x0 (the destination's own
// @Description native gas token, which no bridge deposit can deliver) answers 400.
// @Tags fee-estimation
// @Param network_id query uint32 true "Destination network ID (0 for L1, the L2 network ID otherwise)"
// @Param origin_network query uint32 true "Network the token is bridged from"
// @Param origin_token_address query string true "Token address on the origin network (0x0 = native gas token)"
// @Produce json
// @Success 200 {object} feeestimate.ClaimFeeEstimateResponse "Claim fee estimate"
// @Failure 400 {object} types.ErrorResponse "Bad Request"
// @Failure 500 {object} types.ErrorResponse "Internal Server Error"
// @Failure 503 {object} types.ErrorResponse "Service Unavailable"
// @Router /claim-fee-estimate [get]
func (b *BridgeService) GetClaimFeeEstimateHandler(c *gin.Context) {
	b.logger.Debugf("GetClaimFeeEstimate request received (network id=%s, origin network=%s, origin token address=%s)",
		c.Query(networkIDParam), c.Query(originNetworkParam), c.Query(originTokenAddrParam))

	statusCode := http.StatusOK
	startTime := time.Now()
	defer func() {
		reportMetrics(getClaimFeeEstimateReq, statusCode, startTime)
	}()

	ctx, cancel := context.WithTimeout(c, b.readTimeout)
	defer cancel()

	networkID, err := parseUintQuery(c, networkIDParam, true, uint32(0))
	if err != nil {
		b.logger.Warnf(errNetworkID, err)
		statusCode = http.StatusBadRequest
		c.JSON(statusCode, gin.H{"error": err.Error()})
		return
	}

	originNetwork, err := parseUintQuery(c, originNetworkParam, true, uint32(0))
	if err != nil {
		b.logger.Warnf(errInvalidParam, originNetworkParam)
		statusCode = http.StatusBadRequest
		c.JSON(statusCode, gin.H{"error": err.Error()})
		return
	}

	originToken, err := parseAddressQuery(c, originTokenAddrParam)
	if err != nil {
		b.logger.Warnf(errInvalidParam, originTokenAddrParam)
		statusCode = http.StatusBadRequest
		c.JSON(statusCode, gin.H{"error": err.Error()})
		return
	}

	switch networkID {
	case mainnetNetworkID, b.networkID:
		// a network this instance serves data for (L1 or its own L2)
	default:
		b.logger.Warnf(errNetworkID, networkID)
		statusCode = http.StatusBadRequest
		c.JSON(statusCode, gin.H{"error": fmt.Sprintf(errNetworkID, networkID)})
		return
	}

	estimator := b.feeEstimators[networkID]
	if estimator == nil {
		b.logger.Warnf(errFeeEstimationNotConfigured, networkID)
		statusCode = http.StatusServiceUnavailable
		c.JSON(statusCode, gin.H{"error": fmt.Sprintf(errFeeEstimationNotConfigured, networkID)})
		return
	}

	resp, err := estimator.EstimateClaimFee(ctx, originNetwork, originToken)
	if err != nil {
		statusCode = b.respondFeeEstimateError(c, err, "estimate claim fee")
		return
	}
	if resp == nil {
		// A nil estimate with a nil error would answer 200 with a null body; these endpoints
		// never serve a default value, so treat it as an internal fault (fail closed).
		b.logger.Errorf("fee estimator returned no claim fee estimate for network %d", networkID)
		statusCode = http.StatusInternalServerError
		c.JSON(statusCode, gin.H{"error": "fee estimator returned no claim fee estimate"})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// GetBridgeFeeEstimateHandler returns the estimated cost of bridging a token out of the queried
// origin network.
//
// @Summary Get bridge fee estimate
// @Description Returns the estimated cost of bridgeAsset out of the origin network, covering only
// @Description what the user or sponsor pays: execution gas times the gas price of the origin
// @Description network (chains served by this endpoint do not charge senders an L1 data fee). It
// @Description is coarser than the claim estimate: one estimate per bridging mechanism, with no
// @Description per-token breakdown. The mechanism is native gas token via msg.value
// @Description (token_address 0x0), lock of an origin-native ERC20, or burn of a wrapped token.
// @Description The estimate fails closed: with no estimator configured for the network, a required
// @Description RPC call failing or no samples for the resolved mechanism it answers 503, never a
// @Description default value. No server-side margin is added: callers buffer for gas-price
// @Description movement themselves. Message bridges (bridgeMessage) are not estimated.
// @Tags fee-estimation
// @Param network_id query uint32 true "Origin network ID (0 for L1, the L2 network ID otherwise)"
// @Param token_address query string true "Token address on the origin network, 0x-prefixed (0x0 = native gas token)"
// @Produce json
// @Success 200 {object} feeestimate.BridgeFeeEstimateResponse "Bridge fee estimate"
// @Failure 400 {object} types.ErrorResponse "Bad Request"
// @Failure 500 {object} types.ErrorResponse "Internal Server Error"
// @Failure 503 {object} types.ErrorResponse "Service Unavailable"
// @Router /bridge-fee-estimate [get]
func (b *BridgeService) GetBridgeFeeEstimateHandler(c *gin.Context) {
	b.logger.Debugf("GetBridgeFeeEstimate request received (network id=%s, token address=%s)",
		c.Query(networkIDParam), c.Query(tokenAddressParam))

	statusCode := http.StatusOK
	startTime := time.Now()
	defer func() {
		reportMetrics(getBridgeFeeEstimateReq, statusCode, startTime)
	}()

	ctx, cancel := context.WithTimeout(c, b.readTimeout)
	defer cancel()

	networkID, err := parseUintQuery(c, networkIDParam, true, uint32(0))
	if err != nil {
		b.logger.Warnf(errNetworkID, err)
		statusCode = http.StatusBadRequest
		c.JSON(statusCode, gin.H{"error": err.Error()})
		return
	}

	token, err := parseAddressQuery(c, tokenAddressParam)
	if err != nil {
		b.logger.Warnf(errInvalidParam, tokenAddressParam)
		statusCode = http.StatusBadRequest
		c.JSON(statusCode, gin.H{"error": err.Error()})
		return
	}

	switch networkID {
	case mainnetNetworkID, b.networkID:
		// a network this instance serves data for (L1 or its own L2)
	default:
		b.logger.Warnf(errNetworkID, networkID)
		statusCode = http.StatusBadRequest
		c.JSON(statusCode, gin.H{"error": fmt.Sprintf(errNetworkID, networkID)})
		return
	}

	estimator := b.feeEstimators[networkID]
	if estimator == nil {
		b.logger.Warnf(errFeeEstimationNotConfigured, networkID)
		statusCode = http.StatusServiceUnavailable
		c.JSON(statusCode, gin.H{"error": fmt.Sprintf(errFeeEstimationNotConfigured, networkID)})
		return
	}

	resp, err := estimator.EstimateBridgeFee(ctx, token)
	if err != nil {
		statusCode = b.respondFeeEstimateError(c, err, "estimate bridge fee")
		return
	}
	if resp == nil {
		// A nil estimate with a nil error would answer 200 with a null body; these endpoints
		// never serve a default value, so treat it as an internal fault (fail closed).
		b.logger.Errorf("fee estimator returned no bridge fee estimate for network %d", networkID)
		statusCode = http.StatusInternalServerError
		c.JSON(statusCode, gin.H{"error": "fee estimator returned no bridge fee estimate"})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// respondFeeEstimateError classifies err with httpStatusForSyncerError (which knows the fee
// estimate sentinels and the syncer errors) and writes the response the fee estimate endpoints
// answer with. The error's own message is kept in the body for every classified status, so a
// fail-closed estimate says why it is unavailable; only an unexpected fault gets a generic prefix.
// It logs at a level matching the status (debug 404, warn 400/503, error 500) and returns the
// status code so the caller can propagate it to the request metrics.
func (b *BridgeService) respondFeeEstimateError(c *gin.Context, err error, action string) int {
	statusCode := httpStatusForSyncerError(err)
	msg := err.Error()
	switch statusCode {
	case http.StatusInternalServerError:
		msg = fmt.Sprintf("failed to %s: %s", action, err)
		b.logger.Error(msg)
	case http.StatusNotFound:
		b.logger.Debug(msg)
	default:
		b.logger.Warn(msg)
	}

	c.JSON(statusCode, gin.H{"error": msg})

	return statusCode
}
