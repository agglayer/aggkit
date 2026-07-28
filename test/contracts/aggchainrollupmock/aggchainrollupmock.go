// Code generated - DO NOT EDIT.
// This file is a generated binding and any manual changes will be lost.

package aggchainrollupmock

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

// AggchainrollupmockMetaData contains all meta data concerning the Aggchainrollupmock contract.
var AggchainrollupmockMetaData = &bind.MetaData{
	ABI: "[{\"anonymous\":false,\"inputs\":[{\"indexed\":true,\"internalType\":\"string\",\"name\":\"key\",\"type\":\"string\"},{\"indexed\":false,\"internalType\":\"string\",\"name\":\"value\",\"type\":\"string\"}],\"name\":\"AggchainMetadataSet\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":false,\"internalType\":\"string\",\"name\":\"newTrustedSequencerURL\",\"type\":\"string\"}],\"name\":\"SetTrustedSequencerURL\",\"type\":\"event\"},{\"inputs\":[{\"internalType\":\"string\",\"name\":\"key\",\"type\":\"string\"}],\"name\":\"aggchainMetadata\",\"outputs\":[{\"internalType\":\"string\",\"name\":\"\",\"type\":\"string\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"string\",\"name\":\"key\",\"type\":\"string\"},{\"internalType\":\"string\",\"name\":\"value\",\"type\":\"string\"}],\"name\":\"setAggchainMetadata\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"string\",\"name\":\"newTrustedSequencerURL\",\"type\":\"string\"}],\"name\":\"setTrustedSequencerURL\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[],\"name\":\"trustedSequencerURL\",\"outputs\":[{\"internalType\":\"string\",\"name\":\"\",\"type\":\"string\"}],\"stateMutability\":\"view\",\"type\":\"function\"}]",
	Bin: "0x608060405234801561001057600080fd5b506105db806100206000396000f3fe608060405234801561001057600080fd5b506004361061004c5760003560e01c8063052358be14610051578063542028d51461006657806359a03e0f14610084578063c89e42df14610097575b600080fd5b61006461005f36600461030a565b6100aa565b005b61006e610131565b60405161007b9190610376565b60405180910390f35b61006e6100923660046103c4565b6101c3565b6100646100a53660046103c4565b610276565b8181600186866040516100be929190610406565b908152602001604051809103902091826100d99291906104b5565b5083836040516100ea929190610406565b60405180910390207f2779f9edd5ec4e0a99bffdea4008c8b979200959062a2bf00142acb939ca1b648383604051610123929190610576565b60405180910390a250505050565b6060600080546101409061042c565b80601f016020809104026020016040519081016040528092919081815260200182805461016c9061042c565b80156101b95780601f1061018e576101008083540402835291602001916101b9565b820191906000526020600020905b81548152906001019060200180831161019c57829003601f168201915b5050505050905090565b6060600183836040516101d7929190610406565b908152602001604051809103902080546101f09061042c565b80601f016020809104026020016040519081016040528092919081815260200182805461021c9061042c565b80156102695780601f1061023e57610100808354040283529160200191610269565b820191906000526020600020905b81548152906001019060200180831161024c57829003601f168201915b5050505050905092915050565b60006102838284836104b5565b507f6b8f723a4c7a5335cafae8a598a0aa0301be1387c037dccc085b62add6448b2082826040516102b5929190610576565b60405180910390a15050565b60008083601f8401126102d357600080fd5b50813567ffffffffffffffff8111156102eb57600080fd5b60208301915083602082850101111561030357600080fd5b9250929050565b6000806000806040858703121561032057600080fd5b843567ffffffffffffffff8082111561033857600080fd5b610344888389016102c1565b9096509450602087013591508082111561035d57600080fd5b5061036a878288016102c1565b95989497509550505050565b600060208083528351808285015260005b818110156103a357858101830151858201604001528201610387565b506000604082860101526040601f19601f8301168501019250505092915050565b600080602083850312156103d757600080fd5b823567ffffffffffffffff8111156103ee57600080fd5b6103fa858286016102c1565b90969095509350505050565b8183823760009101908152919050565b634e487b7160e01b600052604160045260246000fd5b600181811c9082168061044057607f821691505b60208210810361046057634e487b7160e01b600052602260045260246000fd5b50919050565b601f8211156104b057600081815260208120601f850160051c8101602086101561048d5750805b601f850160051c820191505b818110156104ac57828155600101610499565b5050505b505050565b67ffffffffffffffff8311156104cd576104cd610416565b6104e1836104db835461042c565b83610466565b6000601f84116001811461051557600085156104fd5750838201355b600019600387901b1c1916600186901b17835561056f565b600083815260209020601f19861690835b828110156105465786850135825560209485019460019092019101610526565b50868210156105635760001960f88860031b161c19848701351681555b505060018560011b0183555b5050505050565b60208152816020820152818360408301376000818301604090810191909152601f909201601f1916010191905056fea26469706673582212202a3df1e05da5c4d7543470d247b211a92730a36b21b9c9f67df540d98e298fe764736f6c63430008120033",
}

