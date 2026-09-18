// Code generated - DO NOT EDIT.
// This file is a generated binding and any manual changes will be lost.

package bridgeeventimpostor

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

// BridgeeventimpostorMetaData contains all meta data concerning the Bridgeeventimpostor contract.
var BridgeeventimpostorMetaData = &bind.MetaData{
	ABI: "[{\"anonymous\":false,\"inputs\":[{\"indexed\":false,\"internalType\":\"uint8\",\"name\":\"leafType\",\"type\":\"uint8\"},{\"indexed\":false,\"internalType\":\"uint32\",\"name\":\"originNetwork\",\"type\":\"uint32\"},{\"indexed\":false,\"internalType\":\"address\",\"name\":\"originAddress\",\"type\":\"address\"},{\"indexed\":false,\"internalType\":\"uint32\",\"name\":\"destinationNetwork\",\"type\":\"uint32\"},{\"indexed\":false,\"internalType\":\"address\",\"name\":\"destinationAddress\",\"type\":\"address\"},{\"indexed\":false,\"internalType\":\"uint256\",\"name\":\"amount\",\"type\":\"uint256\"},{\"indexed\":false,\"internalType\":\"bytes\",\"name\":\"metadata\",\"type\":\"bytes\"},{\"indexed\":false,\"internalType\":\"uint32\",\"name\":\"depositCount\",\"type\":\"uint32\"}],\"name\":\"BridgeEvent\",\"type\":\"event\"},{\"inputs\":[{\"internalType\":\"uint8\",\"name\":\"leafType\",\"type\":\"uint8\"},{\"internalType\":\"uint32\",\"name\":\"originNetwork\",\"type\":\"uint32\"},{\"internalType\":\"address\",\"name\":\"originAddress\",\"type\":\"address\"},{\"internalType\":\"uint32\",\"name\":\"destinationNetwork\",\"type\":\"uint32\"},{\"internalType\":\"address\",\"name\":\"destinationAddress\",\"type\":\"address\"},{\"internalType\":\"uint256\",\"name\":\"amount\",\"type\":\"uint256\"},{\"internalType\":\"bytes\",\"name\":\"metadata\",\"type\":\"bytes\"},{\"internalType\":\"uint32\",\"name\":\"depositCount\",\"type\":\"uint32\"}],\"name\":\"emitFakeBridgeEvent\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"}]",
	Bin: "0x60808060405234610016576101a0908161001c8239f35b600080fdfe608080604052600436101561001357600080fd5b600090813560e01c63eb5b20261461002a57600080fd5b346101665761010090816003193601126101625760043560ff811680910361015e5760243563ffffffff9384821680920361015a576001600160a01b03604435818116969194908790036101565760643592828416809403610152576084359586168096036101525760c4359567ffffffffffffffff9485881161014e573660238901121561014e57876004013595861161014e57366024878a01011161014e5760e43594851680950361014e577f501781209a1f8899323b96b4ef08b168df93e0a90c673d1e4cce39366cb62f9b99899787958952602089015260408801526060870152608086015260a43560a08601528060c0860152840152816024610120950185850137828201840187905260e0830152601f01601f19168101030190a180f35b8a80fd5b8880fd5b8780fd5b8580fd5b8380fd5b8280fd5b5080fdfea264697066735822122004d6a41842c3adfc02985f602c0223d9c7ca650783d2511d6c663231c4070dfa64736f6c63430008120033",
}

// BridgeeventimpostorABI is the input ABI used to generate the binding from.
// Deprecated: Use BridgeeventimpostorMetaData.ABI instead.
var BridgeeventimpostorABI = BridgeeventimpostorMetaData.ABI

// BridgeeventimpostorBin is the compiled bytecode used for deploying new contracts.
// Deprecated: Use BridgeeventimpostorMetaData.Bin instead.
var BridgeeventimpostorBin = BridgeeventimpostorMetaData.Bin

// DeployBridgeeventimpostor deploys a new Ethereum contract, binding an instance of Bridgeeventimpostor to it.
func DeployBridgeeventimpostor(auth *bind.TransactOpts, backend bind.ContractBackend) (common.Address, *types.Transaction, *Bridgeeventimpostor, error) {
	parsed, err := BridgeeventimpostorMetaData.GetAbi()
	if err != nil {
		return common.Address{}, nil, nil, err
	}
	if parsed == nil {
		return common.Address{}, nil, nil, errors.New("GetABI returned nil")
	}

	address, tx, contract, err := bind.DeployContract(auth, *parsed, common.FromHex(BridgeeventimpostorBin), backend)
	if err != nil {
		return common.Address{}, nil, nil, err
	}
	return address, tx, &Bridgeeventimpostor{BridgeeventimpostorCaller: BridgeeventimpostorCaller{contract: contract}, BridgeeventimpostorTransactor: BridgeeventimpostorTransactor{contract: contract}, BridgeeventimpostorFilterer: BridgeeventimpostorFilterer{contract: contract}}, nil
}

