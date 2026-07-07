package bridgeservice

import (
	"context"
	"math/big"

	"github.com/agglayer/aggkit/bridgesync"
	claimsynctype "github.com/agglayer/aggkit/claimsync/types"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/l2gersync"
	tree "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
)

type Bridger interface {
	GetProof(ctx context.Context, depositCount uint32, localExitRoot common.Hash) (tree.Proof, error)
	GetRootByLER(ctx context.Context, ler common.Hash) (*tree.Root, error)
	GetLastRoot(ctx context.Context) (*tree.Root, error)
	GetBridgesPaged(ctx context.Context, pageNumber, pageSize uint32,
		depositCount *uint64, networkIDs []uint32, fromAddress string) ([]*bridgesync.Bridge, int, error)
	GetBridgesInDepositRange(ctx context.Context, pageNumber, pageSize uint32,
		fromDepositCount *uint64, toDepositCount uint64,
		destinationNetworkIDs []uint32) ([]*bridgesync.Bridge, int, error)
	GetTokenMappings(ctx context.Context, pageNumber, pageSize uint32,
		originTokenAddress string) ([]*bridgesync.TokenMapping, int, error)
	GetLegacyTokenMigrations(ctx context.Context,
		pageNumber, pageSize uint32) ([]*bridgesync.LegacyTokenMigration, int, error)
	GetLastReorgEvent(ctx context.Context) (*bridgesync.LastReorg, error)
	GetContractDepositCount(ctx context.Context) (uint32, error)
	GetLastProcessedBlock(ctx context.Context) (uint64, bool, error)
	GetLatestNetworkBlock(ctx context.Context) (uint64, error)
	IsActive(ctx context.Context) bool
	GetBridgeByDepositCount(ctx context.Context, depositCount uint32) (*bridgesync.Bridge, error)
	GetBridgesByContent(ctx context.Context, leafType uint8, originAddress common.Address,
		destinationNetwork uint32, destinationAddress common.Address,
		amount *big.Int, metadata []byte) ([]*bridgesync.Bridge, error)
}

type Claimer interface {
	GetClaimsPaged(ctx context.Context, page, pageSize uint32,
		networkIDs []uint32, globalIndex *big.Int) ([]*claimsynctype.Claim, int, error)
	GetUnsetClaimsPaged(ctx context.Context, page, pageSize uint32,
		globalIndex *big.Int) ([]*claimsynctype.UnsetClaim, int, error)
	GetSetClaimsPaged(ctx context.Context, page, pageSize uint32,
		globalIndex *big.Int) ([]*claimsynctype.SetClaim, int, error)
	GetClaimsByGER(ctx context.Context, globalExitRoot common.Hash) ([]*claimsynctype.Claim, error)
}

type L2GERSyncer interface {
	GetFirstGERAfterL1InfoTreeIndex(
		ctx context.Context, atOrAfterL1InfoTreeIndex uint32,
	) (l2gersync.GlobalExitRootInfo, error)
	GetRemoveGEREvents(
		ctx context.Context, globalExitRoot *common.Hash, limit uint32,
	) ([]*l2gersync.RemoveGEREvent, error)
}

type L1InfoTreeSyncer interface {
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
