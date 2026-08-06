package bridgesync

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	mutex "sync"
	"time"

	bridgetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync/migrations"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/db/compatibility"
	dbtypes "github.com/agglayer/aggkit/db/types"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	"github.com/agglayer/aggkit/tree"
	"github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/russross/meddler"
	"go.uber.org/zap/zapcore"
)

const (
	globalIndexPartSize = 4
	mainnetFlagPosition = 64
	rollupIndexPosition = 32

	// bridgeTableName is the name of the table that stores bridge events
	bridgeTableName = "bridge"

	// tokenMappingTableName is the name of the table that stores token mapping events
	tokenMappingTableName = "token_mapping"

	// legacyTokenMigrationTableName is the name of the table that stores legacy token migration events
	legacyTokenMigrationTableName = "legacy_token_migration"

	// backwardLETTableName is the name of the table that stores backward local exit tree events
	backwardLETTableName = "backward_let"

	// forwardLETTableName is the name of the table that stores forward local exit tree events
	forwardLETTableName = "forward_let"

	// nilStr holds nil string
	nilStr = "nil"
)

const (
	// orderByBlockDesc is the default order by clause for block-based queries
	orderByBlockDesc = "block_num DESC, block_pos DESC"

	// bridgeByDepositCountSQL is the query used by GetBridgeByDepositCount for the main bridge table.
	// deposit_count is a unique monotonic counter per bridge event in the contract, so no
	// additional origin_network filter is needed (it would incorrectly exclude L2-native tokens).
	bridgeByDepositCountSQL = "SELECT * FROM " + bridgeTableName +
		" WHERE deposit_count = $1 LIMIT 1"

	// archiveByDepositCountSQL is the query used by GetBridgeByDepositCount for bridge_archive.
	archiveByDepositCountSQL = `SELECT * FROM bridge_archive WHERE deposit_count = $1 LIMIT 1`

	// bridgesByContentWhereNoMeta is the WHERE clause for GetBridgesByContent without metadata.
	bridgesByContentWhereNoMeta = "origin_network = 0 AND leaf_type = $1 AND origin_address = $2" +
		" AND destination_network = $3 AND destination_address = $4 AND amount = $5" +
		" AND (metadata IS NULL OR metadata = x'')"

	// bridgesByContentWhereWithMeta is the WHERE clause for GetBridgesByContent with metadata.
	bridgesByContentWhereWithMeta = "origin_network = 0 AND leaf_type = $1 AND origin_address = $2" +
		" AND destination_network = $3 AND destination_address = $4 AND amount = $5" +
		" AND metadata = $6"

	// Precomputed full SELECT queries for GetBridgesByContent (bridge and bridge_archive tables,
	// with and without metadata filter). Using compile-time constants avoids dynamic SQL construction.
	bridgeByContentNoMetaSQL    = "SELECT * FROM " + bridgeTableName + " WHERE " + bridgesByContentWhereNoMeta
	bridgeByContentWithMetaSQL  = "SELECT * FROM " + bridgeTableName + " WHERE " + bridgesByContentWhereWithMeta
	archiveByContentNoMetaSQL   = "SELECT * FROM bridge_archive WHERE " + bridgesByContentWhereNoMeta
	archiveByContentWithMetaSQL = "SELECT * FROM bridge_archive WHERE " + bridgesByContentWhereWithMeta
)

var (
	// tableNameRegex is the regex pattern to validate table names
	tableNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

	// deleteLegacyTokenSQL is the SQL statement to delete legacy token migration event
	// with specific legacy token address
	deleteLegacyTokenSQL = fmt.Sprintf("DELETE FROM %s WHERE legacy_token_address = $1", legacyTokenMigrationTableName)

	// getBridgesBlockRangeSelectSQL is the SELECT clause for bridges within a block range
	getBridgesBlockRangeSelectSQL = fmt.Sprintf(`
	SELECT * FROM %s
		WHERE block_num >= $1 AND block_num <= $2
		ORDER BY block_num ASC, block_pos ASC;
	`, bridgeTableName)
)

type BridgeSource string

const (
	BridgeSourceBackwardLET BridgeSource = "backward_let"
	BridgeSourceForwardLET  BridgeSource = "forward_let"
)

// Bridge is the representation of a bridge event
type Bridge struct {
	BlockNum           uint64          `meddler:"block_num"`
	BlockPos           uint64          `meddler:"block_pos"`
	FromAddress        *common.Address `meddler:"from_address,address"`
	TxHash             common.Hash     `meddler:"tx_hash,hash"`
	BlockTimestamp     uint64          `meddler:"block_timestamp"`
	LeafType           uint8           `meddler:"leaf_type"`
	OriginNetwork      uint32          `meddler:"origin_network"`
	OriginAddress      common.Address  `meddler:"origin_address"`
	DestinationNetwork uint32          `meddler:"destination_network"`
	DestinationAddress common.Address  `meddler:"destination_address"`
	Amount             *big.Int        `meddler:"amount,bigint"`
	Metadata           []byte          `meddler:"metadata"`
	DepositCount       uint32          `meddler:"deposit_count"`
	TxnSender          common.Address  `meddler:"txn_sender,address"`
	Source             BridgeSource    `meddler:"source"`
	ToAddress          common.Address  `meddler:"to_address,address"`
}

func (b *Bridge) String() string {
	amountStr := nilStr
	if b.Amount != nil {
		amountStr = b.Amount.String()
	}
	fromAddrStr := nilStr
	if b.FromAddress != nil {
		fromAddrStr = b.FromAddress.String()
	}
	return fmt.Sprintf("Bridge{BlockNum: %d, BlockPos: %d, FromAddress: %s, TxHash: %s, "+
		"BlockTimestamp: %d, LeafType: %d, OriginNetwork: %d, OriginAddress: %s, "+
		"DestinationNetwork: %d, DestinationAddress: %s, Amount: %s, Metadata: %x, "+
		"DepositCount: %d, TxnSender: %s, Source: %s, ToAddress: %s}",
		b.BlockNum, b.BlockPos, fromAddrStr, b.TxHash.String(),
		b.BlockTimestamp, b.LeafType, b.OriginNetwork, b.OriginAddress.String(),
		b.DestinationNetwork, b.DestinationAddress.String(), amountStr, b.Metadata,
		b.DepositCount, b.TxnSender.String(), b.Source, b.ToAddress.String())
}

// Hash returns the hash of the bridge event as expected by the exit tree
// Note: can't change the Hash() here after adding BlockTimestamp and TxHash. Might affect previous versions
func (b *Bridge) Hash() common.Hash {
	const (
		uint32ByteSize = 4
		bigIntSize     = 32
	)
	origNet := make([]byte, uint32ByteSize)
	binary.BigEndian.PutUint32(origNet, b.OriginNetwork)
	destNet := make([]byte, uint32ByteSize)
	binary.BigEndian.PutUint32(destNet, b.DestinationNetwork)

	metaHash := crypto.Keccak256(b.Metadata)
	var buf [bigIntSize]byte
	if b.Amount == nil {
		b.Amount = common.Big0
	}

	return crypto.Keccak256Hash(
		[]byte{b.LeafType},
		origNet,
		b.OriginAddress[:],
		destNet,
		b.DestinationAddress[:],
		b.Amount.FillBytes(buf[:]),
		metaHash,
	)
}

// TokenMapping representation of a NewWrappedToken event, that is emitted by the bridge contract
type TokenMapping struct {
	BlockNum            uint64                       `meddler:"block_num"`
	BlockPos            uint64                       `meddler:"block_pos"`
	BlockTimestamp      uint64                       `meddler:"block_timestamp"`
	TxHash              common.Hash                  `meddler:"tx_hash,hash"`
	OriginNetwork       uint32                       `meddler:"origin_network"`
	OriginTokenAddress  common.Address               `meddler:"origin_token_address,address"`
	WrappedTokenAddress common.Address               `meddler:"wrapped_token_address,address"`
	Metadata            []byte                       `meddler:"metadata"`
	IsNotMintable       bool                         `meddler:"is_not_mintable"`
	Type                bridgetypes.TokenMappingType `meddler:"token_type"`
}

func (t *TokenMapping) String() string {
	return fmt.Sprintf("TokenMapping{BlockNum: %d, BlockPos: %d, BlockTimestamp: %d, TxHash: %s, "+
		"OriginNetwork: %d, OriginTokenAddress: %s, WrappedTokenAddress: %s, Metadata: %x, "+
		"IsNotMintable: %t, Type: %s}",
		t.BlockNum, t.BlockPos, t.BlockTimestamp, t.TxHash.String(),
		t.OriginNetwork, t.OriginTokenAddress.String(), t.WrappedTokenAddress.String(), t.Metadata,
		t.IsNotMintable, t.Type.String())
}

