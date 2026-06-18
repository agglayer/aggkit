// Code generated - DO NOT EDIT.
// This file is a generated binding and any manual changes will be lost.

package sequencerurlrollupmock

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

// SequencerurlrollupmockMetaData contains all meta data concerning the Sequencerurlrollupmock contract.
var SequencerurlrollupmockMetaData = &bind.MetaData{
	ABI: "[{\"inputs\":[{\"internalType\":\"string\",\"name\":\"initialSequencerURL\",\"type\":\"string\"}],\"stateMutability\":\"nonpayable\",\"type\":\"constructor\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":false,\"internalType\":\"string\",\"name\":\"newTrustedSequencerURL\",\"type\":\"string\"}],\"name\":\"SetTrustedSequencerURL\",\"type\":\"event\"},{\"inputs\":[{\"internalType\":\"string\",\"name\":\"newTrustedSequencerURL\",\"type\":\"string\"}],\"name\":\"setTrustedSequencerURL\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[],\"name\":\"trustedSequencerURL\",\"outputs\":[{\"internalType\":\"string\",\"name\":\"\",\"type\":\"string\"}],\"stateMutability\":\"view\",\"type\":\"function\"}]",
	Bin: "0x6080604052346101f55761060480380380610019816101fa565b928339810190602080828403126101f55781516001600160401b03928382116101f5570192601f908082860112156101f55784518481116101cb57601f1995610067828501881686016101fa565b928284528583830101116101f557849060005b8381106101e15750506000918301015280519384116101cb57600054926001938481811c911680156101c1575b828210146101ab57838111610165575b50809285116001146101005750839450908392916000946100f5575b50501b916000199060031b1c1916176000555b6040516103e490816102208239f35b0151925038806100d3565b9294849081166000805284600020946000905b8883831061014b5750505010610132575b505050811b016000556100e6565b015160001960f88460031b161c19169055388080610124565b858701518855909601959485019487935090810190610113565b60008052816000208480880160051c8201928489106101a2575b0160051c019085905b8281106101965750506100b7565b60008155018590610188565b9250819261017f565b634e487b7160e01b600052602260045260246000fd5b90607f16906100a7565b634e487b7160e01b600052604160045260246000fd5b81810183015185820184015286920161007a565b600080fd5b6040519190601f01601f191682016001600160401b038111838210176101cb5760405256fe6080604090808252600436101561001557600080fd5b600090813560e01c908163542028d514610236575063c89e42df1461003957600080fd5b346102335760208060031936011261022f576004359267ffffffffffffffff9182851161022b573660238601121561022b57846004013592831161022b576024368185880101116102275761008e8554610374565b601f81116101c6575b5084601f851160011461011e5781869786809481947f6b8f723a4c7a5335cafae8a598a0aa0301be1387c037dccc085b62add6448b209a92610111575b50508360011b906000198560031b1c19161789555b855196858896875286015201848401378181018301869052601f01601f19168101030190a180f35b83010135905082386100d4565b858052957f290decd9548b62a8d60345a988386fc84ba6bc95484008f6362f93160ef3e563601f198616875b8181106101ad5750918693917f6b8f723a4c7a5335cafae8a598a0aa0301be1387c037dccc085b62add6448b20989985809510610191575b5050600183811b0189556100e9565b8201830135600019600386901b60f8161c191690553880610182565b838a01850135835598850198600190920191850161014a565b8580527f290decd9548b62a8d60345a988386fc84ba6bc95484008f6362f93160ef3e563601f860160051c81019184871061021d575b601f0160051c01905b8181106102125750610097565b868155600101610205565b90915081906101fc565b8480fd5b8380fd5b5080fd5b80fd5b839150346103705782600319360112610370578083845461025681610374565b8084529060019081811690811561035257506001146102f4575b5050601f801993849203011681019381851067ffffffffffffffff8611176102e05791849192828552602090818452845191828186015281955b8387106102c85750508394508582601f949501015201168101030190f35b868101820151898801890152958101958895506102aa565b634e487b7160e01b81526041600452602490fd5b8680529092915085907f290decd9548b62a8d60345a988386fc84ba6bc95484008f6362f93160ef3e5635b84831061033757505081602092935001018580610270565b8193509081602092548385890101520191019091849261031f565b9150506020925060ff191682840152151560051b8201018580610270565b8280fd5b90600182811c921680156103a4575b602083101461038e57565b634e487b7160e01b600052602260045260246000fd5b91607f169161038356fea26469706673582212202c513f823c9bc94fe5b562d5da7cb19bc45be82931d6ef5285ea6e1e3898004c64736f6c63430008120033",
}

