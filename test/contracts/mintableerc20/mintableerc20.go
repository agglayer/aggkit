// Code generated - DO NOT EDIT.
// This file is a generated binding and any manual changes will be lost.

package mintableerc20

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

// Mintableerc20MetaData contains all meta data concerning the Mintableerc20 contract.
var Mintableerc20MetaData = &bind.MetaData{
	ABI: "[{\"inputs\":[{\"internalType\":\"string\",\"name\":\"_name\",\"type\":\"string\"},{\"internalType\":\"string\",\"name\":\"_symbol\",\"type\":\"string\"}],\"stateMutability\":\"nonpayable\",\"type\":\"constructor\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":true,\"internalType\":\"address\",\"name\":\"owner\",\"type\":\"address\"},{\"indexed\":true,\"internalType\":\"address\",\"name\":\"spender\",\"type\":\"address\"},{\"indexed\":false,\"internalType\":\"uint256\",\"name\":\"value\",\"type\":\"uint256\"}],\"name\":\"Approval\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":true,\"internalType\":\"address\",\"name\":\"from\",\"type\":\"address\"},{\"indexed\":true,\"internalType\":\"address\",\"name\":\"to\",\"type\":\"address\"},{\"indexed\":false,\"internalType\":\"uint256\",\"name\":\"value\",\"type\":\"uint256\"}],\"name\":\"Transfer\",\"type\":\"event\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"\",\"type\":\"address\"},{\"internalType\":\"address\",\"name\":\"\",\"type\":\"address\"}],\"name\":\"allowance\",\"outputs\":[{\"internalType\":\"uint256\",\"name\":\"\",\"type\":\"uint256\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"spender\",\"type\":\"address\"},{\"internalType\":\"uint256\",\"name\":\"amount\",\"type\":\"uint256\"}],\"name\":\"approve\",\"outputs\":[{\"internalType\":\"bool\",\"name\":\"\",\"type\":\"bool\"}],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"\",\"type\":\"address\"}],\"name\":\"balanceOf\",\"outputs\":[{\"internalType\":\"uint256\",\"name\":\"\",\"type\":\"uint256\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[],\"name\":\"decimals\",\"outputs\":[{\"internalType\":\"uint8\",\"name\":\"\",\"type\":\"uint8\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"to\",\"type\":\"address\"},{\"internalType\":\"uint256\",\"name\":\"amount\",\"type\":\"uint256\"}],\"name\":\"mint\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[],\"name\":\"name\",\"outputs\":[{\"internalType\":\"string\",\"name\":\"\",\"type\":\"string\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[],\"name\":\"symbol\",\"outputs\":[{\"internalType\":\"string\",\"name\":\"\",\"type\":\"string\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[],\"name\":\"totalSupply\",\"outputs\":[{\"internalType\":\"uint256\",\"name\":\"\",\"type\":\"uint256\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"to\",\"type\":\"address\"},{\"internalType\":\"uint256\",\"name\":\"amount\",\"type\":\"uint256\"}],\"name\":\"transfer\",\"outputs\":[{\"internalType\":\"bool\",\"name\":\"\",\"type\":\"bool\"}],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"from\",\"type\":\"address\"},{\"internalType\":\"address\",\"name\":\"to\",\"type\":\"address\"},{\"internalType\":\"uint256\",\"name\":\"amount\",\"type\":\"uint256\"}],\"name\":\"transferFrom\",\"outputs\":[{\"internalType\":\"bool\",\"name\":\"\",\"type\":\"bool\"}],\"stateMutability\":\"nonpayable\",\"type\":\"function\"}]",
	Bin: "0x60806040523480156200001157600080fd5b50604051620009763803806200097683398101604081905262000034916200011f565b600062000042838262000218565b50600162000051828262000218565b505050620002e4565b634e487b7160e01b600052604160045260246000fd5b600082601f8301126200008257600080fd5b81516001600160401b03808211156200009f576200009f6200005a565b604051601f8301601f19908116603f01168101908282118183101715620000ca57620000ca6200005a565b81604052838152602092508683858801011115620000e757600080fd5b600091505b838210156200010b5785820183015181830184015290820190620000ec565b600093810190920192909252949350505050565b600080604083850312156200013357600080fd5b82516001600160401b03808211156200014b57600080fd5b620001598683870162000070565b935060208501519150808211156200017057600080fd5b506200017f8582860162000070565b9150509250929050565b600181811c908216806200019e57607f821691505b602082108103620001bf57634e487b7160e01b600052602260045260246000fd5b50919050565b601f8211156200021357600081815260208120601f850160051c81016020861015620001ee5750805b601f850160051c820191505b818110156200020f57828155600101620001fa565b5050505b505050565b81516001600160401b038111156200023457620002346200005a565b6200024c8162000245845462000189565b84620001c5565b602080601f8311600181146200028457600084156200026b5750858301515b600019600386901b1c1916600185901b1785556200020f565b600085815260208120601f198616915b82811015620002b55788860151825594840194600190910190840162000294565b5085821015620002d45787850151600019600388901b60f8161c191681555b5050505050600190811b01905550565b61068280620002f46000396000f3fe608060405234801561001057600080fd5b506004361061009e5760003560e01c806340c10f191161006657806340c10f191461012857806370a082311461013d57806395d89b411461015d578063a9059cbb14610165578063dd62ed3e1461017857600080fd5b806306fdde03146100a3578063095ea7b3146100c157806318160ddd146100e457806323b872dd146100fb578063313ce5671461010e575b600080fd5b6100ab6101a3565b6040516100b891906104b1565b60405180910390f35b6100d46100cf36600461051b565b610231565b60405190151581526020016100b8565b6100ed60025481565b6040519081526020016100b8565b6100d4610109366004610545565b61029e565b610116601281565b60405160ff90911681526020016100b8565b61013b61013636600461051b565b61038b565b005b6100ed61014b366004610581565b60036020526000908152604090205481565b6100ab610414565b6100d461017336600461051b565b610421565b6100ed6101863660046105a3565b600460209081526000928352604080842090915290825290205481565b600080546101b0906105d6565b80601f01602080910402602001604051908101604052809291908181526020018280546101dc906105d6565b80156102295780601f106101fe57610100808354040283529160200191610229565b820191906000526020600020905b81548152906001019060200180831161020c57829003601f168201915b505050505081565b3360008181526004602090815260408083206001600160a01b038716808552925280832085905551919290917f8c5be1e5ebec7d5bd14f71427d1e84f3dd0314c0f7b2291e5b200ac8c7c3b9259061028c9086815260200190565b60405180910390a35060015b92915050565b6001600160a01b03831660009081526004602090815260408083203384529091528120805483919083906102d3908490610626565b90915550506001600160a01b03841660009081526003602052604081208054849290610300908490610626565b90915550506001600160a01b0383166000908152600360205260408120805484929061032d908490610639565b92505081905550826001600160a01b0316846001600160a01b03167fddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef8460405161037991815260200190565b60405180910390a35060019392505050565b806002600082825461039d9190610639565b90915550506001600160a01b038216600090815260036020526040812080548392906103ca908490610639565b90915550506040518181526001600160a01b038316906000907fddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef9060200160405180910390a35050565b600180546101b0906105d6565b33600090815260036020526040812080548391908390610442908490610626565b90915550506001600160a01b0383166000908152600360205260408120805484929061046f908490610639565b90915550506040518281526001600160a01b0384169033907fddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef9060200161028c565b600060208083528351808285015260005b818110156104de578581018301518582016040015282016104c2565b506000604082860101526040601f19601f8301168501019250505092915050565b80356001600160a01b038116811461051657600080fd5b919050565b6000806040838503121561052e57600080fd5b610537836104ff565b946020939093013593505050565b60008060006060848603121561055a57600080fd5b610563846104ff565b9250610571602085016104ff565b9150604084013590509250925092565b60006020828403121561059357600080fd5b61059c826104ff565b9392505050565b600080604083850312156105b657600080fd5b6105bf836104ff565b91506105cd602084016104ff565b90509250929050565b600181811c908216806105ea57607f821691505b60208210810361060a57634e487b7160e01b600052602260045260246000fd5b50919050565b634e487b7160e01b600052601160045260246000fd5b8181038181111561029857610298610610565b808201808211156102985761029861061056fea26469706673582212202dd5c467bf43fd8ac18a8a61ba0d72bd972fe5578b9423a313202c31db709b5264736f6c63430008120033",
}

