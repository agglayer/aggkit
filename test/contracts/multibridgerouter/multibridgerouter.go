// Code generated - DO NOT EDIT.
// This file is a generated binding and any manual changes will be lost.

package multibridgerouter

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

// MultibridgerouterMetaData contains all meta data concerning the Multibridgerouter contract.
var MultibridgerouterMetaData = &bind.MetaData{
	ABI: "[{\"inputs\":[{\"internalType\":\"address\",\"name\":\"bridge\",\"type\":\"address\"},{\"internalType\":\"uint32\",\"name\":\"destinationNetworkA\",\"type\":\"uint32\"},{\"internalType\":\"address\",\"name\":\"destinationAddressA\",\"type\":\"address\"},{\"internalType\":\"uint256\",\"name\":\"amountA\",\"type\":\"uint256\"},{\"internalType\":\"uint32\",\"name\":\"destinationNetworkB\",\"type\":\"uint32\"},{\"internalType\":\"address\",\"name\":\"destinationAddressB\",\"type\":\"address\"},{\"internalType\":\"uint256\",\"name\":\"amountB\",\"type\":\"uint256\"}],\"name\":\"bridgeTwice\",\"outputs\":[],\"stateMutability\":\"payable\",\"type\":\"function\"}]",
	Bin: "0x608060405234801561001057600080fd5b506102bd806100206000396000f3fe60806040526004361061001e5760003560e01c80632e288acc14610023575b600080fd5b6100366100313660046101a1565b610038565b005b6100428185610219565b34146100945760405162461bcd60e51b815260206004820152601e60248201527f4d756c7469427269646765526f757465723a2077726f6e672076616c75650000604482015260640160405180910390fd5b60405163cd58657960e01b81526001600160a01b0388169063cd5865799086906100cc908a908a908490600090600190600401610240565b6000604051808303818588803b1580156100e557600080fd5b505af11580156100f9573d6000803e3d6000fd5b505060405163cd58657960e01b81526001600160a01b038b16935063cd586579925084915061013690879087908490600090600190600401610240565b6000604051808303818588803b15801561014f57600080fd5b505af1158015610163573d6000803e3d6000fd5b505050505050505050505050565b80356001600160a01b038116811461018857600080fd5b919050565b803563ffffffff8116811461018857600080fd5b600080600080600080600060e0888a0312156101bc57600080fd5b6101c588610171565b96506101d36020890161018d565b95506101e160408901610171565b9450606088013593506101f66080890161018d565b925061020460a08901610171565b915060c0880135905092959891949750929550565b8082018082111561023a57634e487b7160e01b600052601160045260246000fd5b92915050565b63ffffffff9590951685526001600160a01b039384166020860152604085019290925290911660608301521515608082015260c060a0820181905260009082015260e0019056fea26469706673582212204f2d95b323d1ce491080ebb428df4604c657890321ba48b2f5f9039a4fdfd6aa64736f6c63430008120033",
}

// MultibridgerouterABI is the input ABI used to generate the binding from.
// Deprecated: Use MultibridgerouterMetaData.ABI instead.
var MultibridgerouterABI = MultibridgerouterMetaData.ABI

// MultibridgerouterBin is the compiled bytecode used for deploying new contracts.
// Deprecated: Use MultibridgerouterMetaData.Bin instead.
var MultibridgerouterBin = MultibridgerouterMetaData.Bin

// DeployMultibridgerouter deploys a new Ethereum contract, binding an instance of Multibridgerouter to it.
func DeployMultibridgerouter(auth *bind.TransactOpts, backend bind.ContractBackend) (common.Address, *types.Transaction, *Multibridgerouter, error) {
	parsed, err := MultibridgerouterMetaData.GetAbi()
	if err != nil {
		return common.Address{}, nil, nil, err
	}
	if parsed == nil {
		return common.Address{}, nil, nil, errors.New("GetABI returned nil")
	}

	address, tx, contract, err := bind.DeployContract(auth, *parsed, common.FromHex(MultibridgerouterBin), backend)
	if err != nil {
		return common.Address{}, nil, nil, err
	}
	return address, tx, &Multibridgerouter{MultibridgerouterCaller: MultibridgerouterCaller{contract: contract}, MultibridgerouterTransactor: MultibridgerouterTransactor{contract: contract}, MultibridgerouterFilterer: MultibridgerouterFilterer{contract: contract}}, nil
}

