package optimistic

import (
	"context"
	"fmt"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/aggchainfep"
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
		certBuildParams *types.CertificateBuildParams,
	) (common.Hash, error)
}

type OptimisticSignatureCalculatorImpl struct {
	QueryAggregationProofPublicValues OptimisticAggregationProofPublicValuesQuerier
	Signer                            signertypes.HashSigner
	Logger                            *log.Logger
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
	publicAddrSigner := signer.PublicAddress()
	trustedSequencerAddr, err := aggchainFEPContract.TrustedSequencer(nil)
	if err != nil {
		return nil, fmt.Errorf("optimisitc. error aggchainFEPContract.TrustedSequencer. Err: %w", err)
	}
	if publicAddrSigner != trustedSequencerAddr {
		return nil, fmt.Errorf("optimisitc. error signer.PublicAddress() %s != aggchainFEPContract.TrustedSequencer %s",
			publicAddrSigner.Hex(), trustedSequencerAddr.Hex())
	}

	logger.Infof("OptimisticSignatureCalculatorImpl.signerPublicKey: %s, trustedSequencerAddr: %s", signer.PublicAddress().Hex(),
		trustedSequencerAddr.Hex())
	query := NewOptimisticAggregationProofPublicValuesQuery(
		aggchainFEPContract,
		cfg.AggchainFEPAddr,
		opnode.NewOpNodeClient(cfg.OpNodeURL),
		signer.PublicAddress())

	return &OptimisticSignatureCalculatorImpl{
		QueryAggregationProofPublicValues: query,
		Signer:                            signer,
		Logger:                            logger,
	}, nil

}

func (o *OptimisticSignatureCalculatorImpl) Sign(ctx context.Context,
	aggchainReq types.AggchainProofRequest,
	newLocalExitRoot common.Hash,
	certBuildParams *types.CertificateBuildParams,
) ([]byte, error) {
	o.Logger.Debugf("OptimisticSignatureCalculatorImpl.Sign. L1InfoTreeLeaf.BlockNumber=%d", aggchainReq.L1InfoTreeLeaf.BlockNumber)
	aggregationProofPublicValues, err := o.QueryAggregationProofPublicValues.GetAggregationProofPublicValuesData(
		aggchainReq.LastProvenBlock,
		aggchainReq.RequestedEndBlock,
		aggchainReq.L1InfoTreeLeaf.PreviousBlockHash,
	)
	if err != nil {
		return nil, err
	}
	o.Logger.Infof("OptimisticSignatureCalculatorImpl.Sign agg:%s", aggregationProofPublicValues.String())
	aggregationProofPublicValuesHash, err := aggregationProofPublicValues.Hash()
	if err != nil {
		return nil, fmt.Errorf("aggregationProofPublicValues.Hash: error hashing aggregationProofPublicValues: %w", err)
	}
	importedBridgesHash := CalculateCommitImportedBrdigeExitsHashFromClaims(certBuildParams.Claims)
	optimisticSignature := OptimisticSignatureData{
		aggregationProofPublicValuesHash: aggregationProofPublicValuesHash,
		newLocalExitRoot:                 newLocalExitRoot,
		commitImportedBridgeExits:        importedBridgesHash,
	}
	hashToSign := optimisticSignature.Hash()

	signData, err := o.Signer.SignHash(ctx, hashToSign)
	if err != nil {
		return nil, fmt.Errorf("OptimisticSignatureData.Sign: error signing hash: %w", err)
	}
	return signData, nil
}
