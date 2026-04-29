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

// AggLayerClaim is an auto generated low-level Go binding around an user-defined struct.
type AggLayerClaim struct {
	SmtProofLocalExitRoot  [32][32]byte
	SmtProofRollupExitRoot [32][32]byte
	GlobalIndex            *big.Int
	MainnetExitRoot        [32]byte
	RollupExitRoot         [32]byte
	OriginNetwork          uint32
	OriginTokenAddress     common.Address
	DestinationNetwork     uint32
	DestinationAddress     common.Address
	Amount                 *big.Int
	Metadata               []byte
}

// Origin is an auto generated low-level Go binding around an user-defined struct.
type Origin struct {
	SrcEid uint32
	Sender [32]byte
	Nonce  uint64
}

// AggLayerDVNCoordinatorMetaData contains all meta data concerning the AggLayerDVNCoordinator contract.
var AggLayerDVNCoordinatorMetaData = &bind.MetaData{
	ABI: "[{\"inputs\":[{\"internalType\":\"address\",\"name\":\"initialOwner\",\"type\":\"address\"},{\"internalType\":\"address\",\"name\":\"receiveLib_\",\"type\":\"address\"},{\"internalType\":\"address\",\"name\":\"aggLayerOFTReceiver_\",\"type\":\"address\"}],\"stateMutability\":\"nonpayable\",\"type\":\"constructor\"},{\"inputs\":[{\"internalType\":\"bytes32\",\"name\":\"releaseKey\",\"type\":\"bytes32\"}],\"name\":\"AlreadyProcessed\",\"type\":\"error\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"owner\",\"type\":\"address\"}],\"name\":\"OwnableInvalidOwner\",\"type\":\"error\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"account\",\"type\":\"address\"}],\"name\":\"OwnableUnauthorizedAccount\",\"type\":\"error\"},{\"inputs\":[{\"internalType\":\"string\",\"name\":\"reason\",\"type\":\"string\"}],\"name\":\"PacketHeaderInvalid\",\"type\":\"error\"},{\"inputs\":[{\"internalType\":\"bytes32\",\"name\":\"computed\",\"type\":\"bytes32\"},{\"internalType\":\"bytes32\",\"name\":\"provided\",\"type\":\"bytes32\"}],\"name\":\"PayloadHashMismatch\",\"type\":\"error\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"worker\",\"type\":\"address\"}],\"name\":\"UnauthorizedWorker\",\"type\":\"error\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":true,\"internalType\":\"bytes32\",\"name\":\"releaseKey\",\"type\":\"bytes32\"},{\"indexed\":true,\"internalType\":\"bytes32\",\"name\":\"guid\",\"type\":\"bytes32\"},{\"indexed\":false,\"internalType\":\"bytes32\",\"name\":\"payloadHash\",\"type\":\"bytes32\"}],\"name\":\"ClaimedAndVerified\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":true,\"internalType\":\"address\",\"name\":\"previousOwner\",\"type\":\"address\"},{\"indexed\":true,\"internalType\":\"address\",\"name\":\"newOwner\",\"type\":\"address\"}],\"name\":\"OwnershipTransferred\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":true,\"internalType\":\"address\",\"name\":\"worker\",\"type\":\"address\"}],\"name\":\"WorkerAdded\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":true,\"internalType\":\"address\",\"name\":\"worker\",\"type\":\"address\"}],\"name\":\"WorkerRemoved\",\"type\":\"event\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"worker\",\"type\":\"address\"}],\"name\":\"addWorker\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[],\"name\":\"aggLayerOFTReceiver\",\"outputs\":[{\"internalType\":\"address\",\"name\":\"\",\"type\":\"address\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"\",\"type\":\"address\"}],\"name\":\"allowedWorkers\",\"outputs\":[{\"internalType\":\"bool\",\"name\":\"\",\"type\":\"bool\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[{\"components\":[{\"internalType\":\"uint32\",\"name\":\"srcEid\",\"type\":\"uint32\"},{\"internalType\":\"bytes32\",\"name\":\"sender\",\"type\":\"bytes32\"},{\"internalType\":\"uint64\",\"name\":\"nonce\",\"type\":\"uint64\"}],\"internalType\":\"structOrigin\",\"name\":\"origin\",\"type\":\"tuple\"},{\"internalType\":\"bytes32\",\"name\":\"guid\",\"type\":\"bytes32\"},{\"internalType\":\"bytes\",\"name\":\"message\",\"type\":\"bytes\"},{\"components\":[{\"internalType\":\"bytes32[32]\",\"name\":\"smtProofLocalExitRoot\",\"type\":\"bytes32[32]\"},{\"internalType\":\"bytes32[32]\",\"name\":\"smtProofRollupExitRoot\",\"type\":\"bytes32[32]\"},{\"internalType\":\"uint256\",\"name\":\"globalIndex\",\"type\":\"uint256\"},{\"internalType\":\"bytes32\",\"name\":\"mainnetExitRoot\",\"type\":\"bytes32\"},{\"internalType\":\"bytes32\",\"name\":\"rollupExitRoot\",\"type\":\"bytes32\"},{\"internalType\":\"uint32\",\"name\":\"originNetwork\",\"type\":\"uint32\"},{\"internalType\":\"address\",\"name\":\"originTokenAddress\",\"type\":\"address\"},{\"internalType\":\"uint32\",\"name\":\"destinationNetwork\",\"type\":\"uint32\"},{\"internalType\":\"address\",\"name\":\"destinationAddress\",\"type\":\"address\"},{\"internalType\":\"uint256\",\"name\":\"amount\",\"type\":\"uint256\"},{\"internalType\":\"bytes\",\"name\":\"metadata\",\"type\":\"bytes\"}],\"internalType\":\"structAggLayerClaim\",\"name\":\"claim\",\"type\":\"tuple\"},{\"internalType\":\"bytes\",\"name\":\"packetHeader\",\"type\":\"bytes\"},{\"internalType\":\"bytes32\",\"name\":\"payloadHash\",\"type\":\"bytes32\"},{\"internalType\":\"uint64\",\"name\":\"confirmations\",\"type\":\"uint64\"}],\"name\":\"claimAndVerify\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[],\"name\":\"owner\",\"outputs\":[{\"internalType\":\"address\",\"name\":\"\",\"type\":\"address\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[],\"name\":\"receiveLib\",\"outputs\":[{\"internalType\":\"address\",\"name\":\"\",\"type\":\"address\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"worker\",\"type\":\"address\"}],\"name\":\"removeWorker\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[],\"name\":\"renounceOwnership\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"newOwner\",\"type\":\"address\"}],\"name\":\"transferOwnership\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"}]",
}

