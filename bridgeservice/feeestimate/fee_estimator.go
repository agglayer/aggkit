package feeestimate

import (
	"context"

	"github.com/ethereum/go-ethereum/common"
)

// FeeEstimator estimates costs of txs executed on one network. The bridge service holds one
// estimator per network it serves (0 = L1 and/or its own L2); handlers pick it by the
// network_id query parameter. Implementations fail closed: they return an error, never a
// default value, when an estimate cannot be computed. Errors
// mapped by the HTTP layer are ErrFeeEstimateUnavailable (503) and ErrFeeEstimateBadRequest
// (400); syncer errors flow through httpStatusForSyncerError.
type FeeEstimator interface {
	// EstimateClaimFee estimates the cost of claiming originToken (minted on originNetwork)
	// on the estimator's own network, which is the claim destination. The response reports
	// both the first-claim (wrapper deployment) and repeat-claim estimates; exactly one of
	// them is applicable and the other is null with a reason.
	EstimateClaimFee(ctx context.Context, originNetwork uint32,
		originToken common.Address) (*ClaimFeeEstimateResponse, error)

	// EstimateBridgeFee estimates the cost of bridging token out of the estimator's own
	// network, which is the bridge origin, by bridging mechanism. token 0x0 is the
	// network's native gas token.
	EstimateBridgeFee(ctx context.Context, token common.Address) (*BridgeFeeEstimateResponse, error)
}