// SequencerurlrollupmockABI is the input ABI used to generate the binding from.
// Deprecated: Use SequencerurlrollupmockMetaData.ABI instead.
var SequencerurlrollupmockABI = SequencerurlrollupmockMetaData.ABI

// SequencerurlrollupmockBin is the compiled bytecode used for deploying new contracts.
// Deprecated: Use SequencerurlrollupmockMetaData.Bin instead.
var SequencerurlrollupmockBin = SequencerurlrollupmockMetaData.Bin

// DeploySequencerurlrollupmock deploys a new Ethereum contract, binding an instance of Sequencerurlrollupmock to it.
func DeploySequencerurlrollupmock(auth *bind.TransactOpts, backend bind.ContractBackend, initialSequencerURL string) (common.Address, *types.Transaction, *Sequencerurlrollupmock, error) {
	parsed, err := SequencerurlrollupmockMetaData.GetAbi()
	if err != nil {
		return common.Address{}, nil, nil, err
	}
	if parsed == nil {
		return common.Address{}, nil, nil, errors.New("GetABI returned nil")
	}

	address, tx, contract, err := bind.DeployContract(auth, *parsed, common.FromHex(SequencerurlrollupmockBin), backend, initialSequencerURL)
	if err != nil {
		return common.Address{}, nil, nil, err
	}
	return address, tx, &Sequencerurlrollupmock{SequencerurlrollupmockCaller: SequencerurlrollupmockCaller{contract: contract}, SequencerurlrollupmockTransactor: SequencerurlrollupmockTransactor{contract: contract}, SequencerurlrollupmockFilterer: SequencerurlrollupmockFilterer{contract: contract}}, nil
}

// Sequencerurlrollupmock is an auto generated Go binding around an Ethereum contract.
type Sequencerurlrollupmock struct {
	SequencerurlrollupmockCaller     // Read-only binding to the contract
	SequencerurlrollupmockTransactor // Write-only binding to the contract
	SequencerurlrollupmockFilterer   // Log filterer for contract events
}

// SequencerurlrollupmockCaller is an auto generated read-only Go binding around an Ethereum contract.
type SequencerurlrollupmockCaller struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// SequencerurlrollupmockTransactor is an auto generated write-only Go binding around an Ethereum contract.
type SequencerurlrollupmockTransactor struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// SequencerurlrollupmockFilterer is an auto generated log filtering Go binding around an Ethereum contract events.
type SequencerurlrollupmockFilterer struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// SequencerurlrollupmockSession is an auto generated Go binding around an Ethereum contract,
// with pre-set call and transact options.
type SequencerurlrollupmockSession struct {
	Contract     *Sequencerurlrollupmock // Generic contract binding to set the session for
	CallOpts     bind.CallOpts           // Call options to use throughout this session
	TransactOpts bind.TransactOpts       // Transaction auth options to use throughout this session
}

// SequencerurlrollupmockCallerSession is an auto generated read-only Go binding around an Ethereum contract,
// with pre-set call options.
type SequencerurlrollupmockCallerSession struct {
	Contract *SequencerurlrollupmockCaller // Generic contract caller binding to set the session for
	CallOpts bind.CallOpts                 // Call options to use throughout this session
}

