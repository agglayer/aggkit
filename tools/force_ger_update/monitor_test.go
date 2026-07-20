package force_ger_update

import (
	"context"
	"testing"
	"time"

	configtypes "github.com/agglayer/aggkit/config/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var testGERAddr = common.HexToAddress("0x1111111111111111111111111111111111111111")

const testL1WSURL = "ws://localhost:8546"

func testMonitorConfig() ForceGERUpdateConfig {
	return ForceGERUpdateConfig{
		GlobalExitRootManagerAddr: testGERAddr,
		InitialLookbackBlocks:     1000,
		FilterLogsChunkSize:       100,
		EventPollInterval:         durationOf(10 * time.Millisecond),
	}
}

func durationOf(d time.Duration) configtypes.Duration {
	return configtypes.Duration{Duration: d}
}

// filterQueryRange builds a mock.MatchedBy matcher asserting a FilterLogs/FilterQuery call's block range.
func filterQueryRange(t *testing.T, from, to uint64) interface{} {
	t.Helper()
	return mock.MatchedBy(func(q ethereum.FilterQuery) bool {
		return q.FromBlock.Uint64() == from && q.ToBlock.Uint64() == to
	})
}

func gerLog(blockNumber uint64, index uint) types.Log {
	return types.Log{
		Address:     testGERAddr,
		Topics:      []common.Hash{updateL1InfoTreeSignature},
		BlockNumber: blockNumber,
		Index:       index,
	}
}

func headerAt(blockNumber, timestamp uint64) *aggkittypes.BlockHeader {
	return &aggkittypes.BlockHeader{Number: blockNumber, Time: timestamp}
}

// --- LastGERUpdate (boot scan) ---

func TestMonitor_LastGERUpdate_FoundInFirstChunk(t *testing.T) {
	client := mocks.NewBaseEthereumClienter(t)
	cfg := testMonitorConfig()

	client.EXPECT().BlockNumber(mock.Anything).Return(uint64(5000), nil).Once()
	// lookbackFloor = 5000-1000 = 4000; first chunk = [4901,5000].
	client.EXPECT().FilterLogs(mock.Anything, filterQueryRange(t, 4901, 5000)).
		Return([]types.Log{gerLog(4950, 0)}, nil).Once()
	client.EXPECT().CustomHeaderByNumber(mock.Anything, mock.Anything).
		Return(headerAt(4950, 12345), nil).Once()

	m, err := NewMonitor(cfg, client, nil)
	require.NoError(t, err)

	ts, err := m.LastGERUpdate()
	require.NoError(t, err)
	require.Equal(t, time.Unix(12345, 0).UTC(), ts)
}

func TestMonitor_LastGERUpdate_FoundNChunksBack(t *testing.T) {
	client := mocks.NewBaseEthereumClienter(t)
	cfg := testMonitorConfig()

	client.EXPECT().BlockNumber(mock.Anything).Return(uint64(5000), nil).Once()
	// lookbackFloor = 4000. Chunks scanned backwards: [4901,5000], [4801,4900], [4701,4800] (hit).
	client.EXPECT().FilterLogs(mock.Anything, filterQueryRange(t, 4901, 5000)).
		Return(nil, nil).Once()
	client.EXPECT().FilterLogs(mock.Anything, filterQueryRange(t, 4801, 4900)).
		Return(nil, nil).Once()
	client.EXPECT().FilterLogs(mock.Anything, filterQueryRange(t, 4701, 4800)).
		Return([]types.Log{gerLog(4750, 2)}, nil).Once()
	client.EXPECT().CustomHeaderByNumber(mock.Anything, mock.Anything).
		Return(headerAt(4750, 999), nil).Once()

	m, err := NewMonitor(cfg, client, nil)
	require.NoError(t, err)

	ts, err := m.LastGERUpdate()
	require.NoError(t, err)
	require.Equal(t, time.Unix(999, 0).UTC(), ts)
}

func TestMonitor_LastGERUpdate_MultipleLogsInChunkPicksNewest(t *testing.T) {
	client := mocks.NewBaseEthereumClienter(t)
	cfg := testMonitorConfig()

	client.EXPECT().BlockNumber(mock.Anything).Return(uint64(5000), nil).Once()
	client.EXPECT().FilterLogs(mock.Anything, filterQueryRange(t, 4901, 5000)).
		Return([]types.Log{gerLog(4920, 3), gerLog(4980, 0), gerLog(4980, 1)}, nil).Once()
	client.EXPECT().CustomHeaderByNumber(mock.Anything, mock.Anything).
		Return(headerAt(4980, 555), nil).Once()

	m, err := NewMonitor(cfg, client, nil)
	require.NoError(t, err)

	ts, err := m.LastGERUpdate()
	require.NoError(t, err)
	require.Equal(t, time.Unix(555, 0).UTC(), ts)
}

func TestMonitor_LastGERUpdate_NoEventWithinLookback_ReturnsStaleZeroTime(t *testing.T) {
	client := mocks.NewBaseEthereumClienter(t)
	cfg := testMonitorConfig()
	cfg.InitialLookbackBlocks = 250
	cfg.FilterLogsChunkSize = 100

	client.EXPECT().BlockNumber(mock.Anything).Return(uint64(1000), nil).Once()
	// lookbackFloor = 750. Chunks: [901,1000], [801,900], [750,800] (all empty).
	client.EXPECT().FilterLogs(mock.Anything, filterQueryRange(t, 901, 1000)).Return(nil, nil).Once()
	client.EXPECT().FilterLogs(mock.Anything, filterQueryRange(t, 801, 900)).Return(nil, nil).Once()
	client.EXPECT().FilterLogs(mock.Anything, filterQueryRange(t, 750, 800)).Return(nil, nil).Once()

	m, err := NewMonitor(cfg, client, nil)
	require.NoError(t, err)

	ts, err := m.LastGERUpdate()
	require.NoError(t, err)
	require.True(t, ts.IsZero(), "expected the zero-time stale sentinel, got %s", ts)
}

func TestMonitor_LastGERUpdate_FilterLogsError(t *testing.T) {
	client := mocks.NewBaseEthereumClienter(t)
	cfg := testMonitorConfig()

	client.EXPECT().BlockNumber(mock.Anything).Return(uint64(5000), nil).Once()
	client.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return(nil, testStringError("boom")).Once()

	m, err := NewMonitor(cfg, client, nil)
	require.NoError(t, err)

	_, err = m.LastGERUpdate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "boom")
}

