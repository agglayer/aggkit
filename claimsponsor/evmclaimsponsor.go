package claimsponsor

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/polygonzkevmbridgev2"
	ethtxtypes "github.com/0xPolygon/zkevm-ethtx-manager/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

const (
	// LeafTypeAsset represents a bridge asset
	LeafTypeAsset uint8 = 0
	// LeafTypeMessage represents a bridge message
	LeafTypeMessage uint8 = 1
)

var ErrGasEstimateTooHigh = errors.New(
	"claim gas estimate exceeds maximum gas allowed by claim sponsor service",
)

type EthClienter interface {
	ethereum.GasEstimator
	bind.ContractBackend
}

type EthTxManager interface {
	Remove(ctx context.Context, id common.Hash) error
	ResultsByStatus(ctx context.Context, statuses []ethtxtypes.MonitoredTxStatus) ([]ethtxtypes.MonitoredTxResult, error)
	Result(ctx context.Context, id common.Hash) (ethtxtypes.MonitoredTxResult, error)
	Add(ctx context.Context, to *common.Address, value *big.Int, data []byte,
		gasOffset uint64, sidecar *types.BlobTxSidecar) (common.Hash, error)
}

type EVMClaimSponsor struct {
	l2Client     EthClienter
	bridgeABI    *abi.ABI
	bridgeAddr   common.Address
	ethTxManager EthTxManager
	sender       common.Address
	gasOffset    uint64
	maxGas       uint64
}

func NewEVMClaimSponsor(
	logger *log.Logger,
	dbPath string,
	l2Client EthClienter,
	bridgeAddr common.Address,
	sender common.Address,
	maxGas, gasOffset uint64,
	ethTxManager EthTxManager,
	retryAfterErrorPeriod time.Duration,
	maxRetryAttemptsAfterError int,
	waitTxToBeMinedPeriod time.Duration,
	waitOnEmptyQueue time.Duration,
) (*ClaimSponsor, error) {
	abi, err := polygonzkevmbridgev2.Polygonzkevmbridgev2MetaData.GetAbi()
	if err != nil {
		return nil, err
	}

	evmSponsor := &EVMClaimSponsor{
		l2Client:     l2Client,
		bridgeABI:    abi,
		bridgeAddr:   bridgeAddr,
		sender:       sender,
		gasOffset:    gasOffset,
		maxGas:       maxGas,
		ethTxManager: ethTxManager,
	}

	baseSponsor, err := newClaimSponsor(
		logger,
		dbPath,
		evmSponsor,
		retryAfterErrorPeriod,
		maxRetryAttemptsAfterError,
		waitTxToBeMinedPeriod,
		waitOnEmptyQueue,
	)
	if err != nil {
		return nil, err
	}

	return baseSponsor, nil
}

func (c *EVMClaimSponsor) checkClaim(ctx context.Context, claim *Claim, data []byte) error {
	// if maxGas is zero, that means “no limit”
	if c.maxGas == 0 {
		return nil
	}

	gas, err := c.l2Client.EstimateGas(ctx, ethereum.CallMsg{
		From: c.sender,
		To:   &c.bridgeAddr,
		Data: data,
	})
	if err != nil {
		return err
	}
	if gas > c.maxGas {
		return fmt.Errorf(
			"%w: estimated %d, maximum allowed: %d",
			ErrGasEstimateTooHigh, gas, c.maxGas,
		)
	}

	return nil
}

func (c *EVMClaimSponsor) sendClaim(ctx context.Context, claim *Claim) (string, error) {
	data, err := c.buildClaimTxData(claim)
	if err != nil {
		return "", err
	}

	if err := c.checkClaim(ctx, claim, data); err != nil {
		return "", err
	}

	id, err := c.ethTxManager.Add(ctx, &c.bridgeAddr, common.Big0, data, c.gasOffset, nil)
	if err != nil {
		return "", err
	}

	return id.Hex(), nil
}

func (c *EVMClaimSponsor) claimStatus(ctx context.Context, id string) (ClaimStatus, error) {
	res, err := c.ethTxManager.Result(ctx, common.HexToHash(id))
	if err != nil {
		return "", err
	}
	switch res.Status {
	case ethtxtypes.MonitoredTxStatusCreated,
		ethtxtypes.MonitoredTxStatusSent:
		return WIPClaimStatus, nil
	case ethtxtypes.MonitoredTxStatusFailed:
		return FailedClaimStatus, nil
	case ethtxtypes.MonitoredTxStatusMined,
		ethtxtypes.MonitoredTxStatusSafe,
		ethtxtypes.MonitoredTxStatusFinalized:
		log.Infof("claim tx with id %s mined at block %d", id, res.MinedAtBlockNumber)

		return SuccessClaimStatus, nil
	default:
		return "", fmt.Errorf("unexpected tx status: %v", res.Status)
	}
}

func (c *EVMClaimSponsor) buildClaimTxData(claim *Claim) ([]byte, error) {
	switch claim.LeafType {
	case LeafTypeAsset:
		return c.bridgeABI.Pack(
			"claimAsset",
			claim.ProofLocalExitRoot,  // bytes32[32] smtProofLocalExitRoot
			claim.ProofRollupExitRoot, // bytes32[32] smtProofRollupExitRoot
			claim.GlobalIndex,         // uint256 globalIndex
			claim.MainnetExitRoot,     // bytes32 mainnetExitRoot
			claim.RollupExitRoot,      // bytes32 rollupExitRoot
			claim.OriginNetwork,       // uint32 originNetwork
			claim.OriginTokenAddress,  // address originTokenAddress,
			claim.DestinationNetwork,  // uint32 destinationNetwork
			claim.DestinationAddress,  // address destinationAddress
			claim.Amount,              // uint256 amount
			claim.Metadata,            // bytes metadata
		)
	case LeafTypeMessage:
		return c.bridgeABI.Pack(
			"claimMessage",
			claim.ProofLocalExitRoot,  // bytes32[32] smtProofLocalExitRoot
			claim.ProofRollupExitRoot, // bytes32[32] smtProofRollupExitRoot
			claim.GlobalIndex,         // uint256 globalIndex
			claim.MainnetExitRoot,     // bytes32 mainnetExitRoot
			claim.RollupExitRoot,      // bytes32 rollupExitRoot
			claim.OriginNetwork,       // uint32 originNetwork
			claim.OriginTokenAddress,  // address originTokenAddress,
			claim.DestinationNetwork,  // uint32 destinationNetwork
			claim.DestinationAddress,  // address destinationAddress
			claim.Amount,              // uint256 amount
			claim.Metadata,            // bytes metadata
		)
	default:
		return nil, fmt.Errorf("unexpected leaf type %d", claim.LeafType)
	}
}
