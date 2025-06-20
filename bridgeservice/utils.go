package bridgeservice

import (
	"encoding/hex"
	"fmt"
	"strconv"

	bridgetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/gin-gonic/gin"
)

const (
	// DefaultPageSize is the default number of records to be fetched
	DefaultPageSize = uint32(20)
	// MaxPageSize is the maximum number of records to be fetched
	MaxPageSize = 200
	// DefaultPage is the default page number to be used when fetching records
	DefaultPage = uint32(1)
)

// validatePaginationParams validates the page number and page size
func validatePaginationParams(pageNumber, pageSize uint32) error {
	if pageNumber == 0 {
		return bridgesync.ErrInvalidPageNumber
	}

	if pageSize == 0 {
		return bridgesync.ErrInvalidPageSize
	}

	if pageSize > MaxPageSize {
		return fmt.Errorf("page size must be less than or equal to %d", MaxPageSize)
	}

	return nil
}

type UintParam interface {
	~uint32 | ~uint64
}

// parseUintQuery parses a uint32 or uint64 query parameter from the request context.
// If the parameter is mandatory and not present or invalid, it returns an error.
// If the parameter is optional, it returns the default value if not provided or invalid.
func parseUintQuery[T UintParam](c *gin.Context, key string, mandatory bool, defaultVal T) (T, error) {
	paramStr := c.Query(key)
	if paramStr == "" {
		if mandatory {
			return 0, fmt.Errorf("%s is mandatory", key)
		}
		return defaultVal, nil
	}

	param64, err := strconv.ParseUint(paramStr, 10, 64)
	if err != nil {
		if mandatory {
			return 0, fmt.Errorf("invalid %s parameter: %w", key, err)
		}
		return defaultVal, nil
	}

	var result T
	switch any(result).(type) {
	case uint32:
		if param64 > uint64(^uint32(0)) {
			return 0, fmt.Errorf("%s value out of range for uint32", key)
		}
		result = T(uint32(param64))
	case uint64:
		result = T(param64)
	default:
		return 0, fmt.Errorf("unsupported type for %s", key)
	}

	return result, nil
}

// parseUint32SliceParam parses a slice of uint32 parameters from the request context
func parseUint32SliceParam(c *gin.Context, key string) ([]uint32, error) {
	vals := c.QueryArray(key)
	result := make([]uint32, 0, len(vals))
	for _, v := range vals {
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return nil, err
		}
		result = append(result, uint32(n))
	}
	return result, nil
}

// NewBridgeResponse creates a new BridgeResponse instance out of the provided Bridge instance
func NewBridgeResponse(bridge *bridgesync.Bridge) *bridgetypes.BridgeResponse {
	return &bridgetypes.BridgeResponse{
		BlockNum:           bridge.BlockNum,
		BlockPos:           bridge.BlockPos,
		FromAddress:        bridgetypes.Address(bridge.FromAddress.Hex()),
		TxHash:             bridgetypes.Hash(bridge.TxHash.Hex()),
		Calldata:           fmt.Sprintf("0x%s", hex.EncodeToString(bridge.Calldata)),
		BlockTimestamp:     bridge.BlockTimestamp,
		LeafType:           bridge.LeafType,
		OriginNetwork:      bridge.OriginNetwork,
		OriginAddress:      bridgetypes.Address(bridge.OriginAddress.Hex()),
		DestinationNetwork: bridge.DestinationNetwork,
		DestinationAddress: bridgetypes.Address(bridge.DestinationAddress.Hex()),
		Amount:             bridgetypes.BigIntString(bridge.Amount.String()),
		Metadata:           fmt.Sprintf("0x%s", hex.EncodeToString(bridge.Metadata)),
		DepositCount:       bridge.DepositCount,
		IsNativeToken:      bridge.IsNativeToken,
		BridgeHash:         bridgetypes.Hash(bridge.Hash().Hex()),
	}
}