// LegacyTokenMigration representation of a MigrateLegacyToken event,
// that is emitted by the sovereign chain bridge contract.
type LegacyTokenMigration struct {
	BlockNum            uint64         `meddler:"block_num"`
	BlockPos            uint64         `meddler:"block_pos"`
	BlockTimestamp      uint64         `meddler:"block_timestamp"`
	TxHash              common.Hash    `meddler:"tx_hash,hash"`
	Sender              common.Address `meddler:"sender,address"`
	LegacyTokenAddress  common.Address `meddler:"legacy_token_address,address"`
	UpdatedTokenAddress common.Address `meddler:"updated_token_address,address"`
	Amount              *big.Int       `meddler:"amount,bigint"`
}

func (l *LegacyTokenMigration) String() string {
	amountStr := nilStr
	if l.Amount != nil {
		amountStr = l.Amount.String()
	}
	return fmt.Sprintf("LegacyTokenMigration{BlockNum: %d, BlockPos: %d, BlockTimestamp: %d, TxHash: %s, "+
		"Sender: %s, LegacyTokenAddress: %s, UpdatedTokenAddress: %s, Amount: %s}",
		l.BlockNum, l.BlockPos, l.BlockTimestamp, l.TxHash.String(),
		l.Sender.String(), l.LegacyTokenAddress.String(), l.UpdatedTokenAddress.String(),
		amountStr)
}

// RemoveLegacyToken representation of a RemoveLegacySovereignTokenAddress event,
// that is emitted by the sovereign chain bridge contract.
type RemoveLegacyToken struct {
	BlockNum           uint64         `meddler:"block_num"`
	BlockPos           uint64         `meddler:"block_pos"`
	BlockTimestamp     uint64         `meddler:"block_timestamp"`
	TxHash             common.Hash    `meddler:"tx_hash,hash"`
	LegacyTokenAddress common.Address `meddler:"legacy_token_address,address"`
}

func (r *RemoveLegacyToken) String() string {
	return fmt.Sprintf("RemoveLegacyToken{BlockNum: %d, BlockPos: %d, BlockTimestamp: %d, TxHash: %s, "+
		"LegacyTokenAddress: %s}",
		r.BlockNum, r.BlockPos, r.BlockTimestamp, r.TxHash.String(),
		r.LegacyTokenAddress.String())
}

// BackwardLET representation of a BackwardLET event,
// that is emitted by the L2 bridge contract when a LET is rolled back.
type BackwardLET struct {
	BlockNum             uint64      `meddler:"block_num"`
	BlockPos             uint64      `meddler:"block_pos"`
	PreviousDepositCount *big.Int    `meddler:"previous_deposit_count,bigint"`
	PreviousRoot         common.Hash `meddler:"previous_root,hash"`
	NewDepositCount      *big.Int    `meddler:"new_deposit_count,bigint"`
	NewRoot              common.Hash `meddler:"new_root,hash"`
}

// String returns a formatted string representation of BackwardLET for debugging and logging.
func (b *BackwardLET) String() string {
	previousDepositCountStr := nilStr
	if b.PreviousDepositCount != nil {
		previousDepositCountStr = b.PreviousDepositCount.String()
	}
	newDepositCountStr := nilStr
	if b.NewDepositCount != nil {
		newDepositCountStr = b.NewDepositCount.String()
	}
	return fmt.Sprintf("BackwardLET{BlockNum: %d, BlockPos: %d, "+
		"PreviousDepositCount: %s, PreviousRoot: %s, NewDepositCount: %s, NewRoot: %s}",
		b.BlockNum, b.BlockPos, previousDepositCountStr, b.PreviousRoot.String(), newDepositCountStr, b.NewRoot.String())
}

// ForwardLET representation of a ForwardLET event,
// that is emitted by the L2 bridge contract when a LET is advanced.
type ForwardLET struct {
	BlockNum             uint64      `meddler:"block_num"`
	BlockPos             uint64      `meddler:"block_pos"`
	BlockTimestamp       uint64      `meddler:"block_timestamp"`
	TxnHash              common.Hash `meddler:"tx_hash,hash"`
	PreviousDepositCount *big.Int    `meddler:"previous_deposit_count,bigint"`
	PreviousRoot         common.Hash `meddler:"previous_root,hash"`
	NewDepositCount      *big.Int    `meddler:"new_deposit_count,bigint"`
	NewRoot              common.Hash `meddler:"new_root,hash"`
	NewLeaves            []byte      `meddler:"new_leaves"`
}

// String returns a formatted string representation of ForwardLET for debugging and logging.
func (f *ForwardLET) String() string {
	prevDepositCountStr := nilStr
	if f.PreviousDepositCount != nil {
		prevDepositCountStr = f.PreviousDepositCount.String()
	}

	newDepositCountStr := nilStr
	if f.NewDepositCount != nil {
		newDepositCountStr = f.NewDepositCount.String()
	}

	return fmt.Sprintf("ForwardLET{BlockNum: %d, BlockPos: %d, "+
		"BlockTimestamp: %d, TxnHash: %s, "+
		"PreviousDepositCount: %s, PreviousRoot: %s, "+
		"NewDepositCount: %s, NewRoot: %s, NewLeaves: %x}",
		f.BlockNum, f.BlockPos,
		f.BlockTimestamp, f.TxnHash.String(),
		prevDepositCountStr, f.PreviousRoot.String(),
		newDepositCountStr, f.NewRoot.String(), f.NewLeaves)
}

// Event combination of bridge, claim, token mapping and legacy token migration events
type Event struct {
	Bridge *Bridge

	TokenMapping         *TokenMapping
	LegacyTokenMigration *LegacyTokenMigration
	RemoveLegacyToken    *RemoveLegacyToken
	BackwardLET          *BackwardLET
	ForwardLET           *ForwardLET
	// Claim                *Claim
	// UnsetClaim           *UnsetClaim
	// SetClaim             *SetClaim

}

func (e Event) String() string {
	parts := []string{}
	if e.Bridge != nil {
		parts = append(parts, e.Bridge.String())
	}
	if e.TokenMapping != nil {
		parts = append(parts, e.TokenMapping.String())
	}
	if e.LegacyTokenMigration != nil {
		parts = append(parts, e.LegacyTokenMigration.String())
	}
	if e.RemoveLegacyToken != nil {
		parts = append(parts, e.RemoveLegacyToken.String())
	}
	if e.BackwardLET != nil {
		parts = append(parts, e.BackwardLET.String())
	}
	if e.ForwardLET != nil {
		parts = append(parts, e.ForwardLET.String())
	}
	return "bridgesync.Event{" + strings.Join(parts, ", ") + "}"
}

// BridgeSyncRuntimeData contains runtime environment data used for database compatibility checks.
// It includes chain ID, contract addresses, and database version information.
type BridgeSyncRuntimeData struct {
	// This fields are coming from legacy sync.RuntimeData
	ChainID   uint64
	Addresses []common.Address
	// DBVersion tracks the database schema version for compatibility validation
	DBVersion *int
	// SyncFromInBridges tracks if FromAddress extraction was enabled for this database
	// By default is true
	SyncFromInBridges *bool
}

func (b BridgeSyncRuntimeData) String() string {
	res := fmt.Sprintf("ChainID: %d, Addresses: ", b.ChainID)
	for _, addr := range b.Addresses {
		res += addr.String() + ", "
	}
	if b.DBVersion != nil {
		res += fmt.Sprintf("DBVersion: %d, ", *b.DBVersion)
	}
	if b.SyncFromInBridges != nil {
		res += fmt.Sprintf("SyncFromInBridges: %t", *b.SyncFromInBridges)
	}
	return res
}

func (b BridgeSyncRuntimeData) IsCompatible(storage BridgeSyncRuntimeData) (*BridgeSyncRuntimeData, error) {
	// First check the basic runtimedata compatibility using the existing logic in sync.RuntimeData
	tmp := sync.RuntimeData{
		ChainID:   b.ChainID,
		Addresses: b.Addresses,
	}
	if _, err := tmp.IsCompatible(sync.RuntimeData{ChainID: storage.ChainID, Addresses: storage.Addresses}); err != nil {
		return nil, err
	}

	// Check database schema version compatibility, this is to introduce
	// changes beyond migration mechanism.
	// You can control that the data in DB is invalid and need to be deleted
	// or, in the future, you can create a way to update it.
	if storage.DBVersion == nil || *storage.DBVersion != *b.DBVersion {
		return nil, fmt.Errorf("database schema version mismatch (current: %v, stored: %v). "+
			"Drop BridgeL1Sync and BridgeL2Sync databases and restart",
			b.DBVersion, storage.DBVersion)
	}
	if b.SyncFromInBridges == nil {
		return nil, errors.New("invalid runtime data: missing SyncFromInBridges field (internal error)")
	}

	if storage.SyncFromInBridges == nil {
		// If storage doesn't have this field, the database was created before this field existed,
		// so we assume 'true' by default (historical behavior).
		if b.SyncFromInBridges != nil && !*b.SyncFromInBridges {
			log.Warnf("Database created without SyncFromInBridges field, assuming true. " +
				"Current config has SyncFromInBridges set to false, new bridges will not have FromAddress.",
			)
		}
		// we update storage with current value
		return &b, nil
	}
	// Validate SyncFromInBridges compatibility

	// false → true: FORBIDDEN (missing FromAddress cannot be recovered)
	if !*storage.SyncFromInBridges && *b.SyncFromInBridges {
		log.Warnf("SyncFromInBridges changed from false to true. " +
			"The missing FromAddress are going to be filled by a background " +
			"process, but it might take a while")
	}
	// true → false: ALLOWED (log warning about inconsistent data)
	if *storage.SyncFromInBridges && !*b.SyncFromInBridges {
		log.Warnf("SyncFromInBridges changed from true to false. " +
			"Existing bridges have FromAddress, new bridges will not.",
		)
	}

	return nil, nil
}

