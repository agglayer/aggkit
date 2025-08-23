package helpers

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type Processorer interface {
	GetLastProcessedBlock(ctx context.Context) (uint64, error)
}

func RequireProcessorUpdated(t *testing.T, processor Processorer, targetBlock uint64) {
	t.Helper()
	const (
		maxIterations         = 100
		sleepTimePerIteration = 200 * time.Millisecond
	)
	var (
		lpb uint64
		err error
	)
	ctx := context.Background()
	for i := 0; i < maxIterations; i++ {
		lpb, err = processor.GetLastProcessedBlock(ctx)
		require.NoError(t, err)
		if targetBlock <= lpb {
			return
		}

		if i%30 == 0 { // Log every 30th iteration (every 3 seconds)
			t.Logf("Waiting for processor to catch up: last processed block=%d, target block=%d, iteration=%d", lpb, targetBlock, i)
		}
		time.Sleep(sleepTimePerIteration)
	}
	require.Fail(t, fmt.Sprintf("processor not updated. Last block: %d, target block: %d", lpb, targetBlock))
}
