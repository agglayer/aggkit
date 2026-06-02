// Code generated - DO NOT EDIT.
// This file is a generated binding and any manual changes will be lost.

package bridgemessagereceivermock

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

// BridgemessagereceivermockMetaData contains all meta data concerning the Bridgemessagereceivermock contract.
var BridgemessagereceivermockMetaData = &bind.MetaData{
	ABI: "[{\"type\":\"constructor\",\"inputs\":[{\"name\":\"_bridgeAddress\",\"type\":\"address\",\"internalType\":\"contractIPolygonZkEVMBridgeV2\"}],\"stateMutability\":\"nonpayable\"},{\"type\":\"function\",\"name\":\"bridgeAddress\",\"inputs\":[],\"outputs\":[{\"name\":\"\",\"type\":\"address\",\"internalType\":\"contractIPolygonZkEVMBridgeV2\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"onMessageReceived\",\"inputs\":[{\"name\":\"originAddress\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"originNetwork\",\"type\":\"uint32\",\"internalType\":\"uint32\"},{\"name\":\"data\",\"type\":\"bytes\",\"internalType\":\"bytes\"}],\"outputs\":[],\"stateMutability\":\"payable\"},{\"type\":\"function\",\"name\":\"testClaim\",\"inputs\":[{\"name\":\"claimData1\",\"type\":\"bytes\",\"internalType\":\"bytes\"},{\"name\":\"bridgeAsset\",\"type\":\"bytes\",\"internalType\":\"bytes\"},{\"name\":\"claimData2\",\"type\":\"bytes\",\"internalType\":\"bytes\"}],\"outputs\":[],\"stateMutability\":\"payable\"},{\"type\":\"function\",\"name\":\"updateParameters\",\"inputs\":[{\"name\":\"msmtProofLocalExitRoot\",\"type\":\"bytes32[32]\",\"internalType\":\"bytes32[32]\"},{\"name\":\"msmtProofRollupExitRoot\",\"type\":\"bytes32[32]\",\"internalType\":\"bytes32[32]\"},{\"name\":\"mglobalIndex\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"mmainnetExitRoot\",\"type\":\"bytes32\",\"internalType\":\"bytes32\"},{\"name\":\"mrollupExitRoot\",\"type\":\"bytes32\",\"internalType\":\"bytes32\"},{\"name\":\"moriginNetwork\",\"type\":\"uint32\",\"internalType\":\"uint32\"},{\"name\":\"moriginAddress\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"mdestinationNetwork\",\"type\":\"uint32\",\"internalType\":\"uint32\"},{\"name\":\"mdestinationAddress\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"mamount\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"mmetadata\",\"type\":\"bytes\",\"internalType\":\"bytes\"}],\"outputs\":[],\"stateMutability\":\"nonpayable\"},{\"type\":\"event\",\"name\":\"MessageReceived\",\"inputs\":[{\"name\":\"destinationAddress\",\"type\":\"address\",\"indexed\":false,\"internalType\":\"address\"}],\"anonymous\":false},{\"type\":\"event\",\"name\":\"UpdateParameters\",\"inputs\":[],\"anonymous\":false}]",
	Bin: "0x60a0346100a157601f610f0138819003918201601f19168301916001600160401b038311848410176100a5578084926020946040528339810103126100a157516001600160a01b03811681036100a157608052604051610e4790816100ba8239608051818181605c0152818161013b015281816101cc015281816102bf015281816103d40152818161041b01528181610478015281816104d6015261091f0152f35b5f80fd5b634e487b7160e01b5f52604160045260245ffdfe6101806040526004361015610012575f80fd5b5f5f3560e01c80631806b5f2146108b557806381119ab214610609578063862e4a0c1461008e5763a3c573eb14610047575f80fd5b3461008b578060031936011261008b576040517f00000000000000000000000000000000000000000000000000000000000000006001600160a01b03168152602090f35b80fd5b50606036600319011261008b5760043567ffffffffffffffff8111610601576100bb903690600401610b1b565b60243567ffffffffffffffff8111610605576100db903690600401610b1b565b908260443567ffffffffffffffff811161060157610100610110913690600401610b1b565b9260208082518301019101610c5c565b60e05260c05260805261016094909452610120949094526101009490945261014094909452909291907f00000000000000000000000000000000000000000000000000000000000000006001600160a01b03163b156105fd57849060405160a05263f5efcd7960e01b60a0515260a051906101c260a0519160e0519060c0519060018060a01b03608051169060018060a01b038b16898b61016051610120516101005161014051600460a05101610d45565b60a05191900390837f00000000000000000000000000000000000000000000000000000000000000006001600160a01b03165af19283156105f25784936105d6575b63ffffffff60405192602084019463f5efcd7960e01b865261022c6024860161014051610cfa565b61023d610424860161010051610cfa565b6101205161082486015261016051610844860152610864850152166108848301526001600160a01b039081166108a48301526103e86108c4830152608051166108e482015260c05161090482015261092061092482015260e0516102ba9082906102ac90610944830190610d21565b03601f198101835282610ac9565b5190827f00000000000000000000000000000000000000000000000000000000000000006001600160a01b03165af16102f1610dc2565b506105915781839251810160c08282031261058c5761031260208301610bf1565b9061031f60408401610c02565b91606084015161033160808601610c02565b9460a0810151948515158096036105885760c08201519467ffffffffffffffff8611610584576001978a976103756103c99560208b9a816102ac9701920101610c16565b9160405196879563ffffffff602088019a63cd58657960e01b8c521660248801528c8060a01b0316604487015260648601528a8060a01b0316608485015260a484015260c060c484015260e4830190610d21565b519034858060a01b037f0000000000000000000000000000000000000000000000000000000000000000165af16103fe610dc2565b50151503610581576040516338b8fbbb60e01b81526020816004817f00000000000000000000000000000000000000000000000000000000000000006001600160a01b03165afa80156105765761052d575b508060208061046493518301019101610c5c565b9460018060a09c9396959499989b9a9c1b037f0000000000000000000000000000000000000000000000000000000000000000163b15610529578b996104d1976040519c8d9b8c9b63f5efcd7960e01b8d5260018060a01b03169760018060a01b03169560048d01610d45565b0381837f00000000000000000000000000000000000000000000000000000000000000006001600160a01b03165af1801561051e5761050d5750f35b8161051791610ac9565b61008b5780f35b6040513d84823e3d90fd5b8b80fd5b6020813d60201161056e575b8161054660209383610ac9565b8101031261056a5751906001600160a01b038216820361056a579050610464610450565b5050fd5b3d9150610539565b6040513d85823e3d90fd5b50fd5b8980fd5b8880fd5b505050fd5b60405162461bcd60e51b815260206004820152601960248201527f44657374696e6174696f6e4e6574776f726b496e76616c6964000000000000006044820152606490fd5b926105e38160a051610ac9565b6105ee578392610204565b8380fd5b6040513d86823e3d90fd5b8480fd5b5080fd5b8280fd5b503461008b5761092036600319011261008b57366104041161008b57366108041161008b57610864359063ffffffff821680920361008b5761088435916001600160a01b0383168303610601576108a43563ffffffff81168103610605576108c4356001600160a01b03811691908290036105ee57610904359467ffffffffffffffff86116105fd57366023870112156105fd5785600401359367ffffffffffffffff85116108b15736602486890101116108b1576004865b6020811061089f575050610404865b6020811061088a5750506108043560405561082435604155610844356042556043549263ffffffff60c01b9060c01b169263ffffffff60e01b161790640100000000600160c01b039060201b1617176043556bffffffffffffffffffffffff60a01b60445416176044556108e43560455561074d604654610b61565b601f8111610826575b5081601f82116001146107b75782938293926107a9575b50508160011b915f199060031b1c1916176046555b7f9d226db03d4d6614ea01926ce8a588879492a2681b9684eb655b1470d32d4b9e8180a180f35b602492500101355f8061076d565b601f198216935f516020610df25f395f51905f5291845b86811061080b57508360019596106107ef575b505050811b01604655610782565b01602401355f19600384901b60f8161c191690555f80806107e1565b909260206001819260248787010135815501940191016107ce565b601f820160051c5f516020610df25f395f51905f52019060208310610875575b601f0160051c5f516020610df25f395f51905f5201905b81811061086a5750610756565b83815560010161085d565b5f516020610df25f395f51905f529150610846565b600190602083359301928160200155016106d1565b600190602083359301928155016106c2565b8580fd5b506060366003190112610ac5576004356001600160a01b03811690819003610ac55760243563ffffffff8116809103610ac55760443567ffffffffffffffff8111610ac557610908903690600401610b1b565b5060405460415460425460435460445460455494967f00000000000000000000000000000000000000000000000000000000000000006001600160a01b039081169792169460c09390931c63ffffffff16939092873b15610ac55760405163f5efcd7960e01b8152985f8060048c015b60208210610aaf575050506104048a016020905f905b60208210610a99575050506108048a01526108248901526108448801526108648701526108848601526108a48501526108c48401526108e4830152610920610904830152815f6046546109e081610b61565b90816109248501526001811690815f14610a765750600114610a2f575b5091815f81819503925af18015610a2457610a16575080f35b610a2291505f90610ac9565b005b6040513d5f823e3d90fd5b60465f90815291505f516020610df25f395f51905f525b818310610a5b575050810161094401816109fd565b80546109448488010152859350602090920191600101610a46565b60ff19166109448581019190915291151560051b840190910191508290506109fd565b600160208192855481520193019101909161098e565b6001602081928554815201930191019091610978565b5f80fd5b90601f8019910116810190811067ffffffffffffffff821117610aeb57604052565b634e487b7160e01b5f52604160045260245ffd5b67ffffffffffffffff8111610aeb57601f01601f191660200190565b81601f82011215610ac557803590610b3282610aff565b92610b406040519485610ac9565b82845260208383010111610ac557815f926020809301838601378301015290565b90600182811c92168015610b8f575b6020831014610b7b57565b634e487b7160e01b5f52602260045260245ffd5b91607f1691610b70565b9080601f83011215610ac55760405191610400830183811067ffffffffffffffff821117610aeb5760405282906104008101928311610ac557905b828210610be15750505090565b8151815260209182019101610bd4565b519063ffffffff82168203610ac557565b51906001600160a01b0382168203610ac557565b81601f82011215610ac557805190610c2d82610aff565b92610c3b6040519485610ac9565b82845260208383010111610ac557815f9260208093018386015e8301015290565b91909161092081840312610ac557610c748382610b99565b92610c83816104008401610b99565b92610800830151926108208101519261084082015192610ca66108608401610bf1565b92610cb46108808201610c02565b92610cc26108a08301610bf1565b92610cd06108c08401610c02565b926108e08101519261090082015167ffffffffffffffff8111610ac557610cf79201610c16565b90565b905f905b60208210610d0b57505050565b6020806001928551815201930191019091610cfe565b805180835260209291819084018484015e5f828201840152601f01601f1916010190565b97939463ffffffff95610cf79c9b9894610d748895610d698d6109209f9c97610cfa565b6104008d0190610cfa565b6108008b01526108208a0152610840890152166108608701526001600160a01b0390811661088087015291166108a0850152166108c08301526108e082015261090081018290520190610d21565b3d15610dec573d90610dd382610aff565b91610de16040519384610ac9565b82523d5f602084013e565b60609056fe128667f541fed74a8429f9d592c26c2c6a4beb9ae5ead9912c98b2595c842310a2646970667358221220e3e5936a865b6813452b6ea0837fe04674f6fad44a42a5c650bad0722ed302db64736f6c634300081c0033",
}