// Mintableerc20ABI is the input ABI used to generate the binding from.
// Deprecated: Use Mintableerc20MetaData.ABI instead.
var Mintableerc20ABI = Mintableerc20MetaData.ABI

// Mintableerc20Bin is the compiled bytecode used for deploying new contracts.
// Deprecated: Use Mintableerc20MetaData.Bin instead.
var Mintableerc20Bin = Mintableerc20MetaData.Bin

// DeployMintableerc20 deploys a new Ethereum contract, binding an instance of Mintableerc20 to it.
func DeployMintableerc20(auth *bind.TransactOpts, backend bind.ContractBackend, _name string, _symbol string) (common.Address, *types.Transaction, *Mintableerc20, error) {
	parsed, err := Mintableerc20MetaData.GetAbi()
	if err != nil {
		return common.Address{}, nil, nil, err
	}
	if parsed == nil {
		return common.Address{}, nil, nil, errors.New("GetABI returned nil")
	}

	address, tx, contract, err := bind.DeployContract(auth, *parsed, common.FromHex(Mintableerc20Bin), backend, _name, _symbol)
	if err != nil {
		return common.Address{}, nil, nil, err
	}
	return address, tx, &Mintableerc20{Mintableerc20Caller: Mintableerc20Caller{contract: contract}, Mintableerc20Transactor: Mintableerc20Transactor{contract: contract}, Mintableerc20Filterer: Mintableerc20Filterer{contract: contract}}, nil
}

