// Code generated - DO NOT EDIT.
// This file is a generated binding and any manual changes will be lost.

package rollupmanagermock

import (
	"errors"
	"math/big"
	"strings"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/event"
)

// Reference imports to suppress errors if they are not otherwise used.
var (
	_ = errors.New
	_ = big.NewInt
	_ = strings.NewReader
	_ = ethereum.NotFound
	_ = bind.Bind
	_ = common.Big1
	_ = types.BloomLookup
	_ = event.NewSubscription
	_ = abi.ConvertType
)

// RollupmanagermockMetaData contains all meta data concerning the Rollupmanagermock contract.
var RollupmanagermockMetaData = &bind.MetaData{
	ABI: "[{\"anonymous\":false,\"inputs\":[{\"indexed\":true,\"internalType\":\"uint32\",\"name\":\"rollupID\",\"type\":\"uint32\"},{\"indexed\":false,\"internalType\":\"uint64\",\"name\":\"forkID\",\"type\":\"uint64\"},{\"indexed\":false,\"internalType\":\"address\",\"name\":\"rollupAddress\",\"type\":\"address\"},{\"indexed\":false,\"internalType\":\"uint64\",\"name\":\"chainID\",\"type\":\"uint64\"},{\"indexed\":false,\"internalType\":\"uint8\",\"name\":\"rollupVerifierType\",\"type\":\"uint8\"},{\"indexed\":false,\"internalType\":\"uint64\",\"name\":\"lastVerifiedBatchBeforeUpgrade\",\"type\":\"uint64\"},{\"indexed\":false,\"internalType\":\"bytes32\",\"name\":\"programVKey\",\"type\":\"bytes32\"},{\"indexed\":false,\"internalType\":\"bytes32\",\"name\":\"initPessimisticRoot\",\"type\":\"bytes32\"}],\"name\":\"AddExistingRollup\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":true,\"internalType\":\"uint32\",\"name\":\"rollupID\",\"type\":\"uint32\"},{\"indexed\":false,\"internalType\":\"uint32\",\"name\":\"rollupTypeID\",\"type\":\"uint32\"},{\"indexed\":false,\"internalType\":\"address\",\"name\":\"rollupAddress\",\"type\":\"address\"},{\"indexed\":false,\"internalType\":\"uint64\",\"name\":\"chainID\",\"type\":\"uint64\"},{\"indexed\":false,\"internalType\":\"uint8\",\"name\":\"rollupVerifierType\",\"type\":\"uint8\"},{\"indexed\":false,\"internalType\":\"bytes\",\"name\":\"initializeBytesAggchain\",\"type\":\"bytes\"}],\"name\":\"CreateNewAggchain\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":true,\"internalType\":\"uint32\",\"name\":\"rollupID\",\"type\":\"uint32\"},{\"indexed\":false,\"internalType\":\"uint32\",\"name\":\"rollupTypeID\",\"type\":\"uint32\"},{\"indexed\":false,\"internalType\":\"address\",\"name\":\"rollupAddress\",\"type\":\"address\"},{\"indexed\":false,\"internalType\":\"uint64\",\"name\":\"chainID\",\"type\":\"uint64\"},{\"indexed\":false,\"internalType\":\"address\",\"name\":\"gasTokenAddress\",\"type\":\"address\"}],\"name\":\"CreateNewRollup\",\"type\":\"event\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"rollupAddress\",\"type\":\"address\"},{\"internalType\":\"address\",\"name\":\"verifier\",\"type\":\"address\"},{\"internalType\":\"uint64\",\"name\":\"forkID\",\"type\":\"uint64\"},{\"internalType\":\"uint64\",\"name\":\"chainID\",\"type\":\"uint64\"},{\"internalType\":\"bytes32\",\"name\":\"initRoot\",\"type\":\"bytes32\"},{\"internalType\":\"uint8\",\"name\":\"rollupVerifierType\",\"type\":\"uint8\"},{\"internalType\":\"bytes32\",\"name\":\"programVKey\",\"type\":\"bytes32\"},{\"internalType\":\"bytes32\",\"name\":\"initPessimisticRoot\",\"type\":\"bytes32\"}],\"name\":\"addExistingRollup\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[],\"name\":\"rollupCount\",\"outputs\":[{\"internalType\":\"uint32\",\"name\":\"\",\"type\":\"uint32\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"uint32\",\"name\":\"\",\"type\":\"uint32\"}],\"name\":\"rollupIDToRollupData\",\"outputs\":[{\"internalType\":\"address\",\"name\":\"rollupContract\",\"type\":\"address\"},{\"internalType\":\"uint64\",\"name\":\"chainID\",\"type\":\"uint64\"},{\"internalType\":\"address\",\"name\":\"verifier\",\"type\":\"address\"},{\"internalType\":\"uint64\",\"name\":\"forkID\",\"type\":\"uint64\"},{\"internalType\":\"bytes32\",\"name\":\"lastLocalExitRoot\",\"type\":\"bytes32\"},{\"internalType\":\"uint64\",\"name\":\"lastBatchSequenced\",\"type\":\"uint64\"},{\"internalType\":\"uint64\",\"name\":\"lastVerifiedBatch\",\"type\":\"uint64\"},{\"internalType\":\"uint64\",\"name\":\"legacyLastPendingState\",\"type\":\"uint64\"},{\"internalType\":\"uint64\",\"name\":\"legacyLastPendingStateConsolidated\",\"type\":\"uint64\"},{\"internalType\":\"uint64\",\"name\":\"lastVerifiedBatchBeforeUpgrade\",\"type\":\"uint64\"},{\"internalType\":\"uint64\",\"name\":\"rollupTypeID\",\"type\":\"uint64\"},{\"internalType\":\"uint8\",\"name\":\"rollupVerifierType\",\"type\":\"uint8\"}],\"stateMutability\":\"view\",\"type\":\"function\"}]",
	Bin: "0x6080806040523461001657610415908161001c8239f35b600080fdfe6080604052600436101561001257600080fd5b60003560e01c8063e80e503014610136578063f4e92675146101125763f9c4c2ae1461003d57600080fd5b3461010d57602036600319011261010d5760043563ffffffff811680910361010d576000526001602052610180604060002060ff815491600181015460028201549160046003820154910154926040519560018060a01b0393848216885267ffffffffffffffff9485809360a01c1660208a01528116604089015260a01c166060870152608086015281811660a0860152818160401c1660c0860152818160801c1660e086015260c01c6101008501528082166101208501528160401c1661014084015260801c16610160820152f35b600080fd5b3461010d57600036600319011261010d57602063ffffffff60005416604051908152f35b3461010d5761010036600319011261010d576004356001600160a01b0381169081900361010d576024356001600160a01b0381169081900361010d576044359067ffffffffffffffff821680920361010d576064359167ffffffffffffffff831680930361010d5760a4359160ff8316830361010d576000549263ffffffff94600186861601948686116103c95763ffffffff191685871617600055610200956040968752608088905260a082815260c09490945260e08581526084356101005260006101208190526101408190526101608190526101808190526101a08190526101c081905260ff8581166101e052978316815260016020529790972080546001600160e01b0319166001600160a01b038a16179483901b67ffffffffffffffff60a01b169490941784557f4da47f6e9bbd9ef91887183a576aaebcf1b9bb7d2a567b33b075044c6d36082e9695936102d56001600160a01b031960c05160018401805460e05167ffffffffffffffff60a01b60a09190911b166001600160a01b03909316931667ffffffffffffffff60a01b191692909217179055565b6101005160028201556101205161014051608089810151610180516001600160c01b031960c091821b1667ffffffffffffffff60801b92841b929092166fffffffffffffffff0000000000000000604095861b811667ffffffffffffffff97881617919091179290921760038701556101a051600490960180546101c0516101e0518d16861b90871b909416979096166fffffffffffffffffffffffffffffffff19909616959095179590951789831b191617909255600080548251998a5260208a019c909c52908801949094529390941660608601529184015260c43560a084015260e4359183019190915290931692a2005b634e487b7160e01b600052601160045260246000fdfea2646970667358221220ea14ead8783b2a195687b46e896268da27700d64a0c2cdfe3e9fcd979c011b7464736f6c63430008120033",
}

