package domain

import (
	"container/heap"
	"math/big"
	"slices"
	"strings"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
)

// ActivityPage selects one page of an ActivityQuerier.GetActivity result: PageNumber is 1-based,
// PageSize the number of entries per page (both validated by the HTTP command, see
// api.parseActivityPageParams). It is applied by the store itself, so the work of a request is
// bounded by the page, not by the address's whole history
type ActivityPage struct {
	PageNumber uint32
	PageSize   uint32
}

// Offset is how many entries come before the page, computed in uint64 so it cannot overflow
func (p ActivityPage) Offset() uint64 {
	if p.PageNumber == 0 {
		return 0
	}
	return uint64(p.PageNumber-1) * uint64(p.PageSize)
}

// GlobalIndexSortWidth is the width GlobalIndexSortKey pads to: the number of decimal digits of
// 2^256-1, wider than any global index (at most 2^65-1, 20 digits)
const GlobalIndexSortWidth = 78

// CompareActivityEntries is the order of GetActivity results: most recent first (CreatedAt
// descending), entries sharing a CreatedAt by global index descending, compared numerically (it
// can exceed 2^64), and entries sharing both — only possible under the legacy pre-Etrog global
// index collision, see BridgeKey — by BridgeKey descending, so the order is total and the same on
// every request and in every store. It returns a negative number when a comes before b, positive
// when after, 0 only for the same bridge
func CompareActivityEntries(a, b *ActivityEntry) int {
	if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
		return c
	}
	if c := compareGlobalIndexes(globalIndexOf(b), globalIndexOf(a)); c != 0 {
		return c
	}
	return b.Key().Compare(a.Key())
}

func globalIndexOf(e *ActivityEntry) bridgeservicetypes.BigIntString {
	if e.Bridge == nil {
		return ""
	}
	return e.Bridge.GlobalIndex
}

// compareGlobalIndexes compares two global indexes numerically, as canonical decimals (see
// canonicalGlobalIndex)
func compareGlobalIndexes(a, b bridgeservicetypes.BigIntString) int {
	ca, cb := canonicalGlobalIndex(a), canonicalGlobalIndex(b)
	if len(ca) != len(cb) {
		return len(ca) - len(cb)
	}
	return strings.Compare(ca, cb)
}

// GlobalIndexSortKey is gi as a fixed-width (GlobalIndexSortWidth), zero-padded decimal string:
// ordering these keys as text is ordering the global indexes numerically, exactly as
// CompareActivityEntries does. The SQLite store keeps it in a column to sort and page in SQL
func GlobalIndexSortKey(gi bridgeservicetypes.BigIntString) string {
	digits := canonicalGlobalIndex(gi)
	if len(digits) >= GlobalIndexSortWidth {
		return digits
	}
	return strings.Repeat("0", GlobalIndexSortWidth-len(digits)) + digits
}

// canonicalGlobalIndex is gi as a decimal without leading zeros. A value that is not a
// non-negative base-10 integer (never served by a bridge service) counts as 0
func canonicalGlobalIndex(gi bridgeservicetypes.BigIntString) string {
	s := string(gi)
	if s != "" && strings.Trim(s, "0123456789") == "" {
		if trimmed := strings.TrimLeft(s, "0"); trimmed != "" {
			return trimmed
		}
		return "0"
	}
	n, ok := new(big.Int).SetString(s, 10) //nolint:mnd // base 10
	if !ok || n.Sign() < 0 {
		return "0"
	}
	return n.String()
}

// ActivityPageSelector picks one page out of entries offered in any order, in
// CompareActivityEntries order, without sorting them all: it keeps only the first
// Offset()+PageSize entries seen so far (a bounded max-heap), so selecting a page out of N entries
// costs O(N log K) time and O(K) memory, K being the end of the page. It also counts every entry
// offered: the total GetActivity reports
type ActivityPageSelector struct {
	page  ActivityPage
	limit int
	kept  activityEntryHeap
	total int
}

// NewActivityPageSelector returns a selector for page; maxEntries bounds how many entries can be
// offered at most (the address's entry count), so a page far past the end allocates nothing
func NewActivityPageSelector(page ActivityPage, maxEntries int) *ActivityPageSelector {
	limit := page.Offset() + uint64(page.PageSize)
	if limit > uint64(maxEntries) {
		limit = uint64(maxEntries)
	}
	return &ActivityPageSelector{page: page, limit: int(limit), kept: make(activityEntryHeap, 0, int(limit))}
}

// Offer counts entry and keeps it if it can still be on the page
func (s *ActivityPageSelector) Offer(entry *ActivityEntry) {
	s.total++
	switch {
	case s.limit == 0:
	case len(s.kept) < s.limit:
		heap.Push(&s.kept, entry)
	case CompareActivityEntries(entry, s.kept[0]) < 0:
		s.kept[0] = entry
		heap.Fix(&s.kept, 0)
	}
}

// Page returns the page's entries in order (an empty, non-nil slice past the end) and the total
// number of entries offered
func (s *ActivityPageSelector) Page() ([]*ActivityEntry, int) {
	kept := []*ActivityEntry(s.kept)
	slices.SortFunc(kept, CompareActivityEntries)
	offset := s.page.Offset()
	if offset >= uint64(len(kept)) {
		return []*ActivityEntry{}, s.total
	}
	return kept[offset:], s.total
}

// SelectActivityPage returns page of entries (in CompareActivityEntries order) and len(entries)
func SelectActivityPage(entries []*ActivityEntry, page ActivityPage) ([]*ActivityEntry, int) {
	selector := NewActivityPageSelector(page, len(entries))
	for _, entry := range entries {
		selector.Offer(entry)
	}
	return selector.Page()
}

// activityEntryHeap is a max-heap in CompareActivityEntries order: its root is the kept entry
// that comes last, the first to drop when an earlier one is offered
type activityEntryHeap []*ActivityEntry

func (h activityEntryHeap) Len() int           { return len(h) }
func (h activityEntryHeap) Less(i, j int) bool { return CompareActivityEntries(h[i], h[j]) > 0 }
func (h activityEntryHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *activityEntryHeap) Push(x any) {
	entry, ok := x.(*ActivityEntry)
	if !ok {
		return
	}
	*h = append(*h, entry)
}

func (h *activityEntryHeap) Pop() any {
	old := *h
	n := len(old)
	entry := old[n-1]
	*h = old[:n-1]
	return entry
}