// Mintableerc20 is an auto generated Go binding around an Ethereum contract.
type Mintableerc20 struct {
	Mintableerc20Caller     // Read-only binding to the contract
	Mintableerc20Transactor // Write-only binding to the contract
	Mintableerc20Filterer   // Log filterer for contract events
}

// Mintableerc20Caller is an auto generated read-only Go binding around an Ethereum contract.
type Mintableerc20Caller struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// Mintableerc20Transactor is an auto generated write-only Go binding around an Ethereum contract.
type Mintableerc20Transactor struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// Mintableerc20Filterer is an auto generated log filtering Go binding around an Ethereum contract events.
type Mintableerc20Filterer struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// Mintableerc20Session is an auto generated Go binding around an Ethereum contract,
// with pre-set call and transact options.
type Mintableerc20Session struct {
	Contract     *Mintableerc20    // Generic contract binding to set the session for
	CallOpts     bind.CallOpts     // Call options to use throughout this session
	TransactOpts bind.TransactOpts // Transaction auth options to use throughout this session
}

// Mintableerc20CallerSession is an auto generated read-only Go binding around an Ethereum contract,
// with pre-set call options.
type Mintableerc20CallerSession struct {
	Contract *Mintableerc20Caller // Generic contract caller binding to set the session for
	CallOpts bind.CallOpts        // Call options to use throughout this session
}

// Mintableerc20TransactorSession is an auto generated write-only Go binding around an Ethereum contract,
// with pre-set transact options.
type Mintableerc20TransactorSession struct {
	Contract     *Mintableerc20Transactor // Generic contract transactor binding to set the session for
	TransactOpts bind.TransactOpts        // Transaction auth options to use throughout this session
}

// Mintableerc20Raw is an auto generated low-level Go binding around an Ethereum contract.
type Mintableerc20Raw struct {
	Contract *Mintableerc20 // Generic contract binding to access the raw methods on
}

// Mintableerc20CallerRaw is an auto generated low-level read-only Go binding around an Ethereum contract.
type Mintableerc20CallerRaw struct {
	Contract *Mintableerc20Caller // Generic read-only contract binding to access the raw methods on
}

// Mintableerc20TransactorRaw is an auto generated low-level write-only Go binding around an Ethereum contract.
type Mintableerc20TransactorRaw struct {
	Contract *Mintableerc20Transactor // Generic write-only contract binding to access the raw methods on
}

// NewMintableerc20 creates a new instance of Mintableerc20, bound to a specific deployed contract.
func NewMintableerc20(address common.Address, backend bind.ContractBackend) (*Mintableerc20, error) {
	contract, err := bindMintableerc20(address, backend, backend, backend)
	if err != nil {
		return nil, err
	}
	return &Mintableerc20{Mintableerc20Caller: Mintableerc20Caller{contract: contract}, Mintableerc20Transactor: Mintableerc20Transactor{contract: contract}, Mintableerc20Filterer: Mintableerc20Filterer{contract: contract}}, nil
}

