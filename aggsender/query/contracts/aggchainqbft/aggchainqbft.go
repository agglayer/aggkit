// Code generated - DO NOT EDIT.
// This file is a generated binding and any manual changes will be lost.

package aggchainqbft

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"time"

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
	_ = time.Tick
	_ = context.Background
)

// AggchainqbftMetaData contains all meta data concerning the Aggchainqbft contract.
var AggchainqbftMetaData = &bind.MetaData{
	ABI: "[{\"type\":\"function\",\"name\":\"AGGCHAIN_TYPE\",\"inputs\":[],\"outputs\":[{\"name\":\"\",\"type\":\"bytes2\",\"internalType\":\"bytes2\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"lastProvenBlockNumber\",\"inputs\":[],\"outputs\":[{\"name\":\"\",\"type\":\"uint64\",\"internalType\":\"uint64\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"lastProvenBlockHash\",\"inputs\":[],\"outputs\":[{\"name\":\"\",\"type\":\"bytes32\",\"internalType\":\"bytes32\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"consensusStateHash\",\"inputs\":[],\"outputs\":[{\"name\":\"\",\"type\":\"bytes32\",\"internalType\":\"bytes32\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"chainConfigHash\",\"inputs\":[],\"outputs\":[{\"name\":\"\",\"type\":\"bytes32\",\"internalType\":\"bytes32\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"epochLength\",\"inputs\":[],\"outputs\":[{\"name\":\"\",\"type\":\"uint64\",\"internalType\":\"uint64\"}],\"stateMutability\":\"view\"}]",
}

// AggchainqbftABI is the input ABI used to generate the binding from.
// Deprecated: Use AggchainqbftMetaData.ABI instead.
var AggchainqbftABI = AggchainqbftMetaData.ABI

// Aggchainqbft is an auto generated Go binding around an Ethereum contract.
type Aggchainqbft struct {
	AggchainqbftCaller     // Read-only binding to the contract
	AggchainqbftTransactor // Write-only binding to the contract
	AggchainqbftFilterer   // Log filterer for contract events
}

// AggchainqbftCaller is an auto generated read-only Go binding around an Ethereum contract.
type AggchainqbftCaller struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// AggchainqbftTransactor is an auto generated write-only Go binding around an Ethereum contract.
type AggchainqbftTransactor struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// AggchainqbftFilterer is an auto generated log filtering Go binding around an Ethereum contract events.
type AggchainqbftFilterer struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// AggchainqbftSession is an auto generated Go binding around an Ethereum contract,
// with pre-set call and transact options.
type AggchainqbftSession struct {
	Contract     *Aggchainqbft     // Generic contract binding to set the session for
	CallOpts     bind.CallOpts     // Call options to use throughout this session
	TransactOpts bind.TransactOpts // Transaction auth options to use throughout this session
}

// AggchainqbftCallerSession is an auto generated read-only Go binding around an Ethereum contract,
// with pre-set call options.
type AggchainqbftCallerSession struct {
	Contract *AggchainqbftCaller // Generic contract caller binding to set the session for
	CallOpts bind.CallOpts       // Call options to use throughout this session
}

// AggchainqbftTransactorSession is an auto generated write-only Go binding around an Ethereum contract,
// with pre-set transact options.
type AggchainqbftTransactorSession struct {
	Contract     *AggchainqbftTransactor // Generic contract transactor binding to set the session for
	TransactOpts bind.TransactOpts       // Transaction auth options to use throughout this session
}

// AggchainqbftRaw is an auto generated low-level Go binding around an Ethereum contract.
type AggchainqbftRaw struct {
	Contract *Aggchainqbft // Generic contract binding to access the raw methods on
}

// AggchainqbftCallerRaw is an auto generated low-level read-only Go binding around an Ethereum contract.
type AggchainqbftCallerRaw struct {
	Contract *AggchainqbftCaller // Generic read-only contract binding to access the raw methods on
}

