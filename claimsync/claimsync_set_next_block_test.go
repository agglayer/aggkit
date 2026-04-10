package claimsync

import (
	"context"
	"testing"

	logger "github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// newTestClaimSyncForSetNext builds a minimal ClaimSync with a real processor (temp SQLite)
// and no driver, suitable for testing SetNextRequiredBlock cap behaviour.
// The "already has blocks" path is taken in all tests below so driver is never called.
func newTestClaimSyncForSetNext(t *testing.T, initialBlockNum uint64) *ClaimSync {
	t.Helper()
	proc := newTestProcessor(t)
	return &ClaimSync{
		processor: proc,
		cfg: ConfigStandalone{
			ConfigEmbedded:  ConfigEmbedded{BridgeAddr: common.Address{}},
			InitialBlockNum: initialBlockNum,
			BlockFinality:   *aggkittypes.NewBlockNumber(1000),
		},
		logger: logger.WithFields("module", "test"),
	}
}

// addProcessedBlock stores a single empty block in the processor so that
// GetLastProcessedBlock / GetFirstProcessedBlock return meaningful values.
func addProcessedBlock(t *testing.T, ctx context.Context, proc *processor, blockNum uint64) {
	t.Helper()
	require.NoError(t, proc.ProcessBlock(ctx, sync.Block{
		Num:  blockNum,
		Hash: common.HexToHash("0x01"),
	}))
}

func TestSetNextRequiredBlock_BelowInitialBlockNum_IsCapped(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	c := newTestClaimSyncForSetNext(t, 100)

	// Pre-populate the processor so found=true and driver is never reached.
	addProcessedBlock(t, ctx, c.processor, 50)

	err := c.SetNextRequiredBlock(ctx, 10) // 10 < 100 → capped to 100; 100 >= firstBlock(50), no error
	require.NoError(t, err)
}

func TestSetNextRequiredBlock_AboveInitialBlockNum_NotCapped(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	c := newTestClaimSyncForSetNext(t, 100)

	addProcessedBlock(t, ctx, c.processor, 50)

	err := c.SetNextRequiredBlock(ctx, 200) // 200 >= 100 → not capped; 200 >= firstBlock(50), no error
	require.NoError(t, err)
}

func TestSetNextRequiredBlock_EqualToInitialBlockNum_NotCapped(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	c := newTestClaimSyncForSetNext(t, 100)

	addProcessedBlock(t, ctx, c.processor, 50)

	err := c.SetNextRequiredBlock(ctx, 100) // 100 == 100 → not capped; >= firstBlock(50), no error
	require.NoError(t, err)
}

func TestSetNextRequiredBlock_CappedBelowFirstBlock_ReturnsError(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	// InitialBlockNum=5, firstBlock in DB=10 → capped value (5) < firstBlock → error
	c := newTestClaimSyncForSetNext(t, 5)

	addProcessedBlock(t, ctx, c.processor, 10)

	err := c.SetNextRequiredBlock(ctx, 3) // 3 < 5 → capped to 5; 5 < firstBlock(10) → error
	require.ErrorContains(t, err, "must be greater or equal than the first block in DB")
}