// NewMintableerc20Caller creates a new read-only instance of Mintableerc20, bound to a specific deployed contract.
func NewMintableerc20Caller(address common.Address, caller bind.ContractCaller) (*Mintableerc20Caller, error) {
	contract, err := bindMintableerc20(address, caller, nil, nil)
	if err != nil {
		return nil, err
	}
	return &Mintableerc20Caller{contract: contract}, nil
}

// NewMintableerc20Transactor creates a new write-only instance of Mintableerc20, bound to a specific deployed contract.
func NewMintableerc20Transactor(address common.Address, transactor bind.ContractTransactor) (*Mintableerc20Transactor, error) {
	contract, err := bindMintableerc20(address, nil, transactor, nil)
	if err != nil {
		return nil, err
	}
	return &Mintableerc20Transactor{contract: contract}, nil
}

// NewMintableerc20Filterer creates a new log filterer instance of Mintableerc20, bound to a specific deployed contract.
func NewMintableerc20Filterer(address common.Address, filterer bind.ContractFilterer) (*Mintableerc20Filterer, error) {
	contract, err := bindMintableerc20(address, nil, nil, filterer)
	if err != nil {
		return nil, err
	}
	return &Mintableerc20Filterer{contract: contract}, nil
}

// bindMintableerc20 binds a generic wrapper to an already deployed contract.
func bindMintableerc20(address common.Address, caller bind.ContractCaller, transactor bind.ContractTransactor, filterer bind.ContractFilterer) (*bind.BoundContract, error) {
	parsed, err := Mintableerc20MetaData.GetAbi()
	if err != nil {
		return nil, err
	}
	return bind.NewBoundContract(address, *parsed, caller, transactor, filterer), nil
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_Mintableerc20 *Mintableerc20Raw) Call(opts *bind.CallOpts, result *[]interface{}, method string, params ...interface{}) error {
	return _Mintableerc20.Contract.Mintableerc20Caller.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_Mintableerc20 *Mintableerc20Raw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _Mintableerc20.Contract.Mintableerc20Transactor.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_Mintableerc20 *Mintableerc20Raw) Transact(opts *bind.TransactOpts, method string, params ...interface{}) (*types.Transaction, error) {
	return _Mintableerc20.Contract.Mintableerc20Transactor.contract.Transact(opts, method, params...)
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_Mintableerc20 *Mintableerc20CallerRaw) Call(opts *bind.CallOpts, result *[]interface{}, method string, params ...interface{}) error {
	return _Mintableerc20.Contract.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_Mintableerc20 *Mintableerc20TransactorRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _Mintableerc20.Contract.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_Mintableerc20 *Mintableerc20TransactorRaw) Transact(opts *bind.TransactOpts, method string, params ...interface{}) (*types.Transaction, error) {
	return _Mintableerc20.Contract.contract.Transact(opts, method, params...)
}

// Allowance is a free data retrieval call binding the contract method 0xdd62ed3e.
//
// Solidity: function allowance(address , address ) view returns(uint256)
func (_Mintableerc20 *Mintableerc20Caller) Allowance(opts *bind.CallOpts, arg0 common.Address, arg1 common.Address) (*big.Int, error) {
	var out []interface{}
	err := _Mintableerc20.contract.Call(opts, &out, "allowance", arg0, arg1)

	if err != nil {
		return *new(*big.Int), err
	}

	out0 := *abi.ConvertType(out[0], new(*big.Int)).(**big.Int)

	return out0, err

}

// Allowance is a free data retrieval call binding the contract method 0xdd62ed3e.
//
// Solidity: function allowance(address , address ) view returns(uint256)
func (_Mintableerc20 *Mintableerc20Session) Allowance(arg0 common.Address, arg1 common.Address) (*big.Int, error) {
	return _Mintableerc20.Contract.Allowance(&_Mintableerc20.CallOpts, arg0, arg1)
}

// Allowance is a free data retrieval call binding the contract method 0xdd62ed3e.
//
// Solidity: function allowance(address , address ) view returns(uint256)
func (_Mintableerc20 *Mintableerc20CallerSession) Allowance(arg0 common.Address, arg1 common.Address) (*big.Int, error) {
	return _Mintableerc20.Contract.Allowance(&_Mintableerc20.CallOpts, arg0, arg1)
}

