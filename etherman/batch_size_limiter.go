package etherman

import (
	"sync/atomic"

	aggkitcommon "github.com/agglayer/aggkit/common"
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
)

// batchSizeLimiter holds the current maximum JSON-RPC batch size. It only ever decreases.
// Invariant: current >= 1.
type batchSizeLimiter struct {
	current atomic.Int64
}

// newBatchSizeLimiter creates a limiter starting at initial.
// initial <= 0 means ethermanconfig.DefaultBatchRequestMaxSize.
func newBatchSizeLimiter(initial int) *batchSizeLimiter {
	if initial <= 0 {
		initial = ethermanconfig.DefaultBatchRequestMaxSize
	}
	l := &batchSizeLimiter{}
	l.current.Store(int64(initial))
	return l
}

// Size returns the current maximum batch size.
func (l *batchSizeLimiter) Size() int {
	return int(l.current.Load())
}

// shrinkAfterRejection lowers the size after a chunk of sentSize requests was rejected with err.
// target = parsed limit if it satisfies 1 <= parsed < sentSize, else max(1, sentSize/2).
// It only lowers the size, never raises it. changed is true when this caller performed the lowering.
func (l *batchSizeLimiter) shrinkAfterRejection(sentSize int, err error) (oldSize, newSize int, changed bool) {
	target := int64(max(1, sentSize/2)) //nolint:mnd
	if err != nil {
		if parsed, ok := aggkitcommon.ParseMaxBatchSizeFromError(err.Error()); ok &&
			parsed >= 1 && parsed < uint64(sentSize) {
			target = int64(parsed)
		}
	}
	for {
		cur := l.current.Load()
		if target >= cur {
			return int(cur), int(cur), false
		}
		if l.current.CompareAndSwap(cur, target) {
			return int(cur), int(target), true
		}
	}
}
