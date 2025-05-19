package types

import (
	"database/sql/driver"
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

type CertificateType uint8

const (
	CertificateTypeUnknownStr string = ""
	CertificateTypePPStr      string = "pp"
	CertificateTypeFEPStr     string = "fep"

	CertificateTypeUnknown CertificateType = 0
	CertificateTypePP      CertificateType = 1
	CertificateTypeFEP     CertificateType = 2
)

func (c CertificateType) String() string {
	switch c {
	case CertificateTypeFEP:
		return CertificateTypeFEPStr
	case CertificateTypePP:
		return CertificateTypePPStr
	default:
		return CertificateTypeUnknownStr
	}
}

// meddler support for store as string
func (c CertificateType) Value() (driver.Value, error) {
	return c.String(), nil
}

// meddler support for store as string
func (c *CertificateType) Scan(value interface{}) error {
	str, ok := value.(string)
	if !ok {
		return fmt.Errorf("CertificateType: expected string, got %T", value)
	}
	v, err := NewCertificateTypeFromStr(str)
	if err != nil {
		return fmt.Errorf("CertificateType.Scan(...): %w", err)
	}
	*c = v
	return nil
}

func (c CertificateType) ToInt() uint8 {
	return uint8(c)
}

func NewCertificateTypeFromInt(v uint8) CertificateType {
	return CertificateType(v)
}

func NewCertificateTypeFromStr(v string) (CertificateType, error) {
	switch v {
	case CertificateTypePPStr:
		return CertificateTypePP, nil
	case CertificateTypeFEPStr:
		return CertificateTypeFEP, nil
	case CertificateTypeUnknownStr:
		return CertificateTypeUnknown, nil
	default:
		return CertificateTypeUnknown, fmt.Errorf("unknown CertificateType: %s", v)
	}
}

type CertificateSource string

const (
	CertificateSourceAggLayer CertificateSource = "agglayer"
	CertificateSourceLocal    CertificateSource = "local"
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
	LastProvenBlock uint64
	EndBlock        uint64
	CustomChainData []byte
	LocalExitRoot   common.Hash
	AggchainParams  common.Hash
	Context         map[string][]byte
	SP1StarkProof   *SP1StarkProof
	Signature       []byte
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
	Version string
	// SP1 stark proof.
	Proof []byte
	// SP1 stark proof verification key.
	Vkey []byte
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
	return fmt.Sprintf("%d/%s (retry: %d, type: %s)",
		c.Height, c.CertificateID.String(), c.RetryCount, c.CertType.String())
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

func (c *Certificate) DetermineCertType(startL2Block uint64) CertificateType {
	if c == nil {
		return CertificateTypeUnknown
	}
	if c.Header.CertType == CertificateTypeUnknown {
		if c.AggchainProof != nil {
			return CertificateTypeFEP
		}
		// If the certificate is not set, we can determine the type based on the FromBlock
		if startL2Block == 0 {
			return CertificateTypeUnknown
		}
		// If fromBlock it's before startL2Block it's a valid determination that it is a PP
		if c.Header.FromBlock < startL2Block {
			return CertificateTypePP
		}
		// If not then we assume it's a FEP
		return CertificateTypeFEP
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
