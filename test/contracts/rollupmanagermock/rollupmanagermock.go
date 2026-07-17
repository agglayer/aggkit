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
	ABI: "[{\"anonymous\":false,\"inputs\":[{\"indexed\":true,\"internalType\":\"uint32\",\"name\":\"rollupID\",\"type\":\"uint32\"},{\"indexed\":false,\"internalType\":\"uint64\",\"name\":\"forkID\",\"type\":\"uint64\"},{\"indexed\":false,\"internalType\":\"address\",\"name\":\"rollupAddress\",\"type\":\"address\"},{\"indexed\":false,\"internalType\":\"uint64\",\"name\":\"chainID\",\"type\":\"uint64\"},{\"indexed\":false,\"internalType\":\"uint8\",\"name\":\"rollupVerifierType\",\"type\":\"uint8\"},{\"indexed\":false,\"internalType\":\"uint64\",\"name\":\"lastVerifiedBatchBeforeUpgrade\",\"type\":\"uint64\"},{\"indexed\":false,\"internalType\":\"bytes32\",\"name\":\"programVKey\",\"type\":\"bytes32\"},{\"indexed\":false,\"internalType\":\"bytes32\",\"name\":\"initPessimisticRoot\",\"type\":\"bytes32\"}],\"name\":\"AddExistingRollup\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":true,\"internalType\":\"uint32\",\"name\":\"rollupID\",\"type\":\"uint32\"},{\"indexed\":false,\"internalType\":\"uint32\",\"name\":\"rollupTypeID\",\"type\":\"uint32\"},{\"indexed\":false,\"internalType\":\"address\",\"name\":\"rollupAddress\",\"type\":\"address\"},{\"indexed\":false,\"internalType\":\"uint64\",\"name\":\"chainID\",\"type\":\"uint64\"},{\"indexed\":false,\"internalType\":\"uint8\",\"name\":\"rollupVerifierType\",\"type\":\"uint8\"},{\"indexed\":false,\"internalType\":\"bytes\",\"name\":\"initializeBytesAggchain\",\"type\":\"bytes\"}],\"name\":\"CreateNewAggchain\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":true,\"internalType\":\"uint32\",\"name\":\"rollupID\",\"type\":\"uint32\"},{\"indexed\":false,\"internalType\":\"uint32\",\"name\":\"rollupTypeID\",\"type\":\"uint32\"},{\"indexed\":false,\"internalType\":\"address\",\"name\":\"rollupAddress\",\"type\":\"address\"},{\"indexed\":false,\"internalType\":\"uint64\",\"name\":\"chainID\",\"type\":\"uint64\"},{\"indexed\":false,\"internalType\":\"address\",\"name\":\"gasTokenAddress\",\"type\":\"address\"}],\"name\":\"CreateNewRollup\",\"type\":\"event\"},{\"inputs\":[{\"internalType\":\"uint32\",\"name\":\"rollupID\",\"type\":\"uint32\"},{\"internalType\":\"address\",\"name\":\"rollupContractAddr\",\"type\":\"address\"}],\"name\":\"emitAddExistingRollup\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"uint32\",\"name\":\"rollupID\",\"type\":\"uint32\"},{\"internalType\":\"address\",\"name\":\"rollupContractAddr\",\"type\":\"address\"}],\"name\":\"emitCreateNewAggchain\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"uint32\",\"name\":\"rollupID\",\"type\":\"uint32\"},{\"internalType\":\"address\",\"name\":\"rollupContractAddr\",\"type\":\"address\"}],\"name\":\"emitCreateNewRollup\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[],\"name\":\"rollupCount\",\"outputs\":[{\"internalType\":\"uint32\",\"name\":\"\",\"type\":\"uint32\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"uint32\",\"name\":\"rollupID\",\"type\":\"uint32\"}],\"name\":\"rollupIDToRollupData\",\"outputs\":[{\"components\":[{\"internalType\":\"address\",\"name\":\"rollupContract\",\"type\":\"address\"},{\"internalType\":\"uint64\",\"name\":\"chainID\",\"type\":\"uint64\"},{\"internalType\":\"address\",\"name\":\"verifier\",\"type\":\"address\"},{\"internalType\":\"uint64\",\"name\":\"forkID\",\"type\":\"uint64\"},{\"internalType\":\"bytes32\",\"name\":\"lastLocalExitRoot\",\"type\":\"bytes32\"},{\"internalType\":\"uint64\",\"name\":\"lastBatchSequenced\",\"type\":\"uint64\"},{\"internalType\":\"uint64\",\"name\":\"lastVerifiedBatch\",\"type\":\"uint64\"},{\"internalType\":\"uint64\",\"name\":\"_legacyLastPendingState\",\"type\":\"uint64\"},{\"internalType\":\"uint64\",\"name\":\"_legacyLastPendingStateConsolidated\",\"type\":\"uint64\"},{\"internalType\":\"uint64\",\"name\":\"lastVerifiedBatchBeforeUpgrade\",\"type\":\"uint64\"},{\"internalType\":\"uint64\",\"name\":\"rollupTypeID\",\"type\":\"uint64\"},{\"internalType\":\"uint8\",\"name\":\"rollupVerifierType\",\"type\":\"uint8\"}],\"internalType\":\"structRollupManagerMock.RollupDataReturn\",\"name\":\"rollupData\",\"type\":\"tuple\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"uint32\",\"name\":\"rollupID\",\"type\":\"uint32\"},{\"internalType\":\"address\",\"name\":\"rollupContractAddr\",\"type\":\"address\"}],\"name\":\"setRollupContract\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"uint32\",\"name\":\"newRollupCount\",\"type\":\"uint32\"}],\"name\":\"setRollupCount\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"}]",
	Bin: "0x608060405234801561001057600080fd5b5061069e806100206000396000f3fe608060405234801561001057600080fd5b506004361061007d5760003560e01c8063f103ffff1161005b578063f103ffff146100d4578063f4e92675146100e7578063f9c4c2ae14610111578063f9f43b1a1461026557600080fd5b806323c998d5146100825780632b844fe8146100ae5780632cd34f32146100c1575b600080fd5b6100ac6100903660046104f6565b6000805463ffffffff191663ffffffff92909216919091179055565b005b6100ac6100bc366004610518565b610278565b6100ac6100cf366004610518565b610333565b6100ac6100e2366004610518565b6103df565b6000546100f79063ffffffff1681565b60405163ffffffff90911681526020015b60405180910390f35b61025861011f3660046104f6565b6040805161018081018252600080825260208201819052918101829052606081018290526080810182905260a0810182905260c0810182905260e081018290526101008101829052610120810182905261014081018290526101608101919091525063ffffffff1660009081526001602081815260409283902083516101808101855281546001600160a01b03808216835267ffffffffffffffff600160a01b928390048116958401959095529483015494851695820195909552939092048116606084015260028201546080840152600382015480821660a085015268010000000000000000808204831660c0860152600160801b808304841660e0870152600160c01b909204831661010086015260049093015480831661012086015292830490911661014084015260ff91041661016082015290565b604051610108919061055b565b6100ac610273366004610518565b61047d565b60005463ffffffff90811690831611156102a2576000805463ffffffff191663ffffffff84161790555b63ffffffff8216600081815260016020908152604080832080546001600160a01b0319166001600160a01b0387169081179091558151848152928301528101829052606081018290526080810182905260a0810182905260c08101919091527f4da47f6e9bbd9ef91887183a576aaebcf1b9bb7d2a567b33b075044c6d36082e9060e0015b60405180910390a25050565b60005463ffffffff908116908316111561035d576000805463ffffffff191663ffffffff84161790555b63ffffffff8216600081815260016020908152604080832080546001600160a01b0319166001600160a01b03871690811790915581518481529283015281018290526060810182905260a0608082018190528101919091527f144e3f9b5c63682a3bb7e9ad31e99c043890d3d540cd79dcebc3b5bdfba94c9b9060c001610327565b60005463ffffffff9081169083161115610409576000805463ffffffff191663ffffffff84161790555b63ffffffff8216600081815260016020908152604080832080546001600160a01b0319166001600160a01b038716908117909155815184815292830152810182905260608101919091527f194c983456df6701c6a50830b90fe80e72b823411d0d524970c9590dc277a64190608001610327565b60005463ffffffff90811690831611156104a7576000805463ffffffff191663ffffffff84161790555b63ffffffff91909116600090815260016020526040902080546001600160a01b0319166001600160a01b03909216919091179055565b803563ffffffff811681146104f157600080fd5b919050565b60006020828403121561050857600080fd5b610511826104dd565b9392505050565b6000806040838503121561052b57600080fd5b610534836104dd565b915060208301356001600160a01b038116811461055057600080fd5b809150509250929050565b81516001600160a01b0316815261018081016020830151610588602084018267ffffffffffffffff169052565b5060408301516105a360408401826001600160a01b03169052565b5060608301516105bf606084018267ffffffffffffffff169052565b506080830151608083015260a08301516105e560a084018267ffffffffffffffff169052565b5060c083015161060160c084018267ffffffffffffffff169052565b5060e083015161061d60e084018267ffffffffffffffff169052565b506101008381015167ffffffffffffffff908116918401919091526101208085015182169084015261014080850151909116908301526101609283015160ff1692909101919091529056fea2646970667358221220995e60aad17d2c1c9557140629e5212521c2aa0037ef5ff30b3920608aba648d64736f6c63430008120033",
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

