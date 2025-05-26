package types

import (
	"fmt"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/ethereum/go-ethereum/common"
)

const NilStr = "nil"

type AggsenderMode string

const (
	PessimisticProofMode AggsenderMode = "PessimisticProof"
	AggchainProofMode    AggsenderMode = "AggchainProof"
)

type CertificateType string
type CertificateTypeInt uint8

const (
	CertificateTypeUnknown CertificateType = ""
	CertificateTypePP      CertificateType = "pp"
	CertificateTypeFEP     CertificateType = "fep"

	CertificateTypeUnknownInt CertificateTypeInt = 0
	CertificateTypePPInt      CertificateTypeInt = 1
	CertificateTypeFEPInt     CertificateTypeInt = 2
)

func (c CertificateType) String() string {
	return string(c)
}

func (ct CertificateType) ToInt() uint8 {
	switch ct {
	case CertificateTypeFEP:
		return uint8(CertificateTypeFEPInt)
	case CertificateTypePP:
		return uint8(CertificateTypePPInt)
	default:
		return uint8(CertificateTypeUnknownInt)
	}
}
func NewCertificateTypeFromInt(i uint8) CertificateType {
	switch i {
	case uint8(CertificateTypePPInt):
		return CertificateTypePP
	case uint8(CertificateTypeFEPInt):
		return CertificateTypeFEP
	default:
		return CertificateTypeUnknown
	}
}

type CertificateSource string

const (
	CertificateSourceAggLayer CertificateSource = "agglayer"
	CertificateSourceLocal    CertificateSource = "Local"
	CertificateSourceUnknown  CertificateSource = ""
)

func (c CertificateSource) String() string {
	return string(c)
}

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
	// CertType must be private but there are a lot of code that create CertificateInfo directly
	// so I add a GetCertType() that is not idiomatic but helps to determine the kind of certificate
	CertType CertificateType `meddler:"cert_type"`
	// This is the origin of this data, it can be from the AggLayer or from the local sender
	CertSource CertificateSource `meddler:"cert_source"`
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
		"Type: %s \n"+
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
		"FinalizedL1InfoTreeRoot: %s \n"+
		"Source: %s \n",
		c.CertType.String(),
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
		c.CertSource.String(),
	)
}

// ID returns a string with the unique identifier of the cerificate (height+certificateID)
func (c *CertificateHeader) ID() string {
	if c == nil {
		return NilStr
	}
	return fmt.Sprintf("%d/%s (retry: %d, type: %s)", c.Height, c.CertificateID.String(), c.RetryCount, c.CertSource.String())
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

func (c *Certificate) GetCertType() CertificateType {
	if c == nil {
		return CertificateTypeUnknown
	}
	if c.Header.CertType == CertificateTypeUnknown {
		if c.AggchainProof != nil {
			return CertificateTypeFEP
		} else {
			return CertificateTypePP
		}
	}
	return c.Header.CertType
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
