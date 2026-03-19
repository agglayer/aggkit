package sync

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/agglayer/aggkit/db/compatibility"
	compmocks "github.com/agglayer/aggkit/db/compatibility/mocks"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/reorgdetector"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var (
	reorgDetectorID = "foo"
	errUnittest     = errors.New("unittest error")
)

func TestSync(t *testing.T) {
	rh := &RetryHandler{
		MaxRetryAttemptsAfterError: 5,
		RetryAfterErrorPeriod:      time.Millisecond * 100,
	}
	ctx := t.Context()
	rdm := NewReorgDetectorMock(t)
	pm := NewProcessorMock(t)
	dm := NewDownloaderMock(t)
	compatibilityCheckerMock := compmocks.NewCompatibilityChecker(t)
	compatibilityCheckerMock.EXPECT().Check(ctx, mock.Anything).Return(nil)

	firstReorgedBlock := make(chan uint64)
	reorgProcessed := make(chan bool)
	rdm.EXPECT().Subscribe(reorgDetectorID).
		Return(
			&reorgdetector.Subscription{
				ReorgedBlock:   firstReorgedBlock,
				ReorgProcessed: reorgProcessed,
			}, nil)
	driver, err := NewEVMDriver(rdm, pm, dm, reorgDetectorID, 10, rh, compatibilityCheckerMock)
	require.NoError(t, err)
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
	dm.EXPECT().Download(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(ctx context.Context, _ uint64, downloadedCh chan EVMBlock, _ *uint64, _ bool) {
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
	pm.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(3), true, nil)
	rdm.EXPECT().AddBlockToTrack(mock.Anything, reorgDetectorID, expectedBlock1.Num, expectedBlock1.Hash).Return(nil)
	pm.EXPECT().ProcessBlock(mock.Anything, Block{Num: expectedBlock1.Num, Events: expectedBlock1.Events, Hash: expectedBlock1.Hash}).
		Return(nil)
	rdm.EXPECT().AddBlockToTrack(mock.Anything, reorgDetectorID, expectedBlock2.Num, expectedBlock2.Hash).
		Return(nil)
	pm.EXPECT().
		ProcessBlock(mock.Anything, Block{Num: expectedBlock2.Num, Events: expectedBlock2.Events, Hash: expectedBlock2.Hash}).
		Return(nil)
	go driver.Sync(ctx, nil)
	time.Sleep(time.Millisecond * 200) // time to download expectedBlock1

	// Trigger reorg 1
	reorgedBlock1 := uint64(5)
	pm.EXPECT().Reorg(ctx, reorgedBlock1).Return(nil)
	firstReorgedBlock <- reorgedBlock1
	ok := <-reorgProcessed
	require.True(t, ok)
	reorg1Completed.mu.Lock()
	reorg1Completed.green = true
	reorg1Completed.mu.Unlock()
	time.Sleep(time.Millisecond * 200) // time to download expectedBlock2

	// Trigger reorg 2: syncer restarts the process
	reorgedBlock2 := uint64(7)
	pm.EXPECT().Reorg(ctx, reorgedBlock2).Return(nil)
	firstReorgedBlock <- reorgedBlock2
	ok = <-reorgProcessed
	require.True(t, ok)
}

func TestSync_ReorgCancelsRetryHandlerInHandleNewBlock(t *testing.T) {
	rh := &RetryHandler{
		MaxRetryAttemptsAfterError: -1, // infinite retries
		RetryAfterErrorPeriod:      100 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	rdm := NewReorgDetectorMock(t)
	pm := NewProcessorMock(t)
	dm := NewDownloaderMock(t)
	compatibilityCheckerMock := compmocks.NewCompatibilityChecker(t)
	compatibilityCheckerMock.EXPECT().Check(ctx, mock.Anything).Return(nil)

	var (
		reorgedBlockCh   = make(chan uint64)
		reorgProcessedCh = make(chan bool)
	)

	rdm.EXPECT().Subscribe(reorgDetectorID).
		Return(&reorgdetector.Subscription{
			ReorgedBlock:   reorgedBlockCh,
			ReorgProcessed: reorgProcessedCh,
		}, nil)

	driver, err := NewEVMDriver(rdm, pm, dm, reorgDetectorID, 10, rh, compatibilityCheckerMock)
	require.NoError(t, err)

	reorgedBlock := uint64(5)

	expectedBlock := EVMBlock{
		EVMBlockHeader: EVMBlockHeader{
			Num:  10,
			Hash: common.HexToHash("a"),
		},
	}

	cancelObserved := make(chan struct{})

	// infinite loop that keeps feeding the same block
	dm.EXPECT().Download(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(ctx context.Context, _ uint64, ch chan EVMBlock, _ *uint64, _ bool) {
			for {
				ch <- expectedBlock
				select {
				case <-ctx.Done():
					close(ch)
					return
				case <-time.After(50 * time.Millisecond):
					continue
				}
			}
		})

	pm.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(3), true, nil)

	// AddBlockToTrack always returns nil
	rdm.EXPECT().AddBlockToTrack(mock.Anything, reorgDetectorID, expectedBlock.Num, expectedBlock.Hash).
		Return(nil)

	// ProcessBlock always errors, until cancelled
	pm.EXPECT().
		ProcessBlock(mock.Anything, Block{Num: expectedBlock.Num, Hash: expectedBlock.Hash}).
		RunAndReturn(func(ctx context.Context, _ Block) error {
			select {
			case <-ctx.Done():
				close(cancelObserved)
				return ctx.Err()
			case <-time.After(50 * time.Millisecond):
				return errors.New("processing failed")
			}
		})

	go driver.Sync(ctx, nil)

	time.Sleep(300 * time.Millisecond) // Let it retry a few times

	// trigger reorg while it's retrying
	pm.EXPECT().Reorg(ctx, reorgedBlock).Return(nil)
	reorgedBlockCh <- reorgedBlock

	ok := <-reorgProcessedCh
	require.True(t, ok)
}

func TestHandleNewBlock(t *testing.T) {
	type call struct {
		addBlockErrs []error
		processErrs  []error
		expectedErr  error
	}

	tests := []struct {
		name  string
		block EVMBlock
		calls call
	}{
		{
			name: "happy path",
			block: EVMBlock{
				EVMBlockHeader: EVMBlockHeader{
					Num:  1,
					Hash: common.HexToHash("f00"),
				},
			},
			calls: call{
				addBlockErrs: []error{nil},
				processErrs:  []error{nil},
			},
		},
		{
			name: "reorg detector fails once",
			block: EVMBlock{
				EVMBlockHeader: EVMBlockHeader{
					Num:  2,
					Hash: common.HexToHash("f00"),
				},
			},
			calls: call{
				addBlockErrs: []error{errors.New("foo"), nil},
				processErrs:  []error{nil},
			},
		},
		{
			name: "processor fails once",
			block: EVMBlock{
				EVMBlockHeader: EVMBlockHeader{
					Num:  3,
					Hash: common.HexToHash("f00"),
				},
			},
			calls: call{
				addBlockErrs: []error{nil},
				processErrs:  []error{errors.New("foo"), nil},
			},
		},
		{
			name: "processor returns ErrInconsistentState",
			block: EVMBlock{
				EVMBlockHeader: EVMBlockHeader{
					Num:  4,
					Hash: common.HexToHash("f00"),
				},
			},
			calls: call{
				addBlockErrs: []error{nil},
				processErrs:  []error{ErrInconsistentState},
				expectedErr:  ErrInconsistentState,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rh := &RetryHandler{
				MaxRetryAttemptsAfterError: 5,
				RetryAfterErrorPeriod:      100 * time.Millisecond,
			}

			ctx := t.Context()
			rdm := NewReorgDetectorMock(t)
			pm := NewProcessorMock(t)
			dm := NewDownloaderMock(t)
			compatibilityCheckerMock := compmocks.NewCompatibilityChecker(t)

			rdm.EXPECT().Subscribe(reorgDetectorID).Return(&reorgdetector.Subscription{}, nil)
			driver, err := NewEVMDriver(rdm, pm, dm, reorgDetectorID, 10, rh, compatibilityCheckerMock)
			require.NoError(t, err)

			// Expectations for AddBlockToTrack
			for _, err := range tt.calls.addBlockErrs {
				rdm.EXPECT().
					AddBlockToTrack(ctx, reorgDetectorID, tt.block.Num, tt.block.Hash).
					Return(err).Once()
			}

			// Expectations for ProcessBlock
			for _, err := range tt.calls.processErrs {
				pm.EXPECT().
					ProcessBlock(ctx, Block{Num: tt.block.Num, Events: tt.block.Events, Hash: tt.block.Hash}).
					Return(err).Once()
			}

			err = driver.handleNewBlock(ctx, tt.block)

			if tt.calls.expectedErr != nil {
				require.ErrorIs(t, err, tt.calls.expectedErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestHandleReorg(t *testing.T) {
	tests := []struct {
		name              string
		firstReorgedBlock uint64
		reorgReturns      []error
	}{
		{
			name:              "happy path",
			firstReorgedBlock: 5,
			reorgReturns:      []error{nil},
		},
		{
			name:              "processor fails twice then succeeds",
			firstReorgedBlock: 7,
			reorgReturns: []error{
				errors.New("first failure"),
				errors.New("second failure"),
				nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rh := &RetryHandler{
				MaxRetryAttemptsAfterError: 5,
				RetryAfterErrorPeriod:      10 * time.Millisecond,
			}

			rdm := NewReorgDetectorMock(t)
			pm := NewProcessorMock(t)
			dm := NewDownloaderMock(t)
			compatChecker := compmocks.NewCompatibilityChecker(t)

			reorgProcessed := make(chan bool, 1) // buffer to avoid blocking

			rdm.EXPECT().Subscribe(reorgDetectorID).
				Return(&reorgdetector.Subscription{ReorgProcessed: reorgProcessed}, nil)

			ctx := t.Context()
			driver, err := NewEVMDriver(rdm, pm, dm, reorgDetectorID, 10, rh, compatChecker)
			require.NoError(t, err)

			// Set expectations for Reorg calls
			for _, ret := range tt.reorgReturns {
				pm.EXPECT().
					Reorg(mock.Anything, tt.firstReorgedBlock).
					Return(ret).
					Once()
			}

			// Call the method
			err = driver.handleReorg(ctx, tt.firstReorgedBlock)
			require.NoError(t, err)

			// Wait for signal with timeout
			select {
			case ok := <-reorgProcessed:
				require.True(t, ok, "expected true on reorgProcessed channel")
			case <-time.After(100 * time.Millisecond):
				t.Fatal("timeout waiting for ReorgProcessed signal")
			}
		})
	}
}

func TestCheckCompatibility(t *testing.T) {
	reorgDetectorMock := NewReorgDetectorMock(t)
	processorMock := NewProcessorMock(t)
	downloaderMock := NewDownloaderMock(t)
	retryHandler := &RetryHandler{
		MaxRetryAttemptsAfterError: 1,
		RetryAfterErrorPeriod:      time.Millisecond * 1,
	}
	compatibilityCheckerMock := compmocks.NewCompatibilityChecker(t)

	reorgDetectorMock.EXPECT().Subscribe(reorgDetectorID).Return(&reorgdetector.Subscription{}, nil)

	driver, err := NewEVMDriver(reorgDetectorMock, processorMock, downloaderMock, reorgDetectorID, 10, retryHandler, compatibilityCheckerMock)
	require.NoError(t, err)
	driver.compatibilityChecker = compatibilityCheckerMock
	t.Run("pass compatibility check", func(t *testing.T) {
		compatibilityCheckerMock.EXPECT().Check(context.Background(), nil).Return(nil)
		processorMock.EXPECT().GetLastProcessedBlock(context.Background()).Return(uint64(1), false, errUnittest)
		LogFatalf = func(format string, args ...any) {
			panic("should not call log.Fatalf")
		}
		require.Panics(t, func() {
			driver.Sync(context.Background(), nil)
		}, "should stop because GetLastProcessedBlock failed")
	})
	t.Run("fails compatibility check ", func(t *testing.T) {
		compatibilityCheckerMock.EXPECT().Check(context.Background(), nil).Return(errUnittest)
		LogFatalf = func(format string, args ...any) {
			panic("should not call log.Fatalf")
		}
		require.Panics(t, func() {
			driver.Sync(context.Background(), nil)
		}, "should stop because GetLastProcessedBlock failed")
	})
}

func TestEVMDriver_Sync(t *testing.T) {
	tests := []struct {
		name string // description of this test case
		// Named input parameters for receiver constructor.
		reorgDetector        ReorgDetector
		processor            processorInterface
		downloader           Downloader
		reorgDetectorID      string
		downloadBufferSize   int
		rh                   *RetryHandler
		compatibilityChecker compatibility.CompatibilityChecker
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := NewEVMDriver(tt.reorgDetector, tt.processor, tt.downloader, tt.reorgDetectorID, tt.downloadBufferSize, tt.rh, tt.compatibilityChecker)
			if err != nil {
				t.Fatalf("could not construct receiver type: %v", err)
			}
			d.Sync(context.Background(), nil)
		})
	}
}

func TestEVMDriver_GetCompletionPercentage(t *testing.T) {
	sut := &EVMDriver{}
	require.Nil(t, sut.GetCompletionPercentage(), "expected GetCompletionPercentage to return nil for legacy syncer")
}

func TestRuntimeData_String(t *testing.T) {
	tests := []struct {
		name     string
		data     RuntimeData
		expected string
	}{
		{
			name: "empty addresses",
			data: RuntimeData{
				ChainID:   1,
				Addresses: []common.Address{},
			},
			expected: "ChainID: 1, Addresses: ",
		},
		{
			name: "single address",
			data: RuntimeData{
				ChainID:   1,
				Addresses: []common.Address{common.HexToAddress("0x123")},
			},
			expected: "ChainID: 1, Addresses: 0x0000000000000000000000000000000000000123, ",
		},
		{
			name: "two addresses",
			data: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
					common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"),
				},
			},
			expected: "ChainID: 1, Addresses: 0x1234567890AbcdEF1234567890aBcdef12345678, 0xABcdEFABcdEFabcdEfAbCdefabcdeFABcDEFabCD, ",
		},
		{
			name: "multiple addresses",
			data: RuntimeData{
				ChainID: 42,
				Addresses: []common.Address{
					common.HexToAddress("0x123"),
					common.HexToAddress("0x456"),
					common.HexToAddress("0x789"),
				},
			},
			expected: "ChainID: 42, Addresses: 0x0000000000000000000000000000000000000123, 0x0000000000000000000000000000000000000456, 0x0000000000000000000000000000000000000789, ",
		},
		{
			name: "zero chain ID",
			data: RuntimeData{
				ChainID:   0,
				Addresses: []common.Address{common.HexToAddress("0xabc")},
			},
			expected: "ChainID: 0, Addresses: 0x0000000000000000000000000000000000000aBc, ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.data.String()
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestRuntimeData_IsCompatible_Success(t *testing.T) {
	tests := []struct {
		name  string
		data1 RuntimeData
		data2 RuntimeData
	}{
		{
			name: "identical data with single address",
			data1: RuntimeData{
				ChainID:   1,
				Addresses: []common.Address{common.HexToAddress("0x123")},
			},
			data2: RuntimeData{
				ChainID:   1,
				Addresses: []common.Address{common.HexToAddress("0x123")},
			},
		},
		{
			name: "identical data with two addresses",
			data1: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
					common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"),
				},
			},
			data2: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
					common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"),
				},
			},
		},
		{
			name: "identical data with multiple addresses",
			data1: RuntimeData{
				ChainID: 42,
				Addresses: []common.Address{
					common.HexToAddress("0x123"),
					common.HexToAddress("0x456"),
					common.HexToAddress("0x789"),
				},
			},
			data2: RuntimeData{
				ChainID: 42,
				Addresses: []common.Address{
					common.HexToAddress("0x123"),
					common.HexToAddress("0x456"),
					common.HexToAddress("0x789"),
				},
			},
		},
		{
			name: "both have empty addresses",
			data1: RuntimeData{
				ChainID:   1,
				Addresses: []common.Address{},
			},
			data2: RuntimeData{
				ChainID:   1,
				Addresses: []common.Address{},
			},
		},
		{
			name: "zero chain ID with matching data",
			data1: RuntimeData{
				ChainID:   0,
				Addresses: []common.Address{common.HexToAddress("0x789")},
			},
			data2: RuntimeData{
				ChainID:   0,
				Addresses: []common.Address{common.HexToAddress("0x789")},
			},
		},
		{
			name: "zero chain ID with matching data",
			data1: RuntimeData{
				ChainID:   0,
				Addresses: []common.Address{common.HexToAddress("0x789")},
			},
			data2: RuntimeData{
				ChainID:   0,
				Addresses: []common.Address{common.HexToAddress("0x789")},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.data1.IsCompatible(tt.data2)
			require.NoError(t, err)
			require.Nil(t, result)
		})
	}
}

func TestRuntimeData_IsCompatible_ChainIDMismatch(t *testing.T) {
	tests := []struct {
		name     string
		data1    RuntimeData
		data2    RuntimeData
		chainID1 uint64
		chainID2 uint64
	}{
		{
			name: "different chain IDs with same address",
			data1: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
				},
			},
			data2: RuntimeData{
				ChainID: 2,
				Addresses: []common.Address{
					common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
				},
			},
			chainID1: 1,
			chainID2: 2,
		},
		{
			name: "chain ID 0 vs 1",
			data1: RuntimeData{
				ChainID:   0,
				Addresses: []common.Address{common.HexToAddress("0x123")},
			},
			data2: RuntimeData{
				ChainID:   1,
				Addresses: []common.Address{common.HexToAddress("0x123")},
			},
			chainID1: 0,
			chainID2: 1,
		},
		{
			name: "large chain ID difference",
			data1: RuntimeData{
				ChainID:   1,
				Addresses: []common.Address{common.HexToAddress("0x123")},
			},
			data2: RuntimeData{
				ChainID:   999999,
				Addresses: []common.Address{common.HexToAddress("0x123")},
			},
			chainID1: 1,
			chainID2: 999999,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.data1.IsCompatible(tt.data2)
			require.Error(t, err)
			require.Nil(t, result)
			require.Contains(t, err.Error(), "chain ID mismatch")
		})
	}
}

