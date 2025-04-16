package bridgesync

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	mutex "sync"

	"github.com/agglayer/aggkit/bridgesync/migrations"
	aggkitCommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/db/compatibility"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	"github.com/agglayer/aggkit/tree"
	"github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/iden3/go-iden3-crypto/keccak256"
	"github.com/russross/meddler"
	_ "modernc.org/sqlite"
)

const (
	globalIndexPartSize = 4
	globalIndexMaxSize  = 9

	// bridgeTableName is the name of the table that stores bridge events
	bridgeTableName = "bridge"

	// claimTableName is the name of the table that stores claim events
	claimTableName = "claim"

	// tokenMappingTableName is the name of the table that stores token mapping events
	tokenMappingTableName = "token_mapping"

	// legacyTokenMigrationTableName is the name of the table that stores legacy token migration events
	legacyTokenMigrationTableName = "legacy_token_migration"
)

var (
	// errBlockNotProcessedFormat indicates that the given block(s) have not been processed yet.
	errBlockNotProcessedFormat = fmt.Sprintf("block %%d not processed, last processed: %%d")

	// tableNameRegex is the regex pattern to validate table names
	tableNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

	// deleteLegacyTokenSQL is the SQL statement to delete legacy token migration event
	// with specific legacy token address
	deleteLegacyTokenSQL = fmt.Sprintf("DELETE FROM %s WHERE legacy_token_address = $1", legacyTokenMigrationTableName)
)

// Bridge is the representation of a bridge event
type Bridge struct {
	BlockNum           uint64         `meddler:"block_num" json:"block_num"`
	BlockPos           uint64         `meddler:"block_pos" json:"block_pos"`
	FromAddress        common.Address `meddler:"from_address,address" json:"from_address"`
	TxHash             common.Hash    `meddler:"tx_hash,hash" json:"tx_hash"`
	Calldata           []byte         `meddler:"calldata" json:"calldata"`
	BlockTimestamp     uint64         `meddler:"block_timestamp" json:"block_timestamp"`
	LeafType           uint8          `meddler:"leaf_type" json:"leaf_type"`
	OriginNetwork      uint32         `meddler:"origin_network" json:"origin_network"`
	OriginAddress      common.Address `meddler:"origin_address" json:"origin_address"`
	DestinationNetwork uint32         `meddler:"destination_network" json:"destination_network"`
	DestinationAddress common.Address `meddler:"destination_address" json:"destination_address"`
	Amount             *big.Int       `meddler:"amount,bigint" json:"amount"`
	Metadata           []byte         `meddler:"metadata" json:"metadata"`
	DepositCount       uint32         `meddler:"deposit_count" json:"deposit_count"`
}

// Cant change the Hash() here after adding BlockTimestamp, TxHash. Might affect previous versions
// Hash returns the hash of the bridge event as expected by the exit tree
func (b *Bridge) Hash() common.Hash {
	const (
		uint32ByteSize = 4
		bigIntSize     = 32
	)
	origNet := make([]byte, uint32ByteSize)
	binary.BigEndian.PutUint32(origNet, b.OriginNetwork)
	destNet := make([]byte, uint32ByteSize)
	binary.BigEndian.PutUint32(destNet, b.DestinationNetwork)

	metaHash := keccak256.Hash(b.Metadata)
	var buf [bigIntSize]byte
	if b.Amount == nil {
		b.Amount = big.NewInt(0)
	}

	return common.BytesToHash(keccak256.Hash(
		[]byte{b.LeafType},
		origNet,
		b.OriginAddress[:],
		destNet,
		b.DestinationAddress[:],
		b.Amount.FillBytes(buf[:]),
		metaHash,
	))
}

// BridgeResponse is the representation of a bridge event with additional fields
type BridgeResponse struct {
	BridgeHash common.Hash `json:"bridge_hash"`
	Bridge
}

// MarshalJSON for hex-encoding Metadata field
func (b *BridgeResponse) MarshalJSON() ([]byte, error) {
	type Alias BridgeResponse // Prevent recursion
	return json.Marshal(&struct {
		Metadata string `json:"metadata"`
		CallData string `json:"calldata"`
		*Alias
	}{
		Metadata: fmt.Sprintf("0x%s", hex.EncodeToString(b.Metadata)),
		CallData: fmt.Sprintf("0x%s", hex.EncodeToString(b.Calldata)),
		Alias:    (*Alias)(b),
	})
}

