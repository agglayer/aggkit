package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/agglayer/aggkit/claimsync/storage/migrations"
	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/db/compatibility"
	dbtypes "github.com/agglayer/aggkit/db/types"
	aggsync "github.com/agglayer/aggkit/sync"
	"github.com/ethereum/go-ethereum/common"
	"github.com/russross/meddler"
)

var _ claimsynctypes.ClaimStorager = (*claimStorage)(nil)

// blockRecord is the meddler-tagged struct for the block table.
type blockRecord struct {
	Num  uint64 `meddler:"num"`
	Hash string `meddler:"hash"`
}

const (
	// claimColumnsSQL is the list of all claim columns
	claimColumnsSQL = `block_num,
		block_pos,
		tx_hash,
		global_index,
		origin_network,
		origin_address,
		destination_address,
		amount,
		proof_local_exit_root,
		proof_rollup_exit_root,
		mainnet_exit_root,
		rollup_exit_root,
		global_exit_root,
		destination_network,
		metadata,
		is_message,
		block_timestamp,
		type`

	// compactedClaimsSelectSQL is the SELECT clause for compacted claims
	// It combines metadata from the oldest claim with proofs and exit roots from the newest claim
	compactedClaimsSelectSQL = `
		o.block_num,
		o.block_pos,
		o.tx_hash,
		o.global_index,
		o.origin_network,
		o.origin_address,
		o.destination_address,
		o.amount,
		n.proof_local_exit_root,
		n.proof_rollup_exit_root,
		n.mainnet_exit_root,
		n.rollup_exit_root,
		n.global_exit_root,
		o.destination_network,
		o.metadata,
		o.is_message,
		o.block_timestamp,
		o.type`

	// claimsByGERSQL is the query used by GetClaimsByGER.
	claimsByGERSQL = "SELECT " + claimColumnsSQL +
		" FROM claim WHERE global_exit_root = $1 AND type = $2" +
		" ORDER BY block_num ASC, block_pos ASC"
)

type claimStorage struct {
	database       *sql.DB
	compatStore    compatibility.CompatibilityDataStorager[aggsync.RuntimeData]
	log            aggkitcommon.Logger
	dbQueryTimeout time.Duration
}

// NewStandalone opens (or creates) the SQLite database at dbPath, runs all pending migrations,
// and returns a ready-to-use Storage along with the underlying *sql.DB
// (needed by the processor for transaction management).
func NewStandalone(logger aggkitcommon.Logger, dbPath string, ownerName string,
	dbQueryTimeout time.Duration) (claimsynctypes.ClaimStorager, error) {
	database, err := db.NewSQLiteDB(dbPath)
	if err != nil {
		return nil, fmt.Errorf("claimsync storage: failed to open SQLite DB at %s: %w", dbPath, err)
	}

	if err := migrations.RunMigrations(logger, database); err != nil {
		database.Close() //nolint:errcheck
		return nil, fmt.Errorf("claimsync storage: failed to run migrations: %w", err)
	}

	return &claimStorage{
		database:       database,
		compatStore:    compatibility.NewKeyValueToCompatibilityStorage[aggsync.RuntimeData](db.NewKeyValueStorage(database), ownerName),
		log:            logger,
		dbQueryTimeout: dbQueryTimeout,
	}, nil
}

// New creates a Storage using the provided sql.DB, so it can share
func New(logger aggkitcommon.Logger, database *sql.DB, ownerName string, dbQueryTimeout time.Duration) (claimsynctypes.ClaimStorager, error) {
	return &claimStorage{
		database:       database,
		compatStore:    compatibility.NewKeyValueToCompatibilityStorage[aggsync.RuntimeData](db.NewKeyValueStorage(database), ownerName),
		log:            logger,
		dbQueryTimeout: dbQueryTimeout,
	}, nil
}

