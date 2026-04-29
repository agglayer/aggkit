// Code generated - DO NOT EDIT.
// This file is a generated binding and any manual changes will be lost.

package bindings

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

// AssignJobParam is an auto generated low-level Go binding around an user-defined struct.
type AssignJobParam struct {
	DstEid        uint32
	PacketHeader  []byte
	PayloadHash   [32]byte
	Confirmations uint64
	Sender        common.Address
}

// AggLayerDVNMetaData contains all meta data concerning the AggLayerDVN contract.
var AggLayerDVNMetaData = &bind.MetaData{
	ABI: "[{\"inputs\":[{\"internalType\":\"address\",\"name\":\"initialOwner\",\"type\":\"address\"}],\"stateMutability\":\"nonpayable\",\"type\":\"constructor\"},{\"inputs\":[],\"name\":\"FlatFeeNotSet\",\"type\":\"error\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"owner\",\"type\":\"address\"}],\"name\":\"OwnableInvalidOwner\",\"type\":\"error\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"account\",\"type\":\"address\"}],\"name\":\"OwnableUnauthorizedAccount\",\"type\":\"error\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"sender\",\"type\":\"address\"}],\"name\":\"UnauthorizedSender\",\"type\":\"error\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":false,\"internalType\":\"address\",\"name\":\"to\",\"type\":\"address\"},{\"indexed\":false,\"internalType\":\"uint256\",\"name\":\"amount\",\"type\":\"uint256\"}],\"name\":\"FeesWithdrawn\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":false,\"internalType\":\"uint256\",\"name\":\"fee\",\"type\":\"uint256\"}],\"name\":\"FlatFeeSet\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":false,\"internalType\":\"bytes\",\"name\":\"packetHeader\",\"type\":\"bytes\"},{\"indexed\":false,\"internalType\":\"bytes32\",\"name\":\"payloadHash\",\"type\":\"bytes32\"},{\"indexed\":false,\"internalType\":\"uint32\",\"name\":\"dstEid\",\"type\":\"uint32\"},{\"indexed\":false,\"internalType\":\"address\",\"name\":\"sender\",\"type\":\"address\"},{\"indexed\":false,\"internalType\":\"uint256\",\"name\":\"fee\",\"type\":\"uint256\"}],\"name\":\"JobAssigned\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":true,\"internalType\":\"address\",\"name\":\"previousOwner\",\"type\":\"address\"},{\"indexed\":true,\"internalType\":\"address\",\"name\":\"newOwner\",\"type\":\"address\"}],\"name\":\"OwnershipTransferred\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":false,\"internalType\":\"address\",\"name\":\"sender\",\"type\":\"address\"}],\"name\":\"SenderAdded\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":false,\"internalType\":\"address\",\"name\":\"sender\",\"type\":\"address\"}],\"name\":\"SenderRemoved\",\"type\":\"event\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"sender\",\"type\":\"address\"}],\"name\":\"addSender\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"\",\"type\":\"address\"}],\"name\":\"allowedSenders\",\"outputs\":[{\"internalType\":\"bool\",\"name\":\"\",\"type\":\"bool\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[{\"components\":[{\"internalType\":\"uint32\",\"name\":\"dstEid\",\"type\":\"uint32\"},{\"internalType\":\"bytes\",\"name\":\"packetHeader\",\"type\":\"bytes\"},{\"internalType\":\"bytes32\",\"name\":\"payloadHash\",\"type\":\"bytes32\"},{\"internalType\":\"uint64\",\"name\":\"confirmations\",\"type\":\"uint64\"},{\"internalType\":\"address\",\"name\":\"sender\",\"type\":\"address\"}],\"internalType\":\"structAssignJobParam\",\"name\":\"_param\",\"type\":\"tuple\"},{\"internalType\":\"bytes\",\"name\":\"_options\",\"type\":\"bytes\"}],\"name\":\"assignJob\",\"outputs\":[{\"internalType\":\"uint256\",\"name\":\"fee\",\"type\":\"uint256\"}],\"stateMutability\":\"payable\",\"type\":\"function\"},{\"inputs\":[],\"name\":\"flatFee\",\"outputs\":[{\"internalType\":\"uint256\",\"name\":\"\",\"type\":\"uint256\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[],\"name\":\"owner\",\"outputs\":[{\"internalType\":\"address\",\"name\":\"\",\"type\":\"address\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"sender\",\"type\":\"address\"}],\"name\":\"removeSender\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[],\"name\":\"renounceOwnership\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"uint256\",\"name\":\"fee_\",\"type\":\"uint256\"}],\"name\":\"setFlatFee\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"newOwner\",\"type\":\"address\"}],\"name\":\"transferOwnership\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"addresspayable\",\"name\":\"to\",\"type\":\"address\"},{\"internalType\":\"uint256\",\"name\":\"amount\",\"type\":\"uint256\"}],\"name\":\"withdrawFees\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"}]",
}

// AggLayerDVNABI is the input ABI used to generate the binding from.
// Deprecated: Use AggLayerDVNMetaData.ABI instead.
var AggLayerDVNABI = AggLayerDVNMetaData.ABI

// AggLayerDVN is an auto generated Go binding around an Ethereum contract.
type AggLayerDVN struct {
	AggLayerDVNCaller     // Read-only binding to the contract
	AggLayerDVNTransactor // Write-only binding to the contract
	AggLayerDVNFilterer   // Log filterer for contract events
}

