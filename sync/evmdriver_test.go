package sync

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/reorgdetector"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var (
	reorgDetectorID = "foo"
	forcedError     = errors.New("forced error")
)

func TestSync(t *testing.T) {
	rh := &RetryHandler{
		MaxRetryAttemptsAfterError: 5,
		RetryAfterErrorPeriod:      time.Millisecond * 100,
	}
	rdm := NewReorgDetectorMock(t)
	pm := NewProcessorMock(t)
	dm := NewEVMDownloaderMock(t)
	firstReorgedBlock := make(chan uint64)
	reorgProcessed := make(chan bool)
	rdm.On("Subscribe", reorgDetectorID).Return(&reorgdetector.Subscription{
		ReorgedBlock:   firstReorgedBlock,
		ReorgProcessed: reorgProcessed,
	}, nil)
	driver, err := NewEVMDriver(rdm, pm, dm, reorgDetectorID, 10, rh)
	require.NoError(t, err)
	ctx := context.Background()
	expectedBlock1 := EVMBlock{
		EVMBlockHeader: EVMBlockHeader{
			Num:  3,
			Hash: common.HexToHash("03"),
		},
	}
	expectedBlock2 := EVMBlock{
		EVMBlockHeader: EVMBlockHeader{
			Num:  9,
			Hash: common.HexToHash("09"),
		},
	}
	type reorgSemaphore struct {
		mu    sync.Mutex
		green bool
	}
	reorg1Completed := reorgSemaphore{}
	dm.EXPECT().GetRuntimeData(ctx).Return(RuntimeData{}, nil)
	pm.EXPECT().CheckCompatibilityData(RuntimeData{}).Return(nil)
	dm.On("Download", mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		ctx, ok := args.Get(0).(context.Context)
		if !ok {
			log.Error("failed to assert type for context")
			return
		}

		downloadedCh, ok := args.Get(2).(chan EVMBlock)
		if !ok {
			log.Error("failed to assert type for downloadedCh")
			return
		}

		log.Info("entering mock loop")
		for {
			select {
			case <-ctx.Done():
				log.Info("closing channel")
				close(downloadedCh)
				return
			default:
			}
			reorg1Completed.mu.Lock()
			green := reorg1Completed.green
			reorg1Completed.mu.Unlock()
			if green {
				downloadedCh <- expectedBlock2
			} else {
				downloadedCh <- expectedBlock1
			}
			time.Sleep(100 * time.Millisecond)
		}
	})

	// Mocking this actions, the driver should "store" all the blocks from the downloader
	pm.On("GetLastProcessedBlock", ctx).
		Return(uint64(3), nil)
	rdm.On("AddBlockToTrack", ctx, reorgDetectorID, expectedBlock1.Num, expectedBlock1.Hash).
		Return(nil)
	pm.On("ProcessBlock", ctx, Block{Num: expectedBlock1.Num, Events: expectedBlock1.Events, Hash: expectedBlock1.Hash}).
		Return(nil)
	rdm.On("AddBlockToTrack", ctx, reorgDetectorID, expectedBlock2.Num, expectedBlock2.Hash).
		Return(nil)
	pm.On("ProcessBlock", ctx, Block{Num: expectedBlock2.Num, Events: expectedBlock2.Events, Hash: expectedBlock2.Hash}).
		Return(nil)
	go driver.Sync(ctx)
	time.Sleep(time.Millisecond * 200) // time to download expectedBlock1

	// Trigger reorg 1
	reorgedBlock1 := uint64(5)
	pm.On("Reorg", ctx, reorgedBlock1).Return(nil)
	firstReorgedBlock <- reorgedBlock1
	ok := <-reorgProcessed
	require.True(t, ok)
	reorg1Completed.mu.Lock()
	reorg1Completed.green = true
	reorg1Completed.mu.Unlock()
	time.Sleep(time.Millisecond * 200) // time to download expectedBlock2

	// Trigger reorg 2: syncer restarts the porcess
	reorgedBlock2 := uint64(7)
	pm.On("Reorg", ctx, reorgedBlock2).Return(nil)
	firstReorgedBlock <- reorgedBlock2
	ok = <-reorgProcessed
	require.True(t, ok)
}