// RollupmanagermockABI is the input ABI used to generate the binding from.
// Deprecated: Use RollupmanagermockMetaData.ABI instead.
var RollupmanagermockABI = RollupmanagermockMetaData.ABI

// RollupmanagermockBin is the compiled bytecode used for deploying new contracts.
// Deprecated: Use RollupmanagermockMetaData.Bin instead.
var RollupmanagermockBin = RollupmanagermockMetaData.Bin

// DeployRollupmanagermock deploys a new Ethereum contract, binding an instance of Rollupmanagermock to it.
func DeployRollupmanagermock(auth *bind.TransactOpts, backend bind.ContractBackend) (common.Address, *types.Transaction, *Rollupmanagermock, error) {
	parsed, err := RollupmanagermockMetaData.GetAbi()
	if err != nil {
		return common.Address{}, nil, nil, err
	}
	if parsed == nil {
		return common.Address{}, nil, nil, errors.New("GetABI returned nil")
	}

	address, tx, contract, err := bind.DeployContract(auth, *parsed, common.FromHex(RollupmanagermockBin), backend)
	if err != nil {
		return common.Address{}, nil, nil, err
	}
	return address, tx, &Rollupmanagermock{RollupmanagermockCaller: RollupmanagermockCaller{contract: contract}, RollupmanagermockTransactor: RollupmanagermockTransactor{contract: contract}, RollupmanagermockFilterer: RollupmanagermockFilterer{contract: contract}}, nil
}

// Rollupmanagermock is an auto generated Go binding around an Ethereum contract.
type Rollupmanagermock struct {
	RollupmanagermockCaller     // Read-only binding to the contract
	RollupmanagermockTransactor // Write-only binding to the contract
	RollupmanagermockFilterer   // Log filterer for contract events
}