// AggLayerDVNCaller is an auto generated read-only Go binding around an Ethereum contract.
type AggLayerDVNCaller struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// AggLayerDVNTransactor is an auto generated write-only Go binding around an Ethereum contract.
type AggLayerDVNTransactor struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// AggLayerDVNFilterer is an auto generated log filtering Go binding around an Ethereum contract events.
type AggLayerDVNFilterer struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// AggLayerDVNSession is an auto generated Go binding around an Ethereum contract,
// with pre-set call and transact options.
type AggLayerDVNSession struct {
	Contract     *AggLayerDVN      // Generic contract binding to set the session for
	CallOpts     bind.CallOpts     // Call options to use throughout this session
	TransactOpts bind.TransactOpts // Transaction auth options to use throughout this session
}

// AggLayerDVNCallerSession is an auto generated read-only Go binding around an Ethereum contract,
// with pre-set call options.
type AggLayerDVNCallerSession struct {
	Contract *AggLayerDVNCaller // Generic contract caller binding to set the session for
	CallOpts bind.CallOpts      // Call options to use throughout this session
}

// AggLayerDVNTransactorSession is an auto generated write-only Go binding around an Ethereum contract,
// with pre-set transact options.
type AggLayerDVNTransactorSession struct {
	Contract     *AggLayerDVNTransactor // Generic contract transactor binding to set the session for
	TransactOpts bind.TransactOpts      // Transaction auth options to use throughout this session
}

// AggLayerDVNRaw is an auto generated low-level Go binding around an Ethereum contract.
type AggLayerDVNRaw struct {
	Contract *AggLayerDVN // Generic contract binding to access the raw methods on
}

// AggLayerDVNCallerRaw is an auto generated low-level read-only Go binding around an Ethereum contract.
type AggLayerDVNCallerRaw struct {
	Contract *AggLayerDVNCaller // Generic read-only contract binding to access the raw methods on
}

// AggLayerDVNTransactorRaw is an auto generated low-level write-only Go binding around an Ethereum contract.
type AggLayerDVNTransactorRaw struct {
	Contract *AggLayerDVNTransactor // Generic write-only contract binding to access the raw methods on
}

// NewAggLayerDVN creates a new instance of AggLayerDVN, bound to a specific deployed contract.
func NewAggLayerDVN(address common.Address, backend bind.ContractBackend) (*AggLayerDVN, error) {
	contract, err := bindAggLayerDVN(address, backend, backend, backend)
	if err != nil {
		return nil, err
	}
	return &AggLayerDVN{AggLayerDVNCaller: AggLayerDVNCaller{contract: contract}, AggLayerDVNTransactor: AggLayerDVNTransactor{contract: contract}, AggLayerDVNFilterer: AggLayerDVNFilterer{contract: contract}}, nil
}

// NewAggLayerDVNCaller creates a new read-only instance of AggLayerDVN, bound to a specific deployed contract.
func NewAggLayerDVNCaller(address common.Address, caller bind.ContractCaller) (*AggLayerDVNCaller, error) {
	contract, err := bindAggLayerDVN(address, caller, nil, nil)
	if err != nil {
		return nil, err
	}
	return &AggLayerDVNCaller{contract: contract}, nil
}

// NewAggLayerDVNTransactor creates a new write-only instance of AggLayerDVN, bound to a specific deployed contract.
func NewAggLayerDVNTransactor(address common.Address, transactor bind.ContractTransactor) (*AggLayerDVNTransactor, error) {
	contract, err := bindAggLayerDVN(address, nil, transactor, nil)
	if err != nil {
		return nil, err
	}
	return &AggLayerDVNTransactor{contract: contract}, nil
}

// NewAggLayerDVNFilterer creates a new log filterer instance of AggLayerDVN, bound to a specific deployed contract.
func NewAggLayerDVNFilterer(address common.Address, filterer bind.ContractFilterer) (*AggLayerDVNFilterer, error) {
	contract, err := bindAggLayerDVN(address, nil, nil, filterer)
	if err != nil {
		return nil, err
	}
	return &AggLayerDVNFilterer{contract: contract}, nil
}

// bindAggLayerDVN binds a generic wrapper to an already deployed contract.
func bindAggLayerDVN(address common.Address, caller bind.ContractCaller, transactor bind.ContractTransactor, filterer bind.ContractFilterer) (*bind.BoundContract, error) {
	parsed, err := AggLayerDVNMetaData.GetAbi()
	if err != nil {
		return nil, err
	}
	return bind.NewBoundContract(address, *parsed, caller, transactor, filterer), nil
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_AggLayerDVN *AggLayerDVNRaw) Call(opts *bind.CallOpts, result *[]interface{}, method string, params ...interface{}) error {
	return _AggLayerDVN.Contract.AggLayerDVNCaller.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_AggLayerDVN *AggLayerDVNRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _AggLayerDVN.Contract.AggLayerDVNTransactor.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_AggLayerDVN *AggLayerDVNRaw) Transact(opts *bind.TransactOpts, method string, params ...interface{}) (*types.Transaction, error) {
	return _AggLayerDVN.Contract.AggLayerDVNTransactor.contract.Transact(opts, method, params...)
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_AggLayerDVN *AggLayerDVNCallerRaw) Call(opts *bind.CallOpts, result *[]interface{}, method string, params ...interface{}) error {
	return _AggLayerDVN.Contract.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_AggLayerDVN *AggLayerDVNTransactorRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _AggLayerDVN.Contract.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_AggLayerDVN *AggLayerDVNTransactorRaw) Transact(opts *bind.TransactOpts, method string, params ...interface{}) (*types.Transaction, error) {
	return _AggLayerDVN.Contract.contract.Transact(opts, method, params...)
}

