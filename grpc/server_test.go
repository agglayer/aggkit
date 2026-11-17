package grpc

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

const (
	testServiceName    = "aggkit.test.v1.TestHarness"
	testEchoMethod     = "/" + testServiceName + "/Echo"
	testPanicMethod    = "/" + testServiceName + "/Boom"
	testBlockingMethod = "/" + testServiceName + "/Block"
)

// testHarnessServer is the service registered by the tests. The blocking method uses the
// channels to let a test hold a request in the handler.
type testHarnessServer struct {
	entered chan struct{}
	release chan struct{}
}

func (s *testHarnessServer) signalEntered() {
	if s.entered != nil {
		select {
		case s.entered <- struct{}{}:
		default:
		}
	}
}

var testHarnessDesc = grpc.ServiceDesc{
	ServiceName: testServiceName,
	HandlerType: (*testHarness)(nil),
	Methods: []grpc.MethodDesc{
		{MethodName: "Echo", Handler: echoHandler},
		{MethodName: "Boom", Handler: panicHandler},
		{MethodName: "Block", Handler: blockingHandler},
	},
	Metadata: "test.proto",
}

type testHarness interface{}

func echoHandler(_ interface{}, ctx context.Context, dec func(interface{}) error,
	interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(emptypb.Empty)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return in, nil
	}
	info := &grpc.UnaryServerInfo{FullMethod: testEchoMethod}
	handler := func(_ context.Context, req interface{}) (interface{}, error) { return req, nil }

	return interceptor(ctx, in, info, handler)
}

func panicHandler(_ interface{}, ctx context.Context, dec func(interface{}) error,
	interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(emptypb.Empty)
	if err := dec(in); err != nil {
		return nil, err
	}
	info := &grpc.UnaryServerInfo{FullMethod: testPanicMethod}
	handler := func(_ context.Context, _ interface{}) (interface{}, error) {
		panic("handler panic")
	}

	return interceptor(ctx, in, info, handler)
}

func blockingHandler(srv interface{}, ctx context.Context, dec func(interface{}) error,
	interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(emptypb.Empty)
	if err := dec(in); err != nil {
		return nil, err
	}
	info := &grpc.UnaryServerInfo{FullMethod: testBlockingMethod}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		impl, ok := srv.(*testHarnessServer)
		if !ok {
			return req, nil
		}
		impl.signalEntered()

		select {
		case <-impl.release:
			return req, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	return interceptor(ctx, in, info, handler)
}

// startTestServer starts a server on a free loopback port and returns it with a client
// connection to it.
func startTestServer(t *testing.T, cfg ServerConfig, impl *testHarnessServer) (*Server, *grpc.ClientConn) {
	t.Helper()

	cfg.Host = "127.0.0.1"
	cfg.Port = 0

	s, err := NewServer(cfg)
	require.NoError(t, err)

	s.GRPC().RegisterService(&testHarnessDesc, impl)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Start(ctx)
		close(done)
	}()

	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("server did not stop after context cancellation")
		}
	})

	conn, err := grpc.NewClient(s.listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return s, conn
}

func invoke(ctx context.Context, conn *grpc.ClientConn, method string) error {
	return conn.Invoke(ctx, method, &emptypb.Empty{}, &emptypb.Empty{})
}

func TestServer(t *testing.T) {
	cfg := ServerConfig{
		Host:             "127.0.0.1",
		Port:             1111,
		EnableReflection: true,
	}

	s, err := NewServer(cfg)
	require.NoError(t, err, "Failed to create gRPC server")
	require.NotNil(t, s.GRPC())
	require.Equal(t, fmt.Sprintf("%s:%d", cfg.Host, cfg.Port), s.Addr(), "Server address should match config")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	// Start the server in a goroutine
	go func() {
		s.Start(ctx)
		close(done)
	}()

	// Wait for the server to start
	time.Sleep(200 * time.Millisecond)

	cancel()

	select {
	case <-done:
		// Success: server stopped after context cancel
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop after context cancellation")
	}
}

func TestServerKeepsServingAfterHandlerPanic(t *testing.T) {
	_, conn := startTestServer(t, ServerConfig{}, &testHarnessServer{})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := invoke(ctx, conn, testPanicMethod)
	require.Error(t, err)
	require.Equal(t, codes.Internal, status.Code(err),
		"a panicking handler must answer Internal, not crash the server")

	require.NoError(t, invoke(ctx, conn, testEchoMethod),
		"the server must keep serving after a handler panic")
}

func TestServerLimitsConcurrentRequests(t *testing.T) {
	impl := &testHarnessServer{
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	_, conn := startTestServer(t, ServerConfig{MaxConcurrentRequests: 1}, impl)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	firstDone := make(chan error, 1)
	go func() { firstDone <- invoke(ctx, conn, testBlockingMethod) }()

	select {
	case <-impl.entered:
	case <-ctx.Done():
		t.Fatal("the first request did not reach the handler")
	}

	err := invoke(ctx, conn, testEchoMethod)
	require.Error(t, err)
	require.Equal(t, codes.ResourceExhausted, status.Code(err),
		"a request above the concurrency limit must be rejected")

	close(impl.release)
	require.NoError(t, <-firstDone, "the request holding the slot must finish")
}

func TestServerRateLimitsPerPeer(t *testing.T) {
	_, conn := startTestServer(t, ServerConfig{
		MaxRequestsPerSecondPerPeer: 1,
		MaxRequestsBurstPerPeer:     1,
	}, &testHarnessServer{})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	require.NoError(t, invoke(ctx, conn, testEchoMethod), "the first request must be allowed")

	err := invoke(ctx, conn, testEchoMethod)
	require.Error(t, err)
	require.Equal(t, codes.ResourceExhausted, status.Code(err),
		"a request above the per-peer rate must be rejected")
}

func TestLimitsDoNotApplyToHealthChecks(t *testing.T) {
	t.Parallel()

	handler := func(context.Context, interface{}) (interface{}, error) { return "ok", nil }
	healthInfo := &grpc.UnaryServerInfo{FullMethod: "/aggkit.aggsender.validator.v1.AggsenderValidator/HealthCheck"}
	otherInfo := &grpc.UnaryServerInfo{FullMethod: "/aggkit.aggsender.validator.v1.AggsenderValidator/ValidateCertificate"}

	// No concurrency slot is free and the peer has no rate left.
	slots := make(chan struct{}, 1)
	slots <- struct{}{}
	limiter := newPerPeerRateLimiter(1, 1)
	require.True(t, limiter.allow(t.Context()))

	concurrency := concurrencyLimitUnaryInterceptor(slots)
	rateLimit := rateLimitUnaryInterceptor(limiter)

	for _, interceptor := range []grpc.UnaryServerInterceptor{concurrency, rateLimit} {
		resp, err := interceptor(t.Context(), nil, healthInfo, handler)
		require.NoError(t, err)
		require.Equal(t, "ok", resp)

		_, err = interceptor(t.Context(), nil, otherInfo, handler)
		require.Equal(t, codes.ResourceExhausted, status.Code(err))
	}
}
