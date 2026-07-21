package force_ger_update

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeMonitor is a minimal hand-written GERMonitor test double. A generated mockery mock cannot be
// used here: this file is an internal test of the unexported runLoop, so it must stay in package
// force_ger_update, and a mocks package generated for GERMonitor/ForcedUpdateSender would import
// force_ger_update itself — creating an import cycle rejected by the Go tool
// ("import cycle not allowed in test"). See run.go's runLoop doc for the loop this drives.
type fakeMonitor struct {
	events <-chan GERUpdateEvent
}

func (m *fakeMonitor) LastGERUpdate() (time.Time, error) {
	return time.Time{}, nil
}

func (m *fakeMonitor) Start(context.Context) (<-chan GERUpdateEvent, error) {
	return m.events, nil
}

var _ GERMonitor = (*fakeMonitor)(nil)

// fakeSender is a minimal hand-written ForcedUpdateSender test double (see fakeMonitor for why it
// is hand-written rather than generated). send is invoked, with the call already counted, on every
// SendForcedGERUpdate call.
type fakeSender struct {
	calls atomic.Int32
	send  func(ctx context.Context) error
}

func (s *fakeSender) SendForcedGERUpdate(ctx context.Context) error {
	s.calls.Add(1)
	return s.send(ctx)
}

var _ ForcedUpdateSender = (*fakeSender)(nil)

// blockingSender returns a fakeSender whose SendForcedGERUpdate blocks on block (or ctx.Done(),
// per the real Sender's documented contract) before returning nil — simulating a send that stays
// "in flight" until the test releases it. A nil block returns immediately.
func blockingSender(block <-chan struct{}) *fakeSender {
	return &fakeSender{
		send: func(ctx context.Context) error {
			if block == nil {
				return nil
			}
			select {
			case <-block:
			case <-ctx.Done():
			}
			return nil
		},
	}
}

// TestRunLoop_StaleOnBoot_SendsExactlyOnce covers acceptance scenario (a): booting with the
// GERMonitor's stale sentinel (zero time.Time) as lastGERUpdate must make elapsed enormous, so the
// very first tick fires a send. Because the send is made to block until ctx is done, this also
// proves the in-flight guard holds across every later tick: exactly one call for the whole loop.
func TestRunLoop_StaleOnBoot_SendsExactlyOnce(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	monitor := &fakeMonitor{events: make(chan GERUpdateEvent)} // never written to: no organic update
	// The single send blocks (on ctx.Done(), via blockingSender) so it stays "in flight" for the
	// whole assertion window, letting us prove the in-flight guard suppresses every subsequent tick.
	// The release channel is never closed: the send unblocks only when ctx is cancelled at teardown.
	sender := blockingSender(make(chan struct{}))

	done := make(chan error, 1)
	go func() {
		done <- runLoop(ctx, monitor, sender, time.Time{}, 2*time.Millisecond, time.Millisecond)
	}()

	// Booting with the stale sentinel (zero time.Time) makes elapsed enormous, so the first tick
	// fires a send.
	require.Eventually(t, func() bool { return sender.calls.Load() == 1 }, time.Second, time.Millisecond,
		"stale boot must trigger the first forced-update send")

	// While that send is still in flight, many more ticks fire (every 2ms); the in-flight guard must
	// suppress all of them. We assert on this stable in-flight window rather than after the loop
	// returns: a tick racing with ctx cancellation could otherwise legitimately start a second
	// (harmless) send during shutdown, which is not what this test is about.
	time.Sleep(30 * time.Millisecond)
	require.Equal(t, int32(1), sender.calls.Load(),
		"in-flight guard must suppress every duplicate send while one is in flight")

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("runLoop did not return promptly after ctx cancellation")
	}
}

