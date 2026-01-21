package types

import "sort"

type ListBlockHeaders []*BlockHeader

// NewListBlockHeadersEmpty creates a new ListBlockHeaders with pre-allocated items set to nil
func NewListBlockHeadersEmpty(preAllocatedSize int) ListBlockHeaders {
	return ListBlockHeaders(make([]*BlockHeader, preAllocatedSize, preAllocatedSize))
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
