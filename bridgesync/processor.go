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

	// claimTableName is the name of the table that stores claim events
	claimTableName = "claim"

	// tokenMappingTableName is the name of the table that stores token mapping events
	tokenMappingTableName = "token_mapping"

	// legacyTokenMigrationTableName is the name of the table that stores legacy token migration events
	legacyTokenMigrationTableName = "legacy_token_migration"

	// unsetClaimTableName is the name of the table that stores unset claim events
	unsetClaimTableName = "unset_claim"

	// setClaimTableName is the name of the table that stores set claim events
	setClaimTableName = "set_claim"

	// nilStr holds nil string
	nilStr = "nil"
)

const (
	// orderByBlockDesc is the default order by clause for block-based queries
	orderByBlockDesc = "block_num DESC, block_pos DESC"

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
)

var (
	// errFailToConvertClaims indicates that the conversion from []*Claim to []Claim failed.
	errFailToConvertClaims = errors.New("failed to convert from []*Claim to []Claim")

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

// Bridge is the representation of a bridge event
type Bridge struct {
	BlockNum           uint64         `meddler:"block_num"`
	BlockPos           uint64         `meddler:"block_pos"`
	FromAddress        common.Address `meddler:"from_address,address"`
	TxHash             common.Hash    `meddler:"tx_hash,hash"`
	BlockTimestamp     uint64         `meddler:"block_timestamp"`
	LeafType           uint8          `meddler:"leaf_type"`
	OriginNetwork      uint32         `meddler:"origin_network"`
	OriginAddress      common.Address `meddler:"origin_address"`
	DestinationNetwork uint32         `meddler:"destination_network"`
	DestinationAddress common.Address `meddler:"destination_address"`
	Amount             *big.Int       `meddler:"amount,bigint"`
	Metadata           []byte         `meddler:"metadata"`
	DepositCount       uint32         `meddler:"deposit_count"`
	TxnSender          common.Address `meddler:"txn_sender,address"`
}

func (b *Bridge) String() string {
	amountStr := nilStr
	if b.Amount != nil {
		amountStr = b.Amount.String()
	}
	return fmt.Sprintf("Bridge{BlockNum: %d, BlockPos: %d, FromAddress: %s, TxHash: %s, "+
		"BlockTimestamp: %d, LeafType: %d, OriginNetwork: %d, OriginAddress: %s, "+
		"DestinationNetwork: %d, DestinationAddress: %s, Amount: %s, Metadata: %x, "+
		"DepositCount: %d, TxnSender: %s}",
		b.BlockNum, b.BlockPos, b.FromAddress.String(), b.TxHash.String(),
		b.BlockTimestamp, b.LeafType, b.OriginNetwork, b.OriginAddress.String(),
		b.DestinationNetwork, b.DestinationAddress.String(), amountStr, b.Metadata,
		b.DepositCount, b.TxnSender.String())
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

type ClaimType string

const (
	ClaimEvent         ClaimType = "ClaimEvent"
	DetailedClaimEvent ClaimType = "DetailedClaimEvent"
)

// Claim representation of a claim event
type Claim struct {
	BlockNum            uint64         `meddler:"block_num"`
	BlockPos            uint64         `meddler:"block_pos"`
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
	Type                ClaimType      `meddler:"type"`
}

func (c *Claim) String() string {
	globalIndexStr := nilStr
	if c.GlobalIndex != nil {
		globalIndexStr = c.GlobalIndex.String()
	}

	amountStr := nilStr
	if c.Amount != nil {
		amountStr = c.Amount.String()
	}

	return fmt.Sprintf("Claim{BlockNum: %d, BlockPos: %d, TxHash: %s, GlobalIndex: %s, "+
		"OriginNetwork: %d, OriginAddress: %s, DestinationAddress: %s, Amount: %s, "+
		"ProofLocalExitRoot: %v, ProofRollupExitRoot: %v, MainnetExitRoot: %s, "+
		"RollupExitRoot: %s, GlobalExitRoot: %s, DestinationNetwork: %d, Metadata: %x, "+
		"IsMessage: %t, BlockTimestamp: %d, Type: %s}",
		c.BlockNum, c.BlockPos, c.TxHash.String(), globalIndexStr,
		c.OriginNetwork, c.OriginAddress.String(), c.DestinationAddress.String(), amountStr,
		c.ProofLocalExitRoot.String(), c.ProofRollupExitRoot.String(), c.MainnetExitRoot.String(),
		c.RollupExitRoot.String(), c.GlobalExitRoot.String(), c.DestinationNetwork, c.Metadata,
		c.IsMessage, c.BlockTimestamp, c.Type)
}

// decodeEtrogCalldata decodes claim calldata for Etrog fork
func (c *Claim) decodeEtrogCalldata(data []any) (bool, error) {
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

	rawLERProof, ok := data[0].([types.DefaultHeight][common.HashLength]byte)
	if !ok {
		return false, fmt.Errorf("unexpected type for rawLERProof, expected [32][32]byte got '%T'", data[0])
	}

	rawRERProof, ok := data[1].([types.DefaultHeight][common.HashLength]byte)
	if !ok {
		return false, fmt.Errorf("unexpected type for rawRERProof, expected [32][32]byte got '%T'", data[1])
	}

	c.ProofLocalExitRoot = types.NewProof(rawLERProof)
	c.ProofRollupExitRoot = types.NewProof(rawRERProof)

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

	return true, nil
}

// decodePreEtrogCalldata decodes the claim calldata for pre-Etrog forks
func (c *Claim) decodePreEtrogCalldata(data []any) (bool, error) {
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

	rawLERProof, ok := data[0].([types.DefaultHeight][common.HashLength]byte)
	if !ok {
		return false, fmt.Errorf("unexpected type for proofLERBytes, expected [32][32]byte got '%T'", data[0])
	}

	c.ProofLocalExitRoot = types.NewProof(rawLERProof)

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

	return true, nil
}

type InvalidClaim struct {
	// claim struct fields
	BlockNum            uint64         `meddler:"block_num"`
	BlockPos            uint64         `meddler:"block_pos"`
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
	// additional fields
	Reason    string `meddler:"reason"`
	CreatedAt uint64 `meddler:"created_at"`
}

// NewInvalidClaim creates a new InvalidClaim from a Claim and a reason
func NewInvalidClaim(c *Claim, reason string) *InvalidClaim {
	return &InvalidClaim{
		BlockNum:            c.BlockNum,
		BlockPos:            c.BlockPos,
		TxHash:              c.TxHash,
		GlobalIndex:         c.GlobalIndex,
		OriginNetwork:       c.OriginNetwork,
		OriginAddress:       c.OriginAddress,
		DestinationAddress:  c.DestinationAddress,
		Amount:              c.Amount,
		ProofLocalExitRoot:  c.ProofLocalExitRoot,
		ProofRollupExitRoot: c.ProofRollupExitRoot,
		MainnetExitRoot:     c.MainnetExitRoot,
		RollupExitRoot:      c.RollupExitRoot,
		GlobalExitRoot:      c.GlobalExitRoot,
		DestinationNetwork:  c.DestinationNetwork,
		Metadata:            c.Metadata,
		IsMessage:           c.IsMessage,
		BlockTimestamp:      c.BlockTimestamp,
		Reason:              reason,
		CreatedAt:           uint64(time.Now().UTC().Unix()),
	}
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

// UnsetClaim representation of an UpdatedUnsetGlobalIndexHashChain event,
// that is emitted by the bridge contract when a claim is unset.
type UnsetClaim struct {
	BlockNum                  uint64      `meddler:"block_num"`
	BlockPos                  uint64      `meddler:"block_pos"`
	TxHash                    common.Hash `meddler:"tx_hash,hash"`
	GlobalIndex               *big.Int    `meddler:"global_index,bigint"`
	UnsetGlobalIndexHashChain common.Hash `meddler:"unset_global_index_hash_chain,hash"`
	CreatedAt                 uint64      `meddler:"created_at"`
}

func (u *UnsetClaim) String() string {
	globalIndexStr := nilStr
	if u.GlobalIndex != nil {
		globalIndexStr = u.GlobalIndex.String()
	}

	return fmt.Sprintf("UnsetClaim{BlockNum: %d, BlockPos: %d, TxHash: %s, "+
		"GlobalIndex: %s, UnsetGlobalIndexHashChain: %s, CreatedAt: %d}",
		u.BlockNum, u.BlockPos, u.TxHash.String(),
		globalIndexStr, u.UnsetGlobalIndexHashChain.String(), u.CreatedAt)
}

// SetClaim representation of a SetClaim event,
// that is emitted by the bridge contract when a claim is set.
type SetClaim struct {
	BlockNum    uint64      `meddler:"block_num"`
	BlockPos    uint64      `meddler:"block_pos"`
	TxHash      common.Hash `meddler:"tx_hash,hash"`
	GlobalIndex *big.Int    `meddler:"global_index,bigint"`
	CreatedAt   uint64      `meddler:"created_at"`
}

func (s *SetClaim) String() string {
	globalIndexStr := nilStr
	if s.GlobalIndex != nil {
		globalIndexStr = s.GlobalIndex.String()
	}
	return fmt.Sprintf("SetClaim{BlockNum: %d, BlockPos: %d, TxHash: %s, "+
		"GlobalIndex: %s, CreatedAt: %d}",
		s.BlockNum, s.BlockPos, s.TxHash.String(),
		globalIndexStr, s.CreatedAt)
}

// Event combination of bridge, claim, token mapping and legacy token migration events
type Event struct {
	Bridge               *Bridge
	Claim                *Claim
	TokenMapping         *TokenMapping
	LegacyTokenMigration *LegacyTokenMigration
	RemoveLegacyToken    *RemoveLegacyToken
	UnsetClaim           *UnsetClaim
	SetClaim             *SetClaim
}

func (e Event) String() string {
	parts := []string{}
	if e.Bridge != nil {
		parts = append(parts, e.Bridge.String())
	}
	if e.Claim != nil {
		parts = append(parts, e.Claim.String())
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
	if e.UnsetClaim != nil {
		parts = append(parts, e.UnsetClaim.String())
	}
	if e.SetClaim != nil {
		parts = append(parts, e.SetClaim.String())
	}
	return "Event{" + strings.Join(parts, ", ") + "}"
}

// BridgeSyncRuntimeData contains runtime environment data used for database compatibility checks.
// It includes chain ID, contract addresses, and database version information.
type BridgeSyncRuntimeData struct {
	// This fields are coming from legacy sync.RuntimeData
	ChainID   uint64
	Addresses []common.Address
	// DBVersion tracks the database schema version for compatibility validation
	DBVersion *int
}

func (b BridgeSyncRuntimeData) String() string {
	res := fmt.Sprintf("ChainID: %d, Addresses: ", b.ChainID)
	for _, addr := range b.Addresses {
		res += addr.String() + ", "
	}
	if b.DBVersion != nil {
		res += fmt.Sprintf("DBVersion: %d", *b.DBVersion)
	}
	return res
}
func (b BridgeSyncRuntimeData) IsCompatible(storage BridgeSyncRuntimeData) error {
	tmp := sync.RuntimeData{
		ChainID:   b.ChainID,
		Addresses: b.Addresses,
	}
	if err := tmp.IsCompatible(sync.RuntimeData{ChainID: storage.ChainID, Addresses: storage.Addresses}); err != nil {
		return err
	}
	if storage.DBVersion == nil || *storage.DBVersion != *b.DBVersion {
		return fmt.Errorf("database schema version mismatch (current: %v, stored: %v). "+
			"Drop BridgeL1Sync and BridgeL2Sync databases and restart",
			b.DBVersion, storage.DBVersion)
	}
	return nil
}

type BridgeQuerier interface {
	GetClaimsPaged(ctx context.Context, pageNumber, pageSize uint32,
		networkIDs []uint32, globalIndex *big.Int) ([]*Claim, int, error)
}

var _ BridgeQuerier = (*processor)(nil)

type processor struct {
	syncerID       string
	db             *sql.DB
	exitTree       *tree.AppendOnlyTree
	log            *log.Logger
	mu             mutex.RWMutex
	halted         bool
	haltedReason   string
	dbQueryTimeout time.Duration
	compatibility.CompatibilityDataStorager[BridgeSyncRuntimeData]
}

func newProcessor(
	dbPath string,
	syncerID string,
	logger *log.Logger,
	dbQueryTimeout time.Duration,
) (*processor, error) {
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
		syncerID:       syncerID,
		db:             database,
		exitTree:       exitTree,
		log:            logger,
		dbQueryTimeout: dbQueryTimeout,
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

func (p *processor) GetClaims(ctx context.Context, fromBlock, toBlock uint64) ([]Claim, error) {
	// SQL query with compaction logic implementing three cases:
	// Case 1: If unset_claim exists for a global_index, return all claims in range uncompacted
	// Case 2: If no unset_claim exists and globally oldest is in range, return compacted claim
	// Case 3: If globally oldest is outside range and no unset_claim exists, return nothing
	query := fmt.Sprintf(`
	WITH all_claims_ranked AS (
		SELECT 
			*,
			ROW_NUMBER() OVER (PARTITION BY global_index ORDER BY block_num ASC, block_pos ASC) AS rn_oldest_global,
			ROW_NUMBER() OVER (PARTITION BY global_index ORDER BY block_num DESC, block_pos DESC) AS rn_newest_global
		FROM claim
	),
	claims_in_range AS (
		SELECT *
		FROM all_claims_ranked
		WHERE block_num >= $1 AND block_num <= $2
	),
	claims_with_unset AS (
		-- Case 1: Return all claims in range if unset_claim exists (no compaction)
		SELECT 
			c.%s
		FROM claims_in_range c
		WHERE EXISTS (
			SELECT 1 FROM unset_claim uc 
			WHERE uc.global_index = c.global_index
		)
	),
	compactable_claims AS (
		-- Case 2 & 3: Handle claims without unset_claim
		SELECT 
		%s
		FROM claims_in_range o
		JOIN claims_in_range n ON o.global_index = n.global_index AND n.rn_newest_global = 1
		WHERE o.rn_oldest_global = 1  -- Globally oldest claim must be in range
		AND NOT EXISTS (
			SELECT 1 FROM unset_claim uc 
			WHERE uc.global_index = o.global_index
		)
	)
	SELECT * FROM claims_with_unset
	UNION ALL
	SELECT * FROM compactable_claims
	ORDER BY block_num ASC, block_pos ASC;
`, claimColumnsSQL, compactedClaimsSelectSQL)

	rows, err := p.queryBlockRange(ctx, p.db, fromBlock, toBlock, query)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			p.log.Debugf("no claims were found for block range [%d..%d]", fromBlock, toBlock)
			return []Claim{}, nil
		}
		p.log.Errorf("GetClaims: queryBlockRange failed for block range [%d..%d]: %v", fromBlock, toBlock, err)
		return nil, err
	}

	defer func() {
		if cerr := rows.Close(); cerr != nil {
			p.log.Errorf("error closing rows: %v", cerr)
		}
	}()

	claimPtrs := []*Claim{}
	if err = meddler.ScanAll(rows, &claimPtrs); err != nil {
		p.log.Errorf("GetClaims: meddler.ScanAll failed for block range [%d..%d]: %v", fromBlock, toBlock, err)
		return nil, err
	}
	claimsIface := db.SlicePtrsToSlice(claimPtrs)
	claims, ok := claimsIface.([]Claim)
	if !ok {
		p.log.Errorf("GetClaims: failed to convert from []*Claim to []Claim for block range [%d..%d]", fromBlock, toBlock)
		return nil, errFailToConvertClaims
	}
	return claims, nil
}

func (p *processor) GetClaimsByGlobalIndex(ctx context.Context, globalIndex *big.Int) ([]Claim, error) {
	if globalIndex == nil {
		return nil, fmt.Errorf("global index parameter cannot be nil")
	}

	// SQL query with compaction logic implementing three cases:
	// Case 1: If unset_claim exists for the global_index, return all claims uncompacted
	// Case 2: If no unset_claim exists, return compacted claim (oldest metadata + newest proofs)
	// Case 3: Same as case 2 (all claims for this global_index are considered "in range")
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
		-- Case 1: Return all claims if unset_claim exists (no compaction)
		SELECT 
			c.%s
		FROM all_claims_for_index c
		WHERE EXISTS (
			SELECT 1 FROM unset_claim uc 
			WHERE uc.global_index = $1
		)
	),
	compactable_claims AS (
		-- Case 2: Handle claims without unset_claim (compact)
		SELECT 
		%s
		FROM all_claims_for_index o
		JOIN all_claims_for_index n ON n.rn_newest = 1
		WHERE o.rn_oldest = 1
		AND NOT EXISTS (
			SELECT 1 FROM unset_claim uc 
			WHERE uc.global_index = $1
		)
	)
	SELECT * FROM claims_with_unset
	UNION ALL
	SELECT * FROM compactable_claims
	ORDER BY block_num ASC, block_pos ASC;
`, claimColumnsSQL, compactedClaimsSelectSQL)

	// Create a context with database timeout
	dbCtx, cancel := p.withDatabaseTimeout(ctx)
	defer cancel()

	rows, err := p.db.QueryContext(dbCtx, query, globalIndex.String())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			p.log.Debugf("no claims were found for global index: %s", globalIndex.String())
			return []Claim{}, nil
		}
		p.log.Errorf("GetClaimsByGlobalIndex: query failed for global index %s: %v", globalIndex.String(), err)
		return nil, fmt.Errorf("failed to query claims by global index: %s: %w", globalIndex.String(), err)
	}

	defer func() {
		if cerr := rows.Close(); cerr != nil {
			p.log.Errorf("error closing rows: %v", cerr)
		}
	}()

	claimPtrs := []*Claim{}
	if err = meddler.ScanAll(rows, &claimPtrs); err != nil {
		p.log.Errorf("GetClaimsByGlobalIndex: meddler.ScanAll failed for global index %s: %v", globalIndex.String(), err)
		return nil, fmt.Errorf("failed to scan claims for global index: %s: %w", globalIndex.String(), err)
	}

	claimsIface := db.SlicePtrsToSlice(claimPtrs)
	claims, ok := claimsIface.([]Claim)
	if !ok {
		p.log.Errorf("GetClaimsByGlobalIndex: failed to convert from []*Claim to []Claim for global index: %s",
			globalIndex.String())
		return nil, errFailToConvertClaims
	}

	return claims, nil
}

func (p *processor) GetBridgesPaged(
	ctx context.Context, pageNumber, pageSize uint32,
	depositCount *uint64, networkIDs []uint32, fromAddress string,
) ([]*Bridge, int, error) {
	whereClause := p.buildBridgesFilterClause(depositCount, networkIDs, fromAddress)
	orderByClause := "deposit_count DESC"
	bridgesCount, err := p.GetTotalNumberOfRecords(ctx, bridgeTableName, whereClause)
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

	rows, err := p.queryPaged(ctx, p.db, offset, pageSize, bridgeTableName, orderByClause, whereClause)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			p.log.Debugf("no bridges were found for provided parameters (pageNumber=%d, pageSize=%d, where clause=%s)",
				pageNumber, pageSize, whereClause)
			return nil, bridgesCount, nil
		}
		p.log.Errorf("GetBridgesPaged: queryPaged failed for pageNumber=%d, pageSize=%d: %v", pageNumber, pageSize, err)
		return nil, 0, err
	}

	defer func() {
		if cerr := rows.Close(); cerr != nil {
			p.log.Errorf("error closing rows: %v", cerr)
		}
	}()

	bridges := []*Bridge{}
	if err = meddler.ScanAll(rows, &bridges); err != nil {
		p.log.Errorf("GetBridgesPaged: meddler.ScanAll failed for pageNumber=%d, pageSize=%d: %v", pageNumber, pageSize, err)
		return nil, 0, err
	}

	return bridges, bridgesCount, nil
}

// buildBridgesFilterClause builds the WHERE clause for the bridges table
// based on the provided depositCount, networkIDs, fromAddress and globalIndex
func (p *processor) buildBridgesFilterClause(depositCount *uint64, networkIDs []uint32, fromAddress string) string {
	const clauseCapacity = 3
	clauses := make([]string, 0, clauseCapacity)
	if depositCount != nil {
		clauses = append(clauses, fmt.Sprintf("deposit_count = %d", *depositCount))
	}

	if len(networkIDs) > 0 {
		clauses = append(clauses, buildNetworkIDsFilter(networkIDs, "destination_network"))
	}

	if fromAddress != "" && common.IsHexAddress(fromAddress) {
		clauses = append(clauses, fmt.Sprintf("UPPER(from_address) LIKE '%s'", fromAddress))
	}

	if len(clauses) > 0 {
		return " WHERE " + strings.Join(clauses, " AND ")
	}
	return ""
}

func (p *processor) GetClaimsPaged(
	ctx context.Context, pageNumber, pageSize uint32,
	networkIDs []uint32, globalIndex *big.Int,
) ([]*Claim, int, error) {
	whereClause := p.buildClaimsFilterClause(networkIDs, globalIndex)
	claimsCount, err := p.GetTotalNumberOfRecords(ctx, claimTableName, whereClause)
	if err != nil {
		return nil, 0, err
	}

	if claimsCount == 0 {
		return []*Claim{}, 0, nil
	}

	offset, err := p.calculateOffset(pageNumber, pageSize, claimsCount, "claims")
	if err != nil {
		return nil, 0, err
	}

	// Create a context with database timeout
	dbCtx, cancel := p.withDatabaseTimeout(ctx)
	defer cancel()

	// Pagination query with compaction logic implementing three cases:
	// Case 1: If unset_claim exists for a global_index, return all claims on page uncompacted
	// Case 2: If no unset_claim exists and globally oldest is on page, return compacted claim
	// Case 3: If globally oldest is outside page and no unset_claim exists, exclude from results
	//
	// This query:
	// - Gets claims for the requested page (DESC order: newest first)
	// - Ranks all claims globally by global_index to find oldest and newest
	// - For claims with unset_claim: returns all instances on the page uncompacted
	// - For claims without unset_claim: only returns compacted version if newest is on page
	//nolint:gosec
	query := fmt.Sprintf(`
		WITH page_claims AS (
			SELECT *
			FROM claim
			%s
			ORDER BY block_num DESC, block_pos DESC
			LIMIT $1 OFFSET $2
		),
		all_claims_ranked AS (
			SELECT 
				*,
				ROW_NUMBER() OVER (PARTITION BY global_index ORDER BY block_num ASC, block_pos ASC) AS rn_oldest_global,
				ROW_NUMBER() OVER (PARTITION BY global_index ORDER BY block_num DESC, block_pos DESC) AS rn_newest_global
			FROM claim
			%s
		),
		claims_with_unset_on_page AS (
			-- Case 1: Return all claims on page if unset_claim exists (no compaction)
			SELECT 
				pc.%s
			FROM page_claims pc
			WHERE EXISTS (
				SELECT 1 FROM unset_claim uc 
				WHERE uc.global_index = pc.global_index
			)
		),
		newest_on_page AS (
			SELECT DISTINCT pc.global_index
			FROM page_claims pc
			JOIN all_claims_ranked acr ON pc.global_index = acr.global_index AND acr.rn_newest_global = 1
			WHERE pc.block_num = acr.block_num AND pc.block_pos = acr.block_pos
			AND NOT EXISTS (
				SELECT 1 FROM unset_claim uc 
				WHERE uc.global_index = pc.global_index
			)
		),
		compactable_claims AS (
			-- Case 2 & 3: Handle claims without unset_claim
			SELECT 
			%s
			FROM all_claims_ranked o
			JOIN all_claims_ranked n ON o.global_index = n.global_index AND n.rn_newest_global = 1
			WHERE o.rn_oldest_global = 1  -- Globally oldest claim
			AND o.global_index IN (SELECT global_index FROM newest_on_page)
		)
		SELECT * FROM claims_with_unset_on_page
		UNION ALL
		SELECT * FROM compactable_claims
		ORDER BY block_num DESC, block_pos DESC;
	`, whereClause, whereClause, claimColumnsSQL, compactedClaimsSelectSQL)

	rows, err := p.db.QueryContext(dbCtx, query, pageSize, offset)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			p.log.Debugf("no claims were found for provided parameters (pageNumber=%d, pageSize=%d)",
				pageNumber, pageSize)
			return nil, claimsCount, nil
		}
		p.log.Errorf("GetClaimsPaged: queryPaged failed for pageNumber=%d, pageSize=%d: %v", pageNumber, pageSize, err)
		return nil, 0, err
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			p.log.Errorf("error closing rows: %v", cerr)
		}
	}()

	claims := []*Claim{}
	if err = meddler.ScanAll(rows, &claims); err != nil {
		p.log.Errorf("GetClaimsPaged: meddler.ScanAll failed for pageNumber=%d, pageSize=%d: %v", pageNumber, pageSize, err)
		return nil, 0, err
	}

	return claims, claimsCount, nil
}

// GetUnsetClaimsPaged returns a paginated list of unset claims
func (p *processor) GetUnsetClaimsPaged(
	ctx context.Context, pageNumber, pageSize uint32,
	globalIndex *big.Int,
) ([]*UnsetClaim, int, error) {
	whereClause := p.buildUnsetClaimsFilterClause(globalIndex)
	unclaimsCount, err := p.GetTotalNumberOfRecords(ctx, unsetClaimTableName, whereClause)
	if err != nil {
		return nil, 0, err
	}

	if unclaimsCount == 0 {
		return []*UnsetClaim{}, 0, nil
	}

	offset, err := p.calculateOffset(pageNumber, pageSize, unclaimsCount, unsetClaimTableName)
	if err != nil {
		return nil, 0, err
	}

	rows, err := p.queryPaged(ctx, p.db, offset, pageSize, unsetClaimTableName, orderByBlockDesc, whereClause)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			p.log.Debugf("no unset claims were found for provided parameters (pageNumber=%d, pageSize=%d)",
				pageNumber, pageSize)
			return nil, unclaimsCount, nil
		}
		p.log.Errorf("GetUnsetClaimsPaged: queryPaged failed for pageNumber=%d, pageSize=%d: %v", pageNumber, pageSize, err)
		return nil, 0, err
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			p.log.Errorf("error closing rows: %v", cerr)
		}
	}()

	unsetClaims := []*UnsetClaim{}
	if err = meddler.ScanAll(rows, &unsetClaims); err != nil {
		p.log.Errorf("GetUnsetClaimsPaged: meddler.ScanAll failed for pageNumber=%d, pageSize=%d: %v",
			pageNumber, pageSize, err)
		return nil, 0, err
	}

	return unsetClaims, unclaimsCount, nil
}

// buildUnsetClaimsFilterClause builds the WHERE clause for the unset_claim table
// based on the provided globalIndex
func (p *processor) buildUnsetClaimsFilterClause(globalIndex *big.Int) string {
	if globalIndex != nil {
		return " WHERE " + fmt.Sprintf("global_index = '%s'", globalIndex.String())
	}

	return ""
}

// buildClaimsFilterClause builds the WHERE clause for the claims table
// based on the provided networkIDs and globalIndex
func (p *processor) buildClaimsFilterClause(networkIDs []uint32, globalIndex *big.Int) string {
	const clauseCapacity = 2
	clauses := make([]string, 0, clauseCapacity)
	if len(networkIDs) > 0 {
		clauses = append(clauses, buildNetworkIDsFilter(networkIDs, "origin_network"))
	}

	if globalIndex != nil {
		clauses = append(clauses,
			fmt.Sprintf("global_index = '%s'", globalIndex.String()),
		)
	}

	if len(clauses) > 0 {
		return " WHERE " + strings.Join(clauses, " AND ")
	}
	return ""
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

// GetLastProcessedBlock returns the last processed block by the processor, including blocks
// that don't have events
func (p *processor) GetLastProcessedBlock(ctx context.Context) (uint64, error) {
	return p.getLastProcessedBlockWithTx(ctx, p.db)
}

func (p *processor) getLastProcessedBlockWithTx(ctx context.Context, tx dbtypes.Querier) (uint64, error) {
	var lastProcessedBlockNum uint64

	// Create a context with database timeout
	dbCtx, cancel := p.withDatabaseTimeout(ctx)
	defer cancel()

	row := tx.QueryRowContext(dbCtx, "SELECT num FROM block ORDER BY num DESC LIMIT 1;")
	err := row.Scan(&lastProcessedBlockNum)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return lastProcessedBlockNum, err
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

	blocksRes, err := tx.Exec(`DELETE FROM block WHERE num >= $1;`, firstReorgedBlock)
	if err != nil {
		p.log.Errorf("failed to delete blocks during reorg: %v", err)
		return err
	}
	rowsAffected, err := blocksRes.RowsAffected()
	if err != nil {
		p.log.Errorf("failed to get rows affected during reorg: %v", err)
		return err
	}

	if err = p.exitTree.Reorg(tx, firstReorgedBlock); err != nil {
		p.log.Errorf("failed to reorg exit tree: %v", err)
		return err
	}

	if err = tx.Commit(); err != nil {
		p.log.Errorf("failed to commit reorg transaction: %v", err)
		return err
	}

	shouldRollback = false

	if rowsAffected > 0 {
		p.unhalt()
	}

	p.log.Infof("reorged to block %d, %d rows affected", firstReorgedBlock, rowsAffected)

	return nil
}

// ProcessBlock process the events of the block to build the exit tree
// and updates the last processed block (can be called without events for that purpose)
func (p *processor) ProcessBlock(ctx context.Context, block sync.Block) error {
	if p.isHalted() {
		p.log.Errorf("processor is halted due to: %s", p.haltedReason)
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

	for _, e := range block.Events {
		event, ok := e.(Event)
		if !ok {
			p.log.Errorf("failed to convert event to Event type in block %d", block.Num)
			return errors.New("failed to convert sync.Block.Event to Event")
		}

		if event.Bridge != nil {
			if _, err = p.exitTree.PutLeaf(tx, block.Num, event.Bridge.BlockPos, types.Leaf{
				Index: event.Bridge.DepositCount,
				Hash:  event.Bridge.Hash(),
			}); err != nil {
				if errors.Is(err, tree.ErrInvalidIndex) {
					p.halt(fmt.Sprintf("error adding leaf to the exit tree: %v", err))
				}
				return sync.ErrInconsistentState
			}
			if err = meddler.Insert(tx, bridgeTableName, event.Bridge); err != nil {
				p.log.Errorf("failed to insert bridge event at block %d: %v", block.Num, err)
				return err
			}
		}

		if event.Claim != nil {
			if err = meddler.Insert(tx, claimTableName, event.Claim); err != nil {
				p.log.Errorf("failed to insert claim event at block %d: %v", block.Num, err)
				return err
			}
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

		if event.UnsetClaim != nil {
			if err = meddler.Insert(tx, unsetClaimTableName, event.UnsetClaim); err != nil {
				p.log.Errorf("failed to insert unset claim event at block %d: %v", block.Num, err)
				return err
			}
		}

		if event.SetClaim != nil {
			if err = meddler.Insert(tx, setClaimTableName, event.SetClaim); err != nil {
				p.log.Errorf("failed to insert set claim event at block %d: %v", block.Num, err)
				return err
			}
		}
	}

	if err := tx.Commit(); err != nil {
		p.log.Errorf("failed to commit db transaction (block number %d): %v", block.Num, err)
		return err
	}
	shouldRollback = false

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

// buildNetworkIDsFilter builds SQL filter for the given network IDs
func buildNetworkIDsFilter(networkIDs []uint32, networkIDColumn string) string {
	placeholders := make([]string, len(networkIDs))
	for i, id := range networkIDs {
		placeholders[i] = fmt.Sprintf("%d", id)
	}
	return fmt.Sprintf("%s IN (%s)", networkIDColumn, strings.Join(placeholders, ", "))
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
	if offset >= uint32(recordsCount) {
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
	p.log.Errorf("processor is halted due to the following reason: %s", reason)
}

// unhalt sets the processor to a non-halted state, allowing it to process blocks again
func (p *processor) unhalt() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.halted = false
	p.haltedReason = ""
	p.log.Info("processor unhalted")
}

func (p *processor) withDatabaseTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, p.dbQueryTimeout)
}