type processor struct {
	syncerID     string
	db           *sql.DB
	exitTree     types.FullTreer
	log          *log.Logger
	mu           mutex.RWMutex
	halted       bool
	haltedReason string
	// haltGuardHits counts consecutive isHalted() short-circuits in ProcessBlock, so the
	// "processor is halted" log can be throttled instead of firing on every call. Reset on
	// unhalt.
	haltGuardHits    int
	dbQueryTimeout   time.Duration
	bridgeSubscriber aggkitcommon.PubSub[uint64]
	initialLER       common.Hash
	compatibility.CompatibilityDataStorager[BridgeSyncRuntimeData]
}

func newSqliteDB(dbPath string) (*sql.DB, error) {
	err := migrations.RunMigrations(dbPath)
	if err != nil {
		return nil, err
	}
	database, err := db.NewSQLiteDB(dbPath)
	if err != nil {
		return nil, err
	}
	return database, nil
}

func newProcessor(
	database *sql.DB,
	syncerID string,
	logger *log.Logger,
	dbQueryTimeout time.Duration,
) (*processor, error) {
	exitTree := tree.NewAppendOnlyTree(database, "")

	return &processor{
		syncerID:         syncerID,
		db:               database,
		exitTree:         exitTree,
		log:              logger,
		dbQueryTimeout:   dbQueryTimeout,
		bridgeSubscriber: aggkitcommon.NewGenericSubscriber[uint64](),
		CompatibilityDataStorager: compatibility.NewKeyValueToCompatibilityStorage[BridgeSyncRuntimeData](
			db.NewKeyValueStorage(database),
			syncerID,
		),
	}, nil
}

func (p *processor) GetBridges(
	ctx context.Context, fromBlock, toBlock uint64,
) ([]Bridge, error) {
	rows, err := p.queryBlockRange(ctx, p.db, fromBlock, toBlock, getBridgesBlockRangeSelectSQL)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			p.log.Debugf("no bridges were found for block range [%d..%d]", fromBlock, toBlock)
			return []Bridge{}, nil
		}
		p.log.Errorf("GetBridges: queryBlockRange failed for block range [%d..%d]: %v", fromBlock, toBlock, err)
		return nil, err
	}

	defer func() {
		if cerr := rows.Close(); cerr != nil {
			p.log.Errorf("error closing rows: %v", cerr)
		}
	}()

	bridgePtrs := []*Bridge{}
	if err = meddler.ScanAll(rows, &bridgePtrs); err != nil {
		p.log.Errorf("GetBridges: meddler.ScanAll failed for block range [%d..%d]: %v", fromBlock, toBlock, err)
		return nil, err
	}
	bridgesIface := db.SlicePtrsToSlice(bridgePtrs)
	bridges, ok := bridgesIface.([]Bridge)
	if !ok {
		p.log.Errorf("GetBridges: failed to convert from []*Bridge to []Bridge for block range [%d..%d]", fromBlock, toBlock)
		return nil, errors.New("failed to convert from []*Bridge to []Bridge")
	}
	return bridges, nil
}

func (p *processor) GetBridgesPaged(
	ctx context.Context, pageNumber, pageSize uint32,
	depositCount *uint64, networkIDs []uint32, fromAddress string,
) ([]*Bridge, int, error) {
	whereClause, whereArgs := p.buildBridgesFilterClause(depositCount, networkIDs, fromAddress)
	orderByClause := "deposit_count DESC"

	bridgesCount, err := p.GetTotalNumberOfRecordsWithParams(ctx, bridgeTableName, whereClause, whereArgs)
	if err != nil {
		return []*Bridge{}, 0, err
	}

	if bridgesCount == 0 {
		return []*Bridge{}, 0, nil
	}

	offset, err := p.calculateOffset(pageNumber, pageSize, bridgesCount, "bridges")
	if err != nil {
		return nil, 0, err
	}

	rows, err := p.queryPagedWithParams(ctx, p.db, offset, pageSize, bridgeTableName,
		orderByClause, whereClause, whereArgs)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			p.log.Debugf("no bridges were found for provided parameters (pageNumber=%d, pageSize=%d, where clause=%s)",
				pageNumber, pageSize, whereClause)
			return nil, bridgesCount, nil
		}
		p.log.Errorf("GetBridgesPaged: queryPagedWithParams failed for pageNumber=%d, pageSize=%d: %v",
			pageNumber, pageSize, err)
		return nil, 0, err
	}

	defer func() {
		if cerr := rows.Close(); cerr != nil {
			p.log.Errorf("error closing rows: %v", cerr)
		}
	}()

	bridges := []*Bridge{}
	if err = meddler.ScanAll(rows, &bridges); err != nil {
		p.log.Errorf("GetBridgesPaged: meddler.ScanAll failed for pageNumber=%d, pageSize=%d: %v",
			pageNumber, pageSize, err)
		return nil, 0, err
	}

	return bridges, bridgesCount, nil
}

// buildBridgesFilterClause builds the WHERE clause for the bridges table
// based on the provided depositCount, networkIDs, fromAddress and globalIndex
// Returns the WHERE clause with placeholders and the corresponding arguments for parameterized queries
func (p *processor) buildBridgesFilterClause(depositCount *uint64, networkIDs []uint32,
	fromAddress string) (string, []interface{}) {
	const clauseCapacity = 3
	clauses := make([]string, 0, clauseCapacity)
	args := make([]interface{}, 0, clauseCapacity)
	paramIndex := 1

	if depositCount != nil {
		clauses = append(clauses, fmt.Sprintf("deposit_count = $%d", paramIndex))
		args = append(args, *depositCount)
		paramIndex++
	}

	if len(networkIDs) > 0 {
		placeholders := make([]string, len(networkIDs))
		for i, id := range networkIDs {
			placeholders[i] = fmt.Sprintf("$%d", paramIndex)
			args = append(args, id)
			paramIndex++
		}
		clauses = append(clauses, fmt.Sprintf("destination_network IN (%s)", strings.Join(placeholders, ", ")))
	}

	if fromAddress != "" && common.IsHexAddress(fromAddress) {
		// Only match non-NULL from_address values with the specified address
		// NULL values will not match this filter (intentional - explicit filtering)
		// Use UPPER for case-insensitive comparison (addresses stored in checksum format)
		clauses = append(clauses, fmt.Sprintf("UPPER(from_address) = UPPER($%d)", paramIndex))
		args = append(args, fromAddress)
	}

	if len(clauses) > 0 {
		return " WHERE " + strings.Join(clauses, " AND "), args
	}
	return "", nil
}

// GetBridgesInDepositRange returns bridges with deposit_count in the range
// (fromDepositCount, toDepositCount] (exclusive lower bound, inclusive upper bound) whose
// destination_network is one of destinationNetworkIDs (all destination networks when empty),
// ordered by deposit_count ASC and paged. A nil fromDepositCount means no lower bound (full
// history up to toDepositCount). Only the live "bridge" table is queried; bridge_archive rows
// (bridges rolled back by a BackwardLET) are intentionally excluded, since claim candidates are
// only meaningful for bridges still present in the current exit tree.
func (p *processor) GetBridgesInDepositRange(
	ctx context.Context, pageNumber, pageSize uint32,
	fromDepositCount *uint64, toDepositCount uint64, destinationNetworkIDs []uint32,
) ([]*Bridge, int, error) {
	whereClause, whereArgs := p.buildDepositRangeFilterClause(fromDepositCount, toDepositCount, destinationNetworkIDs)
	const orderByClause = "deposit_count ASC"

	bridgesCount, err := p.GetTotalNumberOfRecordsWithParams(ctx, bridgeTableName, whereClause, whereArgs)
	if err != nil {
		return []*Bridge{}, 0, err
	}

	if bridgesCount == 0 {
		return []*Bridge{}, 0, nil
	}

	offset, err := p.calculateOffset(pageNumber, pageSize, bridgesCount, "bridges")
	if err != nil {
		return nil, 0, err
	}

	rows, err := p.queryPagedWithParams(ctx, p.db, offset, pageSize, bridgeTableName,
		orderByClause, whereClause, whereArgs)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			p.log.Debugf("no bridges were found in deposit range for provided parameters "+
				"(pageNumber=%d, pageSize=%d, where clause=%s)", pageNumber, pageSize, whereClause)
			return nil, bridgesCount, nil
		}
		p.log.Errorf("GetBridgesInDepositRange: queryPagedWithParams failed for pageNumber=%d, pageSize=%d: %v",
			pageNumber, pageSize, err)
		return nil, 0, err
	}

	defer func() {
		if cerr := rows.Close(); cerr != nil {
			p.log.Errorf("error closing rows: %v", cerr)
		}
	}()

	bridges := []*Bridge{}
	if err = meddler.ScanAll(rows, &bridges); err != nil {
		p.log.Errorf("GetBridgesInDepositRange: meddler.ScanAll failed for pageNumber=%d, pageSize=%d: %v",
			pageNumber, pageSize, err)
		return nil, 0, err
	}

	return bridges, bridgesCount, nil
}