// RollupmanagermockCaller is an auto generated read-only Go binding around an Ethereum contract.
type RollupmanagermockCaller struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// RollupmanagermockTransactor is an auto generated write-only Go binding around an Ethereum contract.
type RollupmanagermockTransactor struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// RollupmanagermockFilterer is an auto generated log filtering Go binding around an Ethereum contract events.
type RollupmanagermockFilterer struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// RollupmanagermockSession is an auto generated Go binding around an Ethereum contract,
// with pre-set call and transact options.
type RollupmanagermockSession struct {
	Contract     *Rollupmanagermock // Generic contract binding to set the session for
	CallOpts     bind.CallOpts      // Call options to use throughout this session
	TransactOpts bind.TransactOpts  // Transaction auth options to use throughout this session
}

// RollupmanagermockCallerSession is an auto generated read-only Go binding around an Ethereum contract,
// with pre-set call options.
type RollupmanagermockCallerSession struct {
	Contract *RollupmanagermockCaller // Generic contract caller binding to set the session for
	CallOpts bind.CallOpts            // Call options to use throughout this session
}

// RollupmanagermockTransactorSession is an auto generated write-only Go binding around an Ethereum contract,
// with pre-set transact options.
type RollupmanagermockTransactorSession struct {
	Contract     *RollupmanagermockTransactor // Generic contract transactor binding to set the session for
	TransactOpts bind.TransactOpts            // Transaction auth options to use throughout this session
}

// RollupmanagermockRaw is an auto generated low-level Go binding around an Ethereum contract.
type RollupmanagermockRaw struct {
	Contract *Rollupmanagermock // Generic contract binding to access the raw methods on
}

// RollupmanagermockCallerRaw is an auto generated low-level read-only Go binding around an Ethereum contract.
type RollupmanagermockCallerRaw struct {
	Contract *RollupmanagermockCaller // Generic read-only contract binding to access the raw methods on
}

// RollupmanagermockTransactorRaw is an auto generated low-level write-only Go binding around an Ethereum contract.
type RollupmanagermockTransactorRaw struct {
	Contract *RollupmanagermockTransactor // Generic write-only contract binding to access the raw methods on
}

// NewRollupmanagermock creates a new instance of Rollupmanagermock, bound to a specific deployed contract.
func NewRollupmanagermock(address common.Address, backend bind.ContractBackend) (*Rollupmanagermock, error) {
	contract, err := bindRollupmanagermock(address, backend, backend, backend)
	if err != nil {
		return nil, err
	}
	return &Rollupmanagermock{RollupmanagermockCaller: RollupmanagermockCaller{contract: contract}, RollupmanagermockTransactor: RollupmanagermockTransactor{contract: contract}, RollupmanagermockFilterer: RollupmanagermockFilterer{contract: contract}}, nil
}

// NewRollupmanagermockCaller creates a new read-only instance of Rollupmanagermock, bound to a specific deployed contract.
func NewRollupmanagermockCaller(address common.Address, caller bind.ContractCaller) (*RollupmanagermockCaller, error) {
	contract, err := bindRollupmanagermock(address, caller, nil, nil)
	if err != nil {
		return nil, err
	}
	return &RollupmanagermockCaller{contract: contract}, nil
}

// NewRollupmanagermockTransactor creates a new write-only instance of Rollupmanagermock, bound to a specific deployed contract.
func NewRollupmanagermockTransactor(address common.Address, transactor bind.ContractTransactor) (*RollupmanagermockTransactor, error) {
	contract, err := bindRollupmanagermock(address, nil, transactor, nil)
	if err != nil {
		return nil, err
	}
	return &RollupmanagermockTransactor{contract: contract}, nil
}

// NewRollupmanagermockFilterer creates a new log filterer instance of Rollupmanagermock, bound to a specific deployed contract.
func NewRollupmanagermockFilterer(address common.Address, filterer bind.ContractFilterer) (*RollupmanagermockFilterer, error) {
	contract, err := bindRollupmanagermock(address, nil, nil, filterer)
	if err != nil {
		return nil, err
	}
	return &RollupmanagermockFilterer{contract: contract}, nil
}

// bindRollupmanagermock binds a generic wrapper to an already deployed contract.
func bindRollupmanagermock(address common.Address, caller bind.ContractCaller, transactor bind.ContractTransactor, filterer bind.ContractFilterer) (*bind.BoundContract, error) {
	parsed, err := RollupmanagermockMetaData.GetAbi()
	if err != nil {
		return nil, err
	}
	return bind.NewBoundContract(address, *parsed, caller, transactor, filterer), nil
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_Rollupmanagermock *RollupmanagermockRaw) Call(opts *bind.CallOpts, result *[]interface{}, method string, params ...interface{}) error {
	return _Rollupmanagermock.Contract.RollupmanagermockCaller.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_Rollupmanagermock *RollupmanagermockRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _Rollupmanagermock.Contract.RollupmanagermockTransactor.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_Rollupmanagermock *RollupmanagermockRaw) Transact(opts *bind.TransactOpts, method string, params ...interface{}) (*types.Transaction, error) {
	return _Rollupmanagermock.Contract.RollupmanagermockTransactor.contract.Transact(opts, method, params...)
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_Rollupmanagermock *RollupmanagermockCallerRaw) Call(opts *bind.CallOpts, result *[]interface{}, method string, params ...interface{}) error {
	return _Rollupmanagermock.Contract.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_Rollupmanagermock *RollupmanagermockTransactorRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _Rollupmanagermock.Contract.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_Rollupmanagermock *RollupmanagermockTransactorRaw) Transact(opts *bind.TransactOpts, method string, params ...interface{}) (*types.Transaction, error) {
	return _Rollupmanagermock.Contract.contract.Transact(opts, method, params...)
}

