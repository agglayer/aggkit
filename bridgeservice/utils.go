package bridgeservice

import (
	"fmt"
	"strconv"

	"github.com/agglayer/aggkit/bridgesync"
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
