package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/agglayer/aggkit/tools/ger_validator_signer/service"
)

// retryAfterSeconds is what the clients are told to wait before asking again when the answer can change
const retryAfterSeconds = 5

// statusTooEarly is the HTTP status of a GER that is not final yet (RFC 8470, "Too Early"). The AggOracle
// decides from the code of the body, the status is only informative.
const statusTooEarly = http.StatusTooEarly

// apiError is how an error of the service is told to the client
type apiError struct {
	status  int
	code    string
	message string
	// retryAfter is the seconds the client should wait, or 0
	retryAfter int
}

// toAPIError converts an error of the service into the answer for the client. The details of the internal
// failures (what L1 or the signer said) are not told to the client: they are logged.
func toAPIError(err error) apiError {
	switch {
	case errors.Is(err, service.ErrInvalidGER):
		return apiError{status: http.StatusBadRequest, code: CodeInvalidRequest, message: err.Error()}
	case errors.Is(err, service.ErrGERUnknown):
		return apiError{status: http.StatusNotFound, code: CodeGERUnknown, message: err.Error()}
	case errors.Is(err, service.ErrGERBlockNotCanonical):
		return apiError{status: http.StatusConflict, code: CodeGERBlockNotCanon, message: err.Error()}
	case errors.Is(err, service.ErrGERNotFinal):
		return apiError{
			status: statusTooEarly, code: CodeGERNotFinal, message: err.Error(), retryAfter: retryAfterSeconds,
		}
	case errors.Is(err, context.DeadlineExceeded):
		return apiError{status: http.StatusInternalServerError, code: CodeInternal, message: "the request timed out"}
	default:
		return apiError{status: http.StatusInternalServerError, code: CodeInternal, message: "internal error"}
	}
}