// Multibridgerouter is an auto generated Go binding around an Ethereum contract.
type Multibridgerouter struct {
	MultibridgerouterCaller     // Read-only binding to the contract
	MultibridgerouterTransactor // Write-only binding to the contract
	MultibridgerouterFilterer   // Log filterer for contract events
}

// MultibridgerouterCaller is an auto generated read-only Go binding around an Ethereum contract.
type MultibridgerouterCaller struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// MultibridgerouterTransactor is an auto generated write-only Go binding around an Ethereum contract.
type MultibridgerouterTransactor struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// MultibridgerouterFilterer is an auto generated log filtering Go binding around an Ethereum contract events.
type MultibridgerouterFilterer struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// MultibridgerouterSession is an auto generated Go binding around an Ethereum contract,
// with pre-set call and transact options.
type MultibridgerouterSession struct {
	Contract     *Multibridgerouter // Generic contract binding to set the session for
	CallOpts     bind.CallOpts      // Call options to use throughout this session
	TransactOpts bind.TransactOpts  // Transaction auth options to use throughout this session
}

// MultibridgerouterCallerSession is an auto generated read-only Go binding around an Ethereum contract,
// with pre-set call options.
type MultibridgerouterCallerSession struct {
	Contract *MultibridgerouterCaller // Generic contract caller binding to set the session for
	CallOpts bind.CallOpts            // Call options to use throughout this session
}

// MultibridgerouterTransactorSession is an auto generated write-only Go binding around an Ethereum contract,
// with pre-set transact options.
type MultibridgerouterTransactorSession struct {
	Contract     *MultibridgerouterTransactor // Generic contract transactor binding to set the session for
	TransactOpts bind.TransactOpts            // Transaction auth options to use throughout this session
}

// MultibridgerouterRaw is an auto generated low-level Go binding around an Ethereum contract.
type MultibridgerouterRaw struct {
	Contract *Multibridgerouter // Generic contract binding to access the raw methods on
}

// MultibridgerouterCallerRaw is an auto generated low-level read-only Go binding around an Ethereum contract.
type MultibridgerouterCallerRaw struct {
	Contract *MultibridgerouterCaller // Generic read-only contract binding to access the raw methods on
}

// MultibridgerouterTransactorRaw is an auto generated low-level write-only Go binding around an Ethereum contract.
type MultibridgerouterTransactorRaw struct {
	Contract *MultibridgerouterTransactor // Generic write-only contract binding to access the raw methods on
}

// NewMultibridgerouter creates a new instance of Multibridgerouter, bound to a specific deployed contract.
func NewMultibridgerouter(address common.Address, backend bind.ContractBackend) (*Multibridgerouter, error) {
	contract, err := bindMultibridgerouter(address, backend, backend, backend)
	if err != nil {
		return nil, err
	}
	return &Multibridgerouter{MultibridgerouterCaller: MultibridgerouterCaller{contract: contract}, MultibridgerouterTransactor: MultibridgerouterTransactor{contract: contract}, MultibridgerouterFilterer: MultibridgerouterFilterer{contract: contract}}, nil
}

// NewMultibridgerouterCaller creates a new read-only instance of Multibridgerouter, bound to a specific deployed contract.
func NewMultibridgerouterCaller(address common.Address, caller bind.ContractCaller) (*MultibridgerouterCaller, error) {
	contract, err := bindMultibridgerouter(address, caller, nil, nil)
	if err != nil {
		return nil, err
	}
	return &MultibridgerouterCaller{contract: contract}, nil
}

// NewMultibridgerouterTransactor creates a new write-only instance of Multibridgerouter, bound to a specific deployed contract.
func NewMultibridgerouterTransactor(address common.Address, transactor bind.ContractTransactor) (*MultibridgerouterTransactor, error) {
	contract, err := bindMultibridgerouter(address, nil, transactor, nil)
	if err != nil {
		return nil, err
	}
	return &MultibridgerouterTransactor{contract: contract}, nil
}

// NewMultibridgerouterFilterer creates a new log filterer instance of Multibridgerouter, bound to a specific deployed contract.
func NewMultibridgerouterFilterer(address common.Address, filterer bind.ContractFilterer) (*MultibridgerouterFilterer, error) {
	contract, err := bindMultibridgerouter(address, nil, nil, filterer)
	if err != nil {
		return nil, err
	}
	return &MultibridgerouterFilterer{contract: contract}, nil
}

