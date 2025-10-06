package flows

import (
	"context"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
)

var _ types.AggsenderVerifierFlow = (*PPVerifierFlow)(nil)

// PPVerifierFlow is a struct that holds the logic for the regular pessimistic proof flow verification
type PPVerifierFlow struct {
	*PPBuilderFlow
}

// NewPPVerifierFlow returns a new instance of the PPVerifierFlow
func NewPPVerifierFlow(builderFlow *PPBuilderFlow) *PPVerifierFlow {
	return &PPVerifierFlow{
		PPBuilderFlow: builderFlow,
	}
}

// VerifyCertificate verifies the new certificate
// This function is used in the validator to verify the certificate
func (p *PPVerifierFlow) VerifyCertificate(
	ctx context.Context,
	cert *agglayertypes.Certificate,
	lastBlockInCert uint64,
	lastSettledBlock uint64) error {
	// for PP certificates there is nothing to verify specific to PP flow
	// signature of the proposer will be added with signatures of other committee members
	// in the multisig, so no need to verify anything here
	return nil
}
