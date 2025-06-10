package aggsender

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/agglayer/aggkit/aggsender/mocks"
	aggsendertypes "github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/etherman"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestExploratoryBlockNotifierPolling(t *testing.T) {
	t.Skip()
	urlRPCL1 := os.Getenv("L1URL")
	fmt.Println("URL=", urlRPCL1)
	ethClient, err := ethclient.Dial(urlRPCL1)
	require.NoError(t, err)

	sut, errSut := NewBlockNotifierPolling(ethClient,
		ConfigBlockNotifierPolling{
			BlockFinalityType: etherman.LatestBlock,
		}, log.WithFields("test", "test"), nil)
	require.NoError(t, errSut)
	go sut.Start(context.Background())
	ch := sut.Subscribe("test")
	for block := range ch {
		fmt.Println(block)
	}
}

func TestBlockNotifierPollingStep(t *testing.T) {
	time0 := time.Unix(1731322117, 0)
	period0 := time.Second * 10
	period0_80percent := time.Second * 8
	time1 := time0.Add(period0)
	tests := []struct {
		name                      string
		previousStatus            *blockNotifierPollingInternalStatus
		cfg                       *ConfigBlockNotifierPolling
		headerByNumberError       bool
		headerByNumberErrorNumber uint64
		forcedTime                time.Time
		mockLoggerFn              func() aggkitcommon.Logger
		expectedStatus            *blockNotifierPollingInternalStatus
		expectedDelay             time.Duration
		expectedEvent             *aggsendertypes.EventNewBlock
	}{
		{
			name:                      "initial->receive block",
			previousStatus:            nil,
			headerByNumberError:       false,
			headerByNumberErrorNumber: 100,
			forcedTime:                time0,
			expectedStatus: &blockNotifierPollingInternalStatus{
				lastBlockSeen: 100,
				lastBlockTime: time0,
			},
			expectedDelay: minBlockInterval,
			expectedEvent: nil,
		},
		{
			name:                "received block->error",
			previousStatus:      nil,
			headerByNumberError: true,
			forcedTime:          time0,
			expectedStatus:      &blockNotifierPollingInternalStatus{},
			expectedDelay:       minBlockInterval,
			expectedEvent:       nil,
		},

		{
			name: "have block period->receive new block",
			previousStatus: &blockNotifierPollingInternalStatus{
				lastBlockSeen:     100,
				lastBlockTime:     time0,
				previousBlockTime: &period0,
			},
			headerByNumberError:       false,
			headerByNumberErrorNumber: 101,
			forcedTime:                time1,
			expectedStatus: &blockNotifierPollingInternalStatus{
				lastBlockSeen:     101,
				lastBlockTime:     time1,
				previousBlockTime: &period0,
			},
			expectedDelay: period0_80percent,
			expectedEvent: &aggsendertypes.EventNewBlock{
				BlockNumber: 101,
			},
		},
		{
			name: "missed blocks - BlockFinalityType=LatestBlock",
			previousStatus: &blockNotifierPollingInternalStatus{
				lastBlockSeen:     100,
				lastBlockTime:     time0,
				previousBlockTime: &period0,
			},
			mockLoggerFn: func() aggkitcommon.Logger {
				mockLogger := mocks.NewLogger(t)
				mockLogger.EXPECT().Warnf("Missed block(s) [finality:%s]: %d -> %d", etherman.LatestBlock, uint64(100), uint64(105)).Once()
				return mockLogger
			},
			headerByNumberError:       false,
			headerByNumberErrorNumber: 105,
			forcedTime:                time1,
			expectedStatus: &blockNotifierPollingInternalStatus{
				lastBlockSeen: 105,
				lastBlockTime: time1,
			},
			expectedDelay: time.Second,
			expectedEvent: &aggsendertypes.EventNewBlock{
				BlockNumber: 105,
			},
		},
		{
			name: "missed blocks - BlockFinalityType=FinalizedBlock",
			cfg: &ConfigBlockNotifierPolling{
				BlockFinalityType: etherman.FinalizedBlock,
			},
			previousStatus: &blockNotifierPollingInternalStatus{
				lastBlockSeen:     100,
				lastBlockTime:     time0,
				previousBlockTime: &period0,
			},
			mockLoggerFn: func() aggkitcommon.Logger {
				// we do not expect any warning here
				// if the code logs a warning, it will fail the test
				// because we didn't mock it
				return mocks.NewLogger(t)
			},
			headerByNumberError:       false,
			headerByNumberErrorNumber: 105,
			forcedTime:                time1,
			expectedStatus: &blockNotifierPollingInternalStatus{
				lastBlockSeen: 105,
				lastBlockTime: time1,
			},
			expectedDelay: time.Second,
			expectedEvent: &aggsendertypes.EventNewBlock{
				BlockNumber: 105,
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			testData := newBlockNotifierPollingTestData(t, tt.cfg)

			timeNowFunc = func() time.Time {
				return tt.forcedTime
			}

			if tt.headerByNumberError == false {
				hdr1 := &types.Header{
					Number: big.NewInt(int64(tt.headerByNumberErrorNumber)),
				}
				testData.ethClientMock.EXPECT().HeaderByNumber(mock.Anything, mock.Anything).Return(hdr1, nil).Once()
			} else {
				testData.ethClientMock.EXPECT().HeaderByNumber(mock.Anything, mock.Anything).Return(nil, fmt.Errorf("error")).Once()
			}

			if tt.mockLoggerFn != nil {
				testData.sut.logger = tt.mockLoggerFn()
			}

			delay, newStatus, event := testData.sut.step(context.TODO(), tt.previousStatus)
			require.Equal(t, tt.expectedDelay, delay, "delay")
			require.Equal(t, tt.expectedStatus, newStatus, "new_status")
			if tt.expectedEvent == nil {
				require.Nil(t, event, "send_event")
			} else {
				require.Equal(t, tt.expectedEvent.BlockNumber, event.BlockNumber, "send_event")
			}
		})
	}
}

