package flows

import (
	"context"
	"fmt"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
)

var _ types.AggsenderVerifierFlow = (*AggchainProverVerifierFlow)(nil)

// AggchainProverVerifierFlow is a struct that holds the logic for the aggchain prover flow verification
type AggchainProverVerifierFlow struct {
	*AggchainProverBuilderFlow

	fepInputsQuery types.FEPInputsQuerier
}

// NewAggchainProverVerifierFlow creates a new AggchainProverVerifierFlow
func NewAggchainProverVerifierFlow(
	builderFlow *AggchainProverBuilderFlow,
	fepInputsQuery types.FEPInputsQuerier,
) *AggchainProverVerifierFlow {
	return &AggchainProverVerifierFlow{
		AggchainProverBuilderFlow: builderFlow,
		fepInputsQuery:            fepInputsQuery,
	}
}

// VerifyCertificate verifies the new certificate
// This function is used in the validator to verify the certificate
func (a *AggchainProverVerifierFlow) VerifyCertificate(
	ctx context.Context,
	cert *agglayertypes.Certificate,
	lastBlockInCert uint64,
	lastSettledBlock uint64) error {
	if cert.AggchainData == nil {
		return fmt.Errorf("aggchainProverFlow: certificate AggchainData is nil")
	}
	var aggchainDataProof *agglayertypes.AggchainDataProof
	switch v := cert.AggchainData.(type) {
	case *agglayertypes.AggchainDataProof:
		aggchainDataProof = v
	case *agglayertypes.AggchainDataMultisigWithProof:
		aggchainDataProof = v.AggchainProof
	default:
		return fmt.Errorf("aggchainProverFlow: certificate AggchainData is of unknown type %T", cert.AggchainData)
	}

	// we need to reconstruct the AggchainParams field using what proposer provided,
	// plus, the current data from the L1 and L2 networks (what was last settled)
	l1InfoLeaf, err := a.l1InfoTreeDataQuerier.GetInfoByIndex(ctx, cert.L1InfoTreeLeafCount-1)
	if err != nil {
		return fmt.Errorf("aggchainProverFlow - error getting L1InfoLeaf by index %d: %w",
			cert.L1InfoTreeLeafCount-1, err)
	}

	expectedAggchainParams, err := a.fepInputsQuery.GetAggchainParams(
		lastSettledBlock,
		lastBlockInCert,
		l1InfoLeaf.Hash,
	)
	if err != nil {
		return fmt.Errorf("aggchainProverFlow - error getting expected aggchain proof public values: %w", err)
	}

	expectedAggchainParamsHash, err := expectedAggchainParams.Hash()
	if err != nil {
		return fmt.Errorf("aggchainProverFlow - error calculating expected aggchain params hash: %w", err)
	}

	if aggchainDataProof.AggchainParams != expectedAggchainParamsHash {
		a.log.Infof("Aggchain-params unrolled values: %s. Last proven block: %d",
			expectedAggchainParams.String(), lastSettledBlock)
		return fmt.Errorf("aggchainProverFlow - aggchain params do not match: expected %s, got %s",
			expectedAggchainParamsHash, aggchainDataProof.AggchainParams)
	}

	a.log.Infof("Aggchain params match successfully: %s", expectedAggchainParams.String())

	return nil
}
