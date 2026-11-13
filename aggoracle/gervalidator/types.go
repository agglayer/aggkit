// Package gervalidator implements the AggOracle side of the GER validator protocol of the
// AggOracleCommittee v2: it asks the GER validators (ger_validator_signer) to sign a Global Exit Root and
// builds the signatures argument of AggOracleCommittee.injectGER.
package gervalidator

import (
	"context"
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

const (
	// SignaturePath is the path, relative to the validator base URL, of the endpoint that signs a GER
	SignaturePath = "v1/ger/sign"

	// SignatureLength is the length of a signature: r (32 bytes) + s (32 bytes) + v (1 byte)
	SignatureLength = 65
)

// Error codes returned by the GER validator in the body of its error responses
const (
	CodeInvalidRequest   = "INVALID_REQUEST"
	CodeGERUnknown       = "GER_UNKNOWN"
	CodeGERBlockNotCanon = "GER_BLOCK_NOT_CANONICAL"
	CodeGERNotFinal      = "GER_NOT_FINAL"
	CodeRateLimited      = "RATE_LIMITED"
	CodeInternalError    = "INTERNAL"
)

// maxErrorMessageLogSize limits how much of the message of a validator error is kept in the error
const maxErrorMessageLogSize = 256

var (
	// ErrGERUnknown: the validator didn't find the GER in the AgglayerGER contract of L1
	ErrGERUnknown = errors.New("GER unknown to the validator")
	// ErrGERNotCanonical: the L1 block where the GER was inserted is not in the canonical chain (reorg)
	ErrGERNotCanonical = errors.New("GER block is not canonical")
	// ErrGERNotFinal: the GER doesn't meet the finality required by the validator yet. It is retryable
	ErrGERNotFinal = errors.New("GER is not final yet for the validator")
	// ErrRateLimited: the validator rejected the request because of its rate limit. It is retryable
	ErrRateLimited = errors.New("validator rate limit reached")
	// ErrBadRequest: the validator rejected the request as malformed
	ErrBadRequest = errors.New("validator rejected the request")
	// ErrInvalidResponse: the response of the validator is malformed or its signature is not valid
	ErrInvalidResponse = errors.New("invalid response from the validator")
	// ErrNoURL: there is no URL to reach the validator
	ErrNoURL = errors.New("the validator has no URL")
	// ErrThresholdNotReached: not enough valid signatures were collected
	ErrThresholdNotReached = errors.New("not enough valid signatures")
)

// Validator is a GER validator registered on the AggOracleCommittee contract
type Validator struct {
	// Address is the address that signs, as registered on the contract
	Address common.Address
	// URL is the base URL of the REST API of the validator (from the contract, or overridden by configuration)
	URL string
}

// SignRequest is the body of the request to sign a GER (POST /v1/ger/sign)
type SignRequest struct {
	GER common.Hash `json:"ger"`
}

// SignResponse is the body of the response of a signed GER
type SignResponse struct {
	// GER is the GER that was signed
	GER common.Hash `json:"ger"`
	// Signature is r, s and v (65 bytes) of the signature of keccak256(GER)
	Signature hexutil.Bytes `json:"signature"`
	// Signer is the address that signed. It is informative: the AggOracle recovers the signer
	// from the signature and ignores this field
	Signer common.Address `json:"signer"`
}

// ErrorResponse is the body of the error responses of the validator
type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// APIError is an error response of the validator that doesn't have a more specific error
type APIError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("validator error (HTTP %d, code %q): %s", e.StatusCode, e.Code, truncate(e.Message))
}

func truncate(msg string) string {
	if len(msg) > maxErrorMessageLogSize {
		return msg[:maxErrorMessageLogSize] + "..."
	}
	return msg
}

// Client asks a GER validator to sign a GER
type Client interface {
	// SignGER asks the validator at baseURL to sign the GER. It returns one of the typed errors of
	// this package when the validator rejects the request
	SignGER(ctx context.Context, baseURL string, ger common.Hash) (*SignResponse, error)
}

// FailureKind classifies why a validator didn't provide a signature
type FailureKind int

const (
	// FailureTransient can change if the validators are asked again later: network error, timeout,
	// GER not final yet, rate limit, server error
	FailureTransient FailureKind = iota
	// FailureGERRejected: the validator knows the GER is not valid for it (unknown or not canonical)
	FailureGERRejected
	// FailureInvalid: the validator is misconfigured or misbehaving (no URL, malformed response,
	// signature that is not from the validator...)
	FailureInvalid
)

func (k FailureKind) String() string {
	switch k {
	case FailureTransient:
		return "transient"
	case FailureGERRejected:
		return "ger-rejected"
	case FailureInvalid:
		return "invalid"
	default:
		return fmt.Sprintf("unknown(%d)", int(k))
	}
}

// ClassifyFailure returns the kind of a failure to get a signature from a validator
func ClassifyFailure(err error) FailureKind {
	switch {
	case errors.Is(err, ErrGERUnknown), errors.Is(err, ErrGERNotCanonical):
		return FailureGERRejected
	case errors.Is(err, ErrInvalidResponse), errors.Is(err, ErrBadRequest), errors.Is(err, ErrNoURL):
		return FailureInvalid
	default:
		return FailureTransient
	}
}

// ValidatorFailure is the reason a validator didn't provide a signature
type ValidatorFailure struct {
	Validator common.Address
	Kind      FailureKind
	Err       error
}

// ThresholdNotReachedError is returned when not enough valid signatures were collected
type ThresholdNotReachedError struct {
	GER       common.Hash
	Threshold int
	Valid     int
	Failures  []ValidatorFailure
}

func (e *ThresholdNotReachedError) Error() string {
	return fmt.Sprintf("%s for GER %s: %d valid of threshold %d (%d validators failed: %s)",
		ErrThresholdNotReached, e.GER, e.Valid, e.Threshold, len(e.Failures), e.summary())
}

// Is makes errors.Is(err, ErrThresholdNotReached) work
func (e *ThresholdNotReachedError) Is(target error) bool {
	return target == ErrThresholdNotReached
}

// Retryable returns true if asking the validators again later can reach the threshold: it is
// not reachable if too many validators rejected the GER or are misconfigured
func (e *ThresholdNotReachedError) Retryable() bool {
	transient := 0
	for _, failure := range e.Failures {
		if failure.Kind == FailureTransient {
			transient++
		}
	}
	return e.Valid+transient >= e.Threshold
}

func (e *ThresholdNotReachedError) summary() string {
	counts := map[FailureKind]int{}
	for _, failure := range e.Failures {
		counts[failure.Kind]++
	}
	return fmt.Sprintf("%d transient, %d ger-rejected, %d invalid",
		counts[FailureTransient], counts[FailureGERRejected], counts[FailureInvalid])
}