// AggchainrollupmockABI is the input ABI used to generate the binding from.
// Deprecated: Use AggchainrollupmockMetaData.ABI instead.
var AggchainrollupmockABI = AggchainrollupmockMetaData.ABI

// AggchainrollupmockBin is the compiled bytecode used for deploying new contracts.
// Deprecated: Use AggchainrollupmockMetaData.Bin instead.
var AggchainrollupmockBin = AggchainrollupmockMetaData.Bin

// DeployAggchainrollupmock deploys a new Ethereum contract, binding an instance of Aggchainrollupmock to it.
func DeployAggchainrollupmock(auth *bind.TransactOpts, backend bind.ContractBackend) (common.Address, *types.Transaction, *Aggchainrollupmock, error) {
	parsed, err := AggchainrollupmockMetaData.GetAbi()
	if err != nil {
		return common.Address{}, nil, nil, err
	}
	if parsed == nil {
		return common.Address{}, nil, nil, errors.New("GetABI returned nil")
	}

	address, tx, contract, err := bind.DeployContract(auth, *parsed, common.FromHex(AggchainrollupmockBin), backend)
	if err != nil {
		return common.Address{}, nil, nil, err
	}
	return address, tx, &Aggchainrollupmock{AggchainrollupmockCaller: AggchainrollupmockCaller{contract: contract}, AggchainrollupmockTransactor: AggchainrollupmockTransactor{contract: contract}, AggchainrollupmockFilterer: AggchainrollupmockFilterer{contract: contract}}, nil
}

// Aggchainrollupmock is an auto generated Go binding around an Ethereum contract.
type Aggchainrollupmock struct {
	AggchainrollupmockCaller     // Read-only binding to the contract
	AggchainrollupmockTransactor // Write-only binding to the contract
	AggchainrollupmockFilterer   // Log filterer for contract events
}

// AggchainrollupmockCaller is an auto generated read-only Go binding around an Ethereum contract.
type AggchainrollupmockCaller struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// AggchainrollupmockTransactor is an auto generated write-only Go binding around an Ethereum contract.
type AggchainrollupmockTransactor struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// AggchainrollupmockFilterer is an auto generated log filtering Go binding around an Ethereum contract events.
type AggchainrollupmockFilterer struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// AggchainrollupmockSession is an auto generated Go binding around an Ethereum contract,
// with pre-set call and transact options.
type AggchainrollupmockSession struct {
	Contract     *Aggchainrollupmock // Generic contract binding to set the session for
	CallOpts     bind.CallOpts       // Call options to use throughout this session
	TransactOpts bind.TransactOpts   // Transaction auth options to use throughout this session
}

// AggchainrollupmockCallerSession is an auto generated read-only Go binding around an Ethereum contract,
// with pre-set call options.
type AggchainrollupmockCallerSession struct {
	Contract *AggchainrollupmockCaller // Generic contract caller binding to set the session for
	CallOpts bind.CallOpts             // Call options to use throughout this session
}

// AggchainrollupmockTransactorSession is an auto generated write-only Go binding around an Ethereum contract,
// with pre-set transact options.
type AggchainrollupmockTransactorSession struct {
	Contract     *AggchainrollupmockTransactor // Generic contract transactor binding to set the session for
	TransactOpts bind.TransactOpts             // Transaction auth options to use throughout this session
}

// AggchainrollupmockRaw is an auto generated low-level Go binding around an Ethereum contract.
type AggchainrollupmockRaw struct {
	Contract *Aggchainrollupmock // Generic contract binding to access the raw methods on
}

