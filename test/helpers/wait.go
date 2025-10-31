package helpers

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/stretchr/testify/require"
)

type Processorer interface {
	GetLastProcessedBlock(ctx context.Context) (uint64, error)
}

func RequireProcessorUpdated(t *testing.T, processor Processorer, targetBlock uint64, ethClient aggkittypes.BaseEthereumClienter) {
	t.Helper()
	const (
		maxIterations = int(200)
		sleepInterval = 50 * time.Millisecond
		logEvery      = 30 // Log every 30th iteration (every 3 seconds)
	)

	var (
		lastProcessedBlock uint64
		networkBlock       uint64
		err                error
	)

	ctx := context.Background()
	for i := range maxIterations {
		lastProcessedBlock, err = processor.GetLastProcessedBlock(ctx)
		if errors.Is(err, sync.ErrInconsistentState) {
			time.Sleep(sleepInterval)
			continue
		}

		require.NoError(t, err)
		if lastProcessedBlock >= targetBlock {
			return
		}

		if i%logEvery == 0 {
			if ethClient != nil {
				networkBlock, err = ethClient.BlockNumber(ctx)
				require.NoError(t, err)
			}

			t.Logf("Waiting for processor to catch up: last processed block=%d, target block=%d, last block in network=%d, iteration=%d",
				lastProcessedBlock, targetBlock, networkBlock, i)
		}
		time.Sleep(sleepInterval)
	}
	require.Failf(t,
		fmt.Sprintf("processor not updated after %d iterations (~%.1fs)", maxIterations, float64(maxIterations)*sleepInterval.Seconds()),
		"last processed block=%d, target block=%d", lastProcessedBlock, targetBlock,
	)
}
