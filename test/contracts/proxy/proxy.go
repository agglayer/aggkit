// Code generated - DO NOT EDIT.
// This file is a generated binding and any manual changes will be lost.

package proxy

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

// ProxyMetaData contains all meta data concerning the Proxy contract.
var ProxyMetaData = &bind.MetaData{
	ABI: "[{\"inputs\":[{\"internalType\":\"address\",\"name\":\"_logic\",\"type\":\"address\"},{\"internalType\":\"address\",\"name\":\"initialOwner\",\"type\":\"address\"},{\"internalType\":\"bytes\",\"name\":\"_data\",\"type\":\"bytes\"}],\"stateMutability\":\"payable\",\"type\":\"constructor\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"target\",\"type\":\"address\"}],\"name\":\"AddressEmptyCode\",\"type\":\"error\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"admin\",\"type\":\"address\"}],\"name\":\"ERC1967InvalidAdmin\",\"type\":\"error\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"implementation\",\"type\":\"address\"}],\"name\":\"ERC1967InvalidImplementation\",\"type\":\"error\"},{\"inputs\":[],\"name\":\"ERC1967NonPayable\",\"type\":\"error\"},{\"inputs\":[],\"name\":\"FailedCall\",\"type\":\"error\"},{\"inputs\":[],\"name\":\"ProxyDeniedAdminAccess\",\"type\":\"error\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":false,\"internalType\":\"address\",\"name\":\"previousAdmin\",\"type\":\"address\"},{\"indexed\":false,\"internalType\":\"address\",\"name\":\"newAdmin\",\"type\":\"address\"}],\"name\":\"AdminChanged\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":true,\"internalType\":\"address\",\"name\":\"implementation\",\"type\":\"address\"}],\"name\":\"Upgraded\",\"type\":\"event\"},{\"stateMutability\":\"payable\",\"type\":\"fallback\"}]",
	Bin: "0x60a06040526040516119d23803806119d283398181016040528101906100259190610707565b828161003782826100c160201b60201c565b50508160405161004690610553565b6100509190610782565b604051809103905ff080158015610069573d5f5f3e3d5ffd5b5073ffffffffffffffffffffffffffffffffffffffff1660808173ffffffffffffffffffffffffffffffffffffffff16815250506100b96100ae61014560201b60201c565b61014e60201b60201c565b50505061081c565b6100d0826101a660201b60201c565b8173ffffffffffffffffffffffffffffffffffffffff167fbc7cd75a20ee27fd9adebab32041f755214dbc6bffa90cc0225b39da2e5c2d3b60405160405180910390a25f815111156101325761012c828261027560201b60201c565b50610141565b6101406102fb60201b60201c565b5b5050565b5f608051905090565b7f7e644d79422f17c01e4894b5f4f588d331ebfa28653d42ae832dc59e38c9798f61017d61033760201b60201c565b8260405161018c92919061079b565b60405180910390a16101a38161039060201b60201c565b50565b5f8173ffffffffffffffffffffffffffffffffffffffff163b0361020157806040517f4c9c8ce30000000000000000000000000000000000000000000000000000000081526004016101f89190610782565b60405180910390fd5b806102337f360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc5f1b61047460201b60201c565b5f015f6101000a81548173ffffffffffffffffffffffffffffffffffffffff021916908373ffffffffffffffffffffffffffffffffffffffff16021790555050565b60605f5f8473ffffffffffffffffffffffffffffffffffffffff168460405161029e9190610806565b5f60405180830381855af49150503d805f81146102d6576040519150601f19603f3d011682016040523d82523d5f602084013e6102db565b606091505b50915091506102f185838361047d60201b60201c565b9250505092915050565b5f341115610335576040517fb398979f00000000000000000000000000000000000000000000000000000000815260040160405180910390fd5b565b5f6103697fb53127684a568b3173ae13b9f8a6016e243e63b6e8ee1178d6a717850b5d61035f1b61047460201b60201c565b5f015f9054906101000a900473ffffffffffffffffffffffffffffffffffffffff16905090565b5f73ffffffffffffffffffffffffffffffffffffffff168173ffffffffffffffffffffffffffffffffffffffff1603610400575f6040517f62e77ba20000000000000000000000000000000000000000000000000000000081526004016103f79190610782565b60405180910390fd5b806104327fb53127684a568b3173ae13b9f8a6016e243e63b6e8ee1178d6a717850b5d61035f1b61047460201b60201c565b5f015f6101000a81548173ffffffffffffffffffffffffffffffffffffffff021916908373ffffffffffffffffffffffffffffffffffffffff16021790555050565b5f819050919050565b606082610498576104938261051060201b60201c565b610508565b5f82511480156104be57505f8473ffffffffffffffffffffffffffffffffffffffff163b145b1561050057836040517f9996b3150000000000000000000000000000000000000000000000000000000081526004016104f79190610782565b60405180910390fd5b819050610509565b5b9392505050565b5f8151111561052157805160208201fd5b6040517fd6bda27500000000000000000000000000000000000000000000000000000000815260040160405180910390fd5b6109c68061100c83390190565b5f604051905090565b5f5ffd5b5f5ffd5b5f73ffffffffffffffffffffffffffffffffffffffff82169050919050565b5f61059a82610571565b9050919050565b6105aa81610590565b81146105b4575f5ffd5b50565b5f815190506105c5816105a1565b92915050565b5f5ffd5b5f5ffd5b5f601f19601f8301169050919050565b7f4e487b71000000000000000000000000000000000000000000000000000000005f52604160045260245ffd5b610619826105d3565b810181811067ffffffffffffffff82111715610638576106376105e3565b5b80604052505050565b5f61064a610560565b90506106568282610610565b919050565b5f67ffffffffffffffff821115610675576106746105e3565b5b61067e826105d3565b9050602081019050919050565b8281835e5f83830152505050565b5f6106ab6106a68461065b565b610641565b9050828152602081018484840111156106c7576106c66105cf565b5b6106d284828561068b565b509392505050565b5f82601f8301126106ee576106ed6105cb565b5b81516106fe848260208601610699565b91505092915050565b5f5f5f6060848603121561071e5761071d610569565b5b5f61072b868287016105b7565b935050602061073c868287016105b7565b925050604084015167ffffffffffffffff81111561075d5761075c61056d565b5b610769868287016106da565b9150509250925092565b61077c81610590565b82525050565b5f6020820190506107955f830184610773565b92915050565b5f6040820190506107ae5f830185610773565b6107bb6020830184610773565b9392505050565b5f81519050919050565b5f81905092915050565b5f6107e0826107c2565b6107ea81856107cc565b93506107fa81856020860161068b565b80840191505092915050565b5f61081182846107d6565b915081905092915050565b6080516107d86108345f395f61010401526107d85ff3fe608060405261000c61000e565b005b610016610101565b73ffffffffffffffffffffffffffffffffffffffff163373ffffffffffffffffffffffffffffffffffffffff16036100f657634f1ef28660e01b7bffffffffffffffffffffffffffffffffffffffffffffffffffffffff19165f357fffffffff00000000000000000000000000000000000000000000000000000000167bffffffffffffffffffffffffffffffffffffffffffffffffffffffff1916146100e9576040517fd2b576ec00000000000000000000000000000000000000000000000000000000815260040160405180910390fd5b6100f1610128565b6100ff565b6100fe61015c565b5b565b5f7f0000000000000000000000000000000000000000000000000000000000000000905090565b5f5f5f36600490809261013d939291906104cf565b81019061014a91906106a7565b91509150610158828261016e565b5050565b61016c6101676101e0565b6101ee565b565b6101778261020d565b8173ffffffffffffffffffffffffffffffffffffffff167fbc7cd75a20ee27fd9adebab32041f755214dbc6bffa90cc0225b39da2e5c2d3b60405160405180910390a25f815111156101d3576101cd82826102d6565b506101dc565b6101db610356565b5b5050565b5f6101e9610392565b905090565b365f5f375f5f365f845af43d5f5f3e805f8114610209573d5ff35b3d5ffd5b5f8173ffffffffffffffffffffffffffffffffffffffff163b0361026857806040517f4c9c8ce300000000000000000000000000000000000000000000000000000000815260040161025f9190610721565b60405180910390fd5b806102947f360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc5f1b6103e5565b5f015f6101000a81548173ffffffffffffffffffffffffffffffffffffffff021916908373ffffffffffffffffffffffffffffffffffffffff16021790555050565b60605f5f8473ffffffffffffffffffffffffffffffffffffffff16846040516102ff919061078c565b5f60405180830381855af49150503d805f8114610337576040519150601f19603f3d011682016040523d82523d5f602084013e61033c565b606091505b509150915061034c8583836103ee565b9250505092915050565b5f341115610390576040517fb398979f00000000000000000000000000000000000000000000000000000000815260040160405180910390fd5b565b5f6103be7f360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc5f1b6103e5565b5f015f9054906101000a900473ffffffffffffffffffffffffffffffffffffffff16905090565b5f819050919050565b606082610403576103fe8261047b565b610473565b5f825114801561042957505f8473ffffffffffffffffffffffffffffffffffffffff163b145b1561046b57836040517f9996b3150000000000000000000000000000000000000000000000000000000081526004016104629190610721565b60405180910390fd5b819050610474565b5b9392505050565b5f8151111561048c57805160208201fd5b6040517fd6bda27500000000000000000000000000000000000000000000000000000000815260040160405180910390fd5b5f604051905090565b5f5ffd5b5f5ffd5b5f5f858511156104e2576104e16104c7565b5b838611156104f3576104f26104cb565b5b6001850283019150848603905094509492505050565b5f5ffd5b5f5ffd5b5f73ffffffffffffffffffffffffffffffffffffffff82169050919050565b5f61053a82610511565b9050919050565b61054a81610530565b8114610554575f5ffd5b50565b5f8135905061056581610541565b92915050565b5f5ffd5b5f5ffd5b5f601f19601f8301169050919050565b7f4e487b71000000000000000000000000000000000000000000000000000000005f52604160045260245ffd5b6105b982610573565b810181811067ffffffffffffffff821117156105d8576105d7610583565b5b80604052505050565b5f6105ea6104be565b90506105f682826105b0565b919050565b5f67ffffffffffffffff82111561061557610614610583565b5b61061e82610573565b9050602081019050919050565b828183375f83830152505050565b5f61064b610646846105fb565b6105e1565b9050828152602081018484840111156106675761066661056f565b5b61067284828561062b565b509392505050565b5f82601f83011261068e5761068d61056b565b5b813561069e848260208601610639565b91505092915050565b5f5f604083850312156106bd576106bc610509565b5b5f6106ca85828601610557565b925050602083013567ffffffffffffffff8111156106eb576106ea61050d565b5b6106f78582860161067a565b9150509250929050565b5f61070b82610511565b9050919050565b61071b81610701565b82525050565b5f6020820190506107345f830184610712565b92915050565b5f81519050919050565b5f81905092915050565b8281835e5f83830152505050565b5f6107668261073a565b6107708185610744565b935061078081856020860161074e565b80840191505092915050565b5f610797828461075c565b91508190509291505056fea264697066735822122019e3bb6be0c262b470489c9bbaccfafabf7a009d12d1c8b11eb3ff62d10c769264736f6c634300081e0033608060405234801561000f575f5ffd5b506040516109c63803806109c6833981810160405281019061003191906101d7565b805f73ffffffffffffffffffffffffffffffffffffffff168173ffffffffffffffffffffffffffffffffffffffff16036100a2575f6040517f1e4fbdf70000000000000000000000000000000000000000000000000000000081526004016100999190610211565b60405180910390fd5b6100b1816100b860201b60201c565b505061022a565b5f5f5f9054906101000a900473ffffffffffffffffffffffffffffffffffffffff169050815f5f6101000a81548173ffffffffffffffffffffffffffffffffffffffff021916908373ffffffffffffffffffffffffffffffffffffffff1602179055508173ffffffffffffffffffffffffffffffffffffffff168173ffffffffffffffffffffffffffffffffffffffff167f8be0079c531659141344cd1fd0a4f28419497f9722a3daafe3b4186f6b6457e060405160405180910390a35050565b5f5ffd5b5f73ffffffffffffffffffffffffffffffffffffffff82169050919050565b5f6101a68261017d565b9050919050565b6101b68161019c565b81146101c0575f5ffd5b50565b5f815190506101d1816101ad565b92915050565b5f602082840312156101ec576101eb610179565b5b5f6101f9848285016101c3565b91505092915050565b61020b8161019c565b82525050565b5f6020820190506102245f830184610202565b92915050565b61078f806102375f395ff3fe608060405260043610610049575f3560e01c8063715018a61461004d5780638da5cb5b146100635780639623609d1461008d578063ad3cb1cc146100a9578063f2fde38b146100d3575b5f5ffd5b348015610058575f5ffd5b506100616100fb565b005b34801561006e575f5ffd5b5061007761010e565b60405161008491906103f7565b60405180910390f35b6100a760048036038101906100a291906105c2565b610135565b005b3480156100b4575f5ffd5b506100bd6101ac565b6040516100ca919061068e565b60405180910390f35b3480156100de575f5ffd5b506100f960048036038101906100f491906106ae565b6101e5565b005b610103610269565b61010c5f6102f0565b565b5f5f5f9054906101000a900473ffffffffffffffffffffffffffffffffffffffff16905090565b61013d610269565b8273ffffffffffffffffffffffffffffffffffffffff16634f1ef2863484846040518463ffffffff1660e01b815260040161017992919061072b565b5f604051808303818588803b158015610190575f5ffd5b505af11580156101a2573d5f5f3e3d5ffd5b5050505050505050565b6040518060400160405280600581526020017f352e302e3000000000000000000000000000000000000000000000000000000081525081565b6101ed610269565b5f73ffffffffffffffffffffffffffffffffffffffff168173ffffffffffffffffffffffffffffffffffffffff160361025d575f6040517f1e4fbdf700000000000000000000000000000000000000000000000000000000815260040161025491906103f7565b60405180910390fd5b610266816102f0565b50565b6102716103b1565b73ffffffffffffffffffffffffffffffffffffffff1661028f61010e565b73ffffffffffffffffffffffffffffffffffffffff16146102ee576102b26103b1565b6040517f118cdaa70000000000000000000000000000000000000000000000000000000081526004016102e591906103f7565b60405180910390fd5b565b5f5f5f9054906101000a900473ffffffffffffffffffffffffffffffffffffffff169050815f5f6101000a81548173ffffffffffffffffffffffffffffffffffffffff021916908373ffffffffffffffffffffffffffffffffffffffff1602179055508173ffffffffffffffffffffffffffffffffffffffff168173ffffffffffffffffffffffffffffffffffffffff167f8be0079c531659141344cd1fd0a4f28419497f9722a3daafe3b4186f6b6457e060405160405180910390a35050565b5f33905090565b5f73ffffffffffffffffffffffffffffffffffffffff82169050919050565b5f6103e1826103b8565b9050919050565b6103f1816103d7565b82525050565b5f60208201905061040a5f8301846103e8565b92915050565b5f604051905090565b5f5ffd5b5f5ffd5b5f61042b826103d7565b9050919050565b61043b81610421565b8114610445575f5ffd5b50565b5f8135905061045681610432565b92915050565b610465816103d7565b811461046f575f5ffd5b50565b5f813590506104808161045c565b92915050565b5f5ffd5b5f5ffd5b5f601f19601f8301169050919050565b7f4e487b71000000000000000000000000000000000000000000000000000000005f52604160045260245ffd5b6104d48261048e565b810181811067ffffffffffffffff821117156104f3576104f261049e565b5b80604052505050565b5f610505610410565b905061051182826104cb565b919050565b5f67ffffffffffffffff8211156105305761052f61049e565b5b6105398261048e565b9050602081019050919050565b828183375f83830152505050565b5f61056661056184610516565b6104fc565b9050828152602081018484840111156105825761058161048a565b5b61058d848285610546565b509392505050565b5f82601f8301126105a9576105a8610486565b5b81356105b9848260208601610554565b91505092915050565b5f5f5f606084860312156105d9576105d8610419565b5b5f6105e686828701610448565b93505060206105f786828701610472565b925050604084013567ffffffffffffffff8111156106185761061761041d565b5b61062486828701610595565b9150509250925092565b5f81519050919050565b5f82825260208201905092915050565b8281835e5f83830152505050565b5f6106608261062e565b61066a8185610638565b935061067a818560208601610648565b6106838161048e565b840191505092915050565b5f6020820190508181035f8301526106a68184610656565b905092915050565b5f602082840312156106c3576106c2610419565b5b5f6106d084828501610472565b91505092915050565b5f81519050919050565b5f82825260208201905092915050565b5f6106fd826106d9565b61070781856106e3565b9350610717818560208601610648565b6107208161048e565b840191505092915050565b5f60408201905061073e5f8301856103e8565b818103602083015261075081846106f3565b9050939250505056fea26469706673582212203d7fa3469b6b7940d80599b447ae6cff4f5eb0a539fc07d0a91bc8c9c516385e64736f6c634300081e0033",
}

