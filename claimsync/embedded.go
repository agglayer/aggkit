package claimsync

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	claimsyncStorage "github.com/agglayer/aggkit/claimsync/storage"
	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	dbtypes "github.com/agglayer/aggkit/db/types"
	"github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

type claimEmbeddedProcessor struct {
	log     aggkitcommon.Logger
	storage claimsynctypes.ClaimStorager
}

func newEmbeddedProcessor(logger aggkitcommon.Logger, storage claimsynctypes.ClaimStorager) *claimEmbeddedProcessor {
	return &claimEmbeddedProcessor{
		log:     logger,
		storage: storage,
	}
}

// --- Embedded mode ---

// EmbeddedClaimSync is passed to bridgesync as a ClaimEventsProcessor.
// It has no own EVMDriver; bridgesync drives event download and calls ProcessClaimEvents
// from its own ProcessBlock, reusing bridgesync's transaction for atomicity.
type EmbeddedClaimSync struct {
	Appender  sync.LogAppenderMap
	Processor claimsynctypes.EmbeddedProcessor
	Reader    claimsynctypes.ClaimsReader
}

// Event combination claim events
type Event struct {
	Claim      *Claim
	UnsetClaim *UnsetClaim
	SetClaim   *SetClaim
}

func (e Event) String() string {
	parts := []string{}
	if e.Claim != nil {
		parts = append(parts, e.Claim.String())
	}
	if e.UnsetClaim != nil {
		parts = append(parts, e.UnsetClaim.String())
	}
	if e.SetClaim != nil {
		parts = append(parts, e.SetClaim.String())
	}
	return "claimsync.Event{" + strings.Join(parts, ", ") + "}"
}

// NewClaimStorage creates a claim storage instance for embedded mode, using the provided database connection.
func NewClaimStorage(
	database *sql.DB,
	logger aggkitcommon.Logger,
	syncerID claimsynctypes.ClaimSyncerID,
	dbQueryTimeout time.Duration,
) (claimsynctypes.ClaimStorager, error) {
	store, err := claimsyncStorage.New(logger, database, syncerID.String(), dbQueryTimeout)
	if err != nil {
		return nil, fmt.Errorf("claimsync: failed to create storage: %w", err)
	}
	return store, nil
}

// NewEmbedded creates a ClaimEventsProcessor for embedding inside bridgesync.
// It provides claimsync's claim event handlers (for appender merging) and processes
// claim events using bridgesync's own transaction — no separate DB or EVMDriver is created.
// The querier is typically bridgesync's processor (satisfies ClaimQuerier).
func NewEmbedded(
	ctx context.Context,
	storage claimsynctypes.ClaimStorager,
	bridgeAddr common.Address,
	ethClient aggkittypes.EthClienter,
	querier ClaimQuerier,
	syncerID claimsynctypes.ClaimSyncerID,
	dbQueryTimeout time.Duration,
	logger aggkitcommon.Logger,
) (*EmbeddedClaimSync, error) {
	proc := newEmbeddedProcessor(logger, storage)
	deployment, err := resolveBridgeDeployment(ctx, bridgeAddr, ethClient)
	if err != nil {
		return nil, fmt.Errorf("claimsync embedded: failed to detect chain type: %w", err)
	}

	appender, err := buildAppender(ctx, ethClient, storage, bridgeAddr, deployment, logger)
	if err != nil {
		return nil, fmt.Errorf("claimsync embedded: failed to build appender: %w", err)
	}

	logger.Infof("claimsync embedded created: bridgeAddr=%s sovereign=%t", bridgeAddr.String(), deployment.kind == SovereignChain)

	return &EmbeddedClaimSync{
		Processor: proc,
		Reader:    storage,
		Appender:  appender}, nil
}
func (p *claimEmbeddedProcessor) ProcessBlockWithTx(ctx context.Context, tx dbtypes.Querier, block sync.Block, eventRaw any) error {

	event, ok := eventRaw.(Event)
	if !ok {
		return fmt.Errorf("claimsync ProcessBlock: unexpected event type %T in block %d", event, block.Num)
	}

	if event.Claim != nil {
		if err := p.storage.InsertClaim(ctx, tx, *event.Claim); err != nil {
			p.log.Errorf("failed to insert claim event at block %d: %v", block.Num, err)
			return err
		}
	}

	if event.UnsetClaim != nil {
		if err := p.storage.InsertUnsetClaim(ctx, tx, *event.UnsetClaim); err != nil {
			p.log.Errorf("failed to insert unset_claim event at block %d: %v", block.Num, err)
			return err
		}
	}

	if event.SetClaim != nil {
		if err := p.storage.InsertSetClaim(ctx, tx, *event.SetClaim); err != nil {
			p.log.Errorf("failed to insert set_claim event at block %d: %v", block.Num, err)
			return err
		}
	}

	return nil
}

// ReorgWithTx deletes all blocks >= firstReorgedBlock using the provided transaction.
// The caller is responsible for commit and rollback.
// If it's embbedded maybe have been deleted the block already in the same tx
// it returns:
// - the number of rows affected (currently the number of blocks deleted)
// - error if the deletion failed, or nil if successful
func (p *claimEmbeddedProcessor) ReorgWithTx(ctx context.Context, tx dbtypes.Querier, firstReorgedBlock uint64) (int64, error) {
	return p.deleteBlocksFrom(ctx, tx, firstReorgedBlock)
}

func (p *claimEmbeddedProcessor) deleteBlocksFrom(ctx context.Context, tx dbtypes.Querier, firstReorgedBlock uint64) (int64, error) {
	rowsAffected, err := p.storage.DeleteBlocksFrom(ctx, tx, firstReorgedBlock)
	if err != nil {
		return 0, fmt.Errorf("claimsync deleteBlocksFrom: %w", err)
	}
	return rowsAffected, nil
}