func TestRuntimeData_IsCompatible_AddressesLenMismatch(t *testing.T) {
	tests := []struct {
		name  string
		data1 RuntimeData
		data2 RuntimeData
	}{
		{
			name: "data1 has more addresses",
			data1: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
					common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"),
				},
			},
			data2: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
				},
			},
		},
		{
			name: "data2 has more addresses",
			data1: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
				},
			},
			data2: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
					common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"),
				},
			},
		},
		{
			name: "data1 empty, data2 has addresses",
			data1: RuntimeData{
				ChainID:   1,
				Addresses: []common.Address{},
			},
			data2: RuntimeData{
				ChainID:   1,
				Addresses: []common.Address{common.HexToAddress("0x123")},
			},
		},
		{
			name: "data1 has addresses, data2 empty",
			data1: RuntimeData{
				ChainID:   1,
				Addresses: []common.Address{common.HexToAddress("0x123")},
			},
			data2: RuntimeData{
				ChainID:   1,
				Addresses: []common.Address{},
			},
		},
		{
			name: "large difference in address count",
			data1: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x111"),
					common.HexToAddress("0x222"),
					common.HexToAddress("0x333"),
					common.HexToAddress("0x444"),
					common.HexToAddress("0x555"),
				},
			},
			data2: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x111"),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.data1.IsCompatible(tt.data2)
			require.Error(t, err)
			require.Nil(t, result)
			require.Contains(t, err.Error(), "addresses len mismatch")
		})
	}
}

