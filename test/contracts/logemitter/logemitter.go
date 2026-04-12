// Code generated - DO NOT EDIT.
// This file is a generated binding and any manual changes will be lost.

package logemitter

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

// LogemitterMetaData contains all meta data concerning the Logemitter contract.
var LogemitterMetaData = &bind.MetaData{
	ABI: "[{\"inputs\":[{\"internalType\":\"string\",\"name\":\"bootMessage\",\"type\":\"string\"}],\"stateMutability\":\"nonpayable\",\"type\":\"constructor\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":true,\"internalType\":\"address\",\"name\":\"from\",\"type\":\"address\"},{\"indexed\":true,\"internalType\":\"bytes32\",\"name\":\"topic\",\"type\":\"bytes32\"},{\"indexed\":false,\"internalType\":\"bytes\",\"name\":\"data\",\"type\":\"bytes\"}],\"name\":\"Data\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":true,\"internalType\":\"address\",\"name\":\"from\",\"type\":\"address\"},{\"indexed\":true,\"internalType\":\"uint256\",\"name\":\"id\",\"type\":\"uint256\"},{\"indexed\":false,\"internalType\":\"string\",\"name\":\"message\",\"type\":\"string\"}],\"name\":\"Ping\",\"type\":\"event\"},{\"inputs\":[],\"name\":\"counter\",\"outputs\":[{\"internalType\":\"uint256\",\"name\":\"\",\"type\":\"uint256\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"bytes32\",\"name\":\"topic\",\"type\":\"bytes32\"},{\"internalType\":\"bytes\",\"name\":\"data\",\"type\":\"bytes\"}],\"name\":\"emitData\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"uint256\",\"name\":\"id\",\"type\":\"uint256\"},{\"internalType\":\"string\",\"name\":\"message\",\"type\":\"string\"}],\"name\":\"emitPing\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"}]",
	Bin: "0x6080604052346100e05761033b80380380610019816100fb565b9283398101602080838303126100e05782516001600160401b03938482116100e057019082601f830112156100e05781519384116100e557601f1993610065601f8201861683016100fb565b818152828101948383860101116100e0576000956100a960409387867f70b9fa9db7248779b82f3212f84983f03b8f0b0df01c3e83a8c642df6897002a9801610120565b6100c58351948593818552519788809286015285850190610120565b601f339601168101030190a36040516101f790816101448239f35b600080fd5b634e487b7160e01b600052604160045260246000fd5b6040519190601f01601f191682016001600160401b038111838210176100e557604052565b60005b8381106101335750506000910152565b818101518382015260200161012356fe60808060405260048036101561001457600080fd5b600091823560e01c90816361bc221a14610153575080638b692c37146100c05763e85f05f21461004357600080fd5b346100bc5760403660031901126100bc5760243567ffffffffffffffff81116100b8577f5046ba6c1f270fb52212c8d175bba9a2f32035c54f076818682099b666acf9b26100976100b2923690850161016d565b929093604051918291602083523595339560208401916101a0565b0390a380f35b8280fd5b5080fd5b50346100bc5760403660031901126100bc5760243567ffffffffffffffff81116100b8576100f1903690830161016d565b9091835460018101809111610140577f70b9fa9db7248779b82f3212f84983f03b8f0b0df01c3e83a8c642df6897002a916100b2918655604051918291602083523595339560208401916101a0565b634e487b7160e01b855260118252602485fd5b8390346100bc57816003193601126100bc57602091548152f35b9181601f8401121561019b5782359167ffffffffffffffff831161019b576020838186019501011161019b57565b600080fd5b908060209392818452848401376000828201840152601f01601f191601019056fea26469706673582212200fe8d91d7cb5850d0d16712aa2af488fa7d2240ee843a08e90d8fc7ba83ddc3d64736f6c63430008120033",
}

// LogemitterABI is the input ABI used to generate the binding from.
// Deprecated: Use LogemitterMetaData.ABI instead.
var LogemitterABI = LogemitterMetaData.ABI

