package rpc

const DefaultPageSize = 20
const MaxPageSize = 200
const DefaultPage = 1

func ValidatePaginationParams(page, pageSize uint32) (uint32, uint32) {
	// Force valid page: must be at least 1
	if page < 1 {
		page = DefaultPage
	}

	// pageSize must be in [1..200]; otherwise, default to 20
	if pageSize < 1 || pageSize > MaxPageSize {
		pageSize = DefaultPageSize
	}
	return page, pageSize
}