// Bridgeeventimpostor is an auto generated Go binding around an Ethereum contract.
type Bridgeeventimpostor struct {
	BridgeeventimpostorCaller     // Read-only binding to the contract
	BridgeeventimpostorTransactor // Write-only binding to the contract
	BridgeeventimpostorFilterer   // Log filterer for contract events
}

// BridgeeventimpostorCaller is an auto generated read-only Go binding around an Ethereum contract.
type BridgeeventimpostorCaller struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// BridgeeventimpostorTransactor is an auto generated write-only Go binding around an Ethereum contract.
type BridgeeventimpostorTransactor struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// BridgeeventimpostorFilterer is an auto generated log filtering Go binding around an Ethereum contract events.
type BridgeeventimpostorFilterer struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// BridgeeventimpostorSession is an auto generated Go binding around an Ethereum contract,
// with pre-set call and transact options.
type BridgeeventimpostorSession struct {
	Contract     *Bridgeeventimpostor // Generic contract binding to set the session for
	CallOpts     bind.CallOpts        // Call options to use throughout this session
	TransactOpts bind.TransactOpts    // Transaction auth options to use throughout this session
}

// BridgeeventimpostorCallerSession is an auto generated read-only Go binding around an Ethereum contract,
// with pre-set call options.
type BridgeeventimpostorCallerSession struct {
	Contract *BridgeeventimpostorCaller // Generic contract caller binding to set the session for
	CallOpts bind.CallOpts              // Call options to use throughout this session
}

// BridgeeventimpostorTransactorSession is an auto generated write-only Go binding around an Ethereum contract,
// with pre-set transact options.
type BridgeeventimpostorTransactorSession struct {
	Contract     *BridgeeventimpostorTransactor // Generic contract transactor binding to set the session for
	TransactOpts bind.TransactOpts              // Transaction auth options to use throughout this session
}

// BridgeeventimpostorRaw is an auto generated low-level Go binding around an Ethereum contract.
type BridgeeventimpostorRaw struct {
	Contract *Bridgeeventimpostor // Generic contract binding to access the raw methods on
}

// BridgeeventimpostorCallerRaw is an auto generated low-level read-only Go binding around an Ethereum contract.
type BridgeeventimpostorCallerRaw struct {
	Contract *BridgeeventimpostorCaller // Generic read-only contract binding to access the raw methods on
}

// BridgeeventimpostorTransactorRaw is an auto generated low-level write-only Go binding around an Ethereum contract.
type BridgeeventimpostorTransactorRaw struct {
	Contract *BridgeeventimpostorTransactor // Generic write-only contract binding to access the raw methods on
}

// NewBridgeeventimpostor creates a new instance of Bridgeeventimpostor, bound to a specific deployed contract.
func NewBridgeeventimpostor(address common.Address, backend bind.ContractBackend) (*Bridgeeventimpostor, error) {
	contract, err := bindBridgeeventimpostor(address, backend, backend, backend)
	if err != nil {
		return nil, err
	}
	return &Bridgeeventimpostor{BridgeeventimpostorCaller: BridgeeventimpostorCaller{contract: contract}, BridgeeventimpostorTransactor: BridgeeventimpostorTransactor{contract: contract}, BridgeeventimpostorFilterer: BridgeeventimpostorFilterer{contract: contract}}, nil
}

// NewBridgeeventimpostorCaller creates a new read-only instance of Bridgeeventimpostor, bound to a specific deployed contract.
func NewBridgeeventimpostorCaller(address common.Address, caller bind.ContractCaller) (*BridgeeventimpostorCaller, error) {
	contract, err := bindBridgeeventimpostor(address, caller, nil, nil)
	if err != nil {
		return nil, err
	}
	return &BridgeeventimpostorCaller{contract: contract}, nil
}

