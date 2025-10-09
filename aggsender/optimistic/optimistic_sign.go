package optimistic

import (
	"context"
	"fmt"

	optimistichash "github.com/agglayer/aggkit/aggsender/optimistic/optimistichash"
	"github.com/agglayer/aggkit/aggsender/query"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/opnode"
	"github.com/agglayer/go_signer/signer"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/common"
)

// OptimisticSignatureCalculatorImpl implements the OptimisticSignatureCalculator interface.
type OptimisticSignatureCalculatorImpl struct {
	queryAggregationProofPublicValues types.AggProofPublicValuesQuerier
	signer                            signertypes.HashSigner
	logger                            *log.Logger
}

// NewOptimisticSignatureCalculatorImpl creates a new instance of OptimisticSignatureCalculatorImpl.
func NewOptimisticSignatureCalculatorImpl(
	ctx context.Context,
	logger *log.Logger,
	aggchainFEPContract types.FEPContractQuerier,
	chainID uint64,
	cfg Config,
) (*OptimisticSignatureCalculatorImpl, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("[OPTIMISTIC] invalid config: %w", err)
	}
	signer, err := signer.NewSigner(ctx, chainID, cfg.TrustedSequencerKey, "optimistic", logger)
	if err != nil {
		return nil, fmt.Errorf("[OPTIMISTIC] failed to instantiate signer. Err: %w", err)
	}

	if err := signer.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("[OPTIMISTIC] failed to initialize signer. Err: %w", err)
	}

	signerAddr := signer.PublicAddress()
	trustedSignerAddr, err := validateSignerAgainstContract(
		logger,
		aggchainFEPContract,
		signerAddr,
		cfg.RequireKeyMatchTrustedSequencer,
	)
	if err != nil {
		return nil, err
	}

	logger.Infof("OptimisticSignatureCalculatorImpl.signerAddress: %s, trustedSignerAddr: %s",
		signerAddr.Hex(),
		trustedSignerAddr.Hex(),
	)

	query := query.NewAggProofPublicValuesQuery(
		aggchainFEPContract,
		cfg.SovereignRollupAddr,
		opnode.NewOpNodeClient(cfg.OpNodeURL),
		signerAddr,
	)

	return &OptimisticSignatureCalculatorImpl{
		queryAggregationProofPublicValues: query,
		signer:                            signer,
		logger:                            logger,
	}, nil
}

// validateSignerAgainstContract ensures the signer is present in the AggchainFEP contract signers
// and matches the trusted signer address if required.
func validateSignerAgainstContract(
	logger *log.Logger,
	contract types.FEPContractQuerier,
	signerAddr common.Address,
	requireKeyMatch bool,
) (common.Address, error) {
	trustedSignerAddress, err := query.GetTrustedSignerAddr(contract)
	if err != nil {
		err = fmt.Errorf("[OPTIMISTIC] failed to fetch the aggchain signers from the AggchainFEP contract. Err: %w", err)
		if requireKeyMatch {
			return common.Address{}, err
		}
		logger.Warn(err.Error())
	}

	if err == nil && signerAddr != trustedSignerAddress {
		err := fmt.Errorf("[OPTIMISTIC] "+
			"configured trusted signer address (%s) differs from the one initialized on the AggchainFEP contract (%s)",
			signerAddr.Hex(), trustedSignerAddress.Hex())
		if requireKeyMatch {
			return trustedSignerAddress, err
		}
		logger.Warn(err.Error())
	}

	return trustedSignerAddress, nil
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