// Claim representation of a claim event
type Claim struct {
	BlockNum            uint64         `meddler:"block_num"`
	BlockPos            uint64         `meddler:"block_pos"`
	FromAddress         common.Address `meddler:"from_address,address"`
	TxHash              common.Hash    `meddler:"tx_hash,hash"`
	GlobalIndex         *big.Int       `meddler:"global_index,bigint"`
	OriginNetwork       uint32         `meddler:"origin_network"`
	OriginAddress       common.Address `meddler:"origin_address"`
	DestinationAddress  common.Address `meddler:"destination_address"`
	Amount              *big.Int       `meddler:"amount,bigint"`
	ProofLocalExitRoot  types.Proof    `meddler:"proof_local_exit_root,merkleproof"`
	ProofRollupExitRoot types.Proof    `meddler:"proof_rollup_exit_root,merkleproof"`
	MainnetExitRoot     common.Hash    `meddler:"mainnet_exit_root,hash"`
	RollupExitRoot      common.Hash    `meddler:"rollup_exit_root,hash"`
	GlobalExitRoot      common.Hash    `meddler:"global_exit_root,hash"`
	DestinationNetwork  uint32         `meddler:"destination_network"`
	Metadata            []byte         `meddler:"metadata"`
	IsMessage           bool           `meddler:"is_message"`
	BlockTimestamp      uint64         `meddler:"block_timestamp"`
}

// decodeEtrogCalldata decodes claim calldata for Etrog fork
func (c *Claim) decodeEtrogCalldata(senderAddr common.Address, data []any) (bool, error) {
	// Unpack method inputs. Note that both claimAsset and claimMessage have the same interface
	// for the relevant parts
	// claimAsset/claimMessage(
	// 	0: smtProofLocalExitRoot,
	// 	1: smtProofRollupExitRoot,
	// 	2: globalIndex,
	// 	3: mainnetExitRoot,
	// 	4: rollupExitRoot,
	// 	5: originNetwork,
	// 	6: originTokenAddress/originAddress,
	// 	7: destinationNetwork,
	// 	8: destinationAddress,
	// 	9: amount,
	// 	10: metadata,
	// )

	actualGlobalIndex, ok := data[2].(*big.Int)
	if !ok {
		return false, fmt.Errorf("unexpected type for actualGlobalIndex, expected *big.Int got '%T'", data[2])
	}
	if actualGlobalIndex.Cmp(c.GlobalIndex) != 0 {
		// not the claim we're looking for
		return false, nil
	}
	proofLER := [types.DefaultHeight]common.Hash{}
	proofLERBytes, ok := data[0].([types.DefaultHeight][common.HashLength]byte)
	if !ok {
		return false, fmt.Errorf("unexpected type for proofLERBytes, expected [32][32]byte got '%T'", data[0])
	}

	proofRER := [types.DefaultHeight]common.Hash{}
	proofRERBytes, ok := data[1].([types.DefaultHeight][common.HashLength]byte)
	if !ok {
		return false, fmt.Errorf("unexpected type for proofRERBytes, expected [32][32]byte got '%T'", data[1])
	}

	for i := range int(types.DefaultHeight) {
		proofLER[i] = proofLERBytes[i]
		proofRER[i] = proofRERBytes[i]
	}
	c.ProofLocalExitRoot = proofLER
	c.ProofRollupExitRoot = proofRER

	c.MainnetExitRoot, ok = data[3].([common.HashLength]byte)
	if !ok {
		return false, fmt.Errorf("unexpected type for 'MainnetExitRoot'. Expected '[32]byte', got '%T'", data[3])
	}

	c.RollupExitRoot, ok = data[4].([common.HashLength]byte)
	if !ok {
		return false, fmt.Errorf("unexpected type for 'RollupExitRoot'. Expected '[32]byte', got '%T'", data[4])
	}

	c.DestinationNetwork, ok = data[7].(uint32)
	if !ok {
		return false, fmt.Errorf("unexpected type for 'DestinationNetwork'. Expected 'uint32', got '%T'", data[7])
	}

	c.Metadata, ok = data[10].([]byte)
	if !ok {
		return false, fmt.Errorf("unexpected type for 'claim Metadata'. Expected '[]byte', got '%T'", data[10])
	}

	c.GlobalExitRoot = crypto.Keccak256Hash(c.MainnetExitRoot.Bytes(), c.RollupExitRoot.Bytes())
	c.FromAddress = senderAddr

	return true, nil
}

