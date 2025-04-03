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

const nilStr = "nil"

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
	LastProvenBlock uint64            `json:"last_proven_block"`
	EndBlock        uint64            `json:"end_block"`
	CustomChainData []byte            `json:"custom_chain_data,omitempty"`
	LocalExitRoot   common.Hash       `json:"local_exit_root"`
	AggchainParams  common.Hash       `json:"aggchain_params"`
	Context         map[string][]byte `json:"context,omitempty"`
	SP1StarkProof   *SP1StarkProof    `json:"sp1_stark_proof,omitempty"`
}

func (a *AggchainProof) String() string {
	if a == nil {
		return nilStr
	}

	return fmt.Sprintf("LastProvenBlock: %d \n"+
		"EndBlock: %d \n"+
		"CustomChainData: %x \n"+
		"LocalExitRoot: %s \n"+
		"AggchainParams: %s \n"+
		"Context: %v \n"+
		"SP1StarkProof: %v \n",
		a.LastProvenBlock,
		a.EndBlock,
		a.CustomChainData,
		a.LocalExitRoot.String(),
		a.AggchainParams.String(),
		a.Context,
		a.SP1StarkProof.String(),
	)
}

type SP1StarkProof struct {
	// SP1 Version
	Version string `json:"version,omitempty"`
	// SP1 stark proof.
	Proof []byte `json:"proof,omitempty"`
	// SP1 stark proof verification key.
	Vkey []byte `json:"vkey,omitempty"`
}

func (s *SP1StarkProof) String() string {
	if s == nil {
		return nilStr
	}

	return fmt.Sprintf("Version: %s \n"+
		"Proof: %x \n"+
		"Vkey: %x",
		s.Version,
		s.Proof,
		s.Vkey,
	)
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
	AggchainProof           *AggchainProof                  `meddler:"aggchain_proof,aggchainproof"`
	FinalizedL1InfoTreeRoot *common.Hash                    `meddler:"finalized_l1_info_tree_root,hash"`
}

func (c *CertificateInfo) String() string {
	if c == nil {
		return nilStr
	}
	previousLocalExitRoot := nilStr
	if c.PreviousLocalExitRoot != nil {
		previousLocalExitRoot = c.PreviousLocalExitRoot.String()
	}
	finalizedL1InfoTreeRoot := nilStr
	if c.FinalizedL1InfoTreeRoot != nil {
		finalizedL1InfoTreeRoot = c.FinalizedL1InfoTreeRoot.String()
	}
	aggchainProof := nilStr
	if c.AggchainProof != nil {
		aggchainProof = c.AggchainProof.String()
	}

	return fmt.Sprintf("aggsender.CertificateInfo: \n"+
		"Height: %d \n"+
		"RetryCount: %d \n"+
		"CertificateID: %s \n"+
		"PreviousLocalExitRoot: %s \n"+
		"NewLocalExitRoot: %s \n"+
		"Status: %s \n"+
		"FromBlock: %d \n"+
		"ToBlock: %d \n"+
		"CreatedAt: %s \n"+
		"UpdatedAt: %s \n"+
		"AggchainProof: %s \n"+
		"FinalizedL1InfoTreeRoot: %s \n",
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
		aggchainProof,
		finalizedL1InfoTreeRoot,
	)
}

// ID returns a string with the unique identifier of the cerificate (height+certificateID)
func (c *CertificateInfo) ID() string {
	if c == nil {
		return nilStr
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
