package types

import (
	"sort"

	aggkitcommon "github.com/agglayer/aggkit/common"
)

type ListBlockHeaders []*BlockHeader

// NewListBlockHeadersEmpty creates a new ListBlockHeaders with pre-allocated items set to nil
func NewListBlockHeadersEmpty(preAllocatedSize int) ListBlockHeaders {
	return ListBlockHeaders(make([]*BlockHeader, 0, preAllocatedSize))
}

// NewListBlockHeaders creates a new ListBlockHeaders with the given size to zero element
func NewListBlockHeaders(size int) ListBlockHeaders {
	return ListBlockHeaders(make([]*BlockHeader, size))
}
func (lbs ListBlockHeaders) Len() int {
	return len(lbs)
}

func (lbs ListBlockHeaders) ToMap() MapBlockHeaders {
	result := NewMapBlockHeadersEmpty(lbs.Len())
	for _, header := range lbs {
		if header != nil {
			result[header.Number] = header
		}
	}
	return result
}

func (lbs ListBlockHeaders) BlockNumbers() []uint64 {
	result := make([]uint64, 0, len(lbs))
	for _, header := range lbs {
		if header != nil {
			result = append(result, header.Number)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i] < result[j]
	})
	return result
}

func (lbs ListBlockHeaders) BlockRange() aggkitcommon.BlockRange {
	if len(lbs) == 0 {
		return aggkitcommon.BlockRange{}
	}
	var minBlock, maxBlock uint64
	initialized := false
	for _, header := range lbs {
		if header != nil {
			if !initialized {
				minBlock = header.Number
				maxBlock = header.Number
				initialized = true
			} else {
				if header.Number < minBlock {
					minBlock = header.Number
				}
				if header.Number > maxBlock {
					maxBlock = header.Number
				}
			}
		}
	}
	if !initialized {
		return aggkitcommon.BlockRange{}
	}
	return aggkitcommon.NewBlockRange(minBlock, maxBlock)
}