// BridgemessagereceivermockABI is the input ABI used to generate the binding from.
// Deprecated: Use BridgemessagereceivermockMetaData.ABI instead.
var BridgemessagereceivermockABI = BridgemessagereceivermockMetaData.ABI

// BridgemessagereceivermockBin is the compiled bytecode used for deploying new contracts.
// Deprecated: Use BridgemessagereceivermockMetaData.Bin instead.
var BridgemessagereceivermockBin = BridgemessagereceivermockMetaData.Bin

// DeployBridgemessagereceivermock deploys a new Ethereum contract, binding an instance of Bridgemessagereceivermock to it.
func DeployBridgemessagereceivermock(auth *bind.TransactOpts, backend bind.ContractBackend, _bridgeAddress common.Address) (common.Address, *types.Transaction, *Bridgemessagereceivermock, error) {
	parsed, err := BridgemessagereceivermockMetaData.GetAbi()
	if err != nil {
		return common.Address{}, nil, nil, err
	}
	if parsed == nil {
		return common.Address{}, nil, nil, errors.New("GetABI returned nil")
	}

	address, tx, contract, err := bind.DeployContract(auth, *parsed, common.FromHex(BridgemessagereceivermockBin), backend, _bridgeAddress)
	if err != nil {
		return common.Address{}, nil, nil, err
	}
	return address, tx, &Bridgemessagereceivermock{BridgemessagereceivermockCaller: BridgemessagereceivermockCaller{contract: contract}, BridgemessagereceivermockTransactor: BridgemessagereceivermockTransactor{contract: contract}, BridgemessagereceivermockFilterer: BridgemessagereceivermockFilterer{contract: contract}}, nil
}