// AllowedSenders is a free data retrieval call binding the contract method 0xfadbcf48.
//
// Solidity: function allowedSenders(address ) view returns(bool)
func (_AggLayerDVN *AggLayerDVNCaller) AllowedSenders(opts *bind.CallOpts, arg0 common.Address) (bool, error) {
	var out []interface{}
	err := _AggLayerDVN.contract.Call(opts, &out, "allowedSenders", arg0)

	if err != nil {
		return *new(bool), err
	}

	out0 := *abi.ConvertType(out[0], new(bool)).(*bool)

	return out0, err

}

// AllowedSenders is a free data retrieval call binding the contract method 0xfadbcf48.
//
// Solidity: function allowedSenders(address ) view returns(bool)
func (_AggLayerDVN *AggLayerDVNSession) AllowedSenders(arg0 common.Address) (bool, error) {
	return _AggLayerDVN.Contract.AllowedSenders(&_AggLayerDVN.CallOpts, arg0)
}

// AllowedSenders is a free data retrieval call binding the contract method 0xfadbcf48.
//
// Solidity: function allowedSenders(address ) view returns(bool)
func (_AggLayerDVN *AggLayerDVNCallerSession) AllowedSenders(arg0 common.Address) (bool, error) {
	return _AggLayerDVN.Contract.AllowedSenders(&_AggLayerDVN.CallOpts, arg0)
}

// FlatFee is a free data retrieval call binding the contract method 0xd9eb5947.
//
// Solidity: function flatFee() view returns(uint256)
func (_AggLayerDVN *AggLayerDVNCaller) FlatFee(opts *bind.CallOpts) (*big.Int, error) {
	var out []interface{}
	err := _AggLayerDVN.contract.Call(opts, &out, "flatFee")

	if err != nil {
		return *new(*big.Int), err
	}

	out0 := *abi.ConvertType(out[0], new(*big.Int)).(**big.Int)

	return out0, err

}

// FlatFee is a free data retrieval call binding the contract method 0xd9eb5947.
//
// Solidity: function flatFee() view returns(uint256)
func (_AggLayerDVN *AggLayerDVNSession) FlatFee() (*big.Int, error) {
	return _AggLayerDVN.Contract.FlatFee(&_AggLayerDVN.CallOpts)
}

// FlatFee is a free data retrieval call binding the contract method 0xd9eb5947.
//
// Solidity: function flatFee() view returns(uint256)
func (_AggLayerDVN *AggLayerDVNCallerSession) FlatFee() (*big.Int, error) {
	return _AggLayerDVN.Contract.FlatFee(&_AggLayerDVN.CallOpts)
}

// Owner is a free data retrieval call binding the contract method 0x8da5cb5b.
//
// Solidity: function owner() view returns(address)
func (_AggLayerDVN *AggLayerDVNCaller) Owner(opts *bind.CallOpts) (common.Address, error) {
	var out []interface{}
	err := _AggLayerDVN.contract.Call(opts, &out, "owner")

	if err != nil {
		return *new(common.Address), err
	}

	out0 := *abi.ConvertType(out[0], new(common.Address)).(*common.Address)

	return out0, err

}

// Owner is a free data retrieval call binding the contract method 0x8da5cb5b.
//
// Solidity: function owner() view returns(address)
func (_AggLayerDVN *AggLayerDVNSession) Owner() (common.Address, error) {
	return _AggLayerDVN.Contract.Owner(&_AggLayerDVN.CallOpts)
}

// Owner is a free data retrieval call binding the contract method 0x8da5cb5b.
//
// Solidity: function owner() view returns(address)
func (_AggLayerDVN *AggLayerDVNCallerSession) Owner() (common.Address, error) {
	return _AggLayerDVN.Contract.Owner(&_AggLayerDVN.CallOpts)
}

// AddSender is a paid mutator transaction binding the contract method 0xb697f531.
//
// Solidity: function addSender(address sender) returns()
func (_AggLayerDVN *AggLayerDVNTransactor) AddSender(opts *bind.TransactOpts, sender common.Address) (*types.Transaction, error) {
	return _AggLayerDVN.contract.Transact(opts, "addSender", sender)
}

// AddSender is a paid mutator transaction binding the contract method 0xb697f531.
//
// Solidity: function addSender(address sender) returns()
func (_AggLayerDVN *AggLayerDVNSession) AddSender(sender common.Address) (*types.Transaction, error) {
	return _AggLayerDVN.Contract.AddSender(&_AggLayerDVN.TransactOpts, sender)
}

// AddSender is a paid mutator transaction binding the contract method 0xb697f531.
//
// Solidity: function addSender(address sender) returns()
func (_AggLayerDVN *AggLayerDVNTransactorSession) AddSender(sender common.Address) (*types.Transaction, error) {
	return _AggLayerDVN.Contract.AddSender(&_AggLayerDVN.TransactOpts, sender)
}