// decodePreEtrogCalldata decodes the claim calldata for pre-Etrog forks
func (c *Claim) decodePreEtrogCalldata(senderAddr common.Address, data []any) (bool, error) {
	// claimMessage/claimAsset(
	// 	0: bytes32[32] smtProof,
	// 	1: uint32 index,
	// 	2: bytes32 mainnetExitRoot,
	// 	3: bytes32 rollupExitRoot,
	// 	4: uint32 originNetwork,
	// 	5: address originTokenAddress,
	// 	6: uint32 destinationNetwork,
	// 	7: address destinationAddress,
	// 	8: uint256 amount,
	// 	9: bytes metadata
	// )
	actualGlobalIndex, ok := data[1].(uint32)
	if !ok {
		return false, fmt.Errorf("unexpected type for actualGlobalIndex, expected uint32 got '%T'", data[1])
	}

	if new(big.Int).SetUint64(uint64(actualGlobalIndex)).Cmp(c.GlobalIndex) != 0 {
		// not the claim we're looking for
		return false, nil
	}

	proof := [types.DefaultHeight]common.Hash{}
	proofBytes, ok := data[0].([types.DefaultHeight][common.HashLength]byte)
	if !ok {
		return false, fmt.Errorf("unexpected type for proofLERBytes, expected [32][32]byte got '%T'", data[0])
	}

	for i := range int(types.DefaultHeight) {
		proof[i] = proofBytes[i]
	}
	c.ProofLocalExitRoot = proof

	c.MainnetExitRoot, ok = data[2].([common.HashLength]byte)
	if !ok {
		return false, fmt.Errorf("unexpected type for 'MainnetExitRoot'. Expected '[32]byte', got '%T'", data[2])
	}

	c.RollupExitRoot, ok = data[3].([common.HashLength]byte)
	if !ok {
		return false, fmt.Errorf("unexpected type for 'RollupExitRoot'. Expected '[32]byte', got '%T'", data[3])
	}

	c.DestinationNetwork, ok = data[6].(uint32)
	if !ok {
		return false, fmt.Errorf("unexpected type for 'DestinationNetwork'. Expected 'uint32', got '%T'", data[6])
	}

	c.Metadata, ok = data[9].([]byte)
	if !ok {
		return false, fmt.Errorf("unexpected type for 'Metadata'. Expected '[]byte', got '%T'", data[9])
	}

	c.GlobalExitRoot = crypto.Keccak256Hash(c.MainnetExitRoot.Bytes(), c.RollupExitRoot.Bytes())
	c.FromAddress = senderAddr

	return true, nil
}

// ClaimResponse is the representation of a claim event with trimmed fields
type ClaimResponse struct {
	BlockNum           uint64         `json:"block_num"`
	BlockTimestamp     uint64         `json:"block_timestamp"`
	TxHash             common.Hash    `json:"tx_hash"`
	GlobalIndex        *big.Int       `json:"global_index"`
	OriginAddress      common.Address `json:"origin_address"`
	OriginNetwork      uint32         `json:"origin_network"`
	DestinationAddress common.Address `json:"destination_address"`
	DestinationNetwork uint32         `json:"destination_network"`
	Amount             *big.Int       `json:"amount"`
	FromAddress        common.Address `json:"from_address"`
}

type TokenMappingType uint8

const (
	WrappedToken = iota
	SovereignToken
)

func (l TokenMappingType) String() string {
	return [...]string{"WrappedToken", "SovereignToken"}[l]
}

// TokenMapping representation of a NewWrappedToken event, that is emitted by the bridge contract
type TokenMapping struct {
	BlockNum            uint64           `meddler:"block_num" json:"block_num"`
	BlockPos            uint64           `meddler:"block_pos" json:"block_pos"`
	BlockTimestamp      uint64           `meddler:"block_timestamp" json:"block_timestamp"`
	TxHash              common.Hash      `meddler:"tx_hash,hash" json:"tx_hash"`
	OriginNetwork       uint32           `meddler:"origin_network" json:"origin_network"`
	OriginTokenAddress  common.Address   `meddler:"origin_token_address,address" json:"origin_token_address"`
	WrappedTokenAddress common.Address   `meddler:"wrapped_token_address,address" json:"wrapped_token_address"`
	Metadata            []byte           `meddler:"metadata" json:"metadata"`
	IsNotMintable       bool             `meddler:"is_not_mintable" json:"is_not_mintable"`
	Calldata            []byte           `meddler:"calldata" json:"calldata"`
	Type                TokenMappingType `meddler:"token_type" json:"token_type"`
}