// ProxyABI is the input ABI used to generate the binding from.
// Deprecated: Use ProxyMetaData.ABI instead.
var ProxyABI = ProxyMetaData.ABI

// ProxyBin is the compiled bytecode used for deploying new contracts.
// Deprecated: Use ProxyMetaData.Bin instead.
var ProxyBin = ProxyMetaData.Bin

// DeployProxy deploys a new Ethereum contract, binding an instance of Proxy to it.
func DeployProxy(auth *bind.TransactOpts, backend bind.ContractBackend, _logic common.Address, initialOwner common.Address, _data []byte) (common.Address, *types.Transaction, *Proxy, error) {
	parsed, err := ProxyMetaData.GetAbi()
	if err != nil {
		return common.Address{}, nil, nil, err
	}
	if parsed == nil {
		return common.Address{}, nil, nil, errors.New("GetABI returned nil")
	}

	address, tx, contract, err := bind.DeployContract(auth, *parsed, common.FromHex(ProxyBin), backend, _logic, initialOwner, _data)
	if err != nil {
		return common.Address{}, nil, nil, err
	}
	return address, tx, &Proxy{ProxyCaller: ProxyCaller{contract: contract}, ProxyTransactor: ProxyTransactor{contract: contract}, ProxyFilterer: ProxyFilterer{contract: contract}}, nil
}

