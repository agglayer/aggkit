package aggsender

import (
	"context"
	"fmt"
	"testing"

	"github.com/agglayer/aggkit/agglayer"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestConfigEpochNotifierPerBlockString(t *testing.T) {
	cfg := ConfigEpochNotifierPerBlock{
		StartingEpochBlock:          123,
		NumBlockPerEpoch:            456,
		EpochNotificationPercentage: 789,
	}
	require.Equal(t, "{startEpochBlock=123, sizeEpoch=456, threshold=789%}", cfg.String())
	var cfg2 *ConfigEpochNotifierPerBlock
	require.Equal(t, "nil", cfg2.String())
}

func TestStartingBlockEpoch(t *testing.T) {
	testData := newNotifierPerBlockTestData(t, &ConfigEpochNotifierPerBlock{
		StartingEpochBlock:          9,
		NumBlockPerEpoch:            10,
		EpochNotificationPercentage: 80,
	})
	// EPOCH: ---0 ----+----1 -----+----2 ----+----3 ----+----4 ----+----5 ----+----
	// BLOCK:          9           19  	      29         39         49
	require.Equal(t, uint64(8), testData.sut.startingBlockEpoch(0))
	require.Equal(t, uint64(9), testData.sut.startingBlockEpoch(1))
	require.Equal(t, uint64(19), testData.sut.startingBlockEpoch(2))
}

func TestEpochNotifyPercentageEdgeCase0(t *testing.T) {
	testData := newNotifierPerBlockTestData(t, nil)
	testData.sut.Config.EpochNotificationPercentage = 0
	notify, epoch := testData.sut.isNotificationRequired(9, 0)
	require.True(t, notify)
	require.Equal(t, uint64(1), epoch)
}

// if percent is 99 means at end of epoch, so in a config 0, epoch-size=10,
// 99% means last block of epoch
func TestEpochNotifyPercentageEdgeCase99(t *testing.T) {
	testData := newNotifierPerBlockTestData(t, nil)
	testData.sut.Config.EpochNotificationPercentage = 99
	notify, epoch := testData.sut.isNotificationRequired(9, 0)
	require.True(t, notify)
	require.Equal(t, uint64(1), epoch)
}

func TestEpochStep(t *testing.T) {
	testData := newNotifierPerBlockTestData(t, &ConfigEpochNotifierPerBlock{
		StartingEpochBlock:          9,
		NumBlockPerEpoch:            10,
		EpochNotificationPercentage: 50,
	})
	// EPOCH: ---0 ----+----1 -----+----2 ----+----3 ----+----4 ----+----5 ----+----
	// BLOCK:          9           19  	      29         39         49
	// start EPOCH#1 -> 9
	// end EPOCH#1 -> 19
	// start EPOCH#2 -> 19

	tests := []struct {
		name                       string
		initialStatus              internalStatus
		blockNumber                uint64
		expectedEvent              bool
		expectedEventEpoch         uint64
		expectedEventPendingBlocks int
	}{
		{
			name:                       "First block of epoch, no notification until close to end",
			initialStatus:              internalStatus{lastBlockSeen: 8, waitingForEpoch: 0},
			blockNumber:                9,
			expectedEvent:              false,
			expectedEventEpoch:         1,
			expectedEventPendingBlocks: 0,
		},
		{
			name:                       "epoch#1 close to end, notify it!",
			initialStatus:              internalStatus{lastBlockSeen: 17, waitingForEpoch: 0},
			blockNumber:                18,
			expectedEvent:              true,
			expectedEventEpoch:         1, // Finishing epoch 0
			expectedEventPendingBlocks: 1, // 19 - 18
		},
		{
			name:          "epoch#1 close to end, but already notified",
			initialStatus: internalStatus{lastBlockSeen: 17, waitingForEpoch: 2},
			blockNumber:   18,
			expectedEvent: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, event := testData.sut.step(tt.initialStatus, types.EventNewBlock{BlockNumber: tt.blockNumber, BlockFinalityType: aggkittypes.LatestBlock})
			require.Equal(t, tt.expectedEvent, event != nil)
			if event != nil {
				require.Equal(t, tt.expectedEventEpoch, event.Epoch, "Epoch")
				extraInfo, ok := event.ExtraInfo.(*ExtraInfoEventEpoch)
				require.True(t, ok, "ExtraInfo")
				require.Equal(t, tt.expectedEventPendingBlocks, extraInfo.PendingBlocks, "PendingBlocks")
			}
		})
	}
}

func TestNewConfigEpochNotifierPerBlock(t *testing.T) {
	ctx := context.Background()
	_, err := NewConfigEpochNotifierPerBlock(ctx, nil, 1)
	require.Error(t, err)
	aggLayerMock := agglayer.NewAgglayerClientMock(t)
	aggLayerMock.On("GetEpochConfiguration", mock.Anything).Return(nil, fmt.Errorf("error")).Once()
	_, err = NewConfigEpochNotifierPerBlock(ctx, aggLayerMock, 1)
	require.Error(t, err)
	cfgAggLayer := &agglayertypes.ClockConfiguration{
		GenesisBlock:  123,
		EpochDuration: 456,
	}
	aggLayerMock.On("GetEpochConfiguration", mock.Anything).Return(cfgAggLayer, nil).Once()
	cfg, err := NewConfigEpochNotifierPerBlock(ctx, aggLayerMock, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(123), cfg.StartingEpochBlock)
	require.Equal(t, uint(456), cfg.NumBlockPerEpoch)
}