// SequencerurlrollupmockTransactorSession is an auto generated write-only Go binding around an Ethereum contract,
// with pre-set transact options.
type SequencerurlrollupmockTransactorSession struct {
	Contract     *SequencerurlrollupmockTransactor // Generic contract transactor binding to set the session for
	TransactOpts bind.TransactOpts                 // Transaction auth options to use throughout this session
}

// SequencerurlrollupmockRaw is an auto generated low-level Go binding around an Ethereum contract.
type SequencerurlrollupmockRaw struct {
	Contract *Sequencerurlrollupmock // Generic contract binding to access the raw methods on
}

// SequencerurlrollupmockCallerRaw is an auto generated low-level read-only Go binding around an Ethereum contract.
type SequencerurlrollupmockCallerRaw struct {
	Contract *SequencerurlrollupmockCaller // Generic read-only contract binding to access the raw methods on
}

// SequencerurlrollupmockTransactorRaw is an auto generated low-level write-only Go binding around an Ethereum contract.
type SequencerurlrollupmockTransactorRaw struct {
	Contract *SequencerurlrollupmockTransactor // Generic write-only contract binding to access the raw methods on
}

// NewSequencerurlrollupmock creates a new instance of Sequencerurlrollupmock, bound to a specific deployed contract.
func NewSequencerurlrollupmock(address common.Address, backend bind.ContractBackend) (*Sequencerurlrollupmock, error) {
	contract, err := bindSequencerurlrollupmock(address, backend, backend, backend)
	if err != nil {
		return nil, err
	}
	return &Sequencerurlrollupmock{SequencerurlrollupmockCaller: SequencerurlrollupmockCaller{contract: contract}, SequencerurlrollupmockTransactor: SequencerurlrollupmockTransactor{contract: contract}, SequencerurlrollupmockFilterer: SequencerurlrollupmockFilterer{contract: contract}}, nil
}

// NewSequencerurlrollupmockCaller creates a new read-only instance of Sequencerurlrollupmock, bound to a specific deployed contract.
func NewSequencerurlrollupmockCaller(address common.Address, caller bind.ContractCaller) (*SequencerurlrollupmockCaller, error) {
	contract, err := bindSequencerurlrollupmock(address, caller, nil, nil)
	if err != nil {
		return nil, err
	}
	return &SequencerurlrollupmockCaller{contract: contract}, nil
}

// NewSequencerurlrollupmockTransactor creates a new write-only instance of Sequencerurlrollupmock, bound to a specific deployed contract.
func NewSequencerurlrollupmockTransactor(address common.Address, transactor bind.ContractTransactor) (*SequencerurlrollupmockTransactor, error) {
	contract, err := bindSequencerurlrollupmock(address, nil, transactor, nil)
	if err != nil {
		return nil, err
	}
	return &SequencerurlrollupmockTransactor{contract: contract}, nil
}

// NewSequencerurlrollupmockFilterer creates a new log filterer instance of Sequencerurlrollupmock, bound to a specific deployed contract.
func NewSequencerurlrollupmockFilterer(address common.Address, filterer bind.ContractFilterer) (*SequencerurlrollupmockFilterer, error) {
	contract, err := bindSequencerurlrollupmock(address, nil, nil, filterer)
	if err != nil {
		return nil, err
	}
	return &SequencerurlrollupmockFilterer{contract: contract}, nil
}

