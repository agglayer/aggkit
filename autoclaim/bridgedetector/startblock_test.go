package bridgedetector

import (
	"context"
	"errors"
	"testing"
	"time"

	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// fakeL1Client is a minimal aggkittypes.EthClienter backing only CustomHeaderByNumber, the only
// method ResolveStartBlock calls. Embedding the (nil) interface satisfies every other method without
// implementing it; calling one would nil-panic, which is exactly what should happen if
// ResolveStartBlock is ever changed to need more than headers.
type fakeL1Client struct {
	aggkittypes.EthClienter
	// blockTime maps a block number to its timestamp. 10s/block starting at block 0 = time 0,
	// unless overridden.
	blockTime func(block uint64) uint64
	headBlock uint64
	calls     []uint64 // every block number requested, Constant(head) recorded as headBlock
	err       error
}

func (c *fakeL1Client) CustomHeaderByNumber(
	_ context.Context, number *aggkittypes.BlockNumberFinality,
) (*aggkittypes.BlockHeader, error) {
	if c.err != nil {
		return nil, c.err
	}
	block := c.headBlock
	if number.Block == aggkittypes.Constant {
		block = number.Specific
	}
	c.calls = append(c.calls, block)
	return aggkittypes.NewBlockHeader(block, common.Hash{}, c.blockTime(block), nil), nil
}

func linearBlockTime(secondsPerBlock uint64) func(uint64) uint64 {
	return func(block uint64) uint64 {
		return block * secondsPerBlock
	}
}

func uint64Ptr(v uint64) *uint64 { return &v }

func TestResolveStartBlockUsesExplicitValueVerbatimWithoutAnyRPCCall(t *testing.T) {
	client := &fakeL1Client{err: errors.New("must not be called")}
	configured := uint64Ptr(5_000_000)

	resolution, err := ResolveStartBlock(
		context.Background(), client, configured, time.Hour, 100, time.Now(), nil, "test")

	require.NoError(t, err)
	require.Equal(t, uint64(5_000_000), resolution.Block)
	require.Empty(t, client.calls)
}

func TestResolveStartBlockExplicitZeroUsedVerbatimWithoutAnyRPCCall(t *testing.T) {
	client := &fakeL1Client{err: errors.New("must not be called")}
	configured := uint64Ptr(0)

	resolution, err := ResolveStartBlock(
		context.Background(), client, configured, time.Hour, 100, time.Now(), nil, "test")

	require.NoError(t, err)
	require.Equal(t, uint64(0), resolution.Block)
	require.Empty(t, client.calls)
}

func TestResolveStartBlockUnsetResolvesFromLookback(t *testing.T) {
	// 12s/block, head at block 10,000 (timestamp 120,000). A 1h (3600s) lookback from a "now" equal
	// to the head timestamp targets timestamp 116,400, i.e. block 9,700.
	client := &fakeL1Client{blockTime: linearBlockTime(12), headBlock: 10_000}
	now := time.Unix(120_000, 0).UTC()

	resolution, err := ResolveStartBlock(
		context.Background(), client, nil, time.Hour, 0, now, nil, "test")

	require.NoError(t, err)
	require.Equal(t, uint64(9_700), resolution.Block)
	// One call for the head header, plus a binary search over ~14 bits (10,000 blocks) -- nowhere
	// near a linear scan of the 10,000-block range.
	require.Less(t, len(client.calls), 20)
}

func TestResolveStartBlockClampsToHeadWhenLookbackIsNegativeOrZero(t *testing.T) {
	client := &fakeL1Client{blockTime: linearBlockTime(12), headBlock: 10_000}
	now := time.Unix(120_000, 0).UTC()

	resolution, err := ResolveStartBlock(context.Background(), client, nil, 0, 0, now, nil, "test")

	require.NoError(t, err)
	require.Equal(t, uint64(10_000), resolution.Block)
}

func TestResolveStartBlockClampsToMinBlockWhenLookbackPredatesIt(t *testing.T) {
	// minBlock is 9,900 (timestamp 118,800), well after the resolved lookback target (116,400 /
	// block 9,700): the result must never go below minBlock.
	client := &fakeL1Client{blockTime: linearBlockTime(12), headBlock: 10_000}
	now := time.Unix(120_000, 0).UTC()

	resolution, err := ResolveStartBlock(
		context.Background(), client, nil, time.Hour, 9_900, now, nil, "test")

	require.NoError(t, err)
	require.Equal(t, uint64(9_900), resolution.Block)
}

func TestResolveStartBlockClampsToHeadWhenHeadIsBelowMinBlock(t *testing.T) {
	client := &fakeL1Client{blockTime: linearBlockTime(12), headBlock: 50}

	resolution, err := ResolveStartBlock(
		context.Background(), client, nil, time.Hour, 100, time.Now(), nil, "test")

	require.NoError(t, err)
	require.Equal(t, uint64(50), resolution.Block)
}

func TestResolveStartBlockPropagatesL1ClientError(t *testing.T) {
	client := &fakeL1Client{err: errors.New("rpc down")}

	_, err := ResolveStartBlock(context.Background(), client, nil, time.Hour, 0, time.Now(), nil, "test")

	require.ErrorContains(t, err, "rpc down")
}