// AggchainrollupmockCallerRaw is an auto generated low-level read-only Go binding around an Ethereum contract.
type AggchainrollupmockCallerRaw struct {
	Contract *AggchainrollupmockCaller // Generic read-only contract binding to access the raw methods on
}

// AggchainrollupmockTransactorRaw is an auto generated low-level write-only Go binding around an Ethereum contract.
type AggchainrollupmockTransactorRaw struct {
	Contract *AggchainrollupmockTransactor // Generic write-only contract binding to access the raw methods on
}

// NewAggchainrollupmock creates a new instance of Aggchainrollupmock, bound to a specific deployed contract.
func NewAggchainrollupmock(address common.Address, backend bind.ContractBackend) (*Aggchainrollupmock, error) {
	contract, err := bindAggchainrollupmock(address, backend, backend, backend)
	if err != nil {
		return nil, err
	}
	return &Aggchainrollupmock{AggchainrollupmockCaller: AggchainrollupmockCaller{contract: contract}, AggchainrollupmockTransactor: AggchainrollupmockTransactor{contract: contract}, AggchainrollupmockFilterer: AggchainrollupmockFilterer{contract: contract}}, nil
}

// NewAggchainrollupmockCaller creates a new read-only instance of Aggchainrollupmock, bound to a specific deployed contract.
func NewAggchainrollupmockCaller(address common.Address, caller bind.ContractCaller) (*AggchainrollupmockCaller, error) {
	contract, err := bindAggchainrollupmock(address, caller, nil, nil)
	if err != nil {
		return nil, err
	}
	return &AggchainrollupmockCaller{contract: contract}, nil
}

// NewAggchainrollupmockTransactor creates a new write-only instance of Aggchainrollupmock, bound to a specific deployed contract.
func NewAggchainrollupmockTransactor(address common.Address, transactor bind.ContractTransactor) (*AggchainrollupmockTransactor, error) {
	contract, err := bindAggchainrollupmock(address, nil, transactor, nil)
	if err != nil {
		return nil, err
	}
	return &AggchainrollupmockTransactor{contract: contract}, nil
}

// NewAggchainrollupmockFilterer creates a new log filterer instance of Aggchainrollupmock, bound to a specific deployed contract.
func NewAggchainrollupmockFilterer(address common.Address, filterer bind.ContractFilterer) (*AggchainrollupmockFilterer, error) {
	contract, err := bindAggchainrollupmock(address, nil, nil, filterer)
	if err != nil {
		return nil, err
	}
	return &AggchainrollupmockFilterer{contract: contract}, nil
}

// bindAggchainrollupmock binds a generic wrapper to an already deployed contract.
func bindAggchainrollupmock(address common.Address, caller bind.ContractCaller, transactor bind.ContractTransactor, filterer bind.ContractFilterer) (*bind.BoundContract, error) {
	parsed, err := AggchainrollupmockMetaData.GetAbi()
	if err != nil {
		return nil, err
	}
	return bind.NewBoundContract(address, *parsed, caller, transactor, filterer), nil
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_Aggchainrollupmock *AggchainrollupmockRaw) Call(opts *bind.CallOpts, result *[]interface{}, method string, params ...interface{}) error {
	return _Aggchainrollupmock.Contract.AggchainrollupmockCaller.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_Aggchainrollupmock *AggchainrollupmockRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _Aggchainrollupmock.Contract.AggchainrollupmockTransactor.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_Aggchainrollupmock *AggchainrollupmockRaw) Transact(opts *bind.TransactOpts, method string, params ...interface{}) (*types.Transaction, error) {
	return _Aggchainrollupmock.Contract.AggchainrollupmockTransactor.contract.Transact(opts, method, params...)
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_Aggchainrollupmock *AggchainrollupmockCallerRaw) Call(opts *bind.CallOpts, result *[]interface{}, method string, params ...interface{}) error {
	return _Aggchainrollupmock.Contract.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_Aggchainrollupmock *AggchainrollupmockTransactorRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _Aggchainrollupmock.Contract.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_Aggchainrollupmock *AggchainrollupmockTransactorRaw) Transact(opts *bind.TransactOpts, method string, params ...interface{}) (*types.Transaction, error) {
	return _Aggchainrollupmock.Contract.contract.Transact(opts, method, params...)
}