// AssignJob is a paid mutator transaction binding the contract method 0x95d376d7.
//
// Solidity: function assignJob((uint32,bytes,bytes32,uint64,address) _param, bytes _options) payable returns(uint256 fee)
func (_AggLayerDVN *AggLayerDVNTransactor) AssignJob(opts *bind.TransactOpts, _param AssignJobParam, _options []byte) (*types.Transaction, error) {
	return _AggLayerDVN.contract.Transact(opts, "assignJob", _param, _options)
}

// AssignJob is a paid mutator transaction binding the contract method 0x95d376d7.
//
// Solidity: function assignJob((uint32,bytes,bytes32,uint64,address) _param, bytes _options) payable returns(uint256 fee)
func (_AggLayerDVN *AggLayerDVNSession) AssignJob(_param AssignJobParam, _options []byte) (*types.Transaction, error) {
	return _AggLayerDVN.Contract.AssignJob(&_AggLayerDVN.TransactOpts, _param, _options)
}

// AssignJob is a paid mutator transaction binding the contract method 0x95d376d7.
//
// Solidity: function assignJob((uint32,bytes,bytes32,uint64,address) _param, bytes _options) payable returns(uint256 fee)
func (_AggLayerDVN *AggLayerDVNTransactorSession) AssignJob(_param AssignJobParam, _options []byte) (*types.Transaction, error) {
	return _AggLayerDVN.Contract.AssignJob(&_AggLayerDVN.TransactOpts, _param, _options)
}

// RemoveSender is a paid mutator transaction binding the contract method 0xb2f87643.
//
// Solidity: function removeSender(address sender) returns()
func (_AggLayerDVN *AggLayerDVNTransactor) RemoveSender(opts *bind.TransactOpts, sender common.Address) (*types.Transaction, error) {
	return _AggLayerDVN.contract.Transact(opts, "removeSender", sender)
}

// RemoveSender is a paid mutator transaction binding the contract method 0xb2f87643.
//
// Solidity: function removeSender(address sender) returns()
func (_AggLayerDVN *AggLayerDVNSession) RemoveSender(sender common.Address) (*types.Transaction, error) {
	return _AggLayerDVN.Contract.RemoveSender(&_AggLayerDVN.TransactOpts, sender)
}

// RemoveSender is a paid mutator transaction binding the contract method 0xb2f87643.
//
// Solidity: function removeSender(address sender) returns()
func (_AggLayerDVN *AggLayerDVNTransactorSession) RemoveSender(sender common.Address) (*types.Transaction, error) {
	return _AggLayerDVN.Contract.RemoveSender(&_AggLayerDVN.TransactOpts, sender)
}

// RenounceOwnership is a paid mutator transaction binding the contract method 0x715018a6.
//
// Solidity: function renounceOwnership() returns()
func (_AggLayerDVN *AggLayerDVNTransactor) RenounceOwnership(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _AggLayerDVN.contract.Transact(opts, "renounceOwnership")
}

// RenounceOwnership is a paid mutator transaction binding the contract method 0x715018a6.
//
// Solidity: function renounceOwnership() returns()
func (_AggLayerDVN *AggLayerDVNSession) RenounceOwnership() (*types.Transaction, error) {
	return _AggLayerDVN.Contract.RenounceOwnership(&_AggLayerDVN.TransactOpts)
}

// RenounceOwnership is a paid mutator transaction binding the contract method 0x715018a6.
//
// Solidity: function renounceOwnership() returns()
func (_AggLayerDVN *AggLayerDVNTransactorSession) RenounceOwnership() (*types.Transaction, error) {
	return _AggLayerDVN.Contract.RenounceOwnership(&_AggLayerDVN.TransactOpts)
}

// SetFlatFee is a paid mutator transaction binding the contract method 0x23fa495a.
//
// Solidity: function setFlatFee(uint256 fee_) returns()
func (_AggLayerDVN *AggLayerDVNTransactor) SetFlatFee(opts *bind.TransactOpts, fee_ *big.Int) (*types.Transaction, error) {
	return _AggLayerDVN.contract.Transact(opts, "setFlatFee", fee_)
}

// SetFlatFee is a paid mutator transaction binding the contract method 0x23fa495a.
//
// Solidity: function setFlatFee(uint256 fee_) returns()
func (_AggLayerDVN *AggLayerDVNSession) SetFlatFee(fee_ *big.Int) (*types.Transaction, error) {
	return _AggLayerDVN.Contract.SetFlatFee(&_AggLayerDVN.TransactOpts, fee_)
}

// SetFlatFee is a paid mutator transaction binding the contract method 0x23fa495a.
//
// Solidity: function setFlatFee(uint256 fee_) returns()
func (_AggLayerDVN *AggLayerDVNTransactorSession) SetFlatFee(fee_ *big.Int) (*types.Transaction, error) {
	return _AggLayerDVN.Contract.SetFlatFee(&_AggLayerDVN.TransactOpts, fee_)
}

// TransferOwnership is a paid mutator transaction binding the contract method 0xf2fde38b.
//
// Solidity: function transferOwnership(address newOwner) returns()
func (_AggLayerDVN *AggLayerDVNTransactor) TransferOwnership(opts *bind.TransactOpts, newOwner common.Address) (*types.Transaction, error) {
	return _AggLayerDVN.contract.Transact(opts, "transferOwnership", newOwner)
}

