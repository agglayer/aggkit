package flows

import (
	"fmt"
	"math/big"

	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
)

type invalidClaimAssessmentReason string

const (
	invalidClaimAssessmentReasonRecoverable invalidClaimAssessmentReason = "recoverable"
	invalidClaimAssessmentReasonNoUnclaim   invalidClaimAssessmentReason = "no_unclaim"
)

type invalidClaimAssessment struct {
	reason            invalidClaimAssessmentReason
	cutBlock          uint64
	cutClaim          *bridgesync.Claim
	culpritClaim      *bridgesync.Claim
	culpritUnclaim    uint64
	hasCulpritUnclaim bool
}

// validateUnclaimsForUnfinalizedGERs validates unclaims for unfinalized GERs that don't exist on L1
// in a single pass. This function combines two checks:
//  1. Checks if any claim with unfinalized GER that doesn't exist on L1 has an unclaim that appears
//     after a later unfinalized claim. If so, returns the block number to cut at.
//  2. Validates that any previous claims with unfinalized GERs that don't exist on L1 have their
//     unclaims before the current claim's block. If a claim without an unclaim is found, returns
//     the block number to cut at.
//
// Returns the earliest cut block found (or 0 if no cut is needed) and an error if validation fails.
func (f *baseFlow) validateUnclaimsForUnfinalizedGERs(
	certParams *types.CertificateBuildParams,
	cache *gerStatusCache) (*invalidClaimAssessment, error) {
	unclaimMap := earliestUnclaimByGlobalIndex(certParams.Unclaims)

	var recoverableClaim *invalidClaimAssessment

	for i, claim := range certParams.Claims {
		isGERFinalized, err := f.getGERFinalizedStatus(cache, claim.GlobalExitRoot, certParams.L1InfoTreeLeafCount)
		if err != nil {
			return nil, fmt.Errorf("error checking if claim's GER %s is finalized: %w",
				claim.GlobalExitRoot.String(), err)
		}
		if isGERFinalized {
			continue
		}

		unclaimBlock, hasUnclaim := unclaimMap[claim.GlobalIndex.String()]
		if !hasUnclaim {
			currentClaim := claim
			return &invalidClaimAssessment{
				reason:       invalidClaimAssessmentReasonNoUnclaim,
				cutBlock:     claim.BlockNum,
				cutClaim:     &currentClaim,
				culpritClaim: &currentClaim,
			}, nil
		}

		for j := i + 1; j < len(certParams.Claims); j++ {
			laterClaim := certParams.Claims[j]
			if laterClaim.BlockNum > unclaimBlock {
				continue
			}

			isLaterGERFinalized, err := f.getGERFinalizedStatus(
				cache, laterClaim.GlobalExitRoot, certParams.L1InfoTreeLeafCount)
			if err != nil {
				return nil, fmt.Errorf("error checking if later claim's GER %s is finalized: %w",
					laterClaim.GlobalExitRoot.String(), err)
			}
			if isLaterGERFinalized {
				continue
			}

			if _, hasLaterUnclaim := unclaimMap[laterClaim.GlobalIndex.String()]; hasLaterUnclaim {
				continue
			}

			cutClaim := claim
			blockingClaim := laterClaim
			return &invalidClaimAssessment{
				reason:       invalidClaimAssessmentReasonNoUnclaim,
				cutBlock:     claim.BlockNum,
				cutClaim:     &cutClaim,
				culpritClaim: &blockingClaim,
			}, nil
		}

		if recoverableClaim == nil {
			currentClaim := claim
			recoverableClaim = &invalidClaimAssessment{
				reason:            invalidClaimAssessmentReasonRecoverable,
				culpritClaim:      &currentClaim,
				culpritUnclaim:    unclaimBlock,
				hasCulpritUnclaim: true,
			}
		}
	}

	return recoverableClaim, nil
}