// Bridgemessagereceivermock is an auto generated Go binding around an Ethereum contract.
type Bridgemessagereceivermock struct {
	BridgemessagereceivermockCaller     // Read-only binding to the contract
	BridgemessagereceivermockTransactor // Write-only binding to the contract
	BridgemessagereceivermockFilterer   // Log filterer for contract events
}

// BridgemessagereceivermockCaller is an auto generated read-only Go binding around an Ethereum contract.
type BridgemessagereceivermockCaller struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// BridgemessagereceivermockTransactor is an auto generated write-only Go binding around an Ethereum contract.
type BridgemessagereceivermockTransactor struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// BridgemessagereceivermockFilterer is an auto generated log filtering Go binding around an Ethereum contract events.
type BridgemessagereceivermockFilterer struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// BridgemessagereceivermockSession is an auto generated Go binding around an Ethereum contract,
// with pre-set call and transact options.
type BridgemessagereceivermockSession struct {
	Contract     *Bridgemessagereceivermock // Generic contract binding to set the session for
	CallOpts     bind.CallOpts              // Call options to use throughout this session
	TransactOpts bind.TransactOpts          // Transaction auth options to use throughout this session
}

// BridgemessagereceivermockCallerSession is an auto generated read-only Go binding around an Ethereum contract,
// with pre-set call options.
type BridgemessagereceivermockCallerSession struct {
	Contract *BridgemessagereceivermockCaller // Generic contract caller binding to set the session for
	CallOpts bind.CallOpts                    // Call options to use throughout this session
}

