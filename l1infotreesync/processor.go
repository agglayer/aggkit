package l1infotreesync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	mutex "sync"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db"
	dbtypes "github.com/agglayer/aggkit/db/types"
	"github.com/agglayer/aggkit/l1infotreesync/migrations"
	"github.com/agglayer/aggkit/log"
	mdrsynctypes "github.com/agglayer/aggkit/multidownloader/sync/types"
	"github.com/agglayer/aggkit/sync"
	"github.com/agglayer/aggkit/tree"
	treetypes "github.com/agglayer/aggkit/tree/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/russross/meddler"
)

var (
	ErrBlockNotProcessed = errors.New("given block(s) have not been processed yet")
	ErrNoBlock0          = errors.New("blockNum must be greater than 0")
)

type processor struct {
	db             *sql.DB
	l1InfoTree     treetypes.FullTreer
	rollupExitTree treetypes.FullTreer
	mu             mutex.RWMutex
	halted         bool
	haltedReason   string
	// haltedBlock is the block number that caused the current halt, when known (set by
	// haltAtBlock). It's used to detect that a Reorg recovery attempt is being retried at the
	// same block with no progress, so it can be escalated. nil for halts raised through the
	// legacy halt(reason) entry point, which carry no block context.
	haltedBlock *uint64
	// lastReorgRecoveryBlock remembers the haltedBlock of the most recent Reorg call that was
	// invoked while the processor was halted. It intentionally survives unhalt/halt cycles: if
	// the next halt happens at the very same block, the corresponding Reorg call is the second
	// consecutive recovery attempt for that block, meaning the previous purge made no progress,
	// and the reorg must be escalated (see Reorg).
	lastReorgRecoveryBlock *uint64
	// haltGuardHits counts consecutive isHalted() short-circuits in ProcessBlocks/ProcessBlock,
	// so the "processor is halted" log can be throttled instead of firing on every call. Reset
	// on unhalt.
	haltGuardHits int
	// initialBlock is the syncer's configured starting block (Config.InitialBlock). It's used as
	// the fallback escalation target when a Reorg recovery must deepen but no verified checkpoint
	// has ever been recorded (e.g. a fresh or pre-upgrade DB).
	initialBlock uint64
	log          *log.Logger
}

// UpdateL1InfoTree representation of the UpdateL1InfoTree event
type UpdateL1InfoTree struct {
	BlockPosition   uint64
	MainnetExitRoot common.Hash
	RollupExitRoot  common.Hash
	ParentHash      common.Hash
	Timestamp       uint64
}

type UpdateL1InfoTreeV2 struct {
	CurrentL1InfoRoot common.Hash
	LeafCount         uint32
	Blockhash         common.Hash
	MinTimestamp      uint64
}

// VerifyBatches representation of the VerifyBatches and VerifyBatchesTrustedAggregator events
type VerifyBatches struct {
	BlockNumber   uint64         `meddler:"block_num"`
	BlockPosition uint64         `meddler:"block_pos"`
	RollupID      uint32         `meddler:"rollup_id"`
	NumBatch      uint64         `meddler:"batch_num"`
	StateRoot     common.Hash    `meddler:"state_root,hash"`
	ExitRoot      common.Hash    `meddler:"exit_root,hash"`
	Aggregator    common.Address `meddler:"aggregator,address"`

	// Not provided by downloader
	RollupExitRoot common.Hash `meddler:"rollup_exit_root,hash"`
}

func (v *VerifyBatches) String() string {
	return fmt.Sprintf("BlockNumber: %d, BlockPosition: %d, RollupID: %d, NumBatch: %d, StateRoot: %s, "+
		"ExitRoot: %s, Aggregator: %s, RollupExitRoot: %s",
		v.BlockNumber, v.BlockPosition, v.RollupID, v.NumBatch, v.StateRoot.String(),
		v.ExitRoot.String(), v.Aggregator.String(), v.RollupExitRoot.String())
}

type InitL1InfoRootMap struct {
	LeafCount         uint32
	CurrentL1InfoRoot common.Hash
}

func (i *InitL1InfoRootMap) String() string {
	return fmt.Sprintf("LeafCount: %d, CurrentL1InfoRoot: %s", i.LeafCount, i.CurrentL1InfoRoot.String())
}

