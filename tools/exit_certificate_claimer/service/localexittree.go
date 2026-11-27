package claimer

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/tools/exit_certificate/bridgesyncerlite"
	"github.com/agglayer/aggkit/tree"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
)

// LocalExitTree wraps the L2 "lite" bridge sync SQLite database produced by the exit_certificate
// tool (step-g-l2bridgesyncerlite.sqlite). It exposes the leaves of the local exit tree by deposit
// count (their index in the tree) and merkle proofs against the tree built in that database.
type LocalExitTree struct {
	db   *sql.DB
	tree treetypes.ReadTreer
	// leaves holds every leaf of the tree, indexed by deposit count. Leaves are looked up by position,
	// never by hash: two bridges with the same content have the same leaf hash and different deposit
	// counts. Each leaf keeps the raw bridge metadata: the claim needs the raw bytes (the bridge
	// contract hashes them itself to rebuild the leaf), whereas the certificate only carries the hash.
	leaves []localLeaf
}

// localLeaf is what the claimer keeps of a leaf of the local exit tree.
type localLeaf struct {
	hash     common.Hash
	metadata []byte
}

// OpenLocalExitTree opens the local exit tree database in read-only fashion: it builds the
// leaf-hash → deposit-count index from the bridge table and prepares the append-only tree for
// proof generation. The DB must already contain a fully built tree (the exit_certificate Step G2
// output), otherwise proofs against NewLocalExitRoot will not resolve.
func OpenLocalExitTree(ctx context.Context, dbPath string, logger *log.Logger) (*LocalExitTree, error) {
	// Build the deposit-count index using the lite syncer in DB-only mode (no RPC), which knows
	// how to read the bridge table via meddler.
	syncer, err := bridgesyncerlite.New(ctx, bridgesyncerlite.Config{DBPath: dbPath}, logger)
	if err != nil {
		return nil, fmt.Errorf("opening lite bridge syncer at %q: %w", dbPath, err)
	}
	bridges, err := syncer.GetBridges(ctx)
	if err != nil {
		_ = syncer.Close()
		return nil, fmt.Errorf("reading bridges from %q: %w", dbPath, err)
	}
	if closeErr := syncer.Close(); closeErr != nil {
		return nil, fmt.Errorf("closing lite bridge syncer: %w", closeErr)
	}

	leaves, err := indexLeaves(bridges)
	if err != nil {
		return nil, fmt.Errorf("local exit tree DB %q: %w", dbPath, err)
	}

	// Open a dedicated connection for the tree. The lite syncer uses an empty tree prefix.
	database, err := db.NewSQLiteDB(dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening local exit tree DB at %q: %w", dbPath, err)
	}

	return &LocalExitTree{
		db:     database,
		tree:   tree.NewAppendOnlyTree(database, ""),
		leaves: leaves,
	}, nil
}

// indexLeaves orders the bridges by deposit count and requires them to be the contiguous sequence
// 0..n-1, so that a leaf's position is its deposit count.
func indexLeaves(bridges []bridgesyncerlite.BridgeLeaf) ([]localLeaf, error) {
	leaves := make([]localLeaf, len(bridges))
	seen := make([]bool, len(bridges))
	for i := range bridges {
		b := &bridges[i]
		if uint64(b.DepositCount) >= uint64(len(bridges)) {
			return nil, fmt.Errorf("deposit count %d out of range: the tree has %d leaves (gap in the bridge table)",
				b.DepositCount, len(bridges))
		}
		if seen[b.DepositCount] {
			return nil, fmt.Errorf("deposit count %d appears twice in the bridge table", b.DepositCount)
		}
		seen[b.DepositCount] = true
		leaves[b.DepositCount] = localLeaf{hash: b.Hash(), metadata: b.Metadata}
	}
	return leaves, nil
}

// LeafCount returns the number of leaves of the tree (the next deposit count).
func (l *LocalExitTree) LeafCount() uint32 {
	return uint32(len(l.leaves))
}

// LeafHash returns the hash of the leaf at depositCount.
func (l *LocalExitTree) LeafHash(depositCount uint32) (common.Hash, bool) {
	if uint64(depositCount) >= uint64(len(l.leaves)) {
		return common.Hash{}, false
	}
	return l.leaves[depositCount].hash, true
}

// Metadata returns the raw bridge metadata of the leaf at depositCount, as recorded on-chain in the
// BridgeEvent. The claim needs these raw bytes: the bridge contract hashes them itself to rebuild
// the exit leaf, so passing the certificate's already-hashed metadata would double-hash and fail
// the SMT proof.
func (l *LocalExitTree) Metadata(depositCount uint32) ([]byte, bool) {
	if uint64(depositCount) >= uint64(len(l.leaves)) {
		return nil, false
	}
	return l.leaves[depositCount].metadata, true
}

// Proof returns the merkle proof of the leaf at depositCount against the given local exit root.
func (l *LocalExitTree) Proof(
	ctx context.Context, depositCount uint32, localExitRoot common.Hash,
) (treetypes.Proof, error) {
	return l.tree.GetProof(ctx, depositCount, localExitRoot)
}

// Close releases the underlying database connection.
func (l *LocalExitTree) Close() error {
	if l.db == nil {
		return nil
	}
	return l.db.Close()
}
