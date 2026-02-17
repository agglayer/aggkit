package types

type MapBlockHeaders map[uint64]*BlockHeader

func NewMapBlockHeadersEmpty(preAllocatedSize int) MapBlockHeaders {
	return MapBlockHeaders(make(map[uint64]*BlockHeader, preAllocatedSize))
}