// AggLayerDVNCoordinatorABI is the input ABI used to generate the binding from.
// Deprecated: Use AggLayerDVNCoordinatorMetaData.ABI instead.
var AggLayerDVNCoordinatorABI = AggLayerDVNCoordinatorMetaData.ABI

// AggLayerDVNCoordinator is an auto generated Go binding around an Ethereum contract.
type AggLayerDVNCoordinator struct {
	AggLayerDVNCoordinatorCaller     // Read-only binding to the contract
	AggLayerDVNCoordinatorTransactor // Write-only binding to the contract
	AggLayerDVNCoordinatorFilterer   // Log filterer for contract events
}

// AggLayerDVNCoordinatorCaller is an auto generated read-only Go binding around an Ethereum contract.
type AggLayerDVNCoordinatorCaller struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// AggLayerDVNCoordinatorTransactor is an auto generated write-only Go binding around an Ethereum contract.
type AggLayerDVNCoordinatorTransactor struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// AggLayerDVNCoordinatorFilterer is an auto generated log filtering Go binding around an Ethereum contract events.
type AggLayerDVNCoordinatorFilterer struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// AggLayerDVNCoordinatorSession is an auto generated Go binding around an Ethereum contract,
// with pre-set call and transact options.
type AggLayerDVNCoordinatorSession struct {
	Contract     *AggLayerDVNCoordinator // Generic contract binding to set the session for
	CallOpts     bind.CallOpts           // Call options to use throughout this session
	TransactOpts bind.TransactOpts       // Transaction auth options to use throughout this session
}

// AggLayerDVNCoordinatorCallerSession is an auto generated read-only Go binding around an Ethereum contract,
// with pre-set call options.
type AggLayerDVNCoordinatorCallerSession struct {
	Contract *AggLayerDVNCoordinatorCaller // Generic contract caller binding to set the session for
	CallOpts bind.CallOpts                 // Call options to use throughout this session
}

// AggLayerDVNCoordinatorTransactorSession is an auto generated write-only Go binding around an Ethereum contract,
// with pre-set transact options.
type AggLayerDVNCoordinatorTransactorSession struct {
	Contract     *AggLayerDVNCoordinatorTransactor // Generic contract transactor binding to set the session for
	TransactOpts bind.TransactOpts                 // Transaction auth options to use throughout this session
}

// AggLayerDVNCoordinatorRaw is an auto generated low-level Go binding around an Ethereum contract.
type AggLayerDVNCoordinatorRaw struct {
	Contract *AggLayerDVNCoordinator // Generic contract binding to access the raw methods on
}

// AggLayerDVNCoordinatorCallerRaw is an auto generated low-level read-only Go binding around an Ethereum contract.
type AggLayerDVNCoordinatorCallerRaw struct {
	Contract *AggLayerDVNCoordinatorCaller // Generic read-only contract binding to access the raw methods on
}

// AggLayerDVNCoordinatorTransactorRaw is an auto generated low-level write-only Go binding around an Ethereum contract.
type AggLayerDVNCoordinatorTransactorRaw struct {
	Contract *AggLayerDVNCoordinatorTransactor // Generic write-only contract binding to access the raw methods on
}