func TestRuntimeData_IsCompatible_AddressMismatch(t *testing.T) {
	tests := []struct {
		name  string
		data1 RuntimeData
		data2 RuntimeData
		index int
	}{
		{
			name: "single address mismatch",
			data1: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
				},
			},
			data2: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"),
				},
			},
			index: 0,
		},
		{
			name: "first address differs",
			data1: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x123"),
					common.HexToAddress("0x456"),
				},
			},
			data2: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x789"),
					common.HexToAddress("0x456"),
				},
			},
			index: 0,
		},
		{
			name: "second address differs",
			data1: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x123"),
					common.HexToAddress("0x456"),
				},
			},
			data2: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x123"),
					common.HexToAddress("0x789"),
				},
			},
			index: 1,
		},
		{
			name: "middle address differs in longer list",
			data1: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x111"),
					common.HexToAddress("0x222"),
					common.HexToAddress("0x333"),
					common.HexToAddress("0x444"),
				},
			},
			data2: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x111"),
					common.HexToAddress("0x222"),
					common.HexToAddress("0x999"),
					common.HexToAddress("0x444"),
				},
			},
			index: 2,
		},
		{
			name: "last address differs",
			data1: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x111"),
					common.HexToAddress("0x222"),
					common.HexToAddress("0x333"),
				},
			},
			data2: RuntimeData{
				ChainID: 1,
				Addresses: []common.Address{
					common.HexToAddress("0x111"),
					common.HexToAddress("0x222"),
					common.HexToAddress("0x999"),
				},
			},
			index: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.data1.IsCompatible(tt.data2)
			require.Error(t, err)
			require.Nil(t, result)
			require.Contains(t, err.Error(), "addresses")
			require.Contains(t, err.Error(), "mismatch")
		})
	}
}