// RollupCount is a free data retrieval call binding the contract method 0xf4e92675.
//
// Solidity: function rollupCount() view returns(uint32)
func (_Rollupmanagermock *RollupmanagermockCaller) RollupCount(opts *bind.CallOpts) (uint32, error) {
	var out []interface{}
	err := _Rollupmanagermock.contract.Call(opts, &out, "rollupCount")

	if err != nil {
		return *new(uint32), err
	}

	out0 := *abi.ConvertType(out[0], new(uint32)).(*uint32)

	return out0, err

}

// RollupCount is a free data retrieval call binding the contract method 0xf4e92675.
//
// Solidity: function rollupCount() view returns(uint32)
func (_Rollupmanagermock *RollupmanagermockSession) RollupCount() (uint32, error) {
	return _Rollupmanagermock.Contract.RollupCount(&_Rollupmanagermock.CallOpts)
}

// RollupCount is a free data retrieval call binding the contract method 0xf4e92675.
//
// Solidity: function rollupCount() view returns(uint32)
func (_Rollupmanagermock *RollupmanagermockCallerSession) RollupCount() (uint32, error) {
	return _Rollupmanagermock.Contract.RollupCount(&_Rollupmanagermock.CallOpts)
}

// RollupIDToRollupData is a free data retrieval call binding the contract method 0xf9c4c2ae.
//
// Solidity: function rollupIDToRollupData(uint32 ) view returns(address rollupContract, uint64 chainID, address verifier, uint64 forkID, bytes32 lastLocalExitRoot, uint64 lastBatchSequenced, uint64 lastVerifiedBatch, uint64 legacyLastPendingState, uint64 legacyLastPendingStateConsolidated, uint64 lastVerifiedBatchBeforeUpgrade, uint64 rollupTypeID, uint8 rollupVerifierType)
func (_Rollupmanagermock *RollupmanagermockCaller) RollupIDToRollupData(opts *bind.CallOpts, arg0 uint32) (struct {
	RollupContract                     common.Address
	ChainID                            uint64
	Verifier                           common.Address
	ForkID                             uint64
	LastLocalExitRoot                  [32]byte
	LastBatchSequenced                 uint64
	LastVerifiedBatch                  uint64
	LegacyLastPendingState             uint64
	LegacyLastPendingStateConsolidated uint64
	LastVerifiedBatchBeforeUpgrade     uint64
	RollupTypeID                       uint64
	RollupVerifierType                 uint8
}, error) {
	var out []interface{}
	err := _Rollupmanagermock.contract.Call(opts, &out, "rollupIDToRollupData", arg0)

	outstruct := new(struct {
		RollupContract                     common.Address
		ChainID                            uint64
		Verifier                           common.Address
		ForkID                             uint64
		LastLocalExitRoot                  [32]byte
		LastBatchSequenced                 uint64
		LastVerifiedBatch                  uint64
		LegacyLastPendingState             uint64
		LegacyLastPendingStateConsolidated uint64
		LastVerifiedBatchBeforeUpgrade     uint64
		RollupTypeID                       uint64
		RollupVerifierType                 uint8
	})
	if err != nil {
		return *outstruct, err
	}

	outstruct.RollupContract = *abi.ConvertType(out[0], new(common.Address)).(*common.Address)
	outstruct.ChainID = *abi.ConvertType(out[1], new(uint64)).(*uint64)
	outstruct.Verifier = *abi.ConvertType(out[2], new(common.Address)).(*common.Address)
	outstruct.ForkID = *abi.ConvertType(out[3], new(uint64)).(*uint64)
	outstruct.LastLocalExitRoot = *abi.ConvertType(out[4], new([32]byte)).(*[32]byte)
	outstruct.LastBatchSequenced = *abi.ConvertType(out[5], new(uint64)).(*uint64)
	outstruct.LastVerifiedBatch = *abi.ConvertType(out[6], new(uint64)).(*uint64)
	outstruct.LegacyLastPendingState = *abi.ConvertType(out[7], new(uint64)).(*uint64)
	outstruct.LegacyLastPendingStateConsolidated = *abi.ConvertType(out[8], new(uint64)).(*uint64)
	outstruct.LastVerifiedBatchBeforeUpgrade = *abi.ConvertType(out[9], new(uint64)).(*uint64)
	outstruct.RollupTypeID = *abi.ConvertType(out[10], new(uint64)).(*uint64)
	outstruct.RollupVerifierType = *abi.ConvertType(out[11], new(uint8)).(*uint8)

	return *outstruct, err

}

