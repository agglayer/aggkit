package types

import (
	"fmt"
	"math"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/bridgesync"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/ethereum/go-ethereum/common"
)

const claimSizeFactor = 200 // Size factor for claims in bytes

// CertificatePreBuildParams is a struct that holds the parameters to pre-build a certificate
// basically it's used for generate CertificateBuildParams that add bridges
type CertificatePreBuildParams struct {
	BlockRange          BlockRange
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
	Claims                         []bridgesync.Claim
	Unclaims                       []bridgesynctypes.Unclaim
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

// Range create a new CertificateBuildParams with the given range
func (c *CertificateBuildParams) Range(fromBlock, toBlock uint64) (*CertificateBuildParams, error) {
	if c.FromBlock == fromBlock && c.ToBlock == toBlock {
		return c, nil
	}
	if c.FromBlock > fromBlock || c.ToBlock < toBlock {
		return nil, fmt.Errorf("invalid range. FromBlock %d and ToBlock %d are not within "+
			"the certificate range FromBlock %d and ToBlock %d",
			fromBlock, toBlock, c.FromBlock, c.ToBlock)
	}

	if fromBlock > toBlock {
		return nil, fmt.Errorf("invalid range. FromBlock %d is greater than toBlock %d", fromBlock, toBlock)
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
		Unclaims: make([]bridgesynctypes.Unclaim, 0,
			aggkitcommon.EstimateSliceCapacity(len(c.Unclaims), span, fullSpan)),
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

	for _, unclaim := range c.Unclaims {
		if unclaim.BlockNumber >= fromBlock && unclaim.BlockNumber <= toBlock {
			newCert.Unclaims = append(newCert.Unclaims, unclaim)
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

// AdjustToBlock adjusts the certificate build parameters to a new target block.
// If newToBlock is higher than the current ToBlock, it returns an error.
// If newToBlock is lower than the current ToBlock, it creates new parameters
// with an adjusted range that includes only bridges, claims and unclaims within the new range.
// If newToBlock equals the current ToBlock, it returns the original parameters unchanged.
//
// Parameters:
//   - newToBlock: the new target block number to adjust to
//
// Returns:
//   - *CertificateBuildParams: adjusted parameters or original if no adjustment needed
//   - error: if newToBlock is higher than current ToBlock or if range adjustment fails
func (c *CertificateBuildParams) AdjustToBlock(newToBlock uint64) (*CertificateBuildParams, error) {
	if c.ToBlock < newToBlock {
		return nil, fmt.Errorf("cannot adjust toBlock to a higher value. current toBlock: %d, new toBlock: %d",
			c.ToBlock, newToBlock)
	}

	if c.ToBlock > newToBlock {
		// if the toBlock was adjusted, we need to adjust the bridges and claims
		// to only include the ones in the new range that aggchain prover returned
		adjustedParams, err := c.Range(c.FromBlock, newToBlock)
		if err != nil {
			return nil, fmt.Errorf("aggchainProverFlow - error adjusting the range of the certificate: %w", err)
		}

		return adjustedParams, nil
	}

	return c, nil
}