// AggchainqbftTransactorRaw is an auto generated low-level write-only Go binding around an Ethereum contract.
type AggchainqbftTransactorRaw struct {
	Contract *AggchainqbftTransactor // Generic write-only contract binding to access the raw methods on
}

// NewAggchainqbft creates a new instance of Aggchainqbft, bound to a specific deployed contract.
func NewAggchainqbft(address common.Address, backend bind.ContractBackend) (*Aggchainqbft, error) {
	contract, err := bindAggchainqbft(address, backend, backend, backend)
	if err != nil {
		return nil, err
	}
	return &Aggchainqbft{AggchainqbftCaller: AggchainqbftCaller{contract: contract}, AggchainqbftTransactor: AggchainqbftTransactor{contract: contract}, AggchainqbftFilterer: AggchainqbftFilterer{contract: contract}}, nil
}

// NewAggchainqbftCaller creates a new read-only instance of Aggchainqbft, bound to a specific deployed contract.
func NewAggchainqbftCaller(address common.Address, caller bind.ContractCaller) (*AggchainqbftCaller, error) {
	contract, err := bindAggchainqbft(address, caller, nil, nil)
	if err != nil {
		return nil, err
	}
	return &AggchainqbftCaller{contract: contract}, nil
}

// NewAggchainqbftTransactor creates a new write-only instance of Aggchainqbft, bound to a specific deployed contract.
func NewAggchainqbftTransactor(address common.Address, transactor bind.ContractTransactor) (*AggchainqbftTransactor, error) {
	contract, err := bindAggchainqbft(address, nil, transactor, nil)
	if err != nil {
		return nil, err
	}
	return &AggchainqbftTransactor{contract: contract}, nil
}

// NewAggchainqbftFilterer creates a new log filterer instance of Aggchainqbft, bound to a specific deployed contract.
func NewAggchainqbftFilterer(address common.Address, filterer bind.ContractFilterer) (*AggchainqbftFilterer, error) {
	contract, err := bindAggchainqbft(address, nil, nil, filterer)
	if err != nil {
		return nil, err
	}
	return &AggchainqbftFilterer{contract: contract}, nil
}

// bindAggchainqbft binds a generic wrapper to an already deployed contract.
func bindAggchainqbft(address common.Address, caller bind.ContractCaller, transactor bind.ContractTransactor, filterer bind.ContractFilterer) (*bind.BoundContract, error) {
	parsed, err := AggchainqbftMetaData.GetAbi()
	if err != nil {
		return nil, err
	}
	return bind.NewBoundContract(address, *parsed, caller, transactor, filterer), nil
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_Aggchainqbft *AggchainqbftRaw) Call(opts *bind.CallOpts, result *[]interface{}, method string, params ...interface{}) error {
	return _Aggchainqbft.Contract.AggchainqbftCaller.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_Aggchainqbft *AggchainqbftRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _Aggchainqbft.Contract.AggchainqbftTransactor.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_Aggchainqbft *AggchainqbftRaw) Transact(opts *bind.TransactOpts, method string, params ...interface{}) (*types.Transaction, error) {
	return _Aggchainqbft.Contract.AggchainqbftTransactor.contract.Transact(opts, method, params...)
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_Aggchainqbft *AggchainqbftCallerRaw) Call(opts *bind.CallOpts, result *[]interface{}, method string, params ...interface{}) error {
	return _Aggchainqbft.Contract.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_Aggchainqbft *AggchainqbftTransactorRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _Aggchainqbft.Contract.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_Aggchainqbft *AggchainqbftTransactorRaw) Transact(opts *bind.TransactOpts, method string, params ...interface{}) (*types.Transaction, error) {
	return _Aggchainqbft.Contract.contract.Transact(opts, method, params...)
}