func TestRuntimeData_IsCompatible_ErrorPrecedence(t *testing.T) {
	t.Run("chain ID mismatch takes precedence over address differences", func(t *testing.T) {
		data1 := RuntimeData{
			ChainID:   1,
			Addresses: []common.Address{common.HexToAddress("0x123")},
		}
		data2 := RuntimeData{
			ChainID:   2,
			Addresses: []common.Address{common.HexToAddress("0x456")},
		}

		result, err := data1.IsCompatible(data2)
		require.Error(t, err)
		require.Nil(t, result)
		require.Contains(t, err.Error(), "chain ID mismatch")
	})

	t.Run("length mismatch checked before address comparison", func(t *testing.T) {
		data1 := RuntimeData{
			ChainID:   1,
			Addresses: []common.Address{common.HexToAddress("0x123")},
		}
		data2 := RuntimeData{
			ChainID: 1,
			Addresses: []common.Address{
				common.HexToAddress("0x456"),
				common.HexToAddress("0x789"),
			},
		}

		result, err := data1.IsCompatible(data2)
		require.Error(t, err)
		require.Nil(t, result)
		require.Contains(t, err.Error(), "addresses len mismatch")
	})
}

func TestRuntimeData_IsCompatible_NilAddresses(t *testing.T) {
	t.Run("both nil addresses", func(t *testing.T) {
		data1 := RuntimeData{
			ChainID:   1,
			Addresses: nil,
		}
		data2 := RuntimeData{
			ChainID:   1,
			Addresses: nil,
		}

		result, err := data1.IsCompatible(data2)
		require.NoError(t, err)
		require.Nil(t, result)
	})

	t.Run("one nil, one empty", func(t *testing.T) {
		data1 := RuntimeData{
			ChainID:   1,
			Addresses: nil,
		}
		data2 := RuntimeData{
			ChainID:   1,
			Addresses: []common.Address{},
		}

		result, err := data1.IsCompatible(data2)
		require.NoError(t, err)
		require.Nil(t, result)
	})

	t.Run("nil vs non-empty", func(t *testing.T) {
		data1 := RuntimeData{
			ChainID:   1,
			Addresses: nil,
		}
		data2 := RuntimeData{
			ChainID:   1,
			Addresses: []common.Address{common.HexToAddress("0x123")},
		}

		result, err := data1.IsCompatible(data2)
		require.Error(t, err)
		require.Nil(t, result)
		require.Contains(t, err.Error(), "addresses len mismatch")
	})
}