// RollupIDToRollupData is a free data retrieval call binding the contract method 0xf9c4c2ae.
//
// Solidity: function rollupIDToRollupData(uint32 ) view returns(address rollupContract, uint64 chainID, address verifier, uint64 forkID, bytes32 lastLocalExitRoot, uint64 lastBatchSequenced, uint64 lastVerifiedBatch, uint64 legacyLastPendingState, uint64 legacyLastPendingStateConsolidated, uint64 lastVerifiedBatchBeforeUpgrade, uint64 rollupTypeID, uint8 rollupVerifierType)
func (_Rollupmanagermock *RollupmanagermockSession) RollupIDToRollupData(arg0 uint32) (struct {
	RollupContract                     common.Address
	ChainID                            uint64
	Verifier                           common.Address
	ForkID                             uint64
	LastLocalExitRoot                  [32]byte
	LastBatchSequenced                 uint64
	LastVerifiedBatch                  uint64
	LegacyLastPendingState             uint64
	LegacyLastPendingStateConsolidated uint64
	LastVerifiedBatchBeforeUpgrade     uint64
	RollupTypeID                       uint64
	RollupVerifierType                 uint8
}, error) {
	return _Rollupmanagermock.Contract.RollupIDToRollupData(&_Rollupmanagermock.CallOpts, arg0)
}

// RollupIDToRollupData is a free data retrieval call binding the contract method 0xf9c4c2ae.
//
// Solidity: function rollupIDToRollupData(uint32 ) view returns(address rollupContract, uint64 chainID, address verifier, uint64 forkID, bytes32 lastLocalExitRoot, uint64 lastBatchSequenced, uint64 lastVerifiedBatch, uint64 legacyLastPendingState, uint64 legacyLastPendingStateConsolidated, uint64 lastVerifiedBatchBeforeUpgrade, uint64 rollupTypeID, uint8 rollupVerifierType)
func (_Rollupmanagermock *RollupmanagermockCallerSession) RollupIDToRollupData(arg0 uint32) (struct {
	RollupContract                     common.Address
	ChainID                            uint64
	Verifier                           common.Address
	ForkID                             uint64
	LastLocalExitRoot                  [32]byte
	LastBatchSequenced                 uint64
	LastVerifiedBatch                  uint64
	LegacyLastPendingState             uint64
	LegacyLastPendingStateConsolidated uint64
	LastVerifiedBatchBeforeUpgrade     uint64
	RollupTypeID                       uint64
	RollupVerifierType                 uint8
}, error) {
	return _Rollupmanagermock.Contract.RollupIDToRollupData(&_Rollupmanagermock.CallOpts, arg0)
}

// AddExistingRollup is a paid mutator transaction binding the contract method 0xe80e5030.
//
// Solidity: function addExistingRollup(address rollupAddress, address verifier, uint64 forkID, uint64 chainID, bytes32 initRoot, uint8 rollupVerifierType, bytes32 programVKey, bytes32 initPessimisticRoot) returns()
func (_Rollupmanagermock *RollupmanagermockTransactor) AddExistingRollup(opts *bind.TransactOpts, rollupAddress common.Address, verifier common.Address, forkID uint64, chainID uint64, initRoot [32]byte, rollupVerifierType uint8, programVKey [32]byte, initPessimisticRoot [32]byte) (*types.Transaction, error) {
	return _Rollupmanagermock.contract.Transact(opts, "addExistingRollup", rollupAddress, verifier, forkID, chainID, initRoot, rollupVerifierType, programVKey, initPessimisticRoot)
}

// AddExistingRollup is a paid mutator transaction binding the contract method 0xe80e5030.
//
// Solidity: function addExistingRollup(address rollupAddress, address verifier, uint64 forkID, uint64 chainID, bytes32 initRoot, uint8 rollupVerifierType, bytes32 programVKey, bytes32 initPessimisticRoot) returns()
func (_Rollupmanagermock *RollupmanagermockSession) AddExistingRollup(rollupAddress common.Address, verifier common.Address, forkID uint64, chainID uint64, initRoot [32]byte, rollupVerifierType uint8, programVKey [32]byte, initPessimisticRoot [32]byte) (*types.Transaction, error) {
	return _Rollupmanagermock.Contract.AddExistingRollup(&_Rollupmanagermock.TransactOpts, rollupAddress, verifier, forkID, chainID, initRoot, rollupVerifierType, programVKey, initPessimisticRoot)
}

// AddExistingRollup is a paid mutator transaction binding the contract method 0xe80e5030.
//
// Solidity: function addExistingRollup(address rollupAddress, address verifier, uint64 forkID, uint64 chainID, bytes32 initRoot, uint8 rollupVerifierType, bytes32 programVKey, bytes32 initPessimisticRoot) returns()
func (_Rollupmanagermock *RollupmanagermockTransactorSession) AddExistingRollup(rollupAddress common.Address, verifier common.Address, forkID uint64, chainID uint64, initRoot [32]byte, rollupVerifierType uint8, programVKey [32]byte, initPessimisticRoot [32]byte) (*types.Transaction, error) {
	return _Rollupmanagermock.Contract.AddExistingRollup(&_Rollupmanagermock.TransactOpts, rollupAddress, verifier, forkID, chainID, initRoot, rollupVerifierType, programVKey, initPessimisticRoot)
}

// RollupmanagermockAddExistingRollupIterator is returned from FilterAddExistingRollup and is used to iterate over the raw logs and unpacked data for AddExistingRollup events raised by the Rollupmanagermock contract.
type RollupmanagermockAddExistingRollupIterator struct {
	Event *RollupmanagermockAddExistingRollup // Event containing the contract specifics and raw log

	contract *bind.BoundContract // Generic contract to use for unpacking event data
	event    string              // Event name to use for unpacking event data

	logs chan types.Log        // Log channel receiving the found contract events
	sub  ethereum.Subscription // Subscription for errors, completion and termination
	done bool                  // Whether the subscription completed delivering logs
	fail error                 // Occurred error to stop iteration
}

