package config

import (
	"github.com/ethereum/go-ethereum/common"
)

// L1Config represents the configuration of the network used in L1
type L1Config struct {
	// URL is the URL of the Ethereum node for L1
	URL string `mapstructure:"URL"`
	// Chain ID of the L1 network
	L1ChainID uint64 `json:"chainId"`
	// RollupAddr Address of the L1 rollup contract
	RollupAddr common.Address `json:"polygonZkEVMAddress"`
	// RollupManagerAddr Address of the L1 contract
	RollupManagerAddr common.Address `json:"polygonRollupManagerAddress"`
	// POLTokenAddr Address of the L1 POL token Contract
	POLTokenAddr common.Address `json:"polTokenAddress"`
	// GlobalExitRootManagerAddr Address of the L1 GlobalExitRootManager contract
	GlobalExitRootManagerAddr common.Address `json:"polygonZkEVMGlobalExitRootAddress"`
}
