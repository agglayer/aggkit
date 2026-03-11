package claimsync

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/agglayer/aggkit/bridgesync"
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

// embeddedClaimSync is passed to bridgesync as a ClaimEventsProcessor.
// It has no own EVMDriver; bridgesync drives event download and calls ProcessClaimEvents
// from its own ProcessBlock, reusing bridgesync's transaction for atomicity.
type embeddedClaimSync struct {
	Appender  sync.LogAppenderMap
	Processor *claimEmbeddedProcessor
	Reader    claimsynctypes.ClaimsReader
}

// NewClaimStorage creates a claim storage instance for embedded mode, using the provided database connection.
func NewClaimStorage(
	database *sql.DB,
	logger aggkitcommon.Logger,
	syncerID claimsynctypes.ClaimSyncerID,
) (claimsynctypes.ClaimStorager, error) {
	store, err := claimsyncStorage.New(logger, database, syncerID.String())
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
) (*embeddedClaimSync, error) {
	proc := newEmbeddedProcessor(logger, storage)
	agglayerBridgeContract, err := agglayerbridge.NewAgglayerbridge(bridgeAddr, ethClient)
	if err != nil {
		return nil, fmt.Errorf("claimsync embedded: failed to create AgglayerBridge binding: %w", err)
	}
	reader := NewProcessorReader(logger, storage)

	isSovereign, agglayerBridgeL2Contract, err := detectSovereignChain(ctx, bridgeAddr, ethClient)
	if err != nil {
		return nil, fmt.Errorf("claimsync embedded: failed to detect chain type: %w", err)
	}

	appender, err := buildAppender(ctx, ethClient, reader, bridgeAddr,
		agglayerBridgeContract, agglayerBridgeL2Contract, isSovereign, logger)
	if err != nil {
		return nil, fmt.Errorf("claimsync embedded: failed to build appender: %w", err)
	}

	logger.Infof("claimsync embedded created: bridgeAddr=%s sovereign=%t", bridgeAddr.String(), isSovereign)

	return &embeddedClaimSync{
		Processor: proc,
		Reader:    reader,
		Appender:  appender}, nil
}
func (p *claimEmbeddedProcessor) ProcessBlockWithTx(tx dbtypes.Querier, block *sync.Block, insertBlock bool) error {
	if insertBlock {
		if err := p.storage.InsertBlock(tx, block.Num, block.Hash.String()); err != nil {
			p.log.Errorf("failed to insert block %d: %v", block.Num, err)
			return err
		}
	}

	for _, e := range block.Events {
		event, ok := e.(bridgesync.Event)
		if !ok {
			p.log.Errorf("failed to convert event to bridgesync.Event type in block %d", block.Num)
			return fmt.Errorf("claimsync ProcessBlock: unexpected event type %T in block %d", e, block.Num)
		}

		if event.Claim != nil {
			if err := p.storage.InsertClaim(tx, *event.Claim); err != nil {
				p.log.Errorf("failed to insert claim event at block %d: %v", block.Num, err)
				return err
			}
		}

		if event.UnsetClaim != nil {
			if err := p.storage.InsertUnsetClaim(tx, *event.UnsetClaim); err != nil {
				p.log.Errorf("failed to insert unset_claim event at block %d: %v", block.Num, err)
				return err
			}
		}

		if event.SetClaim != nil {
			if err := p.storage.InsertSetClaim(tx, *event.SetClaim); err != nil {
				p.log.Errorf("failed to insert set_claim event at block %d: %v", block.Num, err)
				return err
			}
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
func (p *claimEmbeddedProcessor) ReorgWithTx(tx dbtypes.Querier, firstReorgedBlock uint64) (int64, error) {
	return p.deleteBlocksFrom(tx, firstReorgedBlock)
}

func (p *claimEmbeddedProcessor) deleteBlocksFrom(tx dbtypes.Querier, firstReorgedBlock uint64) (int64, error) {
	rowsAffected, err := p.storage.DeleteBlocksFrom(tx, firstReorgedBlock)
	if err != nil {
		return 0, fmt.Errorf("claimsync deleteBlocksFrom: %w", err)
	}
	return rowsAffected, nil
}