// NewAggLayerDVNCoordinator creates a new instance of AggLayerDVNCoordinator, bound to a specific deployed contract.
func NewAggLayerDVNCoordinator(address common.Address, backend bind.ContractBackend) (*AggLayerDVNCoordinator, error) {
	contract, err := bindAggLayerDVNCoordinator(address, backend, backend, backend)
	if err != nil {
		return nil, err
	}
	return &AggLayerDVNCoordinator{AggLayerDVNCoordinatorCaller: AggLayerDVNCoordinatorCaller{contract: contract}, AggLayerDVNCoordinatorTransactor: AggLayerDVNCoordinatorTransactor{contract: contract}, AggLayerDVNCoordinatorFilterer: AggLayerDVNCoordinatorFilterer{contract: contract}}, nil
}

// NewAggLayerDVNCoordinatorCaller creates a new read-only instance of AggLayerDVNCoordinator, bound to a specific deployed contract.
func NewAggLayerDVNCoordinatorCaller(address common.Address, caller bind.ContractCaller) (*AggLayerDVNCoordinatorCaller, error) {
	contract, err := bindAggLayerDVNCoordinator(address, caller, nil, nil)
	if err != nil {
		return nil, err
	}
	return &AggLayerDVNCoordinatorCaller{contract: contract}, nil
}

// NewAggLayerDVNCoordinatorTransactor creates a new write-only instance of AggLayerDVNCoordinator, bound to a specific deployed contract.
func NewAggLayerDVNCoordinatorTransactor(address common.Address, transactor bind.ContractTransactor) (*AggLayerDVNCoordinatorTransactor, error) {
	contract, err := bindAggLayerDVNCoordinator(address, nil, transactor, nil)
	if err != nil {
		return nil, err
	}
	return &AggLayerDVNCoordinatorTransactor{contract: contract}, nil
}

// NewAggLayerDVNCoordinatorFilterer creates a new log filterer instance of AggLayerDVNCoordinator, bound to a specific deployed contract.
func NewAggLayerDVNCoordinatorFilterer(address common.Address, filterer bind.ContractFilterer) (*AggLayerDVNCoordinatorFilterer, error) {
	contract, err := bindAggLayerDVNCoordinator(address, nil, nil, filterer)
	if err != nil {
		return nil, err
	}
	return &AggLayerDVNCoordinatorFilterer{contract: contract}, nil
}

// bindAggLayerDVNCoordinator binds a generic wrapper to an already deployed contract.
func bindAggLayerDVNCoordinator(address common.Address, caller bind.ContractCaller, transactor bind.ContractTransactor, filterer bind.ContractFilterer) (*bind.BoundContract, error) {
	parsed, err := AggLayerDVNCoordinatorMetaData.GetAbi()
	if err != nil {
		return nil, err
	}
	return bind.NewBoundContract(address, *parsed, caller, transactor, filterer), nil
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorRaw) Call(opts *bind.CallOpts, result *[]interface{}, method string, params ...interface{}) error {
	return _AggLayerDVNCoordinator.Contract.AggLayerDVNCoordinatorCaller.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _AggLayerDVNCoordinator.Contract.AggLayerDVNCoordinatorTransactor.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorRaw) Transact(opts *bind.TransactOpts, method string, params ...interface{}) (*types.Transaction, error) {
	return _AggLayerDVNCoordinator.Contract.AggLayerDVNCoordinatorTransactor.contract.Transact(opts, method, params...)
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorCallerRaw) Call(opts *bind.CallOpts, result *[]interface{}, method string, params ...interface{}) error {
	return _AggLayerDVNCoordinator.Contract.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorTransactorRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _AggLayerDVNCoordinator.Contract.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorTransactorRaw) Transact(opts *bind.TransactOpts, method string, params ...interface{}) (*types.Transaction, error) {
	return _AggLayerDVNCoordinator.Contract.contract.Transact(opts, method, params...)
}

// AggLayerOFTReceiver is a free data retrieval call binding the contract method 0x11c08fe0.
//
// Solidity: function aggLayerOFTReceiver() view returns(address)
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorCaller) AggLayerOFTReceiver(opts *bind.CallOpts) (common.Address, error) {
	var out []interface{}
	err := _AggLayerDVNCoordinator.contract.Call(opts, &out, "aggLayerOFTReceiver")

	if err != nil {
		return *new(common.Address), err
	}

	out0 := *abi.ConvertType(out[0], new(common.Address)).(*common.Address)

	return out0, err

}

// AggLayerOFTReceiver is a free data retrieval call binding the contract method 0x11c08fe0.
//
// Solidity: function aggLayerOFTReceiver() view returns(address)
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorSession) AggLayerOFTReceiver() (common.Address, error) {
	return _AggLayerDVNCoordinator.Contract.AggLayerOFTReceiver(&_AggLayerDVNCoordinator.CallOpts)
}

// AggLayerOFTReceiver is a free data retrieval call binding the contract method 0x11c08fe0.
//
// Solidity: function aggLayerOFTReceiver() view returns(address)
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorCallerSession) AggLayerOFTReceiver() (common.Address, error) {
	return _AggLayerDVNCoordinator.Contract.AggLayerOFTReceiver(&_AggLayerDVNCoordinator.CallOpts)
}