func (f *baseFlow) logInvalidClaimNeedsUnclaim(
	certParams *types.CertificateBuildParams,
	assessment *invalidClaimAssessment,
) {
	if assessment == nil || assessment.culpritClaim == nil {
		return
	}

	msg := fmt.Sprintf("blocking invalid claim requires an unclaim before aggsender can proceed. %s, synced_cert_range=%d-%d",
		formatClaimForLogs(*assessment.culpritClaim), certParams.FromBlock, certParams.ToBlock)
	if assessment.cutClaim != nil && assessment.cutClaim.GlobalIndex != nil &&
		assessment.culpritClaim.GlobalIndex != nil &&
		assessment.cutClaim.GlobalIndex.Cmp(assessment.culpritClaim.GlobalIndex) != 0 {
		msg += fmt.Sprintf(", current_cut_claim_block=%d, current_cut_claim_global_index=%s",
			assessment.cutClaim.BlockNum, assessment.cutClaim.GlobalIndex.String())
	}
	f.log.Warnf("%s. No matching unclaim was found in the current DB-backed candidate certificate. An unclaim needs to happen for aggsender to get unstuck.", msg)
}

func (f *baseFlow) logLimiterBlockedInvalidClaim(
	fullCert *types.CertificateBuildParams,
	limitedCert *types.CertificateBuildParams,
	limiterName string,
) error {
	if fullCert == nil || limitedCert == nil || limitedCert.ToBlock >= fullCert.ToBlock {
		return nil
	}

	cache := newGERStatusCache()
	unclaimMap := earliestUnclaimByGlobalIndex(fullCert.Unclaims)

	for _, claim := range fullCert.Claims {
		if claim.BlockNum > limitedCert.ToBlock {
			break
		}

		isGERFinalized, err := f.getGERFinalizedStatus(cache, claim.GlobalExitRoot, fullCert.L1InfoTreeLeafCount)
		if err != nil {
			return fmt.Errorf("error checking if claim's GER %s is finalized: %w", claim.GlobalExitRoot.String(), err)
		}
		if isGERFinalized {
			continue
		}

		unclaimBlock, hasUnclaim := unclaimMap[bigIntKey(claim.GlobalIndex)]
		if !hasUnclaim || unclaimBlock <= limitedCert.ToBlock {
			continue
		}

		suggestion := fmt.Sprintf("increase %s so block %d fits in the same certificate as this claim", limiterName, unclaimBlock)
		if limiterName == "MaxL2BlockNumber" {
			suggestion = fmt.Sprintf("increase MaxL2BlockNumber to at least %d", unclaimBlock)
		}

		f.log.Warnf("%s prevents aggsender from including the unclaim that clears a blocking invalid claim. %s, required_unclaim_block=%d, full_cert_range=%d-%d, limited_cert_range=%d-%d. Suggested config change: %s.",
			limiterName, formatClaimForLogs(claim), unclaimBlock, fullCert.FromBlock, fullCert.ToBlock, limitedCert.FromBlock, limitedCert.ToBlock, suggestion)
		return nil
	}

	return nil
}

func formatClaimForLogs(claim bridgesync.Claim) string {
	amount := "nil"
	if claim.Amount != nil {
		amount = claim.Amount.String()
	}

	return fmt.Sprintf("claim_block=%d, global_index=%s, token=%s, amount=%s",
		claim.BlockNum, bigIntKey(claim.GlobalIndex), claim.OriginAddress.Hex(), amount)
}

func bigIntKey(value *big.Int) string {
	if value == nil {
		return "nil"
	}

	return value.String()
}

func logLimiterBlockedInvalidClaim(
	base types.AggsenderFlowBaser,
	fullCert *types.CertificateBuildParams,
	limitedCert *types.CertificateBuildParams,
	limiterName string,
) {
	concreteBaseFlow, ok := base.(*baseFlow)
	if !ok {
		return
	}

	if err := concreteBaseFlow.logLimiterBlockedInvalidClaim(fullCert, limitedCert, limiterName); err != nil {
		concreteBaseFlow.log.Warnf("unable to assess non-finalized claim after %s reduction: %v", limiterName, err)
	}
}

func earliestUnclaimByGlobalIndex(unclaims []bridgesynctypes.Unclaim) map[string]uint64 {
	unclaimMap := make(map[string]uint64)
	for _, unclaim := range unclaims {
		key := bigIntKey(unclaim.GlobalIndex)
		if key == "" {
			continue
		}
		if existing, ok := unclaimMap[key]; !ok || unclaim.BlockNumber < existing {
			unclaimMap[key] = unclaim.BlockNumber
		}
	}

	return unclaimMap
}
