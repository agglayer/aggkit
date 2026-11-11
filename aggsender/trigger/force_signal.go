package trigger

import (
	"context"
	"sync"
)

// forceSignal coalesces forced trigger requests: at most one request is pending at any time,
// so requesting it repeatedly never queues more than a single forced event.
// The zero value is ready to use.
type forceSignal struct {
	once sync.Once
	ch   chan struct{}
}

func (f *forceSignal) channel() chan struct{} {
	f.once.Do(func() {
		f.ch = make(chan struct{}, 1)
	})
	return f.ch
}

// request records a forced trigger request. It never blocks.
func (f *forceSignal) request() {
	select {
	case f.channel() <- struct{}{}:
	default:
	}
}

// pending returns the channel that signals a forced trigger request.
func (f *forceSignal) pending() <-chan struct{} {
	return f.channel()
}

// sendCoalescing sends event to out, waiting for a reader or for ctx to be done.
// Forced trigger requests that arrive while the event is waiting are absorbed by it,
// because the event that is about to be read already answers them.
// It returns false when ctx is done before the event was read.
func sendCoalescing[T any](ctx context.Context, out chan<- T, event T, force <-chan struct{}) bool {
	for {
		select {
		case out <- event:
			return true
		case <-force:
		case <-ctx.Done():
			return false
		}
	}
}
