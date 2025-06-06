package certificatebuild

import (
	"fmt"

	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/ethereum/go-ethereum/common"
	"golang.org/x/crypto/sha3"
)

var _ types.CertificateBuildVerifier = (*CertificateBuildVerifier)(nil)

// CertificateBuildVerifier is responsible for verifying the parameters used in building certificates.
// It provides methods to ensure that certificate parameters meet the required criteria before certificate creation.
type CertificateBuildVerifier struct{}

// NewCertificateBuildVerifier creates and returns a new instance of CertificateBuildVerifier.
func NewCertificateBuildVerifier() *CertificateBuildVerifier {
	return &CertificateBuildVerifier{}
}

// VerifyBuildParams performs verification checks on the provided CertificateBuildParams.
// It currently validates the claims using verifyClaimGERs, and serves as an entry point
// for adding additional verification logic in the future.
//
// Parameters:
//   - fullCert: a pointer to types.CertificateBuildParams containing the certificate build parameters to verify.
//
// Returns:
//   - error: an error if verification fails, or nil if all checks pass.
func (v *CertificateBuildVerifier) VerifyBuildParams(fullCert *types.CertificateBuildParams) error {
	// this will be a good place to add more verification checks in the future
	return v.verifyClaimGERs(fullCert.Claims)
}

// verifyClaimGERs verifies the correctnes GERs of the claims
func (v *CertificateBuildVerifier) verifyClaimGERs(claims []bridgesync.Claim) error {
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