// makeDriver is a helper that creates an EVMDriver with fresh mocks for each test.
func makeDriver(t *testing.T) (*EVMDriver, *ReorgDetectorMock, *ProcessorMock, *DownloaderMock) {
	t.Helper()
	rh := &RetryHandler{
		MaxRetryAttemptsAfterError: 5,
		RetryAfterErrorPeriod:      10 * time.Millisecond,
	}
	rdm := NewReorgDetectorMock(t)
	pm := NewProcessorMock(t)
	dm := NewDownloaderMock(t)
	compatMock := compmocks.NewCompatibilityChecker(t)
	rdm.EXPECT().Subscribe(reorgDetectorID).Return(&reorgdetector.Subscription{}, nil)
	driver, err := NewEVMDriver(rdm, pm, dm, reorgDetectorID, 10, rh, compatMock)
	if err != nil {
		t.Fatalf("could not construct EVMDriver: %v", err)
	}
	return driver, rdm, pm, dm
}

// --- SyncNextBlock ---

func TestSyncNextBlock_AlreadyBootstrapped(t *testing.T) {
	t.Parallel()
	driver, _, pm, _ := makeDriver(t)
	pm.EXPECT().GetLastProcessedBlock(mock.Anything).Return(uint64(5), true, nil)

	err := driver.SyncNextBlock(t.Context(), 1)
	require.ErrorIs(t, err, ErrAlreadyBootstrapped)
}

