package api

import (
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	// maxTrackedClients limits the memory of the rate limiter: a client that was not seen for clientIdleTTL
	// is forgotten when the limit is reached
	maxTrackedClients = 10000
	clientIdleTTL     = 10 * time.Minute
)

type trackedClient struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// clientLimiter limits the requests of each client (IP address) with a token bucket
type clientLimiter struct {
	mu      sync.Mutex
	rate    rate.Limit
	burst   int
	clients map[string]*trackedClient
	now     func() time.Time
}

// newClientLimiter creates a limiter of requestsPerSecond (sustained) and burst per client. It returns nil
// (no limit) if requestsPerSecond is 0.
func newClientLimiter(requestsPerSecond float64, burst int) *clientLimiter {
	if requestsPerSecond <= 0 {
		return nil
	}
	return &clientLimiter{
		rate:    rate.Limit(requestsPerSecond),
		burst:   burst,
		clients: make(map[string]*trackedClient),
		now:     time.Now,
	}
}

// allow returns true if the client can do a request now
func (l *clientLimiter) allow(client string) bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	tracked, ok := l.clients[client]
	if !ok {
		l.makeRoom(now)
		tracked = &trackedClient{limiter: rate.NewLimiter(l.rate, l.burst)}
		l.clients[client] = tracked
	}
	tracked.lastSeen = now
	return tracked.limiter.AllowN(now, 1)
}

// makeRoom forgets the clients that were idle for long and, if that is not enough, the least recently seen one.
// The caller holds the lock.
func (l *clientLimiter) makeRoom(now time.Time) {
	if len(l.clients) < maxTrackedClients {
		return
	}
	var oldestClient string
	var oldestSeen time.Time
	for client, tracked := range l.clients {
		if now.Sub(tracked.lastSeen) > clientIdleTTL {
			delete(l.clients, client)
			continue
		}
		if oldestClient == "" || tracked.lastSeen.Before(oldestSeen) {
			oldestClient, oldestSeen = client, tracked.lastSeen
		}
	}
	if len(l.clients) >= maxTrackedClients && oldestClient != "" {
		delete(l.clients, oldestClient)
	}
}

// clientAddress is what identifies the client of a request: the IP address of the connection. The headers
// that proxies add (X-Forwarded-For) are not used, because any client can send them.
func clientAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
