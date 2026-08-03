package apitypes

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
)

// ParseRequestFilter builds an Auto Claim request filter from query parameters.
func ParseRequestFilter(c *gin.Context) (autoclaimtypes.RequestFilter, error) {
	var filter autoclaimtypes.RequestFilter
	var err error
	if filter.SourceNetwork, err = parseOptionalUint32(c, "source_network"); err != nil {
		return filter, err
	}
	if filter.OriginNetwork, err = parseOptionalUint32(c, "origin_network"); err != nil {
		return filter, err
	}
	if filter.DestinationNetwork, err = parseOptionalUint32(c, "destination_network"); err != nil {
		return filter, err
	}
	if filter.Status, err = parseOptionalStatus(c); err != nil {
		return filter, err
	}
	if filter.PolicyResult, err = parseOptionalPolicyResult(c); err != nil {
		return filter, err
	}
	if filter.BridgeTxHash, err = parseOptionalHash(c, "bridge_tx_hash"); err != nil {
		return filter, err
	}
	if filter.ClaimTxHash, err = parseOptionalHash(c, "claim_tx_hash"); err != nil {
		return filter, err
	}
	if filter.FromBlock, err = parseOptionalUint64(c, "from_block"); err != nil {
		return filter, err
	}
	if filter.ToBlock, err = parseOptionalUint64(c, "to_block"); err != nil {
		return filter, err
	}
	pageNumber, err := parseOptionalUint32(c, "page_number")
	if err != nil {
		return filter, err
	}
	if pageNumber != nil {
		filter.PageNumber = *pageNumber
	}
	pageSize, err := parseOptionalUint32(c, "page_size")
	if err != nil {
		return filter, err
	}
	if pageSize != nil {
		if *pageSize > autoclaimtypes.MaxRequestPageSize {
			return filter, fmt.Errorf("page_size parameter must be less than or equal to %d",
				autoclaimtypes.MaxRequestPageSize)
		}
		filter.PageSize = *pageSize
	}
	return filter, nil
}

// EffectivePageSize returns the page size applied when the caller did not specify one.
func EffectivePageSize(pageSize uint32) uint32 {
	if pageSize == 0 {
		return autoclaimtypes.DefaultRequestPageSize
	}
	return pageSize
}

func parseOptionalUint32(c *gin.Context, name string) (*uint32, error) {
	value := strings.TrimSpace(c.Query(name))
	if value == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid %s parameter: %w", name, err)
	}
	result := uint32(parsed)
	return &result, nil
}

func parseOptionalUint64(c *gin.Context, name string) (*uint64, error) {
	value := strings.TrimSpace(c.Query(name))
	if value == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid %s parameter: %w", name, err)
	}
	return &parsed, nil
}

func parseOptionalStatus(c *gin.Context) (*autoclaimtypes.RequestStatus, error) {
	value := strings.TrimSpace(c.Query("status"))
	if value == "" {
		return nil, nil
	}
	status := autoclaimtypes.RequestStatus(value)
	switch status {
	case autoclaimtypes.RequestStatusDetected,
		autoclaimtypes.RequestStatusPolicyApproved,
		autoclaimtypes.RequestStatusPolicyRejected,
		autoclaimtypes.RequestStatusManualApprovalRequired,
		autoclaimtypes.RequestStatusQueued,
		autoclaimtypes.RequestStatusSending,
		autoclaimtypes.RequestStatusSent,
		autoclaimtypes.RequestStatusConfirmed,
		autoclaimtypes.RequestStatusDryRun,
		autoclaimtypes.RequestStatusFailed:
		return &status, nil
	default:
		return nil, fmt.Errorf("invalid status parameter: %s", value)
	}
}

func parseOptionalPolicyResult(c *gin.Context) (*autoclaimtypes.PolicyResult, error) {
	value := strings.TrimSpace(c.Query("policy_status"))
	if value == "" {
		value = strings.TrimSpace(c.Query("policy_result"))
	}
	if value == "" {
		return nil, nil
	}
	result := autoclaimtypes.PolicyResult(value)
	switch result {
	case autoclaimtypes.PolicyResultApproved,
		autoclaimtypes.PolicyResultRejected,
		autoclaimtypes.PolicyResultManual:
		return &result, nil
	default:
		return nil, fmt.Errorf("invalid policy_status parameter: %s", value)
	}
}

func parseOptionalHash(c *gin.Context, name string) (*common.Hash, error) {
	value := strings.TrimSpace(c.Query(name))
	if value == "" {
		return nil, nil
	}
	if !isHexHash(value) {
		return nil, fmt.Errorf("invalid %s parameter: must be a 0x-prefixed 32-byte hash", name)
	}
	hash := common.HexToHash(value)
	return &hash, nil
}

func isHexHash(value string) bool {
	trimmed := strings.TrimPrefix(value, "0x")
	if len(trimmed) != common.HashLength*2 {
		return false
	}
	_, err := hex.DecodeString(trimmed)
	return err == nil
}