// AllowedWorkers is a free data retrieval call binding the contract method 0x0245f686.
//
// Solidity: function allowedWorkers(address ) view returns(bool)
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorCaller) AllowedWorkers(opts *bind.CallOpts, arg0 common.Address) (bool, error) {
	var out []interface{}
	err := _AggLayerDVNCoordinator.contract.Call(opts, &out, "allowedWorkers", arg0)

	if err != nil {
		return *new(bool), err
	}

	out0 := *abi.ConvertType(out[0], new(bool)).(*bool)

	return out0, err

}

// AllowedWorkers is a free data retrieval call binding the contract method 0x0245f686.
//
// Solidity: function allowedWorkers(address ) view returns(bool)
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorSession) AllowedWorkers(arg0 common.Address) (bool, error) {
	return _AggLayerDVNCoordinator.Contract.AllowedWorkers(&_AggLayerDVNCoordinator.CallOpts, arg0)
}

// AllowedWorkers is a free data retrieval call binding the contract method 0x0245f686.
//
// Solidity: function allowedWorkers(address ) view returns(bool)
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorCallerSession) AllowedWorkers(arg0 common.Address) (bool, error) {
	return _AggLayerDVNCoordinator.Contract.AllowedWorkers(&_AggLayerDVNCoordinator.CallOpts, arg0)
}

// Owner is a free data retrieval call binding the contract method 0x8da5cb5b.
//
// Solidity: function owner() view returns(address)
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorCaller) Owner(opts *bind.CallOpts) (common.Address, error) {
	var out []interface{}
	err := _AggLayerDVNCoordinator.contract.Call(opts, &out, "owner")

	if err != nil {
		return *new(common.Address), err
	}

	out0 := *abi.ConvertType(out[0], new(common.Address)).(*common.Address)

	return out0, err

}

// Owner is a free data retrieval call binding the contract method 0x8da5cb5b.
//
// Solidity: function owner() view returns(address)
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorSession) Owner() (common.Address, error) {
	return _AggLayerDVNCoordinator.Contract.Owner(&_AggLayerDVNCoordinator.CallOpts)
}

// Owner is a free data retrieval call binding the contract method 0x8da5cb5b.
//
// Solidity: function owner() view returns(address)
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorCallerSession) Owner() (common.Address, error) {
	return _AggLayerDVNCoordinator.Contract.Owner(&_AggLayerDVNCoordinator.CallOpts)
}

// ReceiveLib is a free data retrieval call binding the contract method 0xbd3ed5ff.
//
// Solidity: function receiveLib() view returns(address)
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorCaller) ReceiveLib(opts *bind.CallOpts) (common.Address, error) {
	var out []interface{}
	err := _AggLayerDVNCoordinator.contract.Call(opts, &out, "receiveLib")

	if err != nil {
		return *new(common.Address), err
	}

	out0 := *abi.ConvertType(out[0], new(common.Address)).(*common.Address)

	return out0, err

}

// ReceiveLib is a free data retrieval call binding the contract method 0xbd3ed5ff.
//
// Solidity: function receiveLib() view returns(address)
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorSession) ReceiveLib() (common.Address, error) {
	return _AggLayerDVNCoordinator.Contract.ReceiveLib(&_AggLayerDVNCoordinator.CallOpts)
}

// ReceiveLib is a free data retrieval call binding the contract method 0xbd3ed5ff.
//
// Solidity: function receiveLib() view returns(address)
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorCallerSession) ReceiveLib() (common.Address, error) {
	return _AggLayerDVNCoordinator.Contract.ReceiveLib(&_AggLayerDVNCoordinator.CallOpts)
}

// AddWorker is a paid mutator transaction binding the contract method 0x806ad57e.
//
// Solidity: function addWorker(address worker) returns()
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorTransactor) AddWorker(opts *bind.TransactOpts, worker common.Address) (*types.Transaction, error) {
	return _AggLayerDVNCoordinator.contract.Transact(opts, "addWorker", worker)
}

// AddWorker is a paid mutator transaction binding the contract method 0x806ad57e.
//
// Solidity: function addWorker(address worker) returns()
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorSession) AddWorker(worker common.Address) (*types.Transaction, error) {
	return _AggLayerDVNCoordinator.Contract.AddWorker(&_AggLayerDVNCoordinator.TransactOpts, worker)
}

// AddWorker is a paid mutator transaction binding the contract method 0x806ad57e.
//
// Solidity: function addWorker(address worker) returns()
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorTransactorSession) AddWorker(worker common.Address) (*types.Transaction, error) {
	return _AggLayerDVNCoordinator.Contract.AddWorker(&_AggLayerDVNCoordinator.TransactOpts, worker)
}

// ClaimAndVerify is a paid mutator transaction binding the contract method 0x57832fdb.
//
// Solidity: function claimAndVerify((uint32,bytes32,uint64) origin, bytes32 guid, bytes message, (bytes32[32],bytes32[32],uint256,bytes32,bytes32,uint32,address,uint32,address,uint256,bytes) claim, bytes packetHeader, bytes32 payloadHash, uint64 confirmations) returns()
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorTransactor) ClaimAndVerify(opts *bind.TransactOpts, origin Origin, guid [32]byte, message []byte, claim AggLayerClaim, packetHeader []byte, payloadHash [32]byte, confirmations uint64) (*types.Transaction, error) {
	return _AggLayerDVNCoordinator.contract.Transact(opts, "claimAndVerify", origin, guid, message, claim, packetHeader, payloadHash, confirmations)
}