// LogemitterBin is the compiled bytecode used for deploying new contracts.
// Deprecated: Use LogemitterMetaData.Bin instead.
var LogemitterBin = LogemitterMetaData.Bin

// DeployLogemitter deploys a new Ethereum contract, binding an instance of Logemitter to it.
func DeployLogemitter(auth *bind.TransactOpts, backend bind.ContractBackend, bootMessage string) (common.Address, *types.Transaction, *Logemitter, error) {
	parsed, err := LogemitterMetaData.GetAbi()
	if err != nil {
		return common.Address{}, nil, nil, err
	}
	if parsed == nil {
		return common.Address{}, nil, nil, errors.New("GetABI returned nil")
	}

	address, tx, contract, err := bind.DeployContract(auth, *parsed, common.FromHex(LogemitterBin), backend, bootMessage)
	if err != nil {
		return common.Address{}, nil, nil, err
	}
	return address, tx, &Logemitter{LogemitterCaller: LogemitterCaller{contract: contract}, LogemitterTransactor: LogemitterTransactor{contract: contract}, LogemitterFilterer: LogemitterFilterer{contract: contract}}, nil
}

// Logemitter is an auto generated Go binding around an Ethereum contract.
type Logemitter struct {
	LogemitterCaller     // Read-only binding to the contract
	LogemitterTransactor // Write-only binding to the contract
	LogemitterFilterer   // Log filterer for contract events
}

// LogemitterCaller is an auto generated read-only Go binding around an Ethereum contract.
type LogemitterCaller struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// LogemitterTransactor is an auto generated write-only Go binding around an Ethereum contract.
type LogemitterTransactor struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// LogemitterFilterer is an auto generated log filtering Go binding around an Ethereum contract events.
type LogemitterFilterer struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// LogemitterSession is an auto generated Go binding around an Ethereum contract,
// with pre-set call and transact options.
type LogemitterSession struct {
	Contract     *Logemitter       // Generic contract binding to set the session for
	CallOpts     bind.CallOpts     // Call options to use throughout this session
	TransactOpts bind.TransactOpts // Transaction auth options to use throughout this session
}

// LogemitterCallerSession is an auto generated read-only Go binding around an Ethereum contract,
// with pre-set call options.
type LogemitterCallerSession struct {
	Contract *LogemitterCaller // Generic contract caller binding to set the session for
	CallOpts bind.CallOpts     // Call options to use throughout this session
}

// LogemitterTransactorSession is an auto generated write-only Go binding around an Ethereum contract,
// with pre-set transact options.
type LogemitterTransactorSession struct {
	Contract     *LogemitterTransactor // Generic contract transactor binding to set the session for
	TransactOpts bind.TransactOpts     // Transaction auth options to use throughout this session
}

// LogemitterRaw is an auto generated low-level Go binding around an Ethereum contract.
type LogemitterRaw struct {
	Contract *Logemitter // Generic contract binding to access the raw methods on
}

// LogemitterCallerRaw is an auto generated low-level read-only Go binding around an Ethereum contract.
type LogemitterCallerRaw struct {
	Contract *LogemitterCaller // Generic read-only contract binding to access the raw methods on
}

// LogemitterTransactorRaw is an auto generated low-level write-only Go binding around an Ethereum contract.
type LogemitterTransactorRaw struct {
	Contract *LogemitterTransactor // Generic write-only contract binding to access the raw methods on
}

// NewLogemitter creates a new instance of Logemitter, bound to a specific deployed contract.
func NewLogemitter(address common.Address, backend bind.ContractBackend) (*Logemitter, error) {
	contract, err := bindLogemitter(address, backend, backend, backend)
	if err != nil {
		return nil, err
	}
	return &Logemitter{LogemitterCaller: LogemitterCaller{contract: contract}, LogemitterTransactor: LogemitterTransactor{contract: contract}, LogemitterFilterer: LogemitterFilterer{contract: contract}}, nil
}