// bindSequencerurlrollupmock binds a generic wrapper to an already deployed contract.
func bindSequencerurlrollupmock(address common.Address, caller bind.ContractCaller, transactor bind.ContractTransactor, filterer bind.ContractFilterer) (*bind.BoundContract, error) {
	parsed, err := SequencerurlrollupmockMetaData.GetAbi()
	if err != nil {
		return nil, err
	}
	return bind.NewBoundContract(address, *parsed, caller, transactor, filterer), nil
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_Sequencerurlrollupmock *SequencerurlrollupmockRaw) Call(opts *bind.CallOpts, result *[]interface{}, method string, params ...interface{}) error {
	return _Sequencerurlrollupmock.Contract.SequencerurlrollupmockCaller.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_Sequencerurlrollupmock *SequencerurlrollupmockRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _Sequencerurlrollupmock.Contract.SequencerurlrollupmockTransactor.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_Sequencerurlrollupmock *SequencerurlrollupmockRaw) Transact(opts *bind.TransactOpts, method string, params ...interface{}) (*types.Transaction, error) {
	return _Sequencerurlrollupmock.Contract.SequencerurlrollupmockTransactor.contract.Transact(opts, method, params...)
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_Sequencerurlrollupmock *SequencerurlrollupmockCallerRaw) Call(opts *bind.CallOpts, result *[]interface{}, method string, params ...interface{}) error {
	return _Sequencerurlrollupmock.Contract.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_Sequencerurlrollupmock *SequencerurlrollupmockTransactorRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _Sequencerurlrollupmock.Contract.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_Sequencerurlrollupmock *SequencerurlrollupmockTransactorRaw) Transact(opts *bind.TransactOpts, method string, params ...interface{}) (*types.Transaction, error) {
	return _Sequencerurlrollupmock.Contract.contract.Transact(opts, method, params...)
}

// TrustedSequencerURL is a free data retrieval call binding the contract method 0x542028d5.
//
// Solidity: function trustedSequencerURL() view returns(string)
func (_Sequencerurlrollupmock *SequencerurlrollupmockCaller) TrustedSequencerURL(opts *bind.CallOpts) (string, error) {
	var out []interface{}
	err := _Sequencerurlrollupmock.contract.Call(opts, &out, "trustedSequencerURL")

	if err != nil {
		return *new(string), err
	}

	out0 := *abi.ConvertType(out[0], new(string)).(*string)

	return out0, err

}

// TrustedSequencerURL is a free data retrieval call binding the contract method 0x542028d5.
//
// Solidity: function trustedSequencerURL() view returns(string)
func (_Sequencerurlrollupmock *SequencerurlrollupmockSession) TrustedSequencerURL() (string, error) {
	return _Sequencerurlrollupmock.Contract.TrustedSequencerURL(&_Sequencerurlrollupmock.CallOpts)
}

// TrustedSequencerURL is a free data retrieval call binding the contract method 0x542028d5.
//
// Solidity: function trustedSequencerURL() view returns(string)
func (_Sequencerurlrollupmock *SequencerurlrollupmockCallerSession) TrustedSequencerURL() (string, error) {
	return _Sequencerurlrollupmock.Contract.TrustedSequencerURL(&_Sequencerurlrollupmock.CallOpts)
}

// SetTrustedSequencerURL is a paid mutator transaction binding the contract method 0xc89e42df.
//
// Solidity: function setTrustedSequencerURL(string newTrustedSequencerURL) returns()
func (_Sequencerurlrollupmock *SequencerurlrollupmockTransactor) SetTrustedSequencerURL(opts *bind.TransactOpts, newTrustedSequencerURL string) (*types.Transaction, error) {
	return _Sequencerurlrollupmock.contract.Transact(opts, "setTrustedSequencerURL", newTrustedSequencerURL)
}

// SetTrustedSequencerURL is a paid mutator transaction binding the contract method 0xc89e42df.
//
// Solidity: function setTrustedSequencerURL(string newTrustedSequencerURL) returns()
func (_Sequencerurlrollupmock *SequencerurlrollupmockSession) SetTrustedSequencerURL(newTrustedSequencerURL string) (*types.Transaction, error) {
	return _Sequencerurlrollupmock.Contract.SetTrustedSequencerURL(&_Sequencerurlrollupmock.TransactOpts, newTrustedSequencerURL)
}