// ClaimAndVerify is a paid mutator transaction binding the contract method 0x57832fdb.
//
// Solidity: function claimAndVerify((uint32,bytes32,uint64) origin, bytes32 guid, bytes message, (bytes32[32],bytes32[32],uint256,bytes32,bytes32,uint32,address,uint32,address,uint256,bytes) claim, bytes packetHeader, bytes32 payloadHash, uint64 confirmations) returns()
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorSession) ClaimAndVerify(origin Origin, guid [32]byte, message []byte, claim AggLayerClaim, packetHeader []byte, payloadHash [32]byte, confirmations uint64) (*types.Transaction, error) {
	return _AggLayerDVNCoordinator.Contract.ClaimAndVerify(&_AggLayerDVNCoordinator.TransactOpts, origin, guid, message, claim, packetHeader, payloadHash, confirmations)
}

// ClaimAndVerify is a paid mutator transaction binding the contract method 0x57832fdb.
//
// Solidity: function claimAndVerify((uint32,bytes32,uint64) origin, bytes32 guid, bytes message, (bytes32[32],bytes32[32],uint256,bytes32,bytes32,uint32,address,uint32,address,uint256,bytes) claim, bytes packetHeader, bytes32 payloadHash, uint64 confirmations) returns()
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorTransactorSession) ClaimAndVerify(origin Origin, guid [32]byte, message []byte, claim AggLayerClaim, packetHeader []byte, payloadHash [32]byte, confirmations uint64) (*types.Transaction, error) {
	return _AggLayerDVNCoordinator.Contract.ClaimAndVerify(&_AggLayerDVNCoordinator.TransactOpts, origin, guid, message, claim, packetHeader, payloadHash, confirmations)
}

// RemoveWorker is a paid mutator transaction binding the contract method 0xc4f987a5.
//
// Solidity: function removeWorker(address worker) returns()
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorTransactor) RemoveWorker(opts *bind.TransactOpts, worker common.Address) (*types.Transaction, error) {
	return _AggLayerDVNCoordinator.contract.Transact(opts, "removeWorker", worker)
}

// RemoveWorker is a paid mutator transaction binding the contract method 0xc4f987a5.
//
// Solidity: function removeWorker(address worker) returns()
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorSession) RemoveWorker(worker common.Address) (*types.Transaction, error) {
	return _AggLayerDVNCoordinator.Contract.RemoveWorker(&_AggLayerDVNCoordinator.TransactOpts, worker)
}

// RemoveWorker is a paid mutator transaction binding the contract method 0xc4f987a5.
//
// Solidity: function removeWorker(address worker) returns()
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorTransactorSession) RemoveWorker(worker common.Address) (*types.Transaction, error) {
	return _AggLayerDVNCoordinator.Contract.RemoveWorker(&_AggLayerDVNCoordinator.TransactOpts, worker)
}

// RenounceOwnership is a paid mutator transaction binding the contract method 0x715018a6.
//
// Solidity: function renounceOwnership() returns()
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorTransactor) RenounceOwnership(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _AggLayerDVNCoordinator.contract.Transact(opts, "renounceOwnership")
}

// RenounceOwnership is a paid mutator transaction binding the contract method 0x715018a6.
//
// Solidity: function renounceOwnership() returns()
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorSession) RenounceOwnership() (*types.Transaction, error) {
	return _AggLayerDVNCoordinator.Contract.RenounceOwnership(&_AggLayerDVNCoordinator.TransactOpts)
}

// RenounceOwnership is a paid mutator transaction binding the contract method 0x715018a6.
//
// Solidity: function renounceOwnership() returns()
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorTransactorSession) RenounceOwnership() (*types.Transaction, error) {
	return _AggLayerDVNCoordinator.Contract.RenounceOwnership(&_AggLayerDVNCoordinator.TransactOpts)
}

// TransferOwnership is a paid mutator transaction binding the contract method 0xf2fde38b.
//
// Solidity: function transferOwnership(address newOwner) returns()
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorTransactor) TransferOwnership(opts *bind.TransactOpts, newOwner common.Address) (*types.Transaction, error) {
	return _AggLayerDVNCoordinator.contract.Transact(opts, "transferOwnership", newOwner)
}

// TransferOwnership is a paid mutator transaction binding the contract method 0xf2fde38b.
//
// Solidity: function transferOwnership(address newOwner) returns()
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorSession) TransferOwnership(newOwner common.Address) (*types.Transaction, error) {
	return _AggLayerDVNCoordinator.Contract.TransferOwnership(&_AggLayerDVNCoordinator.TransactOpts, newOwner)
}

// TransferOwnership is a paid mutator transaction binding the contract method 0xf2fde38b.
//
// Solidity: function transferOwnership(address newOwner) returns()
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorTransactorSession) TransferOwnership(newOwner common.Address) (*types.Transaction, error) {
	return _AggLayerDVNCoordinator.Contract.TransferOwnership(&_AggLayerDVNCoordinator.TransactOpts, newOwner)
}

