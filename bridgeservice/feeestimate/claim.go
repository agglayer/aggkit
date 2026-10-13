package feeestimate

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/agglayer/aggkit/bridgeservice/types"
	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
)

// notApplicable reasons carried by ClaimFeeEstimateResponse.Unavailable for the estimate the
// request does not resolve to (D5): that estimate is null and only its reason is reported.
const (
	// firstClaimWrapperDeployedReason applies when the wrapper exists, so the next claim is
	// a repeat claim.
	firstClaimWrapperDeployedReason = "wrapper already deployed; the first-claim estimate does not apply"
	// firstClaimClassHasNoWrapperReason applies to token classes that never deploy a wrapper.
	firstClaimClassHasNoWrapperReason = "token class %s never deploys a wrapper; the first-claim estimate does not apply"
	// repeatClaimWrapperMissingReason applies when no wrapper exists yet, so the next claim
	// deploys it.
	repeatClaimWrapperMissingReason = "wrapper not yet deployed; the repeat-claim estimate does not apply"
)

// tokenKey identifies an origin token by the pair the bridge itself keys wrappers by: the
// network the token is native to, and its address there.
type tokenKey struct {
	originNetwork uint32
	originToken   common.Address
}

// claimStats is the gas-unit statistics snapshot of the claim window on the estimator's
// network: the buckets the claim estimate reads. It is computed from the latest claims the
// claims reader has indexed and their receipts, and reused for Config.StatsTTL.
type claimStats struct {
	// computedAt is when the snapshot computation started; the freshest data in it is at
	// least this old.
	computedAt time.Time
	// firstByClass holds the gasUsed of first claims (the claim deployed the wrapper) by
	// token class.
	firstByClass map[TokenClass][]uint64
	// repeatByClass holds the gasUsed of repeat claims by token class.
	repeatByClass map[TokenClass][]uint64
	// repeatByToken holds the gasUsed of repeat claims by origin token, the per-token
	// buckets behind the source "token" estimate.
	repeatByToken map[tokenKey][]uint64
}

// classifyToken classifies the token identified by originNetwork and originToken from the
// point of view of destinationNetwork: a token class the response reports and the repeat
// buckets are keyed by.
func classifyToken(destinationNetwork, originNetwork uint32, originToken common.Address) TokenClass {
	switch {
	case originNetwork == destinationNetwork:
		return OriginNativeERC20
	case originToken == (common.Address{}):
		return GasToken
	default:
		return WrappedERC20
	}
}

// EstimateClaimFee estimates the cost of claiming originToken, which originates on
// originNetwork, on the estimator's own network (the claim destination). It samples the
// latest indexed claims, classifies them first-claim (wrapper deployment) versus repeat
// claim, and prices them with a live gas price read. It fails closed (D1): when the
// applicable estimate (D5, chosen by the wrapper's existence) cannot be computed, it
// returns an error wrapping ErrFeeEstimateUnavailable instead of a default value.
func (e *Estimator) EstimateClaimFee(
	ctx context.Context, originNetwork uint32, originToken common.Address,
) (*ClaimFeeEstimateResponse, error) {
	if e.claims == nil {
		return nil, fmt.Errorf("%w: claim fee estimation is not configured for network %d",
			ErrFeeEstimateUnavailable, e.cfg.NetworkID)
	}

	stats, err := e.claimStats.get(ctx, e.collectClaimStats)
	if err != nil {
		return nil, fmt.Errorf("%w: computing the claim statistics: %w", ErrFeeEstimateUnavailable, err)
	}
	gas, err := e.gasPrice.get(ctx, e.readGasPrice)
	if err != nil {
		return nil, fmt.Errorf("%w: computing the gas price: %w", ErrFeeEstimateUnavailable, err)
	}

	class := classifyToken(e.cfg.NetworkID, originNetwork, originToken)
	wrapperExists, applicable, err := e.resolveApplicable(ctx, class, originNetwork, originToken)
	if err != nil {
		return nil, err
	}

	first := e.firstEstimate(stats, class, gas.price())
	repeat := e.repeatEstimate(stats, class, tokenKey{originNetwork: originNetwork, originToken: originToken},
		gas.price())

	if applicable == FirstClaim && first == nil {
		return nil, fmt.Errorf(
			"%w: no first-claim samples for token class %s in the latest %d claims of network %d (need %d)",
			ErrFeeEstimateUnavailable, class, e.cfg.Window, e.cfg.NetworkID, e.cfg.MinSamples)
	}
	if applicable == RepeatClaim && repeat == nil {
		return nil, fmt.Errorf(
			"%w: no repeat-claim samples for token class %s in the latest %d claims of network %d (need %d)",
			ErrFeeEstimateUnavailable, class, e.cfg.Window, e.cfg.NetworkID, e.cfg.MinSamples)
	}

	// The estimate the request does not resolve to is null with its reason (D5); only the
	// applicable one can ever be served.
	unavailable := make(map[Applicable]string, 1)
	if applicable == RepeatClaim {
		first = nil
		if wrapperExists != nil {
			unavailable[FirstClaim] = firstClaimWrapperDeployedReason
		} else {
			unavailable[FirstClaim] = fmt.Sprintf(firstClaimClassHasNoWrapperReason, class)
		}
	} else {
		repeat = nil
		unavailable[RepeatClaim] = repeatClaimWrapperMissingReason
	}

	// The oldest input the estimates reflect: statistics snapshot or gas price read.
	computedAt := stats.computedAt
	if gas.readAt.Before(computedAt) {
		computedAt = gas.readAt
	}

	return &ClaimFeeEstimateResponse{
		NetworkID:          e.cfg.NetworkID,
		OriginNetwork:      originNetwork,
		OriginTokenAddress: types.Address(originToken.Hex()),
		TokenClass:         class,
		WrapperExists:      wrapperExists,
		Applicable:         applicable,
		GasPrice:           gas.info,
		FirstClaim:         first,
		RepeatClaim:        repeat,
		Unavailable:        unavailable,
		ComputedAt:         uint64(computedAt.Unix()),
	}, nil
}

