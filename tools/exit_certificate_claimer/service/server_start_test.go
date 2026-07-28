package claimer

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestServerStartGracefulShutdown starts the HTTP server on an ephemeral port and verifies that
// cancelling the context triggers a clean shutdown (Start returns nil).
func TestServerStartGracefulShutdown(t *testing.T) {
	t.Parallel()

	cert, err := LoadCertificate(writeSampleCert(t))
	require.NoError(t, err)
	claimer, _ := buildTestClaimer(t, cert.NewLocalExitRoot)
	// Port 0 lets the OS pick a free ephemeral port.
	cfg := &Config{Address: "127.0.0.1", Port: 0, ReadTimeoutSeconds: 1, WriteTimeoutSeconds: 1}
	srv := NewServer(cfg, claimer, claimer.logger)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }()

	// Give the listener a moment to come up, then cancel to trigger graceful shutdown.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after context cancellation")
	}
}

// TestServerStartListenError exercises the error branch: an unbindable address makes ListenAndServe
// fail immediately and Start returns that error.
func TestServerStartListenError(t *testing.T) {
	t.Parallel()

	cert, err := LoadCertificate(writeSampleCert(t))
	require.NoError(t, err)
	claimer, _ := buildTestClaimer(t, cert.NewLocalExitRoot)
	// An out-of-range port cannot be bound.
	cfg := &Config{Address: "127.0.0.1", Port: 99999999, ReadTimeoutSeconds: 1, WriteTimeoutSeconds: 1}
	srv := NewServer(cfg, claimer, claimer.logger)

	err = srv.Start(context.Background())
	require.Error(t, err)
}

func TestParseLeafTypeMessage(t *testing.T) {
	t.Parallel()

	lt, err := parseLeafType("Message")
	require.NoError(t, err)
	require.Equal(t, leafTypeMessage, lt)

	lt, err = parseLeafType("Transfer")
	require.NoError(t, err)
	require.Equal(t, leafTypeAsset, lt)

	_, err = parseLeafType("bogus")
	require.Error(t, err)
}
