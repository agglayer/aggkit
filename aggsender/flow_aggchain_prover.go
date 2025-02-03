package aggsender

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/grpc"
	"github.com/agglayer/aggkit/aggsender/types"
)

// aggchainProverFlow is a struct that holds the logic for the AggchainProver prover type flow
type aggchainProverFlow struct {
	*baseFlow

	aggchainProofClient grpc.AggchainProofClientInterface
}

// newAggchainProverFlow returns a new instance of the aggchainProverFlow
func newAggchainProverFlow(log types.Logger,
	cfg Config,
	aggkitProverClient grpc.AggchainProofClientInterface,
	storage db.AggSenderStorage,
	l1InfoTreeSyncer types.L1InfoTreeSyncer,
	l2Syncer types.L2BridgeSyncer) *aggchainProverFlow {
	return &aggchainProverFlow{
		aggchainProofClient: aggkitProverClient,
		baseFlow: &baseFlow{
			log:              log,
			cfg:              cfg,
			l2Syncer:         l2Syncer,
			storage:          storage,
			l1InfoTreeSyncer: l1InfoTreeSyncer,
		},
	}
}

// GetCertificateBuildParams returns the parameters to build a certificate
// this function is the implementation of the FlowManager interface
// What differentiates this function from the regular PP flow is that,
// if the last sent certificate is in error, we need to resend the exact same certificate
func (a *aggchainProverFlow) GetCertificateBuildParams(ctx context.Context) (*types.CertificateBuildParams, error) {
	lastSentCertificateInfo, err := a.storage.GetLastSentCertificate()
	if err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error getting last sent certificate: %w", err)
	}

	if lastSentCertificateInfo != nil && lastSentCertificateInfo.Status == agglayer.InError {
		// if the last certificate was in error, we need to resend it

		a.log.Infof("resending the same InError certificate: %s", lastSentCertificateInfo.String())

		bridges, claims, err := a.getBridgesAndClaims(ctx, lastSentCertificateInfo.FromBlock, lastSentCertificateInfo.ToBlock)
		if err != nil {
			return nil, fmt.Errorf("aggchainProverFlow - error getting bridges and claims: %w", err)
		}

		if len(bridges) == 0 {
			// this should never happen, if it does, we need to investigate
			// (maybe someone deleted the bridge syncer db, so we might need to wait for it to catch up)
			// just keep return an error here
			return nil, fmt.Errorf("aggchainProverFlow - we have an InError certificate: %s, "+
				"but no bridges to resend the same certificate", lastSentCertificateInfo.String())
		}

		proof := lastSentCertificateInfo.AggchainProof
		toBlock := lastSentCertificateInfo.ToBlock

		if proof == "" {
			aggchainProof, err := a.aggchainProofClient.GenerateAggchainProof(lastSentCertificateInfo.FromBlock,
				lastSentCertificateInfo.ToBlock)
			if err != nil {
				return nil, fmt.Errorf("aggchainProverFlow - error fetching aggchain proof for block range %d : %d : %w",
					lastSentCertificateInfo.FromBlock, lastSentCertificateInfo.ToBlock, err)
			}

			a.log.Infof("aggchainProverFlow - InError certificate did not have auth proof, "+
				"so got it from the aggchain prover for range %d : %d. Proof: %s. Requested range: %d : %d",
				aggchainProof.StartBlock, aggchainProof.EndBlock, aggchainProof.Proof,
				lastSentCertificateInfo.FromBlock, lastSentCertificateInfo.ToBlock)

			proof = aggchainProof.Proof

			if aggchainProof.EndBlock < lastSentCertificateInfo.ToBlock {
				// aggchain prover can return a proof for a smaller range than requested
				// so we need to adjust the toBlock
				toBlock = aggchainProof.EndBlock
			}
		}

		// we need to resend the same certificate
		buildParams := &types.CertificateBuildParams{
			FromBlock:           lastSentCertificateInfo.FromBlock,
			ToBlock:             lastSentCertificateInfo.ToBlock,
			RetryCount:          lastSentCertificateInfo.RetryCount + 1,
			Bridges:             bridges,
			Claims:              claims,
			LastSentCertificate: lastSentCertificateInfo,
			CreatedAt:           lastSentCertificateInfo.CreatedAt,
			AggchainProof:       proof,
		}

		buildParams, err = adjustBlockRange(buildParams, lastSentCertificateInfo.ToBlock, toBlock)
		if err != nil {
			return nil, err
		}

		return buildParams, nil
	}

	// use the old logic, where we build the new certificate
	buildParams, err := a.baseFlow.GetCertificateBuildParams(ctx)
	if err != nil {
		return nil, err
	}

	aggchainProof, err := a.aggchainProofClient.GenerateAggchainProof(buildParams.FromBlock, buildParams.ToBlock)
	if err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error fetching aggchain proof for block range %d : %d : %w",
			buildParams.FromBlock, buildParams.ToBlock, err)
	}

	a.log.Infof("aggchainProverFlow - fetched auth proof: %s Range %d : %d from aggchain prover. Requested range: %d : %d",
		aggchainProof.Proof, aggchainProof.StartBlock, aggchainProof.EndBlock,
		buildParams.FromBlock, buildParams.ToBlock)

	buildParams.AggchainProof = aggchainProof.Proof

	buildParams, err = adjustBlockRange(buildParams, buildParams.ToBlock, aggchainProof.EndBlock)
	if err != nil {
		return nil, err
	}

	return buildParams, nil
}

// adjustBlockRange adjusts the block range of the certificate to match the range returned by the aggchain prover
func adjustBlockRange(buildParams *types.CertificateBuildParams,
	requestedToBlock, aggchainProverToBlock uint64) (*types.CertificateBuildParams, error) {
	var err error
	if requestedToBlock != aggchainProverToBlock {
		// if the toBlock was adjusted, we need to adjust the bridges and claims
		// to only include the ones in the new range that aggchain prover returned
		buildParams, err = buildParams.Range(buildParams.FromBlock, aggchainProverToBlock)
		if err != nil {
			return nil, fmt.Errorf("aggchainProverFlow - error adjusting the range of the certificate: %w", err)
		}
	}

	return buildParams, nil
}

// getLastSentBlockAndRetryCount returns the last sent block of the last sent certificate
// if there is no previosly sent certificate, it returns 0 and 0
func getLastSentBlockAndRetryCount(lastSentCertificateInfo *types.CertificateInfo) (uint64, int) {
	if lastSentCertificateInfo == nil {
		return 0, 0
	}

	retryCount := 0
	lastSentBlock := lastSentCertificateInfo.ToBlock

	if lastSentCertificateInfo.Status == agglayer.InError {
		// if the last certificate was in error, we need to resend it
		// from the block before the error
		if lastSentCertificateInfo.FromBlock > 0 {
			lastSentBlock = lastSentCertificateInfo.FromBlock - 1
		}

		retryCount = lastSentCertificateInfo.RetryCount + 1
	}

	return lastSentBlock, retryCount
}
