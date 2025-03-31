package types

import (
	"context"
	"encoding/binary"
	"fmt"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/etherman"
	"github.com/agglayer/aggkit/l1infotreesync"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

type AggsenderMode string

const (
	PessimisticProofMode AggsenderMode = "PessimisticProof"
	AggchainProofMode    AggsenderMode = "AggchainProof"
)

// AggsenderFlow is an interface that defines the methods to manage the flow of the AggSender
// based on the different prover types
type AggsenderFlow interface {
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
	GetBridgesPublished(ctx context.Context, fromBlock, toBlock uint64) ([]bridgesync.Bridge, error)
	GetClaims(ctx context.Context, fromBlock, toBlock uint64) ([]bridgesync.Claim, error)
	OriginNetwork() uint32
	BlockFinality() etherman.BlockNumberFinality
	GetLastProcessedBlock(ctx context.Context) (uint64, error)
}

// ChainGERReader is an interface defining functions that an ChainGERReader should implement
type ChainGERReader interface {
	GetInjectedGERsForRange(ctx context.Context, fromBlock, toBlock uint64) (map[uint64][]common.Hash, error)
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

// EthClient is an interface defining functions that an EthClient should implement
type EthClient interface {
	bind.ContractBackend
	ethereum.LogFilterer
	ethereum.BlockNumberReader
	ethereum.ChainReader
	bind.ContractBackend
}

// Logger is an interface that defines the methods to log messages
type Logger interface {
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

type AggchainProof struct {
	LastProvenBlock uint64
	EndBlock        uint64
	Proof           []byte
	CustomChainData []byte
	LocalExitRoot   common.Hash
}

type CertificateInfo struct {
	Height        uint64      `meddler:"height"`
	RetryCount    int         `meddler:"retry_count"`
	CertificateID common.Hash `meddler:"certificate_id,hash"`
	// PreviousLocalExitRoot if it's nil means no reported
	PreviousLocalExitRoot   *common.Hash                    `meddler:"previous_local_exit_root,hash"`
	NewLocalExitRoot        common.Hash                     `meddler:"new_local_exit_root,hash"`
	FromBlock               uint64                          `meddler:"from_block"`
	ToBlock                 uint64                          `meddler:"to_block"`
	Status                  agglayertypes.CertificateStatus `meddler:"status"`
	CreatedAt               uint32                          `meddler:"created_at"`
	UpdatedAt               uint32                          `meddler:"updated_at"`
	SignedCertificate       string                          `meddler:"signed_certificate"`
	AggchainProof           []byte                          `meddler:"aggchain_proof"`
	FinalizedL1InfoTreeRoot *common.Hash                    `meddler:"finalized_l1_info_tree_root,hash"`
}

func (c *CertificateInfo) String() string {
	if c == nil {
		//nolint:all
		return "nil"
	}
	previousLocalExitRoot := "nil"
	if c.PreviousLocalExitRoot != nil {
		previousLocalExitRoot = c.PreviousLocalExitRoot.String()
	}
	finalizedL1InfoTreeRoot := "nil"
	if c.FinalizedL1InfoTreeRoot != nil {
		finalizedL1InfoTreeRoot = c.FinalizedL1InfoTreeRoot.String()
	}

	return fmt.Sprintf("aggsender.CertificateInfo: "+
		"Height: %d "+
		"RetryCount: %d "+
		"CertificateID: %s "+
		"PreviousLocalExitRoot: %s "+
		"NewLocalExitRoot: %s "+
		"Status: %s "+
		"FromBlock: %d "+
		"ToBlock: %d "+
		"CreatedAt: %s "+
		"UpdatedAt: %s "+
		"AggchainProof: %s "+
		"FinalizedL1InfoTreeRoot: %s",
		c.Height,
		c.RetryCount,
		c.CertificateID.String(),
		previousLocalExitRoot,
		c.NewLocalExitRoot.String(),
		c.Status.String(),
		c.FromBlock,
		c.ToBlock,
		time.Unix(int64(c.CreatedAt), 0),
		time.Unix(int64(c.UpdatedAt), 0),
		c.AggchainProof,
		finalizedL1InfoTreeRoot,
	)
}

// ID returns a string with the unique identifier of the cerificate (height+certificateID)
func (c *CertificateInfo) ID() string {
	if c == nil {
		return "nil"
	}
	return fmt.Sprintf("%d/%s (retry %d)", c.Height, c.CertificateID.String(), c.RetryCount)
}

// StatusString returns the string representation of the status
func (c *CertificateInfo) StatusString() string {
	if c == nil {
		return "???"
	}
	return c.Status.String()
}

// IsClosed returns true if the certificate is closed (settled or inError)
func (c *CertificateInfo) IsClosed() bool {
	if c == nil {
		return false
	}
	return c.Status.IsClosed()
}

// ElapsedTimeSinceCreation returns the time elapsed since the certificate was created
func (c *CertificateInfo) ElapsedTimeSinceCreation() time.Duration {
	if c == nil {
		return 0
	}
	return time.Now().UTC().Sub(time.Unix(int64(c.CreatedAt), 0))
}

type CertificateMetadata struct {
	// ToBlock contains the pre v1 value stored in the metadata certificate field
	// is not stored in the hash post v1
	ToBlock uint64

	// FromBlock is the block number from which the certificate contains data
	FromBlock uint64

	// Offset is the number of blocks from the FromBlock that the certificate contains
	Offset uint32

	// CreatedAt is the timestamp when the certificate was created
	CreatedAt uint32

	// Version is the version of the metadata
	Version uint8
}

// NewCertificateMetadataFromHash returns a new CertificateMetadata from the given hash
func NewCertificateMetadata(fromBlock uint64, offset uint32, createdAt uint32) *CertificateMetadata {
	return &CertificateMetadata{
		FromBlock: fromBlock,
		Offset:    offset,
		CreatedAt: createdAt,
		Version:   1,
	}
}

// NewCertificateMetadataFromHash returns a new CertificateMetadata from the given hash
func NewCertificateMetadataFromHash(hash common.Hash) *CertificateMetadata {
	b := hash.Bytes()

	if b[0] < 1 {
		return &CertificateMetadata{
			ToBlock: hash.Big().Uint64(),
		}
	}

	return &CertificateMetadata{
		Version:   b[0],
		FromBlock: binary.BigEndian.Uint64(b[1:9]),
		Offset:    binary.BigEndian.Uint32(b[9:13]),
		CreatedAt: binary.BigEndian.Uint32(b[13:17]),
	}
}

// ToHash returns the hash of the metadata
func (c *CertificateMetadata) ToHash() common.Hash {
	b := make([]byte, common.HashLength) // 32-byte hash

	// Encode version
	b[0] = c.Version

	// Encode fromBlock
	binary.BigEndian.PutUint64(b[1:9], c.FromBlock)

	// Encode offset
	binary.BigEndian.PutUint32(b[9:13], c.Offset)

	// Encode createdAt
	binary.BigEndian.PutUint32(b[13:17], c.CreatedAt)

	// Last 8 bytes remain as zero padding

	return common.BytesToHash(b)
}