// NewBridgeeventimpostorTransactor creates a new write-only instance of Bridgeeventimpostor, bound to a specific deployed contract.
func NewBridgeeventimpostorTransactor(address common.Address, transactor bind.ContractTransactor) (*BridgeeventimpostorTransactor, error) {
	contract, err := bindBridgeeventimpostor(address, nil, transactor, nil)
	if err != nil {
		return nil, err
	}
	return &BridgeeventimpostorTransactor{contract: contract}, nil
}

// NewBridgeeventimpostorFilterer creates a new log filterer instance of Bridgeeventimpostor, bound to a specific deployed contract.
func NewBridgeeventimpostorFilterer(address common.Address, filterer bind.ContractFilterer) (*BridgeeventimpostorFilterer, error) {
	contract, err := bindBridgeeventimpostor(address, nil, nil, filterer)
	if err != nil {
		return nil, err
	}
	return &BridgeeventimpostorFilterer{contract: contract}, nil
}

// bindBridgeeventimpostor binds a generic wrapper to an already deployed contract.
func bindBridgeeventimpostor(address common.Address, caller bind.ContractCaller, transactor bind.ContractTransactor, filterer bind.ContractFilterer) (*bind.BoundContract, error) {
	parsed, err := BridgeeventimpostorMetaData.GetAbi()
	if err != nil {
		return nil, err
	}
	return bind.NewBoundContract(address, *parsed, caller, transactor, filterer), nil
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_Bridgeeventimpostor *BridgeeventimpostorRaw) Call(opts *bind.CallOpts, result *[]interface{}, method string, params ...interface{}) error {
	return _Bridgeeventimpostor.Contract.BridgeeventimpostorCaller.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_Bridgeeventimpostor *BridgeeventimpostorRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _Bridgeeventimpostor.Contract.BridgeeventimpostorTransactor.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_Bridgeeventimpostor *BridgeeventimpostorRaw) Transact(opts *bind.TransactOpts, method string, params ...interface{}) (*types.Transaction, error) {
	return _Bridgeeventimpostor.Contract.BridgeeventimpostorTransactor.contract.Transact(opts, method, params...)
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_Bridgeeventimpostor *BridgeeventimpostorCallerRaw) Call(opts *bind.CallOpts, result *[]interface{}, method string, params ...interface{}) error {
	return _Bridgeeventimpostor.Contract.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_Bridgeeventimpostor *BridgeeventimpostorTransactorRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _Bridgeeventimpostor.Contract.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_Bridgeeventimpostor *BridgeeventimpostorTransactorRaw) Transact(opts *bind.TransactOpts, method string, params ...interface{}) (*types.Transaction, error) {
	return _Bridgeeventimpostor.Contract.contract.Transact(opts, method, params...)
}

// EmitFakeBridgeEvent is a paid mutator transaction binding the contract method 0xeb5b2026.
//
// Solidity: function emitFakeBridgeEvent(uint8 leafType, uint32 originNetwork, address originAddress, uint32 destinationNetwork, address destinationAddress, uint256 amount, bytes metadata, uint32 depositCount) returns()
func (_Bridgeeventimpostor *BridgeeventimpostorTransactor) EmitFakeBridgeEvent(opts *bind.TransactOpts, leafType uint8, originNetwork uint32, originAddress common.Address, destinationNetwork uint32, destinationAddress common.Address, amount *big.Int, metadata []byte, depositCount uint32) (*types.Transaction, error) {
	return _Bridgeeventimpostor.contract.Transact(opts, "emitFakeBridgeEvent", leafType, originNetwork, originAddress, destinationNetwork, destinationAddress, amount, metadata, depositCount)
}

// EmitFakeBridgeEvent is a paid mutator transaction binding the contract method 0xeb5b2026.
//
// Solidity: function emitFakeBridgeEvent(uint8 leafType, uint32 originNetwork, address originAddress, uint32 destinationNetwork, address destinationAddress, uint256 amount, bytes metadata, uint32 depositCount) returns()
func (_Bridgeeventimpostor *BridgeeventimpostorSession) EmitFakeBridgeEvent(leafType uint8, originNetwork uint32, originAddress common.Address, destinationNetwork uint32, destinationAddress common.Address, amount *big.Int, metadata []byte, depositCount uint32) (*types.Transaction, error) {
	return _Bridgeeventimpostor.Contract.EmitFakeBridgeEvent(&_Bridgeeventimpostor.TransactOpts, leafType, originNetwork, originAddress, destinationNetwork, destinationAddress, amount, metadata, depositCount)
}

