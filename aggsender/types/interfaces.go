package types

import (
	"context"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggoracle/chaingerreader"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/etherman"
	"github.com/agglayer/aggkit/l1infotreesync"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
)

// AggsenderFlow is an interface that defines the methods to manage the flow of the AggSender
// based on the different prover types
type AggsenderFlow interface {
	// CheckInitialStatus checks the initial status for the flow it's ok
	CheckInitialStatus(ctx context.Context) error
	// GetCertificateBuildParams returns the parameters to build a certificate
	GetCertificateBuildParams(ctx context.Context) (*CertificateBuildParams, error)
	// BuildCertificate builds a certificate based on the buildParams
	BuildCertificate(ctx context.Context,
		buildParams *CertificateBuildParams) (*agglayertypes.Certificate, error)
}

// L1InfoTreeSyncer is an interface defining functions that an L1InfoTreeSyncer should implement
type L1InfoTreeSyncer interface {
	GetInfoByGlobalExitRoot(globalExitRoot common.Hash) (*l1infotreesync.L1InfoTreeLeaf, error)
	GetL1InfoTreeMerkleProofFromIndexToRoot(
		ctx context.Context, index uint32, root common.Hash,
	) (treetypes.Proof, error)
	GetL1InfoTreeRootByIndex(ctx context.Context, index uint32) (treetypes.Root, error)
	GetProcessedBlockUntil(ctx context.Context, blockNumber uint64) (uint64, common.Hash, error)
	GetInfoByIndex(ctx context.Context, index uint32) (*l1infotreesync.L1InfoTreeLeaf, error)
	GetLatestInfoUntilBlock(ctx context.Context, blockNum uint64) (*l1infotreesync.L1InfoTreeLeaf, error)
}

// L2BridgeSyncer is an interface defining functions that an L2BridgeSyncer should implement
type L2BridgeSyncer interface {
	GetBlockByLER(ctx context.Context, ler common.Hash) (uint64, error)
	GetExitRootByIndex(ctx context.Context, index uint32) (treetypes.Root, error)
	GetBridges(ctx context.Context, fromBlock, toBlock uint64) ([]bridgesync.Bridge, error)
	GetClaims(ctx context.Context, fromBlock, toBlock uint64) ([]bridgesync.Claim, error)
	OriginNetwork() uint32
	BlockFinality() etherman.BlockNumberFinality
	GetLastProcessedBlock(ctx context.Context) (uint64, error)
}

// BridgeQuerier is an interface defining functions that an BridgeQuerier should implement
type BridgeQuerier interface {
	GetBridgesAndClaims(
		ctx context.Context,
		fromBlock, toBlock uint64,
		allowEmptyCert bool,
	) ([]bridgesync.Bridge, []bridgesync.Claim, error)
	GetExitRootByIndex(ctx context.Context, index uint32) (common.Hash, error)
	GetLastProcessedBlock(ctx context.Context) (uint64, error)
	OriginNetwork() uint32
}

// ChainGERReader is an interface defining functions that an ChainGERReader should implement
type ChainGERReader interface {
	GetInjectedGERsForRange(
		ctx context.Context,
		fromBlock, toBlock uint64) (map[common.Hash]chaingerreader.InjectedGER, error)
}

// L1InfoTreeDataQuerier is an interface defining functions that an L1InfoTreeDataQuerier should implement
// It is used to query data from the L1 Info tree
type L1InfoTreeDataQuerier interface {
	// GetLatestFinalizedL1InfoRoot returns the latest processed l1 info tree root
	// based on the latest finalized l1 block
	GetLatestFinalizedL1InfoRoot(ctx context.Context) (*treetypes.Root, *l1infotreesync.L1InfoTreeLeaf, error)

	// GetFinalizedL1InfoTreeData returns the L1 Info tree data for the last finalized processed block
	// l1InfoTreeData is:
	// - merkle proof of given l1 info tree leaf
	// - the leaf data of the highest index leaf on that block and root
	// - the root of the l1 info tree on that block
	GetFinalizedL1InfoTreeData(ctx context.Context,
	) (treetypes.Proof, *l1infotreesync.L1InfoTreeLeaf, *treetypes.Root, error)

	// GetProofForGER returns the L1 Info tree leaf and the merkle proof for the given GER
	GetProofForGER(ctx context.Context, ger, rootFromWhichToProve common.Hash) (
		*l1infotreesync.L1InfoTreeLeaf, treetypes.Proof, error)

	// CheckIfClaimsArePartOfFinalizedL1InfoTree checks if the claims are part of the finalized L1 Info tree
	CheckIfClaimsArePartOfFinalizedL1InfoTree(
		finalizedL1InfoTreeRoot *treetypes.Root, claims []bridgesync.Claim) error
}

// GERQuerier is an interface defining functions that an GERQuerier should implement
type GERQuerier interface {
	GetInjectedGERsProofs(
		ctx context.Context,
		finalizedL1InfoTreeRoot *treetypes.Root,
		fromBlock, toBlock uint64) (map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber, error)
}

// Logger is an interface that defines the methods to log messages
type Logger interface {
	Panicf(format string, args ...interface{})
	Fatalf(format string, args ...interface{})
	Info(args ...interface{})
	Infof(format string, args ...interface{})
	Error(args ...interface{})
	Errorf(format string, args ...interface{})
	Warn(args ...interface{})
	Warnf(format string, args ...interface{})
	Debug(args ...interface{})
	Debugf(format string, args ...interface{})
}

// CertificateStatusChecker is an interface defining functions that a CertificateStatusChecker should implement
type CertificateStatusChecker interface {
	CheckPendingCertificatesStatus(ctx context.Context) CertStatus
	CheckInitialStatus(
		ctx context.Context,
		delayBetweenRetries time.Duration,
		aggsenderStatus *AggsenderStatus)
}