// Proxy is an auto generated Go binding around an Ethereum contract.
type Proxy struct {
	ProxyCaller     // Read-only binding to the contract
	ProxyTransactor // Write-only binding to the contract
	ProxyFilterer   // Log filterer for contract events
}

// ProxyCaller is an auto generated read-only Go binding around an Ethereum contract.
type ProxyCaller struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// ProxyTransactor is an auto generated write-only Go binding around an Ethereum contract.
type ProxyTransactor struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// ProxyFilterer is an auto generated log filtering Go binding around an Ethereum contract events.
type ProxyFilterer struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// ProxySession is an auto generated Go binding around an Ethereum contract,
// with pre-set call and transact options.
type ProxySession struct {
	Contract     *Proxy            // Generic contract binding to set the session for
	CallOpts     bind.CallOpts     // Call options to use throughout this session
	TransactOpts bind.TransactOpts // Transaction auth options to use throughout this session
}

// ProxyCallerSession is an auto generated read-only Go binding around an Ethereum contract,
// with pre-set call options.
type ProxyCallerSession struct {
	Contract *ProxyCaller  // Generic contract caller binding to set the session for
	CallOpts bind.CallOpts // Call options to use throughout this session
}

// ProxyTransactorSession is an auto generated write-only Go binding around an Ethereum contract,
// with pre-set transact options.
type ProxyTransactorSession struct {
	Contract     *ProxyTransactor  // Generic contract transactor binding to set the session for
	TransactOpts bind.TransactOpts // Transaction auth options to use throughout this session
}