// buildDepositRangeFilterClause builds the WHERE clause for GetBridgesInDepositRange:
// deposit_count in (fromDepositCount, toDepositCount] and, when non-empty, destination_network
// IN networkIDs. Returns the WHERE clause with placeholders and the corresponding arguments for
// parameterized queries.
func (p *processor) buildDepositRangeFilterClause(
	fromDepositCount *uint64, toDepositCount uint64, networkIDs []uint32,
) (string, []interface{}) {
	const clauseCapacity = 2
	clauses := make([]string, 0, clauseCapacity)
	args := make([]interface{}, 0, clauseCapacity)
	paramIndex := 1

	if fromDepositCount != nil {
		clauses = append(clauses, fmt.Sprintf("deposit_count > $%d", paramIndex))
		args = append(args, *fromDepositCount)
		paramIndex++
	}

	clauses = append(clauses, fmt.Sprintf("deposit_count <= $%d", paramIndex))
	args = append(args, toDepositCount)
	paramIndex++

	if len(networkIDs) > 0 {
		placeholders := make([]string, len(networkIDs))
		for i, id := range networkIDs {
			placeholders[i] = fmt.Sprintf("$%d", paramIndex)
			args = append(args, id)
			paramIndex++
		}
		clauses = append(clauses, fmt.Sprintf("destination_network IN (%s)", strings.Join(placeholders, ", ")))
	}

	return " WHERE " + strings.Join(clauses, " AND "), args
}

// buildTokenMappingsFilterClause builds the WHERE clause for the token_mapping table
// based on the provided originTokenAddress
func (p *processor) buildTokenMappingsFilterClause(originTokenAddress string) string {
	if common.IsHexAddress(originTokenAddress) {
		return fmt.Sprintf(" WHERE UPPER(origin_token_address) LIKE '%s'", strings.ToUpper(originTokenAddress))
	}
	return ""
}

// GetLegacyTokenMigrations returns the paged legacy token migrations from the database
func (p *processor) GetLegacyTokenMigrations(
	ctx context.Context, pageNumber, pageSize uint32) ([]*LegacyTokenMigration, int, error) {
	whereClause := ""
	legacyTokenMigrationsCount, err := p.GetTotalNumberOfRecords(ctx, legacyTokenMigrationTableName, whereClause)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to fetch the total number of %s entries: %w", legacyTokenMigrationTableName, err)
	}

	if legacyTokenMigrationsCount == 0 {
		return []*LegacyTokenMigration{}, 0, nil
	}

	offset, err := p.calculateOffset(pageNumber, pageSize, legacyTokenMigrationsCount, "legacy token migrations")
	if err != nil {
		return nil, 0, err
	}

	rows, err := p.queryPaged(
		ctx, p.db, offset, pageSize, legacyTokenMigrationTableName, orderByBlockDesc, whereClause,
	)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			p.log.Debugf("no legacy token migrations were found for provided parameters (pageNumber=%d, pageSize=%d)",
				pageNumber, pageSize)
			return nil, legacyTokenMigrationsCount, nil
		}
		p.log.Errorf("GetLegacyTokenMigrations: queryPaged failed for pageNumber=%d, pageSize=%d: %v",
			pageNumber, pageSize, err)
		return nil, 0, err
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			p.log.Errorf("error closing rows: %v", cerr)
		}
	}()

	tokenMigrations := []*LegacyTokenMigration{}
	if err = meddler.ScanAll(rows, &tokenMigrations); err != nil {
		p.log.Errorf("GetLegacyTokenMigrations: meddler.ScanAll failed for pageNumber=%d, pageSize=%d: %v",
			pageNumber, pageSize, err)
		return nil, 0, err
	}

	return tokenMigrations, legacyTokenMigrationsCount, nil
}

func (p *processor) queryBlockRange(
	ctx context.Context, tx dbtypes.Querier,
	fromBlock, toBlock uint64, query string,
) (*sql.Rows, error) {
	// Create a context with database timeout
	dbCtx, _ := p.withDatabaseTimeout(ctx)
	rows, err := tx.QueryContext(dbCtx, query, fromBlock, toBlock)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, db.ErrNotFound
		}
		return nil, err
	}
	return rows, nil
}

// queryPaged returns a paged result from the given table with context support
func (p *processor) queryPaged(ctx context.Context, tx dbtypes.Querier,
	offset, pageSize uint32,
	table, orderByClause, whereClause string,
) (*sql.Rows, error) {
	// Create a context with database timeout
	dbCtx, _ := p.withDatabaseTimeout(ctx)
	rows, err := tx.QueryContext(dbCtx, fmt.Sprintf(`
		SELECT *
		FROM %s
		%s
		ORDER BY %s
		LIMIT $1 OFFSET $2;
	`, table, whereClause, orderByClause), pageSize, offset)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, db.ErrNotFound
		}
		return nil, err
	}
	return rows, nil
}

// queryPagedWithParams returns a paged result from the given table with parameterized WHERE clause
// to prevent SQL injection attacks
func (p *processor) queryPagedWithParams(ctx context.Context, tx dbtypes.Querier,
	offset, pageSize uint32,
	table, orderByClause, whereClause string,
	whereArgs []interface{},
) (*sql.Rows, error) {
	// Create a context with database timeout
	dbCtx, _ := p.withDatabaseTimeout(ctx)

	// Build the query with placeholders for pagination
	// whereArgs already contains placeholders starting from $1
	// We need to adjust LIMIT and OFFSET to use the next available placeholders
	nextParam := len(whereArgs) + 1
	query := fmt.Sprintf(`
		SELECT *
		FROM %s
		%s
		ORDER BY %s
		LIMIT $%d OFFSET $%d;
	`, table, whereClause, orderByClause, nextParam, nextParam+1)

	// Combine WHERE args with pagination args
	args := make([]interface{}, 0, len(whereArgs)+2) //nolint:mnd
	args = append(args, whereArgs...)
	args = append(args, pageSize, offset)

	rows, err := tx.QueryContext(dbCtx, query, args...)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, db.ErrNotFound
		}
		return nil, err
	}
	return rows, nil
}

// GetLastProcessedBlock returns the last processed block by the processor, including blocks
// that don't have events
func (p *processor) GetLastProcessedBlock(ctx context.Context) (uint64, bool, error) {
	return p.getLastProcessedBlockWithTx(ctx, p.db)
}

func (p *processor) getLastProcessedBlockWithTx(ctx context.Context, tx dbtypes.Querier) (uint64, bool, error) {
	var lastProcessedBlockNum uint64

	// Create a context with database timeout
	dbCtx, cancel := p.withDatabaseTimeout(ctx)
	defer cancel()

	row := tx.QueryRowContext(dbCtx, "SELECT num FROM block ORDER BY num DESC LIMIT 1;")
	err := row.Scan(&lastProcessedBlockNum)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return lastProcessedBlockNum, err == nil, err
}

