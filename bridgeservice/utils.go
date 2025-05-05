package bridgeservice

import (
	"fmt"

	"github.com/agglayer/aggkit/bridgesync"
)

const (
	// DefaultPageSize is the default number of records to be fetched
	DefaultPageSize = 20
	// MaxPageSize is the maximum number of records to be fetched
	MaxPageSize = 200
	// DefaultPage is the default page number to be used when fetching records
	DefaultPage = 1
)

// validatePaginationParams validates the page number and page size
func validatePaginationParams(pageNumber, pageSize *uint32) (uint32, uint32, error) {
	if pageNumber == nil {
		pageNumber = new(uint32)
		*pageNumber = DefaultPage
	}

	if pageSize == nil {
		pageSize = new(uint32)
		*pageSize = DefaultPageSize
	}

	if *pageNumber == 0 {
		return 0, 0, bridgesync.ErrInvalidPageNumber
	}

	if *pageSize == 0 {
		return 0, 0, bridgesync.ErrInvalidPageSize
	}

	if *pageSize > MaxPageSize {
		return 0, 0, fmt.Errorf("page size must be less than or equal to %d", MaxPageSize)
	}

	return *pageNumber, *pageSize, nil
}
