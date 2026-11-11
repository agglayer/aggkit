package api

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

// Error codes of the error responses. The AggOracle decides what to do from the code, not from the HTTP status
const (
	CodeInvalidRequest   = "INVALID_REQUEST"
	CodeGERUnknown       = "GER_UNKNOWN"
	CodeGERBlockNotCanon = "GER_BLOCK_NOT_CANONICAL"
	CodeGERNotFinal      = "GER_NOT_FINAL"
	CodeRateLimited      = "RATE_LIMITED"
	CodeInternal         = "INTERNAL"
)

// SignRequest is the body of the request to sign a GER
//
//	@Description	GER to sign
type SignRequest struct {
	// GER is the global exit root: 32 bytes, as a hex string with the 0x prefix
	GER string `json:"ger" example:"0x0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"`
}

// SignResponse is the body of the response of a signed GER
//
//	@Description	Signature of a GER
type SignResponse struct {
	// GER is the GER that was signed
	GER common.Hash `json:"ger" swaggertype:"string" example:"0x0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"` //nolint:lll
	// Signature is r, s and v (65 bytes) of the signature of keccak256(GER). v is 27 or 28 and s is in the lower
	// half of the curve order, as AggOracleCommittee.injectGER requires
	Signature hexutil.Bytes `json:"signature" swaggertype:"string" example:"0x0e9eb15c213e333243b2e85294bdb204112c64d19e2cbfe3d2740e4a888147b0415ac1d65fd197e3ff38a5d3920c5d167d95cc88b28e26bc549e4d7783348a9f1c"` //nolint:lll
	// Signer is the address of the validator, as registered in the AggOracleCommittee
	Signer common.Address `json:"signer" swaggertype:"string" example:"0x7e5f4552091a69125d5dfcb7b8c2659029395bdf"`
}

// ErrorResponse is the body of the error responses
//
//	@Description	Why the request failed
type ErrorResponse struct {
	// Code says what happened: INVALID_REQUEST, GER_UNKNOWN, GER_BLOCK_NOT_CANONICAL, GER_NOT_FINAL (retryable),
	// RATE_LIMITED (retryable) or INTERNAL (retryable)
	Code string `json:"code" example:"GER_NOT_FINAL"`
	// Message is a description for humans
	Message string `json:"message" example:"the L1 block that inserted the GER is not final yet"`
}

// FinalityInfo is the finality the validator requires
type FinalityInfo struct {
	// Tag is the block tag: latest, safe or finalized
	Tag string `json:"tag" example:"finalized"`
	// NumBlocks is subtracted from the number of the block of the tag
	NumBlocks uint64 `json:"num_blocks" example:"0"`
}

// InfoResponse describes the validator
//
//	@Description	Version and configuration of the validator
type InfoResponse struct {
	Version   string `json:"version" example:"v0.10.0"`
	GitRev    string `json:"git_rev" example:"5453f35"`
	GitBranch string `json:"git_branch" example:"main"`
	BuildDate string `json:"build_date" example:"2026-11-11T10:00:00Z"`
	// Signer is the address that signs: the one to register in the AggOracleCommittee
	Signer common.Address `json:"signer" swaggertype:"string" example:"0x7e5f4552091a69125d5dfcb7b8c2659029395bdf"`
	// AgglayerGER is the contract of L1 where the GERs are looked up
	AgglayerGER common.Address `json:"agglayer_ger" swaggertype:"string" example:"0x1111111111111111111111111111111111111111"` //nolint:lll
	// L1ChainID is the chain id of L1
	L1ChainID uint64       `json:"l1_chain_id" example:"11155111"`
	Finality  FinalityInfo `json:"finality"`
}

// HealthResponse is the body of the health check
//
//	@Description	The validator is running
type HealthResponse struct {
	Status string `json:"status" example:"ok"`
}