// Next advances the iterator to the subsequent event, returning whether there
// are any more events found. In case of a retrieval or parsing error, false is
// returned and Error() can be queried for the exact failure.
func (it *RollupmanagermockAddExistingRollupIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(RollupmanagermockAddExistingRollup)
			if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
				it.fail = err
				return false
			}
			it.Event.Raw = log
			return true

		default:
			return false
		}
	}
	// Iterator still in progress, wait for either a data or an error event
	select {
	case log := <-it.logs:
		it.Event = new(RollupmanagermockAddExistingRollup)
		if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
			it.fail = err
			return false
		}
		it.Event.Raw = log
		return true

	case err := <-it.sub.Err():
		it.done = true
		it.fail = err
		return it.Next()
	}
}

// Error returns any retrieval or parsing error occurred during filtering.
func (it *RollupmanagermockAddExistingRollupIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *RollupmanagermockAddExistingRollupIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// RollupmanagermockAddExistingRollup represents a AddExistingRollup event raised by the Rollupmanagermock contract.
type RollupmanagermockAddExistingRollup struct {
	RollupID                       uint32
	ForkID                         uint64
	RollupAddress                  common.Address
	ChainID                        uint64
	RollupVerifierType             uint8
	LastVerifiedBatchBeforeUpgrade uint64
	ProgramVKey                    [32]byte
	InitPessimisticRoot            [32]byte
	Raw                            types.Log // Blockchain specific contextual infos
}

// FilterAddExistingRollup is a free log retrieval operation binding the contract event 0x4da47f6e9bbd9ef91887183a576aaebcf1b9bb7d2a567b33b075044c6d36082e.
//
// Solidity: event AddExistingRollup(uint32 indexed rollupID, uint64 forkID, address rollupAddress, uint64 chainID, uint8 rollupVerifierType, uint64 lastVerifiedBatchBeforeUpgrade, bytes32 programVKey, bytes32 initPessimisticRoot)
func (_Rollupmanagermock *RollupmanagermockFilterer) FilterAddExistingRollup(opts *bind.FilterOpts, rollupID []uint32) (*RollupmanagermockAddExistingRollupIterator, error) {

	var rollupIDRule []interface{}
	for _, rollupIDItem := range rollupID {
		rollupIDRule = append(rollupIDRule, rollupIDItem)
	}

	logs, sub, err := _Rollupmanagermock.contract.FilterLogs(opts, "AddExistingRollup", rollupIDRule)
	if err != nil {
		return nil, err
	}
	return &RollupmanagermockAddExistingRollupIterator{contract: _Rollupmanagermock.contract, event: "AddExistingRollup", logs: logs, sub: sub}, nil
}

// WatchAddExistingRollup is a free log subscription operation binding the contract event 0x4da47f6e9bbd9ef91887183a576aaebcf1b9bb7d2a567b33b075044c6d36082e.
//
// Solidity: event AddExistingRollup(uint32 indexed rollupID, uint64 forkID, address rollupAddress, uint64 chainID, uint8 rollupVerifierType, uint64 lastVerifiedBatchBeforeUpgrade, bytes32 programVKey, bytes32 initPessimisticRoot)
func (_Rollupmanagermock *RollupmanagermockFilterer) WatchAddExistingRollup(opts *bind.WatchOpts, sink chan<- *RollupmanagermockAddExistingRollup, rollupID []uint32) (event.Subscription, error) {

	var rollupIDRule []interface{}
	for _, rollupIDItem := range rollupID {
		rollupIDRule = append(rollupIDRule, rollupIDItem)
	}

	logs, sub, err := _Rollupmanagermock.contract.WatchLogs(opts, "AddExistingRollup", rollupIDRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(RollupmanagermockAddExistingRollup)
				if err := _Rollupmanagermock.contract.UnpackLog(event, "AddExistingRollup", log); err != nil {
					return err
				}
				event.Raw = log

				select {
				case sink <- event:
				case err := <-sub.Err():
					return err
				case <-quit:
					return nil
				}
			case err := <-sub.Err():
				return err
			case <-quit:
				return nil
			}
		}
	}), nil
}