type Event struct {
	UpdateL1InfoTree   *UpdateL1InfoTree
	UpdateL1InfoTreeV2 *UpdateL1InfoTreeV2
	VerifyBatches      *VerifyBatches
	InitL1InfoRootMap  *InitL1InfoRootMap
}

// L1InfoTreeLeaf representation of a leaf of the L1 Info tree
type L1InfoTreeLeaf struct {
	BlockNumber       uint64      `meddler:"block_num" json:"block_num"`
	BlockPosition     uint64      `meddler:"block_pos" json:"block_pos"`
	L1InfoTreeIndex   uint32      `meddler:"position" json:"l1_info_tree_index"`
	PreviousBlockHash common.Hash `meddler:"previous_block_hash,hash" json:"previous_block_hash"`
	Timestamp         uint64      `meddler:"timestamp" json:"timestamp"`
	MainnetExitRoot   common.Hash `meddler:"mainnet_exit_root,hash" json:"mainnet_exit_root"`
	RollupExitRoot    common.Hash `meddler:"rollup_exit_root,hash" json:"rollup_exit_root"`
	GlobalExitRoot    common.Hash `meddler:"global_exit_root,hash" json:"global_exit_root"`
	Hash              common.Hash `meddler:"hash,hash" json:"hash"`
}

func (l *L1InfoTreeLeaf) String() string {
	return fmt.Sprintf("BlockNumber: %d, BlockPosition: %d, L1InfoTreeIndex: %d, PreviousBlockHash: %s, "+
		"Timestamp: %d, MainnetExitRoot: %s, RollupExitRoot: %s, GlobalExitRoot: %s, Hash: %s",
		l.BlockNumber, l.BlockPosition, l.L1InfoTreeIndex, l.PreviousBlockHash.String(),
		l.Timestamp, l.MainnetExitRoot.String(), l.RollupExitRoot.String(), l.GlobalExitRoot.String(), l.Hash.String())
}

// L1InfoTreeInitial representation of the initial info of the L1 Info tree for this rollup
type L1InfoTreeInitial struct {
	BlockNumber uint64      `meddler:"block_num"`
	LeafCount   uint32      `meddler:"leaf_count"`
	L1InfoRoot  common.Hash `meddler:"l1_info_root,hash"`
}

func (l *L1InfoTreeInitial) String() string {
	return fmt.Sprintf("BlockNumber: %d, LeafCount: %d, L1InfoRoot: %s", l.BlockNumber, l.LeafCount, l.L1InfoRoot.String())
}

// Hash as expected by the tree
func (l *L1InfoTreeLeaf) GetHash() common.Hash {
	rawTimestamp := aggkitcommon.Uint64ToBigEndianBytes(l.Timestamp)
	return crypto.Keccak256Hash(l.GetGlobalExitRoot().Bytes(), l.PreviousBlockHash.Bytes(), rawTimestamp)
}

// GlobalExitRoot returns the GER
func (l *L1InfoTreeLeaf) GetGlobalExitRoot() common.Hash {
	return CalculateGER(l.MainnetExitRoot, l.RollupExitRoot)
}

// CalculateGER calculates the Global Exit Root (GER) based on the keccak256 hash of concatenated
// mainnet and rollup exit roots
func CalculateGER(mainnetExitRoot, rollupExitRoot common.Hash) common.Hash {
	return crypto.Keccak256Hash(mainnetExitRoot[:], rollupExitRoot[:])
}

func newProcessor(dbPath string) (*processor, error) {
	err := migrations.RunMigrations(dbPath)
	if err != nil {
		return nil, err
	}
	database, err := db.NewSQLiteDB(dbPath)
	if err != nil {
		return nil, err
	}
	return &processor{
		db:             database,
		l1InfoTree:     tree.NewAppendOnlyTree(database, migrations.L1InfoTreePrefix),
		rollupExitTree: tree.NewUpdatableTree(database, migrations.RollupExitTreePrefix),
		log:            log.WithFields("processor", "l1infotreesync"),
	}, nil
}

func (p *processor) getDB() *sql.DB {
	return p.db
}

