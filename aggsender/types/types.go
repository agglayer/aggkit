package types

import (
	"encoding/binary"
	"fmt"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/ethereum/go-ethereum/common"
)

var errNoCertificateHeader = fmt.Errorf("missing certificate header")

const NilStr = "nil"

type AggsenderMode string

const (
	PessimisticProofMode AggsenderMode = "PessimisticProof"
	AggchainProofMode    AggsenderMode = "AggchainProof"
)

// CertStatus holds the status of pending and in error certificates
type CertStatus struct {
	ExistPendingCerts   bool
	ExistNewInErrorCert bool
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
		return NilStr
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
		return NilStr
	}

	return fmt.Sprintf("Version: %s \n"+
		"Proof: %x \n"+
		"Vkey: %x",
		s.Version,
		s.Proof,
		s.Vkey,
	)
}

type CertificateHeader struct {
	Height                  uint64                          `meddler:"height"`
	RetryCount              int                             `meddler:"retry_count"`
	CertificateID           common.Hash                     `meddler:"certificate_id,hash"`
	PreviousLocalExitRoot   *common.Hash                    `meddler:"previous_local_exit_root,hash"`
	NewLocalExitRoot        common.Hash                     `meddler:"new_local_exit_root,hash"`
	FromBlock               uint64                          `meddler:"from_block"`
	ToBlock                 uint64                          `meddler:"to_block"`
	Status                  agglayertypes.CertificateStatus `meddler:"status"`
	CreatedAt               uint32                          `meddler:"created_at"`
	UpdatedAt               uint32                          `meddler:"updated_at"`
	FinalizedL1InfoTreeRoot *common.Hash                    `meddler:"finalized_l1_info_tree_root,hash"`
	L1InfoTreeLeafCount     uint32                          `meddler:"l1_info_tree_leaf_count"`
}

func (c *CertificateHeader) String() string {
	if c == nil {
		return NilStr
	}
	previousLocalExitRoot := NilStr
	if c.PreviousLocalExitRoot != nil {
		previousLocalExitRoot = c.PreviousLocalExitRoot.String()
	}
	finalizedL1InfoTreeRoot := NilStr
	if c.FinalizedL1InfoTreeRoot != nil {
		finalizedL1InfoTreeRoot = c.FinalizedL1InfoTreeRoot.String()
	}

	return fmt.Sprintf("aggsender.CertificateHeader: \n"+
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
		finalizedL1InfoTreeRoot,
	)
}

// ID returns a string with the unique identifier of the cerificate (height+certificateID)
func (c *CertificateHeader) ID() string {
	if c == nil {
		return NilStr
	}
	return fmt.Sprintf("%d/%s (retry %d)", c.Height, c.CertificateID.String(), c.RetryCount)
}

// StatusString returns the string representation of the status
func (c *CertificateHeader) StatusString() string {
	if c == nil {
		return "???"
	}
	return c.Status.String()
}

// IsClosed returns true if the certificate is closed (settled or inError)
func (c *CertificateHeader) IsClosed() bool {
	if c == nil {
		return false
	}
	return c.Status.IsClosed()
}

// ElapsedTimeSinceCreation returns the time elapsed since the certificate was created
func (c *CertificateHeader) ElapsedTimeSinceCreation() time.Duration {
	if c == nil {
		return 0
	}
	return time.Now().UTC().Sub(time.Unix(int64(c.CreatedAt), 0))
}

type Certificate struct {
	Header            *CertificateHeader
	SignedCertificate *string        `meddler:"signed_certificate"`
	AggchainProof     *AggchainProof `meddler:"aggchain_proof,aggchainproof"`
}

func (c *Certificate) String() string {
	if c == nil {
		return NilStr
	}

	aggchainProof := NilStr
	if c.AggchainProof != nil {
		aggchainProof = c.AggchainProof.String()
	}

	return fmt.Sprintf("aggsender.Certificate: \n"+
		"Header: %s \n"+
		"AggchainProof: %s \n",
		c.Header.String(),
		aggchainProof,
	)
}

func (c *Certificate) ToCertificateInfo() (*CertificateInfo, error) {
	if c.Header == nil {
		return nil, errNoCertificateHeader
	}

	return &CertificateInfo{
		CertificateID:           c.Header.CertificateID,
		Height:                  c.Header.Height,
		RetryCount:              c.Header.RetryCount,
		PreviousLocalExitRoot:   c.Header.PreviousLocalExitRoot,
		NewLocalExitRoot:        c.Header.NewLocalExitRoot,
		FromBlock:               c.Header.FromBlock,
		ToBlock:                 c.Header.ToBlock,
		Status:                  c.Header.Status,
		CreatedAt:               c.Header.CreatedAt,
		UpdatedAt:               c.Header.UpdatedAt,
		FinalizedL1InfoTreeRoot: c.Header.FinalizedL1InfoTreeRoot,
		L1InfoTreeLeafCount:     c.Header.L1InfoTreeLeafCount,
		SignedCertificate:       c.SignedCertificate,
		AggchainProof:           c.AggchainProof,
	}, nil
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
	SignedCertificate       *string                         `meddler:"signed_certificate"`
	AggchainProof           *AggchainProof                  `meddler:"aggchain_proof,aggchainproof"`
	FinalizedL1InfoTreeRoot *common.Hash                    `meddler:"finalized_l1_info_tree_root,hash"`
	L1InfoTreeLeafCount     uint32                          `meddler:"l1_info_tree_leaf_count"`
}

func (c *CertificateInfo) ToCertificate() *Certificate {
	return &Certificate{
		Header: &CertificateHeader{
			Height:                  c.Height,
			RetryCount:              c.RetryCount,
			CertificateID:           c.CertificateID,
			PreviousLocalExitRoot:   c.PreviousLocalExitRoot,
			NewLocalExitRoot:        c.NewLocalExitRoot,
			FromBlock:               c.FromBlock,
			ToBlock:                 c.ToBlock,
			Status:                  c.Status,
			CreatedAt:               c.CreatedAt,
			UpdatedAt:               c.UpdatedAt,
			FinalizedL1InfoTreeRoot: c.FinalizedL1InfoTreeRoot,
			L1InfoTreeLeafCount:     c.L1InfoTreeLeafCount,
		},
		SignedCertificate: c.SignedCertificate,
		AggchainProof:     c.AggchainProof,
	}
}

func (c *CertificateInfo) String() string {
	if c == nil {
		return NilStr
	}
	previousLocalExitRoot := NilStr
	if c.PreviousLocalExitRoot != nil {
		previousLocalExitRoot = c.PreviousLocalExitRoot.String()
	}
	finalizedL1InfoTreeRoot := NilStr
	if c.FinalizedL1InfoTreeRoot != nil {
		finalizedL1InfoTreeRoot = c.FinalizedL1InfoTreeRoot.String()
	}
	aggchainProof := NilStr
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
		return NilStr
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
