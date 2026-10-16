// Package feeestimate holds the fee-estimation API contract shared by the claim and bridge
// fee estimate endpoints: the response types, the string enums they use, the sentinel errors
// the HTTP layer maps to status codes, and the FeeEstimator interface the bridge service
// calls with one instance per network. The estimator implementation itself (sampling, gas
// price, cost) is built on top of this contract in this same package.
package feeestimate

import (
	"errors"

	"github.com/agglayer/aggkit/bridgeservice/types"
)

// Sentinel errors returned by FeeEstimator implementations. The HTTP handlers map them with
// errors.Is, so implementations may wrap them (e.g. fmt.Errorf("...: %w", ...)). They live
// here, next to the interface and response types, so neither package needs to import the
// other side of the bridgeservice dependency edge.
var (
	// ErrFeeEstimateUnavailable maps to HTTP 503: the estimator is not configured for the
	// network, a required RPC call failed, or the applicable bucket has no samples yet.
	// Fail closed: the endpoint answers an error, never a default value.
	ErrFeeEstimateUnavailable = errors.New("fee estimate unavailable")
	// ErrFeeEstimateBadRequest maps to HTTP 400: a request parameter is invalid.
	ErrFeeEstimateBadRequest = errors.New("fee estimate bad request")
)

// TokenClass classifies the token a claim fee estimate is about, from the point of view of
// the destination network the claim runs on. It matches the buckets the estimator samples
// repeat claims into.
// @Description Class of the token a claim fee estimate is about
// @Enum TokenClass
type TokenClass string

const (
	// WrappedERC20 is a token bridged from another network, held on the destination as a
	// wrapped ERC20 (originNetwork is not the destination and originToken is not 0x0).
	WrappedERC20 TokenClass = "wrapped_erc20"
	// OriginNativeERC20 is an ERC20 that already lives on the destination network
	// (originNetwork equals the destination), so no wrapper is involved.
	OriginNativeERC20 TokenClass = "origin_native_erc20"
	// GasToken is the origin's native gas token (originToken 0x0).
	GasToken TokenClass = "gas_token"
)

// Applicable names which of the two claim estimates the request resolves to: the first
// claim of a token on the destination deploys its
// wrapper, every later claim is a repeat claim.
// @Description Which claim estimate applies to the request
// @Enum Applicable
type Applicable string

const (
	// FirstClaim is the estimate for a claim that deploys the token's wrapper on the
	// destination (WrapperExists false).
	FirstClaim Applicable = "first_claim"
	// RepeatClaim is the estimate for a claim that deploys nothing new.
	RepeatClaim Applicable = "repeat_claim"
)

// Mechanism is the bridging mechanism a bridge fee estimate is about. The bridge-side
// estimate is per mechanism, deliberately with
// no per-token breakdown.
// @Description Bridging mechanism a bridge fee estimate is about
// @Enum Mechanism
type Mechanism string

const (
	// Native bridges the network's native gas token via msg.value.
	Native Mechanism = "native"
	// ERC20Lock locks an origin-native ERC20 in the bridge contract.
	ERC20Lock Mechanism = "erc20_lock"
	// WrappedBurn burns a wrapped token on its origin network.
	WrappedBurn Mechanism = "wrapped_burn"
)

// GasPriceInfo is the gas price snapshot an estimate was computed with, in wei of the
// estimated network's native gas token. Big values are decimal strings (types.BigIntString).
// @Description Gas price snapshot an estimate was computed with (wei, decimal strings)
// @example {"base_fee":"15000000000","priority_fee":"2000000000","gas_price":"17000000000",
// "block_number":123}
type GasPriceInfo struct {
	// BaseFee is the base fee of the block the snapshot was taken at. Null on chains that
	// do not use EIP-1559, where GasPrice comes from eth_gasPrice instead.
	BaseFee *types.BigIntString `json:"base_fee" example:"15000000000"`
	// PriorityFee is the suggested priority fee (eth_maxPriorityFeePerGas). Null on chains
	// that do not use EIP-1559.
	PriorityFee *types.BigIntString `json:"priority_fee" example:"2000000000"`
	// GasPrice is the effective gas price the cost is computed with: base fee plus priority
	// fee on EIP-1559 chains, eth_gasPrice otherwise. Always present.
	GasPrice types.BigIntString `json:"gas_price" example:"17000000000"`
	// BlockNumber is the block the snapshot was taken at.
	BlockNumber uint64 `json:"block_number" example:"123"`
}

// FeeEstimate is the shared estimate object carried by both fee estimate responses: the
// gas units of comparable observed transactions, times the gas price snapshot, with no
// server-side margin added (callers buffer for gas-price movement themselves).
// @Description Cost estimate: observed gas units times the gas price snapshot
// @example {"gas_units":110000,"cost":"1870000000000000","sample_count":37,"source":"class"}
type FeeEstimate struct {
	// GasUnits is the p90 (or, for a small bucket, the maximum) gasUsed of the observed
	// transactions in the bucket.
	GasUnits uint64 `json:"gas_units" example:"110000"`
	// Cost is what the tx sender pays on the estimated network: GasUnits times
	// GasPriceInfo.GasPrice, in wei of that network's native gas token, as a decimal string.
	Cost types.BigIntString `json:"cost" example:"1870000000000000"`
	// SampleCount is the number of observed transactions the bucket holds.
	SampleCount int `json:"sample_count" example:"37"`
	// Source names the bucket granularity the estimate came from: "token" (a per-token
	// bucket), "class" (a token-class fallback bucket) or "mechanism" (bridge side).
	Source string `json:"source" example:"class"`
}