// AGGCHAINTYPE is a free data retrieval call binding the contract method 0x6e7fbce9.
//
// Solidity: function AGGCHAIN_TYPE() view returns(bytes2)
func (_Aggchainqbft *AggchainqbftCaller) AGGCHAINTYPE(opts *bind.CallOpts) ([2]byte, error) {
	var out []interface{}
	err := _Aggchainqbft.contract.Call(opts, &out, "AGGCHAIN_TYPE")

	if err != nil {
		return *new([2]byte), err
	}

	out0 := *abi.ConvertType(out[0], new([2]byte)).(*[2]byte)

	return out0, err

}

// AGGCHAINTYPE is a free data retrieval call binding the contract method 0x6e7fbce9.
//
// Solidity: function AGGCHAIN_TYPE() view returns(bytes2)
func (_Aggchainqbft *AggchainqbftSession) AGGCHAINTYPE() ([2]byte, error) {
	return _Aggchainqbft.Contract.AGGCHAINTYPE(&_Aggchainqbft.CallOpts)
}

// AGGCHAINTYPE is a free data retrieval call binding the contract method 0x6e7fbce9.
//
// Solidity: function AGGCHAIN_TYPE() view returns(bytes2)
func (_Aggchainqbft *AggchainqbftCallerSession) AGGCHAINTYPE() ([2]byte, error) {
	return _Aggchainqbft.Contract.AGGCHAINTYPE(&_Aggchainqbft.CallOpts)
}

// ChainConfigHash is a free data retrieval call binding the contract method 0xb7fd13ce.
//
// Solidity: function chainConfigHash() view returns(bytes32)
func (_Aggchainqbft *AggchainqbftCaller) ChainConfigHash(opts *bind.CallOpts) ([32]byte, error) {
	var out []interface{}
	err := _Aggchainqbft.contract.Call(opts, &out, "chainConfigHash")

	if err != nil {
		return *new([32]byte), err
	}

	out0 := *abi.ConvertType(out[0], new([32]byte)).(*[32]byte)

	return out0, err

}

// ChainConfigHash is a free data retrieval call binding the contract method 0xb7fd13ce.
//
// Solidity: function chainConfigHash() view returns(bytes32)
func (_Aggchainqbft *AggchainqbftSession) ChainConfigHash() ([32]byte, error) {
	return _Aggchainqbft.Contract.ChainConfigHash(&_Aggchainqbft.CallOpts)
}

// ChainConfigHash is a free data retrieval call binding the contract method 0xb7fd13ce.
//
// Solidity: function chainConfigHash() view returns(bytes32)
func (_Aggchainqbft *AggchainqbftCallerSession) ChainConfigHash() ([32]byte, error) {
	return _Aggchainqbft.Contract.ChainConfigHash(&_Aggchainqbft.CallOpts)
}

// ConsensusStateHash is a free data retrieval call binding the contract method 0xf2180a2c.
//
// Solidity: function consensusStateHash() view returns(bytes32)
func (_Aggchainqbft *AggchainqbftCaller) ConsensusStateHash(opts *bind.CallOpts) ([32]byte, error) {
	var out []interface{}
	err := _Aggchainqbft.contract.Call(opts, &out, "consensusStateHash")

	if err != nil {
		return *new([32]byte), err
	}

	out0 := *abi.ConvertType(out[0], new([32]byte)).(*[32]byte)

	return out0, err

}

// ConsensusStateHash is a free data retrieval call binding the contract method 0xf2180a2c.
//
// Solidity: function consensusStateHash() view returns(bytes32)
func (_Aggchainqbft *AggchainqbftSession) ConsensusStateHash() ([32]byte, error) {
	return _Aggchainqbft.Contract.ConsensusStateHash(&_Aggchainqbft.CallOpts)
}

// ConsensusStateHash is a free data retrieval call binding the contract method 0xf2180a2c.
//
// Solidity: function consensusStateHash() view returns(bytes32)
func (_Aggchainqbft *AggchainqbftCallerSession) ConsensusStateHash() ([32]byte, error) {
	return _Aggchainqbft.Contract.ConsensusStateHash(&_Aggchainqbft.CallOpts)
}