// EmitAddExistingRollup is a paid mutator transaction binding the contract method 0x2b844fe8.
//
// Solidity: function emitAddExistingRollup(uint32 rollupID, address rollupContractAddr) returns()
func (_Rollupmanagermock *RollupmanagermockTransactor) EmitAddExistingRollup(opts *bind.TransactOpts, rollupID uint32, rollupContractAddr common.Address) (*types.Transaction, error) {
	return _Rollupmanagermock.contract.Transact(opts, "emitAddExistingRollup", rollupID, rollupContractAddr)
}

// EmitAddExistingRollup is a paid mutator transaction binding the contract method 0x2b844fe8.
//
// Solidity: function emitAddExistingRollup(uint32 rollupID, address rollupContractAddr) returns()
func (_Rollupmanagermock *RollupmanagermockSession) EmitAddExistingRollup(rollupID uint32, rollupContractAddr common.Address) (*types.Transaction, error) {
	return _Rollupmanagermock.Contract.EmitAddExistingRollup(&_Rollupmanagermock.TransactOpts, rollupID, rollupContractAddr)
}

// EmitAddExistingRollup is a paid mutator transaction binding the contract method 0x2b844fe8.
//
// Solidity: function emitAddExistingRollup(uint32 rollupID, address rollupContractAddr) returns()
func (_Rollupmanagermock *RollupmanagermockTransactorSession) EmitAddExistingRollup(rollupID uint32, rollupContractAddr common.Address) (*types.Transaction, error) {
	return _Rollupmanagermock.Contract.EmitAddExistingRollup(&_Rollupmanagermock.TransactOpts, rollupID, rollupContractAddr)
}

