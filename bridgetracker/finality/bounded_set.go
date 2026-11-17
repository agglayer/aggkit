package finality

import (
	"container/list"
	"sync"
)

// boundedSet is a set of at most limit keys that evicts the least recently used one when full.
// It is safe for concurrent use
type boundedSet[K comparable] struct {
	mu    sync.Mutex
	limit int
	order *list.List // front = most recently used; elements hold a K
	items map[K]*list.Element
}

func newBoundedSet[K comparable](limit int) *boundedSet[K] {
	return &boundedSet[K]{limit: limit, order: list.New(), items: make(map[K]*list.Element)}
}

// contains reports whether key is in the set, marking it as recently used
func (s *boundedSet[K]) contains(key K) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	elem, ok := s.items[key]
	if ok {
		s.order.MoveToFront(elem)
	}
	return ok
}

// add puts key in the set (or marks it as recently used), evicting the least recently used key
// if that exceeds the maximum size
func (s *boundedSet[K]) add(key K) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if elem, ok := s.items[key]; ok {
		s.order.MoveToFront(elem)
		return
	}
	s.items[key] = s.order.PushFront(key)
	for s.order.Len() > s.limit {
		oldest := s.order.Back()
		s.order.Remove(oldest)
		if oldKey, ok := oldest.Value.(K); ok {
			delete(s.items, oldKey)
		}
	}
}

// len returns the number of keys in the set
func (s *boundedSet[K]) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.order.Len()
}