// Reorg triggers a purge and reset process on the processor to leaf it on a state
// as if the last block processed was firstReorgedBlock-1
func (p *processor) Reorg(ctx context.Context, firstReorgedBlock uint64) error {
	p.log.Infof("reorg detected at %d block", firstReorgedBlock)

	// Create a context with database timeout for the transaction
	dbCtx, cancel := p.withDatabaseTimeout(ctx)
	defer cancel()

	tx, err := db.NewTx(dbCtx, p.db)
	if err != nil {
		p.log.Errorf("failed to start transaction for reorg: %v", err)
		return err
	}

	shouldRollback := true
	defer func() {
		if shouldRollback {
			p.rollbackTransaction(tx)
		}
	}()

	// ---------------------------------------------------------------------
	// 1. Load affected deposit counts and BackwardLETs BEFORE deleting blocks, bridges and BackwardLET entries
	// ---------------------------------------------------------------------
	backwardLETsQuery := `
		SELECT previous_deposit_count, new_deposit_count
        FROM backward_let
        WHERE block_num >= $1`
	var backwardLETs []*BackwardLET
	if err := meddler.QueryAll(tx, &backwardLETs, backwardLETsQuery, firstReorgedBlock); err != nil {
		return fmt.Errorf("failed to retrieve the affected backward LETs: %w", err)
	}

	var depositCountsToRemove map[uint32]struct{}
	if len(backwardLETs) > 0 {
		depositCountsToRemove, err = loadReorgedDepositCounts(tx, firstReorgedBlock)
		if err != nil {
			p.log.Errorf("failed to retrieve reorged bridges: %v", err)
			return err
		}
	}

	// ---------------------------------------------------------
	// 2. Delete blocks (cascade delete everything else)
	// ---------------------------------------------------------
	blocksRes, err := tx.Exec(`DELETE FROM block WHERE num >= $1`, firstReorgedBlock)
	if err != nil {
		p.log.Errorf("failed to delete blocks during reorg: %v", err)
		return err
	}

	rowsAffected, err := blocksRes.RowsAffected()
	if err != nil {
		p.log.Errorf("failed to get rows affected during reorg: %v", err)
		return err
	}

	// ---------------------------------------------------------
	// 3. Reorg exit tree to clean state
	// ---------------------------------------------------------
	if err := p.exitTree.Reorg(tx, firstReorgedBlock); err != nil {
		p.log.Errorf("failed to reorg exit tree: %v", err)
		return err
	}

	// ---------------------------------------------------------
	// 4. Restore bridges removed by BackwardLET
	// ---------------------------------------------------------
	err = p.restoreBackwardLETBridges(tx, backwardLETs, depositCountsToRemove)
	if err != nil {
		return err
	}

	if err = tx.Commit(); err != nil {
		p.log.Errorf("failed to commit reorg transaction: %v", err)
		return err
	}

	shouldRollback = false

	// Unhalt unconditionally: a successfully committed purge leaves the DB at a valid
	// consolidation point even when it deleted nothing (rowsAffected == 0), because a halt
	// can be caused by a block whose tx was rolled back and never persisted (e.g. data built
	// from an undetected tip reorg).
	p.unhalt()

	p.log.Infof("reorged to block %d, %d rows affected", firstReorgedBlock, rowsAffected)

	return nil
}

// restoreBackwardLETBridges restores bridges that were previously removed by BackwardLET events
func (p *processor) restoreBackwardLETBridges(tx dbtypes.Txer, backwardLETs []*BackwardLET,
	removedDepositCounts map[uint32]struct{}) error {
	restoreQuery := `
		SELECT *
		FROM bridge_archive
		WHERE deposit_count >= $1 AND deposit_count <= $2
		ORDER BY deposit_count ASC
	`

	for _, backwardLET := range backwardLETs {
		prev, err := aggkitcommon.SafeUint64(backwardLET.PreviousDepositCount)
		if err != nil {
			return fmt.Errorf("invalid previous deposit count: %w", err)
		}

		next, err := aggkitcommon.SafeUint64(backwardLET.NewDepositCount)
		if err != nil {
			return fmt.Errorf("invalid new deposit count: %w", err)
		}

		var bridges []*Bridge
		if err := meddler.QueryAll(tx, &bridges, restoreQuery, next, prev); err != nil {
			return err
		}

		for _, b := range bridges {
			if _, ok := removedDepositCounts[b.DepositCount]; ok {
				// skip cascade-deleted bridges (prevent from restoring them)
				continue
			}

			// reset source
			b.Source = ""
			if err := meddler.Insert(tx, bridgeTableName, b); err != nil {
				return err
			}

			leaf := types.Leaf{
				Index: b.DepositCount,
				Hash:  b.Hash(),
			}
			if _, err := p.exitTree.PutLeaf(tx, b.BlockNum, b.BlockPos, leaf); err != nil {
				return err
			}
		}

		// cleanup bridge_archive
		if _, err := tx.Exec(`
			DELETE FROM bridge_archive
			WHERE deposit_count >= $1 AND deposit_count <= $2
		`, next, prev); err != nil {
			return err
		}
	}

	return nil
}

// loadReorgedDepositCounts retrieves the bridges that are going to be deleted by the reorg,
// and returns its deposit counts.
// The bridges are retrieved from the bridge_archive table, because in case there were BackwardLET events,
// they would have already deleted the bridges from bridge table.
func loadReorgedDepositCounts(tx dbtypes.Txer, fromBlock uint64) (map[uint32]struct{}, error) {
	rows, err := tx.Query(`
		SELECT deposit_count
		FROM bridge_archive
		WHERE block_num >= $1
	`, fromBlock)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[uint32]struct{})
	for rows.Next() {
		var depositCount uint32
		if err := rows.Scan(&depositCount); err != nil {
			return nil, err
		}
		result[depositCount] = struct{}{}
	}
	return result, nil
}

// ProcessBlock process the events of the block to build the exit tree
// and updates the last processed block (can be called without events for that purpose)
func (p *processor) ProcessBlock(ctx context.Context, block sync.Block) error {
	if p.isHalted() {
		p.logHaltGuardHit()
		return sync.ErrInconsistentState
	}

	// Create a context with database timeout for the transaction
	dbCtx, cancel := p.withDatabaseTimeout(ctx)
	defer cancel()

	tx, err := db.NewTx(dbCtx, p.db)
	if err != nil {
		p.log.Errorf("failed to start transaction for block %d: %v", block.Num, err)
		return err
	}
	shouldRollback := true
	defer func() {
		if shouldRollback {
			p.rollbackTransaction(tx)
		}
	}()

	query := `INSERT INTO block (num, hash) VALUES ($1, $2) ON CONFLICT (num) DO UPDATE SET hash = $2`
	if _, err := tx.Exec(query, block.Num, block.Hash.String()); err != nil {
		p.log.Errorf("failed to insert block %d: %v", block.Num, err)
		return err
	}

	var blockPos *uint64
	var hasAnyBridge bool
	for _, e := range block.Events {
		event, ok := e.(Event)
		if !ok {
			err = fmt.Errorf("ProcessBlock: failed to convert event %T to Event type in block %d", e, block.Num)
			p.log.Errorf(err.Error())
			return err
		}

		if event.Bridge != nil {
			if blockPos != nil {
				// increment block position based on forward LET events processed so far
				// in the current block
				event.Bridge.BlockPos = *blockPos
				*blockPos++
			}

			if _, err = p.exitTree.PutLeaf(tx, block.Num, event.Bridge.BlockPos, types.Leaf{
				Index: event.Bridge.DepositCount,
				Hash:  event.Bridge.Hash(),
			}); err != nil {
				if errors.Is(err, tree.ErrInvalidIndex) {
					p.halt(fmt.Sprintf("error adding leaf %d in block %d to the exit tree: %v",
						event.Bridge.DepositCount, block.Num, err))
				}
				return sync.ErrInconsistentState
			}
			if err = meddler.Insert(tx, bridgeTableName, event.Bridge); err != nil {
				p.log.Errorf("failed to insert bridge event at block %d: %v", block.Num, err)
				return err
			}
			// Mark that this block has at least one bridge
			hasAnyBridge = true
		}

		if event.TokenMapping != nil {
			if err = meddler.Insert(tx, tokenMappingTableName, event.TokenMapping); err != nil {
				p.log.Errorf("failed to insert token mapping event at block %d: %v", block.Num, err)
				return err
			}
		}

		if event.LegacyTokenMigration != nil {
			if err = meddler.Insert(tx, legacyTokenMigrationTableName, event.LegacyTokenMigration); err != nil {
				p.log.Errorf("failed to insert legacy token migration event at block %d: %v", block.Num, err)
				return err
			}
		}

		if event.RemoveLegacyToken != nil {
			_, err := tx.Exec(deleteLegacyTokenSQL, event.RemoveLegacyToken.LegacyTokenAddress.Hex())
			if err != nil {
				p.log.Errorf("failed to remove legacy token at block %d: %v", block.Num, err)
				return err
			}
		}

		if event.BackwardLET != nil {
			if err := p.insertBackwardLET(ctx, tx, block.Num, event.BackwardLET); err != nil {
				return err
			}
		}

		if event.ForwardLET != nil {
			newBlockPos, err := p.handleForwardLETEvent(tx, event.ForwardLET, blockPos)
			if err != nil {
				p.log.Errorf("failed to handle forward LET event at block %d: %v", block.Num, err)
				return err
			}

			blockPos = &newBlockPos
		}
	}
	if err := tx.Commit(); err != nil {
		p.log.Errorf("failed to commit db transaction (block number %d): %v", block.Num, err)
		return err
	}
	shouldRollback = false

	// Publish block number to bridge subscribers if this block contains any bridge
	if hasAnyBridge {
		p.bridgeSubscriber.Publish(block.Num)
	}

	logMsg := fmt.Sprintf("block %d processed with %d events", block.Num, len(block.Events))
	if len(block.Events) > 0 {
		p.log.Info(logMsg)

		if p.log.IsEnabledLogLevel(zapcore.DebugLevel) {
			p.log.Debugf("[%s] indexed events: ", p.syncerID)
			for _, e := range block.Events {
				event, ok := e.(Event)
				if !ok {
					p.log.Errorf("failed to convert event to Event type in block %d for debug logging", block.Num)
					return errors.New("failed to convert sync.Block.Event to Event for debug logging")
				}
				p.log.Debugf("%s", event.String())
			}
		}
	}

	return nil
}