// ParseAddExistingRollup is a log parse operation binding the contract event 0x4da47f6e9bbd9ef91887183a576aaebcf1b9bb7d2a567b33b075044c6d36082e.
//
// Solidity: event AddExistingRollup(uint32 indexed rollupID, uint64 forkID, address rollupAddress, uint64 chainID, uint8 rollupVerifierType, uint64 lastVerifiedBatchBeforeUpgrade, bytes32 programVKey, bytes32 initPessimisticRoot)
func (_Rollupmanagermock *RollupmanagermockFilterer) ParseAddExistingRollup(log types.Log) (*RollupmanagermockAddExistingRollup, error) {
	event := new(RollupmanagermockAddExistingRollup)
	if err := _Rollupmanagermock.contract.UnpackLog(event, "AddExistingRollup", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// RollupmanagermockCreateNewAggchainIterator is returned from FilterCreateNewAggchain and is used to iterate over the raw logs and unpacked data for CreateNewAggchain events raised by the Rollupmanagermock contract.
type RollupmanagermockCreateNewAggchainIterator struct {
	Event *RollupmanagermockCreateNewAggchain // Event containing the contract specifics and raw log

	contract *bind.BoundContract // Generic contract to use for unpacking event data
	event    string              // Event name to use for unpacking event data

	logs chan types.Log        // Log channel receiving the found contract events
	sub  ethereum.Subscription // Subscription for errors, completion and termination
	done bool                  // Whether the subscription completed delivering logs
	fail error                 // Occurred error to stop iteration
}

// Next advances the iterator to the subsequent event, returning whether there
// are any more events found. In case of a retrieval or parsing error, false is
// returned and Error() can be queried for the exact failure.
func (it *RollupmanagermockCreateNewAggchainIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(RollupmanagermockCreateNewAggchain)
			if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
				it.fail = err
				return false
			}
			it.Event.Raw = log
			return true

		default:
			return false
		}
	}
	// Iterator still in progress, wait for either a data or an error event
	select {
	case log := <-it.logs:
		it.Event = new(RollupmanagermockCreateNewAggchain)
		if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
			it.fail = err
			return false
		}
		it.Event.Raw = log
		return true

	case err := <-it.sub.Err():
		it.done = true
		it.fail = err
		return it.Next()
	}
}

// Error returns any retrieval or parsing error occurred during filtering.
func (it *RollupmanagermockCreateNewAggchainIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *RollupmanagermockCreateNewAggchainIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// RollupmanagermockCreateNewAggchain represents a CreateNewAggchain event raised by the Rollupmanagermock contract.
type RollupmanagermockCreateNewAggchain struct {
	RollupID                uint32
	RollupTypeID            uint32
	RollupAddress           common.Address
	ChainID                 uint64
	RollupVerifierType      uint8
	InitializeBytesAggchain []byte
	Raw                     types.Log // Blockchain specific contextual infos
}

// FilterCreateNewAggchain is a free log retrieval operation binding the contract event 0x144e3f9b5c63682a3bb7e9ad31e99c043890d3d540cd79dcebc3b5bdfba94c9b.
//
// Solidity: event CreateNewAggchain(uint32 indexed rollupID, uint32 rollupTypeID, address rollupAddress, uint64 chainID, uint8 rollupVerifierType, bytes initializeBytesAggchain)
func (_Rollupmanagermock *RollupmanagermockFilterer) FilterCreateNewAggchain(opts *bind.FilterOpts, rollupID []uint32) (*RollupmanagermockCreateNewAggchainIterator, error) {

	var rollupIDRule []interface{}
	for _, rollupIDItem := range rollupID {
		rollupIDRule = append(rollupIDRule, rollupIDItem)
	}

	logs, sub, err := _Rollupmanagermock.contract.FilterLogs(opts, "CreateNewAggchain", rollupIDRule)
	if err != nil {
		return nil, err
	}
	return &RollupmanagermockCreateNewAggchainIterator{contract: _Rollupmanagermock.contract, event: "CreateNewAggchain", logs: logs, sub: sub}, nil
}

// WatchCreateNewAggchain is a free log subscription operation binding the contract event 0x144e3f9b5c63682a3bb7e9ad31e99c043890d3d540cd79dcebc3b5bdfba94c9b.
//
// Solidity: event CreateNewAggchain(uint32 indexed rollupID, uint32 rollupTypeID, address rollupAddress, uint64 chainID, uint8 rollupVerifierType, bytes initializeBytesAggchain)
func (_Rollupmanagermock *RollupmanagermockFilterer) WatchCreateNewAggchain(opts *bind.WatchOpts, sink chan<- *RollupmanagermockCreateNewAggchain, rollupID []uint32) (event.Subscription, error) {

	var rollupIDRule []interface{}
	for _, rollupIDItem := range rollupID {
		rollupIDRule = append(rollupIDRule, rollupIDItem)
	}

	logs, sub, err := _Rollupmanagermock.contract.WatchLogs(opts, "CreateNewAggchain", rollupIDRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(RollupmanagermockCreateNewAggchain)
				if err := _Rollupmanagermock.contract.UnpackLog(event, "CreateNewAggchain", log); err != nil {
					return err
				}
				event.Raw = log

				select {
				case sink <- event:
				case err := <-sub.Err():
					return err
				case <-quit:
					return nil
				}
			case err := <-sub.Err():
				return err
			case <-quit:
				return nil
			}
		}
	}), nil
}