// BalanceOf is a free data retrieval call binding the contract method 0x70a08231.
//
// Solidity: function balanceOf(address ) view returns(uint256)
func (_Mintableerc20 *Mintableerc20Caller) BalanceOf(opts *bind.CallOpts, arg0 common.Address) (*big.Int, error) {
	var out []interface{}
	err := _Mintableerc20.contract.Call(opts, &out, "balanceOf", arg0)

	if err != nil {
		return *new(*big.Int), err
	}

	out0 := *abi.ConvertType(out[0], new(*big.Int)).(**big.Int)

	return out0, err

}

// BalanceOf is a free data retrieval call binding the contract method 0x70a08231.
//
// Solidity: function balanceOf(address ) view returns(uint256)
func (_Mintableerc20 *Mintableerc20Session) BalanceOf(arg0 common.Address) (*big.Int, error) {
	return _Mintableerc20.Contract.BalanceOf(&_Mintableerc20.CallOpts, arg0)
}

// BalanceOf is a free data retrieval call binding the contract method 0x70a08231.
//
// Solidity: function balanceOf(address ) view returns(uint256)
func (_Mintableerc20 *Mintableerc20CallerSession) BalanceOf(arg0 common.Address) (*big.Int, error) {
	return _Mintableerc20.Contract.BalanceOf(&_Mintableerc20.CallOpts, arg0)
}

// Decimals is a free data retrieval call binding the contract method 0x313ce567.
//
// Solidity: function decimals() view returns(uint8)
func (_Mintableerc20 *Mintableerc20Caller) Decimals(opts *bind.CallOpts) (uint8, error) {
	var out []interface{}
	err := _Mintableerc20.contract.Call(opts, &out, "decimals")

	if err != nil {
		return *new(uint8), err
	}

	out0 := *abi.ConvertType(out[0], new(uint8)).(*uint8)

	return out0, err

}

// Decimals is a free data retrieval call binding the contract method 0x313ce567.
//
// Solidity: function decimals() view returns(uint8)
func (_Mintableerc20 *Mintableerc20Session) Decimals() (uint8, error) {
	return _Mintableerc20.Contract.Decimals(&_Mintableerc20.CallOpts)
}

// Decimals is a free data retrieval call binding the contract method 0x313ce567.
//
// Solidity: function decimals() view returns(uint8)
func (_Mintableerc20 *Mintableerc20CallerSession) Decimals() (uint8, error) {
	return _Mintableerc20.Contract.Decimals(&_Mintableerc20.CallOpts)
}

// Name is a free data retrieval call binding the contract method 0x06fdde03.
//
// Solidity: function name() view returns(string)
func (_Mintableerc20 *Mintableerc20Caller) Name(opts *bind.CallOpts) (string, error) {
	var out []interface{}
	err := _Mintableerc20.contract.Call(opts, &out, "name")

	if err != nil {
		return *new(string), err
	}

	out0 := *abi.ConvertType(out[0], new(string)).(*string)

	return out0, err

}

// Name is a free data retrieval call binding the contract method 0x06fdde03.
//
// Solidity: function name() view returns(string)
func (_Mintableerc20 *Mintableerc20Session) Name() (string, error) {
	return _Mintableerc20.Contract.Name(&_Mintableerc20.CallOpts)
}

// Name is a free data retrieval call binding the contract method 0x06fdde03.
//
// Solidity: function name() view returns(string)
func (_Mintableerc20 *Mintableerc20CallerSession) Name() (string, error) {
	return _Mintableerc20.Contract.Name(&_Mintableerc20.CallOpts)
}

// Symbol is a free data retrieval call binding the contract method 0x95d89b41.
//
// Solidity: function symbol() view returns(string)
func (_Mintableerc20 *Mintableerc20Caller) Symbol(opts *bind.CallOpts) (string, error) {
	var out []interface{}
	err := _Mintableerc20.contract.Call(opts, &out, "symbol")

	if err != nil {
		return *new(string), err
	}

	out0 := *abi.ConvertType(out[0], new(string)).(*string)

	return out0, err

}

// Symbol is a free data retrieval call binding the contract method 0x95d89b41.
//
// Solidity: function symbol() view returns(string)
func (_Mintableerc20 *Mintableerc20Session) Symbol() (string, error) {
	return _Mintableerc20.Contract.Symbol(&_Mintableerc20.CallOpts)
}