// normalizeDepositCount checks whether given depositCount can fit into the uint64 and uint32 and downcasts it.
// Otherwise it returns an error.
func normalizeDepositCount(depositCount *big.Int) (uint64, uint32, error) {
	u64, err := aggkitcommon.SafeUint64(depositCount)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid deposit count: %w", err)
	}

	u32, err := aggkitcommon.SafeUint32(u64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid deposit count: %w", err)
	}

	return u64, u32, nil
}

// archiveAndDeleteBridgesAbove archives and removes all the bridges whose depositCount is greater than or equal to
// the provided one. After a BackwardLET to DC=N, leaves 0..N-1 remain valid; any bridge at deposit_count>=N
// is no longer present in the exit tree and must be archived and removed.
func (p *processor) archiveAndDeleteBridgesAbove(ctx context.Context, tx dbtypes.Txer, depositCount uint64) error {
	// 1. Load candidates
	query := fmt.Sprintf(`SELECT * FROM %s WHERE deposit_count >= $1`, bridgeTableName)
	var bridges []*Bridge
	if err := meddler.QueryAll(tx, &bridges, query, depositCount); err != nil {
		return err
	}

	if len(bridges) == 0 {
		return nil
	}

	deletedDepositCounts := make([]uint32, 0, len(bridges))
	// 2. Archive
	for _, b := range bridges {
		// Skip if already archived (can happen when a ForwardLET re-inserts a bridge that
		// was previously archived by an earlier BackwardLET, and then a new BackwardLET
		// targets the same deposit_count again).
		var count int
		if err := tx.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM bridge_archive WHERE deposit_count = ?", b.DepositCount,
		).Scan(&count); err != nil {
			return fmt.Errorf("failed to check bridge_archive for deposit_count %d: %w", b.DepositCount, err)
		}
		if count == 0 {
			b.Source = BridgeSourceBackwardLET
			if err := meddler.Insert(tx, "bridge_archive", b); err != nil {
				return err
			}
		}
		deletedDepositCounts = append(deletedDepositCounts, b.DepositCount)
	}

	// 3. Delete originals
	deleteQuery := fmt.Sprintf(`
		DELETE FROM %s
		WHERE deposit_count >= $1`,
		bridgeTableName)

	_, err := tx.ExecContext(ctx, deleteQuery, depositCount)
	if err != nil {
		return err
	}

	if len(deletedDepositCounts) > 0 {
		p.log.Debugf("BackwardLET archived + removed %d bridges with deposit_count >= %d: %v",
			len(deletedDepositCounts), depositCount, deletedDepositCounts,
		)
	}

	return nil
}

// sanityCheckLatestLER checks if the provided local exit root matches the latest one in the exit tree
func (p *processor) sanityCheckLatestLER(tx dbtypes.Txer, ler common.Hash) error {
	var lastRootHash common.Hash

	root, err := p.exitTree.GetLastRoot(tx)
	if err != nil {
		// if there is no root yet, we consider the zero hash as the last root
		if !errors.Is(err, db.ErrNotFound) {
			return fmt.Errorf("failed to get last root from exit tree: %w", err)
		}
	} else {
		lastRootHash = root.Hash
	}

	if ler == p.initialLER {
		// if the provided LER matches the initial LER, the DB should be empty (ZeroHash)
		if lastRootHash != aggkitcommon.ZeroHash {
			return fmt.Errorf("local exit root mismatch: expected %s, got %s. Note that %s is used to represent the initial LER",
				aggkitcommon.ZeroHash.String(), lastRootHash.String(), p.initialLER.String())
		}
		return nil
	}

	if lastRootHash != ler {
		return fmt.Errorf("local exit root mismatch: expected %s, got %s",
			ler.String(), lastRootHash.String())
	}
	return nil
}

// insertBackwardLET processes a BackwardLET event and updates the database accordingly
func (p *processor) insertBackwardLET(ctx context.Context, tx dbtypes.Txer, blockNum uint64, event *BackwardLET) error {
	// we sanity check that the previous root matches the latest one in the exit tree
	if err := p.sanityCheckLatestLER(tx, event.PreviousRoot); err != nil {
		p.log.Errorf("failed to sanity check LER before processing BackwardLET: %v", err)
		return err
	}

	newDepositCount, leafIndex, err := normalizeDepositCount(event.NewDepositCount)
	if err != nil {
		return err
	}

	// 1. archive and remove all bridges at deposit_count >= newDepositCount.
	// After BackwardLET to NewDepositCount=N, leaves 0..N-1 remain valid;
	// any bridge at DC=N or above is no longer in the exit tree.
	err = p.archiveAndDeleteBridgesAbove(ctx, tx, newDepositCount)
	if err != nil {
		return fmt.Errorf("failed to delete bridges above deposit count %d: %w",
			newDepositCount, err)
	}

	// 2. Remove leaves from the exit tree so that exactly newDepositCount leaves remain.
	// BackwardToIndex(N) keeps positions 0..N (N+1 leaves). To keep exactly newDepositCount
	// leaves (positions 0..newDepositCount-1), we call BackwardToIndex(newDepositCount-1).
	// Special case: for newDepositCount==0 the tree must be fully cleared, so use Reorg(0)
	// which deletes all root entries (block_num >= 0 = all rows).
	if leafIndex == 0 {
		if err := p.exitTree.Reorg(tx, 0); err != nil {
			p.log.Errorf("failed to clear exit tree for BackwardLET to DC=0: %v", err)
			return err
		}
	} else {
		if err := p.exitTree.BackwardToIndex(ctx, tx, leafIndex-1); err != nil {
			p.log.Errorf("failed to backward local exit tree to leaf index %d (deposit count: %d)",
				leafIndex, newDepositCount)
			return err
		}
	}

	// 4. sanity check that the new root matches the latest one in the exit tree
	if err := p.sanityCheckLatestLER(tx, event.NewRoot); err != nil {
		p.log.Errorf("failed to sanity check LER after processing BackwardLET: %v", err)
		return err
	}

	// 5. insert the backward let event to designated table
	if err = meddler.Insert(tx, backwardLETTableName, event); err != nil {
		p.log.Errorf("failed to insert backward local exit tree event at block %d: %v", blockNum, err)
		return err
	}

	return nil
}