// BridgemessagereceivermockTransactorSession is an auto generated write-only Go binding around an Ethereum contract,
// with pre-set transact options.
type BridgemessagereceivermockTransactorSession struct {
	Contract     *BridgemessagereceivermockTransactor // Generic contract transactor binding to set the session for
	TransactOpts bind.TransactOpts                    // Transaction auth options to use throughout this session
}

// BridgemessagereceivermockRaw is an auto generated low-level Go binding around an Ethereum contract.
type BridgemessagereceivermockRaw struct {
	Contract *Bridgemessagereceivermock // Generic contract binding to access the raw methods on
}

// BridgemessagereceivermockCallerRaw is an auto generated low-level read-only Go binding around an Ethereum contract.
type BridgemessagereceivermockCallerRaw struct {
	Contract *BridgemessagereceivermockCaller // Generic read-only contract binding to access the raw methods on
}

// BridgemessagereceivermockTransactorRaw is an auto generated low-level write-only Go binding around an Ethereum contract.
type BridgemessagereceivermockTransactorRaw struct {
	Contract *BridgemessagereceivermockTransactor // Generic write-only contract binding to access the raw methods on
}

// NewBridgemessagereceivermock creates a new instance of Bridgemessagereceivermock, bound to a specific deployed contract.
func NewBridgemessagereceivermock(address common.Address, backend bind.ContractBackend) (*Bridgemessagereceivermock, error) {
	contract, err := bindBridgemessagereceivermock(address, backend, backend, backend)
	if err != nil {
		return nil, err
	}
	return &Bridgemessagereceivermock{BridgemessagereceivermockCaller: BridgemessagereceivermockCaller{contract: contract}, BridgemessagereceivermockTransactor: BridgemessagereceivermockTransactor{contract: contract}, BridgemessagereceivermockFilterer: BridgemessagereceivermockFilterer{contract: contract}}, nil
}

// NewBridgemessagereceivermockCaller creates a new read-only instance of Bridgemessagereceivermock, bound to a specific deployed contract.
func NewBridgemessagereceivermockCaller(address common.Address, caller bind.ContractCaller) (*BridgemessagereceivermockCaller, error) {
	contract, err := bindBridgemessagereceivermock(address, caller, nil, nil)
	if err != nil {
		return nil, err
	}
	return &BridgemessagereceivermockCaller{contract: contract}, nil
}

// NewBridgemessagereceivermockTransactor creates a new write-only instance of Bridgemessagereceivermock, bound to a specific deployed contract.
func NewBridgemessagereceivermockTransactor(address common.Address, transactor bind.ContractTransactor) (*BridgemessagereceivermockTransactor, error) {
	contract, err := bindBridgemessagereceivermock(address, nil, transactor, nil)
	if err != nil {
		return nil, err
	}
	return &BridgemessagereceivermockTransactor{contract: contract}, nil
}