func TestSyncNextBlock_GetLastProcessedBlockError(t *testing.T) {
	t.Parallel()
	driver, _, pm, _ := makeDriver(t)
	pm.EXPECT().GetLastProcessedBlock(mock.Anything).Return(uint64(0), false, errUnittest)

	err := driver.SyncNextBlock(t.Context(), 1)
	require.ErrorContains(t, err, "SyncNextBlock: getting last processed block")
	require.ErrorIs(t, err, errUnittest)
}

func TestSyncNextBlock_DownloadChannelClosedUnexpectedly(t *testing.T) {
	t.Parallel()
	driver, _, pm, dm := makeDriver(t)
	pm.EXPECT().GetLastProcessedBlock(mock.Anything).Return(uint64(0), false, nil)
	dm.EXPECT().Download(mock.Anything, uint64(5), mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, _ uint64, ch chan EVMBlock, _ *uint64, _ bool) {
			close(ch)
		})

	err := driver.SyncNextBlock(t.Context(), 5)
	require.ErrorContains(t, err, "download channel closed unexpectedly")
}

func TestSyncNextBlock_ContextCancelledBeforeBlock(t *testing.T) {
	t.Parallel()
	driver, _, pm, dm := makeDriver(t)
	ctx, cancel := context.WithCancel(t.Context())
	pm.EXPECT().GetLastProcessedBlock(mock.Anything).Return(uint64(0), false, nil)
	// The goroutine may or may not start before the select returns ctx.Done()
	dm.EXPECT().Download(mock.Anything, uint64(5), mock.Anything, mock.Anything, mock.Anything).
		Run(func(downloadCtx context.Context, _ uint64, _ chan EVMBlock, _ *uint64, _ bool) {
			<-downloadCtx.Done()
		}).Maybe()
	cancel()

	err := driver.SyncNextBlock(ctx, 5)
	require.ErrorIs(t, err, context.Canceled)
}