// AggLayerDVNCoordinatorClaimedAndVerifiedIterator is returned from FilterClaimedAndVerified and is used to iterate over the raw logs and unpacked data for ClaimedAndVerified events raised by the AggLayerDVNCoordinator contract.
type AggLayerDVNCoordinatorClaimedAndVerifiedIterator struct {
	Event *AggLayerDVNCoordinatorClaimedAndVerified // Event containing the contract specifics and raw log

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
func (it *AggLayerDVNCoordinatorClaimedAndVerifiedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(AggLayerDVNCoordinatorClaimedAndVerified)
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
		it.Event = new(AggLayerDVNCoordinatorClaimedAndVerified)
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
func (it *AggLayerDVNCoordinatorClaimedAndVerifiedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *AggLayerDVNCoordinatorClaimedAndVerifiedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// AggLayerDVNCoordinatorClaimedAndVerified represents a ClaimedAndVerified event raised by the AggLayerDVNCoordinator contract.
type AggLayerDVNCoordinatorClaimedAndVerified struct {
	ReleaseKey  [32]byte
	Guid        [32]byte
	PayloadHash [32]byte
	Raw         types.Log // Blockchain specific contextual infos
}

// FilterClaimedAndVerified is a free log retrieval operation binding the contract event 0x9cb2b3ae6c86a8fd90323108b48ae6a80f730d2850a39826969467576f7f2f5b.
//
// Solidity: event ClaimedAndVerified(bytes32 indexed releaseKey, bytes32 indexed guid, bytes32 payloadHash)
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorFilterer) FilterClaimedAndVerified(opts *bind.FilterOpts, releaseKey [][32]byte, guid [][32]byte) (*AggLayerDVNCoordinatorClaimedAndVerifiedIterator, error) {

	var releaseKeyRule []interface{}
	for _, releaseKeyItem := range releaseKey {
		releaseKeyRule = append(releaseKeyRule, releaseKeyItem)
	}
	var guidRule []interface{}
	for _, guidItem := range guid {
		guidRule = append(guidRule, guidItem)
	}

	logs, sub, err := _AggLayerDVNCoordinator.contract.FilterLogs(opts, "ClaimedAndVerified", releaseKeyRule, guidRule)
	if err != nil {
		return nil, err
	}
	return &AggLayerDVNCoordinatorClaimedAndVerifiedIterator{contract: _AggLayerDVNCoordinator.contract, event: "ClaimedAndVerified", logs: logs, sub: sub}, nil
}

// WatchClaimedAndVerified is a free log subscription operation binding the contract event 0x9cb2b3ae6c86a8fd90323108b48ae6a80f730d2850a39826969467576f7f2f5b.
//
// Solidity: event ClaimedAndVerified(bytes32 indexed releaseKey, bytes32 indexed guid, bytes32 payloadHash)
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorFilterer) WatchClaimedAndVerified(opts *bind.WatchOpts, sink chan<- *AggLayerDVNCoordinatorClaimedAndVerified, releaseKey [][32]byte, guid [][32]byte) (event.Subscription, error) {

	var releaseKeyRule []interface{}
	for _, releaseKeyItem := range releaseKey {
		releaseKeyRule = append(releaseKeyRule, releaseKeyItem)
	}
	var guidRule []interface{}
	for _, guidItem := range guid {
		guidRule = append(guidRule, guidItem)
	}

	logs, sub, err := _AggLayerDVNCoordinator.contract.WatchLogs(opts, "ClaimedAndVerified", releaseKeyRule, guidRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(AggLayerDVNCoordinatorClaimedAndVerified)
				if err := _AggLayerDVNCoordinator.contract.UnpackLog(event, "ClaimedAndVerified", log); err != nil {
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

// ParseClaimedAndVerified is a log parse operation binding the contract event 0x9cb2b3ae6c86a8fd90323108b48ae6a80f730d2850a39826969467576f7f2f5b.
//
// Solidity: event ClaimedAndVerified(bytes32 indexed releaseKey, bytes32 indexed guid, bytes32 payloadHash)
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorFilterer) ParseClaimedAndVerified(log types.Log) (*AggLayerDVNCoordinatorClaimedAndVerified, error) {
	event := new(AggLayerDVNCoordinatorClaimedAndVerified)
	if err := _AggLayerDVNCoordinator.contract.UnpackLog(event, "ClaimedAndVerified", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// AggLayerDVNCoordinatorOwnershipTransferredIterator is returned from FilterOwnershipTransferred and is used to iterate over the raw logs and unpacked data for OwnershipTransferred events raised by the AggLayerDVNCoordinator contract.
type AggLayerDVNCoordinatorOwnershipTransferredIterator struct {
	Event *AggLayerDVNCoordinatorOwnershipTransferred // Event containing the contract specifics and raw log

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
func (it *AggLayerDVNCoordinatorOwnershipTransferredIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(AggLayerDVNCoordinatorOwnershipTransferred)
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
		it.Event = new(AggLayerDVNCoordinatorOwnershipTransferred)
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
func (it *AggLayerDVNCoordinatorOwnershipTransferredIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *AggLayerDVNCoordinatorOwnershipTransferredIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// AggLayerDVNCoordinatorOwnershipTransferred represents a OwnershipTransferred event raised by the AggLayerDVNCoordinator contract.
type AggLayerDVNCoordinatorOwnershipTransferred struct {
	PreviousOwner common.Address
	NewOwner      common.Address
	Raw           types.Log // Blockchain specific contextual infos
}

// FilterOwnershipTransferred is a free log retrieval operation binding the contract event 0x8be0079c531659141344cd1fd0a4f28419497f9722a3daafe3b4186f6b6457e0.
//
// Solidity: event OwnershipTransferred(address indexed previousOwner, address indexed newOwner)
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorFilterer) FilterOwnershipTransferred(opts *bind.FilterOpts, previousOwner []common.Address, newOwner []common.Address) (*AggLayerDVNCoordinatorOwnershipTransferredIterator, error) {

	var previousOwnerRule []interface{}
	for _, previousOwnerItem := range previousOwner {
		previousOwnerRule = append(previousOwnerRule, previousOwnerItem)
	}
	var newOwnerRule []interface{}
	for _, newOwnerItem := range newOwner {
		newOwnerRule = append(newOwnerRule, newOwnerItem)
	}

	logs, sub, err := _AggLayerDVNCoordinator.contract.FilterLogs(opts, "OwnershipTransferred", previousOwnerRule, newOwnerRule)
	if err != nil {
		return nil, err
	}
	return &AggLayerDVNCoordinatorOwnershipTransferredIterator{contract: _AggLayerDVNCoordinator.contract, event: "OwnershipTransferred", logs: logs, sub: sub}, nil
}

// WatchOwnershipTransferred is a free log subscription operation binding the contract event 0x8be0079c531659141344cd1fd0a4f28419497f9722a3daafe3b4186f6b6457e0.
//
// Solidity: event OwnershipTransferred(address indexed previousOwner, address indexed newOwner)
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorFilterer) WatchOwnershipTransferred(opts *bind.WatchOpts, sink chan<- *AggLayerDVNCoordinatorOwnershipTransferred, previousOwner []common.Address, newOwner []common.Address) (event.Subscription, error) {

	var previousOwnerRule []interface{}
	for _, previousOwnerItem := range previousOwner {
		previousOwnerRule = append(previousOwnerRule, previousOwnerItem)
	}
	var newOwnerRule []interface{}
	for _, newOwnerItem := range newOwner {
		newOwnerRule = append(newOwnerRule, newOwnerItem)
	}

	logs, sub, err := _AggLayerDVNCoordinator.contract.WatchLogs(opts, "OwnershipTransferred", previousOwnerRule, newOwnerRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(AggLayerDVNCoordinatorOwnershipTransferred)
				if err := _AggLayerDVNCoordinator.contract.UnpackLog(event, "OwnershipTransferred", log); err != nil {
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
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorFilterer) ParseOwnershipTransferred(log types.Log) (*AggLayerDVNCoordinatorOwnershipTransferred, error) {
	event := new(AggLayerDVNCoordinatorOwnershipTransferred)
	if err := _AggLayerDVNCoordinator.contract.UnpackLog(event, "OwnershipTransferred", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// AggLayerDVNCoordinatorWorkerAddedIterator is returned from FilterWorkerAdded and is used to iterate over the raw logs and unpacked data for WorkerAdded events raised by the AggLayerDVNCoordinator contract.
type AggLayerDVNCoordinatorWorkerAddedIterator struct {
	Event *AggLayerDVNCoordinatorWorkerAdded // Event containing the contract specifics and raw log

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
func (it *AggLayerDVNCoordinatorWorkerAddedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(AggLayerDVNCoordinatorWorkerAdded)
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
		it.Event = new(AggLayerDVNCoordinatorWorkerAdded)
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
func (it *AggLayerDVNCoordinatorWorkerAddedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *AggLayerDVNCoordinatorWorkerAddedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// AggLayerDVNCoordinatorWorkerAdded represents a WorkerAdded event raised by the AggLayerDVNCoordinator contract.
type AggLayerDVNCoordinatorWorkerAdded struct {
	Worker common.Address
	Raw    types.Log // Blockchain specific contextual infos
}

// FilterWorkerAdded is a free log retrieval operation binding the contract event 0xb10d2a24a8c3686841e966f0c2c64c385cfaecb50a09b16aa3579bfcf3989dcd.
//
// Solidity: event WorkerAdded(address indexed worker)
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorFilterer) FilterWorkerAdded(opts *bind.FilterOpts, worker []common.Address) (*AggLayerDVNCoordinatorWorkerAddedIterator, error) {

	var workerRule []interface{}
	for _, workerItem := range worker {
		workerRule = append(workerRule, workerItem)
	}

	logs, sub, err := _AggLayerDVNCoordinator.contract.FilterLogs(opts, "WorkerAdded", workerRule)
	if err != nil {
		return nil, err
	}
	return &AggLayerDVNCoordinatorWorkerAddedIterator{contract: _AggLayerDVNCoordinator.contract, event: "WorkerAdded", logs: logs, sub: sub}, nil
}

// WatchWorkerAdded is a free log subscription operation binding the contract event 0xb10d2a24a8c3686841e966f0c2c64c385cfaecb50a09b16aa3579bfcf3989dcd.
//
// Solidity: event WorkerAdded(address indexed worker)
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorFilterer) WatchWorkerAdded(opts *bind.WatchOpts, sink chan<- *AggLayerDVNCoordinatorWorkerAdded, worker []common.Address) (event.Subscription, error) {

	var workerRule []interface{}
	for _, workerItem := range worker {
		workerRule = append(workerRule, workerItem)
	}

	logs, sub, err := _AggLayerDVNCoordinator.contract.WatchLogs(opts, "WorkerAdded", workerRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(AggLayerDVNCoordinatorWorkerAdded)
				if err := _AggLayerDVNCoordinator.contract.UnpackLog(event, "WorkerAdded", log); err != nil {
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

// ParseWorkerAdded is a log parse operation binding the contract event 0xb10d2a24a8c3686841e966f0c2c64c385cfaecb50a09b16aa3579bfcf3989dcd.
//
// Solidity: event WorkerAdded(address indexed worker)
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorFilterer) ParseWorkerAdded(log types.Log) (*AggLayerDVNCoordinatorWorkerAdded, error) {
	event := new(AggLayerDVNCoordinatorWorkerAdded)
	if err := _AggLayerDVNCoordinator.contract.UnpackLog(event, "WorkerAdded", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// AggLayerDVNCoordinatorWorkerRemovedIterator is returned from FilterWorkerRemoved and is used to iterate over the raw logs and unpacked data for WorkerRemoved events raised by the AggLayerDVNCoordinator contract.
type AggLayerDVNCoordinatorWorkerRemovedIterator struct {
	Event *AggLayerDVNCoordinatorWorkerRemoved // Event containing the contract specifics and raw log

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
func (it *AggLayerDVNCoordinatorWorkerRemovedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(AggLayerDVNCoordinatorWorkerRemoved)
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
		it.Event = new(AggLayerDVNCoordinatorWorkerRemoved)
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
func (it *AggLayerDVNCoordinatorWorkerRemovedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *AggLayerDVNCoordinatorWorkerRemovedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// AggLayerDVNCoordinatorWorkerRemoved represents a WorkerRemoved event raised by the AggLayerDVNCoordinator contract.
type AggLayerDVNCoordinatorWorkerRemoved struct {
	Worker common.Address
	Raw    types.Log // Blockchain specific contextual infos
}

// FilterWorkerRemoved is a free log retrieval operation binding the contract event 0x6cfb0504498d3a8155a2a3dd5f41940ad5ab571197ac70f6d6948d189f6a0d27.
//
// Solidity: event WorkerRemoved(address indexed worker)
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorFilterer) FilterWorkerRemoved(opts *bind.FilterOpts, worker []common.Address) (*AggLayerDVNCoordinatorWorkerRemovedIterator, error) {

	var workerRule []interface{}
	for _, workerItem := range worker {
		workerRule = append(workerRule, workerItem)
	}

	logs, sub, err := _AggLayerDVNCoordinator.contract.FilterLogs(opts, "WorkerRemoved", workerRule)
	if err != nil {
		return nil, err
	}
	return &AggLayerDVNCoordinatorWorkerRemovedIterator{contract: _AggLayerDVNCoordinator.contract, event: "WorkerRemoved", logs: logs, sub: sub}, nil
}

// WatchWorkerRemoved is a free log subscription operation binding the contract event 0x6cfb0504498d3a8155a2a3dd5f41940ad5ab571197ac70f6d6948d189f6a0d27.
//
// Solidity: event WorkerRemoved(address indexed worker)
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorFilterer) WatchWorkerRemoved(opts *bind.WatchOpts, sink chan<- *AggLayerDVNCoordinatorWorkerRemoved, worker []common.Address) (event.Subscription, error) {

	var workerRule []interface{}
	for _, workerItem := range worker {
		workerRule = append(workerRule, workerItem)
	}

	logs, sub, err := _AggLayerDVNCoordinator.contract.WatchLogs(opts, "WorkerRemoved", workerRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(AggLayerDVNCoordinatorWorkerRemoved)
				if err := _AggLayerDVNCoordinator.contract.UnpackLog(event, "WorkerRemoved", log); err != nil {
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

// ParseWorkerRemoved is a log parse operation binding the contract event 0x6cfb0504498d3a8155a2a3dd5f41940ad5ab571197ac70f6d6948d189f6a0d27.
//
// Solidity: event WorkerRemoved(address indexed worker)
func (_AggLayerDVNCoordinator *AggLayerDVNCoordinatorFilterer) ParseWorkerRemoved(log types.Log) (*AggLayerDVNCoordinatorWorkerRemoved, error) {
	event := new(AggLayerDVNCoordinatorWorkerRemoved)
	if err := _AggLayerDVNCoordinator.contract.UnpackLog(event, "WorkerRemoved", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}
