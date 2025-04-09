package prover

import (
	"context"
	"fmt"

	"github.com/0xPolygon/cdk-rpc/rpc"
	"github.com/agglayer/aggkit/aggsender/flows"
	"github.com/agglayer/aggkit/aggsender/grpc"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	configtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/log"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
)

// ProofGeneration is the interface for generating Aggchain proofs
type AggchainProofGeneration interface {
	GenerateAggchainProof(ctx context.Context, fromBlock, toBlock uint64) (*types.SP1StarkProof, error)
}

// AggchainProofFlow is the interface for the Aggchain proof flow
type AggchainProofFlow interface {
	// GenerateAggchainProof generates an Aggchain proof
	GenerateAggchainProof(
		ctx context.Context,
		lastProvenBlock, toBlock uint64,
		claims []bridgesync.Claim) (*types.AggchainProof, *treetypes.Root, error)
}

// Config is the configuration for the AggchainProofGenerationTool
type Config struct {
	// AggchainProofURL is the URL of the AggkitProver
	AggchainProofURL string `mapstructure:"AggchainProofURL"`

	// GlobalExitRootL2Addr is the address of the GlobalExitRootManager contract on l2 sovereign chain
	// this address is needed for the AggchainProof mode of the AggSender
	GlobalExitRootL2Addr common.Address `mapstructure:"GlobalExitRootL2"`

	// GenerateAggchainProofTimeout is the timeout to wait for the aggkit-prover to generate the AggchainProof
	GenerateAggchainProofTimeout configtypes.Duration `mapstructure:"GenerateAggchainProofTimeout"`

	// SovereignRollupAddr is the address of the sovereign rollup contract on L1
	SovereignRollupAddr common.Address `mapstructure:"SovereignRollupAddr"`
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
	aggchainProofClient, err := grpc.NewAggchainProofClient(
		cfg.AggchainProofURL, cfg.GenerateAggchainProofTimeout.Duration)
	if err != nil {
		return nil, fmt.Errorf("failed to create AggchainProofClient: %w", err)
	}

	aggchainProverFlow, err := flows.NewAggchainProverFlow(
		logger,
		0, false,
		cfg.GlobalExitRootL2Addr, cfg.SovereignRollupAddr,
		aggchainProofClient,
		nil,
		l1InfoTreeSyncer,
		l2Syncer,
		l1Client,
		l2Client,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create the AggchainProverFlow: %w", err)
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
	lastProvenBlock, maxEndBlock uint64) (*types.SP1StarkProof, error) {
	a.logger.Infof("Generating Aggchain proof. Last proven block: %d. "+
		"Max end block: %d", lastProvenBlock, maxEndBlock)

	// get last L2 block synced
	lastL2BlockSynced, err := a.l2Syncer.GetLastProcessedBlock(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting last processed block from l2: %w", err)
	}

	a.logger.Debugf("Last L2 block synced: %d", lastL2BlockSynced)

	// check if last L2 block synced is less than last proven block
	if lastL2BlockSynced < lastProvenBlock {
		a.logger.Errorf("last L2 block synced %d is less than last proven block %d",
			lastL2BlockSynced, lastProvenBlock)

		return nil, fmt.Errorf("the last L2 block synced %d is less than the last proven block %d",
			lastL2BlockSynced, lastProvenBlock)
	}

	fromBlock := lastProvenBlock + 1

	// get claims for the block range
	a.logger.Debugf("Getting claims for block range [%d : %d]", fromBlock, maxEndBlock)

	claims, err := a.l2Syncer.GetClaims(ctx, fromBlock, maxEndBlock)
	if err != nil {
		return nil, fmt.Errorf("error getting claims (imported bridge exits): %w", err)
	}

	a.logger.Debugf("Got %d claims for block range [%d : %d]", len(claims), fromBlock, maxEndBlock)

	// call the prover to generate the proof
	a.logger.Debugf("Calling AggchainProofClient to generate proof for block range [%d : %d]",
		fromBlock, maxEndBlock)

	aggchainProof, _, err := a.flow.GenerateAggchainProof(
		ctx,
		lastProvenBlock,
		maxEndBlock,
		claims,
	)
	if err != nil {
		return nil, fmt.Errorf("error generating Aggchain proof: %w", err)
	}

	a.logger.Infof("Generated Aggchain proof for block range [%d : %d]", fromBlock, maxEndBlock)

	return aggchainProof.SP1StarkProof, nil
}