func TestNotifyEpoch(t *testing.T) {
	testData := newNotifierPerBlockTestData(t, nil)
	ch := testData.sut.Subscribe("test")
	chBlocks := make(chan types.EventNewBlock)
	testData.blockNotifierMock.EXPECT().Subscribe(mock.Anything).Return(chBlocks)
	testData.sut.StartAsync(testData.ctx)
	chBlocks <- types.EventNewBlock{BlockNumber: 109, BlockFinalityType: aggkittypes.LatestBlock}
	epochEvent := <-ch
	require.Equal(t, uint64(11), epochEvent.Epoch)
	testData.ctx.Done()
}

func TestStepSameEpoch(t *testing.T) {
	testData := newNotifierPerBlockTestData(t, nil)
	status := internalStatus{
		lastBlockSeen:   100,
		waitingForEpoch: testData.sut.epochNumber(100),
	}
	newStatus, _ := testData.sut.step(status, types.EventNewBlock{BlockNumber: 103, BlockFinalityType: aggkittypes.LatestBlock})
	require.Equal(t, uint64(103), newStatus.lastBlockSeen)
	require.Equal(t, status.waitingForEpoch, newStatus.waitingForEpoch)
}

func TestStepNotifyEpoch(t *testing.T) {
	testData := newNotifierPerBlockTestData(t, nil)
	status := internalStatus{
		lastBlockSeen:   100,
		waitingForEpoch: testData.sut.epochNumber(100),
	}
	status, _ = testData.sut.step(status, types.EventNewBlock{BlockNumber: 109, BlockFinalityType: aggkittypes.LatestBlock})
	require.Equal(t, uint64(109), status.lastBlockSeen)
	require.Equal(t, uint64(12), status.waitingForEpoch)
}

func TestBlockEpochNumber(t *testing.T) {
	testData := newNotifierPerBlockTestData(t, &ConfigEpochNotifierPerBlock{
		StartingEpochBlock:          105,
		NumBlockPerEpoch:            10,
		EpochNotificationPercentage: 1,
	})
	require.Equal(t, uint64(0), testData.sut.epochNumber(0))
	require.Equal(t, uint64(0), testData.sut.epochNumber(104))
	require.Equal(t, uint64(1), testData.sut.epochNumber(105))
	require.Equal(t, uint64(1), testData.sut.epochNumber(114))
	require.Equal(t, uint64(2), testData.sut.epochNumber(115))
	require.Equal(t, uint64(2), testData.sut.epochNumber(116))
	require.Equal(t, uint64(2), testData.sut.epochNumber(124))
	require.Equal(t, uint64(3), testData.sut.epochNumber(125))
}

func TestBlockBeforeEpoch(t *testing.T) {
	testData := newNotifierPerBlockTestData(t, &ConfigEpochNotifierPerBlock{
		StartingEpochBlock:          105,
		NumBlockPerEpoch:            10,
		EpochNotificationPercentage: 1,
	})
	status := internalStatus{
		lastBlockSeen:   104,
		waitingForEpoch: testData.sut.epochNumber(104),
	}
	newStatus, _ := testData.sut.step(status, types.EventNewBlock{BlockNumber: 104, BlockFinalityType: aggkittypes.LatestBlock})
	// We are previous block of first epoch, so we should do nothing
	require.Equal(t, status, newStatus)
	status = newStatus
	// First block of first epoch
	newStatus, _ = testData.sut.step(status, types.EventNewBlock{BlockNumber: 105, BlockFinalityType: aggkittypes.LatestBlock})
	require.Equal(t, uint64(105), newStatus.lastBlockSeen)
	// Near end  first epoch
	newStatus, _ = testData.sut.step(status, types.EventNewBlock{BlockNumber: 114, BlockFinalityType: aggkittypes.LatestBlock})
	require.Equal(t, uint64(114), newStatus.lastBlockSeen)
}

func TestGetEpochStatus(t *testing.T) {
	testData := newNotifierPerBlockTestData(t, nil)
	testData.blockNotifierMock.EXPECT().GetCurrentBlockNumber().Return(uint64(105))
	status := testData.sut.GetEpochStatus()
	require.Equal(t, uint64(11), status.Epoch)
	require.Equal(t, float64(0.5), status.PercentEpoch)
}

type notifierPerBlockTestData struct {
	sut               *EpochNotifierPerBlock
	blockNotifierMock *mocks.BlockNotifier
	ctx               context.Context
}

func newNotifierPerBlockTestData(t *testing.T, config *ConfigEpochNotifierPerBlock) notifierPerBlockTestData {
	t.Helper()
	if config == nil {
		config = &ConfigEpochNotifierPerBlock{
			StartingEpochBlock:          0,
			NumBlockPerEpoch:            10,
			EpochNotificationPercentage: 50,
		}
	}
	blockNotifierMock := mocks.NewBlockNotifier(t)
	logger := log.WithFields("test", "EpochNotifierPerBlock")
	sut, err := NewEpochNotifierPerBlock(blockNotifierMock, logger, *config, nil)
	require.NoError(t, err)
	return notifierPerBlockTestData{
		sut:               sut,
		blockNotifierMock: blockNotifierMock,
		ctx:               context.TODO(),
	}
}
