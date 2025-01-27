package aggsender

import (
	"context"
	"fmt"
	"time"

	"github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/grpc"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
)

// FlowManager is an interface that defines the methods to manage the flow of the AggSender
// based on the different prover types
type FlowManager interface {
	// GetCertificateBuildParams returns the parameters to build a certificate
	GetCertificateBuildParams(ctx context.Context) (*types.CertificateBuildParams, error)
}

// flowManager is a struct that holds the common logic for the different prover types
type flowManager struct {
	l2Syncer         types.L2BridgeSyncer
	l1infoTreeSyncer types.L1InfoTreeSyncer
	storage          db.AggSenderStorage

	log types.Logger
}

// getBridgesAndClaims returns the bridges and claims consumed from the L2 fromBlock to toBlock
func (f *flowManager) getBridgesAndClaims(
	ctx context.Context,
	fromBlock, toBlock uint64,
) ([]bridgesync.Bridge, []bridgesync.Claim, error) {
	bridges, err := f.l2Syncer.GetBridgesPublished(ctx, fromBlock, toBlock)
	if err != nil {
		return nil, nil, fmt.Errorf("error getting bridges: %w", err)
	}

	if len(bridges) == 0 {
		f.log.Infof("no bridges consumed, no need to send a certificate from block: %d to block: %d",
			fromBlock, toBlock)
		return nil, nil, nil
	}

	claims, err := f.l2Syncer.GetClaims(ctx, fromBlock, toBlock)
	if err != nil {
		return nil, nil, fmt.Errorf("error getting claims: %w", err)
	}

	return bridges, claims, nil
}

// GetCertificateBuildParams returns the parameters to build a certificate
// this funciton is the implementation of the FlowManager interface
func (f *flowManager) GetCertificateBuildParams(ctx context.Context) (*types.CertificateBuildParams, error) {
	lastL2BlockSynced, err := f.l2Syncer.GetLastProcessedBlock(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting last processed block from l2: %w", err)
	}

	lastSentCertificateInfo, err := f.storage.GetLastSentCertificate()
	if err != nil {
		return nil, err
	}

	previousToBlock, retryCount := getLastSentBlockAndRetryCount(lastSentCertificateInfo)

	if previousToBlock >= lastL2BlockSynced {
		f.log.Infof("no new blocks to send a certificate, last certificate block: %d, last L2 block: %d",
			previousToBlock, lastL2BlockSynced)
		return nil, nil
	}

	fromBlock := previousToBlock + 1
	toBlock := lastL2BlockSynced

	bridges, claims, err := f.getBridgesAndClaims(ctx, fromBlock, toBlock)
	if err != nil {
		return nil, err
	}

	return &types.CertificateBuildParams{
		FromBlock:              fromBlock,
		ToBlock:                toBlock,
		RetryCount:             retryCount,
		ShouldBuildCertificate: len(bridges) > 0,
		LastSentCertificate:    lastSentCertificateInfo,
		Bridges:                bridges,
		Claims:                 claims,
		CreatedAt:              uint32(time.Now().UTC().Unix()),
	}, nil
}

// aggkitProverFlow is a struct that holds the logic for the AggkitProver prover type flow
type aggkitProverFlow struct {
	*flowManager

	aggkitProverClient grpc.AggchainProofClientInterface
}

// newAggkitProverFlow returns a new instance of the aggkitProverFlow
func newAggkitProverFlow(aggkitProverClient grpc.AggchainProofClientInterface,
	storage db.AggSenderStorage,
	l2Syncer types.L2BridgeSyncer,
	l1infoTreeSyncer types.L1InfoTreeSyncer) *aggkitProverFlow {
	return &aggkitProverFlow{
		aggkitProverClient: aggkitProverClient,
		flowManager: &flowManager{
			l2Syncer:         l2Syncer,
			l1infoTreeSyncer: l1infoTreeSyncer,
			storage:          storage,
		},
	}
}

// GetCertificateBuildParams returns the parameters to build a certificate
// this funciton is the implementation of the FlowManager interface
// What differentiates this function from the regular PP flow is that,
// if the last sent certificate is in error, we need to resend the exact same certificate
func (a *aggkitProverFlow) GetCertificateBuildParams(ctx context.Context) (*types.CertificateBuildParams, error) {
	lastSentCertificateInfo, err := a.storage.GetLastSentCertificate()
	if err != nil {
		return nil, err
	}

	if lastSentCertificateInfo != nil && lastSentCertificateInfo.Status == agglayer.InError {
		bridges, claims, err := a.getBridgesAndClaims(ctx, lastSentCertificateInfo.FromBlock, lastSentCertificateInfo.ToBlock)
		if err != nil {
			return nil, err
		}

		if len(bridges) == 0 {
			// this should never happen, if it does, we need to investigate
			// (maybe someone deleted the bridge syncer db, so we might need to wait for it to catch up)
			// just keep return an error here
			return nil, fmt.Errorf("we have an InError certificate: %s, but no bridges to resend the same certificate",
				lastSentCertificateInfo.String())
		}

		// we need to resend the exact same certificate
		return &types.CertificateBuildParams{
			FromBlock:              lastSentCertificateInfo.FromBlock,
			ToBlock:                lastSentCertificateInfo.ToBlock,
			RetryCount:             lastSentCertificateInfo.RetryCount + 1,
			ShouldBuildCertificate: true,
			Bridges:                bridges,
			Claims:                 claims,
			LastSentCertificate:    lastSentCertificateInfo,
			CreatedAt:              lastSentCertificateInfo.CreatedAt,
		}, nil
	}

	// use the old logic, where we build the new certificate
	return a.flowManager.GetCertificateBuildParams(ctx)
}

// ppFlow is a struct that holds the logic for the regular pessimistic proof flow
type ppFlow struct {
	*flowManager
}

// newPPFlow returns a new instance of the ppFlow
func newPPFlow(storage db.AggSenderStorage,
	l2Syncer types.L2BridgeSyncer,
	l1infoTreeSyncer types.L1InfoTreeSyncer) *ppFlow {
	return &ppFlow{
		flowManager: &flowManager{
			l2Syncer:         l2Syncer,
			l1infoTreeSyncer: l1infoTreeSyncer,
			storage:          storage,
		},
	}
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