// NewBridgemessagereceivermockFilterer creates a new log filterer instance of Bridgemessagereceivermock, bound to a specific deployed contract.
func NewBridgemessagereceivermockFilterer(address common.Address, filterer bind.ContractFilterer) (*BridgemessagereceivermockFilterer, error) {
	contract, err := bindBridgemessagereceivermock(address, nil, nil, filterer)
	if err != nil {
		return nil, err
	}
	return &BridgemessagereceivermockFilterer{contract: contract}, nil
}

// bindBridgemessagereceivermock binds a generic wrapper to an already deployed contract.
func bindBridgemessagereceivermock(address common.Address, caller bind.ContractCaller, transactor bind.ContractTransactor, filterer bind.ContractFilterer) (*bind.BoundContract, error) {
	parsed, err := BridgemessagereceivermockMetaData.GetAbi()
	if err != nil {
		return nil, err
	}
	return bind.NewBoundContract(address, *parsed, caller, transactor, filterer), nil
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_Bridgemessagereceivermock *BridgemessagereceivermockRaw) Call(opts *bind.CallOpts, result *[]interface{}, method string, params ...interface{}) error {
	return _Bridgemessagereceivermock.Contract.BridgemessagereceivermockCaller.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_Bridgemessagereceivermock *BridgemessagereceivermockRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _Bridgemessagereceivermock.Contract.BridgemessagereceivermockTransactor.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_Bridgemessagereceivermock *BridgemessagereceivermockRaw) Transact(opts *bind.TransactOpts, method string, params ...interface{}) (*types.Transaction, error) {
	return _Bridgemessagereceivermock.Contract.BridgemessagereceivermockTransactor.contract.Transact(opts, method, params...)
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_Bridgemessagereceivermock *BridgemessagereceivermockCallerRaw) Call(opts *bind.CallOpts, result *[]interface{}, method string, params ...interface{}) error {
	return _Bridgemessagereceivermock.Contract.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_Bridgemessagereceivermock *BridgemessagereceivermockTransactorRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _Bridgemessagereceivermock.Contract.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_Bridgemessagereceivermock *BridgemessagereceivermockTransactorRaw) Transact(opts *bind.TransactOpts, method string, params ...interface{}) (*types.Transaction, error) {
	return _Bridgemessagereceivermock.Contract.contract.Transact(opts, method, params...)
}

// BridgeAddress is a free data retrieval call binding the contract method 0xa3c573eb.
//
// Solidity: function bridgeAddress() view returns(address)
func (_Bridgemessagereceivermock *BridgemessagereceivermockCaller) BridgeAddress(opts *bind.CallOpts) (common.Address, error) {
	var out []interface{}
	err := _Bridgemessagereceivermock.contract.Call(opts, &out, "bridgeAddress")

	if err != nil {
		return *new(common.Address), err
	}

	out0 := *abi.ConvertType(out[0], new(common.Address)).(*common.Address)

	return out0, err

}

// BridgeAddress is a free data retrieval call binding the contract method 0xa3c573eb.
//
// Solidity: function bridgeAddress() view returns(address)
func (_Bridgemessagereceivermock *BridgemessagereceivermockSession) BridgeAddress() (common.Address, error) {
	return _Bridgemessagereceivermock.Contract.BridgeAddress(&_Bridgemessagereceivermock.CallOpts)
}

// BridgeAddress is a free data retrieval call binding the contract method 0xa3c573eb.
//
// Solidity: function bridgeAddress() view returns(address)
func (_Bridgemessagereceivermock *BridgemessagereceivermockCallerSession) BridgeAddress() (common.Address, error) {
	return _Bridgemessagereceivermock.Contract.BridgeAddress(&_Bridgemessagereceivermock.CallOpts)
}

// OnMessageReceived is a paid mutator transaction binding the contract method 0x1806b5f2.
//
// Solidity: function onMessageReceived(address originAddress, uint32 originNetwork, bytes data) payable returns()
func (_Bridgemessagereceivermock *BridgemessagereceivermockTransactor) OnMessageReceived(opts *bind.TransactOpts, originAddress common.Address, originNetwork uint32, data []byte) (*types.Transaction, error) {
	return _Bridgemessagereceivermock.contract.Transact(opts, "onMessageReceived", originAddress, originNetwork, data)
}

