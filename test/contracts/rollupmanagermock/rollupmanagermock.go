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

// RollupManagerMockRollupDataReturn is an auto generated low-level Go binding around an user-defined struct.
type RollupManagerMockRollupDataReturn struct {
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
}

// RollupmanagermockMetaData contains all meta data concerning the Rollupmanagermock contract.
var RollupmanagermockMetaData = &bind.MetaData{
	ABI: "[{\"inputs\":[],\"name\":\"rollupCount\",\"outputs\":[{\"internalType\":\"uint32\",\"name\":\"\",\"type\":\"uint32\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"uint32\",\"name\":\"rollupID\",\"type\":\"uint32\"}],\"name\":\"rollupIDToRollupData\",\"outputs\":[{\"components\":[{\"internalType\":\"address\",\"name\":\"rollupContract\",\"type\":\"address\"},{\"internalType\":\"uint64\",\"name\":\"chainID\",\"type\":\"uint64\"},{\"internalType\":\"address\",\"name\":\"verifier\",\"type\":\"address\"},{\"internalType\":\"uint64\",\"name\":\"forkID\",\"type\":\"uint64\"},{\"internalType\":\"bytes32\",\"name\":\"lastLocalExitRoot\",\"type\":\"bytes32\"},{\"internalType\":\"uint64\",\"name\":\"lastBatchSequenced\",\"type\":\"uint64\"},{\"internalType\":\"uint64\",\"name\":\"lastVerifiedBatch\",\"type\":\"uint64\"},{\"internalType\":\"uint64\",\"name\":\"_legacyLastPendingState\",\"type\":\"uint64\"},{\"internalType\":\"uint64\",\"name\":\"_legacyLastPendingStateConsolidated\",\"type\":\"uint64\"},{\"internalType\":\"uint64\",\"name\":\"lastVerifiedBatchBeforeUpgrade\",\"type\":\"uint64\"},{\"internalType\":\"uint64\",\"name\":\"rollupTypeID\",\"type\":\"uint64\"},{\"internalType\":\"uint8\",\"name\":\"rollupVerifierType\",\"type\":\"uint8\"}],\"internalType\":\"structRollupManagerMock.RollupDataReturn\",\"name\":\"rollupData\",\"type\":\"tuple\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"uint32\",\"name\":\"rollupID\",\"type\":\"uint32\"},{\"internalType\":\"address\",\"name\":\"rollupContractAddr\",\"type\":\"address\"}],\"name\":\"setRollupContract\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"uint32\",\"name\":\"newRollupCount\",\"type\":\"uint32\"}],\"name\":\"setRollupCount\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"}]",
	Bin: "0x608060405234801561001057600080fd5b5061042a806100206000396000f3fe608060405234801561001057600080fd5b506004361061004c5760003560e01c806323c998d514610051578063f4e926751461007d578063f9c4c2ae146100a7578063f9f43b1a146101fb575b600080fd5b61007b61005f366004610282565b6000805463ffffffff191663ffffffff92909216919091179055565b005b60005461008d9063ffffffff1681565b60405163ffffffff90911681526020015b60405180910390f35b6101ee6100b5366004610282565b6040805161018081018252600080825260208201819052918101829052606081018290526080810182905260a0810182905260c0810182905260e081018290526101008101829052610120810182905261014081018290526101608101919091525063ffffffff1660009081526001602081815260409283902083516101808101855281546001600160a01b03808216835267ffffffffffffffff600160a01b928390048116958401959095529483015494851695820195909552939092048116606084015260028201546080840152600382015480821660a085015268010000000000000000808204831660c0860152600160801b808304841660e0870152600160c01b909204831661010086015260049093015480831661012086015292830490911661014084015260ff91041661016082015290565b60405161009e91906102a4565b61007b6102093660046103b1565b60005463ffffffff9081169083161115610233576000805463ffffffff191663ffffffff84161790555b63ffffffff91909116600090815260016020526040902080546001600160a01b0319166001600160a01b03909216919091179055565b803563ffffffff8116811461027d57600080fd5b919050565b60006020828403121561029457600080fd5b61029d82610269565b9392505050565b81516001600160a01b03168152610180810160208301516102d1602084018267ffffffffffffffff169052565b5060408301516102ec60408401826001600160a01b03169052565b506060830151610308606084018267ffffffffffffffff169052565b506080830151608083015260a083015161032e60a084018267ffffffffffffffff169052565b5060c083015161034a60c084018267ffffffffffffffff169052565b5060e083015161036660e084018267ffffffffffffffff169052565b506101008381015167ffffffffffffffff908116918401919091526101208085015182169084015261014080850151909116908301526101609283015160ff16929091019190915290565b600080604083850312156103c457600080fd5b6103cd83610269565b915060208301356001600160a01b03811681146103e957600080fd5b80915050925092905056fea26469706673582212209fe330adb50466addcb337374d69daeea986aef2caa8d053d26f7c388b597c4364736f6c63430008120033",
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
// Solidity: function rollupIDToRollupData(uint32 rollupID) view returns((address,uint64,address,uint64,bytes32,uint64,uint64,uint64,uint64,uint64,uint64,uint8) rollupData)
func (_Rollupmanagermock *RollupmanagermockCaller) RollupIDToRollupData(opts *bind.CallOpts, rollupID uint32) (RollupManagerMockRollupDataReturn, error) {
	var out []interface{}
	err := _Rollupmanagermock.contract.Call(opts, &out, "rollupIDToRollupData", rollupID)

	if err != nil {
		return *new(RollupManagerMockRollupDataReturn), err
	}

	out0 := *abi.ConvertType(out[0], new(RollupManagerMockRollupDataReturn)).(*RollupManagerMockRollupDataReturn)

	return out0, err

}