// NewLogemitterCaller creates a new read-only instance of Logemitter, bound to a specific deployed contract.
func NewLogemitterCaller(address common.Address, caller bind.ContractCaller) (*LogemitterCaller, error) {
	contract, err := bindLogemitter(address, caller, nil, nil)
	if err != nil {
		return nil, err
	}
	return &LogemitterCaller{contract: contract}, nil
}

// NewLogemitterTransactor creates a new write-only instance of Logemitter, bound to a specific deployed contract.
func NewLogemitterTransactor(address common.Address, transactor bind.ContractTransactor) (*LogemitterTransactor, error) {
	contract, err := bindLogemitter(address, nil, transactor, nil)
	if err != nil {
		return nil, err
	}
	return &LogemitterTransactor{contract: contract}, nil
}

// NewLogemitterFilterer creates a new log filterer instance of Logemitter, bound to a specific deployed contract.
func NewLogemitterFilterer(address common.Address, filterer bind.ContractFilterer) (*LogemitterFilterer, error) {
	contract, err := bindLogemitter(address, nil, nil, filterer)
	if err != nil {
		return nil, err
	}
	return &LogemitterFilterer{contract: contract}, nil
}

// bindLogemitter binds a generic wrapper to an already deployed contract.
func bindLogemitter(address common.Address, caller bind.ContractCaller, transactor bind.ContractTransactor, filterer bind.ContractFilterer) (*bind.BoundContract, error) {
	parsed, err := LogemitterMetaData.GetAbi()
	if err != nil {
		return nil, err
	}
	return bind.NewBoundContract(address, *parsed, caller, transactor, filterer), nil
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_Logemitter *LogemitterRaw) Call(opts *bind.CallOpts, result *[]interface{}, method string, params ...interface{}) error {
	return _Logemitter.Contract.LogemitterCaller.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_Logemitter *LogemitterRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _Logemitter.Contract.LogemitterTransactor.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_Logemitter *LogemitterRaw) Transact(opts *bind.TransactOpts, method string, params ...interface{}) (*types.Transaction, error) {
	return _Logemitter.Contract.LogemitterTransactor.contract.Transact(opts, method, params...)
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_Logemitter *LogemitterCallerRaw) Call(opts *bind.CallOpts, result *[]interface{}, method string, params ...interface{}) error {
	return _Logemitter.Contract.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_Logemitter *LogemitterTransactorRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _Logemitter.Contract.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_Logemitter *LogemitterTransactorRaw) Transact(opts *bind.TransactOpts, method string, params ...interface{}) (*types.Transaction, error) {
	return _Logemitter.Contract.contract.Transact(opts, method, params...)
}

// Counter is a free data retrieval call binding the contract method 0x61bc221a.
//
// Solidity: function counter() view returns(uint256)
func (_Logemitter *LogemitterCaller) Counter(opts *bind.CallOpts) (*big.Int, error) {
	var out []interface{}
	err := _Logemitter.contract.Call(opts, &out, "counter")

	if err != nil {
		return *new(*big.Int), err
	}

	out0 := *abi.ConvertType(out[0], new(*big.Int)).(**big.Int)

	return out0, err

}

// Counter is a free data retrieval call binding the contract method 0x61bc221a.
//
// Solidity: function counter() view returns(uint256)
func (_Logemitter *LogemitterSession) Counter() (*big.Int, error) {
	return _Logemitter.Contract.Counter(&_Logemitter.CallOpts)
}

// Counter is a free data retrieval call binding the contract method 0x61bc221a.
//
// Solidity: function counter() view returns(uint256)
func (_Logemitter *LogemitterCallerSession) Counter() (*big.Int, error) {
	return _Logemitter.Contract.Counter(&_Logemitter.CallOpts)
}

// EmitData is a paid mutator transaction binding the contract method 0xe85f05f2.
//
// Solidity: function emitData(bytes32 topic, bytes data) returns()
func (_Logemitter *LogemitterTransactor) EmitData(opts *bind.TransactOpts, topic [32]byte, data []byte) (*types.Transaction, error) {
	return _Logemitter.contract.Transact(opts, "emitData", topic, data)
}