// ProxyRaw is an auto generated low-level Go binding around an Ethereum contract.
type ProxyRaw struct {
	Contract *Proxy // Generic contract binding to access the raw methods on
}

// ProxyCallerRaw is an auto generated low-level read-only Go binding around an Ethereum contract.
type ProxyCallerRaw struct {
	Contract *ProxyCaller // Generic read-only contract binding to access the raw methods on
}

// ProxyTransactorRaw is an auto generated low-level write-only Go binding around an Ethereum contract.
type ProxyTransactorRaw struct {
	Contract *ProxyTransactor // Generic write-only contract binding to access the raw methods on
}

// NewProxy creates a new instance of Proxy, bound to a specific deployed contract.
func NewProxy(address common.Address, backend bind.ContractBackend) (*Proxy, error) {
	contract, err := bindProxy(address, backend, backend, backend)
	if err != nil {
		return nil, err
	}
	return &Proxy{ProxyCaller: ProxyCaller{contract: contract}, ProxyTransactor: ProxyTransactor{contract: contract}, ProxyFilterer: ProxyFilterer{contract: contract}}, nil
}

// NewProxyCaller creates a new read-only instance of Proxy, bound to a specific deployed contract.
func NewProxyCaller(address common.Address, caller bind.ContractCaller) (*ProxyCaller, error) {
	contract, err := bindProxy(address, caller, nil, nil)
	if err != nil {
		return nil, err
	}
	return &ProxyCaller{contract: contract}, nil
}