// OnMessageReceived is a paid mutator transaction binding the contract method 0x1806b5f2.
//
// Solidity: function onMessageReceived(address originAddress, uint32 originNetwork, bytes data) payable returns()
func (_Bridgemessagereceivermock *BridgemessagereceivermockSession) OnMessageReceived(originAddress common.Address, originNetwork uint32, data []byte) (*types.Transaction, error) {
	return _Bridgemessagereceivermock.Contract.OnMessageReceived(&_Bridgemessagereceivermock.TransactOpts, originAddress, originNetwork, data)
}

// OnMessageReceived is a paid mutator transaction binding the contract method 0x1806b5f2.
//
// Solidity: function onMessageReceived(address originAddress, uint32 originNetwork, bytes data) payable returns()
func (_Bridgemessagereceivermock *BridgemessagereceivermockTransactorSession) OnMessageReceived(originAddress common.Address, originNetwork uint32, data []byte) (*types.Transaction, error) {
	return _Bridgemessagereceivermock.Contract.OnMessageReceived(&_Bridgemessagereceivermock.TransactOpts, originAddress, originNetwork, data)
}

// TestClaim is a paid mutator transaction binding the contract method 0x862e4a0c.
//
// Solidity: function testClaim(bytes claimData1, bytes bridgeAsset, bytes claimData2) payable returns()
func (_Bridgemessagereceivermock *BridgemessagereceivermockTransactor) TestClaim(opts *bind.TransactOpts, claimData1 []byte, bridgeAsset []byte, claimData2 []byte) (*types.Transaction, error) {
	return _Bridgemessagereceivermock.contract.Transact(opts, "testClaim", claimData1, bridgeAsset, claimData2)
}

// TestClaim is a paid mutator transaction binding the contract method 0x862e4a0c.
//
// Solidity: function testClaim(bytes claimData1, bytes bridgeAsset, bytes claimData2) payable returns()
func (_Bridgemessagereceivermock *BridgemessagereceivermockSession) TestClaim(claimData1 []byte, bridgeAsset []byte, claimData2 []byte) (*types.Transaction, error) {
	return _Bridgemessagereceivermock.Contract.TestClaim(&_Bridgemessagereceivermock.TransactOpts, claimData1, bridgeAsset, claimData2)
}

// TestClaim is a paid mutator transaction binding the contract method 0x862e4a0c.
//
// Solidity: function testClaim(bytes claimData1, bytes bridgeAsset, bytes claimData2) payable returns()
func (_Bridgemessagereceivermock *BridgemessagereceivermockTransactorSession) TestClaim(claimData1 []byte, bridgeAsset []byte, claimData2 []byte) (*types.Transaction, error) {
	return _Bridgemessagereceivermock.Contract.TestClaim(&_Bridgemessagereceivermock.TransactOpts, claimData1, bridgeAsset, claimData2)
}

// UpdateParameters is a paid mutator transaction binding the contract method 0x81119ab2.
//
// Solidity: function updateParameters(bytes32[32] msmtProofLocalExitRoot, bytes32[32] msmtProofRollupExitRoot, uint256 mglobalIndex, bytes32 mmainnetExitRoot, bytes32 mrollupExitRoot, uint32 moriginNetwork, address moriginAddress, uint32 mdestinationNetwork, address mdestinationAddress, uint256 mamount, bytes mmetadata) returns()
func (_Bridgemessagereceivermock *BridgemessagereceivermockTransactor) UpdateParameters(opts *bind.TransactOpts, msmtProofLocalExitRoot [32][32]byte, msmtProofRollupExitRoot [32][32]byte, mglobalIndex *big.Int, mmainnetExitRoot [32]byte, mrollupExitRoot [32]byte, moriginNetwork uint32, moriginAddress common.Address, mdestinationNetwork uint32, mdestinationAddress common.Address, mamount *big.Int, mmetadata []byte) (*types.Transaction, error) {
	return _Bridgemessagereceivermock.contract.Transact(opts, "updateParameters", msmtProofLocalExitRoot, msmtProofRollupExitRoot, mglobalIndex, mmainnetExitRoot, mrollupExitRoot, moriginNetwork, moriginAddress, mdestinationNetwork, mdestinationAddress, mamount, mmetadata)
}

