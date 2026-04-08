package types

import (
	"fmt"
	"math"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/bridgesync"
	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/ethereum/go-ethereum/common"
)

const claimSizeFactor = 200 // Size factor for claims in bytes

// CertificatePreBuildParams is a struct that holds the parameters to pre-build a certificate
// basically it's used for generate CertificateBuildParams that add bridges
type CertificatePreBuildParams struct {
	BlockRange          aggkitcommon.BlockRange
	RetryCount          int
	CertificateType     CertificateType
	LastSentCertificate *CertificateHeader
	L1InfoTreeToProve   *CertificateL1InfoTreeData
	CreatedAt           uint32
}

func (c *CertificatePreBuildParams) String() string {
	if c == nil {
		return "CertificatePreBuildParams is nil"
	}
	return fmt.Sprintf("Type: %s BlockRange: %s,  RetryCount: %d, L1InfoTreeToProve:%s, CreatedAt:%d",
		c.CertificateType, c.BlockRange.String(), c.RetryCount, c.L1InfoTreeToProve.String(), c.CreatedAt)
}

// CertificateL1InfoTreeData is a struct that holds the L1 info tree root and leaf count
// that is used to prove the L1 info tree in the certificate
type CertificateL1InfoTreeData struct {
	L1InfoTreeRootToProve common.Hash
	L1InfoTreeLeafCount   uint32
	L1InfoTreeLeaf        *l1infotreesync.L1InfoTreeLeaf
}

func (c *CertificateL1InfoTreeData) String() string {
	if c == nil {
		return "CertificateL1InfoTreeData is nil"
	}
	return fmt.Sprintf("CertificateL1InfoTreeData{L1InfoTreeRootFromWhichToProve: %s, L1InfoTreeLeafCount: %d}",
		c.L1InfoTreeRootToProve.Hex(), c.L1InfoTreeLeafCount)
}

// CertificateBuildParams is a struct that holds the parameters to build a certificate
type CertificateBuildParams struct {
	FromBlock                      uint64
	ToBlock                        uint64
	Bridges                        []bridgesync.Bridge
	Claims                         []claimsynctypes.Claim
	Unclaims                       []claimsynctypes.Unclaim
	CreatedAt                      uint32
	RetryCount                     int
	LastSentCertificate            *CertificateHeader
	L1InfoTreeRootFromWhichToProve common.Hash
	L1InfoTreeLeafCount            uint32
	AggchainProof                  *AggchainProof
	CertificateType                CertificateType
	ExtraData                      string
}

func (c *CertificateBuildParams) String() string {
	return fmt.Sprintf(
		"Type: %s FromBlock: %d, ToBlock: %d, numBridges: %d, "+
			"numClaims: %d, numUnclaims: %d, createdAt: %d",
		c.CertificateType, c.FromBlock, c.ToBlock, c.NumberOfBridges(), c.NumberOfClaims(), c.NumberOfUnclaims(), c.CreatedAt)
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

// NumberOfUnclaims returns the number of unclaims in the certificate
func (c *CertificateBuildParams) NumberOfUnclaims() int {
	if c == nil {
		return 0
	}
	return len(c.Unclaims)
}

// NumberOfBlocks returns the number of blocks in the certificate
func (c *CertificateBuildParams) NumberOfBlocks() int {
	if c == nil {
		return 0
	}
	numBlocks := c.ToBlock - c.FromBlock + 1
	// Check if result would overflow when converting to int
	if numBlocks > uint64(math.MaxInt) {
		return math.MaxInt
	}

	return int(numBlocks)
}

// EstimatedSize returns the estimated size of the certificate
func (c *CertificateBuildParams) EstimatedSize() uint {
	if c == nil {
		return 0
	}
	// common.HashLength represents the size of a metadata hash in bytes
	sizeBridges := (agglayertypes.EstimatedBridgeExitSize + common.HashLength) * float64(len(c.Bridges))
	sizeClaims := (agglayertypes.EstimatedImportedBridgeExitSize + common.HashLength) * float64(len(c.Claims))

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

// IsARetry returns true if the certificate is a retry
func (c *CertificateBuildParams) IsARetry() bool {
	return c != nil && c.RetryCount > 0 && c.LastSentCertificate != nil
}

// MaxDepoitCount returns the maximum deposit count in the certificate
func (c *CertificateBuildParams) MaxDepositCount() uint32 {
	if c == nil || c.NumberOfBridges() == 0 {
		return 0
	}
	return c.Bridges[len(c.Bridges)-1].DepositCount
}

// GetClaimsFilteringUnclaims returns a list of claims that contains all the claims of the CertificateBuildParams
// except the ones that have been unclaimed
func (c *CertificateBuildParams) GetClaimsFilteringUnclaims() []claimsynctypes.Claim {
	filteredClaims := make([]claimsynctypes.Claim, 0, len(c.Claims))
	if len(c.Unclaims) == 0 {
		// 99.9% of the times c.Unclaims is going to be empty
		filteredClaims = append(filteredClaims, c.Claims...)
		return filteredClaims
	}

	usedUnclaims := make([]bool, len(c.Unclaims))
	for _, claim := range c.Claims {
		isUnclaimed := false
		for i, unclaim := range c.Unclaims {
			if claim.GlobalIndex.Cmp(unclaim.GlobalIndex) == 0 && !usedUnclaims[i] {
				isUnclaimed = true
				usedUnclaims[i] = true
				break
			}
		}
		if !isUnclaimed {
			filteredClaims = append(filteredClaims, claim)
		}
	}

	return filteredClaims
}
