package aggoracle_test

import (
	"context"
	"testing"
	"time"

	"github.com/agglayer/aggkit/aggoracle"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// blockingChainSender is a ChainSender whose ProcessGER blocks until released, so a test can hold
// an injection in flight while the oracle's context is cancelled.
type blockingChainSender struct {
	started  chan struct{}
	release  chan struct{}
	finished chan struct{}
}

func (s *blockingChainSender) IsGERInjected(common.Hash) (bool, error) { return false, nil }

func (s *blockingChainSender) ProcessGER(ctx context.Context, _ common.Hash) error {
	close(s.started)
	<-s.release
	close(s.finished)
	return nil
}

type staticL1InfoTreeSyncer struct{ ger common.Hash }

func (s *staticL1InfoTreeSyncer) GetLatestL1InfoGER(context.Context) (common.Hash, error) {
	return s.ger, nil
}

// TestStart_DrainsOutstandingInjections pins that Start does not return while a GER injection is
// still running: callers tear down resources (e.g. a simulated backend) as soon as Start
// returns, and an injection still using them would race that teardown.
func TestStart_DrainsOutstandingInjections(t *testing.T) {
	sender := &blockingChainSender{
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		finished: make(chan struct{}),
	}
	// A long wait period keeps the loop from spawning a second injection while we hold the first.
	oracle, err := aggoracle.New(
		log.GetDefaultLogger(),
		sender,
		nil, // l1Client, unused by this test
		&staticL1InfoTreeSyncer{ger: common.HexToHash("0x01")},
		time.Hour,
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	started := make(chan struct{})
	go func() {
		defer close(started)
		oracle.Start(ctx)
	}()

	select {
	case <-sender.started: // an injection is now in flight
	case <-time.After(5 * time.Second):
		t.Fatal("the oracle never started an injection")
	}
	cancel() // ask the oracle to stop while that injection is still running

	// Start must not return until the injection finishes.
	select {
	case <-started:
		t.Fatal("Start returned while an injection was still in flight")
	case <-time.After(50 * time.Millisecond):
	}

	close(sender.release) // let the injection finish
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after the injection finished")
	}
	<-sender.finished
}
