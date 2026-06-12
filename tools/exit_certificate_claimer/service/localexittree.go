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
// tool (step-g-l2bridgesyncerlite.sqlite). It exposes the deposit count of each leaf (by leaf hash)
// and merkle proofs against the local exit tree built in that database.
type LocalExitTree struct {
	db           *sql.DB
	tree         treetypes.ReadTreer
	depositCount map[common.Hash]uint32
	// metadata maps a leaf hash to the raw bridge metadata of that exit. The claim needs the raw
	// bytes (the bridge contract hashes them itself to rebuild the leaf), whereas the certificate
	// only carries the already-hashed metadata.
	metadata map[common.Hash][]byte
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

	index := make(map[common.Hash]uint32, len(bridges))
	metadata := make(map[common.Hash][]byte, len(bridges))
	for i := range bridges {
		b := bridges[i]
		h := b.Hash()
		index[h] = b.DepositCount
		metadata[h] = b.Metadata
	}

	// Open a dedicated connection for the tree. The lite syncer uses an empty tree prefix.
	database, err := db.NewSQLiteDB(dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening local exit tree DB at %q: %w", dbPath, err)
	}

	return &LocalExitTree{
		db:           database,
		tree:         tree.NewAppendOnlyTree(database, ""),
		depositCount: index,
		metadata:     metadata,
	}, nil
}

// DepositCount returns the exit-tree leaf index (deposit count) for a given leaf hash.
func (l *LocalExitTree) DepositCount(leafHash common.Hash) (uint32, bool) {
	dc, ok := l.depositCount[leafHash]
	return dc, ok
}

// Metadata returns the raw bridge metadata for a given leaf hash, as recorded on-chain in the
// BridgeEvent. The claim needs these raw bytes: the bridge contract hashes them itself to rebuild
// the exit leaf, so passing the certificate's already-hashed metadata would double-hash and fail
// the SMT proof.
func (l *LocalExitTree) Metadata(leafHash common.Hash) ([]byte, bool) {
	m, ok := l.metadata[leafHash]
	return m, ok
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