// GetLatestL1InfoLeafUntilBlock returns the most recent L1InfoTreeLeaf that occurred before or at blockNum.
// If the blockNum has not been processed yet the error ErrBlockNotProcessed will be returned
func (p *processor) GetLatestL1InfoLeafUntilBlock(ctx context.Context, blockNum *uint64) (*L1InfoTreeLeaf, error) {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := tx.Rollback(); err != nil {
			p.log.Warnf("error rolling back tx: %v", err)
		}
	}()

	if blockNum != nil {
		if *blockNum == 0 {
			return nil, ErrNoBlock0
		}
		lpb, err := p.getLastProcessedBlockWithTx(tx)
		if err != nil {
			return nil, fmt.Errorf("failed to retrieve the last processed block: %w", err)
		}
		if lpb < *blockNum {
			return nil, ErrBlockNotProcessed
		}
	}

	var (
		query string
		args  []any
	)

	if blockNum != nil {
		query = `SELECT * FROM l1info_leaf WHERE block_num <= $1 ORDER BY block_num DESC, block_pos DESC LIMIT 1;`
		args = append(args, *blockNum)
	} else {
		query = `SELECT * FROM l1info_leaf ORDER BY block_num DESC, block_pos DESC LIMIT 1;`
	}

	info := &L1InfoTreeLeaf{}
	err = meddler.QueryRow(tx, info, query, args...)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, db.ErrNotFound
		}
		return nil, err
	}
	return info, nil
}

// GetLatestL1InfoGER returns the most recent Global Exit Root (GER) from the L1 Info tree leaves
func (p *processor) GetLatestL1InfoGER(ctx context.Context) (common.Hash, error) {
	query := `SELECT global_exit_root FROM l1info_leaf ORDER BY block_num DESC, block_pos DESC LIMIT 1;`

	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return common.Hash{}, err
	}
	// ensure tx rolled back (no commit since read-only)
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			p.log.Warnf("error rolling back tx: %v", err)
		}
	}()

	var gerHex string
	row := tx.QueryRowContext(ctx, query)
	if err := row.Scan(&gerHex); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return common.Hash{}, db.ErrNotFound
		}
		return common.Hash{}, fmt.Errorf("querying latest GER: %w", err)
	}

	return common.HexToHash(gerHex), nil
}

// GetInfoByIndex returns the value of a leaf (not the hash) of the L1 info tree
func (p *processor) GetInfoByIndex(ctx context.Context, index uint32) (*L1InfoTreeLeaf, error) {
	return p.getInfoByIndexWithTx(p.db, index)
}

func (p *processor) getInfoByIndexWithTx(tx dbtypes.DBer, index uint32) (*L1InfoTreeLeaf, error) {
	info := &L1InfoTreeLeaf{}
	return info, meddler.QueryRow(
		tx, info,
		`SELECT * FROM l1info_leaf WHERE position = $1;`, index,
	)
}

// GetLastProcessedBlock returns the last processed block.
// Returns (0, false, nil) if no blocks have been processed yet.
func (p *processor) GetLastProcessedBlock(ctx context.Context) (uint64, bool, error) {
	var lastProcessedBlockNum uint64
	row := p.db.QueryRow("SELECT num FROM BLOCK ORDER BY num DESC LIMIT 1;")
	err := row.Scan(&lastProcessedBlockNum)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return lastProcessedBlockNum, true, nil
}