// UpdateParameters is a paid mutator transaction binding the contract method 0x81119ab2.
//
// Solidity: function updateParameters(bytes32[32] msmtProofLocalExitRoot, bytes32[32] msmtProofRollupExitRoot, uint256 mglobalIndex, bytes32 mmainnetExitRoot, bytes32 mrollupExitRoot, uint32 moriginNetwork, address moriginAddress, uint32 mdestinationNetwork, address mdestinationAddress, uint256 mamount, bytes mmetadata) returns()
func (_Bridgemessagereceivermock *BridgemessagereceivermockSession) UpdateParameters(msmtProofLocalExitRoot [32][32]byte, msmtProofRollupExitRoot [32][32]byte, mglobalIndex *big.Int, mmainnetExitRoot [32]byte, mrollupExitRoot [32]byte, moriginNetwork uint32, moriginAddress common.Address, mdestinationNetwork uint32, mdestinationAddress common.Address, mamount *big.Int, mmetadata []byte) (*types.Transaction, error) {
	return _Bridgemessagereceivermock.Contract.UpdateParameters(&_Bridgemessagereceivermock.TransactOpts, msmtProofLocalExitRoot, msmtProofRollupExitRoot, mglobalIndex, mmainnetExitRoot, mrollupExitRoot, moriginNetwork, moriginAddress, mdestinationNetwork, mdestinationAddress, mamount, mmetadata)
}

// UpdateParameters is a paid mutator transaction binding the contract method 0x81119ab2.
//
// Solidity: function updateParameters(bytes32[32] msmtProofLocalExitRoot, bytes32[32] msmtProofRollupExitRoot, uint256 mglobalIndex, bytes32 mmainnetExitRoot, bytes32 mrollupExitRoot, uint32 moriginNetwork, address moriginAddress, uint32 mdestinationNetwork, address mdestinationAddress, uint256 mamount, bytes mmetadata) returns()
func (_Bridgemessagereceivermock *BridgemessagereceivermockTransactorSession) UpdateParameters(msmtProofLocalExitRoot [32][32]byte, msmtProofRollupExitRoot [32][32]byte, mglobalIndex *big.Int, mmainnetExitRoot [32]byte, mrollupExitRoot [32]byte, moriginNetwork uint32, moriginAddress common.Address, mdestinationNetwork uint32, mdestinationAddress common.Address, mamount *big.Int, mmetadata []byte) (*types.Transaction, error) {
	return _Bridgemessagereceivermock.Contract.UpdateParameters(&_Bridgemessagereceivermock.TransactOpts, msmtProofLocalExitRoot, msmtProofRollupExitRoot, mglobalIndex, mmainnetExitRoot, mrollupExitRoot, moriginNetwork, moriginAddress, mdestinationNetwork, mdestinationAddress, mamount, mmetadata)
}