// Symbol is a free data retrieval call binding the contract method 0x95d89b41.
//
// Solidity: function symbol() view returns(string)
func (_Mintableerc20 *Mintableerc20CallerSession) Symbol() (string, error) {
	return _Mintableerc20.Contract.Symbol(&_Mintableerc20.CallOpts)
}

// TotalSupply is a free data retrieval call binding the contract method 0x18160ddd.
//
// Solidity: function totalSupply() view returns(uint256)
func (_Mintableerc20 *Mintableerc20Caller) TotalSupply(opts *bind.CallOpts) (*big.Int, error) {
	var out []interface{}
	err := _Mintableerc20.contract.Call(opts, &out, "totalSupply")

	if err != nil {
		return *new(*big.Int), err
	}

	out0 := *abi.ConvertType(out[0], new(*big.Int)).(**big.Int)

	return out0, err

}

// TotalSupply is a free data retrieval call binding the contract method 0x18160ddd.
//
// Solidity: function totalSupply() view returns(uint256)
func (_Mintableerc20 *Mintableerc20Session) TotalSupply() (*big.Int, error) {
	return _Mintableerc20.Contract.TotalSupply(&_Mintableerc20.CallOpts)
}

// TotalSupply is a free data retrieval call binding the contract method 0x18160ddd.
//
// Solidity: function totalSupply() view returns(uint256)
func (_Mintableerc20 *Mintableerc20CallerSession) TotalSupply() (*big.Int, error) {
	return _Mintableerc20.Contract.TotalSupply(&_Mintableerc20.CallOpts)
}

// Approve is a paid mutator transaction binding the contract method 0x095ea7b3.
//
// Solidity: function approve(address spender, uint256 amount) returns(bool)
func (_Mintableerc20 *Mintableerc20Transactor) Approve(opts *bind.TransactOpts, spender common.Address, amount *big.Int) (*types.Transaction, error) {
	return _Mintableerc20.contract.Transact(opts, "approve", spender, amount)
}

// Approve is a paid mutator transaction binding the contract method 0x095ea7b3.
//
// Solidity: function approve(address spender, uint256 amount) returns(bool)
func (_Mintableerc20 *Mintableerc20Session) Approve(spender common.Address, amount *big.Int) (*types.Transaction, error) {
	return _Mintableerc20.Contract.Approve(&_Mintableerc20.TransactOpts, spender, amount)
}

// Approve is a paid mutator transaction binding the contract method 0x095ea7b3.
//
// Solidity: function approve(address spender, uint256 amount) returns(bool)
func (_Mintableerc20 *Mintableerc20TransactorSession) Approve(spender common.Address, amount *big.Int) (*types.Transaction, error) {
	return _Mintableerc20.Contract.Approve(&_Mintableerc20.TransactOpts, spender, amount)
}

// Mint is a paid mutator transaction binding the contract method 0x40c10f19.
//
// Solidity: function mint(address to, uint256 amount) returns()
func (_Mintableerc20 *Mintableerc20Transactor) Mint(opts *bind.TransactOpts, to common.Address, amount *big.Int) (*types.Transaction, error) {
	return _Mintableerc20.contract.Transact(opts, "mint", to, amount)
}

// Mint is a paid mutator transaction binding the contract method 0x40c10f19.
//
// Solidity: function mint(address to, uint256 amount) returns()
func (_Mintableerc20 *Mintableerc20Session) Mint(to common.Address, amount *big.Int) (*types.Transaction, error) {
	return _Mintableerc20.Contract.Mint(&_Mintableerc20.TransactOpts, to, amount)
}

// Mint is a paid mutator transaction binding the contract method 0x40c10f19.
//
// Solidity: function mint(address to, uint256 amount) returns()
func (_Mintableerc20 *Mintableerc20TransactorSession) Mint(to common.Address, amount *big.Int) (*types.Transaction, error) {
	return _Mintableerc20.Contract.Mint(&_Mintableerc20.TransactOpts, to, amount)
}

// Transfer is a paid mutator transaction binding the contract method 0xa9059cbb.
//
// Solidity: function transfer(address to, uint256 amount) returns(bool)
func (_Mintableerc20 *Mintableerc20Transactor) Transfer(opts *bind.TransactOpts, to common.Address, amount *big.Int) (*types.Transaction, error) {
	return _Mintableerc20.contract.Transact(opts, "transfer", to, amount)
}