// EmitData is a paid mutator transaction binding the contract method 0xe85f05f2.
//
// Solidity: function emitData(bytes32 topic, bytes data) returns()
func (_Logemitter *LogemitterSession) EmitData(topic [32]byte, data []byte) (*types.Transaction, error) {
	return _Logemitter.Contract.EmitData(&_Logemitter.TransactOpts, topic, data)
}

// EmitData is a paid mutator transaction binding the contract method 0xe85f05f2.
//
// Solidity: function emitData(bytes32 topic, bytes data) returns()
func (_Logemitter *LogemitterTransactorSession) EmitData(topic [32]byte, data []byte) (*types.Transaction, error) {
	return _Logemitter.Contract.EmitData(&_Logemitter.TransactOpts, topic, data)
}

// EmitPing is a paid mutator transaction binding the contract method 0x8b692c37.
//
// Solidity: function emitPing(uint256 id, string message) returns()
func (_Logemitter *LogemitterTransactor) EmitPing(opts *bind.TransactOpts, id *big.Int, message string) (*types.Transaction, error) {
	return _Logemitter.contract.Transact(opts, "emitPing", id, message)
}

// EmitPing is a paid mutator transaction binding the contract method 0x8b692c37.
//
// Solidity: function emitPing(uint256 id, string message) returns()
func (_Logemitter *LogemitterSession) EmitPing(id *big.Int, message string) (*types.Transaction, error) {
	return _Logemitter.Contract.EmitPing(&_Logemitter.TransactOpts, id, message)
}

// EmitPing is a paid mutator transaction binding the contract method 0x8b692c37.
//
// Solidity: function emitPing(uint256 id, string message) returns()
func (_Logemitter *LogemitterTransactorSession) EmitPing(id *big.Int, message string) (*types.Transaction, error) {
	return _Logemitter.Contract.EmitPing(&_Logemitter.TransactOpts, id, message)
}

