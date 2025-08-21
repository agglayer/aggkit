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
		maxIterations         = 500 // Increased from 100 to 200 for longer timeout
		sleepTimePerIteration = 2 * time.Second
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
		time.Sleep(sleepTimePerIteration)
	}
	require.Fail(t, fmt.Sprintf("processor not updated. Last block: %d, target block: %d", lpb, targetBlock))
}
