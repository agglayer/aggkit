package claimsponsor

import (
	"github.com/0xPolygon/zkevm-ethtx-manager/ethtxmanager"
	configTypes "github.com/agglayer/aggkit/config/types"
	"github.com/ethereum/go-ethereum/common"
)

// EVMClaimSponsorConfig holds all configuration parameters needed to initialize an EVMClaimSponsor.
type EVMClaimSponsorConfig struct {
	// DBPath is the path of the database file.
	DBPath string `mapstructure:"DBPath"`

	// Enabled indicates whether the sponsor should run or not.
	Enabled bool `mapstructure:"Enabled"`

	// SenderAddr is the Ethereum address that will be used to send claim transactions.
	SenderAddr common.Address `mapstructure:"SenderAddr"`

	// BridgeAddrL2 is the address of the bridge smart contract deployed on L2.
	BridgeAddrL2 common.Address `mapstructure:"BridgeAddrL2"`

	// MaxGas defines the maximum allowed gas limit for a sponsored claim transaction.
	MaxGas uint64 `mapstructure:"MaxGas"`

	// RetryAfterErrorPeriod defines the wait time after an unexpected error before retrying.
	RetryAfterErrorPeriod configTypes.Duration `mapstructure:"RetryAfterErrorPeriod"`

	// MaxRetryAttemptsAfterError defines the maximum consecutive retry attempts before panicking.
	// Any negative number means unlimited retries.
	MaxRetryAttemptsAfterError int `mapstructure:"MaxRetryAttemptsAfterError"`

	// WaitTxToBeMinedPeriod defines the polling interval to check if a transaction has been mined.
	WaitTxToBeMinedPeriod configTypes.Duration `mapstructure:"WaitTxToBeMinedPeriod"`

	// WaitOnEmptyQueue defines the wait time before checking again when the claim queue is empty.
	WaitOnEmptyQueue configTypes.Duration `mapstructure:"WaitOnEmptyQueue"`

	// EthTxManager holds the configuration for the Ethereum transaction manager used by the sponsor.
	EthTxManager ethtxmanager.Config `mapstructure:"EthTxManager"`

	// GasOffset is the additional gas to add on top of the estimated gas when sending transactions.
	GasOffset uint64 `mapstructure:"GasOffset"`
}