// LogemitterDataIterator is returned from FilterData and is used to iterate over the raw logs and unpacked data for Data events raised by the Logemitter contract.
type LogemitterDataIterator struct {
	Event *LogemitterData // Event containing the contract specifics and raw log

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
func (it *LogemitterDataIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(LogemitterData)
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
		it.Event = new(LogemitterData)
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
func (it *LogemitterDataIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *LogemitterDataIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// LogemitterData represents a Data event raised by the Logemitter contract.
type LogemitterData struct {
	From  common.Address
	Topic [32]byte
	Data  []byte
	Raw   types.Log // Blockchain specific contextual infos
}

// FilterData is a free log retrieval operation binding the contract event 0x5046ba6c1f270fb52212c8d175bba9a2f32035c54f076818682099b666acf9b2.
//
// Solidity: event Data(address indexed from, bytes32 indexed topic, bytes data)
func (_Logemitter *LogemitterFilterer) FilterData(opts *bind.FilterOpts, from []common.Address, topic [][32]byte) (*LogemitterDataIterator, error) {

	var fromRule []interface{}
	for _, fromItem := range from {
		fromRule = append(fromRule, fromItem)
	}
	var topicRule []interface{}
	for _, topicItem := range topic {
		topicRule = append(topicRule, topicItem)
	}

	logs, sub, err := _Logemitter.contract.FilterLogs(opts, "Data", fromRule, topicRule)
	if err != nil {
		return nil, err
	}
	return &LogemitterDataIterator{contract: _Logemitter.contract, event: "Data", logs: logs, sub: sub}, nil
}

// WatchData is a free log subscription operation binding the contract event 0x5046ba6c1f270fb52212c8d175bba9a2f32035c54f076818682099b666acf9b2.
//
// Solidity: event Data(address indexed from, bytes32 indexed topic, bytes data)
func (_Logemitter *LogemitterFilterer) WatchData(opts *bind.WatchOpts, sink chan<- *LogemitterData, from []common.Address, topic [][32]byte) (event.Subscription, error) {

	var fromRule []interface{}
	for _, fromItem := range from {
		fromRule = append(fromRule, fromItem)
	}
	var topicRule []interface{}
	for _, topicItem := range topic {
		topicRule = append(topicRule, topicItem)
	}

	logs, sub, err := _Logemitter.contract.WatchLogs(opts, "Data", fromRule, topicRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(LogemitterData)
				if err := _Logemitter.contract.UnpackLog(event, "Data", log); err != nil {
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

// ParseData is a log parse operation binding the contract event 0x5046ba6c1f270fb52212c8d175bba9a2f32035c54f076818682099b666acf9b2.
//
// Solidity: event Data(address indexed from, bytes32 indexed topic, bytes data)
func (_Logemitter *LogemitterFilterer) ParseData(log types.Log) (*LogemitterData, error) {
	event := new(LogemitterData)
	if err := _Logemitter.contract.UnpackLog(event, "Data", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// LogemitterPingIterator is returned from FilterPing and is used to iterate over the raw logs and unpacked data for Ping events raised by the Logemitter contract.
type LogemitterPingIterator struct {
	Event *LogemitterPing // Event containing the contract specifics and raw log

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
func (it *LogemitterPingIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(LogemitterPing)
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
		it.Event = new(LogemitterPing)
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
func (it *LogemitterPingIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *LogemitterPingIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// LogemitterPing represents a Ping event raised by the Logemitter contract.
type LogemitterPing struct {
	From    common.Address
	Id      *big.Int
	Message string
	Raw     types.Log // Blockchain specific contextual infos
}

// FilterPing is a free log retrieval operation binding the contract event 0x70b9fa9db7248779b82f3212f84983f03b8f0b0df01c3e83a8c642df6897002a.
//
// Solidity: event Ping(address indexed from, uint256 indexed id, string message)
func (_Logemitter *LogemitterFilterer) FilterPing(opts *bind.FilterOpts, from []common.Address, id []*big.Int) (*LogemitterPingIterator, error) {

	var fromRule []interface{}
	for _, fromItem := range from {
		fromRule = append(fromRule, fromItem)
	}
	var idRule []interface{}
	for _, idItem := range id {
		idRule = append(idRule, idItem)
	}

	logs, sub, err := _Logemitter.contract.FilterLogs(opts, "Ping", fromRule, idRule)
	if err != nil {
		return nil, err
	}
	return &LogemitterPingIterator{contract: _Logemitter.contract, event: "Ping", logs: logs, sub: sub}, nil
}

// WatchPing is a free log subscription operation binding the contract event 0x70b9fa9db7248779b82f3212f84983f03b8f0b0df01c3e83a8c642df6897002a.
//
// Solidity: event Ping(address indexed from, uint256 indexed id, string message)
func (_Logemitter *LogemitterFilterer) WatchPing(opts *bind.WatchOpts, sink chan<- *LogemitterPing, from []common.Address, id []*big.Int) (event.Subscription, error) {

	var fromRule []interface{}
	for _, fromItem := range from {
		fromRule = append(fromRule, fromItem)
	}
	var idRule []interface{}
	for _, idItem := range id {
		idRule = append(idRule, idItem)
	}

	logs, sub, err := _Logemitter.contract.WatchLogs(opts, "Ping", fromRule, idRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(LogemitterPing)
				if err := _Logemitter.contract.UnpackLog(event, "Ping", log); err != nil {
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

// ParsePing is a log parse operation binding the contract event 0x70b9fa9db7248779b82f3212f84983f03b8f0b0df01c3e83a8c642df6897002a.
//
// Solidity: event Ping(address indexed from, uint256 indexed id, string message)
func (_Logemitter *LogemitterFilterer) ParsePing(log types.Log) (*LogemitterPing, error) {
	event := new(LogemitterPing)
	if err := _Logemitter.contract.UnpackLog(event, "Ping", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}