// BridgemessagereceivermockMessageReceivedIterator is returned from FilterMessageReceived and is used to iterate over the raw logs and unpacked data for MessageReceived events raised by the Bridgemessagereceivermock contract.
type BridgemessagereceivermockMessageReceivedIterator struct {
	Event *BridgemessagereceivermockMessageReceived // Event containing the contract specifics and raw log

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
func (it *BridgemessagereceivermockMessageReceivedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(BridgemessagereceivermockMessageReceived)
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
		it.Event = new(BridgemessagereceivermockMessageReceived)
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
func (it *BridgemessagereceivermockMessageReceivedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *BridgemessagereceivermockMessageReceivedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// BridgemessagereceivermockMessageReceived represents a MessageReceived event raised by the Bridgemessagereceivermock contract.
type BridgemessagereceivermockMessageReceived struct {
	DestinationAddress common.Address
	Raw                types.Log // Blockchain specific contextual infos
}

// FilterMessageReceived is a free log retrieval operation binding the contract event 0xdf9f4a3ac608a3edf2b45dafa2b30a40073df2a24c06756d4a68210b7de0a8b8.
//
// Solidity: event MessageReceived(address destinationAddress)
func (_Bridgemessagereceivermock *BridgemessagereceivermockFilterer) FilterMessageReceived(opts *bind.FilterOpts) (*BridgemessagereceivermockMessageReceivedIterator, error) {

	logs, sub, err := _Bridgemessagereceivermock.contract.FilterLogs(opts, "MessageReceived")
	if err != nil {
		return nil, err
	}
	return &BridgemessagereceivermockMessageReceivedIterator{contract: _Bridgemessagereceivermock.contract, event: "MessageReceived", logs: logs, sub: sub}, nil
}

// WatchMessageReceived is a free log subscription operation binding the contract event 0xdf9f4a3ac608a3edf2b45dafa2b30a40073df2a24c06756d4a68210b7de0a8b8.
//
// Solidity: event MessageReceived(address destinationAddress)
func (_Bridgemessagereceivermock *BridgemessagereceivermockFilterer) WatchMessageReceived(opts *bind.WatchOpts, sink chan<- *BridgemessagereceivermockMessageReceived) (event.Subscription, error) {

	logs, sub, err := _Bridgemessagereceivermock.contract.WatchLogs(opts, "MessageReceived")
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(BridgemessagereceivermockMessageReceived)
				if err := _Bridgemessagereceivermock.contract.UnpackLog(event, "MessageReceived", log); err != nil {
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

// ParseMessageReceived is a log parse operation binding the contract event 0xdf9f4a3ac608a3edf2b45dafa2b30a40073df2a24c06756d4a68210b7de0a8b8.
//
// Solidity: event MessageReceived(address destinationAddress)
func (_Bridgemessagereceivermock *BridgemessagereceivermockFilterer) ParseMessageReceived(log types.Log) (*BridgemessagereceivermockMessageReceived, error) {
	event := new(BridgemessagereceivermockMessageReceived)
	if err := _Bridgemessagereceivermock.contract.UnpackLog(event, "MessageReceived", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// BridgemessagereceivermockUpdateParametersIterator is returned from FilterUpdateParameters and is used to iterate over the raw logs and unpacked data for UpdateParameters events raised by the Bridgemessagereceivermock contract.
type BridgemessagereceivermockUpdateParametersIterator struct {
	Event *BridgemessagereceivermockUpdateParameters // Event containing the contract specifics and raw log

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
func (it *BridgemessagereceivermockUpdateParametersIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(BridgemessagereceivermockUpdateParameters)
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
		it.Event = new(BridgemessagereceivermockUpdateParameters)
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
func (it *BridgemessagereceivermockUpdateParametersIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *BridgemessagereceivermockUpdateParametersIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// BridgemessagereceivermockUpdateParameters represents a UpdateParameters event raised by the Bridgemessagereceivermock contract.
type BridgemessagereceivermockUpdateParameters struct {
	Raw types.Log // Blockchain specific contextual infos
}

// FilterUpdateParameters is a free log retrieval operation binding the contract event 0x9d226db03d4d6614ea01926ce8a588879492a2681b9684eb655b1470d32d4b9e.
//
// Solidity: event UpdateParameters()
func (_Bridgemessagereceivermock *BridgemessagereceivermockFilterer) FilterUpdateParameters(opts *bind.FilterOpts) (*BridgemessagereceivermockUpdateParametersIterator, error) {

	logs, sub, err := _Bridgemessagereceivermock.contract.FilterLogs(opts, "UpdateParameters")
	if err != nil {
		return nil, err
	}
	return &BridgemessagereceivermockUpdateParametersIterator{contract: _Bridgemessagereceivermock.contract, event: "UpdateParameters", logs: logs, sub: sub}, nil
}

// WatchUpdateParameters is a free log subscription operation binding the contract event 0x9d226db03d4d6614ea01926ce8a588879492a2681b9684eb655b1470d32d4b9e.
//
// Solidity: event UpdateParameters()
func (_Bridgemessagereceivermock *BridgemessagereceivermockFilterer) WatchUpdateParameters(opts *bind.WatchOpts, sink chan<- *BridgemessagereceivermockUpdateParameters) (event.Subscription, error) {

	logs, sub, err := _Bridgemessagereceivermock.contract.WatchLogs(opts, "UpdateParameters")
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(BridgemessagereceivermockUpdateParameters)
				if err := _Bridgemessagereceivermock.contract.UnpackLog(event, "UpdateParameters", log); err != nil {
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

// ParseUpdateParameters is a log parse operation binding the contract event 0x9d226db03d4d6614ea01926ce8a588879492a2681b9684eb655b1470d32d4b9e.
//
// Solidity: event UpdateParameters()
func (_Bridgemessagereceivermock *BridgemessagereceivermockFilterer) ParseUpdateParameters(log types.Log) (*BridgemessagereceivermockUpdateParameters, error) {
	event := new(BridgemessagereceivermockUpdateParameters)
	if err := _Bridgemessagereceivermock.contract.UnpackLog(event, "UpdateParameters", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}
