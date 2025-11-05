package types

import (
	"context"
	"math/big"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayergerl2"
	ethtxtypes "github.com/0xPolygon/zkevm-ethtx-manager/types"
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

// AggOracleCommitteeContract is an interface to interact with the AggOracleCommittee contract
type AggOracleCommitteeContract interface {
	// View method to check oracle manager state
	GetAggOracleMemberIndex(opts *bind.CallOpts, oracleMember common.Address) (*big.Int, error)

	// View method to check the last proposed GER by an oracle member
	AddressToLastProposedGER(opts *bind.CallOpts, oracleMember common.Address) ([common.HashLength]byte, error)
}
