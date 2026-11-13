package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/agglayer/aggkit/tools/ger_validator_signer/version"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

// handleSign godoc
//
//	@Summary		Sign a global exit root
//	@Description	Signs keccak256(ger) if the GER is in the AgglayerGER contract of L1 and the L1 block that
//	@Description	inserted it is canonical and final. The signature is what AggOracleCommittee.injectGER verifies.
//	@Description	The error code decides what to do: GER_NOT_FINAL, RATE_LIMITED and INTERNAL can be retried.
//	@Tags			ger
//	@Accept			json
//	@Produce		json
//	@Param			request	body		SignRequest	true	"The GER to sign"
//	@Success		200		{object}	SignResponse
//	@Failure		400		{object}	ErrorResponse	"INVALID_REQUEST"
//	@Failure		404		{object}	ErrorResponse	"GER_UNKNOWN: the GER is not in the AgglayerGER contract"
//	@Failure		409		{object}	ErrorResponse	"GER_BLOCK_NOT_CANONICAL: the L1 block that inserted it is not canonical"
//	@Failure		413		{object}	ErrorResponse	"INVALID_REQUEST: the body is too big"
//	@Failure		425		{object}	ErrorResponse	"GER_NOT_FINAL: retry later (see Retry-After)"
//	@Failure		429		{object}	ErrorResponse	"RATE_LIMITED: retry later (see Retry-After)"
//	@Failure		500		{object}	ErrorResponse	"INTERNAL"
//	@Router			/v1/ger/sign [post]
func (s *Server) handleSign(w http.ResponseWriter, r *http.Request) {
	ger, err := s.readSignRequest(w, r)
	if err != nil {
		status := http.StatusBadRequest
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			status = http.StatusRequestEntityTooLarge
		}
		writeError(w, apiError{status: status, code: CodeInvalidRequest, message: requestErrorMessage(err)})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.PublicREST.RequestTimeout.Duration)
	defer cancel()
	result, err := s.service.SignGER(ctx, ger)
	if err != nil {
		apiErr := toAPIError(err)
		if apiErr.code == CodeInternal {
			// The client is told nothing about the cause: it can have details of L1 or of the signer
			s.logger.Error("failed to sign a GER", "ger", ger, "error", err)
		}
		writeError(w, apiErr)
		return
	}

	writeJSON(w, http.StatusOK, SignResponse{
		GER:       result.GER,
		Signature: result.Signature,
		Signer:    result.Signer,
	})
}

// handleInfo godoc
//
//	@Summary		Version and configuration of the validator
//	@Description	The address that signs is the one to register as a validator in the AggOracleCommittee.
//	@Tags			info
//	@Produce		json
//	@Success		200	{object}	InfoResponse
//	@Failure		429	{object}	ErrorResponse	"RATE_LIMITED"
//	@Router			/v1/info [get]
func (s *Server) handleInfo(w http.ResponseWriter, _ *http.Request) {
	info := s.service.Info()
	build := version.Get()
	writeJSON(w, http.StatusOK, InfoResponse{
		Version:     build.Version,
		GitRev:      build.GitRev,
		GitBranch:   build.GitBranch,
		BuildDate:   build.BuildDate,
		Signer:      info.Signer,
		AgglayerGER: info.AgglayerGER,
		L1ChainID:   s.cfg.L1ChainID,
		Finality:    FinalityInfo{Tag: info.FinalityTag, NumBlocks: info.FinalityNumBlks},
	})
}

// handleHealth godoc
//
//	@Summary		The validator is running
//	@Description	It doesn't query L1 or sign: it only says the process is alive.
//	@Tags			info
//	@Produce		json
//	@Success		200	{object}	HealthResponse
//	@Router			/health [get]
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, HealthResponse{Status: "ok"})
}

// handleSpec serves the swagger specification
func (s *Server) handleSpec(spec []byte, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(spec)
	}
}

// readSignRequest reads and validates the body of a request to sign a GER: a JSON object with only the field ger,
// no bigger than the limit
func (s *Server) readSignRequest(w http.ResponseWriter, r *http.Request) (common.Hash, error) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, s.cfg.PublicREST.RequestBodyLimit()))
	decoder.DisallowUnknownFields()

	var request SignRequest
	if err := decoder.Decode(&request); err != nil {
		return common.Hash{}, err
	}
	// Nothing but one JSON object
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return common.Hash{}, err
		}
		return common.Hash{}, errors.New("the body must be one JSON object")
	}

	return parseGER(request.GER)
}

// errInvalidGER is the error of a GER that is not 32 bytes of hex with the 0x prefix
var errInvalidGER = errors.New("ger must be a 32 bytes hex string with the 0x prefix")

func parseGER(raw string) (common.Hash, error) {
	data, err := hexutil.Decode(raw)
	if err != nil || len(data) != common.HashLength {
		return common.Hash{}, errInvalidGER
	}
	return common.BytesToHash(data), nil
}

// requestErrorMessage describes why a request is invalid, without echoing what the client sent
func requestErrorMessage(err error) string {
	var tooBig *http.MaxBytesError
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	switch {
	case errors.As(err, &tooBig):
		return fmt.Sprintf("the body is too big (the maximum is %d bytes)", tooBig.Limit)
	case errors.Is(err, errInvalidGER):
		return errInvalidGER.Error()
	case errors.Is(err, io.EOF):
		return "the body is empty"
	case errors.As(err, &syntaxErr), errors.Is(err, io.ErrUnexpectedEOF):
		return "the body is not valid JSON"
	case errors.As(err, &typeErr):
		return fmt.Sprintf("the field %q has an invalid type", typeErr.Field)
	default:
		return err.Error()
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, apiErr apiError) {
	if apiErr.retryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(apiErr.retryAfter))
	}
	writeJSON(w, apiErr.status, ErrorResponse{Code: apiErr.code, Message: apiErr.message})
}