// TestRunLoop_EventBeforeThreshold_NoSend covers acceptance scenario (b): an UpdateL1InfoTree
// event observed before the elapsed time reaches MaxTimeWithoutGERUpdate must reset the timer, so
// no send is ever triggered.
func TestRunLoop_EventBeforeThreshold_NoSend(t *testing.T) {
	t.Parallel()

	const (
		checkInterval = 5 * time.Millisecond
		maxWithoutGER = 100 * time.Millisecond
		testDuration  = 60 * time.Millisecond
	)

	ctx, cancel := context.WithTimeout(context.Background(), testDuration)
	defer cancel()

	// Pre-load a fresh UpdateL1InfoTree event so it is already buffered when runLoop starts. The
	// first ticker tick is checkInterval (5ms) away, so the loop's first select iteration consumes
	// the buffered event and resets lastGERUpdate to ~now before any tick can evaluate the elapsed
	// time against the threshold. This keeps the test deterministic on slow/loaded CI runners,
	// rather than relying on a goroutine delivering the event within a narrow race window (the
	// earlier version flaked when that delivery was delayed past the threshold tick).
	events := make(chan GERUpdateEvent, 1)
	events <- GERUpdateEvent{BlockNumber: 42, BlockTimestamp: time.Now()}
	monitor := &fakeMonitor{events: events}
	sender := blockingSender(nil)

	// Boot with the timer already well past the threshold; the buffered event must reset it first,
	// so no send is ever triggered for the remainder of the loop.
	lastGERUpdate := time.Now().Add(-2 * maxWithoutGER)
	err := runLoop(ctx, monitor, sender, lastGERUpdate, checkInterval, maxWithoutGER)
	require.NoError(t, err)
	require.Equal(t, int32(0), sender.calls.Load(), "the reset from an observed event must prevent any send")
}

// TestRunLoop_InFlightGuard_NoDoubleSend covers acceptance scenario (c): while a forced-update
// send is in flight, further ticks that would otherwise trip the threshold must not start a second
// send (the in-flight guard suppresses them); once the in-flight send completes, a monitor event
// that resets the timer must prevent any further send for the rest of the loop's lifetime.
func TestRunLoop_InFlightGuard_NoDoubleSend(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	events := make(chan GERUpdateEvent, 1)
	monitor := &fakeMonitor{events: events}

	release := make(chan struct{})
	sender := blockingSender(release)

	go func() {
		// Several ticks (every 5ms) fire over this window while the first send is still in
		// flight; all of them must be suppressed by the in-flight guard.
		time.Sleep(30 * time.Millisecond)

		// Reset the timer while still in flight (the in-flight guard, still armed, must suppress
		// any tick racing with this), then let the first send complete.
		events <- GERUpdateEvent{BlockNumber: 7, BlockTimestamp: time.Now()}
		time.Sleep(10 * time.Millisecond) // give the loop time to consume the event
		close(release)
	}()

	// maxWithoutGER is generous relative to the remaining time budget after the reset above, so
	// the freshly-reset timer cannot legitimately trip again before ctx expires.
	err := runLoop(ctx, monitor, sender, time.Time{}, 5*time.Millisecond, 500*time.Millisecond)
	require.NoError(t, err)
	require.Equal(t, int32(1), sender.calls.Load(), "expected exactly one send across the whole in-flight window")
}

// TestRunLoop_ContextCancelled_ReturnsPromptly proves the loop, and any in-flight send, unwind
// cleanly (no leaked goroutines, no hang) once ctx is cancelled.
func TestRunLoop_ContextCancelled_ReturnsPromptly(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())

	monitor := &fakeMonitor{events: make(chan GERUpdateEvent)}
	sender := blockingSender(ctx.Done())

	done := make(chan error, 1)
	go func() {
		done <- runLoop(ctx, monitor, sender, time.Time{}, time.Millisecond, time.Millisecond)
	}()

	// Let at least one send start (and stay in flight, since it blocks on ctx.Done()), then cancel.
	require.Eventually(t, func() bool { return sender.calls.Load() >= 1 }, time.Second, time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("runLoop did not return promptly after ctx cancellation")
	}
}