// RollupIDToRollupData is a free data retrieval call binding the contract method 0xf9c4c2ae.
//
// Solidity: function rollupIDToRollupData(uint32 rollupID) view returns((address,uint64,address,uint64,bytes32,uint64,uint64,uint64,uint64,uint64,uint64,uint8) rollupData)
func (_Rollupmanagermock *RollupmanagermockSession) RollupIDToRollupData(rollupID uint32) (RollupManagerMockRollupDataReturn, error) {
	return _Rollupmanagermock.Contract.RollupIDToRollupData(&_Rollupmanagermock.CallOpts, rollupID)
}

// RollupIDToRollupData is a free data retrieval call binding the contract method 0xf9c4c2ae.
//
// Solidity: function rollupIDToRollupData(uint32 rollupID) view returns((address,uint64,address,uint64,bytes32,uint64,uint64,uint64,uint64,uint64,uint64,uint8) rollupData)
func (_Rollupmanagermock *RollupmanagermockCallerSession) RollupIDToRollupData(rollupID uint32) (RollupManagerMockRollupDataReturn, error) {
	return _Rollupmanagermock.Contract.RollupIDToRollupData(&_Rollupmanagermock.CallOpts, rollupID)
}

// SetRollupContract is a paid mutator transaction binding the contract method 0xf9f43b1a.
//
// Solidity: function setRollupContract(uint32 rollupID, address rollupContractAddr) returns()
func (_Rollupmanagermock *RollupmanagermockTransactor) SetRollupContract(opts *bind.TransactOpts, rollupID uint32, rollupContractAddr common.Address) (*types.Transaction, error) {
	return _Rollupmanagermock.contract.Transact(opts, "setRollupContract", rollupID, rollupContractAddr)
}

// SetRollupContract is a paid mutator transaction binding the contract method 0xf9f43b1a.
//
// Solidity: function setRollupContract(uint32 rollupID, address rollupContractAddr) returns()
func (_Rollupmanagermock *RollupmanagermockSession) SetRollupContract(rollupID uint32, rollupContractAddr common.Address) (*types.Transaction, error) {
	return _Rollupmanagermock.Contract.SetRollupContract(&_Rollupmanagermock.TransactOpts, rollupID, rollupContractAddr)
}

// SetRollupContract is a paid mutator transaction binding the contract method 0xf9f43b1a.
//
// Solidity: function setRollupContract(uint32 rollupID, address rollupContractAddr) returns()
func (_Rollupmanagermock *RollupmanagermockTransactorSession) SetRollupContract(rollupID uint32, rollupContractAddr common.Address) (*types.Transaction, error) {
	return _Rollupmanagermock.Contract.SetRollupContract(&_Rollupmanagermock.TransactOpts, rollupID, rollupContractAddr)
}

// SetRollupCount is a paid mutator transaction binding the contract method 0x23c998d5.
//
// Solidity: function setRollupCount(uint32 newRollupCount) returns()
func (_Rollupmanagermock *RollupmanagermockTransactor) SetRollupCount(opts *bind.TransactOpts, newRollupCount uint32) (*types.Transaction, error) {
	return _Rollupmanagermock.contract.Transact(opts, "setRollupCount", newRollupCount)
}

// SetRollupCount is a paid mutator transaction binding the contract method 0x23c998d5.
//
// Solidity: function setRollupCount(uint32 newRollupCount) returns()
func (_Rollupmanagermock *RollupmanagermockSession) SetRollupCount(newRollupCount uint32) (*types.Transaction, error) {
	return _Rollupmanagermock.Contract.SetRollupCount(&_Rollupmanagermock.TransactOpts, newRollupCount)
}

// SetRollupCount is a paid mutator transaction binding the contract method 0x23c998d5.
//
// Solidity: function setRollupCount(uint32 newRollupCount) returns()
func (_Rollupmanagermock *RollupmanagermockTransactorSession) SetRollupCount(newRollupCount uint32) (*types.Transaction, error) {
	return _Rollupmanagermock.Contract.SetRollupCount(&_Rollupmanagermock.TransactOpts, newRollupCount)
}