// EpochLength is a free data retrieval call binding the contract method 0x57d775f8.
//
// Solidity: function epochLength() view returns(uint64)
func (_Aggchainqbft *AggchainqbftCaller) EpochLength(opts *bind.CallOpts) (uint64, error) {
	var out []interface{}
	err := _Aggchainqbft.contract.Call(opts, &out, "epochLength")

	if err != nil {
		return *new(uint64), err
	}

	out0 := *abi.ConvertType(out[0], new(uint64)).(*uint64)

	return out0, err

}

// EpochLength is a free data retrieval call binding the contract method 0x57d775f8.
//
// Solidity: function epochLength() view returns(uint64)
func (_Aggchainqbft *AggchainqbftSession) EpochLength() (uint64, error) {
	return _Aggchainqbft.Contract.EpochLength(&_Aggchainqbft.CallOpts)
}

// EpochLength is a free data retrieval call binding the contract method 0x57d775f8.
//
// Solidity: function epochLength() view returns(uint64)
func (_Aggchainqbft *AggchainqbftCallerSession) EpochLength() (uint64, error) {
	return _Aggchainqbft.Contract.EpochLength(&_Aggchainqbft.CallOpts)
}

// LastProvenBlockHash is a free data retrieval call binding the contract method 0x95819075.
//
// Solidity: function lastProvenBlockHash() view returns(bytes32)
func (_Aggchainqbft *AggchainqbftCaller) LastProvenBlockHash(opts *bind.CallOpts) ([32]byte, error) {
	var out []interface{}
	err := _Aggchainqbft.contract.Call(opts, &out, "lastProvenBlockHash")

	if err != nil {
		return *new([32]byte), err
	}

	out0 := *abi.ConvertType(out[0], new([32]byte)).(*[32]byte)

	return out0, err

}

// LastProvenBlockHash is a free data retrieval call binding the contract method 0x95819075.
//
// Solidity: function lastProvenBlockHash() view returns(bytes32)
func (_Aggchainqbft *AggchainqbftSession) LastProvenBlockHash() ([32]byte, error) {
	return _Aggchainqbft.Contract.LastProvenBlockHash(&_Aggchainqbft.CallOpts)
}

// LastProvenBlockHash is a free data retrieval call binding the contract method 0x95819075.
//
// Solidity: function lastProvenBlockHash() view returns(bytes32)
func (_Aggchainqbft *AggchainqbftCallerSession) LastProvenBlockHash() ([32]byte, error) {
	return _Aggchainqbft.Contract.LastProvenBlockHash(&_Aggchainqbft.CallOpts)
}

// LastProvenBlockNumber is a free data retrieval call binding the contract method 0xdec0bc6f.
//
// Solidity: function lastProvenBlockNumber() view returns(uint64)
func (_Aggchainqbft *AggchainqbftCaller) LastProvenBlockNumber(opts *bind.CallOpts) (uint64, error) {
	var out []interface{}
	err := _Aggchainqbft.contract.Call(opts, &out, "lastProvenBlockNumber")

	if err != nil {
		return *new(uint64), err
	}

	out0 := *abi.ConvertType(out[0], new(uint64)).(*uint64)

	return out0, err

}

// LastProvenBlockNumber is a free data retrieval call binding the contract method 0xdec0bc6f.
//
// Solidity: function lastProvenBlockNumber() view returns(uint64)
func (_Aggchainqbft *AggchainqbftSession) LastProvenBlockNumber() (uint64, error) {
	return _Aggchainqbft.Contract.LastProvenBlockNumber(&_Aggchainqbft.CallOpts)
}

// LastProvenBlockNumber is a free data retrieval call binding the contract method 0xdec0bc6f.
//
// Solidity: function lastProvenBlockNumber() view returns(uint64)
func (_Aggchainqbft *AggchainqbftCallerSession) LastProvenBlockNumber() (uint64, error) {
	return _Aggchainqbft.Contract.LastProvenBlockNumber(&_Aggchainqbft.CallOpts)
}