// EmitCreateNewAggchain is a paid mutator transaction binding the contract method 0x2cd34f32.
//
// Solidity: function emitCreateNewAggchain(uint32 rollupID, address rollupContractAddr) returns()
func (_Rollupmanagermock *RollupmanagermockTransactor) EmitCreateNewAggchain(opts *bind.TransactOpts, rollupID uint32, rollupContractAddr common.Address) (*types.Transaction, error) {
	return _Rollupmanagermock.contract.Transact(opts, "emitCreateNewAggchain", rollupID, rollupContractAddr)
}

// EmitCreateNewAggchain is a paid mutator transaction binding the contract method 0x2cd34f32.
//
// Solidity: function emitCreateNewAggchain(uint32 rollupID, address rollupContractAddr) returns()
func (_Rollupmanagermock *RollupmanagermockSession) EmitCreateNewAggchain(rollupID uint32, rollupContractAddr common.Address) (*types.Transaction, error) {
	return _Rollupmanagermock.Contract.EmitCreateNewAggchain(&_Rollupmanagermock.TransactOpts, rollupID, rollupContractAddr)
}

// EmitCreateNewAggchain is a paid mutator transaction binding the contract method 0x2cd34f32.
//
// Solidity: function emitCreateNewAggchain(uint32 rollupID, address rollupContractAddr) returns()
func (_Rollupmanagermock *RollupmanagermockTransactorSession) EmitCreateNewAggchain(rollupID uint32, rollupContractAddr common.Address) (*types.Transaction, error) {
	return _Rollupmanagermock.Contract.EmitCreateNewAggchain(&_Rollupmanagermock.TransactOpts, rollupID, rollupContractAddr)
}

// EmitCreateNewRollup is a paid mutator transaction binding the contract method 0xf103ffff.
//
// Solidity: function emitCreateNewRollup(uint32 rollupID, address rollupContractAddr) returns()
func (_Rollupmanagermock *RollupmanagermockTransactor) EmitCreateNewRollup(opts *bind.TransactOpts, rollupID uint32, rollupContractAddr common.Address) (*types.Transaction, error) {
	return _Rollupmanagermock.contract.Transact(opts, "emitCreateNewRollup", rollupID, rollupContractAddr)
}