// MarshalJSON for hex-encoding Metadata and Calldata fields
func (t *TokenMapping) MarshalJSON() ([]byte, error) {
	type Alias TokenMapping // Prevent recursion
	return json.Marshal(&struct {
		Metadata string `json:"metadata"`
		Calldata string `json:"calldata"`
		*Alias
	}{
		Metadata: fmt.Sprintf("0x%s", hex.EncodeToString(t.Metadata)),
		Calldata: fmt.Sprintf("0x%s", hex.EncodeToString(t.Calldata)),
		Alias:    (*Alias)(t),
	})
}

// LegacyTokenMigration representation of a MigrateLegacyToken event,
// that is emitted by the sovereign chain bridge contract.
type LegacyTokenMigration struct {
	BlockNum            uint64         `meddler:"block_num" json:"block_num"`
	BlockPos            uint64         `meddler:"block_pos" json:"block_pos"`
	BlockTimestamp      uint64         `meddler:"block_timestamp" json:"block_timestamp"`
	TxHash              common.Hash    `meddler:"tx_hash,hash" json:"tx_hash"`
	Sender              common.Address `meddler:"sender,address" json:"sender"`
	LegacyTokenAddress  common.Address `meddler:"legacy_token_address,address" json:"legacy_token_address"`
	UpdatedTokenAddress common.Address `meddler:"updated_token_address,address" json:"updated_token_address"`
	Amount              *big.Int       `meddler:"amount,bigint" json:"amount"`
	Calldata            []byte         `meddler:"calldata" json:"calldata"`
}

// RemoveLegacyToken representation of a RemoveLegacySovereignTokenAddress event,
// that is emitted by the sovereign chain bridge contract.
type RemoveLegacyToken struct {
	BlockNum           uint64         `meddler:"block_num" json:"block_num"`
	BlockPos           uint64         `meddler:"block_pos" json:"block_pos"`
	BlockTimestamp     uint64         `meddler:"block_timestamp" json:"block_timestamp"`
	TxHash             common.Hash    `meddler:"tx_hash,hash" json:"tx_hash"`
	LegacyTokenAddress common.Address `meddler:"legacy_token_address,address" json:"legacy_token_address"`
}

// Event combination of bridge, claim, token mapping and legacy token migration events
type Event struct {
	Pos                  uint64
	Bridge               *Bridge
	Claim                *Claim
	TokenMapping         *TokenMapping
	LegacyTokenMigration *LegacyTokenMigration
	RemoveLegacyToken    *RemoveLegacyToken
}

type processor struct {
	db           *sql.DB
	exitTree     *tree.AppendOnlyTree
	log          *log.Logger
	mu           mutex.RWMutex
	halted       bool
	haltedReason string
	compatibility.CompatibilityDataStorager[sync.RuntimeData]
}

func newProcessor(dbPath string, name string, logger *log.Logger) (*processor, error) {
	err := migrations.RunMigrations(dbPath)
	if err != nil {
		return nil, err
	}
	database, err := db.NewSQLiteDB(dbPath)
	if err != nil {
		return nil, err
	}

	exitTree := tree.NewAppendOnlyTree(database, "")
	return &processor{
		db:       database,
		exitTree: exitTree,
		log:      logger,
		CompatibilityDataStorager: compatibility.NewKeyValueToCompatibilityStorage[sync.RuntimeData](
			db.NewKeyValueStorage(database),
			name,
		),
	}, nil
}

func (p *processor) GetBridgesPublished(
	ctx context.Context, fromBlock, toBlock uint64,
) ([]Bridge, error) {
	return p.GetBridges(ctx, fromBlock, toBlock)
}

