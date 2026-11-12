package common

import "sync"

// DefaultSubscriberBufferSize is the number of undelivered values kept for each subscriber
// before the oldest ones start being replaced by newer ones.
const DefaultSubscriberBufferSize = 16

// PubSub defines the interface for a generic publish/subscribe mechanism.
type PubSub[T any] interface {
	Subscribe(subscriberName string) <-chan T
	Publish(data T)
}

// subscription holds the delivery state of a single subscriber.
// Values are queued in `queue` by Publish and handed over to `out` by a dedicated goroutine,
// so a slow reader never blocks the publisher and values keep their publishing order.
type subscription[T any] struct {
	name  string
	out   chan T
	queue chan T
	// pushMu serialises writers so the "drop the oldest" step of push is atomic
	pushMu sync.Mutex
}

// push queues data without blocking. When the queue is full the oldest queued value
// is discarded, so the latest values are the ones that get delivered.
func (s *subscription[T]) push(data T) {
	s.pushMu.Lock()
	defer s.pushMu.Unlock()
	for {
		select {
		case s.queue <- data:
			return
		default:
		}
		select {
		case <-s.queue:
		default:
		}
	}
}

// run delivers the queued values, in order, to the subscriber's channel.
func (s *subscription[T]) run() {
	for data := range s.queue {
		s.out <- data
	}
}

// GenericSubscriber fans out published values to its subscribers.
// Publish never blocks: every subscriber has a bounded buffer and, when a subscriber
// does not read fast enough, its oldest undelivered values are replaced by the newest ones.
type GenericSubscriber[T any] struct {
	// map of subscribers by the channel handed to them
	subs       map[chan T]*subscription[T]
	bufferSize int
	mu         sync.RWMutex
}

// NewGenericSubscriber creates a GenericSubscriber that keeps up to
// DefaultSubscriberBufferSize undelivered values per subscriber.
func NewGenericSubscriber[T any]() *GenericSubscriber[T] {
	return NewGenericSubscriberWithBuffer[T](DefaultSubscriberBufferSize)
}

// NewGenericSubscriberWithBuffer creates a GenericSubscriber that keeps up to
// bufferSize undelivered values per subscriber (at least 1).
func NewGenericSubscriberWithBuffer[T any](bufferSize int) *GenericSubscriber[T] {
	if bufferSize < 1 {
		bufferSize = 1
	}
	return &GenericSubscriber[T]{
		subs:       make(map[chan T]*subscription[T]),
		bufferSize: bufferSize,
	}
}

func (g *GenericSubscriber[T]) Subscribe(subscriberName string) <-chan T {
	sub := &subscription[T]{
		name:  subscriberName,
		out:   make(chan T),
		queue: make(chan T, g.bufferSize),
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.subs[sub.out] = sub
	go sub.run()
	return sub.out
}

// Publish hands data to every subscriber without waiting for them to read it.
func (g *GenericSubscriber[T]) Publish(data T) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	for _, sub := range g.subs {
		sub.push(data)
	}
}
