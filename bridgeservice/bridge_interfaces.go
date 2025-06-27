package bridgeservice

import (
	"context"
	"database/sql"

	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/lastgersync"
	tree "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
)

type Bridger interface {
	GetProof(ctx context.Context, depositCount uint32, localExitRoot common.Hash) (tree.Proof, error)
	GetRootByLER(ctx context.Context, ler common.Hash) (*tree.Root, error)
	GetBridgesPaged(ctx context.Context, pageNumber, pageSize uint32,
		depositCount *uint64, networkIDs []uint32, fromAddress string) ([]*bridgesync.Bridge, int, error)
	GetTokenMappings(ctx context.Context, pageNumber, pageSize uint32) ([]*bridgesync.TokenMapping, int, error)
	GetLegacyTokenMigrations(ctx context.Context,
		pageNumber, pageSize uint32) ([]*bridgesync.LegacyTokenMigration, int, error)
	GetClaimsPaged(ctx context.Context, page, pageSize uint32,
		networkIDs []uint32, fromAddress string) ([]*bridgesync.Claim, int, error)
	GetLastReorgEvent(ctx context.Context) (*bridgesync.LastReorg, error)
	GetContractDepositCount(ctx context.Context) (uint32, error)
	// IsEmpty returns true if the bridge database is empty (no data has been processed)
	IsEmpty(ctx context.Context) (bool, error)
	GetDatabase() *sql.DB
}

type LastGERer interface {
	GetFirstGERAfterL1InfoTreeIndex(
		ctx context.Context, atOrAfterL1InfoTreeIndex uint32,
	) (lastgersync.GlobalExitRootInfo, error)
}

type L1InfoTreer interface {
	GetInfoByIndex(ctx context.Context, index uint32) (*l1infotreesync.L1InfoTreeLeaf, error)
	GetRollupExitTreeMerkleProof(ctx context.Context, networkID uint32, root common.Hash) (tree.Proof, error)
	GetLocalExitRoot(ctx context.Context, networkID uint32, rollupExitRoot common.Hash) (common.Hash, error)
	GetLastInfo() (*l1infotreesync.L1InfoTreeLeaf, error)
	GetFirstInfo() (*l1infotreesync.L1InfoTreeLeaf, error)
	GetFirstInfoAfterBlock(blockNum uint64) (*l1infotreesync.L1InfoTreeLeaf, error)
	GetLastVerifiedBatches(rollupID uint32) (*l1infotreesync.VerifyBatches, error)
	GetFirstVerifiedBatches(rollupID uint32) (*l1infotreesync.VerifyBatches, error)
	GetFirstVerifiedBatchesAfterBlock(rollupID uint32, blockNum uint64) (*l1infotreesync.VerifyBatches, error)
	GetFirstL1InfoWithRollupExitRoot(rollupExitRoot common.Hash) (*l1infotreesync.L1InfoTreeLeaf, error)
}