//nolint:dupl
func (p *processor) GetBridges(
	ctx context.Context, fromBlock, toBlock uint64,
) ([]Bridge, error) {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			log.Warnf("error rolling back tx: %v", err)
		}
	}()
	rows, err := p.queryBlockRange(tx, fromBlock, toBlock, bridgeTableName)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			p.log.Debugf("no bridges were found for block range [%d..%d]", fromBlock, toBlock)
			return []Bridge{}, nil
		}
		return nil, err
	}
	bridgePtrs := []*Bridge{}
	if err = meddler.ScanAll(rows, &bridgePtrs); err != nil {
		return nil, err
	}
	bridgesIface := db.SlicePtrsToSlice(bridgePtrs)
	bridges, ok := bridgesIface.([]Bridge)
	if !ok {
		return nil, errors.New("failed to convert from []*Bridge to []Bridge")
	}
	return bridges, nil
}

//nolint:dupl
func (p *processor) GetClaims(
	ctx context.Context, fromBlock, toBlock uint64,
) ([]Claim, error) {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			log.Warnf("error rolling back tx: %v", err)
		}
	}()
	rows, err := p.queryBlockRange(tx, fromBlock, toBlock, claimTableName)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			p.log.Debugf("no claims were found for block range [%d..%d]", fromBlock, toBlock)
			return []Claim{}, nil
		}
		return nil, err
	}
	claimPtrs := []*Claim{}
	if err = meddler.ScanAll(rows, &claimPtrs); err != nil {
		return nil, err
	}
	claimsIface := db.SlicePtrsToSlice(claimPtrs)
	claims, ok := claimsIface.([]Claim)
	if !ok {
		return nil, errors.New("failed to convert from []*Claim to []Claim")
	}
	return claims, nil
}

func (p *processor) GetBridgesPaged(
	ctx context.Context, pageNumber, pageSize uint32, depositCount *uint64,
) ([]*BridgeResponse, int, error) {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer func() {
		if err := tx.Rollback(); err != nil {
			log.Warnf("error rolling back tx: %v", err)
		}
	}()
	orderByClause := "deposit_count DESC"
	whereClause := ""
	bridgesCount, err := p.GetTotalNumberOfRecords(bridgeTableName)
	if err != nil {
		return nil, 0, err
	}
	if depositCount != nil {
		whereClause = fmt.Sprintf("WHERE deposit_count = %d", *depositCount)
		pageNumber = 1
		pageSize = 1
	}
	offset := (pageNumber - 1) * pageSize
	if offset >= uint32(bridgesCount) {
		p.log.Debugf("offset is greater than total bridges (page number=%d, page size=%d, total bridges=%d)",
			pageNumber, pageSize, bridgesCount)
		return nil, 0, fmt.Errorf("provided offset is greater than the total bridges count (offset=%d, total count=%d)",
			offset, bridgesCount)
	}
	rows, err := p.queryPaged(tx, offset, pageSize, bridgeTableName, orderByClause, whereClause)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			p.log.Debugf("no bridges were found for provided parameters (pageNumber=%d, pageSize=%d, where clause=%s)",
				pageNumber, pageSize, whereClause)
			return nil, bridgesCount, nil
		}
		return nil, 0, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			p.log.Warnf("error closing rows: %v", err)
		}
	}()
	bridgePtrs := []*Bridge{}
	if err = meddler.ScanAll(rows, &bridgePtrs); err != nil {
		return nil, 0, err
	}
	bridgeResponsePtrs := make([]*BridgeResponse, len(bridgePtrs))
	for i, bridgePtr := range bridgePtrs {
		bridgeResponsePtrs[i] = &BridgeResponse{
			Bridge:     *bridgePtr,
			BridgeHash: bridgePtr.Hash(),
		}
	}
	if depositCount != nil {
		bridgesCount = len(bridgePtrs)
	}
	return bridgeResponsePtrs, bridgesCount, nil
}

