package grpc

import (
	"context"
	"net"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/agglayer/aggkit/log"
	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

const (
	// DefaultMaxConcurrentRequests is the number of RPCs a server executes at the same time
	// when the configuration does not set a limit.
	DefaultMaxConcurrentRequests = 8
	// DefaultMaxConcurrentStreams is the number of concurrent streams allowed per HTTP/2
	// connection when the configuration does not set a limit.
	DefaultMaxConcurrentStreams = 100
	// DefaultMaxRequestsPerSecondPerPeer is the request rate allowed for a single peer
	// address when the configuration does not set a limit.
	DefaultMaxRequestsPerSecondPerPeer = 5
	// defaultBurstMultiplier is the burst of the per-peer rate limiter, as a multiple of
	// its rate, when the configuration does not set a burst.
	defaultBurstMultiplier = 4
	// peerLimiterIdleTimeout is how long a peer keeps its rate limiter after its last request.
	peerLimiterIdleTimeout = 10 * time.Minute
	// unknownPeer is the key used when the peer address cannot be determined.
	unknownPeer = "unknown"
)

// recoveryUnaryInterceptor turns a panic in a handler into an Internal error, so a single
// request cannot take the process down.
func recoveryUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (resp interface{}, err error) {
		defer func() {
			if r := recover(); r != nil {
				log.Errorf("panic in gRPC method %s: %v\n%s", info.FullMethod, r, debug.Stack())
				err = status.Errorf(codes.Internal, "internal error")
			}
		}()

		return handler(ctx, req)
	}
}

// recoveryStreamInterceptor is the streaming counterpart of recoveryUnaryInterceptor.
func recoveryStreamInterceptor() grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) (err error) {
		defer func() {
			if r := recover(); r != nil {
				log.Errorf("panic in gRPC stream %s: %v\n%s", info.FullMethod, r, debug.Stack())
				err = status.Errorf(codes.Internal, "internal error")
			}
		}()

		return handler(srv, ss)
	}
}

// concurrencyLimitUnaryInterceptor caps how many RPCs the server runs at the same time.
// Once the limit is reached, further calls fail fast instead of queueing and growing the
// memory held by in-flight requests.
func concurrencyLimitUnaryInterceptor(slots chan struct{}) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		if isHealthCheckMethod(info.FullMethod) {
			return handler(ctx, req)
		}

		select {
		case slots <- struct{}{}:
		default:
			return nil, status.Errorf(codes.ResourceExhausted,
				"too many concurrent requests, retry later")
		}

		defer func() { <-slots }()

		return handler(ctx, req)
	}
}

// perPeerRateLimiter keeps one token bucket per peer address.
type perPeerRateLimiter struct {
	limit  rate.Limit
	burst  int
	mu     sync.Mutex
	peers  map[string]*peerBucket
	lastGC time.Time
	now    func() time.Time
}

type peerBucket struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func newPerPeerRateLimiter(requestsPerSecond float64, burst int) *perPeerRateLimiter {
	if burst <= 0 {
		burst = int(requestsPerSecond * defaultBurstMultiplier)
	}
	if burst < 1 {
		burst = 1
	}

	return &perPeerRateLimiter{
		limit:  rate.Limit(requestsPerSecond),
		burst:  burst,
		peers:  make(map[string]*peerBucket),
		lastGC: time.Now(),
		now:    time.Now,
	}
}

// allow takes a token from the bucket of the peer that sent the request.
func (l *perPeerRateLimiter) allow(ctx context.Context) bool {
	key := peerKey(ctx)

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.gc(now)

	bucket, ok := l.peers[key]
	if !ok {
		bucket = &peerBucket{limiter: rate.NewLimiter(l.limit, l.burst)}
		l.peers[key] = bucket
	}
	bucket.lastSeen = now

	return bucket.limiter.AllowN(now, 1)
}

// gc drops the buckets of peers that have been idle, so the map stays bounded.
func (l *perPeerRateLimiter) gc(now time.Time) {
	if now.Sub(l.lastGC) < peerLimiterIdleTimeout {
		return
	}

	l.lastGC = now
	for key, bucket := range l.peers {
		if now.Sub(bucket.lastSeen) >= peerLimiterIdleTimeout {
			delete(l.peers, key)
		}
	}
}

// rateLimitUnaryInterceptor rejects requests from a peer that exceeds its request rate.
func rateLimitUnaryInterceptor(limiter *perPeerRateLimiter) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		if isHealthCheckMethod(info.FullMethod) {
			return handler(ctx, req)
		}

		if !limiter.allow(ctx) {
			return nil, status.Errorf(codes.ResourceExhausted,
				"rate limit exceeded, retry later")
		}

		return handler(ctx, req)
	}
}

// isHealthCheckMethod reports whether a method is a health check. Health checks are cheap,
// so they are exempt from the rate and concurrency limits: probes and the caller's own
// health checks do not take capacity from real requests, and a flood of other requests does
// not make the service look down.
func isHealthCheckMethod(fullMethod string) bool {
	return strings.HasSuffix(fullMethod, "/HealthCheck") ||
		fullMethod == "/grpc.health.v1.Health/Check"
}

// peerKey returns the address of the peer that sent the request, without its port, so
// every connection from the same peer shares one bucket.
func peerKey(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if !ok || p.Addr == nil {
		return unknownPeer
	}

	host, _, err := net.SplitHostPort(p.Addr.String())
	if err != nil {
		return p.Addr.String()
	}

	return host
}
