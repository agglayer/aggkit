package common

import "sync"

// PubSub defines the interface for a generic publish/subscribe mechanism.
type PubSub[T any] interface {
	Subscribe(subscriberName string) <-chan T
	Publish(data T)
}

type GenericSubscriber[T any] struct {
	// map of subscribers with names
	subs map[chan T]string
	mu   sync.RWMutex
}

func NewGenericSubscriber[T any]() *GenericSubscriber[T] {
	return &GenericSubscriber[T]{
		subs: make(map[chan T]string),
	}
}

func (g *GenericSubscriber[T]) Subscribe(subscriberName string) <-chan T {
	ch := make(chan T)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.subs[ch] = subscriberName
	return ch
}

func (g *GenericSubscriber[T]) Publish(data T) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	for ch := range g.subs {
		go func(ch chan T) {
			ch <- data
		}(ch)
	}
}