// resolveApplicable looks up the wrapper for wrapped tokens, the only class a wrapper
// applies to, and picks the applicable estimate (D5): a missing wrapper makes the next
// claim the first one; everything else repeats. For token classes that never deploy a
// wrapper, no lookup happens and wrapperExists is null.
func (e *Estimator) resolveApplicable(
	ctx context.Context, class TokenClass, originNetwork uint32, originToken common.Address,
) (*bool, Applicable, error) {
	if class != WrappedERC20 {
		return nil, RepeatClaim, nil
	}
	wrapped, err := e.getTokenWrappedAddress(ctx, originNetwork, originToken)
	if err != nil {
		return nil, FirstClaim, err
	}
	exists := wrapped != (common.Address{})
	if exists {
		return &exists, RepeatClaim, nil
	}
	return &exists, FirstClaim, nil
}

// getTokenWrappedAddress eth_calls the bridge's getTokenWrappedAddress(originNetwork,
// originToken) and returns the wrapper the destination already holds, or the zero address
// when none exists. The calldata is packed with a hand-declared ABI (only this one view
// method) so the package needs no generated binding; the selector it produces is the
// well-known 0x22e95f2c.
func (e *Estimator) getTokenWrappedAddress(
	ctx context.Context, originNetwork uint32, originToken common.Address,
) (common.Address, error) {
	calldata, err := e.viewABI.Pack("getTokenWrappedAddress", originNetwork, originToken)
	if err != nil {
		// The ABI is a fixed constant, so a packing failure is a programming error rather
		// than an RPC failure; it does not map to 503.
		return common.Address{}, fmt.Errorf("packing getTokenWrappedAddress: %w", err)
	}
	output, err := e.chain.CallContract(ctx, ethereum.CallMsg{To: &e.cfg.BridgeAddr, Data: calldata}, nil)
	if err != nil {
		return common.Address{}, fmt.Errorf(
			"%w: calling getTokenWrappedAddress on the bridge: %w", ErrFeeEstimateUnavailable, err)
	}
	if len(output) != abiWordLen {
		return common.Address{}, fmt.Errorf(
			"%w: getTokenWrappedAddress returned %d bytes, want %d",
			ErrFeeEstimateUnavailable, len(output), abiWordLen)
	}
	return common.BytesToAddress(output), nil
}

// firstEstimate builds the first-claim (wrapper-deployment) estimate from the class bucket,
// or nil when the bucket is too small (D2). First claims are always quoted from the token
// class: there is no per-token first-claim bucket.
func (e *Estimator) firstEstimate(stats claimStats, class TokenClass, price *big.Int) *FeeEstimate {
	samples := stats.firstByClass[class]
	if len(samples) < e.cfg.MinSamples {
		return nil
	}
	gasUnits := statisticOf(samples, e.cfg.Percentile)
	return &FeeEstimate{
		GasUnits:    gasUnits,
		Cost:        costOf(gasUnits, price),
		SampleCount: len(samples),
		Source:      sourceClass,
	}
}

// repeatEstimate builds the repeat-claim estimate from the origin token's own bucket once
// it holds at least Config.PerTokenMinSamples samples (D2), otherwise from the token-class
// fallback bucket, or nil when neither is usable.
func (e *Estimator) repeatEstimate(
	stats claimStats, class TokenClass, key tokenKey, price *big.Int,
) *FeeEstimate {
	if perToken := stats.repeatByToken[key]; len(perToken) >= e.cfg.PerTokenMinSamples {
		gasUnits := statisticOf(perToken, e.cfg.Percentile)
		return &FeeEstimate{
			GasUnits:    gasUnits,
			Cost:        costOf(gasUnits, price),
			SampleCount: len(perToken),
			Source:      sourceToken,
		}
	}
	byClass := stats.repeatByClass[class]
	if len(byClass) < e.cfg.MinSamples {
		return nil
	}
	gasUnits := statisticOf(byClass, e.cfg.Percentile)
	return &FeeEstimate{
		GasUnits:    gasUnits,
		Cost:        costOf(gasUnits, price),
		SampleCount: len(byClass),
		Source:      sourceClass,
	}
}