// NewProxyTransactor creates a new write-only instance of Proxy, bound to a specific deployed contract.
func NewProxyTransactor(address common.Address, transactor bind.ContractTransactor) (*ProxyTransactor, error) {
	contract, err := bindProxy(address, nil, transactor, nil)
	if err != nil {
		return nil, err
	}
	return &ProxyTransactor{contract: contract}, nil
}

// NewProxyFilterer creates a new log filterer instance of Proxy, bound to a specific deployed contract.
func NewProxyFilterer(address common.Address, filterer bind.ContractFilterer) (*ProxyFilterer, error) {
	contract, err := bindProxy(address, nil, nil, filterer)
	if err != nil {
		return nil, err
	}
	return &ProxyFilterer{contract: contract}, nil
}

// bindProxy binds a generic wrapper to an already deployed contract.
func bindProxy(address common.Address, caller bind.ContractCaller, transactor bind.ContractTransactor, filterer bind.ContractFilterer) (*bind.BoundContract, error) {
	parsed, err := ProxyMetaData.GetAbi()
	if err != nil {
		return nil, err
	}
	return bind.NewBoundContract(address, *parsed, caller, transactor, filterer), nil
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_Proxy *ProxyRaw) Call(opts *bind.CallOpts, result *[]interface{}, method string, params ...interface{}) error {
	return _Proxy.Contract.ProxyCaller.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_Proxy *ProxyRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _Proxy.Contract.ProxyTransactor.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_Proxy *ProxyRaw) Transact(opts *bind.TransactOpts, method string, params ...interface{}) (*types.Transaction, error) {
	return _Proxy.Contract.ProxyTransactor.contract.Transact(opts, method, params...)
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_Proxy *ProxyCallerRaw) Call(opts *bind.CallOpts, result *[]interface{}, method string, params ...interface{}) error {
	return _Proxy.Contract.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_Proxy *ProxyTransactorRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _Proxy.Contract.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_Proxy *ProxyTransactorRaw) Transact(opts *bind.TransactOpts, method string, params ...interface{}) (*types.Transaction, error) {
	return _Proxy.Contract.contract.Transact(opts, method, params...)
}