func (p *processor) GetClaimsPaged(
	ctx context.Context, pageNumber, pageSize uint32,
) ([]*ClaimResponse, int, error) {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer func() {
		if err := tx.Rollback(); err != nil {
			log.Warnf("error rolling back tx: %v", err)
		}
	}()
	claimsCount, err := p.GetTotalNumberOfRecords(claimTableName)
	if err != nil {
		return nil, 0, err
	}

	offset := (pageNumber - 1) * pageSize
	if offset >= uint32(claimsCount) {
		p.log.Debugf("offset is greater than total claims (page number=%d, page size=%d, total claims=%d)",
			pageNumber, pageSize, claimsCount)
		return nil, 0, fmt.Errorf("provided offset is greater than the total claims count (offset=%d, total count=%d)",
			offset, claimsCount)
	}

	orderByClause := "block_num DESC, block_pos DESC"
	whereClause := ""
	rows, err := p.queryPaged(tx, offset, pageSize, claimTableName, orderByClause, whereClause)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			p.log.Debugf("no claims were found for provided parameters (pageNumber=%d, pageSize=%d)",
				pageNumber, pageSize)
			return nil, claimsCount, nil
		}
		return nil, 0, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			p.log.Warnf("error closing rows: %v", err)
		}
	}()
	claimPtrs := []*Claim{}
	if err = meddler.ScanAll(rows, &claimPtrs); err != nil {
		return nil, 0, err
	}

	claimResponsePtrs := make([]*ClaimResponse, len(claimPtrs))
	for i, claimPtr := range claimPtrs {
		claimResponsePtrs[i] = &ClaimResponse{
			GlobalIndex:        claimPtr.GlobalIndex,
			DestinationNetwork: claimPtr.DestinationNetwork,
			TxHash:             claimPtr.TxHash,
			Amount:             claimPtr.Amount,
			BlockNum:           claimPtr.BlockNum,
			FromAddress:        claimPtr.FromAddress,
			DestinationAddress: claimPtr.DestinationAddress,
			OriginAddress:      claimPtr.OriginAddress,
			OriginNetwork:      claimPtr.OriginNetwork,
			BlockTimestamp:     claimPtr.BlockTimestamp,
		}
	}

	return claimResponsePtrs, claimsCount, nil
}

// GetLegacyTokenMigrations returns the paged legacy token migrations from the database
func (p *processor) GetLegacyTokenMigrations(
	ctx context.Context, pageNumber, pageSize uint32) ([]*LegacyTokenMigration, int, error) {
	legacyTokenMigrationsCount, err := p.GetTotalNumberOfRecords(legacyTokenMigrationTableName)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to fetch the total number of %s entries: %w", legacyTokenMigrationTableName, err)
	}

	if legacyTokenMigrationsCount == 0 {
		return nil, 0, nil
	}

	offset := (pageNumber - 1) * pageSize
	if offset >= uint32(legacyTokenMigrationsCount) {
		p.log.Debugf(
			"offset is greater than the total legacy token migrations "+
				"(page number=%d, page size=%d, total legacy token migrations=%d)",
			pageNumber, pageSize, legacyTokenMigrationsCount)
		return nil, 0,
			fmt.Errorf("provided offset is greater than the total legacy token migrations count (offset=%d, total count=%d)",
				offset, legacyTokenMigrationsCount)
	}

	orderByClause := "block_num, block_pos DESC"
	whereClause := ""
	rows, err := p.queryPaged(p.db, offset, pageSize, legacyTokenMigrationTableName, orderByClause, whereClause)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			p.log.Debugf("no legacy token migrations were found for provided parameters (pageNumber=%d, pageSize=%d)",
				pageNumber, pageSize)
			return nil, legacyTokenMigrationsCount, nil
		}
		return nil, 0, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			p.log.Warnf("error closing rows: %v", err)
		}
	}()
	tokenMigrations := []*LegacyTokenMigration{}
	if err = meddler.ScanAll(rows, &tokenMigrations); err != nil {
		return nil, 0, err
	}

	return tokenMigrations, legacyTokenMigrationsCount, nil
}

func (p *processor) queryBlockRange(tx db.Querier, fromBlock, toBlock uint64, table string) (*sql.Rows, error) {
	if err := p.isBlockProcessed(tx, toBlock); err != nil {
		return nil, err
	}
	rows, err := tx.Query(fmt.Sprintf(`
		SELECT * FROM %s
		WHERE block_num >= $1 AND block_num <= $2;
	`, table), fromBlock, toBlock)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, db.ErrNotFound
		}
		return nil, err
	}
	return rows, nil
}

