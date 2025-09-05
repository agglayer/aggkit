package optimistic

import (
	"context"
	"fmt"
	"slices"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/fep/aggchain-ecdsa-multisig/aggchainfep"
	optimistichash "github.com/agglayer/aggkit/aggsender/optimistic/optimistichash"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/opnode"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/agglayer/go_signer/signer"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/common"
)

// OptimisticSignatureCalculatorImpl implements the OptimisticSignatureCalculator interface.
type OptimisticSignatureCalculatorImpl struct {
	queryAggregationProofPublicValues OptimisticAggregationProofPublicValuesQuerier
	signer                            signertypes.HashSigner
	logger                            *log.Logger
}

// NewOptimisticSignatureCalculatorImpl creates a new instance of OptimisticSignatureCalculatorImpl.
func NewOptimisticSignatureCalculatorImpl(
	ctx context.Context,
	logger *log.Logger,
	l1Client aggkittypes.BaseEthereumClienter,
	chainID uint64,
	cfg Config,
) (*OptimisticSignatureCalculatorImpl, error) {
	aggchainFEPContract, err := aggchainfep.NewAggchainfep(cfg.SovereignRollupAddr, l1Client)
	if err != nil {
		return nil, fmt.Errorf("[OPTIMISTIC] failed to create AggchainFEP contract binding. Err: %w", err)
	}
	signer, err := signer.NewSigner(ctx, chainID, cfg.TrustedSequencerKey, "optimistic", logger)
	if err != nil {
		return nil, fmt.Errorf("[OPTIMISTIC] failed to instantiate signer. Err: %w", err)
	}

	if err := signer.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("[OPTIMISTIC] failed to initialize signer. Err: %w", err)
	}
	signerAddr := signer.PublicAddress()
	signers, err := aggchainFEPContract.GetAggchainSigners(nil)
	if err != nil {
		err = fmt.Errorf("[OPTIMISTIC] failed to fetch the aggchain signers from the AggchainFEP contract. Err: %w", err)
		if cfg.RequireKeyMatchTrustedSequencer {
			return nil, err
		}
		logger.Warn(err.Error())
	}

	if len(signers) < 1 {
		err = fmt.Errorf("[OPTIMISTIC] there should be at least one aggchain signer in the AggchainFEP contract")
		if cfg.RequireKeyMatchTrustedSequencer {
			return nil, err
		}
		logger.Warn(err.Error())
	}

	signerIndex := slices.Index(signers, signerAddr)
	if signerIndex < 0 {
		err = fmt.Errorf("[OPTIMISTIC] "+
			"configured trusted signer address (%s) not found in the AggchainFEP contract signers: %v",
			signerAddr.Hex(), signers)
		if cfg.RequireKeyMatchTrustedSequencer {
			return nil, err
		}
		logger.Warn(err.Error())
	}

	trustedSignerAddr := signers[0]
	if err == nil && signerAddr != trustedSignerAddr {
		err := fmt.Errorf("[OPTIMISTIC] "+
			"configured trusted signer address (%s) differs from the one initialized on the AggchainFEP contract (%s)",
			signerAddr.Hex(), trustedSignerAddr.Hex())
		if cfg.RequireKeyMatchTrustedSequencer {
			return nil, err
		}
		logger.Warn(err.Error())
	}

	logger.Infof("OptimisticSignatureCalculatorImpl.signerAddress: %s, trustedSignerAddr: %s",
		signer.PublicAddress().Hex(),
		trustedSignerAddr.Hex())
	query := NewOptimisticAggregationProofPublicValuesQuery(
		aggchainFEPContract,
		cfg.SovereignRollupAddr,
		opnode.NewOpNodeClient(cfg.OpNodeURL),
		signer.PublicAddress())

	return &OptimisticSignatureCalculatorImpl{
		queryAggregationProofPublicValues: query,
		signer:                            signer,
		logger:                            logger,
	}, nil
}

// Sign calculate hash and sign it.
// It returns the signed hash, extra data for logging, and an error if any.
func (o *OptimisticSignatureCalculatorImpl) Sign(ctx context.Context,
	aggchainReq types.AggchainProofRequest,
	newLocalExitRoot common.Hash,
	claims []bridgesync.Claim,
) ([]byte, string, error) {
	o.logger.Debugf("OptimisticSignatureCalculatorImpl.Sign. L1InfoTreeLeaf.BlockNumber=%d",
		aggchainReq.L1InfoTreeLeaf.BlockNumber)
	aggregationProofPublicValues, err := o.queryAggregationProofPublicValues.GetAggregationProofPublicValuesData(
		aggchainReq.LastProvenBlock,
		aggchainReq.RequestedEndBlock,
		aggchainReq.L1InfoTreeLeaf.PreviousBlockHash,
	)
	if err != nil {
		return nil, "", err
	}
	o.logger.Infof("OptimisticSignatureCalculatorImpl.Sign agg:%s", aggregationProofPublicValues.String())
	aggregationProofPublicValuesHash, err := aggregationProofPublicValues.Hash()
	if err != nil {
		return nil, "", fmt.Errorf("aggregationProofPublicValues.Hash: error hashing aggregationProofPublicValues: %w", err)
	}
	importedBridgesHash := optimistichash.CalculateCommitImportedBrdigeExitsHashFromClaims(claims)

	optimisticSignature := optimistichash.OptimisticSignatureData{
		AggregationProofPublicValuesHash: aggregationProofPublicValuesHash,
		NewLocalExitRoot:                 newLocalExitRoot,
		CommitImportedBridgeExits:        importedBridgesHash,
	}
	o.logger.Infof("OptimisticSignatureCalculatorImpl.Sign %s", optimisticSignature.String())
	hashToSign := optimisticSignature.Hash()
	o.logger.Infof("OptimisticSignatureCalculatorImpl.Sign signed_commitment:%s", hashToSign.Hex())
	signData, err := o.signer.SignHash(ctx, hashToSign)
	if err != nil {
		return nil, "", fmt.Errorf("OptimisticSignatureData.Sign: Fails to sign. SignData:%s . Err: %w",
			optimisticSignature.String(), err)
	}
	extraData := fmt.Sprintf(
		"aggregationProofPublicValues: %s. signData:%s (num_claims: %d) "+
			"hashToSign: %s",
		aggregationProofPublicValues.String(), optimisticSignature.String(),
		len(claims), hashToSign.Hex())

	return signData, extraData, nil
}