// Transfer is a paid mutator transaction binding the contract method 0xa9059cbb.
//
// Solidity: function transfer(address to, uint256 amount) returns(bool)
func (_Mintableerc20 *Mintableerc20Session) Transfer(to common.Address, amount *big.Int) (*types.Transaction, error) {
	return _Mintableerc20.Contract.Transfer(&_Mintableerc20.TransactOpts, to, amount)
}

// Transfer is a paid mutator transaction binding the contract method 0xa9059cbb.
//
// Solidity: function transfer(address to, uint256 amount) returns(bool)
func (_Mintableerc20 *Mintableerc20TransactorSession) Transfer(to common.Address, amount *big.Int) (*types.Transaction, error) {
	return _Mintableerc20.Contract.Transfer(&_Mintableerc20.TransactOpts, to, amount)
}

// TransferFrom is a paid mutator transaction binding the contract method 0x23b872dd.
//
// Solidity: function transferFrom(address from, address to, uint256 amount) returns(bool)
func (_Mintableerc20 *Mintableerc20Transactor) TransferFrom(opts *bind.TransactOpts, from common.Address, to common.Address, amount *big.Int) (*types.Transaction, error) {
	return _Mintableerc20.contract.Transact(opts, "transferFrom", from, to, amount)
}

// TransferFrom is a paid mutator transaction binding the contract method 0x23b872dd.
//
// Solidity: function transferFrom(address from, address to, uint256 amount) returns(bool)
func (_Mintableerc20 *Mintableerc20Session) TransferFrom(from common.Address, to common.Address, amount *big.Int) (*types.Transaction, error) {
	return _Mintableerc20.Contract.TransferFrom(&_Mintableerc20.TransactOpts, from, to, amount)
}

// TransferFrom is a paid mutator transaction binding the contract method 0x23b872dd.
//
// Solidity: function transferFrom(address from, address to, uint256 amount) returns(bool)
func (_Mintableerc20 *Mintableerc20TransactorSession) TransferFrom(from common.Address, to common.Address, amount *big.Int) (*types.Transaction, error) {
	return _Mintableerc20.Contract.TransferFrom(&_Mintableerc20.TransactOpts, from, to, amount)
}