// queryPaged returns a paged result from the given table
func (p *processor) queryPaged(tx db.Querier,
	offset, pageSize uint32,
	table, orderByClause, whereClause string,
) (*sql.Rows, error) {
	rows, err := tx.Query(fmt.Sprintf(`
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

func (p *processor) isBlockProcessed(tx db.Querier, blockNum uint64) error {
	lpb, err := p.getLastProcessedBlockWithTx(tx)
	if err != nil {
		return err
	}
	if lpb < blockNum {
		return fmt.Errorf(errBlockNotProcessedFormat, blockNum, lpb)
	}
	return nil
}

// GetLastProcessedBlock returns the last processed block by the processor, including blocks
// that don't have events
func (p *processor) GetLastProcessedBlock(ctx context.Context) (uint64, error) {
	return p.getLastProcessedBlockWithTx(p.db)
}

func (p *processor) getLastProcessedBlockWithTx(tx db.Querier) (uint64, error) {
	var lastProcessedBlock uint64
	row := tx.QueryRow("SELECT num FROM block ORDER BY num DESC LIMIT 1;")
	err := row.Scan(&lastProcessedBlock)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return lastProcessedBlock, err
}

// Reorg triggers a purge and reset process on the processor to leaf it on a state
// as if the last block processed was firstReorgedBlock-1
func (p *processor) Reorg(ctx context.Context, firstReorgedBlock uint64) error {
	tx, err := db.NewTx(ctx, p.db)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			if errRllbck := tx.Rollback(); errRllbck != nil && !errors.Is(errRllbck, sql.ErrTxDone) {
				log.Errorf("error while rolling back tx %v", errRllbck)
			}
		}
	}()

	res, err := tx.Exec(`DELETE FROM block WHERE num >= $1;`, firstReorgedBlock)
	if err != nil {
		return err
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return err
	}

	if err = p.exitTree.Reorg(tx, firstReorgedBlock); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	sync.UnhaltIfAffectedRows(&p.halted, &p.haltedReason, &p.mu, rowsAffected)
	return nil
}

// ProcessBlock process the events of the block to build the exit tree
// and updates the last processed block (can be called without events for that purpose)
func (p *processor) ProcessBlock(ctx context.Context, block sync.Block) error {
	if p.isHalted() {
		log.Errorf("processor is halted due to: %s", p.haltedReason)
		return sync.ErrInconsistentState
	}
	tx, err := db.NewTx(ctx, p.db)
	if err != nil {
		return err
	}
	shouldRollback := true
	defer func() {
		if shouldRollback {
			if errRllbck := tx.Rollback(); errRllbck != nil && !errors.Is(errRllbck, sql.ErrTxDone) {
				log.Errorf("error while rolling back tx %v", errRllbck)
			}
		}
	}()

	if _, err := tx.Exec(`INSERT INTO block (num) VALUES ($1)`, block.Num); err != nil {
		return err
	}
	for _, e := range block.Events {
		event, ok := e.(Event)
		if !ok {
			return errors.New("failed to convert sync.Block.Event to Event")
		}

		if event.Bridge != nil {
			if err = p.exitTree.AddLeaf(tx, block.Num, event.Pos, types.Leaf{
				Index: event.Bridge.DepositCount,
				Hash:  event.Bridge.Hash(),
			}); err != nil {
				if errors.Is(err, tree.ErrInvalidIndex) {
					p.mu.Lock()
					p.halted = true
					p.haltedReason = fmt.Sprintf("error adding leaf to the exit tree: %v", err)
					p.mu.Unlock()
				}
				return sync.ErrInconsistentState
			}
			if err = meddler.Insert(tx, bridgeTableName, event.Bridge); err != nil {
				return err
			}
		}

		if event.Claim != nil {
			if err = meddler.Insert(tx, claimTableName, event.Claim); err != nil {
				return err
			}
		}

		if event.TokenMapping != nil {
			if err = meddler.Insert(tx, tokenMappingTableName, event.TokenMapping); err != nil {
				return err
			}
		}

		if event.LegacyTokenMigration != nil {
			if err = meddler.Insert(tx, legacyTokenMigrationTableName, event.LegacyTokenMigration); err != nil {
				return err
			}
		}

		if event.RemoveLegacyToken != nil {
			_, err := tx.Exec(deleteLegacyTokenSQL, event.RemoveLegacyToken.LegacyTokenAddress.Hex())
			if err != nil {
				return err
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	shouldRollback = false

	p.log.Debugf("processed %d events until block %d", len(block.Events), block.Num)
	return nil
}

// GetTotalNumberOfRecords returns the total number of records in the given table
func (p *processor) GetTotalNumberOfRecords(tableName string) (int, error) {
	if !tableNameRegex.MatchString(tableName) {
		return 0, fmt.Errorf("invalid table name '%s' provided", tableName)
	}

	count := 0
	err := p.db.QueryRow(fmt.Sprintf(`SELECT COUNT(*) AS count FROM %s;`, tableName)).Scan(&count)
	if err != nil {
		return 0, err
	}

	return count, nil
}

// GetTokenMappings returns the paged token mappings from the database
func (p *processor) GetTokenMappings(ctx context.Context, pageNumber, pageSize uint32) ([]*TokenMapping, int, error) {
	totalTokenMappings, err := p.GetTotalNumberOfRecords(tokenMappingTableName)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to fetch the total number of %s entries: %w", tokenMappingTableName, err)
	}

	if totalTokenMappings == 0 {
		return []*TokenMapping{}, 0, nil
	}

	offset := (pageNumber - 1) * pageSize
	if offset >= uint32(totalTokenMappings) {
		p.log.Debugf("offset is greater than total token mappings (page number=%d, page size=%d, total token mappings=%d)",
			pageNumber, pageSize, totalTokenMappings)
		return nil, 0,
			fmt.Errorf("provided offset is greater than the total token mappings count (offset=%d, total count=%d)",
				offset, totalTokenMappings)
	}

	tokenMappings, err := p.fetchTokenMappings(ctx, pageSize, offset)
	if err != nil {
		return nil, 0, err
	}

	return tokenMappings, totalTokenMappings, nil
}

// fetchTokenMappings fetches token mappings from the database, based on the provided pagination parameters
func (p *processor) fetchTokenMappings(ctx context.Context, pageSize uint32, offset uint32) ([]*TokenMapping, error) {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		p.log.Errorf("failed to create the db transaction: %v", err)
		return nil, err
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			p.log.Warnf("error rolling back tx: %v", err)
		}
	}()

	orderByClause := "block_num DESC"
	rows, err := p.queryPaged(tx, offset, pageSize, tokenMappingTableName, orderByClause, "")
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			pageNumber := (offset / pageSize) + 1
			p.log.Debugf("no token mappings were found for provided parameters (pageNumber=%d, pageSize=%d)",
				pageNumber, pageSize)
			return nil, nil
		}

		p.log.Errorf("failed to fetch token mappings: %v", err)
		return nil, err
	}

	defer func() {
		if err := rows.Close(); err != nil {
			p.log.Warnf("error closing rows: %v", err)
		}
	}()

	tokenMappings := []*TokenMapping{}
	if err = meddler.ScanAll(rows, &tokenMappings); err != nil {
		p.log.Errorf("failed to convert token mappings to the object model: %v", err)
		return nil, err
	}

	return tokenMappings, nil
}

func GenerateGlobalIndex(mainnetFlag bool, rollupIndex uint32, localExitRootIndex uint32) *big.Int {
	var (
		globalIndexBytes []byte
		buf              [globalIndexPartSize]byte
	)
	if mainnetFlag {
		globalIndexBytes = append(globalIndexBytes, big.NewInt(1).Bytes()...)
		ri := big.NewInt(0).FillBytes(buf[:])
		globalIndexBytes = append(globalIndexBytes, ri...)
	} else {
		ri := big.NewInt(0).SetUint64(uint64(rollupIndex)).FillBytes(buf[:])
		globalIndexBytes = append(globalIndexBytes, ri...)
	}
	leri := big.NewInt(0).SetUint64(uint64(localExitRootIndex)).FillBytes(buf[:])
	globalIndexBytes = append(globalIndexBytes, leri...)

	result := big.NewInt(0).SetBytes(globalIndexBytes)

	return result
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
	if l > globalIndexMaxSize {
		return false, 0, 0, errors.New("invalid global index length")
	}

	if l == 0 {
		// false, 0, 0
		return
	}

	if l == globalIndexMaxSize {
		// true, rollupIndex, localExitRootIndex
		mainnetFlag = true
	}

	localExitRootFromIdx := l - globalIndexPartSize
	if localExitRootFromIdx < 0 {
		localExitRootFromIdx = 0
	}

	rollupIndexFromIdx := localExitRootFromIdx - globalIndexPartSize
	if rollupIndexFromIdx < 0 {
		rollupIndexFromIdx = 0
	}

	rollupIndex = aggkitCommon.BytesToUint32(globalIndexBytes[rollupIndexFromIdx:localExitRootFromIdx])
	localExitRootIndex = aggkitCommon.BytesToUint32(globalIndexBytes[localExitRootFromIdx:])

	return
}

func (p *processor) isHalted() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.halted
}
