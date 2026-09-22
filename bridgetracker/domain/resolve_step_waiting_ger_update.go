package domain

import (
	"context"
	"fmt"
	"time"

	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
)

// ResultFindFirstL1InfoTreeAfterBlock is read off the GlobalExitRoot contract's own state as of
// the block of the last UpdateL1InfoTree/UpdateL1InfoTreeV2 log found (see
// FindFirstL1InfoTreeAfterBlock), not parsed from the log itself: the two events are not
// mutually exclusive — they normally fire together for the same update — so every field here is
// populated regardless of which of the two matched
type ResultFindFirstL1InfoTreeAfterBlock struct {
	LeafCount       uint32
	BlockNumber     uint64
	BlockTimestamp  uint64
	LogIndex        uint
	GER             common.Hash
	MainnetExitRoot common.Hash
	RollupExitRoot  common.Hash
}

type WaitingGERUpdateSource interface {
	FindFirstL1InfoTreeAfterBlock(
		ctx context.Context, blockNumber uint64, logIndex uint32,
	) (*ResultFindFirstL1InfoTreeAfterBlock, error)
}

// WaitingGERUpdateResolver resolves StepWaitingGERUpdate: whether the Global Exit Root has
// been updated to cover an L1-originated bridge. Only ever the current step of an L1->L2 path,
// since ExpectedPath omits it otherwise
type WaitingGERUpdateResolver struct {
	port WaitingGERUpdateSource
}

func NewWaitingGERUpdateResolver(logger aggkitcommon.Logger, port WaitingGERUpdateSource) *WaitingGERUpdateResolver {
	return &WaitingGERUpdateResolver{port: port}
}

// Resolve implements StepResolver
func (r *WaitingGERUpdateResolver) Resolve(
	logger aggkitcommon.Logger, ctx context.Context, tracking *TrackingData, _ int,
) (any, error) {
	blockNumber := tracking.Info().BlockNumber
	logIndex := tracking.Info().LogIndex
	result, err := r.port.FindFirstL1InfoTreeAfterBlock(ctx, blockNumber, logIndex)
	if err != nil {
		return nil, fmt.Errorf("origin GER: %w", err)
	}
	if result == nil {
		return nil, ErrStepPending
	}
	return &types.GERUpdateResult{
		L1InfoTreeIndex: result.LeafCount - 1,
		GER:             result.GER,
		MainnetExitRoot: result.MainnetExitRoot,
		RollupExitRoot:  result.RollupExitRoot,
		BlockNumber:     result.BlockNumber,
		BlockTimestamp:  result.BlockTimestamp,
		LogIndex:        result.LogIndex,
	}, nil
}

// StartDate returns the origin deposit's own block timestamp: this resolver is only ever the
// first step of an L1->L2 path (see ExpectedPath), whose beginning is the bridge's own creation,
// not "the step before it finished" — there is none
func (r *WaitingGERUpdateResolver) StartDate(info *BridgeInfo, _ any) *time.Time {
	return blockTime(info.BlockTimestamp)
}

// EndDate returns result's own L1 block timestamp: the GER update this step waits for is a
// single on-chain event, so its own block is exactly when the milestone was met
func (r *WaitingGERUpdateResolver) EndDate(result any) *time.Time {
	update, ok := result.(*types.GERUpdateResult)
	if !ok {
		return nil
	}
	return blockTime(update.BlockTimestamp)
}

// Warning never has anything to report: EndDate's own value either exists or falls back to now,
// with no partial-failure mode of its own worth explaining
func (r *WaitingGERUpdateResolver) Warning(_ any) *string {
	return nil
}
