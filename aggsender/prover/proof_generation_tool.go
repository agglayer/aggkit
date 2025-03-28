package prover

import (
	"context"
	"fmt"

	"github.com/0xPolygon/cdk-rpc/rpc"
	"github.com/agglayer/aggkit/aggsender/flows"
	"github.com/agglayer/aggkit/aggsender/grpc"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/log"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
)

// ProofGeneration is the interface for generating Aggchain proofs
type AggchainProofGeneration interface {
	GenerateAggchainProof(ctx context.Context, fromBlock, toBlock uint64) ([]byte, error)
}

// AggchainProofFlow is the interface for the Aggchain proof flow
type AggchainProofFlow interface {
	// GenerateAggchainProof generates an Aggchain proof
	GenerateAggchainProof(
		ctx context.Context,
		fromBlock, toBlock uint64,
		claims []bridgesync.Claim) (*types.AggchainProof, *treetypes.Root, error)
}

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
	flow                AggchainProofFlow
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

	// call the prover to generate the proof
	a.logger.Debugf("Calling AggchainProofClient to generate proof for block range %d : %d", fromBlock, toBlock)

	aggchainProof, _, err := a.flow.GenerateAggchainProof(
		ctx,
		fromBlock,
		toBlock,
		claims,
	)
	if err != nil {
		return nil, fmt.Errorf("error generating Aggchain proof: %w", err)
	}

	a.logger.Infof("Generated Aggchain proof for block range %d : %d", fromBlock, toBlock)

	return aggchainProof.Proof, nil
}