// handleForwardLETEvent processes a ForwardLET event and updates the database accordingly
func (p *processor) handleForwardLETEvent(tx dbtypes.Txer, event *ForwardLET, blockPos *uint64) (uint64, error) {
	// first we sanity check that the previous root matches the latest one in the exit tree
	if err := p.sanityCheckLatestLER(tx, event.PreviousRoot); err != nil {
		return 0, fmt.Errorf("failed to sanity check LER before processing ForwardLET: %w", err)
	}

	// first we decode the new LET leaves from the forward LET event
	// they are basically bridge events, but without some fields set (tx hash, sender, from address)
	decodedNewLeaves, err := decodeForwardLETLeaves(event.NewLeaves)
	if err != nil {
		return 0, fmt.Errorf("failed to decode new leaves in forward LET: %w", err)
	}

	// PreviousDepositCount is the number of leaves already in the tree before this ForwardLET,
	// which equals the deposit_count (leaf index) to assign to the first new leaf.
	// When PreviousRoot matches the initial LER, the tree is empty, so the first leaf index is 0 (Go zero value).
	var newDepositCount uint32
	if event.PreviousRoot != p.initialLER {
		_, newDepositCount, err = normalizeDepositCount(event.PreviousDepositCount)
		if err != nil {
			return 0, fmt.Errorf("failed to normalize previous deposit count in forward LET: %w", err)
		}
	}
	newBlockPos := event.BlockPos
	if blockPos != nil {
		newBlockPos = *blockPos
	}

	const getArchivedBridgesSQL = `
		SELECT * FROM bridge_archive
		WHERE leaf_type = $1
			AND origin_network = $2
			AND origin_address = $3
			AND destination_network = $4
			AND destination_address = $5
			AND amount = $6
			AND metadata = $7
	`

	// now we process each new leaf to insert them into the exit tree and bridges table
	for _, leaf := range decodedNewLeaves {
		var archivedBridges []*Bridge
		err = meddler.QueryAll(tx, &archivedBridges, getArchivedBridgesSQL,
			leaf.LeafType,
			leaf.OriginNetwork,
			leaf.OriginAddress,
			leaf.DestinationNetwork,
			leaf.DestinationAddress,
			leaf.Amount.String(),
			leaf.Metadata,
		)
		if err != nil {
			return 0, fmt.Errorf("failed to query archived bridges: %w", err)
		}

		var (
			txnHash     = event.TxnHash
			txnSender   common.Address
			fromAddrPtr *common.Address
		)

		// let's see if we have exactly one archived bridge that matches the forward LET leaf.
		// usually we should have exactly one match since to recover the LET on L2,
		// we must have a backwards LET done which archives the bridges,
		// and then a forward LET that re-adds them to the exit tree after fixing it.
		// however, this is not always the case (e.g. when a ForwardLET is issued without
		// a preceding BackwardLET). When no match is found, or when there are multiple matches
		// (in which case we cannot determine which one to use), we leave the txnSender and
		// fromAddr fields empty.
		switch len(archivedBridges) {
		case 1:
			archivedBridge := archivedBridges[0]
			txnHash = archivedBridge.TxHash
			txnSender = archivedBridge.TxnSender
			// It copies the fromAddr pointer, which could be nil
			fromAddrPtr = archivedBridge.FromAddress
		case 0:
			p.log.Warnf("no archived bridge found that matches forward LET leaf %s; "+
				"txnSender and fromAddr fields will be left empty", leaf.String())
		default:
			p.log.Warnf("multiple archived bridges found that match forward LET leaf %s; "+
				"cannot set txnSender and fromAddr fields to the bridge", leaf.String())
		}

		// create the new bridge event from the forward LET leaf
		bridge := leaf.ToBridge(
			event.BlockNum,
			newBlockPos,
			event.BlockTimestamp,
			newDepositCount,
			txnHash,
			txnSender,
			fromAddrPtr,
		)

		// insert the new bridge leaf into the local exit tree
		if _, err = p.exitTree.PutLeaf(tx, event.BlockNum, newBlockPos, types.Leaf{
			Index: newDepositCount,
			Hash:  bridge.Hash(),
		}); err != nil {
			if errors.Is(err, tree.ErrInvalidIndex) {
				p.halt(fmt.Sprintf("error adding leaf to the exit tree: %v", err))
			}
			return 0, sync.ErrInconsistentState
		}

		// insert the new bridge into the bridges table
		if err = meddler.Insert(tx, bridgeTableName, &bridge); err != nil {
			return 0, fmt.Errorf("failed to insert bridge event from ForwardLET: %w", err)
		}

		newDepositCount++
		newBlockPos++
	}

	// after processing all new leaves, we sanity check that the new root matches the latest one in the exit tree
	if err := p.sanityCheckLatestLER(tx, event.NewRoot); err != nil {
		return 0, fmt.Errorf("failed to sanity check LER after processing ForwardLET: %w", err)
	}

	// finally, insert the forward LET event into the designated table
	if err = meddler.Insert(tx, forwardLETTableName, event); err != nil {
		return 0, fmt.Errorf("failed to insert forward local exit tree event: %w", err)
	}

	return newBlockPos, nil
}

// GetTotalNumberOfRecords returns the total number of records in the given table
func (p *processor) GetTotalNumberOfRecords(ctx context.Context, tableName, whereClause string) (int, error) {
	if !tableNameRegex.MatchString(tableName) {
		return 0, fmt.Errorf("invalid table name '%s' provided", tableName)
	}

	// Create a context with database timeout
	dbCtx, cancel := p.withDatabaseTimeout(ctx)
	defer cancel()

	count := 0
	err := p.db.QueryRowContext(dbCtx, fmt.Sprintf(
		`SELECT COUNT(*) AS count FROM %s%s;`, tableName, whereClause,
	)).Scan(&count)
	if err != nil {
		return 0, err
	}

	return count, nil
}

// GetTotalNumberOfRecordsWithParams returns the total number of records with parameterized WHERE clause
// Note: whereClause must be constructed internally using parameterized placeholders ($1, $2, etc.)
// and should never contain user input directly concatenated into the string.
func (p *processor) GetTotalNumberOfRecordsWithParams(ctx context.Context, tableName,
	whereClause string, args []interface{}) (int, error) {
	if !tableNameRegex.MatchString(tableName) {
		return 0, fmt.Errorf("invalid table name '%s' provided", tableName)
	}

	// Create a context with database timeout
	dbCtx, cancel := p.withDatabaseTimeout(ctx)
	defer cancel()

	count := 0
	// Safe: tableName is validated by regex, whereClause contains only SQL placeholders ($1, $2, etc.)
	// constructed internally, and actual values are passed via args parameter
	query := "SELECT COUNT(*) AS count FROM " + tableName + whereClause + ";"
	err := p.db.QueryRowContext(dbCtx, query, args...).Scan(&count)
	if err != nil {
		return 0, err
	}

	return count, nil
}

// GetTokenMappings returns the paged token mappings from the database
func (p *processor) GetTokenMappings(ctx context.Context, pageNumber, pageSize uint32, originTokenAddress string,
) ([]*TokenMapping, int, error) {
	whereClause := p.buildTokenMappingsFilterClause(originTokenAddress)
	totalTokenMappings, err := p.GetTotalNumberOfRecords(ctx, tokenMappingTableName, whereClause)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to fetch the total number of %s entries: %w", tokenMappingTableName, err)
	}

	if totalTokenMappings == 0 {
		return []*TokenMapping{}, 0, nil
	}

	offset, err := p.calculateOffset(pageNumber, pageSize, totalTokenMappings, "token mappings")
	if err != nil {
		return nil, 0, fmt.Errorf("failed to calculate offset for pageNumber=%d, pageSize=%d: %w", pageNumber, pageSize, err)
	}

	tokenMappings, err := p.fetchTokenMappings(ctx, pageSize, offset, whereClause)
	if err != nil {
		return nil, 0, err
	}

	return tokenMappings, totalTokenMappings, nil
}

// fetchTokenMappings fetches token mappings from the database, based on the provided pagination parameters
func (p *processor) fetchTokenMappings(ctx context.Context, pageSize uint32, offset uint32, whereClause string,
) ([]*TokenMapping, error) {
	orderByClause := "block_num DESC"

	rows, err := p.queryPaged(ctx, p.db, offset, pageSize, tokenMappingTableName, orderByClause, whereClause)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			pageNumber := (offset / pageSize) + 1
			p.log.Debugf("no token mappings were found for provided parameters (pageNumber=%d, pageSize=%d, where clause=%s)",
				pageNumber, pageSize, whereClause)
			return nil, nil
		}

		p.log.Errorf("failed to fetch token mappings: %v", err)
		return nil, err
	}

	// Ensure rows are closed after we're done with them
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			p.log.Errorf("error closing rows: %v", cerr)
		}
	}()

	tokenMappings := []*TokenMapping{}
	if err = meddler.ScanAll(rows, &tokenMappings); err != nil {
		p.log.Errorf("failed to convert token mappings to the object model: %v", err)
		return nil, err
	}

	return tokenMappings, nil
}

// GetBridgeByDepositCount returns the bridge with the given deposit count from the bridge table,
// falling back to bridge_archive if not found. Returns db.ErrNotFound if absent in both tables.
func (p *processor) GetBridgeByDepositCount(ctx context.Context, depositCount uint32) (*Bridge, error) {
	dbCtx, cancel := p.withDatabaseTimeout(ctx)
	defer cancel()

	for _, tq := range []struct{ name, query string }{
		{bridgeTableName, bridgeByDepositCountSQL},
		{"bridge_archive", archiveByDepositCountSQL},
	} {
		rows, err := p.db.QueryContext(dbCtx, tq.query, depositCount)
		if err != nil {
			if strings.Contains(err.Error(), "no such table") {
				continue
			}
			return nil, fmt.Errorf("GetBridgeByDepositCount (%s): %w", tq.name, err)
		}
		bridges := []*Bridge{}
		scanErr := meddler.ScanAll(rows, &bridges)
		if cerr := rows.Close(); cerr != nil {
			p.log.Errorf("error closing rows: %v", cerr)
		}
		if scanErr != nil {
			return nil, fmt.Errorf("GetBridgeByDepositCount (%s): scan: %w", tq.name, scanErr)
		}
		if len(bridges) > 0 {
			return bridges[0], nil
		}
	}
	return nil, db.ErrNotFound
}

