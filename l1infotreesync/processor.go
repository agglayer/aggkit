package l1infotreesync

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	mutex "sync"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/db/compatibility"
	dbtypes "github.com/agglayer/aggkit/db/types"
	"github.com/agglayer/aggkit/l1infotreesync/migrations"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	"github.com/agglayer/aggkit/tree"
	treeTypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/iden3/go-iden3-crypto/keccak256"
	"github.com/russross/meddler"
	"golang.org/x/crypto/sha3"
)

var (
	ErrBlockNotProcessed = errors.New("given block(s) have not been processed yet")
	ErrNoBlock0          = errors.New("blockNum must be greater than 0")
)

type processor struct {
	db             *sql.DB
	l1InfoTree     *tree.AppendOnlyTree
	rollupExitTree *tree.UpdatableTree
	mu             mutex.RWMutex
	halted         bool
	haltedReason   string
	log            *log.Logger
	compatibility.CompatibilityDataStorager[sync.RuntimeData]
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
	var res [treeTypes.DefaultHeight]byte
	t := make([]byte, 8) //nolint:mnd
	binary.BigEndian.PutUint64(t, l.Timestamp)
	copy(res[:], keccak256.Hash(l.GetGlobalExitRoot().Bytes(), l.PreviousBlockHash.Bytes(), t))
	return res
}

// GlobalExitRoot returns the GER
func (l *L1InfoTreeLeaf) GetGlobalExitRoot() common.Hash {
	var gerBytes [treeTypes.DefaultHeight]byte
	hasher := sha3.NewLegacyKeccak256()
	hasher.Write(l.MainnetExitRoot[:])
	hasher.Write(l.RollupExitRoot[:])
	copy(gerBytes[:], hasher.Sum(nil))

	return gerBytes
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
		CompatibilityDataStorager: compatibility.NewKeyValueToCompatibilityStorage[sync.RuntimeData](
			db.NewKeyValueStorage(database),
			aggkitcommon.L1INFOTREESYNC,
		),
	}, nil
}

// GetL1InfoTreeMerkleProof creates a merkle proof for the L1 Info tree
func (p *processor) GetL1InfoTreeMerkleProof(
	ctx context.Context, index uint32,
) (treeTypes.Proof, treeTypes.Root, error) {
	root, err := p.l1InfoTree.GetRootByIndex(ctx, index)
	if err != nil {
		return treeTypes.Proof{}, treeTypes.Root{}, err
	}

	proof, err := p.l1InfoTree.GetProof(ctx, root.Index, root.Hash)
	return proof, root, err
}

// GetLatestInfoUntilBlock returns the most recent L1InfoTreeLeaf that occurred before or at blockNum.
// If the blockNum has not been processed yet the error ErrBlockNotProcessed will be returned
func (p *processor) GetLatestInfoUntilBlock(ctx context.Context, blockNum uint64) (*L1InfoTreeLeaf, error) {
	if blockNum == 0 {
		return nil, ErrNoBlock0
	}
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := tx.Rollback(); err != nil {
			p.log.Warnf("error rolling back tx: %v", err)
		}
	}()

	lpb, err := p.getLastProcessedBlockWithTx(tx)
	if err != nil {
		return nil, err
	}
	if lpb < blockNum {
		return nil, ErrBlockNotProcessed
	}

	info := &L1InfoTreeLeaf{}
	err = meddler.QueryRow(
		tx, info,
		`SELECT * FROM l1info_leaf WHERE block_num <= $1 ORDER BY block_num DESC, block_pos DESC LIMIT 1;`,
		blockNum,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, db.ErrNotFound
		}
		return nil, err
	}
	return info, nil
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

// GetLastProcessedBlock returns the last processed block
func (p *processor) GetLastProcessedBlock(ctx context.Context) (uint64, error) {
	return p.getLastProcessedBlockWithTx(p.db)
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
// as if the last block processed was firstReorgedBlock-1
func (p *processor) Reorg(ctx context.Context, firstReorgedBlock uint64) error {
	p.log.Infof("reorging to block %d", firstReorgedBlock)

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

	res, err := tx.Exec(`DELETE FROM block WHERE num >= $1;`, firstReorgedBlock)
	if err != nil {
		return err
	}

	if err = p.l1InfoTree.Reorg(tx, firstReorgedBlock); err != nil {
		return err
	}

	if err = p.rollupExitTree.Reorg(tx, firstReorgedBlock); err != nil {
		return err
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	p.log.Infof("reorged to block %d, %d rows affected", firstReorgedBlock, rowsAffected)

	shouldRollback = false

	sync.UnhaltIfAffectedRows(&p.halted, &p.haltedReason, &p.mu, rowsAffected)
	return nil
}

// ProcessBlock process the events of the block to build the rollup exit tree and the l1 info tree
// and updates the last processed block (can be called without events for that purpose)
func (p *processor) ProcessBlock(ctx context.Context, block sync.Block) error {
	if p.isHalted() {
		p.log.Errorf("processor is halted due to: %s", p.haltedReason)
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

			err = p.l1InfoTree.AddLeaf(tx, info.BlockNumber, info.BlockPosition, treeTypes.Leaf{
				Index: info.L1InfoTreeIndex,
				Hash:  info.Hash,
			})
			if err != nil {
				return fmt.Errorf("AddLeaf(%s). err: %w", info.String(), err)
			}
			p.log.Infof("inserted L1InfoTreeLeaf %s", info.String())
			l1InfoLeavesAdded++
		}
		if event.UpdateL1InfoTreeV2 != nil {
			p.log.Infof("handle UpdateL1InfoTreeV2 event. Block: %d, block hash: %s. Event root: %s. Event leaf count: %d.",
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
				p.log.Error(errStr)
				p.mu.Lock()
				p.haltedReason = errStr
				p.halted = true
				p.mu.Unlock()
				return sync.ErrInconsistentState
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

func (p *processor) getLastIndex(tx dbtypes.Querier) (uint32, error) {
	var lastProcessedIndex uint32
	row := tx.QueryRow("SELECT position FROM l1info_leaf ORDER BY block_num DESC, block_pos DESC LIMIT 1;")
	err := row.Scan(&lastProcessedIndex)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, db.ErrNotFound
	}
	return lastProcessedIndex, err
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

func (p *processor) getDBQuerier(tx dbtypes.Txer) dbtypes.Querier {
	if tx != nil {
		return tx
	}
	return p.db
}

func (p *processor) isHalted() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.halted
}