// NewTx implements claimsynctypes.ClaimStorager.
func (s *claimStorage) NewTx(ctx context.Context) (dbtypes.Txer, error) {
	return db.NewTx(ctx, s.database)
}

// GetCompatibilityData implements claimsynctypes.ClaimStorager.
func (s *claimStorage) GetCompatibilityData(ctx context.Context, tx dbtypes.Querier) (bool, aggsync.RuntimeData, error) {
	return s.compatStore.GetCompatibilityData(ctx, tx)
}

// SetCompatibilityData implements claimsynctypes.ClaimStorager.
func (s *claimStorage) SetCompatibilityData(ctx context.Context, tx dbtypes.Querier, data aggsync.RuntimeData) error {
	return s.compatStore.SetCompatibilityData(ctx, tx, data)
}

// getQuerier returns tx if non-nil, otherwise falls back to the default DB connection.
func (s *claimStorage) getQuerier(tx dbtypes.Querier) dbtypes.Querier {
	if tx != nil {
		return tx
	}
	return s.database
}

// InsertBlock inserts a block row using meddler.
func (s *claimStorage) InsertBlock(_ context.Context, tx dbtypes.Querier, blockNum uint64, blockHash common.Hash) error {
	if err := meddler.Insert(s.getQuerier(tx), "block", &blockRecord{Num: blockNum, Hash: blockHash.Hex()}); err != nil {
		return fmt.Errorf("InsertBlock %d: %w", blockNum, err)
	}
	return nil
}

// InsertClaim persists a claim. The referenced block must already exist.
func (s *claimStorage) InsertClaim(_ context.Context, tx dbtypes.Querier, claim claimsynctypes.Claim) error {
	if err := meddler.Insert(s.getQuerier(tx), "claim", &claim); err != nil {
		return fmt.Errorf("InsertClaim (block %d, pos %d): %w", claim.BlockNum, claim.BlockPos, err)
	}
	return nil
}

// InsertUnsetClaim persists an unset claim. The referenced block must already exist.
func (s *claimStorage) InsertUnsetClaim(_ context.Context, tx dbtypes.Querier, u claimsynctypes.UnsetClaim) error {
	if err := meddler.Insert(s.getQuerier(tx), "unset_claim", &u); err != nil {
		return fmt.Errorf("InsertUnsetClaim (block %d, pos %d): %w", u.BlockNum, u.BlockPos, err)
	}
	return nil
}

// InsertSetClaim persists a set claim. The referenced block must already exist.
func (s *claimStorage) InsertSetClaim(_ context.Context, tx dbtypes.Querier, sc claimsynctypes.SetClaim) error {
	if err := meddler.Insert(s.getQuerier(tx), "set_claim", &sc); err != nil {
		return fmt.Errorf("InsertSetClaim (block %d, pos %d): %w", sc.BlockNum, sc.BlockPos, err)
	}
	return nil
}

