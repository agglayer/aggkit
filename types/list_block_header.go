package types

import "sort"

type ListBlockHeaders []*BlockHeader

func NewListBlockHeadersEmpty(preAllocatedSize int) ListBlockHeaders {
	return ListBlockHeaders(make([]*BlockHeader, 0, preAllocatedSize))
}
func (lbs ListBlockHeaders) Len() int {
	return len(lbs)
}

func (lbs ListBlockHeaders) ToMap() MapBlockHeaders {
	result := NewMapBlockHeadersEmpty(lbs.Len())
	for _, header := range lbs {
		result[header.Number] = header
	}
	return result
}

func (lbs ListBlockHeaders) BlockNumbers() []uint64 {
	result := make([]uint64, 0, len(lbs))
	for _, header := range lbs {
		result = append(result, header.Number)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i] < result[j]
	})
	return result
}