// AggchainMetadata is a free data retrieval call binding the contract method 0x59a03e0f.
//
// Solidity: function aggchainMetadata(string key) view returns(string)
func (_Aggchainrollupmock *AggchainrollupmockCaller) AggchainMetadata(opts *bind.CallOpts, key string) (string, error) {
	var out []interface{}
	err := _Aggchainrollupmock.contract.Call(opts, &out, "aggchainMetadata", key)

	if err != nil {
		return *new(string), err
	}

	out0 := *abi.ConvertType(out[0], new(string)).(*string)

	return out0, err

}

// AggchainMetadata is a free data retrieval call binding the contract method 0x59a03e0f.
//
// Solidity: function aggchainMetadata(string key) view returns(string)
func (_Aggchainrollupmock *AggchainrollupmockSession) AggchainMetadata(key string) (string, error) {
	return _Aggchainrollupmock.Contract.AggchainMetadata(&_Aggchainrollupmock.CallOpts, key)
}

// AggchainMetadata is a free data retrieval call binding the contract method 0x59a03e0f.
//
// Solidity: function aggchainMetadata(string key) view returns(string)
func (_Aggchainrollupmock *AggchainrollupmockCallerSession) AggchainMetadata(key string) (string, error) {
	return _Aggchainrollupmock.Contract.AggchainMetadata(&_Aggchainrollupmock.CallOpts, key)
}

// TrustedSequencerURL is a free data retrieval call binding the contract method 0x542028d5.
//
// Solidity: function trustedSequencerURL() view returns(string)
func (_Aggchainrollupmock *AggchainrollupmockCaller) TrustedSequencerURL(opts *bind.CallOpts) (string, error) {
	var out []interface{}
	err := _Aggchainrollupmock.contract.Call(opts, &out, "trustedSequencerURL")

	if err != nil {
		return *new(string), err
	}

	out0 := *abi.ConvertType(out[0], new(string)).(*string)

	return out0, err

}

// TrustedSequencerURL is a free data retrieval call binding the contract method 0x542028d5.
//
// Solidity: function trustedSequencerURL() view returns(string)
func (_Aggchainrollupmock *AggchainrollupmockSession) TrustedSequencerURL() (string, error) {
	return _Aggchainrollupmock.Contract.TrustedSequencerURL(&_Aggchainrollupmock.CallOpts)
}

// TrustedSequencerURL is a free data retrieval call binding the contract method 0x542028d5.
//
// Solidity: function trustedSequencerURL() view returns(string)
func (_Aggchainrollupmock *AggchainrollupmockCallerSession) TrustedSequencerURL() (string, error) {
	return _Aggchainrollupmock.Contract.TrustedSequencerURL(&_Aggchainrollupmock.CallOpts)
}

// SetAggchainMetadata is a paid mutator transaction binding the contract method 0x052358be.
//
// Solidity: function setAggchainMetadata(string key, string value) returns()
func (_Aggchainrollupmock *AggchainrollupmockTransactor) SetAggchainMetadata(opts *bind.TransactOpts, key string, value string) (*types.Transaction, error) {
	return _Aggchainrollupmock.contract.Transact(opts, "setAggchainMetadata", key, value)
}

// SetAggchainMetadata is a paid mutator transaction binding the contract method 0x052358be.
//
// Solidity: function setAggchainMetadata(string key, string value) returns()
func (_Aggchainrollupmock *AggchainrollupmockSession) SetAggchainMetadata(key string, value string) (*types.Transaction, error) {
	return _Aggchainrollupmock.Contract.SetAggchainMetadata(&_Aggchainrollupmock.TransactOpts, key, value)
}

// SetAggchainMetadata is a paid mutator transaction binding the contract method 0x052358be.
//
// Solidity: function setAggchainMetadata(string key, string value) returns()
func (_Aggchainrollupmock *AggchainrollupmockTransactorSession) SetAggchainMetadata(key string, value string) (*types.Transaction, error) {
	return _Aggchainrollupmock.Contract.SetAggchainMetadata(&_Aggchainrollupmock.TransactOpts, key, value)
}