// TransferOwnership is a paid mutator transaction binding the contract method 0xf2fde38b.
//
// Solidity: function transferOwnership(address newOwner) returns()
func (_AggLayerDVN *AggLayerDVNSession) TransferOwnership(newOwner common.Address) (*types.Transaction, error) {
	return _AggLayerDVN.Contract.TransferOwnership(&_AggLayerDVN.TransactOpts, newOwner)
}

// TransferOwnership is a paid mutator transaction binding the contract method 0xf2fde38b.
//
// Solidity: function transferOwnership(address newOwner) returns()
func (_AggLayerDVN *AggLayerDVNTransactorSession) TransferOwnership(newOwner common.Address) (*types.Transaction, error) {
	return _AggLayerDVN.Contract.TransferOwnership(&_AggLayerDVN.TransactOpts, newOwner)
}

// WithdrawFees is a paid mutator transaction binding the contract method 0xad3b1b47.
//
// Solidity: function withdrawFees(address to, uint256 amount) returns()
func (_AggLayerDVN *AggLayerDVNTransactor) WithdrawFees(opts *bind.TransactOpts, to common.Address, amount *big.Int) (*types.Transaction, error) {
	return _AggLayerDVN.contract.Transact(opts, "withdrawFees", to, amount)
}

// WithdrawFees is a paid mutator transaction binding the contract method 0xad3b1b47.
//
// Solidity: function withdrawFees(address to, uint256 amount) returns()
func (_AggLayerDVN *AggLayerDVNSession) WithdrawFees(to common.Address, amount *big.Int) (*types.Transaction, error) {
	return _AggLayerDVN.Contract.WithdrawFees(&_AggLayerDVN.TransactOpts, to, amount)
}

// WithdrawFees is a paid mutator transaction binding the contract method 0xad3b1b47.
//
// Solidity: function withdrawFees(address to, uint256 amount) returns()
func (_AggLayerDVN *AggLayerDVNTransactorSession) WithdrawFees(to common.Address, amount *big.Int) (*types.Transaction, error) {
	return _AggLayerDVN.Contract.WithdrawFees(&_AggLayerDVN.TransactOpts, to, amount)
}