func TestHandleNewBlock(t *testing.T) {
	rh := &RetryHandler{
		MaxRetryAttemptsAfterError: 5,
		RetryAfterErrorPeriod:      time.Millisecond * 100,
	}
	rdm := NewReorgDetectorMock(t)
	pm := NewProcessorMock(t)
	dm := NewEVMDownloaderMock(t)
	rdm.On("Subscribe", reorgDetectorID).Return(&reorgdetector.Subscription{}, nil)
	driver, err := NewEVMDriver(rdm, pm, dm, reorgDetectorID, 10, rh)
	require.NoError(t, err)
	ctx := context.Background()

	// happy path
	b1 := EVMBlock{
		EVMBlockHeader: EVMBlockHeader{
			Num:  1,
			Hash: common.HexToHash("f00"),
		},
	}
	rdm.
		On("AddBlockToTrack", ctx, reorgDetectorID, b1.Num, b1.Hash).
		Return(nil)
	pm.On("ProcessBlock", ctx, Block{Num: b1.Num, Events: b1.Events, Hash: b1.Hash}).
		Return(nil)
	driver.handleNewBlock(ctx, nil, b1)

	// reorg deteector fails once
	b2 := EVMBlock{
		EVMBlockHeader: EVMBlockHeader{
			Num:  2,
			Hash: common.HexToHash("f00"),
		},
	}
	rdm.
		On("AddBlockToTrack", ctx, reorgDetectorID, b2.Num, b2.Hash).
		Return(errors.New("foo")).Once()
	rdm.
		On("AddBlockToTrack", ctx, reorgDetectorID, b2.Num, b2.Hash).
		Return(nil).Once()
	pm.On("ProcessBlock", ctx, Block{Num: b2.Num, Events: b2.Events, Hash: b2.Hash}).
		Return(nil)
	driver.handleNewBlock(ctx, nil, b2)

	// processor fails once
	b3 := EVMBlock{
		EVMBlockHeader: EVMBlockHeader{
			Num:  3,
			Hash: common.HexToHash("f00"),
		},
	}
	rdm.
		On("AddBlockToTrack", ctx, reorgDetectorID, b3.Num, b3.Hash).
		Return(nil)
	pm.On("ProcessBlock", ctx, Block{Num: b3.Num, Events: b3.Events, Hash: b3.Hash}).
		Return(errors.New("foo")).Once()
	pm.On("ProcessBlock", ctx, Block{Num: b3.Num, Events: b3.Events, Hash: b3.Hash}).
		Return(nil).Once()
	driver.handleNewBlock(ctx, nil, b3)

	// inconsistent state error
	b4 := EVMBlock{
		EVMBlockHeader: EVMBlockHeader{
			Num:  4,
			Hash: common.HexToHash("f00"),
		},
	}
	rdm.
		On("AddBlockToTrack", ctx, reorgDetectorID, b4.Num, b4.Hash).
		Return(nil)
	pm.On("ProcessBlock", ctx, Block{Num: b4.Num, Events: b4.Events, Hash: b4.Hash}).
		Return(ErrInconsistentState)
	cancelIsCalled := false
	cancel := func() {
		cancelIsCalled = true
	}
	driver.handleNewBlock(ctx, cancel, b4)
	require.True(t, cancelIsCalled)
}

