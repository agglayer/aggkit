package prover

import (
	"context"
	"fmt"

	"github.com/0xPolygon/cdk-rpc/rpc"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/flows"
	"github.com/agglayer/aggkit/aggsender/grpc"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
)

// Config is the configuration for the AggchainProofGenerationTool
type Config struct {
	// AggchainProofURL is the URL of the AggkitProver
	AggchainProofURL string `mapstructure:"AggchainProofURL"`

	// GlobalExitRootL2Addr is the address of the GlobalExitRootManager contract on l2 sovereign chain
	// this address is needed for the AggchainProof mode of the AggSender
	GlobalExitRootL2Addr common.Address `mapstructure:"GlobalExitRootL2"`
}

// AggchainProofGenerationTool is a tool to generate Aggchain proofs
type AggchainProofGenerationTool struct {
	cfg Config

	logger   *log.Logger
	l2Syncer types.L2BridgeSyncer

	aggchainProofClient grpc.AggchainProofClientInterface
	flow                *flows.AggchainProverFlow
}

// NewAggchainProofGenerationTool creates a new AggchainProofGenerationTool
func NewAggchainProofGenerationTool(
	ctx context.Context,
	logger *log.Logger,
	cfg Config,
	l2Syncer types.L2BridgeSyncer,
	l1InfoTreeSyncer types.L1InfoTreeSyncer,
	l1Client types.EthClient,
	l2Client types.EthClient) (*AggchainProofGenerationTool, error) {
	aggchainProofClient, err := grpc.NewAggchainProofClient(cfg.AggchainProofURL)
	if err != nil {
		return nil, fmt.Errorf("failed to create AggchainProofClient: %w", err)
	}

	aggchainProverFlow, err := flows.NewAggchainProverFlow(
		logger,
		0, false, cfg.GlobalExitRootL2Addr,
		aggchainProofClient,
		nil,
		l1InfoTreeSyncer,
		l2Syncer,
		l1Client,
		l2Client,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create AggchainProverFlow: %w", err)
	}

	return &AggchainProofGenerationTool{
		cfg:                 cfg,
		logger:              logger,
		l2Syncer:            l2Syncer,
		flow:                aggchainProverFlow,
		aggchainProofClient: aggchainProofClient,
	}, nil
}

// GetRPCServices returns the list of services that the RPC provider exposes
func (a *AggchainProofGenerationTool) GetRPCServices() []rpc.Service {
	return []rpc.Service{
		{
			Name:    "aggkit",
			Service: NewAggchainProofGenerationToolRPC(a),
		},
	}
}

// GenerateAggchainProof generates an Aggchain proof
func (a *AggchainProofGenerationTool) GenerateAggchainProof(
	ctx context.Context,
	fromBlock, toBlock uint64) ([]byte, error) {
	a.logger.Infof("Generating Aggchain proof for block range %d : %d", fromBlock, toBlock)

	// get last L2 block synced
	lastL2BlockSynced, err := a.l2Syncer.GetLastProcessedBlock(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting last processed block from l2: %w", err)
	}

	a.logger.Debugf("Last L2 block synced: %d", lastL2BlockSynced)

	// check if last L2 block synced is less than from block
	if lastL2BlockSynced < fromBlock {
		a.logger.Errorf("last L2 block synced %d is less than from block requested %d",
			lastL2BlockSynced, fromBlock)

		return nil, fmt.Errorf("last L2 block synced %d is less than from block requested %d",
			lastL2BlockSynced, fromBlock)
	}

	// get claims for the block range
	a.logger.Debugf("Getting claims for block range %d : %d", fromBlock, toBlock)

	claims, err := a.l2Syncer.GetClaims(ctx, fromBlock, toBlock)
	if err != nil {
		return nil, fmt.Errorf("error getting claims (imported bridge exits): %w", err)
	}

	a.logger.Debugf("Got %d claims for block range %d : %d", len(claims), fromBlock, toBlock)

	// get finalized L1 Info tree data
	a.logger.Debugf("Getting finalized L1 Info tree data")

	proof, leaf, root, err := a.flow.GetFinalizedL1InfoTreeData(ctx)
	if err != nil {
		a.logger.Errorf("error getting finalized L1 Info tree data: %w", err)

		return nil, fmt.Errorf("aggchainProverFlow - error getting finalized L1 Info tree data: %w", err)
	}

	a.logger.Debugf("Got finalized L1 Info tree data with root: %s, L1 block number: %d"+
		"L1 block position: %d, L1 info tree index: %d. GlobalExitRoot: %v",
		root.Hash.String(), leaf.BlockNumber,
		leaf.BlockPosition, leaf.L1InfoTreeIndex, leaf.GlobalExitRoot.String())

	// check if claims are part of finalized L1 Info tree
	a.logger.Debugf("Checking if claims are part of finalized L1 Info tree")

	if err := a.flow.CheckIfClaimsArePartOfFinalizedL1InfoTree(root, claims); err != nil {
		a.logger.Errorf("error checking if claims are part of finalized L1 Info tree root: %s with index: %d: %w",
			root.Hash.String(), root.Index, err)

		return nil, fmt.Errorf("aggchainProverFlow - error checking if claims are part of "+
			"finalized L1 Info tree root: %s with index: %d: %w", root.Hash.String(), root.Index, err)
	}

	a.logger.Debugf("Claims are part of finalized L1 Info tree")

	// get injected GERs proofs on L2 for the block range
	a.logger.Debugf("Getting injected GERs proofs for block range %d : %d, and L1 info tree root: %s",
		fromBlock, toBlock, root.Hash.String())

	injectedGERsProofs, err := a.flow.GetInjectedGERsProofs(ctx, root, fromBlock, toBlock)
	if err != nil {
		a.logger.Errorf("error getting injected GERs proofs: %w", err)

		return nil, fmt.Errorf("aggchainProverFlow - error getting injected GERs proofs: %w", err)
	}

	// convert claims to imported bridge exits for the block range
	a.logger.Debugf("Converting claims to imported bridge exits for block range %d : %d",
		fromBlock, toBlock)

	importedBridgeExits, err := a.flow.GetImportedBridgeExitsForProver(claims)
	if err != nil {
		a.logger.Errorf("error converting claims to imported bridge exits for prover: %w", err)

		return nil, fmt.Errorf("aggchainProverFlow - error getting imported bridge exits for prover: %w", err)
	}

	a.logger.Debugf("Converted %d claims to %d imported bridge exits for block range %d : %d",
		len(claims), len(importedBridgeExits), fromBlock, toBlock)

	// call the prover to generate the proof
	a.logger.Debugf("Calling AggchainProofClient to generate proof for block range %d : %d", fromBlock, toBlock)

	aggchainProof, err := a.aggchainProofClient.GenerateAggchainProof(
		fromBlock,
		toBlock,
		root.Hash,
		*leaf,
		agglayertypes.MerkleProof{
			Root:  root.Hash,
			Proof: proof,
		},
		injectedGERsProofs,
		importedBridgeExits,
	)
	if err != nil {
		a.logger.Errorf("error generating aggchain proof for block range %d : %d: %w", fromBlock, toBlock, err)

		return nil, fmt.Errorf("error fetching aggchain proof for block range %d : %d : %w",
			fromBlock, toBlock, err)
	}

	a.logger.Infof("Generated Aggchain proof for block range %d : %d", fromBlock, toBlock)

	return aggchainProof.Proof, nil
}
