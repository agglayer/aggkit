package optimistic

import (
	"context"
	"fmt"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/aggchainfep"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/opnode"
	"github.com/agglayer/go_signer/signer"

	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/common"
)

type OptimisticSignatureCalculator interface {
	Sign(ctx context.Context,
		aggchainReq types.AggchainProofRequest,
		newLocalExitRoot common.Hash,
		importedBridges []*agglayertypes.ImportedBridgeExit,
	) (common.Hash, error)
}

type OptimisticSignatureCalculatorImpl struct {
	QueryAggregationProofPublicValues OptimisticAggregationProofPublicValuesQuerier
	Signer                            signertypes.HashSigner
}

func NewOptimisticSignatureCalculatorImpl(
	ctx context.Context,
	logger *log.Logger,
	l1Client types.EthClient,
	cfg Config,
) (*OptimisticSignatureCalculatorImpl, error) {
	aggchainFEPContract, err := aggchainfep.NewAggchainfep(cfg.AggchainFEPAddr, l1Client)
	if err != nil {
		return nil, fmt.Errorf("newOptimisticSignatureCalculatorImpl.NewAggchainfep Err: %w", err)
	}
	signer, err := signer.NewSigner(ctx, 0, cfg.SignPrivateKey, "optimistic", logger)
	if err != nil {
		return nil, fmt.Errorf("optimisitc. error NewSigner. Err: %w", err)
	}

	if err := signer.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("optimisitc. error signer.Initialize. Err: %w", err)
	}
	query := NewOptimisticAggregationProofPublicValuesQuery(
		aggchainFEPContract,
		cfg.AggchainFEPAddr,
		opnode.NewOpNodeClient(cfg.OpNodeURL),
		signer.PublicAddress())
	return &OptimisticSignatureCalculatorImpl{
		QueryAggregationProofPublicValues: query,
		Signer:                            signer,
	}, nil

}

func (o *OptimisticSignatureCalculatorImpl) Sign(ctx context.Context,
	aggchainReq types.AggchainProofRequest,
	newLocalExitRoot common.Hash,
	importedBridges []*agglayertypes.ImportedBridgeExit,
) (common.Hash, error) {
	aggregationProofPublicValues, err := o.QueryAggregationProofPublicValues.GetAggregationProofPublicValuesData(
		aggchainReq.LastProvenBlock,
		aggchainReq.RequestedEndBlock,
		aggchainReq.L1InfoTreeLeaf.Hash,
	)
	if err != nil {
		return common.Hash{}, err
	}
	importedBridgesHash := CalculateCommitImportedBridgeExitsHash(importedBridges)
	signature, err := OptimisticSign(ctx, &OptimisticSignatureData{
		aggregationProofPublicValues: *aggregationProofPublicValues,
		newLocalExitRoot:             newLocalExitRoot,
		commitImportedBridgeExits:    importedBridgesHash,
	}, o.Signer)
	if err != nil {
		return common.Hash{}, err
	}
	return signature, nil
}