// bindMultibridgerouter binds a generic wrapper to an already deployed contract.
func bindMultibridgerouter(address common.Address, caller bind.ContractCaller, transactor bind.ContractTransactor, filterer bind.ContractFilterer) (*bind.BoundContract, error) {
	parsed, err := MultibridgerouterMetaData.GetAbi()
	if err != nil {
		return nil, err
	}
	return bind.NewBoundContract(address, *parsed, caller, transactor, filterer), nil
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_Multibridgerouter *MultibridgerouterRaw) Call(opts *bind.CallOpts, result *[]interface{}, method string, params ...interface{}) error {
	return _Multibridgerouter.Contract.MultibridgerouterCaller.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_Multibridgerouter *MultibridgerouterRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _Multibridgerouter.Contract.MultibridgerouterTransactor.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_Multibridgerouter *MultibridgerouterRaw) Transact(opts *bind.TransactOpts, method string, params ...interface{}) (*types.Transaction, error) {
	return _Multibridgerouter.Contract.MultibridgerouterTransactor.contract.Transact(opts, method, params...)
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_Multibridgerouter *MultibridgerouterCallerRaw) Call(opts *bind.CallOpts, result *[]interface{}, method string, params ...interface{}) error {
	return _Multibridgerouter.Contract.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_Multibridgerouter *MultibridgerouterTransactorRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _Multibridgerouter.Contract.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_Multibridgerouter *MultibridgerouterTransactorRaw) Transact(opts *bind.TransactOpts, method string, params ...interface{}) (*types.Transaction, error) {
	return _Multibridgerouter.Contract.contract.Transact(opts, method, params...)
}

// BridgeTwice is a paid mutator transaction binding the contract method 0x2e288acc.
//
// Solidity: function bridgeTwice(address bridge, uint32 destinationNetworkA, address destinationAddressA, uint256 amountA, uint32 destinationNetworkB, address destinationAddressB, uint256 amountB) payable returns()
func (_Multibridgerouter *MultibridgerouterTransactor) BridgeTwice(opts *bind.TransactOpts, bridge common.Address, destinationNetworkA uint32, destinationAddressA common.Address, amountA *big.Int, destinationNetworkB uint32, destinationAddressB common.Address, amountB *big.Int) (*types.Transaction, error) {
	return _Multibridgerouter.contract.Transact(opts, "bridgeTwice", bridge, destinationNetworkA, destinationAddressA, amountA, destinationNetworkB, destinationAddressB, amountB)
}

// BridgeTwice is a paid mutator transaction binding the contract method 0x2e288acc.
//
// Solidity: function bridgeTwice(address bridge, uint32 destinationNetworkA, address destinationAddressA, uint256 amountA, uint32 destinationNetworkB, address destinationAddressB, uint256 amountB) payable returns()
func (_Multibridgerouter *MultibridgerouterSession) BridgeTwice(bridge common.Address, destinationNetworkA uint32, destinationAddressA common.Address, amountA *big.Int, destinationNetworkB uint32, destinationAddressB common.Address, amountB *big.Int) (*types.Transaction, error) {
	return _Multibridgerouter.Contract.BridgeTwice(&_Multibridgerouter.TransactOpts, bridge, destinationNetworkA, destinationAddressA, amountA, destinationNetworkB, destinationAddressB, amountB)
}

// BridgeTwice is a paid mutator transaction binding the contract method 0x2e288acc.
//
// Solidity: function bridgeTwice(address bridge, uint32 destinationNetworkA, address destinationAddressA, uint256 amountA, uint32 destinationNetworkB, address destinationAddressB, uint256 amountB) payable returns()
func (_Multibridgerouter *MultibridgerouterTransactorSession) BridgeTwice(bridge common.Address, destinationNetworkA uint32, destinationAddressA common.Address, amountA *big.Int, destinationNetworkB uint32, destinationAddressB common.Address, amountB *big.Int) (*types.Transaction, error) {
	return _Multibridgerouter.Contract.BridgeTwice(&_Multibridgerouter.TransactOpts, bridge, destinationNetworkA, destinationAddressA, amountA, destinationNetworkB, destinationAddressB, amountB)
}
