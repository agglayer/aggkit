package certificatebuild

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/ethereum/go-ethereum/common"
	"golang.org/x/crypto/sha3"
)

var _ types.CommonCertParamsVerifier = (*CommonParamsVerifier)(nil)

// CommonParamsVerifier is responsible for verifying common parameters related to certificate building.
// It utilizes a BridgeQuerier to interact with the L2 bridge and can enforce a requirement that there
// is no gap between FEP (Finalized Epoch Proof) blocks, depending on the requireNoFEPBlockGap flag.
type CommonParamsVerifier struct {
	l2BridgeQuerier      types.BridgeQuerier
	requireNoFEPBlockGap bool
}

// NewCommonParamsVerifier creates a new CommonParamsVerifier instance
func NewCommonParamsVerifier(
	l2BridgeQuerier types.BridgeQuerier,
	requireNoFEPBlockGap bool,
) *CommonParamsVerifier {
	return &CommonParamsVerifier{
		l2BridgeQuerier:      l2BridgeQuerier,
		requireNoFEPBlockGap: requireNoFEPBlockGap,
	}
}

// VerifyBuildParams verifies the build parameters
func (c *CommonParamsVerifier) VerifyBuildParams(ctx context.Context, fullCert *types.CertificateBuildParams) error {
	if err := c.verifyRetryCertStartingBlock(fullCert); err != nil {
		return fmt.Errorf("error verifying retry certificate starting block: %w", err)
	}

	if err := c.verifyClaimGERs(fullCert.Claims); err != nil {
		return err
	}

	return nil
}

// VerifyBlockRangeGaps checks if there are any gaps in the block range of the certificate
// and verifies that there are no new bridges or claims in the gap.
func (c *CommonParamsVerifier) VerifyBlockRangeGaps(
	ctx context.Context,
	lastSentCertificate *types.CertificateHeader,
	newFromBlock, newToBlock uint64) error {
	if lastSentCertificate == nil {
		return nil
	}

	lastSettledFromBlock := uint64(0)
	lastSettledToBlock := uint64(0)
	if lastSentCertificate.Status.IsInError() {
		// if the last certificate was in error, we need to check the last
		// settled range to be correct
		// we will leave the from block as 0, since we only require the to block
		// to check the gap between the last sent certificate and the new one
		if lastSentCertificate.FromBlock > 0 {
			lastSettledToBlock = lastSentCertificate.FromBlock - 1
		}
	} else {
		lastSettledFromBlock = lastSentCertificate.FromBlock
		lastSettledToBlock = lastSentCertificate.ToBlock
	}

	nextBlockRange := types.NewBlockRange(newFromBlock, newToBlock)
	lastBlockRange := types.NewBlockRange(lastSettledFromBlock, lastSettledToBlock)

	// case 2: is a new cert but is not contiguous to previous one
	gap := nextBlockRange.Gap(lastBlockRange)
	if gap.IsEmpty() {
		return nil
	}

	bridgeDataInTheGap, claimDataInTheGap, err := c.l2BridgeQuerier.GetBridgesAndClaims(
		ctx, gap.FromBlock, gap.ToBlock)
	if err != nil {
		return fmt.Errorf("error getting bridges and claims in the gap %s: %w", gap.String(), err)
	}
	if len(bridgeDataInTheGap) > 0 || len(claimDataInTheGap) > 0 {
		return fmt.Errorf("there are new bridges or claims in the gap %s, len(bridges)=%d. len(claims)=%d",
			gap.String(), len(bridgeDataInTheGap), len(claimDataInTheGap))
	}

	if !gap.IsEmpty() && c.requireNoFEPBlockGap {
		// even though we do not have bridge transactions in the gap,
		// we need to return an error if RequireNoFEPBlockGap is true
		return fmt.Errorf("block gap detected: %s without bridge transactions, but RequireNoFEPBlockGap is true",
			gap.String())
	}

	return nil
}

// verifyRetryCertStartingBlock verifies that the starting block of a retry certificate
// matches the last sent (InError) certificate's starting block.
func (c *CommonParamsVerifier) verifyRetryCertStartingBlock(buildParams *types.CertificateBuildParams) error {
	if buildParams.IsARetry() && buildParams.FromBlock != buildParams.LastSentCertificate.FromBlock {
		return fmt.Errorf("retry certificate fromBlock %d != last sent certificate fromBlock %d",
			buildParams.FromBlock, buildParams.LastSentCertificate.FromBlock)
	}

	return nil
}

// verifyClaimGERs verifies the correctnes GERs of the claims
func (c *CommonParamsVerifier) verifyClaimGERs(claims []bridgesync.Claim) error {
	for _, claim := range claims {
		ger := calculateGER(claim.MainnetExitRoot, claim.RollupExitRoot)
		if ger != claim.GlobalExitRoot {
			return fmt.Errorf("claim[GlobalIndex: %s, BlockNum: %d]: GER mismatch. Expected: %s, got: %s",
				claim.GlobalIndex.String(), claim.BlockNum, claim.GlobalExitRoot.String(), ger.String())
		}
	}

	return nil
}

// calculateGER calculates the GER hash based on the mainnet exit root and the rollup exit root
func calculateGER(mainnetExitRoot, rollupExitRoot common.Hash) common.Hash {
	var gerBytes [common.HashLength]byte
	hasher := sha3.NewLegacyKeccak256()
	hasher.Write(mainnetExitRoot.Bytes())
	hasher.Write(rollupExitRoot.Bytes())
	copy(gerBytes[:], hasher.Sum(nil))

	return gerBytes
}