func TestHandleReorg(t *testing.T) {
	rh := &RetryHandler{
		MaxRetryAttemptsAfterError: 5,
		RetryAfterErrorPeriod:      time.Millisecond * 100,
	}
	rdm := NewReorgDetectorMock(t)
	pm := NewProcessorMock(t)
	dm := NewEVMDownloaderMock(t)
	reorgProcessed := make(chan bool)
	rdm.On("Subscribe", reorgDetectorID).Return(&reorgdetector.Subscription{
		ReorgProcessed: reorgProcessed,
	}, nil)
	driver, err := NewEVMDriver(rdm, pm, dm, reorgDetectorID, 10, rh)
	require.NoError(t, err)
	ctx := context.Background()

	// happy path
	_, cancel := context.WithCancel(ctx)
	firstReorgedBlock := uint64(5)
	pm.On("Reorg", ctx, firstReorgedBlock).Return(nil)
	go driver.handleReorg(ctx, cancel, firstReorgedBlock)
	done := <-reorgProcessed
	require.True(t, done)

	// processor fails 2 times
	_, cancel = context.WithCancel(ctx)
	firstReorgedBlock = uint64(7)
	pm.On("Reorg", ctx, firstReorgedBlock).Return(errors.New("foo")).Once()
	pm.On("Reorg", ctx, firstReorgedBlock).Return(errors.New("foo")).Once()
	pm.On("Reorg", ctx, firstReorgedBlock).Return(nil).Once()
	go driver.handleReorg(ctx, cancel, firstReorgedBlock)
	done = <-reorgProcessed
	require.True(t, done)
}

type loggerPanicMock struct {
}

func (l *loggerPanicMock) Fatal(args ...any) {
	panic("should not call log.Fatal")
}

func (l *loggerPanicMock) Fatalf(format string, args ...any) {
	panic("should not call log.Fatalf")
}
func (l *loggerPanicMock) Info(args ...any) {
	log.Info(args...)
}
func (l *loggerPanicMock) Infof(format string, args ...any) {
	log.Infof(format, args...)
}
func (l *loggerPanicMock) Error(args ...any) {
	log.Error(args...)
}
func (l *loggerPanicMock) Errorf(format string, args ...any) {
	log.Errorf(format, args...)
}
func (l *loggerPanicMock) Warn(args ...any) {
	log.Warn(args...)
}
func (l *loggerPanicMock) Warnf(format string, args ...any) {
	log.Warnf(format, args...)
}
func (l *loggerPanicMock) Debug(args ...any) {
	log.Debug(args...)
}
func (l *loggerPanicMock) Debugf(format string, args ...any) {
	log.Debugf(format, args...)
}

func TestCheckCompatibility(t *testing.T) {
	reorgDetectorMock := NewReorgDetectorMock(t)
	processorMock := NewProcessorMock(t)
	evmDownloaderMock := NewEVMDownloaderMock(t)
	retryHandler := &RetryHandler{
		MaxRetryAttemptsAfterError: 1,
		RetryAfterErrorPeriod:      time.Millisecond * 1,
	}
	reorgDetectorMock.EXPECT().Subscribe(reorgDetectorID).Return(&reorgdetector.Subscription{}, nil)

	driver, err := NewEVMDriver(reorgDetectorMock, processorMock, evmDownloaderMock, reorgDetectorID, 10, retryHandler)
	require.NoError(t, err)
	t.Run("pass compatibility check", func(t *testing.T) {
		evmDownloaderMock.EXPECT().GetRuntimeData(context.Background()).Return(RuntimeData{}, nil)
		processorMock.EXPECT().CheckCompatibilityData(RuntimeData{}).Return(nil)
		processorMock.EXPECT().GetLastProcessedBlock(context.Background()).Return(uint64(1), forcedError)
		LogFatalf = func(format string, args ...any) {
			panic("should not call log.Fatalf")
		}
		require.Panics(t, func() {
			driver.Sync(context.Background())
		}, "should stop because GetLastProcessedBlock failed")
	})

	t.Run("fail compatibility check", func(t *testing.T) {
		evmDownloaderMock.EXPECT().GetRuntimeData(context.Background()).Return(RuntimeData{}, nil)
		processorMock.EXPECT().CheckCompatibilityData(RuntimeData{}).Return(forcedError)
		// This log, in case of fatal, calls Panic insted of Exit() so it can be catched by the test
		driver.log = &loggerPanicMock{}
		require.Panics(t, func() {
			driver.Sync(context.Background())
		}, "should stop because CheckCompatibilityData failed, this invoke a log.Fatal")
	})
}