// AggLayerDVNFeesWithdrawnIterator is returned from FilterFeesWithdrawn and is used to iterate over the raw logs and unpacked data for FeesWithdrawn events raised by the AggLayerDVN contract.
type AggLayerDVNFeesWithdrawnIterator struct {
	Event *AggLayerDVNFeesWithdrawn // Event containing the contract specifics and raw log

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
func (it *AggLayerDVNFeesWithdrawnIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(AggLayerDVNFeesWithdrawn)
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
		it.Event = new(AggLayerDVNFeesWithdrawn)
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
func (it *AggLayerDVNFeesWithdrawnIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *AggLayerDVNFeesWithdrawnIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// AggLayerDVNFeesWithdrawn represents a FeesWithdrawn event raised by the AggLayerDVN contract.
type AggLayerDVNFeesWithdrawn struct {
	To     common.Address
	Amount *big.Int
	Raw    types.Log // Blockchain specific contextual infos
}

// FilterFeesWithdrawn is a free log retrieval operation binding the contract event 0xc0819c13be868895eb93e40eaceb96de976442fa1d404e5c55f14bb65a8c489a.
//
// Solidity: event FeesWithdrawn(address to, uint256 amount)
func (_AggLayerDVN *AggLayerDVNFilterer) FilterFeesWithdrawn(opts *bind.FilterOpts) (*AggLayerDVNFeesWithdrawnIterator, error) {

	logs, sub, err := _AggLayerDVN.contract.FilterLogs(opts, "FeesWithdrawn")
	if err != nil {
		return nil, err
	}
	return &AggLayerDVNFeesWithdrawnIterator{contract: _AggLayerDVN.contract, event: "FeesWithdrawn", logs: logs, sub: sub}, nil
}

// WatchFeesWithdrawn is a free log subscription operation binding the contract event 0xc0819c13be868895eb93e40eaceb96de976442fa1d404e5c55f14bb65a8c489a.
//
// Solidity: event FeesWithdrawn(address to, uint256 amount)
func (_AggLayerDVN *AggLayerDVNFilterer) WatchFeesWithdrawn(opts *bind.WatchOpts, sink chan<- *AggLayerDVNFeesWithdrawn) (event.Subscription, error) {

	logs, sub, err := _AggLayerDVN.contract.WatchLogs(opts, "FeesWithdrawn")
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(AggLayerDVNFeesWithdrawn)
				if err := _AggLayerDVN.contract.UnpackLog(event, "FeesWithdrawn", log); err != nil {
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

// ParseFeesWithdrawn is a log parse operation binding the contract event 0xc0819c13be868895eb93e40eaceb96de976442fa1d404e5c55f14bb65a8c489a.
//
// Solidity: event FeesWithdrawn(address to, uint256 amount)
func (_AggLayerDVN *AggLayerDVNFilterer) ParseFeesWithdrawn(log types.Log) (*AggLayerDVNFeesWithdrawn, error) {
	event := new(AggLayerDVNFeesWithdrawn)
	if err := _AggLayerDVN.contract.UnpackLog(event, "FeesWithdrawn", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// AggLayerDVNFlatFeeSetIterator is returned from FilterFlatFeeSet and is used to iterate over the raw logs and unpacked data for FlatFeeSet events raised by the AggLayerDVN contract.
type AggLayerDVNFlatFeeSetIterator struct {
	Event *AggLayerDVNFlatFeeSet // Event containing the contract specifics and raw log

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
func (it *AggLayerDVNFlatFeeSetIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(AggLayerDVNFlatFeeSet)
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
		it.Event = new(AggLayerDVNFlatFeeSet)
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
func (it *AggLayerDVNFlatFeeSetIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *AggLayerDVNFlatFeeSetIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// AggLayerDVNFlatFeeSet represents a FlatFeeSet event raised by the AggLayerDVN contract.
type AggLayerDVNFlatFeeSet struct {
	Fee *big.Int
	Raw types.Log // Blockchain specific contextual infos
}

// FilterFlatFeeSet is a free log retrieval operation binding the contract event 0x02e6220a8c55d7fd57319e91a44a9aac5a5fd4c64a19bb34bb83c8cedbf3f398.
//
// Solidity: event FlatFeeSet(uint256 fee)
func (_AggLayerDVN *AggLayerDVNFilterer) FilterFlatFeeSet(opts *bind.FilterOpts) (*AggLayerDVNFlatFeeSetIterator, error) {

	logs, sub, err := _AggLayerDVN.contract.FilterLogs(opts, "FlatFeeSet")
	if err != nil {
		return nil, err
	}
	return &AggLayerDVNFlatFeeSetIterator{contract: _AggLayerDVN.contract, event: "FlatFeeSet", logs: logs, sub: sub}, nil
}

// WatchFlatFeeSet is a free log subscription operation binding the contract event 0x02e6220a8c55d7fd57319e91a44a9aac5a5fd4c64a19bb34bb83c8cedbf3f398.
//
// Solidity: event FlatFeeSet(uint256 fee)
func (_AggLayerDVN *AggLayerDVNFilterer) WatchFlatFeeSet(opts *bind.WatchOpts, sink chan<- *AggLayerDVNFlatFeeSet) (event.Subscription, error) {

	logs, sub, err := _AggLayerDVN.contract.WatchLogs(opts, "FlatFeeSet")
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(AggLayerDVNFlatFeeSet)
				if err := _AggLayerDVN.contract.UnpackLog(event, "FlatFeeSet", log); err != nil {
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

// ParseFlatFeeSet is a log parse operation binding the contract event 0x02e6220a8c55d7fd57319e91a44a9aac5a5fd4c64a19bb34bb83c8cedbf3f398.
//
// Solidity: event FlatFeeSet(uint256 fee)
func (_AggLayerDVN *AggLayerDVNFilterer) ParseFlatFeeSet(log types.Log) (*AggLayerDVNFlatFeeSet, error) {
	event := new(AggLayerDVNFlatFeeSet)
	if err := _AggLayerDVN.contract.UnpackLog(event, "FlatFeeSet", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// AggLayerDVNJobAssignedIterator is returned from FilterJobAssigned and is used to iterate over the raw logs and unpacked data for JobAssigned events raised by the AggLayerDVN contract.
type AggLayerDVNJobAssignedIterator struct {
	Event *AggLayerDVNJobAssigned // Event containing the contract specifics and raw log

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
func (it *AggLayerDVNJobAssignedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(AggLayerDVNJobAssigned)
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
		it.Event = new(AggLayerDVNJobAssigned)
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
func (it *AggLayerDVNJobAssignedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *AggLayerDVNJobAssignedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// AggLayerDVNJobAssigned represents a JobAssigned event raised by the AggLayerDVN contract.
type AggLayerDVNJobAssigned struct {
	PacketHeader []byte
	PayloadHash  [32]byte
	DstEid       uint32
	Sender       common.Address
	Fee          *big.Int
	Raw          types.Log // Blockchain specific contextual infos
}

// FilterJobAssigned is a free log retrieval operation binding the contract event 0x7e750041b290fa337f05c8e26201b910be8a271b97a9a97e27af9d89492519a8.
//
// Solidity: event JobAssigned(bytes packetHeader, bytes32 payloadHash, uint32 dstEid, address sender, uint256 fee)
func (_AggLayerDVN *AggLayerDVNFilterer) FilterJobAssigned(opts *bind.FilterOpts) (*AggLayerDVNJobAssignedIterator, error) {

	logs, sub, err := _AggLayerDVN.contract.FilterLogs(opts, "JobAssigned")
	if err != nil {
		return nil, err
	}
	return &AggLayerDVNJobAssignedIterator{contract: _AggLayerDVN.contract, event: "JobAssigned", logs: logs, sub: sub}, nil
}

// WatchJobAssigned is a free log subscription operation binding the contract event 0x7e750041b290fa337f05c8e26201b910be8a271b97a9a97e27af9d89492519a8.
//
// Solidity: event JobAssigned(bytes packetHeader, bytes32 payloadHash, uint32 dstEid, address sender, uint256 fee)
func (_AggLayerDVN *AggLayerDVNFilterer) WatchJobAssigned(opts *bind.WatchOpts, sink chan<- *AggLayerDVNJobAssigned) (event.Subscription, error) {

	logs, sub, err := _AggLayerDVN.contract.WatchLogs(opts, "JobAssigned")
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(AggLayerDVNJobAssigned)
				if err := _AggLayerDVN.contract.UnpackLog(event, "JobAssigned", log); err != nil {
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

// ParseJobAssigned is a log parse operation binding the contract event 0x7e750041b290fa337f05c8e26201b910be8a271b97a9a97e27af9d89492519a8.
//
// Solidity: event JobAssigned(bytes packetHeader, bytes32 payloadHash, uint32 dstEid, address sender, uint256 fee)
func (_AggLayerDVN *AggLayerDVNFilterer) ParseJobAssigned(log types.Log) (*AggLayerDVNJobAssigned, error) {
	event := new(AggLayerDVNJobAssigned)
	if err := _AggLayerDVN.contract.UnpackLog(event, "JobAssigned", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// AggLayerDVNOwnershipTransferredIterator is returned from FilterOwnershipTransferred and is used to iterate over the raw logs and unpacked data for OwnershipTransferred events raised by the AggLayerDVN contract.
type AggLayerDVNOwnershipTransferredIterator struct {
	Event *AggLayerDVNOwnershipTransferred // Event containing the contract specifics and raw log

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
func (it *AggLayerDVNOwnershipTransferredIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(AggLayerDVNOwnershipTransferred)
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
		it.Event = new(AggLayerDVNOwnershipTransferred)
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
func (it *AggLayerDVNOwnershipTransferredIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *AggLayerDVNOwnershipTransferredIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// AggLayerDVNOwnershipTransferred represents a OwnershipTransferred event raised by the AggLayerDVN contract.
type AggLayerDVNOwnershipTransferred struct {
	PreviousOwner common.Address
	NewOwner      common.Address
	Raw           types.Log // Blockchain specific contextual infos
}

// FilterOwnershipTransferred is a free log retrieval operation binding the contract event 0x8be0079c531659141344cd1fd0a4f28419497f9722a3daafe3b4186f6b6457e0.
//
// Solidity: event OwnershipTransferred(address indexed previousOwner, address indexed newOwner)
func (_AggLayerDVN *AggLayerDVNFilterer) FilterOwnershipTransferred(opts *bind.FilterOpts, previousOwner []common.Address, newOwner []common.Address) (*AggLayerDVNOwnershipTransferredIterator, error) {

	var previousOwnerRule []interface{}
	for _, previousOwnerItem := range previousOwner {
		previousOwnerRule = append(previousOwnerRule, previousOwnerItem)
	}
	var newOwnerRule []interface{}
	for _, newOwnerItem := range newOwner {
		newOwnerRule = append(newOwnerRule, newOwnerItem)
	}

	logs, sub, err := _AggLayerDVN.contract.FilterLogs(opts, "OwnershipTransferred", previousOwnerRule, newOwnerRule)
	if err != nil {
		return nil, err
	}
	return &AggLayerDVNOwnershipTransferredIterator{contract: _AggLayerDVN.contract, event: "OwnershipTransferred", logs: logs, sub: sub}, nil
}

// WatchOwnershipTransferred is a free log subscription operation binding the contract event 0x8be0079c531659141344cd1fd0a4f28419497f9722a3daafe3b4186f6b6457e0.
//
// Solidity: event OwnershipTransferred(address indexed previousOwner, address indexed newOwner)
func (_AggLayerDVN *AggLayerDVNFilterer) WatchOwnershipTransferred(opts *bind.WatchOpts, sink chan<- *AggLayerDVNOwnershipTransferred, previousOwner []common.Address, newOwner []common.Address) (event.Subscription, error) {

	var previousOwnerRule []interface{}
	for _, previousOwnerItem := range previousOwner {
		previousOwnerRule = append(previousOwnerRule, previousOwnerItem)
	}
	var newOwnerRule []interface{}
	for _, newOwnerItem := range newOwner {
		newOwnerRule = append(newOwnerRule, newOwnerItem)
	}

	logs, sub, err := _AggLayerDVN.contract.WatchLogs(opts, "OwnershipTransferred", previousOwnerRule, newOwnerRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(AggLayerDVNOwnershipTransferred)
				if err := _AggLayerDVN.contract.UnpackLog(event, "OwnershipTransferred", log); err != nil {
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

// ParseOwnershipTransferred is a log parse operation binding the contract event 0x8be0079c531659141344cd1fd0a4f28419497f9722a3daafe3b4186f6b6457e0.
//
// Solidity: event OwnershipTransferred(address indexed previousOwner, address indexed newOwner)
func (_AggLayerDVN *AggLayerDVNFilterer) ParseOwnershipTransferred(log types.Log) (*AggLayerDVNOwnershipTransferred, error) {
	event := new(AggLayerDVNOwnershipTransferred)
	if err := _AggLayerDVN.contract.UnpackLog(event, "OwnershipTransferred", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// AggLayerDVNSenderAddedIterator is returned from FilterSenderAdded and is used to iterate over the raw logs and unpacked data for SenderAdded events raised by the AggLayerDVN contract.
type AggLayerDVNSenderAddedIterator struct {
	Event *AggLayerDVNSenderAdded // Event containing the contract specifics and raw log

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
func (it *AggLayerDVNSenderAddedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(AggLayerDVNSenderAdded)
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
		it.Event = new(AggLayerDVNSenderAdded)
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
func (it *AggLayerDVNSenderAddedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *AggLayerDVNSenderAddedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// AggLayerDVNSenderAdded represents a SenderAdded event raised by the AggLayerDVN contract.
type AggLayerDVNSenderAdded struct {
	Sender common.Address
	Raw    types.Log // Blockchain specific contextual infos
}

// FilterSenderAdded is a free log retrieval operation binding the contract event 0xd65eca5d561d3a4568c87b9b13ced4ab52a69edadfdfdb22d76bc595f36d7d87.
//
// Solidity: event SenderAdded(address sender)
func (_AggLayerDVN *AggLayerDVNFilterer) FilterSenderAdded(opts *bind.FilterOpts) (*AggLayerDVNSenderAddedIterator, error) {

	logs, sub, err := _AggLayerDVN.contract.FilterLogs(opts, "SenderAdded")
	if err != nil {
		return nil, err
	}
	return &AggLayerDVNSenderAddedIterator{contract: _AggLayerDVN.contract, event: "SenderAdded", logs: logs, sub: sub}, nil
}

// WatchSenderAdded is a free log subscription operation binding the contract event 0xd65eca5d561d3a4568c87b9b13ced4ab52a69edadfdfdb22d76bc595f36d7d87.
//
// Solidity: event SenderAdded(address sender)
func (_AggLayerDVN *AggLayerDVNFilterer) WatchSenderAdded(opts *bind.WatchOpts, sink chan<- *AggLayerDVNSenderAdded) (event.Subscription, error) {

	logs, sub, err := _AggLayerDVN.contract.WatchLogs(opts, "SenderAdded")
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(AggLayerDVNSenderAdded)
				if err := _AggLayerDVN.contract.UnpackLog(event, "SenderAdded", log); err != nil {
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

// ParseSenderAdded is a log parse operation binding the contract event 0xd65eca5d561d3a4568c87b9b13ced4ab52a69edadfdfdb22d76bc595f36d7d87.
//
// Solidity: event SenderAdded(address sender)
func (_AggLayerDVN *AggLayerDVNFilterer) ParseSenderAdded(log types.Log) (*AggLayerDVNSenderAdded, error) {
	event := new(AggLayerDVNSenderAdded)
	if err := _AggLayerDVN.contract.UnpackLog(event, "SenderAdded", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// AggLayerDVNSenderRemovedIterator is returned from FilterSenderRemoved and is used to iterate over the raw logs and unpacked data for SenderRemoved events raised by the AggLayerDVN contract.
type AggLayerDVNSenderRemovedIterator struct {
	Event *AggLayerDVNSenderRemoved // Event containing the contract specifics and raw log

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
func (it *AggLayerDVNSenderRemovedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(AggLayerDVNSenderRemoved)
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
		it.Event = new(AggLayerDVNSenderRemoved)
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
func (it *AggLayerDVNSenderRemovedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *AggLayerDVNSenderRemovedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// AggLayerDVNSenderRemoved represents a SenderRemoved event raised by the AggLayerDVN contract.
type AggLayerDVNSenderRemoved struct {
	Sender common.Address
	Raw    types.Log // Blockchain specific contextual infos
}

// FilterSenderRemoved is a free log retrieval operation binding the contract event 0x384859c5ef5fafac31e8bc92ce7fb48b1f2c74c4dd5e212eb84ec202fa5d9fad.
//
// Solidity: event SenderRemoved(address sender)
func (_AggLayerDVN *AggLayerDVNFilterer) FilterSenderRemoved(opts *bind.FilterOpts) (*AggLayerDVNSenderRemovedIterator, error) {

	logs, sub, err := _AggLayerDVN.contract.FilterLogs(opts, "SenderRemoved")
	if err != nil {
		return nil, err
	}
	return &AggLayerDVNSenderRemovedIterator{contract: _AggLayerDVN.contract, event: "SenderRemoved", logs: logs, sub: sub}, nil
}

// WatchSenderRemoved is a free log subscription operation binding the contract event 0x384859c5ef5fafac31e8bc92ce7fb48b1f2c74c4dd5e212eb84ec202fa5d9fad.
//
// Solidity: event SenderRemoved(address sender)
func (_AggLayerDVN *AggLayerDVNFilterer) WatchSenderRemoved(opts *bind.WatchOpts, sink chan<- *AggLayerDVNSenderRemoved) (event.Subscription, error) {

	logs, sub, err := _AggLayerDVN.contract.WatchLogs(opts, "SenderRemoved")
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(AggLayerDVNSenderRemoved)
				if err := _AggLayerDVN.contract.UnpackLog(event, "SenderRemoved", log); err != nil {
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

// ParseSenderRemoved is a log parse operation binding the contract event 0x384859c5ef5fafac31e8bc92ce7fb48b1f2c74c4dd5e212eb84ec202fa5d9fad.
//
// Solidity: event SenderRemoved(address sender)
func (_AggLayerDVN *AggLayerDVNFilterer) ParseSenderRemoved(log types.Log) (*AggLayerDVNSenderRemoved, error) {
	event := new(AggLayerDVNSenderRemoved)
	if err := _AggLayerDVN.contract.UnpackLog(event, "SenderRemoved", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}
