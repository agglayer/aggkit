package helpers

import (
	"context"
	"fmt"
	"testing"
	"time"

	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/stretchr/testify/require"
)

type Processorer interface {
	GetLastProcessedBlock(ctx context.Context) (uint64, error)
}

func RequireProcessorUpdated(t *testing.T, processor Processorer, targetBlock uint64, ethClient aggkittypes.BaseEthereumClienter) {
	t.Helper()
	const (
		maxIterations         = 200
		sleepTimePerIteration = 500 * time.Millisecond
	)
	var (
		lpb                uint64
		err                error
		lastBlockInNetwork uint64
	)
	ctx := context.Background()
	for i := 0; i < maxIterations; i++ {
		if ethClient != nil {
			lastBlockInNetwork, err = ethClient.BlockNumber(ctx)
			require.NoError(t, err)
		}
		lpb, err = processor.GetLastProcessedBlock(ctx)
		require.NoError(t, err)
		if targetBlock <= lpb {
			return
		}

		if i%30 == 0 { // Log every 30th iteration (every 3 seconds)
			t.Logf("Waiting for processor to catch up: last processed block=%d, target block=%d, last_block_in_network: %d,  iteration=%d",
				lpb, targetBlock, lastBlockInNetwork, i)
		}
		time.Sleep(sleepTimePerIteration)
	}
	require.Fail(t, fmt.Sprintf("processor not updated. Last block: %d, target block: %d", lpb, targetBlock))
}