// SetTrustedSequencerURL is a paid mutator transaction binding the contract method 0xc89e42df.
//
// Solidity: function setTrustedSequencerURL(string newTrustedSequencerURL) returns()
func (_Aggchainrollupmock *AggchainrollupmockTransactor) SetTrustedSequencerURL(opts *bind.TransactOpts, newTrustedSequencerURL string) (*types.Transaction, error) {
	return _Aggchainrollupmock.contract.Transact(opts, "setTrustedSequencerURL", newTrustedSequencerURL)
}

// SetTrustedSequencerURL is a paid mutator transaction binding the contract method 0xc89e42df.
//
// Solidity: function setTrustedSequencerURL(string newTrustedSequencerURL) returns()
func (_Aggchainrollupmock *AggchainrollupmockSession) SetTrustedSequencerURL(newTrustedSequencerURL string) (*types.Transaction, error) {
	return _Aggchainrollupmock.Contract.SetTrustedSequencerURL(&_Aggchainrollupmock.TransactOpts, newTrustedSequencerURL)
}

// SetTrustedSequencerURL is a paid mutator transaction binding the contract method 0xc89e42df.
//
// Solidity: function setTrustedSequencerURL(string newTrustedSequencerURL) returns()
func (_Aggchainrollupmock *AggchainrollupmockTransactorSession) SetTrustedSequencerURL(newTrustedSequencerURL string) (*types.Transaction, error) {
	return _Aggchainrollupmock.Contract.SetTrustedSequencerURL(&_Aggchainrollupmock.TransactOpts, newTrustedSequencerURL)
}