// collectClaimStats pages through the latest Config.Window claims the reader has indexed
// (newest first), keeps the asset claims, fetches each transaction with its receipt, applies
// the eligibility rules and buckets the survivors (D2, D4). It is the compute function
// behind the statistics TTL cache.
func (e *Estimator) collectClaimStats(ctx context.Context) (claimStats, error) {
	stats := claimStats{
		computedAt:    e.now(),
		firstByClass:  make(map[TokenClass][]uint64),
		repeatByClass: make(map[TokenClass][]uint64),
		repeatByToken: make(map[tokenKey][]uint64),
	}
	claims, err := e.pageClaims(ctx, e.cfg.Window)
	if err != nil {
		return claimStats{}, err
	}

	// One entry per transaction: a transaction is only eligible when it performed exactly
	// one claim, and its row carries the origin token the buckets are keyed by.
	rows := make(map[common.Hash]*claimsynctypes.Claim, len(claims))
	order := make([]common.Hash, 0, len(claims))
	for _, claim := range claims {
		if claim == nil || claim.IsMessage {
			continue // message claims are excluded: they run arbitrary receiver code
		}
		if _, dup := rows[claim.TxHash]; dup {
			continue
		}
		rows[claim.TxHash] = claim
		order = append(order, claim.TxHash)
	}

	observations, err := e.txs.fetch(ctx, order)
	if err != nil {
		return claimStats{}, err
	}

	for _, hash := range order {
		observation, ok := observations[hash]
		if !ok {
			continue // unknown or pending transaction: not a sample
		}
		gasUsed, firstClaim, ok := classifyClaimObservation(observation, e.cfg.BridgeAddr)
		if !ok {
			continue
		}
		row := rows[hash]
		class := classifyToken(e.cfg.NetworkID, row.OriginNetwork, row.OriginAddress)
		if firstClaim {
			stats.firstByClass[class] = append(stats.firstByClass[class], gasUsed)
			continue
		}
		stats.repeatByClass[class] = append(stats.repeatByClass[class], gasUsed)
		key := tokenKey{originNetwork: row.OriginNetwork, originToken: row.OriginAddress}
		stats.repeatByToken[key] = append(stats.repeatByToken[key], gasUsed)
	}
	return stats, nil
}

// classifyClaimObservation applies the claim-side eligibility rules (D4) to one observation:
// the transaction must have succeeded and gone directly to the bridge, and its receipt must
// hold exactly one bridge-emitted claim event, by any of the signatures claimsync indexes
// (Etrog and pre-Etrog ClaimEvent, or the sovereign chains' DetailedClaimEvent). It reports
// the gas the claim actually used and whether the claim deployed a wrapper token, the
// first-claim marker.
func classifyClaimObservation(obs *txObservation, bridgeAddr common.Address) (gasUsed uint64, firstClaim, ok bool) {
	if !isSuccessfulBridgeTx(obs, bridgeAddr) {
		return 0, false, false
	}
	if countBridgeLogs(obs, bridgeAddr, claimEventTopics) != 1 {
		return 0, false, false
	}
	firstClaim = countBridgeLogs(obs, bridgeAddr, []common.Hash{newWrappedTokenTopic}) > 0
	return obs.receipt.GasUsed, firstClaim, true
}

// pageClaims returns the newest limit claim rows the reader has indexed. Pages are
// requested newest first at one constant page size (the bridge service's MaxPageSize, or the
// window when smaller), because the reader keys a page's offset to the page size of the
// call; the last page may therefore deliver more rows than the window needs, and the result
// is trimmed down to the window.
func (e *Estimator) pageClaims(ctx context.Context, limit int) ([]*claimsynctypes.Claim, error) {
	pageSize := min(limit, defaultWindow)
	claims := make([]*claimsynctypes.Claim, 0, limit)
	for page := uint32(1); ; page++ {
		pageClaims, count, err := e.claims.GetClaimsPaged(ctx, page, uint32(pageSize), nil, nil)
		if err != nil {
			return nil, fmt.Errorf("paging claims (page %d, size %d): %w", page, pageSize, err)
		}
		if len(pageClaims) == 0 {
			break
		}
		claims = append(claims, pageClaims...)
		if len(claims) >= count || len(claims) >= limit || len(pageClaims) < pageSize {
			break // saw every claim the reader holds, filled the window, or hit the last page
		}
	}
	if len(claims) > limit {
		claims = claims[:limit]
	}
	return claims, nil
}