// EmitCreateNewRollup is a paid mutator transaction binding the contract method 0xf103ffff.
//
// Solidity: function emitCreateNewRollup(uint32 rollupID, address rollupContractAddr) returns()
func (_Rollupmanagermock *RollupmanagermockSession) EmitCreateNewRollup(rollupID uint32, rollupContractAddr common.Address) (*types.Transaction, error) {
	return _Rollupmanagermock.Contract.EmitCreateNewRollup(&_Rollupmanagermock.TransactOpts, rollupID, rollupContractAddr)
}

// EmitCreateNewRollup is a paid mutator transaction binding the contract method 0xf103ffff.
//
// Solidity: function emitCreateNewRollup(uint32 rollupID, address rollupContractAddr) returns()
func (_Rollupmanagermock *RollupmanagermockTransactorSession) EmitCreateNewRollup(rollupID uint32, rollupContractAddr common.Address) (*types.Transaction, error) {
	return _Rollupmanagermock.Contract.EmitCreateNewRollup(&_Rollupmanagermock.TransactOpts, rollupID, rollupContractAddr)
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

// RollupmanagermockAddExistingRollupIterator is returned from FilterAddExistingRollup and is used to iterate over the raw logs and unpacked data for AddExistingRollup events raised by the Rollupmanagermock contract.
type RollupmanagermockAddExistingRollupIterator struct {
	Event *RollupmanagermockAddExistingRollup // Event containing the contract specifics and raw log

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
func (it *RollupmanagermockAddExistingRollupIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(RollupmanagermockAddExistingRollup)
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
		it.Event = new(RollupmanagermockAddExistingRollup)
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
func (it *RollupmanagermockAddExistingRollupIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *RollupmanagermockAddExistingRollupIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// RollupmanagermockAddExistingRollup represents a AddExistingRollup event raised by the Rollupmanagermock contract.
type RollupmanagermockAddExistingRollup struct {
	RollupID                       uint32
	ForkID                         uint64
	RollupAddress                  common.Address
	ChainID                        uint64
	RollupVerifierType             uint8
	LastVerifiedBatchBeforeUpgrade uint64
	ProgramVKey                    [32]byte
	InitPessimisticRoot            [32]byte
	Raw                            types.Log // Blockchain specific contextual infos
}

// FilterAddExistingRollup is a free log retrieval operation binding the contract event 0x4da47f6e9bbd9ef91887183a576aaebcf1b9bb7d2a567b33b075044c6d36082e.
//
// Solidity: event AddExistingRollup(uint32 indexed rollupID, uint64 forkID, address rollupAddress, uint64 chainID, uint8 rollupVerifierType, uint64 lastVerifiedBatchBeforeUpgrade, bytes32 programVKey, bytes32 initPessimisticRoot)
func (_Rollupmanagermock *RollupmanagermockFilterer) FilterAddExistingRollup(opts *bind.FilterOpts, rollupID []uint32) (*RollupmanagermockAddExistingRollupIterator, error) {

	var rollupIDRule []interface{}
	for _, rollupIDItem := range rollupID {
		rollupIDRule = append(rollupIDRule, rollupIDItem)
	}

	logs, sub, err := _Rollupmanagermock.contract.FilterLogs(opts, "AddExistingRollup", rollupIDRule)
	if err != nil {
		return nil, err
	}
	return &RollupmanagermockAddExistingRollupIterator{contract: _Rollupmanagermock.contract, event: "AddExistingRollup", logs: logs, sub: sub}, nil
}

// WatchAddExistingRollup is a free log subscription operation binding the contract event 0x4da47f6e9bbd9ef91887183a576aaebcf1b9bb7d2a567b33b075044c6d36082e.
//
// Solidity: event AddExistingRollup(uint32 indexed rollupID, uint64 forkID, address rollupAddress, uint64 chainID, uint8 rollupVerifierType, uint64 lastVerifiedBatchBeforeUpgrade, bytes32 programVKey, bytes32 initPessimisticRoot)
func (_Rollupmanagermock *RollupmanagermockFilterer) WatchAddExistingRollup(opts *bind.WatchOpts, sink chan<- *RollupmanagermockAddExistingRollup, rollupID []uint32) (event.Subscription, error) {

	var rollupIDRule []interface{}
	for _, rollupIDItem := range rollupID {
		rollupIDRule = append(rollupIDRule, rollupIDItem)
	}

	logs, sub, err := _Rollupmanagermock.contract.WatchLogs(opts, "AddExistingRollup", rollupIDRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(RollupmanagermockAddExistingRollup)
				if err := _Rollupmanagermock.contract.UnpackLog(event, "AddExistingRollup", log); err != nil {
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

// ParseAddExistingRollup is a log parse operation binding the contract event 0x4da47f6e9bbd9ef91887183a576aaebcf1b9bb7d2a567b33b075044c6d36082e.
//
// Solidity: event AddExistingRollup(uint32 indexed rollupID, uint64 forkID, address rollupAddress, uint64 chainID, uint8 rollupVerifierType, uint64 lastVerifiedBatchBeforeUpgrade, bytes32 programVKey, bytes32 initPessimisticRoot)
func (_Rollupmanagermock *RollupmanagermockFilterer) ParseAddExistingRollup(log types.Log) (*RollupmanagermockAddExistingRollup, error) {
	event := new(RollupmanagermockAddExistingRollup)
	if err := _Rollupmanagermock.contract.UnpackLog(event, "AddExistingRollup", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// RollupmanagermockCreateNewAggchainIterator is returned from FilterCreateNewAggchain and is used to iterate over the raw logs and unpacked data for CreateNewAggchain events raised by the Rollupmanagermock contract.
type RollupmanagermockCreateNewAggchainIterator struct {
	Event *RollupmanagermockCreateNewAggchain // Event containing the contract specifics and raw log

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
func (it *RollupmanagermockCreateNewAggchainIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(RollupmanagermockCreateNewAggchain)
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
		it.Event = new(RollupmanagermockCreateNewAggchain)
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
func (it *RollupmanagermockCreateNewAggchainIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *RollupmanagermockCreateNewAggchainIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// RollupmanagermockCreateNewAggchain represents a CreateNewAggchain event raised by the Rollupmanagermock contract.
type RollupmanagermockCreateNewAggchain struct {
	RollupID                uint32
	RollupTypeID            uint32
	RollupAddress           common.Address
	ChainID                 uint64
	RollupVerifierType      uint8
	InitializeBytesAggchain []byte
	Raw                     types.Log // Blockchain specific contextual infos
}

// FilterCreateNewAggchain is a free log retrieval operation binding the contract event 0x144e3f9b5c63682a3bb7e9ad31e99c043890d3d540cd79dcebc3b5bdfba94c9b.
//
// Solidity: event CreateNewAggchain(uint32 indexed rollupID, uint32 rollupTypeID, address rollupAddress, uint64 chainID, uint8 rollupVerifierType, bytes initializeBytesAggchain)
func (_Rollupmanagermock *RollupmanagermockFilterer) FilterCreateNewAggchain(opts *bind.FilterOpts, rollupID []uint32) (*RollupmanagermockCreateNewAggchainIterator, error) {

	var rollupIDRule []interface{}
	for _, rollupIDItem := range rollupID {
		rollupIDRule = append(rollupIDRule, rollupIDItem)
	}

	logs, sub, err := _Rollupmanagermock.contract.FilterLogs(opts, "CreateNewAggchain", rollupIDRule)
	if err != nil {
		return nil, err
	}
	return &RollupmanagermockCreateNewAggchainIterator{contract: _Rollupmanagermock.contract, event: "CreateNewAggchain", logs: logs, sub: sub}, nil
}

// WatchCreateNewAggchain is a free log subscription operation binding the contract event 0x144e3f9b5c63682a3bb7e9ad31e99c043890d3d540cd79dcebc3b5bdfba94c9b.
//
// Solidity: event CreateNewAggchain(uint32 indexed rollupID, uint32 rollupTypeID, address rollupAddress, uint64 chainID, uint8 rollupVerifierType, bytes initializeBytesAggchain)
func (_Rollupmanagermock *RollupmanagermockFilterer) WatchCreateNewAggchain(opts *bind.WatchOpts, sink chan<- *RollupmanagermockCreateNewAggchain, rollupID []uint32) (event.Subscription, error) {

	var rollupIDRule []interface{}
	for _, rollupIDItem := range rollupID {
		rollupIDRule = append(rollupIDRule, rollupIDItem)
	}

	logs, sub, err := _Rollupmanagermock.contract.WatchLogs(opts, "CreateNewAggchain", rollupIDRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(RollupmanagermockCreateNewAggchain)
				if err := _Rollupmanagermock.contract.UnpackLog(event, "CreateNewAggchain", log); err != nil {
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

// ParseCreateNewAggchain is a log parse operation binding the contract event 0x144e3f9b5c63682a3bb7e9ad31e99c043890d3d540cd79dcebc3b5bdfba94c9b.
//
// Solidity: event CreateNewAggchain(uint32 indexed rollupID, uint32 rollupTypeID, address rollupAddress, uint64 chainID, uint8 rollupVerifierType, bytes initializeBytesAggchain)
func (_Rollupmanagermock *RollupmanagermockFilterer) ParseCreateNewAggchain(log types.Log) (*RollupmanagermockCreateNewAggchain, error) {
	event := new(RollupmanagermockCreateNewAggchain)
	if err := _Rollupmanagermock.contract.UnpackLog(event, "CreateNewAggchain", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// RollupmanagermockCreateNewRollupIterator is returned from FilterCreateNewRollup and is used to iterate over the raw logs and unpacked data for CreateNewRollup events raised by the Rollupmanagermock contract.
type RollupmanagermockCreateNewRollupIterator struct {
	Event *RollupmanagermockCreateNewRollup // Event containing the contract specifics and raw log

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
func (it *RollupmanagermockCreateNewRollupIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(RollupmanagermockCreateNewRollup)
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
		it.Event = new(RollupmanagermockCreateNewRollup)
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
func (it *RollupmanagermockCreateNewRollupIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *RollupmanagermockCreateNewRollupIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// RollupmanagermockCreateNewRollup represents a CreateNewRollup event raised by the Rollupmanagermock contract.
type RollupmanagermockCreateNewRollup struct {
	RollupID        uint32
	RollupTypeID    uint32
	RollupAddress   common.Address
	ChainID         uint64
	GasTokenAddress common.Address
	Raw             types.Log // Blockchain specific contextual infos
}

// FilterCreateNewRollup is a free log retrieval operation binding the contract event 0x194c983456df6701c6a50830b90fe80e72b823411d0d524970c9590dc277a641.
//
// Solidity: event CreateNewRollup(uint32 indexed rollupID, uint32 rollupTypeID, address rollupAddress, uint64 chainID, address gasTokenAddress)
func (_Rollupmanagermock *RollupmanagermockFilterer) FilterCreateNewRollup(opts *bind.FilterOpts, rollupID []uint32) (*RollupmanagermockCreateNewRollupIterator, error) {

	var rollupIDRule []interface{}
	for _, rollupIDItem := range rollupID {
		rollupIDRule = append(rollupIDRule, rollupIDItem)
	}

	logs, sub, err := _Rollupmanagermock.contract.FilterLogs(opts, "CreateNewRollup", rollupIDRule)
	if err != nil {
		return nil, err
	}
	return &RollupmanagermockCreateNewRollupIterator{contract: _Rollupmanagermock.contract, event: "CreateNewRollup", logs: logs, sub: sub}, nil
}

// WatchCreateNewRollup is a free log subscription operation binding the contract event 0x194c983456df6701c6a50830b90fe80e72b823411d0d524970c9590dc277a641.
//
// Solidity: event CreateNewRollup(uint32 indexed rollupID, uint32 rollupTypeID, address rollupAddress, uint64 chainID, address gasTokenAddress)
func (_Rollupmanagermock *RollupmanagermockFilterer) WatchCreateNewRollup(opts *bind.WatchOpts, sink chan<- *RollupmanagermockCreateNewRollup, rollupID []uint32) (event.Subscription, error) {

	var rollupIDRule []interface{}
	for _, rollupIDItem := range rollupID {
		rollupIDRule = append(rollupIDRule, rollupIDItem)
	}

	logs, sub, err := _Rollupmanagermock.contract.WatchLogs(opts, "CreateNewRollup", rollupIDRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(RollupmanagermockCreateNewRollup)
				if err := _Rollupmanagermock.contract.UnpackLog(event, "CreateNewRollup", log); err != nil {
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

// ParseCreateNewRollup is a log parse operation binding the contract event 0x194c983456df6701c6a50830b90fe80e72b823411d0d524970c9590dc277a641.
//
// Solidity: event CreateNewRollup(uint32 indexed rollupID, uint32 rollupTypeID, address rollupAddress, uint64 chainID, address gasTokenAddress)
func (_Rollupmanagermock *RollupmanagermockFilterer) ParseCreateNewRollup(log types.Log) (*RollupmanagermockCreateNewRollup, error) {
	event := new(RollupmanagermockCreateNewRollup)
	if err := _Rollupmanagermock.contract.UnpackLog(event, "CreateNewRollup", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}