// AggchainrollupmockAggchainMetadataSetIterator is returned from FilterAggchainMetadataSet and is used to iterate over the raw logs and unpacked data for AggchainMetadataSet events raised by the Aggchainrollupmock contract.
type AggchainrollupmockAggchainMetadataSetIterator struct {
	Event *AggchainrollupmockAggchainMetadataSet // Event containing the contract specifics and raw log

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
func (it *AggchainrollupmockAggchainMetadataSetIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(AggchainrollupmockAggchainMetadataSet)
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
		it.Event = new(AggchainrollupmockAggchainMetadataSet)
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
func (it *AggchainrollupmockAggchainMetadataSetIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *AggchainrollupmockAggchainMetadataSetIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// AggchainrollupmockAggchainMetadataSet represents a AggchainMetadataSet event raised by the Aggchainrollupmock contract.
type AggchainrollupmockAggchainMetadataSet struct {
	Key   common.Hash
	Value string
	Raw   types.Log // Blockchain specific contextual infos
}

// FilterAggchainMetadataSet is a free log retrieval operation binding the contract event 0x2779f9edd5ec4e0a99bffdea4008c8b979200959062a2bf00142acb939ca1b64.
//
// Solidity: event AggchainMetadataSet(string indexed key, string value)
func (_Aggchainrollupmock *AggchainrollupmockFilterer) FilterAggchainMetadataSet(opts *bind.FilterOpts, key []string) (*AggchainrollupmockAggchainMetadataSetIterator, error) {

	var keyRule []interface{}
	for _, keyItem := range key {
		keyRule = append(keyRule, keyItem)
	}

	logs, sub, err := _Aggchainrollupmock.contract.FilterLogs(opts, "AggchainMetadataSet", keyRule)
	if err != nil {
		return nil, err
	}
	return &AggchainrollupmockAggchainMetadataSetIterator{contract: _Aggchainrollupmock.contract, event: "AggchainMetadataSet", logs: logs, sub: sub}, nil
}

// WatchAggchainMetadataSet is a free log subscription operation binding the contract event 0x2779f9edd5ec4e0a99bffdea4008c8b979200959062a2bf00142acb939ca1b64.
//
// Solidity: event AggchainMetadataSet(string indexed key, string value)
func (_Aggchainrollupmock *AggchainrollupmockFilterer) WatchAggchainMetadataSet(opts *bind.WatchOpts, sink chan<- *AggchainrollupmockAggchainMetadataSet, key []string) (event.Subscription, error) {

	var keyRule []interface{}
	for _, keyItem := range key {
		keyRule = append(keyRule, keyItem)
	}

	logs, sub, err := _Aggchainrollupmock.contract.WatchLogs(opts, "AggchainMetadataSet", keyRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(AggchainrollupmockAggchainMetadataSet)
				if err := _Aggchainrollupmock.contract.UnpackLog(event, "AggchainMetadataSet", log); err != nil {
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

// ParseAggchainMetadataSet is a log parse operation binding the contract event 0x2779f9edd5ec4e0a99bffdea4008c8b979200959062a2bf00142acb939ca1b64.
//
// Solidity: event AggchainMetadataSet(string indexed key, string value)
func (_Aggchainrollupmock *AggchainrollupmockFilterer) ParseAggchainMetadataSet(log types.Log) (*AggchainrollupmockAggchainMetadataSet, error) {
	event := new(AggchainrollupmockAggchainMetadataSet)
	if err := _Aggchainrollupmock.contract.UnpackLog(event, "AggchainMetadataSet", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// AggchainrollupmockSetTrustedSequencerURLIterator is returned from FilterSetTrustedSequencerURL and is used to iterate over the raw logs and unpacked data for SetTrustedSequencerURL events raised by the Aggchainrollupmock contract.
type AggchainrollupmockSetTrustedSequencerURLIterator struct {
	Event *AggchainrollupmockSetTrustedSequencerURL // Event containing the contract specifics and raw log

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
func (it *AggchainrollupmockSetTrustedSequencerURLIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(AggchainrollupmockSetTrustedSequencerURL)
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
		it.Event = new(AggchainrollupmockSetTrustedSequencerURL)
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
func (it *AggchainrollupmockSetTrustedSequencerURLIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *AggchainrollupmockSetTrustedSequencerURLIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// AggchainrollupmockSetTrustedSequencerURL represents a SetTrustedSequencerURL event raised by the Aggchainrollupmock contract.
type AggchainrollupmockSetTrustedSequencerURL struct {
	NewTrustedSequencerURL string
	Raw                    types.Log // Blockchain specific contextual infos
}

// FilterSetTrustedSequencerURL is a free log retrieval operation binding the contract event 0x6b8f723a4c7a5335cafae8a598a0aa0301be1387c037dccc085b62add6448b20.
//
// Solidity: event SetTrustedSequencerURL(string newTrustedSequencerURL)
func (_Aggchainrollupmock *AggchainrollupmockFilterer) FilterSetTrustedSequencerURL(opts *bind.FilterOpts) (*AggchainrollupmockSetTrustedSequencerURLIterator, error) {

	logs, sub, err := _Aggchainrollupmock.contract.FilterLogs(opts, "SetTrustedSequencerURL")
	if err != nil {
		return nil, err
	}
	return &AggchainrollupmockSetTrustedSequencerURLIterator{contract: _Aggchainrollupmock.contract, event: "SetTrustedSequencerURL", logs: logs, sub: sub}, nil
}

// WatchSetTrustedSequencerURL is a free log subscription operation binding the contract event 0x6b8f723a4c7a5335cafae8a598a0aa0301be1387c037dccc085b62add6448b20.
//
// Solidity: event SetTrustedSequencerURL(string newTrustedSequencerURL)
func (_Aggchainrollupmock *AggchainrollupmockFilterer) WatchSetTrustedSequencerURL(opts *bind.WatchOpts, sink chan<- *AggchainrollupmockSetTrustedSequencerURL) (event.Subscription, error) {

	logs, sub, err := _Aggchainrollupmock.contract.WatchLogs(opts, "SetTrustedSequencerURL")
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(AggchainrollupmockSetTrustedSequencerURL)
				if err := _Aggchainrollupmock.contract.UnpackLog(event, "SetTrustedSequencerURL", log); err != nil {
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

// ParseSetTrustedSequencerURL is a log parse operation binding the contract event 0x6b8f723a4c7a5335cafae8a598a0aa0301be1387c037dccc085b62add6448b20.
//
// Solidity: event SetTrustedSequencerURL(string newTrustedSequencerURL)
func (_Aggchainrollupmock *AggchainrollupmockFilterer) ParseSetTrustedSequencerURL(log types.Log) (*AggchainrollupmockSetTrustedSequencerURL, error) {
	event := new(AggchainrollupmockSetTrustedSequencerURL)
	if err := _Aggchainrollupmock.contract.UnpackLog(event, "SetTrustedSequencerURL", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}
