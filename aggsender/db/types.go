package db

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/ethereum/go-ethereum/common"
)

const (
	PrefixFilename = "@"
)

type CertificateKey struct {
	Height     uint64 `meddler:"height"`
	RetryCount int    `meddler:"retry_count"`
}

func (c CertificateKey) IsRetry() bool {
	return c.RetryCount > 0
}

func (c CertificateKey) String() string {
	return fmt.Sprintf("height: %d,retry: %d", c.Height, c.RetryCount)
}

// certificateInfo is a struct that holds the information of a certificate in the database
// It is used to store the information of a certificate in the database
// and to retrieve it when needed.
type certificateInfo struct {
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
	AggchainProof           *types.AggchainProof            `meddler:"aggchain_proof,aggchainproof"`
	FinalizedL1InfoTreeRoot *common.Hash                    `meddler:"finalized_l1_info_tree_root,hash"`
	L1InfoTreeLeafCount     uint32                          `meddler:"l1_info_tree_leaf_count"`
	CertType                types.CertificateType           `meddler:"cert_type"`
	CertSource              types.CertificateSource         `meddler:"cert_source"`
	ExtraData               string                          `meddler:"extra_data"`
}

// SetSignedCertificateFilename sets the SignedCertificate field to external file
func (c *certificateInfo) SetSignedCertificateFilename(filename string) {
	if c == nil {
		return
	}
	c.SignedCertificate = nil
	if filename != "" {
		c.SignedCertificate = new(string)
		*c.SignedCertificate = PrefixFilename + filename
	}
}

// SignedCertificateFilename returns the filename if SignedCertificate is a file reference
func (c *certificateInfo) SignedCertificateFilename() *string {
	if c == nil || c.SignedCertificate == nil {
		return nil
	}
	if strings.HasPrefix(*c.SignedCertificate, PrefixFilename) {
		filename := strings.TrimPrefix(*c.SignedCertificate, PrefixFilename)
		return &filename
	}
	return nil
}

func (c *certificateInfo) SignedCertificateData() (*string, error) {
	if c == nil || c.SignedCertificate == nil {
		return nil, nil
	}
	filename := c.SignedCertificateFilename()
	if filename != nil {
		if content, err := os.ReadFile(*filename); err == nil {
			s := string(content)
			return &s, nil
		}
	}

	return c.SignedCertificate, nil
}

// toCertificate converts the certificateInfo struct to a Certificate struct
func (c *certificateInfo) toCertificate() (*types.Certificate, error) {
	signedCert, err := c.SignedCertificateData()
	if err != nil {
		return nil, err
	}
	return &types.Certificate{
		Header: &types.CertificateHeader{
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
			CertType:                c.CertType,
			CertSource:              c.CertSource,
		},
		SignedCertificate: signedCert,
		AggchainProof:     c.AggchainProof,
		ExtraData:         c.ExtraData,
	}, nil
}

// ID returns a string with the unique identifier of the cerificate (height+certificateID)
func (c *certificateInfo) ID() string {
	if c == nil {
		return types.NilStr
	}
	return fmt.Sprintf("%d/%s (retry %d)", c.Height, c.CertificateID.String(), c.RetryCount)
}

type NonAcceptedCertificate struct {
	Height uint64 `meddler:"height"`
	// SignedCertificate is "@<file_path>" or the actual JSON content of the certificate
	SignedCertificate string `meddler:"signed_certificate"`
	CreatedAt         uint32 `meddler:"created_at"`
	// CertificateHash of the file that contains the certificate in JSON format
	CertificateHash common.Hash `meddler:"certificate_hash,hash"`
	// Error message indicating why the certificate was not accepted
	Error string `meddler:"error"`
}

func NewNonAcceptedCertificate(
	cert *agglayertypes.Certificate,
	createdAt uint32,
	certError string) (*NonAcceptedCertificate, error) {
	raw, err := json.Marshal(cert)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal certificate to JSON: %w", err)
	}

	return &NonAcceptedCertificate{
		Height:            cert.Height,
		SignedCertificate: string(raw),
		CreatedAt:         createdAt,
		Error:             certError,
	}, nil
}