// GetClaims returns claims in [fromBlock, toBlock] using compaction logic:
// claims with an unset_claim are returned uncompacted; others are compacted
// (oldest metadata + newest proofs per global_index).
func (s *claimStorage) GetClaims(ctx context.Context, tx dbtypes.Querier, fromBlock, toBlock uint64) ([]claimsynctypes.Claim, error) {
	query := fmt.Sprintf(`
	WITH all_claims_ranked AS (
		SELECT
			*,
			ROW_NUMBER() OVER (PARTITION BY global_index ORDER BY block_num ASC, block_pos ASC) AS rn_oldest_global,
			ROW_NUMBER() OVER (PARTITION BY global_index ORDER BY block_num DESC, block_pos DESC) AS rn_newest_global
		FROM claim
	),
	claims_in_range AS (
		SELECT * FROM all_claims_ranked WHERE block_num >= $1 AND block_num <= $2
	),
	claims_with_unset AS (
		SELECT c.%s
		FROM claims_in_range c
		WHERE EXISTS (SELECT 1 FROM unset_claim uc WHERE uc.global_index = c.global_index)
	),
	compactable_claims AS (
		SELECT %s
		FROM claims_in_range o
		JOIN claims_in_range n ON o.global_index = n.global_index AND n.rn_newest_global = 1
		WHERE o.rn_oldest_global = 1
		AND NOT EXISTS (SELECT 1 FROM unset_claim uc WHERE uc.global_index = o.global_index)
	)
	SELECT * FROM claims_with_unset
	UNION ALL
	SELECT * FROM compactable_claims
	ORDER BY block_num ASC, block_pos ASC;
	`, claimColumnsSQL, compactedClaimsSelectSQL)

	// Create a context with database timeout
	dbCtx, cancel := s.withDatabaseTimeout(ctx)
	defer cancel()

	rows, err := s.getQuerier(tx).QueryContext(dbCtx, query, fromBlock, toBlock)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return []claimsynctypes.Claim{}, nil
		}
		return nil, fmt.Errorf("GetClaims [%d, %d]: %w", fromBlock, toBlock, err)
	}
	defer rows.Close()

	return scanClaims(rows)
}

// GetClaimsByGlobalIndex returns claims for the given global index using compaction logic.
func (s *claimStorage) GetClaimsByGlobalIndex(ctx context.Context, tx dbtypes.Querier, globalIndex *big.Int) ([]claimsynctypes.Claim, error) {
	if globalIndex == nil {
		return nil, errors.New("GetClaimsByGlobalIndex: globalIndex cannot be nil")
	}

	query := fmt.Sprintf(`
	WITH all_claims_for_index AS (
		SELECT
			*,
			ROW_NUMBER() OVER (ORDER BY block_num ASC, block_pos ASC) AS rn_oldest,
			ROW_NUMBER() OVER (ORDER BY block_num DESC, block_pos DESC) AS rn_newest
		FROM claim
		WHERE global_index = $1
	),
	claims_with_unset AS (
		SELECT c.%s
		FROM all_claims_for_index c
		WHERE EXISTS (SELECT 1 FROM unset_claim uc WHERE uc.global_index = $1)
	),
	compactable_claims AS (
		SELECT %s
		FROM all_claims_for_index o
		JOIN all_claims_for_index n ON n.rn_newest = 1
		WHERE o.rn_oldest = 1
		AND NOT EXISTS (SELECT 1 FROM unset_claim uc WHERE uc.global_index = $1)
	)
	SELECT * FROM claims_with_unset
	UNION ALL
	SELECT * FROM compactable_claims
	ORDER BY block_num ASC, block_pos ASC;
	`, claimColumnsSQL, compactedClaimsSelectSQL)

	dbCtx, cancel := s.withDatabaseTimeout(ctx)
	defer cancel()

	rows, err := s.getQuerier(tx).QueryContext(dbCtx, query, globalIndex.String())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return []claimsynctypes.Claim{}, nil
		}
		return nil, fmt.Errorf("GetClaimsByGlobalIndex %s: %w", globalIndex.String(), err)
	}
	defer rows.Close()

	return scanClaims(rows)
}

// GetFirstProcessedBlock returns the lowest block number stored.
// Returns (0, false, nil) if there are no blocks on DB.
func (s *claimStorage) GetFirstProcessedBlock(ctx context.Context, tx dbtypes.Querier) (uint64, bool, error) {
	dbCtx, cancel := s.withDatabaseTimeout(ctx)
	defer cancel()

	var num uint64
	err := s.getQuerier(tx).QueryRowContext(dbCtx, `SELECT num FROM block ORDER BY num ASC LIMIT 1`).Scan(&num)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}

	return num, err == nil, err
}