// --- Start: polling mode ---

func TestMonitor_Start_Poll_PicksUpNewLog(t *testing.T) {
	client := mocks.NewBaseEthereumClienter(t)
	cfg := testMonitorConfig()
	cfg.EventPollInterval = durationOf(5 * time.Millisecond)

	// findExpectedCall (testify) scans registered expectations in order and picks the first one
	// that isn't exhausted, so all one-shot (.Once()) expectations for a given method must be
	// registered before any unlimited (.Maybe()) catch-all for that same method — otherwise the
	// catch-all would shadow the later one-shots forever.
	client.EXPECT().BlockNumber(mock.Anything).Return(uint64(100), nil).Once() // consumed by Start()
	client.EXPECT().BlockNumber(mock.Anything).Return(uint64(105), nil).Once() // consumed by first tick
	client.EXPECT().FilterLogs(mock.Anything, filterQueryRange(t, 101, 105)).
		Return([]types.Log{gerLog(103, 0)}, nil).Once()
	client.EXPECT().CustomHeaderByNumber(mock.Anything, mock.Anything).
		Return(headerAt(103, 4242), nil).Once()
	// Steady-state catch-all: absorbs any further ticks racing with the test's deferred cancel().
	client.EXPECT().BlockNumber(mock.Anything).Return(uint64(105), nil).Maybe()

	m, err := NewMonitor(cfg, client, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := m.Start(ctx)
	require.NoError(t, err)

	select {
	case ev := <-ch:
		require.Equal(t, uint64(103), ev.BlockNumber)
		require.Equal(t, time.Unix(4242, 0).UTC(), ev.BlockTimestamp)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for polled GERUpdateEvent")
	}
}

func TestMonitor_Start_ContextCancellationClosesChannel(t *testing.T) {
	client := mocks.NewBaseEthereumClienter(t)
	cfg := testMonitorConfig()
	// Long interval: the ticker must never fire before cancellation for this test to be meaningful.
	cfg.EventPollInterval = durationOf(time.Hour)

	client.EXPECT().BlockNumber(mock.Anything).Return(uint64(100), nil).Once()

	m, err := NewMonitor(cfg, client, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := m.Start(ctx)
	require.NoError(t, err)

	cancel()

	select {
	case ev, ok := <-ch:
		require.False(t, ok, "expected channel to be closed on context cancellation, got event %+v", ev)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for channel to close after context cancellation")
	}
}

func TestMonitor_Start_Poll_BlockNumberErrorDoesNotHang(t *testing.T) {
	client := mocks.NewBaseEthereumClienter(t)
	cfg := testMonitorConfig()
	cfg.EventPollInterval = durationOf(5 * time.Millisecond)

	client.EXPECT().BlockNumber(mock.Anything).Return(uint64(100), nil).Once()
	client.EXPECT().BlockNumber(mock.Anything).Return(uint64(0), testStringError("rpc down")).Maybe()

	m, err := NewMonitor(cfg, client, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := m.Start(ctx)
	require.NoError(t, err)

	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case _, ok := <-ch:
		require.False(t, ok)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for channel to close")
	}
}

// --- Start: watch (WS) mode ---

// fakeSubscription is a minimal event.Subscription/ethereum.Subscription implementation for tests.
type fakeSubscription struct {
	errCh    chan error
	unsubbed chan struct{}
}

func newFakeSubscription() *fakeSubscription {
	return &fakeSubscription{errCh: make(chan error, 1), unsubbed: make(chan struct{})}
}

func (f *fakeSubscription) Err() <-chan error { return f.errCh }

func (f *fakeSubscription) Unsubscribe() {
	select {
	case <-f.unsubbed:
	default:
		close(f.unsubbed)
	}
}

func watchLog(blockNumber uint64) types.Log {
	return types.Log{
		Address: testGERAddr,
		Topics: []common.Hash{
			updateL1InfoTreeSignature,
			crypto.Keccak256Hash([]byte("mainnet")),
			crypto.Keccak256Hash([]byte("rollup")),
		},
		BlockNumber: blockNumber,
	}
}

func TestMonitor_Start_Watch_DeliversEvent(t *testing.T) {
	l1Client := mocks.NewBaseEthereumClienter(t)
	wsClient := mocks.NewBaseEthereumClienter(t)
	cfg := testMonitorConfig()
	cfg.L1WSURL = testL1WSURL

	sub := newFakeSubscription()
	wsClient.EXPECT().SubscribeFilterLogs(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ ethereum.FilterQuery, ch chan<- types.Log) (ethereum.Subscription, error) {
			go func() { ch <- watchLog(777) }()
			return sub, nil
		}).Once()
	l1Client.EXPECT().CustomHeaderByNumber(mock.Anything, mock.Anything).
		Return(headerAt(777, 8675309), nil).Once()

	m, err := NewMonitor(cfg, l1Client, wsClient)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := m.Start(ctx)
	require.NoError(t, err)

	select {
	case ev := <-ch:
		require.Equal(t, uint64(777), ev.BlockNumber)
		require.Equal(t, time.Unix(8675309, 0).UTC(), ev.BlockTimestamp)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for watched GERUpdateEvent")
	}
}

func TestMonitor_Start_Watch_ResubscribesAfterSubscriptionError(t *testing.T) {
	origBackoff := watchResubscribeBackoff
	watchResubscribeBackoff = 5 * time.Millisecond
	t.Cleanup(func() { watchResubscribeBackoff = origBackoff })

	l1Client := mocks.NewBaseEthereumClienter(t)
	wsClient := mocks.NewBaseEthereumClienter(t)
	cfg := testMonitorConfig()
	cfg.L1WSURL = testL1WSURL

	firstSub := newFakeSubscription()
	secondSub := newFakeSubscription()

	callCount := 0
	wsClient.EXPECT().SubscribeFilterLogs(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ ethereum.FilterQuery, ch chan<- types.Log) (ethereum.Subscription, error) {
			callCount++
			if callCount == 1 {
				// Break the first subscription almost immediately.
				go func() {
					time.Sleep(2 * time.Millisecond)
					firstSub.errCh <- testStringError("ws connection lost")
				}()
				return firstSub, nil
			}
			go func() { ch <- watchLog(888) }()
			return secondSub, nil
		}).Twice()
	l1Client.EXPECT().CustomHeaderByNumber(mock.Anything, mock.Anything).
		Return(headerAt(888, 111), nil).Once()

	m, err := NewMonitor(cfg, l1Client, wsClient)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := m.Start(ctx)
	require.NoError(t, err)

	select {
	case ev := <-ch:
		require.Equal(t, uint64(888), ev.BlockNumber)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for post-resubscribe GERUpdateEvent")
	}
}

// --- NewMonitor validation ---

func TestNewMonitor_Validation(t *testing.T) {
	client := mocks.NewBaseEthereumClienter(t)

	t.Run("nil l1Client rejected", func(t *testing.T) {
		_, err := NewMonitor(testMonitorConfig(), nil, nil)
		require.Error(t, err)
	})

	t.Run("L1WSURL set but wsClient nil rejected", func(t *testing.T) {
		cfg := testMonitorConfig()
		cfg.L1WSURL = testL1WSURL
		_, err := NewMonitor(cfg, client, nil)
		require.Error(t, err)
	})

	t.Run("zero FilterLogsChunkSize rejected", func(t *testing.T) {
		cfg := testMonitorConfig()
		cfg.FilterLogsChunkSize = 0
		_, err := NewMonitor(cfg, client, nil)
		require.Error(t, err)
	})

	t.Run("valid config accepted", func(t *testing.T) {
		_, err := NewMonitor(testMonitorConfig(), client, nil)
		require.NoError(t, err)
	})
}

// testStringError is a tiny error helper to avoid pulling in errors.New at every call site above.
type testStringError string

func (e testStringError) Error() string { return string(e) }