// SetTrustedSequencerURL is a paid mutator transaction binding the contract method 0xc89e42df.
//
// Solidity: function setTrustedSequencerURL(string newTrustedSequencerURL) returns()
func (_Sequencerurlrollupmock *SequencerurlrollupmockTransactorSession) SetTrustedSequencerURL(newTrustedSequencerURL string) (*types.Transaction, error) {
	return _Sequencerurlrollupmock.Contract.SetTrustedSequencerURL(&_Sequencerurlrollupmock.TransactOpts, newTrustedSequencerURL)
}

// SequencerurlrollupmockSetTrustedSequencerURLIterator is returned from FilterSetTrustedSequencerURL and is used to iterate over the raw logs and unpacked data for SetTrustedSequencerURL events raised by the Sequencerurlrollupmock contract.
type SequencerurlrollupmockSetTrustedSequencerURLIterator struct {
	Event *SequencerurlrollupmockSetTrustedSequencerURL // Event containing the contract specifics and raw log

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
func (it *SequencerurlrollupmockSetTrustedSequencerURLIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(SequencerurlrollupmockSetTrustedSequencerURL)
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
		it.Event = new(SequencerurlrollupmockSetTrustedSequencerURL)
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
func (it *SequencerurlrollupmockSetTrustedSequencerURLIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *SequencerurlrollupmockSetTrustedSequencerURLIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// SequencerurlrollupmockSetTrustedSequencerURL represents a SetTrustedSequencerURL event raised by the Sequencerurlrollupmock contract.
type SequencerurlrollupmockSetTrustedSequencerURL struct {
	NewTrustedSequencerURL string
	Raw                    types.Log // Blockchain specific contextual infos
}

// FilterSetTrustedSequencerURL is a free log retrieval operation binding the contract event 0x6b8f723a4c7a5335cafae8a598a0aa0301be1387c037dccc085b62add6448b20.
//
// Solidity: event SetTrustedSequencerURL(string newTrustedSequencerURL)
func (_Sequencerurlrollupmock *SequencerurlrollupmockFilterer) FilterSetTrustedSequencerURL(opts *bind.FilterOpts) (*SequencerurlrollupmockSetTrustedSequencerURLIterator, error) {

	logs, sub, err := _Sequencerurlrollupmock.contract.FilterLogs(opts, "SetTrustedSequencerURL")
	if err != nil {
		return nil, err
	}
	return &SequencerurlrollupmockSetTrustedSequencerURLIterator{contract: _Sequencerurlrollupmock.contract, event: "SetTrustedSequencerURL", logs: logs, sub: sub}, nil
}

// WatchSetTrustedSequencerURL is a free log subscription operation binding the contract event 0x6b8f723a4c7a5335cafae8a598a0aa0301be1387c037dccc085b62add6448b20.
//
// Solidity: event SetTrustedSequencerURL(string newTrustedSequencerURL)
func (_Sequencerurlrollupmock *SequencerurlrollupmockFilterer) WatchSetTrustedSequencerURL(opts *bind.WatchOpts, sink chan<- *SequencerurlrollupmockSetTrustedSequencerURL) (event.Subscription, error) {

	logs, sub, err := _Sequencerurlrollupmock.contract.WatchLogs(opts, "SetTrustedSequencerURL")
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(SequencerurlrollupmockSetTrustedSequencerURL)
				if err := _Sequencerurlrollupmock.contract.UnpackLog(event, "SetTrustedSequencerURL", log); err != nil {
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
func (_Sequencerurlrollupmock *SequencerurlrollupmockFilterer) ParseSetTrustedSequencerURL(log types.Log) (*SequencerurlrollupmockSetTrustedSequencerURL, error) {
	event := new(SequencerurlrollupmockSetTrustedSequencerURL)
	if err := _Sequencerurlrollupmock.contract.UnpackLog(event, "SetTrustedSequencerURL", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}