// GetLastProcessedBlockHeader returns the last processed block header
// this function is used by multidownloader
func (p *processor) GetLastProcessedBlockHeader(ctx context.Context) (*aggkittypes.BlockHeader, error) {
	var lastProcessedBlockNum uint64
	var hash *string
	row := p.db.QueryRow("SELECT num, hash FROM BLOCK ORDER BY num DESC LIMIT 1;")
	err := row.Scan(&lastProcessedBlockNum, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var blockHash common.Hash
	if hash == nil {
		blockHash = common.Hash{} // zero hash if no hash is available
	} else {
		blockHash = common.HexToHash(*hash)
	}
	hdr := aggkittypes.NewBlockHeader(lastProcessedBlockNum, blockHash, 0, nil)
	return hdr, err
}

func (p *processor) getLastProcessedBlockWithTx(tx dbtypes.Querier) (uint64, error) {
	var lastProcessedBlockNum uint64

	row := tx.QueryRow("SELECT num FROM BLOCK ORDER BY num DESC LIMIT 1;")
	err := row.Scan(&lastProcessedBlockNum)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return lastProcessedBlockNum, err
}

// GetProcessedBlockUntil returns the last processed block until the given blockNum
// Returns the given blockNum if it has been processed, or the last processed block before it
func (p *processor) GetProcessedBlockUntil(ctx context.Context, blockNum uint64) (uint64, common.Hash, error) {
	var (
		processedBlockNum  uint64
		processedBlockHash *string
	)

	row := p.db.QueryRow("SELECT num, hash FROM block WHERE num <= $1 ORDER BY num DESC LIMIT 1;", blockNum)
	if err := row.Scan(&processedBlockNum, &processedBlockHash); err != nil {
		return 0, common.Hash{}, err
	}

	hash := common.Hash{}
	if processedBlockHash != nil {
		hash = common.HexToHash(*processedBlockHash)
	}

	return processedBlockNum, hash, nil
}

// Reorg triggers a purge and reset process on the processor to leaf it on a state
// as if the last block processed was firstReorgedBlock-1.
//
// Escalation: if this call is recovering from a halt (the processor is currently halted) and the
// previous Reorg-based recovery attempt was also triggered by a halt at the very same block, then
// the previous purge made no progress (the processor halted again at the exact same place). This
// means the actual divergence lies in already-committed data, at or before that block, and
// firstReorgedBlock (typically the start of the in-memory batch that just failed) can never reach
// it. In that case the purge is deepened to the last verified checkpoint block (the most recent
// block whose UpdateL1InfoTreeV2 sanity check passed), or to the syncer's configured initial block
// if no checkpoint has ever been recorded. This converts a permanent stuck-halt loop into an
// automatic (if occasionally expensive) self-heal. See haltAtBlock/lastReorgRecoveryBlock.
func (p *processor) Reorg(ctx context.Context, firstReorgedBlock uint64) error {
	p.mu.Lock()
	wasHalted := p.halted
	haltedBlock := p.haltedBlock
	lastRecoveryBlock := p.lastReorgRecoveryBlock
	p.mu.Unlock()

	escalate := wasHalted && haltedBlock != nil && lastRecoveryBlock != nil && *lastRecoveryBlock == *haltedBlock

	tx, err := db.NewTx(ctx, p.db)
	if err != nil {
		return err
	}

	shouldRollback := true
	defer func() {
		if shouldRollback {
			if errRllbck := tx.Rollback(); errRllbck != nil {
				p.log.Errorf("error while rolling back tx %v", errRllbck)
			}
		}
	}()

	targetBlock := firstReorgedBlock
	if escalate {
		targetBlock, err = p.escalatedReorgTarget(tx, firstReorgedBlock, *haltedBlock)
		if err != nil {
			return fmt.Errorf("computing escalated reorg target: %w", err)
		}
	}

	p.log.Infof("reorging to block %d", targetBlock)

	res, err := tx.Exec(`DELETE FROM block WHERE num >= $1;`, targetBlock)
	if err != nil {
		return err
	}

	if err = p.l1InfoTree.Reorg(tx, targetBlock); err != nil {
		return err
	}

	if err = p.rollupExitTree.Reorg(tx, targetBlock); err != nil {
		return err
	}

	// The checkpoint vouches for the data at (and before) its own block: if that block is being
	// purged, the checkpoint no longer holds and must be cleared, so a future escalation falls
	// back to an earlier (or, absent any other checkpoint, the initial) block instead of trusting
	// a checkpoint whose block no longer exists.
	if err := clearCheckpointBlockAtOrAfterWithTx(tx, targetBlock); err != nil {
		return fmt.Errorf("clearing stale checkpoint: %w", err)
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	p.log.Infof("reorged to block %d, %d rows affected", targetBlock, rowsAffected)

	shouldRollback = false

	if wasHalted && haltedBlock != nil {
		p.mu.Lock()
		p.lastReorgRecoveryBlock = haltedBlock
		p.mu.Unlock()
	}

	// Unhalt unconditionally: a successfully committed purge leaves the DB at a valid
	// consolidation point even when it deleted nothing (rowsAffected == 0), because a halt
	// can be caused by a batch whose tx was rolled back and never persisted (e.g. data built
	// from an undetected L1 tip reorg).
	p.unhalt()
	return nil
}

// escalatedReorgTarget computes the deepened purge target for an escalated Reorg: the last
// verified checkpoint block, or the syncer's initial block if no checkpoint has ever been
// recorded. It never purges shallower than firstReorgedBlock (see Reorg).
func (p *processor) escalatedReorgTarget(tx dbtypes.Querier, firstReorgedBlock, haltedBlock uint64) (uint64, error) {
	checkpointBlock, hasCheckpoint, err := getCheckpointBlockWithTx(tx)
	if err != nil {
		return 0, fmt.Errorf("reading last verified checkpoint: %w", err)
	}

	if hasCheckpoint {
		target := min(firstReorgedBlock, checkpointBlock)
		p.log.Warnf("escalating reorg recovery: processor halted again at block %d after a previous "+
			"recovery attempt made no progress; deepening purge from %d to the last verified checkpoint "+
			"block %d (the most recent block known to be consistent with L1)",
			haltedBlock, firstReorgedBlock, target)
		return target, nil
	}

	target := min(firstReorgedBlock, p.initialBlock)
	p.log.Warnf("escalating reorg recovery: processor halted again at block %d after a previous "+
		"recovery attempt made no progress, and no verified checkpoint has ever been recorded; "+
		"deepening purge from %d to the syncer's initial block %d — this forces a full resync of l1infotreesync",
		haltedBlock, firstReorgedBlock, target)
	return target, nil
}
func (p *processor) ProcessBlocks(ctx context.Context, blocks *mdrsynctypes.DownloadResult) error {
	if blocks == nil || len(blocks.Data) == 0 {
		return nil
	}
	if p.isHalted() {
		p.logHaltGuardHit()
		return sync.ErrInconsistentState
	}
	return p.processBlocksSameTx(ctx, blocks)
}

// processBlocksSameTx processes the blocks in the same transaction, so if any block fails to
// be processed, all the blocks will be rolled back. This is important to keep the integrity of the data,
// specially for the L1 Info tree that relies on the correct order of the leaves
// Note: Maybe could be problems if it rollback with memory data?
func (p *processor) processBlocksSameTx(ctx context.Context, blocks *mdrsynctypes.DownloadResult) error {
	tx, err := db.NewTx(ctx, p.db)
	if err != nil {
		return err
	}
	shouldRollback := true
	defer func() {
		if shouldRollback {
			p.log.Debugf("rolling back block processing for blocks")
			if errRllbck := tx.Rollback(); errRllbck != nil {
				p.log.Errorf("error while rolling back tx %v", errRllbck)
			}
		}
	}()

	for _, block := range blocks.Data {
		syncBlock := sync.Block{
			Num:    block.Num,
			Hash:   block.Hash,
			Events: block.Events,
		}
		if err := p.processBlock(tx, syncBlock); err != nil {
			return fmt.Errorf("processing block %d: %w", block.Num, err)
		}
		logFunc := p.log.Debugf
		if len(block.Events) > 0 {
			logFunc = p.log.Infof
		}
		logFunc("block %d processed with %d events", block.Num, len(block.Events))
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("err: %w", err)
	}
	shouldRollback = false
	log.Infof("processed %d blocks, percent %.2f%% complete. LastBlock: %d",
		len(blocks.Data), blocks.CompletionPercentage, blocks.Data[len(blocks.Data)-1].Num)
	return nil
}

// ProcessBlock process the events of the block to build the rollup exit tree and the l1 info tree
// and updates the last processed block (can be called without events for that purpose)
func (p *processor) ProcessBlock(ctx context.Context, block sync.Block) error {
	if p.isHalted() {
		p.logHaltGuardHit()
		return sync.ErrInconsistentState
	}

	tx, err := db.NewTx(ctx, p.db)
	if err != nil {
		return err
	}

	p.log.Debugf("init block processing for block %d", block.Num)
	shouldRollback := true
	defer func() {
		if shouldRollback {
			p.log.Debugf("rolling back block processing for block %d", block.Num)
			if errRllbck := tx.Rollback(); errRllbck != nil {
				p.log.Errorf("error while rolling back tx %v", errRllbck)
			}
		}
	}()
	err = p.processBlock(tx, block)
	if err != nil {
		return fmt.Errorf("processing block %d: %w", block.Num, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("err: %w", err)
	}
	shouldRollback = false
	logFunc := p.log.Debugf
	if len(block.Events) > 0 {
		logFunc = p.log.Infof
	}
	logFunc("block %d processed with %d events", block.Num, len(block.Events))
	return nil
}

func (p *processor) processBlock(tx dbtypes.Txer, block sync.Block) error {
	if _, err := tx.Exec(`INSERT INTO block (num, hash) VALUES ($1, $2)`, block.Num, block.Hash.String()); err != nil {
		return fmt.Errorf("insert Block. err: %w", err)
	}

	var (
		initialL1InfoIndex uint32
		l1InfoLeavesAdded  uint32
	)
	lastIndex, err := p.getLastIndex(tx)

	switch {
	case errors.Is(err, db.ErrNotFound):
		initialL1InfoIndex = 0
	case err != nil:
		return fmt.Errorf("getLastIndex err: %w", err)
	default:
		initialL1InfoIndex = lastIndex + 1
	}

	for _, e := range block.Events {
		event, ok := e.(Event)
		if !ok {
			return errors.New("failed to convert from sync.Block.Event into Event")
		}
		if event.UpdateL1InfoTree != nil {
			p.log.Debugf("handle UpdateL1InfoTree event. Block: %d, block hash: %s, mainnet exit root: %s, rollup exit root: %s",
				block.Num, block.Hash, event.UpdateL1InfoTree.MainnetExitRoot, event.UpdateL1InfoTree.RollupExitRoot)
			index := initialL1InfoIndex + l1InfoLeavesAdded
			info := &L1InfoTreeLeaf{
				BlockNumber:       block.Num,
				BlockPosition:     event.UpdateL1InfoTree.BlockPosition,
				L1InfoTreeIndex:   index,
				PreviousBlockHash: event.UpdateL1InfoTree.ParentHash,
				Timestamp:         event.UpdateL1InfoTree.Timestamp,
				MainnetExitRoot:   event.UpdateL1InfoTree.MainnetExitRoot,
				RollupExitRoot:    event.UpdateL1InfoTree.RollupExitRoot,
			}
			info.GlobalExitRoot = info.GetGlobalExitRoot()
			info.Hash = info.GetHash()
			if err = meddler.Insert(tx, "l1info_leaf", info); err != nil {
				return fmt.Errorf("insert l1info_leaf %s. err: %w", info.String(), err)
			}

			_, err = p.l1InfoTree.PutLeaf(tx, info.BlockNumber, info.BlockPosition, treetypes.Leaf{
				Index: info.L1InfoTreeIndex,
				Hash:  info.Hash,
			})
			if err != nil {
				return fmt.Errorf("AddLeaf(%s). err: %w", info.String(), err)
			}
			p.log.Debugf("inserted L1InfoTreeLeaf %s", info.String())
			l1InfoLeavesAdded++
		}
		if event.UpdateL1InfoTreeV2 != nil {
			p.log.Debugf("handle UpdateL1InfoTreeV2 event. Block: %d, block hash: %s. Event root: %s. Event leaf count: %d.",
				block.Num, block.Hash, event.UpdateL1InfoTreeV2.CurrentL1InfoRoot.String(), event.UpdateL1InfoTreeV2.LeafCount)

			root, err := p.l1InfoTree.GetLastRoot(tx)
			if err != nil {
				return fmt.Errorf("GetLastRoot(). err: %w", err)
			}
			// If the sanity check fails, halt the syncer and rollback. The sanity check could have
			// failed due to a reorg. Hopefully, this is the case, eventually the reorg will get detected,
			// and the syncer will get unhalted. Otherwise, this means that the syncer has an inconsistent state
			// compared to the contracts, and this will need manual intervention.
			if root.Hash != event.UpdateL1InfoTreeV2.CurrentL1InfoRoot || root.Index+1 != event.UpdateL1InfoTreeV2.LeafCount {
				errStr := fmt.Sprintf(
					"failed to check UpdateL1InfoTreeV2. Root: %s vs event: %s. "+
						"Index: %d vs event.LeafCount: %d. Happened on block %d",
					root.Hash, event.UpdateL1InfoTreeV2.CurrentL1InfoRoot.String(),
					root.Index, event.UpdateL1InfoTreeV2.LeafCount,
					block.Num,
				)
				blockNum := block.Num
				p.haltAtBlock(errStr, &blockNum)
				return sync.ErrInconsistentState
			}
			// The sanity check passed: the local l1-info-tree root matches L1 as of this event, so
			// block.Num is a verified checkpoint. Persist it in the same tx as the batch so a later
			// escalated Reorg can safely purge back to (and re-verify) this exact block. See Reorg.
			if err := setCheckpointBlockWithTx(tx, block.Num); err != nil {
				return fmt.Errorf("persisting last verified checkpoint at block %d: %w", block.Num, err)
			}
		}
		if event.VerifyBatches != nil {
			p.log.Debugf("handle VerifyBatches event %s", event.VerifyBatches.String())
			err = p.processVerifyBatches(tx, block.Num, event.VerifyBatches)
			if err != nil {
				err = fmt.Errorf("processVerifyBatches. err: %w", err)
				p.log.Errorf("error processing VerifyBatches: %v", err)
				return err
			}
		}

		if event.InitL1InfoRootMap != nil {
			p.log.Debugf("handle InitL1InfoRootMap event %s", event.InitL1InfoRootMap.String())
			err = processEventInitL1InfoRootMap(tx, block.Num, event.InitL1InfoRootMap)
			if err != nil {
				err = fmt.Errorf("initL1InfoRootMap. Err: %w", err)
				p.log.Errorf("error processing InitL1InfoRootMap: %v", err)
				return err
			}
		}
	}
	return nil
}

func (p *processor) getLastIndex(tx dbtypes.Querier) (uint32, error) {
	var lastProcessedIndex uint32
	row := tx.QueryRow("SELECT position FROM l1info_leaf ORDER BY block_num DESC, block_pos DESC LIMIT 1;")
	err := row.Scan(&lastProcessedIndex)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, db.ErrNotFound
	}
	return lastProcessedIndex, err
}

// getCheckpointBlockWithTx returns the last verified checkpoint block (the most recent block
// whose UpdateL1InfoTreeV2 sanity check passed). found is false if no checkpoint has been
// recorded yet.
func getCheckpointBlockWithTx(tx dbtypes.Querier) (blockNum uint64, found bool, err error) {
	row := tx.QueryRow("SELECT block_num FROM l1info_checkpoint WHERE single_row_id = 1;")
	err = row.Scan(&blockNum)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return blockNum, true, nil
}

// setCheckpointBlockWithTx records blockNum as the last verified checkpoint. It must be called
// within the same tx that commits the batch containing the passing UpdateL1InfoTreeV2 event, so
// the checkpoint is atomic with the data it vouches for.
func setCheckpointBlockWithTx(tx dbtypes.Txer, blockNum uint64) error {
	_, err := tx.Exec(`
		INSERT INTO l1info_checkpoint (single_row_id, block_num) VALUES (1, $1)
		ON CONFLICT(single_row_id) DO UPDATE SET block_num = $1;
	`, blockNum)
	return err
}

// clearCheckpointBlockAtOrAfterWithTx deletes the stored checkpoint if its block is being purged
// by a Reorg down to purgeFromBlock (i.e. the checkpoint's own block is >= purgeFromBlock), since
// the checkpoint no longer vouches for data that no longer exists. It's a no-op if there is no
// checkpoint, or if the checkpoint predates purgeFromBlock.
func clearCheckpointBlockAtOrAfterWithTx(tx dbtypes.Txer, purgeFromBlock uint64) error {
	_, err := tx.Exec(`DELETE FROM l1info_checkpoint WHERE block_num >= $1;`, purgeFromBlock)
	return err
}

func (p *processor) GetFirstL1InfoWithRollupExitRoot(rollupExitRoot common.Hash) (*L1InfoTreeLeaf, error) {
	info := &L1InfoTreeLeaf{}
	err := meddler.QueryRow(p.db, info, `
		SELECT * FROM l1info_leaf
		WHERE rollup_exit_root = $1
		ORDER BY block_num ASC, block_pos ASC
		LIMIT 1;
	`, rollupExitRoot.Hex())
	return info, db.ReturnErrNotFound(err)
}

func (p *processor) GetLastInfo() (*L1InfoTreeLeaf, error) {
	info := &L1InfoTreeLeaf{}
	err := meddler.QueryRow(p.db, info, `
		SELECT * FROM l1info_leaf
		ORDER BY block_num DESC, block_pos DESC
		LIMIT 1;
	`)
	return info, db.ReturnErrNotFound(err)
}

func (p *processor) GetFirstInfo() (*L1InfoTreeLeaf, error) {
	info := &L1InfoTreeLeaf{}
	err := meddler.QueryRow(p.db, info, `
		SELECT * FROM l1info_leaf
		ORDER BY block_num ASC, block_pos ASC
		LIMIT 1;
	`)
	return info, db.ReturnErrNotFound(err)
}

func (p *processor) GetFirstInfoAfterBlock(blockNum uint64) (*L1InfoTreeLeaf, error) {
	info := &L1InfoTreeLeaf{}
	err := meddler.QueryRow(p.db, info, `
		SELECT * FROM l1info_leaf
		WHERE block_num >= $1
		ORDER BY block_num ASC, block_pos ASC
		LIMIT 1;
	`, blockNum)
	return info, db.ReturnErrNotFound(err)
}

func (p *processor) GetInfoByGlobalExitRoot(ger common.Hash) (*L1InfoTreeLeaf, error) {
	info := &L1InfoTreeLeaf{}
	err := meddler.QueryRow(p.db, info, `
		SELECT * FROM l1info_leaf
		WHERE global_exit_root = $1
		LIMIT 1;
	`, ger.String())
	return info, db.ReturnErrNotFound(err)
}

func (p *processor) GetInfoByRoot(root common.Hash) (*L1InfoTreeLeaf, error) {
	treeRoot, err := p.l1InfoTree.GetRootByHash(context.Background(), root)
	if err != nil {
		return nil, err
	}
	return p.GetInfoByIndex(context.Background(), treeRoot.Index)
}

func (p *processor) getDBQuerier(tx dbtypes.Txer) dbtypes.Querier {
	if tx != nil {
		return tx
	}
	return p.db
}

// isHalted checks if the processor is in a halted state
func (p *processor) isHalted() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.halted
}

// halt sets the processor to a halted state with a reason
func (p *processor) halt(reason string) {
	p.haltAtBlock(reason, nil)
}

// haltAtBlock is like halt, but additionally records the block number that caused the halt, so
// Reorg can detect that a recovery attempt is being retried at the same block with no progress
// and escalate accordingly (see Reorg).
func (p *processor) haltAtBlock(reason string, blockNum *uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.halted {
		return
	}

	p.halted = true
	p.haltedReason = reason
	p.haltedBlock = blockNum
	p.haltGuardHits = 0
	p.log.Errorf("processor is halted, due to the following reason: %s", reason)
}

// logHaltGuardHit logs (at a throttled rate) that a call was rejected because the processor is
// halted. Production incidents have shown this guard can be hit tens of thousands of times while
// a halt is being (unsuccessfully) recovered from, so logging it at error level unconditionally
// floods the logs; see aggkitcommon.ShouldLogRetryAtError.
func (p *processor) logHaltGuardHit() {
	p.mu.Lock()
	p.haltGuardHits++
	hits := p.haltGuardHits
	reason := p.haltedReason
	p.mu.Unlock()

	if aggkitcommon.ShouldLogRetryAtError(hits) {
		p.log.Errorf("processor is halted due to: %s (rejected call #%d while halted)", reason, hits)
	} else {
		p.log.Debugf("processor is halted due to: %s (rejected call #%d while halted)", reason, hits)
	}
}

// unhalt sets the processor to an unhalted state
// It should be called when the processor is ready to process blocks again
func (p *processor) unhalt() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.halted {
		return
	}

	p.halted = false
	p.haltedReason = ""
	p.haltedBlock = nil
	p.haltGuardHits = 0
	p.log.Info("processor is unhalted")
}
