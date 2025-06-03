package types

import (
	"fmt"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/bridgesync"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
)

const claimSizeFactor = 200 // Size factor for claims in bytes

// CertificateBuildParams is a struct that holds the parameters to build a certificate
type CertificateBuildParams struct {
	FromBlock                      uint64
	ToBlock                        uint64
	Bridges                        []bridgesync.Bridge
	Claims                         []bridgesync.Claim
	CreatedAt                      uint32
	RetryCount                     int
	LastSentCertificate            *CertificateHeader
	L1InfoTreeRootFromWhichToProve common.Hash
	L1InfoTreeLeafCount            uint32
	AggchainProof                  *AggchainProof
	CertificateType                CertificateType
}

func (c *CertificateBuildParams) String() string {
	return fmt.Sprintf("Type: %s FromBlock: %d, ToBlock: %d, numBridges: %d, numClaims: %d, createdAt: %d",
		c.CertificateType, c.FromBlock, c.ToBlock, c.NumberOfBridges(), c.NumberOfClaims(), c.CreatedAt)
}

// Range create a new CertificateBuildParams with the given range
func (c *CertificateBuildParams) Range(fromBlock, toBlock uint64) (*CertificateBuildParams, error) {
	if c.FromBlock == fromBlock && c.ToBlock == toBlock {
		return c, nil
	}
	if c.FromBlock > fromBlock || c.ToBlock < toBlock {
		return nil, fmt.Errorf("invalid range")
	}

	span := toBlock - fromBlock + 1
	fullSpan := c.ToBlock - c.FromBlock + 1

	newCert := &CertificateBuildParams{
		FromBlock: fromBlock,
		ToBlock:   toBlock,
		Bridges: make([]bridgesync.Bridge, 0,
			aggkitcommon.EstimateSliceCapacity(len(c.Bridges), span, fullSpan)),
		Claims: make([]bridgesync.Claim, 0,
			aggkitcommon.EstimateSliceCapacity(len(c.Claims), span, fullSpan)),
		CreatedAt:                      c.CreatedAt,
		RetryCount:                     c.RetryCount,
		LastSentCertificate:            c.LastSentCertificate,
		AggchainProof:                  c.AggchainProof,
		L1InfoTreeRootFromWhichToProve: c.L1InfoTreeRootFromWhichToProve,
		L1InfoTreeLeafCount:            c.L1InfoTreeLeafCount,
		CertificateType:                c.CertificateType,
	}

	for _, bridge := range c.Bridges {
		if bridge.BlockNum >= fromBlock && bridge.BlockNum <= toBlock {
			newCert.Bridges = append(newCert.Bridges, bridge)
		}
	}

	for _, claim := range c.Claims {
		if claim.BlockNum >= fromBlock && claim.BlockNum <= toBlock {
			newCert.Claims = append(newCert.Claims, claim)
		}
	}
	return newCert, nil
}

// NumberOfBridges returns the number of bridges in the certificate
func (c *CertificateBuildParams) NumberOfBridges() int {
	if c == nil {
		return 0
	}
	return len(c.Bridges)
}

// NumberOfClaims returns the number of claims in the certificate
func (c *CertificateBuildParams) NumberOfClaims() int {
	if c == nil {
		return 0
	}
	return len(c.Claims)
}

// NumberOfBlocks returns the number of blocks in the certificate
func (c *CertificateBuildParams) NumberOfBlocks() int {
	if c == nil {
		return 0
	}
	return int(c.ToBlock - c.FromBlock + 1)
}

// EstimatedSize returns the estimated size of the certificate
func (c *CertificateBuildParams) EstimatedSize() uint {
	if c == nil {
		return 0
	}
	sizeBridges := float64(0)
	for _, bridge := range c.Bridges {
		sizeBridges += agglayertypes.EstimatedBridgeExitSize
		sizeBridges += float64(len(bridge.Metadata))
	}

	sizeClaims := float64(0)
	for _, claim := range c.Claims {
		sizeClaims += agglayertypes.EstimatedImportedBridgeExitSize
		sizeClaims += float64(len(claim.Metadata))
	}

	sizeAggchainData := float64(0)
	switch c.CertificateType {
	case CertificateTypeFEP:
		sizeAggchainData += agglayertypes.EstimatedAggchainProofSize
		sizeAggchainData += float64(len(c.Claims) * claimSizeFactor) // for each claim the proof gets bigger by some size
	default:
		sizeAggchainData += agglayertypes.EstimatedAggchainSignatureSize
	}

	return uint(sizeBridges + sizeClaims + sizeAggchainData)
}

// IsEmpty returns true if the certificate is empty
func (c *CertificateBuildParams) IsEmpty() bool {
	return c.NumberOfBridges() == 0 && c.NumberOfClaims() == 0
}

// MaxDepoitCount returns the maximum deposit count in the certificate
func (c *CertificateBuildParams) MaxDepositCount() uint32 {
	if c == nil || c.NumberOfBridges() == 0 {
		return 0
	}
	return c.Bridges[len(c.Bridges)-1].DepositCount
}