// GetLastProcessedBlock returns the highest block number stored.
// Returns (0, false, nil) if there are no blocks on DB.
func (s *claimStorage) GetLastProcessedBlock(ctx context.Context, tx dbtypes.Querier) (uint64, bool, error) {
	dbCtx, cancel := s.withDatabaseTimeout(ctx)
	defer cancel()

	var num uint64
	err := s.getQuerier(tx).QueryRowContext(dbCtx, `SELECT num FROM block ORDER BY num DESC LIMIT 1`).Scan(&num)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}

	return num, err == nil, err
}

// GetBoundaryBlockForClaimType returns the max block_num for claims of the given type.
// Returns db.ErrNotFound if no claims of that type exist.
func (s *claimStorage) GetBoundaryBlockForClaimType(ctx context.Context, tx dbtypes.Querier, claimType claimsynctypes.ClaimType) (uint64, error) {
	dbCtx, cancel := s.withDatabaseTimeout(ctx)
	defer cancel()

	var blockNum *uint64
	if err := s.getQuerier(tx).QueryRowContext(dbCtx, `SELECT MAX(block_num) FROM claim WHERE type = $1`, claimType).
		Scan(&blockNum); err != nil {
		return 0, err
	}
	if blockNum == nil {
		return 0, db.ErrNotFound
	}
	return *blockNum, nil
}

// GetClaimsByGER returns all DetailedClaimEvent claims with the given global exit root,
// ordered by block_num/block_pos ascending. If the claim table does not exist (e.g. L1
// processor), returns nil, nil gracefully.
func (p *claimStorage) GetClaimsByGER(ctx context.Context, globalExitRoot common.Hash) ([]*claimsynctypes.Claim, error) {
	dbCtx, cancel := p.withDatabaseTimeout(ctx)
	defer cancel()

	rows, err := p.database.QueryContext(dbCtx, claimsByGERSQL, globalExitRoot.Hex(), claimsynctypes.DetailedClaimEvent)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, fmt.Errorf("GetClaimsByGER: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			p.log.Errorf("error closing rows: %v", cerr)
		}
	}()

	claims := []*claimsynctypes.Claim{}
	if err = meddler.ScanAll(rows, &claims); err != nil {
		return nil, fmt.Errorf("GetClaimsByGER: scan: %w", err)
	}
	return claims, nil
}

// DeleteBlocksFrom deletes all blocks with num >= firstBlock and returns the count deleted.
// Cascade constraints automatically remove associated claims, unset_claims and set_claims.
func (s *claimStorage) DeleteBlocksFrom(ctx context.Context, tx dbtypes.Querier, firstBlock uint64) (int64, error) {
	dbCtx, cancel := s.withDatabaseTimeout(ctx)
	defer cancel()

	res, err := s.getQuerier(tx).ExecContext(dbCtx, `DELETE FROM block WHERE num >= $1`, firstBlock)
	if err != nil {
		return 0, fmt.Errorf("DeleteBlocksFrom %d: %w", firstBlock, err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func scanClaims(rows *sql.Rows) ([]claimsynctypes.Claim, error) {
	var ptrs []*claimsynctypes.Claim
	if err := meddler.ScanAll(rows, &ptrs); err != nil {
		return nil, fmt.Errorf("scanClaims: %w", err)
	}

	iface := db.SlicePtrsToSlice(ptrs)
	claims, ok := iface.([]claimsynctypes.Claim)
	if !ok {
		return nil, errors.New("scanClaims: type assertion from []*Claim to []Claim failed")
	}

	return claims, nil
}

// buildNetworkIDsFilter builds SQL filter for the given network IDs
func buildNetworkIDsFilter(networkIDs []uint32, networkIDColumn string) string {
	placeholders := make([]string, len(networkIDs))
	for i, id := range networkIDs {
		placeholders[i] = fmt.Sprintf("%d", id)
	}
	return fmt.Sprintf("%s IN (%s)", networkIDColumn, strings.Join(placeholders, ", "))
}

func (p *claimStorage) withDatabaseTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, p.dbQueryTimeout)
}