// NewClaimResponse creates ClaimResponse instance out of the provided Claim
func NewClaimResponse(claim *bridgesync.Claim) *bridgetypes.ClaimResponse {
	return &bridgetypes.ClaimResponse{
		GlobalIndex:        bridgetypes.BigIntString(claim.GlobalIndex.String()),
		DestinationNetwork: claim.DestinationNetwork,
		TxHash:             bridgetypes.Hash(claim.TxHash.Hex()),
		Amount:             bridgetypes.BigIntString(claim.Amount.String()),
		BlockNum:           claim.BlockNum,
		FromAddress:        bridgetypes.Address(claim.FromAddress.Hex()),
		DestinationAddress: bridgetypes.Address(claim.DestinationAddress.Hex()),
		OriginAddress:      bridgetypes.Address(claim.OriginAddress.Hex()),
		OriginNetwork:      claim.OriginNetwork,
		BlockTimestamp:     claim.BlockTimestamp,
		MainnetExitRoot:    bridgetypes.Hash(claim.MainnetExitRoot.Hex()),
	}
}

// NewTokenMappingResponse creates TokenMappingResponse instance out of the provided TokenMapping
func NewTokenMappingResponse(tokenMapping *bridgesync.TokenMapping) *bridgetypes.TokenMappingResponse {
	return &bridgetypes.TokenMappingResponse{
		BlockNum:            tokenMapping.BlockNum,
		BlockPos:            tokenMapping.BlockPos,
		BlockTimestamp:      tokenMapping.BlockTimestamp,
		TxHash:              bridgetypes.Hash(tokenMapping.TxHash.Hex()),
		OriginNetwork:       tokenMapping.OriginNetwork,
		OriginTokenAddress:  bridgetypes.Address(tokenMapping.OriginTokenAddress.Hex()),
		WrappedTokenAddress: bridgetypes.Address(tokenMapping.WrappedTokenAddress.Hex()),
		Metadata:            fmt.Sprintf("0x%s", hex.EncodeToString(tokenMapping.Metadata)),
		IsNotMintable:       tokenMapping.IsNotMintable,
		Calldata:            fmt.Sprintf("0x%s", hex.EncodeToString(tokenMapping.Calldata)),
		Type:                tokenMapping.Type,
	}
}

// NewTokenMigrationResponse creates LegacyTokenMigrationResponse instance out of the provided LegacyTokenMigration
func NewTokenMigrationResponse(
	tokenMigration *bridgesync.LegacyTokenMigration) *bridgetypes.LegacyTokenMigrationResponse {
	return &bridgetypes.LegacyTokenMigrationResponse{
		BlockNum:            tokenMigration.BlockNum,
		BlockPos:            tokenMigration.BlockPos,
		BlockTimestamp:      tokenMigration.BlockTimestamp,
		TxHash:              bridgetypes.Hash(tokenMigration.TxHash.Hex()),
		Sender:              bridgetypes.Address(tokenMigration.Sender.Hex()),
		LegacyTokenAddress:  bridgetypes.Address(tokenMigration.LegacyTokenAddress.Hex()),
		UpdatedTokenAddress: bridgetypes.Address(tokenMigration.UpdatedTokenAddress.Hex()),
		Amount:              bridgetypes.BigIntString(tokenMigration.Amount.String()),
		Calldata:            fmt.Sprintf("0x%s", hex.EncodeToString(tokenMigration.Calldata)),
	}
}

// NewL1InfoTreeLeafResponse creates L1InfoTreeLeafResponse instance out of the provided L1InfoTreeLeaf
func NewL1InfoTreeLeafResponse(leaf *l1infotreesync.L1InfoTreeLeaf) *bridgetypes.L1InfoTreeLeafResponse {
	return &bridgetypes.L1InfoTreeLeafResponse{
		BlockNumber:       leaf.BlockNumber,
		BlockPosition:     leaf.BlockPosition,
		L1InfoTreeIndex:   leaf.L1InfoTreeIndex,
		PreviousBlockHash: bridgetypes.Hash(leaf.PreviousBlockHash.Hex()),
		Timestamp:         leaf.Timestamp,
		MainnetExitRoot:   bridgetypes.Hash(leaf.MainnetExitRoot.Hex()),
		RollupExitRoot:    bridgetypes.Hash(leaf.RollupExitRoot.Hex()),
		GlobalExitRoot:    bridgetypes.Hash(leaf.GlobalExitRoot.Hex()),
		Hash:              bridgetypes.Hash(leaf.Hash.Hex()),
	}
}
