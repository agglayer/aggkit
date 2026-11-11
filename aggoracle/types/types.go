package types

import (
	"context"
	"math/big"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayergerl2"
	ethtxtypes "github.com/0xPolygon/zkevm-ethtx-manager/types"
	"github.com/agglayer/aggkit/aggoracle/contracts/aggoraclecommitteev2"
	"github.com/agglayer/aggkit/aggoracle/gervalidator"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// EthTxManager is an interface to interact with the EthTxManager
type EthTxManager interface {
	Remove(ctx context.Context, id common.Hash) error
	ResultsByStatus(ctx context.Context,
		statuses []ethtxtypes.MonitoredTxStatus,
	) ([]ethtxtypes.MonitoredTxResult, error)
	Result(ctx context.Context, id common.Hash) (ethtxtypes.MonitoredTxResult, error)
	Add(ctx context.Context,
		to *common.Address,
		value *big.Int,
		data []byte,
		gasOffset uint64,
		sidecar *types.BlobTxSidecar,
	) (common.Hash, error)
	From() common.Address
}

// L2GERManagerContract is an interface to interact with the GlobalExitRootManager contract
type L2GERManagerContract interface {
	GlobalExitRootMap(opts *bind.CallOpts, ger [common.HashLength]byte) (*big.Int, error)
	BridgeAddress(*bind.CallOpts) (common.Address, error)
	FilterUpdateHashChainValue(opts *bind.FilterOpts,
		newGlobalExitRoot [][common.HashLength]byte, newHashChainValue [][common.HashLength]byte) (
		*agglayergerl2.Agglayergerl2UpdateHashChainValueIterator, error)
	FilterUpdateRemovalHashChainValue(
		opts *bind.FilterOpts,
		removedGlobalExitRoot [][common.HashLength]byte,
		newRemovalHashChainValue [][common.HashLength]byte) (
		*agglayergerl2.Agglayergerl2UpdateRemovalHashChainValueIterator,
		error,
	)
	GlobalExitRootUpdater(opts *bind.CallOpts) (common.Address, error)
}

// AggOracleCommitteeV2Contract is an interface to interact with the AggOracleCommittee v2 contract
type AggOracleCommitteeV2Contract interface {
	// Version returns the version of the contract ("v2.x.y")
	Version(opts *bind.CallOpts) (string, error)
	// Threshold returns the number of signatures that injectGER requires
	Threshold(opts *bind.CallOpts) (*big.Int, error)
	// Proposer returns the only address allowed to call injectGER
	Proposer(opts *bind.CallOpts) (common.Address, error)
	// AgglayerGERL2 returns the L2 GER manager the committee inserts the GERs on
	AgglayerGERL2(opts *bind.CallOpts) (common.Address, error)
	// GetValidatorsInfo returns the address and the URL of the current validators
	GetValidatorsInfo(opts *bind.CallOpts) ([]aggoraclecommitteev2.IAggOracleCommitteeValidatorInfo, error)
	// InjectedGERs tells if the GER was already processed by the committee. A GER can't be injected twice
	InjectedGERs(opts *bind.CallOpts, globalExitRoot [common.HashLength]byte) (bool, error)
	// VerifyGER returns true if the signatures are valid for the GER, and reverts otherwise
	VerifyGER(opts *bind.CallOpts, globalExitRoot [common.HashLength]byte, signatures []byte) (bool, error)
}

// GERSignatureCollector collects the signatures that AggOracleCommittee v2 requires to inject a GER
type GERSignatureCollector interface {
	// Collect asks the validators to sign the GER and returns exactly threshold signatures,
	// ordered as the contract requires
	Collect(ctx context.Context, ger common.Hash, validators []gervalidator.Validator,
		threshold int) (*gervalidator.Result, error)
}

// AggOracleCommitteeContract is an interface to interact with the AggOracleCommittee contract
type AggOracleCommitteeContract interface {
	// View method to check oracle manager state
	GetAggOracleMemberIndex(opts *bind.CallOpts, oracleMember common.Address) (*big.Int, error)

	// View method to check the last proposed GER by an oracle member
	AddressToLastProposedGER(opts *bind.CallOpts, oracleMember common.Address) ([common.HashLength]byte, error)
}