// GetBridgesByContent returns all bridges (from both bridge and bridge_archive) that match
// the given content fields (leaf_type, origin/destination addresses/networks, amount, metadata).
// Errors from bridge_archive are silently ignored to match the original runbook behavior.
func (p *processor) GetBridgesByContent(
	ctx context.Context,
	leafType uint8,
	originAddress common.Address,
	destinationNetwork uint32,
	destinationAddress common.Address,
	amount *big.Int,
	metadata []byte,
) ([]*Bridge, error) {
	dbCtx, cancel := p.withDatabaseTimeout(ctx)
	defer cancel()

	amountStr := "0"
	if amount != nil {
		amountStr = amount.String()
	}

	// Addresses are stored as raw 20-byte BLOBs by meddler (common.Address is [20]byte,
	// so meddler stores it as binary). Use addr[:] to pass raw bytes for correct BLOB comparison.
	queryArgs := []any{leafType, originAddress[:], destinationNetwork, destinationAddress[:], amountStr}

	// Choose pre-built constant queries based on whether metadata is present.
	// Using compile-time constants avoids dynamic SQL construction.
	type tableQuery struct{ name, bridge, archive string }
	var tq tableQuery
	if len(metadata) == 0 {
		tq = tableQuery{"no-meta", bridgeByContentNoMetaSQL, archiveByContentNoMetaSQL}
	} else {
		tq = tableQuery{"with-meta", bridgeByContentWithMetaSQL, archiveByContentWithMetaSQL}
		queryArgs = append(queryArgs, metadata)
	}

	p.log.Infof("[GetBridgesByContent] leaf_type=%d origin_addr=%s dest_net=%d"+
		" dest_addr=%s amount=%s metadata_len=%d variant=%s",
		leafType, originAddress.Hex(), destinationNetwork, destinationAddress.Hex(), amountStr, len(metadata), tq.name)

	var result []*Bridge
	for _, pair := range []struct{ name, query string }{
		{bridgeTableName, tq.bridge},
		{"bridge_archive", tq.archive},
	} {
		rows, err := p.db.QueryContext(dbCtx, pair.query, queryArgs...)
		p.log.Infof("[GetBridgesByContent] table=%s", pair.name)
		if err != nil {
			if strings.Contains(err.Error(), "no such table") {
				continue
			}
			return nil, fmt.Errorf("GetBridgesByContent (%s): %w", pair.name, err)
		}
		var bridges []*Bridge
		scanErr := meddler.ScanAll(rows, &bridges)
		if cerr := rows.Close(); cerr != nil {
			p.log.Errorf("error closing rows: %v", cerr)
		}
		if scanErr != nil {
			return nil, fmt.Errorf("GetBridgesByContent (%s): scan: %w", pair.name, scanErr)
		}
		p.log.Infof("[GetBridgesByContent] table=%s found=%d", pair.name, len(bridges))
		for i, b := range bridges {
			p.log.Infof("[GetBridgesByContent]   bridge[%d]: deposit_count=%d origin_addr=%s dest_addr=%s amount=%s metadata=%x",
				i, b.DepositCount, b.OriginAddress.Hex(), b.DestinationAddress.Hex(), b.Amount.String(), b.Metadata)
		}
		result = append(result, bridges...)
	}
	return result, nil
}

// GenerateGlobalIndexForNetworkID builds the "global index" used to identify bridges and claims.
func GenerateGlobalIndexForNetworkID(networkID uint32, depositCount uint32) *big.Int {
	rollupIndex := uint32(0)
	mainnetFlag := networkID == 0
	if !mainnetFlag {
		rollupIndex = networkID - 1
	}

	return GenerateGlobalIndex(mainnetFlag, rollupIndex, depositCount)
}

// LegacyZkEVMRollupNetworkID is the legacy zkEVM rollup network ID whose pre-Etrog bridges used a
// bare deposit-count global index instead of the encoded global index.
const LegacyZkEVMRollupNetworkID uint32 = 1

// GlobalIndexForBridge computes the global index for a bridge exit, applying the legacy pre-Etrog
// zkEVM encoding (a bare deposit count) for bridges destined to the legacy zkEVM rollup at or before
// etrogL1UpgradeBlock. An etrogL1UpgradeBlock of 0 disables the pre-Etrog special-casing. networkID
// is the network used to encode the post-Etrog global index. It returns the global index and whether
// the bridge was treated as pre-Etrog.
func GlobalIndexForBridge(
	destinationNetwork uint32,
	blockNum uint64,
	depositCount uint32,
	networkID uint32,
	etrogL1UpgradeBlock uint64,
) (*big.Int, bool) {
	if etrogL1UpgradeBlock > 0 &&
		destinationNetwork == LegacyZkEVMRollupNetworkID &&
		blockNum <= etrogL1UpgradeBlock {
		return new(big.Int).SetUint64(uint64(depositCount)), true
	}
	return GenerateGlobalIndexForNetworkID(networkID, depositCount), false
}

// GenerateGlobalIndex encodes a unique "global index" used for identifying bridges and claims.
// The index is constructed as a big integer from three components:
// - mainnetFlag: indicates if the origin network is mainnet (true) or a rollup (false).
//   - If true, the first 4-byte segment is set to `0x01` and the next 4 bytes are zero.
//   - If false, the first 4-byte segment is the rollupIndex (networkID - 1).
//
// - rollupIndex: only used if mainnetFlag is false; represents (networkID - 1).
// - depositCount: always appended as the final 4-byte segment.
//
// Encoding layout (big-endian concatenation of 4-byte chunks):
//
//	[ mainnetFlag ] [ rollupIndex ] [ depositCount ]
//
// Examples:
//
//	mainnetFlag=true,  depositCount=3  → 0x0100000000000003
//	mainnetFlag=false, rollupIndex=1, depositCount=3 → 0x0000000100000003
//
// The result is returned as a *big.Int that can be used consistently across
// mainnet and rollup networks.
func GenerateGlobalIndex(mainnetFlag bool, rollupIndex uint32, depositCount uint32) *big.Int {
	var (
		globalIndexBytes []byte
		buf              [globalIndexPartSize]byte
	)
	if mainnetFlag {
		globalIndexBytes = append(globalIndexBytes, big.NewInt(1).Bytes()...)
		ri := new(big.Int).FillBytes(buf[:])
		globalIndexBytes = append(globalIndexBytes, ri...)
	} else {
		ri := new(big.Int).SetUint64(uint64(rollupIndex)).FillBytes(buf[:])
		globalIndexBytes = append(globalIndexBytes, ri...)
	}
	leri := new(big.Int).SetUint64(uint64(depositCount)).FillBytes(buf[:])
	globalIndexBytes = append(globalIndexBytes, leri...)

	return new(big.Int).SetBytes(globalIndexBytes)
}

// Decodes global index to its three parts:
// 1. mainnetFlag - first byte
// 2. rollupIndex - next 4 bytes
// 3. localExitRootIndex - last 4 bytes
// NOTE - mainnet flag is not in the global index bytes if it is false
// NOTE - rollup index is 0 if mainnet flag is true
// NOTE - rollup index is not in the global index bytes if mainnet flag is false and rollup index is 0
func DecodeGlobalIndex(globalIndex *big.Int) (mainnetFlag bool,
	rollupIndex uint32, localExitRootIndex uint32, err error) {
	globalIndexBytes := globalIndex.Bytes()
	l := len(globalIndexBytes)

	if l == 0 {
		// false, 0, 0
		return
	}

	bit := globalIndex.Bit(mainnetFlagPosition)
	if bit == 1 {
		// true, rollupIndex, localExitRootIndex
		mainnetFlag = true
	}

	localExitRootFromIdx := max(l-globalIndexPartSize, 0)
	rollupIndexFromIdx := max(localExitRootFromIdx-globalIndexPartSize, 0)

	rollupIndex = aggkitcommon.BytesToUint32(globalIndexBytes[rollupIndexFromIdx:localExitRootFromIdx])
	localExitRootIndex = aggkitcommon.BytesToUint32(globalIndexBytes[localExitRootFromIdx:])

	return
}

// rollbackTransaction rolls back the transaction and logs an error if it fails
func (p *processor) rollbackTransaction(tx dbtypes.SQLTxer) {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		log.Errorf("error rolling back tx: %v", err)
	}
}

func (p *processor) calculateOffset(pageNumber, pageSize uint32,
	recordsCount int, tableName string) (uint32, error) {
	offset := (pageNumber - 1) * pageSize
	if int64(offset) >= int64(recordsCount) {
		msg := fmt.Sprintf("invalid page number for given page size and total number of %s (page=%d, size=%d, total=%d)",
			tableName, pageNumber, pageSize, recordsCount)
		p.log.Debugf(msg)
		return 0, errors.New(msg)
	}
	return offset, nil
}

// isHalted checks if the processor is halted
func (p *processor) isHalted() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.halted
}

// halt sets the processor to a halted state, preventing it from processing blocks
func (p *processor) halt(reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.halted = true
	p.haltedReason = reason
	p.haltGuardHits = 0
	p.log.Errorf("processor is halted due to the following reason: %s", reason)
}

// logHaltGuardHit logs (at a throttled rate) that a call was rejected because the processor is
// halted. See aggkitcommon.ShouldLogRetryAtError.
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

// unhalt sets the processor to a non-halted state, allowing it to process blocks again
func (p *processor) unhalt() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.halted = false
	p.haltedReason = ""
	p.haltGuardHits = 0
	p.log.Info("processor unhalted")
}

func (p *processor) withDatabaseTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, p.dbQueryTimeout)
}
