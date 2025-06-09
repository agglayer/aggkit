package optimistic

import (
	"context"
	"fmt"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/aggchainfep"
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
	cfg Config,
) (*OptimisticSignatureCalculatorImpl, error) {
	aggchainFEPContract, err := aggchainfep.NewAggchainfep(cfg.SovereignRollupAddr, l1Client)
	if err != nil {
		return nil, fmt.Errorf("newOptimisticSignatureCalculatorImpl.NewAggchainfep Err: %w", err)
	}
	signer, err := signer.NewSigner(ctx, 0, cfg.TrustedSequencerKey, "optimistic", logger)
	if err != nil {
		return nil, fmt.Errorf("optimistic. error NewSigner. Err: %w", err)
	}

	if err := signer.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("optimistic. error signer.Initialize. Err: %w", err)
	}
	publicAddrSigner := signer.PublicAddress()
	trustedSequencerAddr, err := aggchainFEPContract.TrustedSequencer(nil)
	if err != nil {
		err = fmt.Errorf("optimistic. error aggchainFEPContract.TrustedSequencer. Err: %w", err)
		if cfg.RequireKeyMatchTrustedSequencer {
			return nil, err
		}
		logger.Warn(err.Error())
	}
	if err == nil && publicAddrSigner != trustedSequencerAddr {
		err := fmt.Errorf("optimistic. error signer.PublicAddress() %s != aggchainFEPContract.TrustedSequencer %s",
			publicAddrSigner.Hex(), trustedSequencerAddr.Hex())
		if cfg.RequireKeyMatchTrustedSequencer {
			return nil, err
		}
		logger.Warn(err.Error())
	}

	logger.Infof("OptimisticSignatureCalculatorImpl.signerPublicKey: %s, trustedSequencerAddr: %s",
		signer.PublicAddress().Hex(),
		trustedSequencerAddr.Hex())
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
