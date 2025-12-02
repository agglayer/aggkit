package flows

import (
	"context"
	"fmt"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/aggchainfep"
	"github.com/agglayer/aggkit/aggsender/config"
	"github.com/agglayer/aggkit/aggsender/query"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/aggsender/validator"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/opnode"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

// NewVerifierFlow creates a new verifier flow based on the provided configuration.
func NewVerifierFlow(
	ctx context.Context,
	cfg validator.Config,
	logger *log.Logger,
	l1Client aggkittypes.BaseEthereumClienter,
	l2Client aggkittypes.BaseEthereumClienter,
	l1InfoTreeSyncer types.L1InfoTreeSyncer,
	l2Syncer types.L2BridgeSyncer,
	rollupDataQuerier types.RollupDataQuerier,
	committeeQuerier types.MultisigQuerier,
) (types.AggsenderVerifierFlow, *CommonFlowComponents, error) {
	switch cfg.Mode {
	case types.PessimisticProofMode:
		commonFlowComponents, err := CreateCommonFlowComponents(
			ctx, logger,
			nil, // storage is not used in validator,
			l1Client, l2Client, l1InfoTreeSyncer, l2Syncer, rollupDataQuerier, committeeQuerier, 0, false,
			cfg.MaxCertSize, cfg.LerQuerier.RollupCreationBlockL1, cfg.DelayBetweenRetries.Duration, cfg.Signer,
			true, // full claims are (eventually) needed in validator mode
			true, // compact claims are needed in validator PP mode
			cfg.RequireCommitteeMembershipCheck,
			cfg.AgglayerBridgeL2Addr,
			cfg.GlobalExitRootL1Addr,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create common flow components: %w", err)
		}

		builderFlow := NewPPBuilderFlow(
			logger,
			commonFlowComponents.BaseFlow,
			nil, // storage is not used in validator
			commonFlowComponents.L1InfoTreeDataQuerier,
			commonFlowComponents.L2BridgeQuerier,
			commonFlowComponents.Signer,
			cfg.PPConfig.RequireOneBridgeInPPCertificate,
			cfg.MaxL2BlockNumber,
		)

		return NewPPVerifierFlow(builderFlow), commonFlowComponents, nil
	case types.AggchainProofMode:
		commonFlowComponents, err := CreateCommonFlowComponents(
			ctx, logger,
			nil, // storage is not used in validator,
			l1Client, l2Client, l1InfoTreeSyncer, l2Syncer, rollupDataQuerier, committeeQuerier,
			0, cfg.FEPConfig.RequireNoBlockGap,
			cfg.MaxCertSize, cfg.LerQuerier.RollupCreationBlockL1,
			cfg.DelayBetweenRetries.Duration, cfg.Signer,
			true,  // full claims are (eventually) needed in validator mode
			false, // compact claims are not needed in validator FEP mode
			cfg.RequireCommitteeMembershipCheck,
			cfg.AgglayerBridgeL2Addr,
			cfg.GlobalExitRootL1Addr,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create common flow components: %w", err)
		}

		fepInputsQuery, err := newFEPInputsQuerier(
			cfg.FEPConfig.SovereignRollupAddr,
			l1Client,
			cfg.FEPConfig.OpNodeURL,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create FEPInputsQuery: %w", err)
		}

		builderFlow := NewAggchainProverBuilderFlow(
			logger,
			NewAggchainProverFlowConfig(cfg.MaxL2BlockNumber),
			commonFlowComponents.BaseFlow,
			nil, // storage is not used in validator
			commonFlowComponents.L1InfoTreeDataQuerier,
			commonFlowComponents.L2BridgeQuerier,
			l1Client,
			commonFlowComponents.Signer,
			nil, // we don't query optimistic mode in validator
			nil, // we don't query the prover in validator mode
		)

		return NewAggchainProverVerifierFlow(builderFlow, fepInputsQuery), commonFlowComponents, nil
	default:
		return nil, nil, fmt.Errorf("unsupported Aggsender Validator mode: %s", cfg.Mode)
	}
}

// NewLocalVerifier creates a new local verifier flow based on the provided configuration.
func NewLocalVerifier(
	ctx context.Context,
	cfg config.Config,
	l1Client aggkittypes.BaseEthereumClienter,
	builderFlow types.AggsenderBuilderFlow,
) (types.AggsenderVerifierFlow, error) {
	switch cfg.Mode {
	case types.PessimisticProofMode:
		ppBuilderFlow, ok := builderFlow.(*PPBuilderFlow)
		if !ok {
			return nil,
				fmt.Errorf("expected PPBuilderFlow for PessimisticProofMode mode, got %T", builderFlow)
		}

		return NewPPVerifierFlow(ppBuilderFlow), nil
	case types.AggchainProofMode:
		if err := cfg.OptimisticModeConfig.Validate(); err != nil {
			return nil, fmt.Errorf("invalid optimistic mode config: %w", err)
		}
		builderFlow, ok := builderFlow.(*AggchainProverBuilderFlow)
		if !ok {
			return nil,
				fmt.Errorf("expected AggchainProverBuilderFlow for AggchainProofMode mode, got %T", builderFlow)
		}

		fepInputsQuery, err := newFEPInputsQuerier(
			cfg.SovereignRollupAddr,
			l1Client,
			cfg.OptimisticModeConfig.OpNodeURL,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create FEPInputsQuery: %w", err)
		}

		return NewAggchainProverVerifierFlow(builderFlow, fepInputsQuery), nil
	default:
		return nil, fmt.Errorf("unsupported Aggsender Validator mode: %s", cfg.Mode)
	}
}

// newFEPInputsQuerier creates a new instance of FEPInputsQuery
func newFEPInputsQuerier(
	aggchainFEPContractAddr common.Address,
	l1Client aggkittypes.BaseEthereumClienter,
	opNodeURL string,
) (types.FEPInputsQuerier, error) {
	aggChainFEPContract, err := aggchainfep.NewAggchainfepCaller(aggchainFEPContractAddr, l1Client)
	if err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error creating AggchainFEP rollup caller (%s): %w",
			aggchainFEPContractAddr.String(), err)
	}

	fepInputsQuery := query.NewFEPInputsQuery(
		aggChainFEPContract,
		aggchainFEPContractAddr,
		opnode.NewOpNodeClient(opNodeURL),
	)

	return fepInputsQuery, nil
}