// Fallback is a paid mutator transaction binding the contract fallback function.
//
// Solidity: fallback() payable returns()
func (_Proxy *ProxyTransactor) Fallback(opts *bind.TransactOpts, calldata []byte) (*types.Transaction, error) {
	return _Proxy.contract.RawTransact(opts, calldata)
}

// Fallback is a paid mutator transaction binding the contract fallback function.
//
// Solidity: fallback() payable returns()
func (_Proxy *ProxySession) Fallback(calldata []byte) (*types.Transaction, error) {
	return _Proxy.Contract.Fallback(&_Proxy.TransactOpts, calldata)
}

// Fallback is a paid mutator transaction binding the contract fallback function.
//
// Solidity: fallback() payable returns()
func (_Proxy *ProxyTransactorSession) Fallback(calldata []byte) (*types.Transaction, error) {
	return _Proxy.Contract.Fallback(&_Proxy.TransactOpts, calldata)
}

// ProxyAdminChangedIterator is returned from FilterAdminChanged and is used to iterate over the raw logs and unpacked data for AdminChanged events raised by the Proxy contract.
type ProxyAdminChangedIterator struct {
	Event *ProxyAdminChanged // Event containing the contract specifics and raw log

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
func (it *ProxyAdminChangedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(ProxyAdminChanged)
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
		it.Event = new(ProxyAdminChanged)
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
func (it *ProxyAdminChangedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *ProxyAdminChangedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// ProxyAdminChanged represents a AdminChanged event raised by the Proxy contract.
type ProxyAdminChanged struct {
	PreviousAdmin common.Address
	NewAdmin      common.Address
	Raw           types.Log // Blockchain specific contextual infos
}

// FilterAdminChanged is a free log retrieval operation binding the contract event 0x7e644d79422f17c01e4894b5f4f588d331ebfa28653d42ae832dc59e38c9798f.
//
// Solidity: event AdminChanged(address previousAdmin, address newAdmin)
func (_Proxy *ProxyFilterer) FilterAdminChanged(opts *bind.FilterOpts) (*ProxyAdminChangedIterator, error) {

	logs, sub, err := _Proxy.contract.FilterLogs(opts, "AdminChanged")
	if err != nil {
		return nil, err
	}
	return &ProxyAdminChangedIterator{contract: _Proxy.contract, event: "AdminChanged", logs: logs, sub: sub}, nil
}

// WatchAdminChanged is a free log subscription operation binding the contract event 0x7e644d79422f17c01e4894b5f4f588d331ebfa28653d42ae832dc59e38c9798f.
//
// Solidity: event AdminChanged(address previousAdmin, address newAdmin)
func (_Proxy *ProxyFilterer) WatchAdminChanged(opts *bind.WatchOpts, sink chan<- *ProxyAdminChanged) (event.Subscription, error) {

	logs, sub, err := _Proxy.contract.WatchLogs(opts, "AdminChanged")
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(ProxyAdminChanged)
				if err := _Proxy.contract.UnpackLog(event, "AdminChanged", log); err != nil {
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

// ParseAdminChanged is a log parse operation binding the contract event 0x7e644d79422f17c01e4894b5f4f588d331ebfa28653d42ae832dc59e38c9798f.
//
// Solidity: event AdminChanged(address previousAdmin, address newAdmin)
func (_Proxy *ProxyFilterer) ParseAdminChanged(log types.Log) (*ProxyAdminChanged, error) {
	event := new(ProxyAdminChanged)
	if err := _Proxy.contract.UnpackLog(event, "AdminChanged", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// ProxyUpgradedIterator is returned from FilterUpgraded and is used to iterate over the raw logs and unpacked data for Upgraded events raised by the Proxy contract.
type ProxyUpgradedIterator struct {
	Event *ProxyUpgraded // Event containing the contract specifics and raw log

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
func (it *ProxyUpgradedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(ProxyUpgraded)
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
		it.Event = new(ProxyUpgraded)
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
func (it *ProxyUpgradedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *ProxyUpgradedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// ProxyUpgraded represents a Upgraded event raised by the Proxy contract.
type ProxyUpgraded struct {
	Implementation common.Address
	Raw            types.Log // Blockchain specific contextual infos
}

// FilterUpgraded is a free log retrieval operation binding the contract event 0xbc7cd75a20ee27fd9adebab32041f755214dbc6bffa90cc0225b39da2e5c2d3b.
//
// Solidity: event Upgraded(address indexed implementation)
func (_Proxy *ProxyFilterer) FilterUpgraded(opts *bind.FilterOpts, implementation []common.Address) (*ProxyUpgradedIterator, error) {

	var implementationRule []interface{}
	for _, implementationItem := range implementation {
		implementationRule = append(implementationRule, implementationItem)
	}

	logs, sub, err := _Proxy.contract.FilterLogs(opts, "Upgraded", implementationRule)
	if err != nil {
		return nil, err
	}
	return &ProxyUpgradedIterator{contract: _Proxy.contract, event: "Upgraded", logs: logs, sub: sub}, nil
}

// WatchUpgraded is a free log subscription operation binding the contract event 0xbc7cd75a20ee27fd9adebab32041f755214dbc6bffa90cc0225b39da2e5c2d3b.
//
// Solidity: event Upgraded(address indexed implementation)
func (_Proxy *ProxyFilterer) WatchUpgraded(opts *bind.WatchOpts, sink chan<- *ProxyUpgraded, implementation []common.Address) (event.Subscription, error) {

	var implementationRule []interface{}
	for _, implementationItem := range implementation {
		implementationRule = append(implementationRule, implementationItem)
	}

	logs, sub, err := _Proxy.contract.WatchLogs(opts, "Upgraded", implementationRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(ProxyUpgraded)
				if err := _Proxy.contract.UnpackLog(event, "Upgraded", log); err != nil {
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

// ParseUpgraded is a log parse operation binding the contract event 0xbc7cd75a20ee27fd9adebab32041f755214dbc6bffa90cc0225b39da2e5c2d3b.
//
// Solidity: event Upgraded(address indexed implementation)
func (_Proxy *ProxyFilterer) ParseUpgraded(log types.Log) (*ProxyUpgraded, error) {
	event := new(ProxyUpgraded)
	if err := _Proxy.contract.UnpackLog(event, "Upgraded", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}
