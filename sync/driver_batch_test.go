package sync

import (
	"context"
	"testing"
	"time"

	compmocks "github.com/agglayer/aggkit/db/compatibility/mocks"
	"github.com/agglayer/aggkit/reorgdetector"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// This file covers the driver-side batching contract:
//   - EnableBlockBatching is strictly opt-in (a nil callback keeps the legacy path);
//   - the wrapper delegates ProcessBlocks to the provided atomic function and keeps
//     forwarding single ProcessBlock calls (the bootstrap path) to the processor;
//   - drainBatch bounds the batch to MaxBatchBlocks without ever waiting for more;
//   - processBlocks invokes the batch processor once and publishes every block in
//     commit order only AFTER the batch committed (nothing on error / cancellation).
//
// The atomicity of the real SQL processors is covered separately by
// bridgesync/processor_batch_test.go and claimsync/processor_batch_test.go.

// recordingSubscriber is a synchronous PubSub[Block] double. The production
// GenericSubscriber.Publish fans out from one goroutine per call, so channel
// delivery order is not guaranteed; this records the exact Publish call order.
type recordingSubscriber struct {
	blocks []Block
}

func (r *recordingSubscriber) Subscribe(string) <-chan Block { return nil }

func (r *recordingSubscriber) Publish(b Block) { r.blocks = append(r.blocks, b) }

// recordingBatchProcessor is a minimal BlockBatchProcessor double that records
// every ProcessBlocks call and can be told to fail.
type recordingBatchProcessor struct {
	calls   int
	batches [][]Block
	err     error
}

func (r *recordingBatchProcessor) ProcessBlocks(_ context.Context, blocks []Block) error {
	r.calls++
	r.batches = append(r.batches, blocks)
	return r.err
}

// TestEnableBlockBatching_OptIn verifies the batching capability is strictly
// opt-in: an unwrapped processor never batches, a nil callback returns the
// original processor unwrapped (legacy path), and a real callback exposes the
// capability.
func TestEnableBlockBatching_OptIn(t *testing.T) {
	t.Parallel()

	pm := NewProcessorMock(t)

	_, ok := any(pm).(BlockBatchProcessor)
	require.False(t, ok, "an unwrapped processor must not expose the batch capability")

	// A nil callback is a no-op: the original processor is returned unwrapped.
	require.Same(t, pm, EnableBlockBatching(pm, nil))

	wrapped := EnableBlockBatching(pm, func(context.Context, []Block) error { return nil })
	require.NotSame(t, pm, wrapped)
	_, ok = wrapped.(BlockBatchProcessor)
	require.True(t, ok, "a wrapped processor must expose the batch capability")
}

// TestBatchingProcessor_ForwardsBatchAndSingle verifies the wrapper calls the
// provided function exactly once with the whole ordered batch, while ProcessBlock
// (used by the bootstrap path) still forwards a single block to the processor.
func TestBatchingProcessor_ForwardsBatchAndSingle(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	pm := NewProcessorMock(t)

	var (
		calls     int
		gotBlocks []Block
	)
	wrapped := EnableBlockBatching(pm, func(_ context.Context, blocks []Block) error {
		calls++
		gotBlocks = blocks
		return nil
	})

	blocks := []Block{{Num: 1}, {Num: 2}, {Num: 3}}
	batchProcessor, ok := wrapped.(BlockBatchProcessor)
	require.True(t, ok, "a wrapped processor must expose the batch capability")
	require.NoError(t, batchProcessor.ProcessBlocks(ctx, blocks))
	require.Equal(t, 1, calls, "the provided function must be invoked exactly once")
	require.Equal(t, blocks, gotBlocks, "the whole ordered batch must be forwarded")

	pm.EXPECT().ProcessBlock(ctx, Block{Num: 7}).Return(nil).Once()
	require.NoError(t, wrapped.ProcessBlock(ctx, Block{Num: 7}))
	require.Equal(t, 1, calls, "ProcessBlock must not go through the batch function")
}

// TestEVMDriver_DrainBatch verifies drainBatch collects only the blocks that are
// already available: it never waits for more, caps the batch at MaxBatchBlocks and
// reports a closed download channel while keeping the last buffered blocks.
func TestEVMDriver_DrainBatch(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	driver := &EVMDriver{}
	first := EVMBlock{EVMBlockHeader: EVMBlockHeader{Num: 0}}

	t.Run("caps the batch at MaxBatchBlocks", func(t *testing.T) {
		t.Parallel()
		ch := make(chan EVMBlock, MaxBatchBlocks+50)
		for i := 1; i <= MaxBatchBlocks+50; i++ {
			ch <- EVMBlock{EVMBlockHeader: EVMBlockHeader{Num: uint64(i)}}
		}

		batch, closed := driver.drainBatch(ctx, ch, first)
		require.Len(t, batch, MaxBatchBlocks)
		require.False(t, closed)
		require.Equal(t, uint64(0), batch[0].Num)
		require.Equal(t, uint64(MaxBatchBlocks-1), batch[MaxBatchBlocks-1].Num)
	})

	t.Run("does not wait when the queue is empty", func(t *testing.T) {
		t.Parallel()
		ch := make(chan EVMBlock)

		batch, closed := driver.drainBatch(ctx, ch, first)
		require.Equal(t, []EVMBlock{first}, batch)
		require.False(t, closed)
	})

	t.Run("closed channel keeps the last buffered blocks", func(t *testing.T) {
		t.Parallel()
		ch := make(chan EVMBlock, 3)
		for i := 1; i <= 3; i++ {
			ch <- EVMBlock{EVMBlockHeader: EVMBlockHeader{Num: uint64(i)}}
		}
		close(ch)

		batch, closed := driver.drainBatch(ctx, ch, first)
		require.True(t, closed)
		require.Len(t, batch, 4)
		require.Equal(t, uint64(3), batch[3].Num)
	})
}

// TestEVMDriver_ProcessBlocks_PublishesAfterCommit verifies processBlocks invokes
// the batch processor exactly once and publishes every block in commit order only
// after a successful commit; a failed batch and a cancelled context publish
// nothing, and a cancelled context never reaches the processor.
func TestEVMDriver_ProcessBlocks_PublishesAfterCommit(t *testing.T) {
	t.Parallel()

	newDriver := func(t *testing.T, bp BlockBatchProcessor) (*EVMDriver, *recordingSubscriber) {
		t.Helper()
		rh := &RetryHandler{MaxRetryAttemptsAfterError: 5, RetryAfterErrorPeriod: 10 * time.Millisecond}
		rdm := NewReorgDetectorMock(t)
		pm := NewProcessorMock(t)
		dm := NewDownloaderMock(t)
		compatMock := compmocks.NewCompatibilityChecker(t)
		rdm.EXPECT().Subscribe(reorgDetectorID).Return(&reorgdetector.Subscription{}, nil)

		// Wrapping the processor is what makes the driver pick up the capability.
		driver, err := NewEVMDriver(rdm, EnableBlockBatching(pm, bp.ProcessBlocks), dm,
			reorgDetectorID, 10, rh, compatMock)
		require.NoError(t, err)
		require.NotNil(t, driver.batchProcessor, "wrapping the processor must enable batching")

		rec := &recordingSubscriber{}
		driver.blockSubscriber = rec
		return driver, rec
	}

	evmBlocks := []EVMBlock{
		{EVMBlockHeader: EVMBlockHeader{Num: 1, Hash: common.HexToHash("0x1")}},
		{EVMBlockHeader: EVMBlockHeader{Num: 2, Hash: common.HexToHash("0x2")}},
		{EVMBlockHeader: EVMBlockHeader{Num: 3, Hash: common.HexToHash("0x3")}},
	}
	expected := []Block{
		{Num: 1, Hash: common.HexToHash("0x1")},
		{Num: 2, Hash: common.HexToHash("0x2")},
		{Num: 3, Hash: common.HexToHash("0x3")},
	}

	t.Run("success publishes every block in order after the batch", func(t *testing.T) {
		t.Parallel()
		bp := &recordingBatchProcessor{}
		driver, rec := newDriver(t, bp)

		require.NoError(t, driver.processBlocks(t.Context(), evmBlocks))
		require.Equal(t, 1, bp.calls, "the batch processor must be invoked once for the whole batch")
		require.Equal(t, expected, bp.batches[0])
		require.Equal(t, expected, rec.blocks, "every block must be published in commit order after success")
	})

	t.Run("error publishes nothing", func(t *testing.T) {
		t.Parallel()
		bp := &recordingBatchProcessor{err: ErrInconsistentState}
		driver, rec := newDriver(t, bp)

		err := driver.processBlocks(t.Context(), evmBlocks)
		require.ErrorIs(t, err, ErrInconsistentState)
		require.Empty(t, rec.blocks, "a failed batch must not publish anything")
	})

	t.Run("cancelled context never reaches the processor", func(t *testing.T) {
		t.Parallel()
		bp := &recordingBatchProcessor{}
		driver, rec := newDriver(t, bp)

		canceled, cancel := context.WithCancel(t.Context())
		cancel()

		require.NoError(t, driver.processBlocks(canceled, evmBlocks))
		require.Zero(t, bp.calls, "a cancelled context must not invoke the processor")
		require.Empty(t, rec.blocks)
	})
}