// ParseCreateNewAggchain is a log parse operation binding the contract event 0x144e3f9b5c63682a3bb7e9ad31e99c043890d3d540cd79dcebc3b5bdfba94c9b.
//
// Solidity: event CreateNewAggchain(uint32 indexed rollupID, uint32 rollupTypeID, address rollupAddress, uint64 chainID, uint8 rollupVerifierType, bytes initializeBytesAggchain)
func (_Rollupmanagermock *RollupmanagermockFilterer) ParseCreateNewAggchain(log types.Log) (*RollupmanagermockCreateNewAggchain, error) {
	event := new(RollupmanagermockCreateNewAggchain)
	if err := _Rollupmanagermock.contract.UnpackLog(event, "CreateNewAggchain", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// RollupmanagermockCreateNewRollupIterator is returned from FilterCreateNewRollup and is used to iterate over the raw logs and unpacked data for CreateNewRollup events raised by the Rollupmanagermock contract.
type RollupmanagermockCreateNewRollupIterator struct {
	Event *RollupmanagermockCreateNewRollup // Event containing the contract specifics and raw log

	contract *bind.BoundContract // Generic contract to use for unpacking event data
	event    string              // Event name to use for unpacking event data

	logs chan types.Log        // Log channel receiving the found contract events
	sub  ethereum.Subscription // Subscription for errors, completion and termination
	done bool                  // Whether the subscription completed delivering logs
	fail error                 // Occurred error to stop iteration
}

// Next advances the iterator to the subsequent event, returning whether there
// are any more events found. In case of a retrieval or parsing error, false is
// returned and Error() can be queried for the exact failure.
func (it *RollupmanagermockCreateNewRollupIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(RollupmanagermockCreateNewRollup)
			if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
				it.fail = err
				return false
			}
			it.Event.Raw = log
			return true

		default:
			return false
		}
	}
	// Iterator still in progress, wait for either a data or an error event
	select {
	case log := <-it.logs:
		it.Event = new(RollupmanagermockCreateNewRollup)
		if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
			it.fail = err
			return false
		}
		it.Event.Raw = log
		return true

	case err := <-it.sub.Err():
		it.done = true
		it.fail = err
		return it.Next()
	}
}

// Error returns any retrieval or parsing error occurred during filtering.
func (it *RollupmanagermockCreateNewRollupIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *RollupmanagermockCreateNewRollupIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// RollupmanagermockCreateNewRollup represents a CreateNewRollup event raised by the Rollupmanagermock contract.
type RollupmanagermockCreateNewRollup struct {
	RollupID        uint32
	RollupTypeID    uint32
	RollupAddress   common.Address
	ChainID         uint64
	GasTokenAddress common.Address
	Raw             types.Log // Blockchain specific contextual infos
}

// FilterCreateNewRollup is a free log retrieval operation binding the contract event 0x194c983456df6701c6a50830b90fe80e72b823411d0d524970c9590dc277a641.
//
// Solidity: event CreateNewRollup(uint32 indexed rollupID, uint32 rollupTypeID, address rollupAddress, uint64 chainID, address gasTokenAddress)
func (_Rollupmanagermock *RollupmanagermockFilterer) FilterCreateNewRollup(opts *bind.FilterOpts, rollupID []uint32) (*RollupmanagermockCreateNewRollupIterator, error) {

	var rollupIDRule []interface{}
	for _, rollupIDItem := range rollupID {
		rollupIDRule = append(rollupIDRule, rollupIDItem)
	}

	logs, sub, err := _Rollupmanagermock.contract.FilterLogs(opts, "CreateNewRollup", rollupIDRule)
	if err != nil {
		return nil, err
	}
	return &RollupmanagermockCreateNewRollupIterator{contract: _Rollupmanagermock.contract, event: "CreateNewRollup", logs: logs, sub: sub}, nil
}

// WatchCreateNewRollup is a free log subscription operation binding the contract event 0x194c983456df6701c6a50830b90fe80e72b823411d0d524970c9590dc277a641.
//
// Solidity: event CreateNewRollup(uint32 indexed rollupID, uint32 rollupTypeID, address rollupAddress, uint64 chainID, address gasTokenAddress)
func (_Rollupmanagermock *RollupmanagermockFilterer) WatchCreateNewRollup(opts *bind.WatchOpts, sink chan<- *RollupmanagermockCreateNewRollup, rollupID []uint32) (event.Subscription, error) {

	var rollupIDRule []interface{}
	for _, rollupIDItem := range rollupID {
		rollupIDRule = append(rollupIDRule, rollupIDItem)
	}

	logs, sub, err := _Rollupmanagermock.contract.WatchLogs(opts, "CreateNewRollup", rollupIDRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(RollupmanagermockCreateNewRollup)
				if err := _Rollupmanagermock.contract.UnpackLog(event, "CreateNewRollup", log); err != nil {
					return err
				}
				event.Raw = log

				select {
				case sink <- event:
				case err := <-sub.Err():
					return err
				case <-quit:
					return nil
				}
			case err := <-sub.Err():
				return err
			case <-quit:
				return nil
			}
		}
	}), nil
}

// ParseCreateNewRollup is a log parse operation binding the contract event 0x194c983456df6701c6a50830b90fe80e72b823411d0d524970c9590dc277a641.
//
// Solidity: event CreateNewRollup(uint32 indexed rollupID, uint32 rollupTypeID, address rollupAddress, uint64 chainID, address gasTokenAddress)
func (_Rollupmanagermock *RollupmanagermockFilterer) ParseCreateNewRollup(log types.Log) (*RollupmanagermockCreateNewRollup, error) {
	event := new(RollupmanagermockCreateNewRollup)
	if err := _Rollupmanagermock.contract.UnpackLog(event, "CreateNewRollup", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}