func TestDelayNoPreviousBLock(t *testing.T) {
	testData := newBlockNotifierPollingTestData(t, nil)
	status := blockNotifierPollingInternalStatus{
		lastBlockSeen: 100,
	}
	delay := testData.sut.nextBlockRequestDelay(&status, nil)
	require.Equal(t, minBlockInterval, delay)
}

func TestDelayBLock(t *testing.T) {
	testData := newBlockNotifierPollingTestData(t, nil)
	pt := time.Second * 10
	status := blockNotifierPollingInternalStatus{
		lastBlockSeen:     100,
		previousBlockTime: &pt,
	}
	delay := testData.sut.nextBlockRequestDelay(&status, nil)
	require.Equal(t, minBlockInterval, delay)
}

func TestNewBlockNotifierPolling(t *testing.T) {
	testData := newBlockNotifierPollingTestData(t, nil)
	require.NotNil(t, testData.sut)
	_, err := NewBlockNotifierPolling(testData.ethClientMock, ConfigBlockNotifierPolling{
		BlockFinalityType: etherman.NewBlockNumberFinality("invalid"),
	}, log.WithFields("test", "test"), nil)
	require.Error(t, err)
}

func TestBlockNotifierPollingString(t *testing.T) {
	testData := newBlockNotifierPollingTestData(t, nil)
	require.NotEmpty(t, testData.sut.String())
	testData.sut.lastStatus = &blockNotifierPollingInternalStatus{
		lastBlockSeen: 100,
	}
	require.NotEmpty(t, testData.sut.String())
}

func TestBlockNotifierPollingStart(t *testing.T) {
	testData := newBlockNotifierPollingTestData(t, nil)
	ch := testData.sut.Subscribe("test")
	hdr1 := &types.Header{
		Number: big.NewInt(100),
	}
	testData.ethClientMock.EXPECT().HeaderByNumber(mock.Anything, mock.Anything).Return(hdr1, nil).Once()
	hdr2 := &types.Header{
		Number: big.NewInt(101),
	}
	testData.ethClientMock.EXPECT().HeaderByNumber(mock.Anything, mock.Anything).Return(hdr2, nil).Once()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go testData.sut.Start(ctx)
	block := <-ch
	require.NotNil(t, block)
	require.Equal(t, uint64(101), block.BlockNumber)
}

func TestBlockGetCurrentBlockNumber(t *testing.T) {
	testData := newBlockNotifierPollingTestData(t, nil)
	bn := testData.sut.GetCurrentBlockNumber()
	require.Equal(t, uint64(0), bn, "no block means block 0")
	hdr0 := &types.Header{
		Number: big.NewInt(int64(10)),
	}
	hdr1 := &types.Header{
		Number: big.NewInt(int64(100)),
	}
	testData.ethClientMock.EXPECT().HeaderByNumber(mock.Anything, mock.Anything).Return(hdr0, nil).Once()
	testData.ethClientMock.EXPECT().HeaderByNumber(mock.Anything, mock.Anything).Return(hdr1, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go testData.sut.Start(ctx)
	ch := testData.sut.Subscribe("test")
	block := <-ch
	require.NotNil(t, block)
	require.Equal(t, uint64(100), testData.sut.GetCurrentBlockNumber())
}

type blockNotifierPollingTestData struct {
	sut           *BlockNotifierPolling
	ethClientMock *mocks.EthClient
	ctx           context.Context
}

func newBlockNotifierPollingTestData(t *testing.T, config *ConfigBlockNotifierPolling) blockNotifierPollingTestData {
	t.Helper()
	if config == nil {
		config = &ConfigBlockNotifierPolling{
			BlockFinalityType:     etherman.LatestBlock,
			CheckNewBlockInterval: 0,
		}
	}
	ethClientMock := mocks.NewEthClient(t)
	logger := log.WithFields("test", "BlockNotifierPolling")
	sut, err := NewBlockNotifierPolling(ethClientMock, *config, logger, nil)
	require.NoError(t, err)
	return blockNotifierPollingTestData{
		sut:           sut,
		ethClientMock: ethClientMock,
		ctx:           context.TODO(),
	}
}
