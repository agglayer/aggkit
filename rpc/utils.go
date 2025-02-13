package rpc

const DEFAULT_PAGE_SIZE = 20
const MAX_PAGE_SIZE = 200
const DEFAULT_PAGE = 1

func ValidatePaginationParams(page, pageSize uint32) (uint32, uint32) {
	// Force valid page: must be at least 1
	if page < 1 {
		page = DEFAULT_PAGE
	}

	// pageSize must be in [1..200]; otherwise, default to 20
	if pageSize < 1 || pageSize > MAX_PAGE_SIZE {
		pageSize = DEFAULT_PAGE_SIZE
	}
	return page, pageSize
}