func TestSyncNextBlock_HappyPath(t *testing.T) {
	t.Parallel()
	driver, rdm, pm, dm := makeDriver(t)
	ctx := t.Context()
	expectedBlock := EVMBlock{
		EVMBlockHeader: EVMBlockHeader{Num: 5, Hash: common.HexToHash("0x5")},
	}

	pm.EXPECT().GetLastProcessedBlock(mock.Anything).Return(uint64(0), false, nil)
	dm.EXPECT().Download(mock.Anything, uint64(5), mock.Anything, mock.Anything, mock.Anything).
		Run(func(downloadCtx context.Context, _ uint64, ch chan EVMBlock, _ *uint64, _ bool) {
			ch <- expectedBlock
			<-downloadCtx.Done() // wait for cancel() triggered inside SyncNextBlock
		})
	rdm.EXPECT().AddBlockToTrack(mock.Anything, reorgDetectorID, expectedBlock.Num, expectedBlock.Hash).Return(nil)
	pm.EXPECT().ProcessBlock(mock.Anything, Block{Num: expectedBlock.Num, Hash: expectedBlock.Hash}).Return(nil)

	err := driver.SyncNextBlock(ctx, 5)
	require.NoError(t, err)
}

// --- Sync with firstBlockNumber ---

func TestSync_WithFirstBlockNumber_StartsFromGivenBlock(t *testing.T) {
	t.Parallel()

	rh := &RetryHandler{
		MaxRetryAttemptsAfterError: 5,
		RetryAfterErrorPeriod:      10 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	rdm := NewReorgDetectorMock(t)
	pm := NewProcessorMock(t)
	dm := NewDownloaderMock(t)
	compatMock := compmocks.NewCompatibilityChecker(t)
	compatMock.EXPECT().Check(mock.Anything, mock.Anything).Return(nil)
	rdm.EXPECT().Subscribe(reorgDetectorID).Return(&reorgdetector.Subscription{
		ReorgedBlock:   make(chan uint64),
		ReorgProcessed: make(chan bool),
	}, nil)

	driver, err := NewEVMDriver(rdm, pm, dm, reorgDetectorID, 10, rh, compatMock)
	require.NoError(t, err)

	firstBlockNum := uint64(42)
	// no processed blocks exist yet
	pm.EXPECT().GetLastProcessedBlock(mock.Anything).Return(uint64(0), false, nil)

	downloadStartedFrom := make(chan uint64, 1)
	dm.EXPECT().Download(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(downloadCtx context.Context, fromBlock uint64, ch chan EVMBlock, _ *uint64, _ bool) {
			downloadStartedFrom <- fromBlock
			<-downloadCtx.Done()
			close(ch)
		})

	go driver.Sync(ctx, &firstBlockNum)

	select {
	case from := <-downloadStartedFrom:
		require.Equal(t, firstBlockNum, from, "Download should start from firstBlockNumber when no processed blocks exist")
		cancel()
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for Download to be called with firstBlockNumber")
	}
}

// --- Sync waits when no processed blocks and no firstBlockNumber ---

func TestSync_WaitsWhenNoProcessedBlockAndNoFirstBlock(t *testing.T) {
	t.Parallel()

	rh := &RetryHandler{
		MaxRetryAttemptsAfterError: 5,
		RetryAfterErrorPeriod:      10 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(t.Context())

	rdm := NewReorgDetectorMock(t)
	pm := NewProcessorMock(t)
	dm := NewDownloaderMock(t)
	compatMock := compmocks.NewCompatibilityChecker(t)
	compatMock.EXPECT().Check(mock.Anything, mock.Anything).Return(nil)
	rdm.EXPECT().Subscribe(reorgDetectorID).Return(&reorgdetector.Subscription{
		ReorgedBlock:   make(chan uint64),
		ReorgProcessed: make(chan bool),
	}, nil)

	driver, err := NewEVMDriver(rdm, pm, dm, reorgDetectorID, 10, rh, compatMock)
	require.NoError(t, err)

	// GetLastProcessedBlock returns not-found on every call
	pm.EXPECT().GetLastProcessedBlock(mock.Anything).Return(uint64(0), false, nil).Maybe()

	done := make(chan struct{})
	go func() {
		defer close(done)
		driver.Sync(ctx, nil)
	}()

	time.Sleep(50 * time.Millisecond) // let it loop a few times with RetryAfterErrorPeriod
	cancel()

	select {
	case <-done:
		// good: Sync exited cleanly after context cancellation
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Sync did not exit after context cancellation")
	}
}