// Mintableerc20ApprovalIterator is returned from FilterApproval and is used to iterate over the raw logs and unpacked data for Approval events raised by the Mintableerc20 contract.
type Mintableerc20ApprovalIterator struct {
	Event *Mintableerc20Approval // Event containing the contract specifics and raw log

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
func (it *Mintableerc20ApprovalIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(Mintableerc20Approval)
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
		it.Event = new(Mintableerc20Approval)
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
func (it *Mintableerc20ApprovalIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *Mintableerc20ApprovalIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// Mintableerc20Approval represents a Approval event raised by the Mintableerc20 contract.
type Mintableerc20Approval struct {
	Owner   common.Address
	Spender common.Address
	Value   *big.Int
	Raw     types.Log // Blockchain specific contextual infos
}

// FilterApproval is a free log retrieval operation binding the contract event 0x8c5be1e5ebec7d5bd14f71427d1e84f3dd0314c0f7b2291e5b200ac8c7c3b925.
//
// Solidity: event Approval(address indexed owner, address indexed spender, uint256 value)
func (_Mintableerc20 *Mintableerc20Filterer) FilterApproval(opts *bind.FilterOpts, owner []common.Address, spender []common.Address) (*Mintableerc20ApprovalIterator, error) {

	var ownerRule []interface{}
	for _, ownerItem := range owner {
		ownerRule = append(ownerRule, ownerItem)
	}
	var spenderRule []interface{}
	for _, spenderItem := range spender {
		spenderRule = append(spenderRule, spenderItem)
	}

	logs, sub, err := _Mintableerc20.contract.FilterLogs(opts, "Approval", ownerRule, spenderRule)
	if err != nil {
		return nil, err
	}
	return &Mintableerc20ApprovalIterator{contract: _Mintableerc20.contract, event: "Approval", logs: logs, sub: sub}, nil
}

// WatchApproval is a free log subscription operation binding the contract event 0x8c5be1e5ebec7d5bd14f71427d1e84f3dd0314c0f7b2291e5b200ac8c7c3b925.
//
// Solidity: event Approval(address indexed owner, address indexed spender, uint256 value)
func (_Mintableerc20 *Mintableerc20Filterer) WatchApproval(opts *bind.WatchOpts, sink chan<- *Mintableerc20Approval, owner []common.Address, spender []common.Address) (event.Subscription, error) {

	var ownerRule []interface{}
	for _, ownerItem := range owner {
		ownerRule = append(ownerRule, ownerItem)
	}
	var spenderRule []interface{}
	for _, spenderItem := range spender {
		spenderRule = append(spenderRule, spenderItem)
	}

	logs, sub, err := _Mintableerc20.contract.WatchLogs(opts, "Approval", ownerRule, spenderRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(Mintableerc20Approval)
				if err := _Mintableerc20.contract.UnpackLog(event, "Approval", log); err != nil {
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

// ParseApproval is a log parse operation binding the contract event 0x8c5be1e5ebec7d5bd14f71427d1e84f3dd0314c0f7b2291e5b200ac8c7c3b925.
//
// Solidity: event Approval(address indexed owner, address indexed spender, uint256 value)
func (_Mintableerc20 *Mintableerc20Filterer) ParseApproval(log types.Log) (*Mintableerc20Approval, error) {
	event := new(Mintableerc20Approval)
	if err := _Mintableerc20.contract.UnpackLog(event, "Approval", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// Mintableerc20TransferIterator is returned from FilterTransfer and is used to iterate over the raw logs and unpacked data for Transfer events raised by the Mintableerc20 contract.
type Mintableerc20TransferIterator struct {
	Event *Mintableerc20Transfer // Event containing the contract specifics and raw log

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
func (it *Mintableerc20TransferIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(Mintableerc20Transfer)
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
		it.Event = new(Mintableerc20Transfer)
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
func (it *Mintableerc20TransferIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *Mintableerc20TransferIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// Mintableerc20Transfer represents a Transfer event raised by the Mintableerc20 contract.
type Mintableerc20Transfer struct {
	From  common.Address
	To    common.Address
	Value *big.Int
	Raw   types.Log // Blockchain specific contextual infos
}

// FilterTransfer is a free log retrieval operation binding the contract event 0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef.
//
// Solidity: event Transfer(address indexed from, address indexed to, uint256 value)
func (_Mintableerc20 *Mintableerc20Filterer) FilterTransfer(opts *bind.FilterOpts, from []common.Address, to []common.Address) (*Mintableerc20TransferIterator, error) {

	var fromRule []interface{}
	for _, fromItem := range from {
		fromRule = append(fromRule, fromItem)
	}
	var toRule []interface{}
	for _, toItem := range to {
		toRule = append(toRule, toItem)
	}

	logs, sub, err := _Mintableerc20.contract.FilterLogs(opts, "Transfer", fromRule, toRule)
	if err != nil {
		return nil, err
	}
	return &Mintableerc20TransferIterator{contract: _Mintableerc20.contract, event: "Transfer", logs: logs, sub: sub}, nil
}

// WatchTransfer is a free log subscription operation binding the contract event 0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef.
//
// Solidity: event Transfer(address indexed from, address indexed to, uint256 value)
func (_Mintableerc20 *Mintableerc20Filterer) WatchTransfer(opts *bind.WatchOpts, sink chan<- *Mintableerc20Transfer, from []common.Address, to []common.Address) (event.Subscription, error) {

	var fromRule []interface{}
	for _, fromItem := range from {
		fromRule = append(fromRule, fromItem)
	}
	var toRule []interface{}
	for _, toItem := range to {
		toRule = append(toRule, toItem)
	}

	logs, sub, err := _Mintableerc20.contract.WatchLogs(opts, "Transfer", fromRule, toRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(Mintableerc20Transfer)
				if err := _Mintableerc20.contract.UnpackLog(event, "Transfer", log); err != nil {
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

// ParseTransfer is a log parse operation binding the contract event 0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef.
//
// Solidity: event Transfer(address indexed from, address indexed to, uint256 value)
func (_Mintableerc20 *Mintableerc20Filterer) ParseTransfer(log types.Log) (*Mintableerc20Transfer, error) {
	event := new(Mintableerc20Transfer)
	if err := _Mintableerc20.contract.UnpackLog(event, "Transfer", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}