// EmitFakeBridgeEvent is a paid mutator transaction binding the contract method 0xeb5b2026.
//
// Solidity: function emitFakeBridgeEvent(uint8 leafType, uint32 originNetwork, address originAddress, uint32 destinationNetwork, address destinationAddress, uint256 amount, bytes metadata, uint32 depositCount) returns()
func (_Bridgeeventimpostor *BridgeeventimpostorTransactorSession) EmitFakeBridgeEvent(leafType uint8, originNetwork uint32, originAddress common.Address, destinationNetwork uint32, destinationAddress common.Address, amount *big.Int, metadata []byte, depositCount uint32) (*types.Transaction, error) {
	return _Bridgeeventimpostor.Contract.EmitFakeBridgeEvent(&_Bridgeeventimpostor.TransactOpts, leafType, originNetwork, originAddress, destinationNetwork, destinationAddress, amount, metadata, depositCount)
}

// BridgeeventimpostorBridgeEventIterator is returned from FilterBridgeEvent and is used to iterate over the raw logs and unpacked data for BridgeEvent events raised by the Bridgeeventimpostor contract.
type BridgeeventimpostorBridgeEventIterator struct {
	Event *BridgeeventimpostorBridgeEvent // Event containing the contract specifics and raw log

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
func (it *BridgeeventimpostorBridgeEventIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(BridgeeventimpostorBridgeEvent)
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
		it.Event = new(BridgeeventimpostorBridgeEvent)
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
func (it *BridgeeventimpostorBridgeEventIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *BridgeeventimpostorBridgeEventIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// BridgeeventimpostorBridgeEvent represents a BridgeEvent event raised by the Bridgeeventimpostor contract.
type BridgeeventimpostorBridgeEvent struct {
	LeafType           uint8
	OriginNetwork      uint32
	OriginAddress      common.Address
	DestinationNetwork uint32
	DestinationAddress common.Address
	Amount             *big.Int
	Metadata           []byte
	DepositCount       uint32
	Raw                types.Log // Blockchain specific contextual infos
}

// FilterBridgeEvent is a free log retrieval operation binding the contract event 0x501781209a1f8899323b96b4ef08b168df93e0a90c673d1e4cce39366cb62f9b.
//
// Solidity: event BridgeEvent(uint8 leafType, uint32 originNetwork, address originAddress, uint32 destinationNetwork, address destinationAddress, uint256 amount, bytes metadata, uint32 depositCount)
func (_Bridgeeventimpostor *BridgeeventimpostorFilterer) FilterBridgeEvent(opts *bind.FilterOpts) (*BridgeeventimpostorBridgeEventIterator, error) {

	logs, sub, err := _Bridgeeventimpostor.contract.FilterLogs(opts, "BridgeEvent")
	if err != nil {
		return nil, err
	}
	return &BridgeeventimpostorBridgeEventIterator{contract: _Bridgeeventimpostor.contract, event: "BridgeEvent", logs: logs, sub: sub}, nil
}

// WatchBridgeEvent is a free log subscription operation binding the contract event 0x501781209a1f8899323b96b4ef08b168df93e0a90c673d1e4cce39366cb62f9b.
//
// Solidity: event BridgeEvent(uint8 leafType, uint32 originNetwork, address originAddress, uint32 destinationNetwork, address destinationAddress, uint256 amount, bytes metadata, uint32 depositCount)
func (_Bridgeeventimpostor *BridgeeventimpostorFilterer) WatchBridgeEvent(opts *bind.WatchOpts, sink chan<- *BridgeeventimpostorBridgeEvent) (event.Subscription, error) {

	logs, sub, err := _Bridgeeventimpostor.contract.WatchLogs(opts, "BridgeEvent")
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(BridgeeventimpostorBridgeEvent)
				if err := _Bridgeeventimpostor.contract.UnpackLog(event, "BridgeEvent", log); err != nil {
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

// ParseBridgeEvent is a log parse operation binding the contract event 0x501781209a1f8899323b96b4ef08b168df93e0a90c673d1e4cce39366cb62f9b.
//
// Solidity: event BridgeEvent(uint8 leafType, uint32 originNetwork, address originAddress, uint32 destinationNetwork, address destinationAddress, uint256 amount, bytes metadata, uint32 depositCount)
func (_Bridgeeventimpostor *BridgeeventimpostorFilterer) ParseBridgeEvent(log types.Log) (*BridgeeventimpostorBridgeEvent, error) {
	event := new(BridgeeventimpostorBridgeEvent)
	if err := _Bridgeeventimpostor.contract.UnpackLog(event, "BridgeEvent", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}