// ClaimFeeEstimateResponse is the response body of GET /bridge/v1/claim-fee-estimate: the
// cost of claiming a token on the queried destination network. It always carries both the
// first-claim (wrapper deployment) and repeat-claim estimates; exactly one of them is
// applicable and the other is null with its reason in Unavailable. When the applicable
// estimate cannot be computed, the endpoint answers an error body instead (fail closed).
// @Description Cost of claiming a token on the destination network
// @example {"network_id":1,"origin_network":0,
// "origin_token_address":"0x1234567890abcdef1234567890abcdef12345678",
// "token_class":"wrapped_erc20","wrapper_exists":true,"applicable":"repeat_claim",
// "gas_price":{"base_fee":"15000000000","priority_fee":"2000000000","gas_price":"17000000000","block_number":123},
// "first_claim":null,
// "repeat_claim":{"gas_units":110000,"cost":"1870000000000000","sample_count":37,"source":"class"},
// "unavailable":{"first_claim":"wrapper already deployed; the first-claim estimate does not apply"},
// "computed_at":1760000000}
type ClaimFeeEstimateResponse struct {
	// NetworkID is the destination network the claim runs on (0 = L1, or the instance's L2 id).
	NetworkID uint32 `json:"network_id" example:"1"`
	// OriginNetwork is the network the token is bridged from.
	OriginNetwork uint32 `json:"origin_network" example:"0"`
	// OriginTokenAddress is the token address on the origin network; 0x0 is the origin's
	// native gas token.
	OriginTokenAddress types.Address `json:"origin_token_address" example:"0x1234567890abcdef1234567890abcdef12345678"`
	// TokenClass classifies the token from the destination's point of view.
	TokenClass TokenClass `json:"token_class" example:"wrapped_erc20"`
	// WrapperExists reports whether the destination already holds a deployed wrapper for the
	// token. Null when no wrapper applies (TokenClass is origin_native_erc20 or gas_token).
	// False means the next claim deploys the wrapper, so the applicable estimate is
	// FirstClaim.
	WrapperExists *bool `json:"wrapper_exists" example:"true"`
	// Applicable names the estimate the request resolves to.
	Applicable Applicable `json:"applicable" example:"repeat_claim"`
	// GasPrice is the gas price snapshot both estimates are computed with.
	GasPrice GasPriceInfo `json:"gas_price"`
	// FirstClaim is the wrapper-deployment claim estimate, or null when RepeatClaim is the
	// applicable estimate (its reason is in Unavailable).
	FirstClaim *FeeEstimate `json:"first_claim"`
	// RepeatClaim is the claim-without-deployment estimate, or null when FirstClaim is the
	// applicable estimate (its reason is in Unavailable).
	RepeatClaim *FeeEstimate `json:"repeat_claim"`
	// Unavailable holds, for the non-applicable estimate, the reason it is null; it is
	// null when unset. The applicable estimate is never listed here: if it cannot be
	// computed, the endpoint errors out instead of returning this body.
	Unavailable map[Applicable]string `json:"unavailable"`
	// ComputedAt is when the estimates were computed, in seconds since the Unix epoch.
	ComputedAt uint64 `json:"computed_at" example:"1760000000"`
}

// BridgeFeeEstimateResponse is the response body of GET /bridge/v1/bridge-fee-estimate: the
// cost of bridging a token out of the queried origin network. It is coarser than the claim
// estimate: one estimate per bridging mechanism, with no per-token breakdown.
// @Description Cost of bridging a token out of the origin network
// @example {"network_id":1,"token_address":"0x0000000000000000000000000000000000000000",
// "mechanism":"native",
// "gas_price":{"base_fee":"15000000000","priority_fee":"2000000000","gas_price":"17000000000","block_number":123},
// "estimate":{"gas_units":65000,"cost":"1105000000000000","sample_count":42,"source":"mechanism"},
// "computed_at":1760000000}
type BridgeFeeEstimateResponse struct {
	// NetworkID is the origin network the bridge tx runs on (0 = L1, or the instance's L2 id).
	NetworkID uint32 `json:"network_id" example:"1"`
	// TokenAddress is the token address on the origin network; 0x0 is the native gas token.
	TokenAddress types.Address `json:"token_address" example:"0x0000000000000000000000000000000000000000"`
	// Mechanism is the bridging mechanism the token resolves to.
	Mechanism Mechanism `json:"mechanism" example:"native"`
	// GasPrice is the gas price snapshot the estimate is computed with.
	GasPrice GasPriceInfo `json:"gas_price"`
	// Estimate is the per-mechanism cost estimate.
	Estimate FeeEstimate `json:"estimate"`
	// ComputedAt is when the estimate was computed, in seconds since the Unix epoch.
	ComputedAt uint64 `json:"computed_at" example:"1760000000"`
}
