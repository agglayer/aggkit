// Code generated - DO NOT EDIT.
// This file is a generated binding and any manual changes will be lost.

package internalclaims

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

// InternalclaimsMetaData contains all meta data concerning the Internalclaims contract.
var InternalclaimsMetaData = &bind.MetaData{
	ABI: "[{\"type\":\"constructor\",\"inputs\":[{\"name\":\"_bridgeAddress\",\"type\":\"address\",\"internalType\":\"contractIPolygonZkEVMBridgeV2\"}],\"stateMutability\":\"nonpayable\"},{\"type\":\"function\",\"name\":\"bridgeAddress\",\"inputs\":[],\"outputs\":[{\"name\":\"\",\"type\":\"address\",\"internalType\":\"contractIPolygonZkEVMBridgeV2\"}],\"stateMutability\":\"view\"},{\"type\":\"function\",\"name\":\"onMessageReceived\",\"inputs\":[{\"name\":\"originAddress1\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"originNetwork1\",\"type\":\"uint32\",\"internalType\":\"uint32\"},{\"name\":\"data1\",\"type\":\"bytes\",\"internalType\":\"bytes\"}],\"outputs\":[],\"stateMutability\":\"payable\"},{\"type\":\"function\",\"name\":\"updateParameters\",\"inputs\":[{\"name\":\"msmtProofLocalExitRoot1\",\"type\":\"bytes32[32]\",\"internalType\":\"bytes32[32]\"},{\"name\":\"msmtProofRollupExitRoot1\",\"type\":\"bytes32[32]\",\"internalType\":\"bytes32[32]\"},{\"name\":\"mglobalIndex1\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"mmainnetExitRoot1\",\"type\":\"bytes32\",\"internalType\":\"bytes32\"},{\"name\":\"mrollupExitRoot1\",\"type\":\"bytes32\",\"internalType\":\"bytes32\"},{\"name\":\"moriginNetwork1\",\"type\":\"uint32\",\"internalType\":\"uint32\"},{\"name\":\"moriginAddress1\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"mdestinationNetwork1\",\"type\":\"uint32\",\"internalType\":\"uint32\"},{\"name\":\"mdestinationAddress1\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"mamount1\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"mmetadata1\",\"type\":\"bytes\",\"internalType\":\"bytes\"},{\"name\":\"msmtProofLocalExitRoot2\",\"type\":\"bytes32[32]\",\"internalType\":\"bytes32[32]\"},{\"name\":\"msmtProofRollupExitRoot2\",\"type\":\"bytes32[32]\",\"internalType\":\"bytes32[32]\"},{\"name\":\"mglobalIndex2\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"mmainnetExitRoot2\",\"type\":\"bytes32\",\"internalType\":\"bytes32\"},{\"name\":\"mrollupExitRoot2\",\"type\":\"bytes32\",\"internalType\":\"bytes32\"},{\"name\":\"moriginNetwork2\",\"type\":\"uint32\",\"internalType\":\"uint32\"},{\"name\":\"moriginAddress2\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"mdestinationNetwork2\",\"type\":\"uint32\",\"internalType\":\"uint32\"},{\"name\":\"mdestinationAddress2\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"mamount2\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"mmetadata2\",\"type\":\"bytes\",\"internalType\":\"bytes\"},{\"name\":\"msmtProofLocalExitRoot3\",\"type\":\"bytes32[32]\",\"internalType\":\"bytes32[32]\"},{\"name\":\"msmtProofRollupExitRoot3\",\"type\":\"bytes32[32]\",\"internalType\":\"bytes32[32]\"},{\"name\":\"mglobalIndex3\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"mmainnetExitRoot3\",\"type\":\"bytes32\",\"internalType\":\"bytes32\"},{\"name\":\"mrollupExitRoot3\",\"type\":\"bytes32\",\"internalType\":\"bytes32\"},{\"name\":\"moriginNetwork3\",\"type\":\"uint32\",\"internalType\":\"uint32\"},{\"name\":\"moriginAddress3\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"mdestinationNetwork3\",\"type\":\"uint32\",\"internalType\":\"uint32\"},{\"name\":\"mdestinationAddress3\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"mamount3\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"mmetadata3\",\"type\":\"bytes\",\"internalType\":\"bytes\"},{\"name\":\"msmtProofLocalExitRoot4\",\"type\":\"bytes32[32]\",\"internalType\":\"bytes32[32]\"},{\"name\":\"msmtProofRollupExitRoot4\",\"type\":\"bytes32[32]\",\"internalType\":\"bytes32[32]\"},{\"name\":\"mglobalIndex4\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"mmainnetExitRoot4\",\"type\":\"bytes32\",\"internalType\":\"bytes32\"},{\"name\":\"mrollupExitRoot4\",\"type\":\"bytes32\",\"internalType\":\"bytes32\"},{\"name\":\"moriginNetwork4\",\"type\":\"uint32\",\"internalType\":\"uint32\"},{\"name\":\"moriginAddress4\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"mdestinationNetwork4\",\"type\":\"uint32\",\"internalType\":\"uint32\"},{\"name\":\"mdestinationAddress4\",\"type\":\"address\",\"internalType\":\"address\"},{\"name\":\"mamount4\",\"type\":\"uint256\",\"internalType\":\"uint256\"},{\"name\":\"mmetadata4\",\"type\":\"bytes\",\"internalType\":\"bytes\"}],\"outputs\":[],\"stateMutability\":\"nonpayable\"},{\"type\":\"event\",\"name\":\"MessageReceived\",\"inputs\":[{\"name\":\"destinationAddress\",\"type\":\"address\",\"indexed\":false,\"internalType\":\"address\"}],\"anonymous\":false},{\"type\":\"event\",\"name\":\"UpdateParameters\",\"inputs\":[],\"anonymous\":false}]",
	Bin: "0x60a034606c57601f6114b438819003918201601f19168301916001600160401b03831184841017607057808492602094604052833981010312606c57516001600160a01b0381168103606c5760805260405161142f90816100858239608051818181605b0152610c710152f35b5f80fd5b634e487b7160e01b5f52604160045260245ffdfe610240806040526004361015610013575f80fd5b5f610220525f3560e01c9081631806b5f214610b6c5750806377ff9f0b146100915763a3c573eb14610043575f80fd5b3461008a576102205136600319011261008a576040517f00000000000000000000000000000000000000000000000000000000000000006001600160a01b03168152602090f35b6102205180fd5b3461008a5761248036600319011261008a57366104041161008a57366108041161008a57610864356101c081905263ffffffff8116900361008a57610884356101408190526001600160a01b038116900361008a576108a43561010081905263ffffffff8116900361008a576108c4356102008190526001600160a01b038116900361008a57610904356001600160401b03811161008a5761013790369060040161136d565b9036610d241161008a57366111241161008a57611184359163ffffffff8316830361008a576111a435926001600160a01b038416840361008a576111c4359063ffffffff8216820361008a576111e4356001600160a01b038116810361008a57611224356001600160401b03811161008a576101b790369060040161136d565b929096366116441161008a5736611a441161008a57611aa435608081905263ffffffff8116900361008a57611ac435946001600160a01b038616860361008a57611ae4359663ffffffff8816880361008a57611b043560a08190526001600160a01b038116900361008a57611b44356001600160401b03811161008a5761024290369060040161136d565b6101605260e05236611f641161008a57366123641161008a576123c4356101e081905263ffffffff8116900361008a576123e4356101a08190526001600160a01b038116900361008a576124043561018081905263ffffffff8116900361008a57612424356101208190526001600160a01b038116900361008a57612464356001600160401b03811161008a576102dd90369060040161136d565b60c052986004610220515b60208110610b5a575050610404610220515b60208110610b4557505061080435604055610824356041556108443560425560435463ffffffff60c01b6101005160c01b169063ffffffff6101c051169063ffffffff60e01b1617640100000000600160c01b036101405160201b16171760435560018060a01b0361020051166001600160601b0360a01b60445416176044556108e4356045556001600160401b0382116107ef5761039a60465461139a565b601f8111610af6575b506102205190601f8311600114610a6a579180916103d9936102205192610a5f575b50508160011b915f199060031b1c19161790565b6046555b610924610220515b60208110610a4a575050610d24610220515b60208110610a3557505061112435608755611144356088556111643560895563ffffffff608a54918160c01b9060c01b1693169063ffffffff60e01b161790640100000000600160c01b039060201b161717608a5560018060a01b03166001600160601b0360a01b608b541617608b5561120435608c556001600160401b0381116107ef57610487608d5461139a565b601f81116109e6575b5061022051601f821160011461095e5781906104c69394959661022051926109535750508160011b915f199060031b1c19161790565b608d555b611244610220515b6020811061093e575050611644610220515b60208110610929575050611a443560ce55611a643560cf55611a843560d05560d1549163ffffffff60c01b9060c01b169163ffffffff608051169063ffffffff60e01b161790640100000000600160c01b039060201b16171760d15560018060a01b0360a051166001600160601b0360a01b60d254161760d255611b243560d3556001600160401b0361016051116107ef5761058160d45461139a565b601f81116108d4575b5061022051601f6101605111600114610840576105c790610220519061016051610833575b5061016051908160011b915f199060031b1c19161790565b60d4555b611b64610220515b6020811061081e5782611f64610220515b602081106108095782612364356101155561238435610116556123a435610117556101185463ffffffff60c01b6101805160c01b169063ffffffff6101e051169063ffffffff60e01b1617640100000000600160c01b036101a05160201b1617176101185560018060a01b0361012051166001600160601b0360a01b610119541617610119556124443561011a556001600160401b0360c051116107ef5761068e61011b5461139a565b601f811161079b575b506102205190601f60c05111600114610714576106d391610220519160c051610709575b505060c051908160011b915f199060031b1c19161790565b61011b555b7f9d226db03d4d6614ea01926ce8a588879492a2681b9684eb655b1470d32d4b9e6102205161022051a16102205180f35b0135905082806106bb565b601f1960c05116917f1602949b6a84a704c7a81815a36f6a6f1fa73b01dfc68b6dff0fdf23198d7c9092610220515b818110610783575060c05111610768575b505050600160c051811b0161011b556106d8565b5f1960f860c05160031b161c19910135169055808080610754565b91936020600181928787013581550195019201610743565b61011b610220515260206102205120601f60c0510160051c810191602060c051106107e5575b601f0160051c01905b8181106107d75750610697565b6102205181556001016107ca565b90915081906107c1565b634e487b7160e01b61022051526041600452602461022051fd5b600190602083359301928160f50155016105e4565b600190602083359301928160d50155016105d3565b905060e0510135836105af565b601f1961016051169060d461022051527f9780e26d96b1f2a9a18ef8fc72d589dbf03ef788137b64f43897e83a91e7feec91610220515b8181106108ba5750610160511161089c575b5050600161016051811b0160d4556105cb565b5f1960f86101605160031b161c199060e05101351690558180610889565b91926020600181928660e051013581550194019201610877565b60d4610220515260206102205120601f610160510160051c8101916020610160511061091f575b601f0160051c01905b818110610911575061058a565b610220518155600101610904565b90915081906108fb565b600190602083359301928160ae0155016104e4565b6001906020833593019281608e0155016104d2565b0135905086806103c5565b601f19821695608d61022051527f37a1be2a88dadcd0e6062f54ddcc01a03360ba61ca7784a744e757488bf8ceb291610220515b8881106109ce575083600195969798106109b5575b505050811b01608d556104ca565b01355f19600384901b60f8161c191690558580806109a7565b90926020600181928686013581550194019101610992565b608d610220515260206102205120601f830160051c81019160208410610a2b575b601f0160051c01905b818110610a1d5750610490565b610220518155600101610a10565b9091508190610a07565b600190602083359301928160670155016103f7565b600190602083359301928160470155016103e5565b013590508c806103c5565b604661022093929351527f128667f541fed74a8429f9d592c26c2c6a4beb9ae5ead9912c98b2595c8423109061022051935b601f1984168510610ade576001945083601f19811610610ac5575b505050811b016046556103dd565b01355f19600384901b60f8161c191690558b8080610ab7565b81810135835560209485019460019093019201610a9c565b6046610220515260206102205120601f840160051c81019160208510610b3b575b601f0160051c01905b818110610b2d57506103a3565b610220518155600101610b20565b9091508190610b17565b600190602083359301928160200155016102fa565b600190602083359301928155016102e8565b6060366003190112611242576004356001600160a01b03811690819003611242576024359063ffffffff8216809203611242576044356001600160401b0381116112425736602382011215611242578060040135906001600160401b03821161133857610be3601f8301601f19166020018661134c565b818552366024838301011161124257815f92602460209301838801378501015282516001600160401b03811161133857610c1f61011c5461139a565b601f81116112d4575b506020601f8211600114611251578190610c589394955f926112465750508160011b915f199060031b1c19161790565b61011c555b6040546041546042546043546044546045547f00000000000000000000000000000000000000000000000000000000000000006001600160a01b03908116989697919692169460c09390931c63ffffffff16939092883b156112425760405163ccaa2d1160e01b815297610cd460048a015f6113d2565b610ce36104048a0160206113d2565b6108048901526108248801526108448701526108648601526108848501526108a48401526108c48301526108e48201526109206109048201525f81604654610d2a8161139a565b90816109248401526001811690815f1461121f57506001146111c3575b505f918190038183865af16111ae575b5060875460885460895490608a549060018060a01b03608b541691608c5493863b1561008a5760405163ccaa2d1160e01b81529563ffffffff9390610da06004890160476113d2565b610daf610404890160676113d2565b610804880152610824870152610844860152808216610864860152602081901c6001600160a01b031661088486015260c01c166108a48401526108c48301526108e482015261092061090482015261022051608d54610e0d8161139a565b90816109248501526001811690815f1461118b5750600114611147575b5081806102205192038161022051865af161112c575b5060ce5460cf5460d0549060d1549060018060a01b0360d254169160d35493863b1561008a5760405163ccaa2d1160e01b81529563ffffffff9390610e8960048901608e6113d2565b610e98610404890160ae6113d2565b610804880152610824870152610844860152808216610864860152602081901c6001600160a01b031661088486015260c01c166108a48401526108c48301526108e48201526109206109048201526102205160d454610ef68161139a565b90816109248501526001811690815f1461110957506001146110c5575b5081806102205192038161022051865af16110aa575b506101155490610116546101175490610118549060018060a01b0361011954169161011a5493853b1561008a5760405163ccaa2d1160e01b81529663ffffffff9390610f7960048a0160d56113d2565b610f886104048a0160f56113d2565b610804890152610824880152610844870152808216610864870152602081901c6001600160a01b031661088487015260c01c166108a48501526108c48401526108e48301526109206109048301526102205161011b5490918391610feb8161139a565b90816109248501526001811690815f146110855750600114611040575b5081806102205194039161022051905af1611025575b6102205180f35b610220516110329161134c565b6102205161008a578061101e565b610220805161011b90525160208120929450915b81831061106a5750508101610944019184611008565b80548387016109440152859350602090920191600101611054565b610940939550600492915060ff1916610944850152151560051b830101019184611008565b610220516110b79161134c565b6102205161008a5781610f29565b905060d461022051526020610220512061022051905b8282106110f15750820161094401905083610f13565b805482850161094401526020909101906001016110db565b6109409350600492915060ff1916610944850152151560051b8301010183610f13565b610220516111399161134c565b6102205161008a5781610e40565b9050608d61022051526020610220512061022051905b8282106111735750820161094401905083610e2a565b8054828501610944015260209091019060010161115d565b6109409350600492915060ff1916610944850152151560051b8301010183610e2a565b5f6111b89161134c565b5f6102205281610d57565b60465f9081529192507f128667f541fed74a8429f9d592c26c2c6a4beb9ae5ead9912c98b2595c8423105b8183106112045750508101610944019080610d47565b805483860161094401528493506020909201916001016111ee565b60ff19166109448085019190915291151560051b83019091019250819050610d47565b5f80fd5b0151905085806103c5565b601f1982169061011c5f527fadd86a4592312086270d45a94ec1626035e36f7028a1706ab88a5151613929fe915f5b8181106112bc575095836001959697106112a4575b505050811b0161011c55610c5d565b01515f1960f88460031b161c19169055848080611295565b9192602060018192868b015181550194019201611280565b61011c5f527fadd86a4592312086270d45a94ec1626035e36f7028a1706ab88a5151613929fe601f830160051c8101916020841061132e575b601f0160051c01905b8181106113235750610c28565b5f8155600101611316565b909150819061130d565b634e487b7160e01b5f52604160045260245ffd5b90601f801991011681019081106001600160401b0382111761133857604052565b9181601f84011215611242578235916001600160401b038311611242576020838186019501011161124257565b90600182811c921680156113c8575b60208310146113b457565b634e487b7160e01b5f52602260045260245ffd5b91607f16916113a9565b905f905b602082106113e357505050565b60016020819285548152019301910190916113d656fea2646970667358221220a3f55ee78e17bc56cc030dd521e1918cb672ad27695d1623a019cadedf8f234764736f6c634300081c0033",
}

// InternalclaimsABI is the input ABI used to generate the binding from.
// Deprecated: Use InternalclaimsMetaData.ABI instead.
var InternalclaimsABI = InternalclaimsMetaData.ABI

// InternalclaimsBin is the compiled bytecode used for deploying new contracts.
// Deprecated: Use InternalclaimsMetaData.Bin instead.
var InternalclaimsBin = InternalclaimsMetaData.Bin

// DeployInternalclaims deploys a new Ethereum contract, binding an instance of Internalclaims to it.
func DeployInternalclaims(auth *bind.TransactOpts, backend bind.ContractBackend, _bridgeAddress common.Address) (common.Address, *types.Transaction, *Internalclaims, error) {
	parsed, err := InternalclaimsMetaData.GetAbi()
	if err != nil {
		return common.Address{}, nil, nil, err
	}
	if parsed == nil {
		return common.Address{}, nil, nil, errors.New("GetABI returned nil")
	}

	address, tx, contract, err := bind.DeployContract(auth, *parsed, common.FromHex(InternalclaimsBin), backend, _bridgeAddress)
	if err != nil {
		return common.Address{}, nil, nil, err
	}
	return address, tx, &Internalclaims{InternalclaimsCaller: InternalclaimsCaller{contract: contract}, InternalclaimsTransactor: InternalclaimsTransactor{contract: contract}, InternalclaimsFilterer: InternalclaimsFilterer{contract: contract}}, nil
}

// Internalclaims is an auto generated Go binding around an Ethereum contract.
type Internalclaims struct {
	InternalclaimsCaller     // Read-only binding to the contract
	InternalclaimsTransactor // Write-only binding to the contract
	InternalclaimsFilterer   // Log filterer for contract events
}

// InternalclaimsCaller is an auto generated read-only Go binding around an Ethereum contract.
type InternalclaimsCaller struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// InternalclaimsTransactor is an auto generated write-only Go binding around an Ethereum contract.
type InternalclaimsTransactor struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// InternalclaimsFilterer is an auto generated log filtering Go binding around an Ethereum contract events.
type InternalclaimsFilterer struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// InternalclaimsSession is an auto generated Go binding around an Ethereum contract,
// with pre-set call and transact options.
type InternalclaimsSession struct {
	Contract     *Internalclaims   // Generic contract binding to set the session for
	CallOpts     bind.CallOpts     // Call options to use throughout this session
	TransactOpts bind.TransactOpts // Transaction auth options to use throughout this session
}

// InternalclaimsCallerSession is an auto generated read-only Go binding around an Ethereum contract,
// with pre-set call options.
type InternalclaimsCallerSession struct {
	Contract *InternalclaimsCaller // Generic contract caller binding to set the session for
	CallOpts bind.CallOpts         // Call options to use throughout this session
}

// InternalclaimsTransactorSession is an auto generated write-only Go binding around an Ethereum contract,
// with pre-set transact options.
type InternalclaimsTransactorSession struct {
	Contract     *InternalclaimsTransactor // Generic contract transactor binding to set the session for
	TransactOpts bind.TransactOpts         // Transaction auth options to use throughout this session
}

// InternalclaimsRaw is an auto generated low-level Go binding around an Ethereum contract.
type InternalclaimsRaw struct {
	Contract *Internalclaims // Generic contract binding to access the raw methods on
}

// InternalclaimsCallerRaw is an auto generated low-level read-only Go binding around an Ethereum contract.
type InternalclaimsCallerRaw struct {
	Contract *InternalclaimsCaller // Generic read-only contract binding to access the raw methods on
}

// InternalclaimsTransactorRaw is an auto generated low-level write-only Go binding around an Ethereum contract.
type InternalclaimsTransactorRaw struct {
	Contract *InternalclaimsTransactor // Generic write-only contract binding to access the raw methods on
}

// NewInternalclaims creates a new instance of Internalclaims, bound to a specific deployed contract.
func NewInternalclaims(address common.Address, backend bind.ContractBackend) (*Internalclaims, error) {
	contract, err := bindInternalclaims(address, backend, backend, backend)
	if err != nil {
		return nil, err
	}
	return &Internalclaims{InternalclaimsCaller: InternalclaimsCaller{contract: contract}, InternalclaimsTransactor: InternalclaimsTransactor{contract: contract}, InternalclaimsFilterer: InternalclaimsFilterer{contract: contract}}, nil
}

// NewInternalclaimsCaller creates a new read-only instance of Internalclaims, bound to a specific deployed contract.
func NewInternalclaimsCaller(address common.Address, caller bind.ContractCaller) (*InternalclaimsCaller, error) {
	contract, err := bindInternalclaims(address, caller, nil, nil)
	if err != nil {
		return nil, err
	}
	return &InternalclaimsCaller{contract: contract}, nil
}

// NewInternalclaimsTransactor creates a new write-only instance of Internalclaims, bound to a specific deployed contract.
func NewInternalclaimsTransactor(address common.Address, transactor bind.ContractTransactor) (*InternalclaimsTransactor, error) {
	contract, err := bindInternalclaims(address, nil, transactor, nil)
	if err != nil {
		return nil, err
	}
	return &InternalclaimsTransactor{contract: contract}, nil
}

// NewInternalclaimsFilterer creates a new log filterer instance of Internalclaims, bound to a specific deployed contract.
func NewInternalclaimsFilterer(address common.Address, filterer bind.ContractFilterer) (*InternalclaimsFilterer, error) {
	contract, err := bindInternalclaims(address, nil, nil, filterer)
	if err != nil {
		return nil, err
	}
	return &InternalclaimsFilterer{contract: contract}, nil
}

// bindInternalclaims binds a generic wrapper to an already deployed contract.
func bindInternalclaims(address common.Address, caller bind.ContractCaller, transactor bind.ContractTransactor, filterer bind.ContractFilterer) (*bind.BoundContract, error) {
	parsed, err := InternalclaimsMetaData.GetAbi()
	if err != nil {
		return nil, err
	}
	return bind.NewBoundContract(address, *parsed, caller, transactor, filterer), nil
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_Internalclaims *InternalclaimsRaw) Call(opts *bind.CallOpts, result *[]interface{}, method string, params ...interface{}) error {
	return _Internalclaims.Contract.InternalclaimsCaller.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_Internalclaims *InternalclaimsRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _Internalclaims.Contract.InternalclaimsTransactor.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_Internalclaims *InternalclaimsRaw) Transact(opts *bind.TransactOpts, method string, params ...interface{}) (*types.Transaction, error) {
	return _Internalclaims.Contract.InternalclaimsTransactor.contract.Transact(opts, method, params...)
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_Internalclaims *InternalclaimsCallerRaw) Call(opts *bind.CallOpts, result *[]interface{}, method string, params ...interface{}) error {
	return _Internalclaims.Contract.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_Internalclaims *InternalclaimsTransactorRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _Internalclaims.Contract.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_Internalclaims *InternalclaimsTransactorRaw) Transact(opts *bind.TransactOpts, method string, params ...interface{}) (*types.Transaction, error) {
	return _Internalclaims.Contract.contract.Transact(opts, method, params...)
}

// BridgeAddress is a free data retrieval call binding the contract method 0xa3c573eb.
//
// Solidity: function bridgeAddress() view returns(address)
func (_Internalclaims *InternalclaimsCaller) BridgeAddress(opts *bind.CallOpts) (common.Address, error) {
	var out []interface{}
	err := _Internalclaims.contract.Call(opts, &out, "bridgeAddress")

	if err != nil {
		return *new(common.Address), err
	}

	out0 := *abi.ConvertType(out[0], new(common.Address)).(*common.Address)

	return out0, err

}

// BridgeAddress is a free data retrieval call binding the contract method 0xa3c573eb.
//
// Solidity: function bridgeAddress() view returns(address)
func (_Internalclaims *InternalclaimsSession) BridgeAddress() (common.Address, error) {
	return _Internalclaims.Contract.BridgeAddress(&_Internalclaims.CallOpts)
}

// BridgeAddress is a free data retrieval call binding the contract method 0xa3c573eb.
//
// Solidity: function bridgeAddress() view returns(address)
func (_Internalclaims *InternalclaimsCallerSession) BridgeAddress() (common.Address, error) {
	return _Internalclaims.Contract.BridgeAddress(&_Internalclaims.CallOpts)
}

// OnMessageReceived is a paid mutator transaction binding the contract method 0x1806b5f2.
//
// Solidity: function onMessageReceived(address originAddress1, uint32 originNetwork1, bytes data1) payable returns()
func (_Internalclaims *InternalclaimsTransactor) OnMessageReceived(opts *bind.TransactOpts, originAddress1 common.Address, originNetwork1 uint32, data1 []byte) (*types.Transaction, error) {
	return _Internalclaims.contract.Transact(opts, "onMessageReceived", originAddress1, originNetwork1, data1)
}

// OnMessageReceived is a paid mutator transaction binding the contract method 0x1806b5f2.
//
// Solidity: function onMessageReceived(address originAddress1, uint32 originNetwork1, bytes data1) payable returns()
func (_Internalclaims *InternalclaimsSession) OnMessageReceived(originAddress1 common.Address, originNetwork1 uint32, data1 []byte) (*types.Transaction, error) {
	return _Internalclaims.Contract.OnMessageReceived(&_Internalclaims.TransactOpts, originAddress1, originNetwork1, data1)
}

// OnMessageReceived is a paid mutator transaction binding the contract method 0x1806b5f2.
//
// Solidity: function onMessageReceived(address originAddress1, uint32 originNetwork1, bytes data1) payable returns()
func (_Internalclaims *InternalclaimsTransactorSession) OnMessageReceived(originAddress1 common.Address, originNetwork1 uint32, data1 []byte) (*types.Transaction, error) {
	return _Internalclaims.Contract.OnMessageReceived(&_Internalclaims.TransactOpts, originAddress1, originNetwork1, data1)
}

// UpdateParameters is a paid mutator transaction binding the contract method 0x77ff9f0b.
//
// Solidity: function updateParameters(bytes32[32] msmtProofLocalExitRoot1, bytes32[32] msmtProofRollupExitRoot1, uint256 mglobalIndex1, bytes32 mmainnetExitRoot1, bytes32 mrollupExitRoot1, uint32 moriginNetwork1, address moriginAddress1, uint32 mdestinationNetwork1, address mdestinationAddress1, uint256 mamount1, bytes mmetadata1, bytes32[32] msmtProofLocalExitRoot2, bytes32[32] msmtProofRollupExitRoot2, uint256 mglobalIndex2, bytes32 mmainnetExitRoot2, bytes32 mrollupExitRoot2, uint32 moriginNetwork2, address moriginAddress2, uint32 mdestinationNetwork2, address mdestinationAddress2, uint256 mamount2, bytes mmetadata2, bytes32[32] msmtProofLocalExitRoot3, bytes32[32] msmtProofRollupExitRoot3, uint256 mglobalIndex3, bytes32 mmainnetExitRoot3, bytes32 mrollupExitRoot3, uint32 moriginNetwork3, address moriginAddress3, uint32 mdestinationNetwork3, address mdestinationAddress3, uint256 mamount3, bytes mmetadata3, bytes32[32] msmtProofLocalExitRoot4, bytes32[32] msmtProofRollupExitRoot4, uint256 mglobalIndex4, bytes32 mmainnetExitRoot4, bytes32 mrollupExitRoot4, uint32 moriginNetwork4, address moriginAddress4, uint32 mdestinationNetwork4, address mdestinationAddress4, uint256 mamount4, bytes mmetadata4) returns()
func (_Internalclaims *InternalclaimsTransactor) UpdateParameters(opts *bind.TransactOpts, msmtProofLocalExitRoot1 [32][32]byte, msmtProofRollupExitRoot1 [32][32]byte, mglobalIndex1 *big.Int, mmainnetExitRoot1 [32]byte, mrollupExitRoot1 [32]byte, moriginNetwork1 uint32, moriginAddress1 common.Address, mdestinationNetwork1 uint32, mdestinationAddress1 common.Address, mamount1 *big.Int, mmetadata1 []byte, msmtProofLocalExitRoot2 [32][32]byte, msmtProofRollupExitRoot2 [32][32]byte, mglobalIndex2 *big.Int, mmainnetExitRoot2 [32]byte, mrollupExitRoot2 [32]byte, moriginNetwork2 uint32, moriginAddress2 common.Address, mdestinationNetwork2 uint32, mdestinationAddress2 common.Address, mamount2 *big.Int, mmetadata2 []byte, msmtProofLocalExitRoot3 [32][32]byte, msmtProofRollupExitRoot3 [32][32]byte, mglobalIndex3 *big.Int, mmainnetExitRoot3 [32]byte, mrollupExitRoot3 [32]byte, moriginNetwork3 uint32, moriginAddress3 common.Address, mdestinationNetwork3 uint32, mdestinationAddress3 common.Address, mamount3 *big.Int, mmetadata3 []byte, msmtProofLocalExitRoot4 [32][32]byte, msmtProofRollupExitRoot4 [32][32]byte, mglobalIndex4 *big.Int, mmainnetExitRoot4 [32]byte, mrollupExitRoot4 [32]byte, moriginNetwork4 uint32, moriginAddress4 common.Address, mdestinationNetwork4 uint32, mdestinationAddress4 common.Address, mamount4 *big.Int, mmetadata4 []byte) (*types.Transaction, error) {
	return _Internalclaims.contract.Transact(opts, "updateParameters", msmtProofLocalExitRoot1, msmtProofRollupExitRoot1, mglobalIndex1, mmainnetExitRoot1, mrollupExitRoot1, moriginNetwork1, moriginAddress1, mdestinationNetwork1, mdestinationAddress1, mamount1, mmetadata1, msmtProofLocalExitRoot2, msmtProofRollupExitRoot2, mglobalIndex2, mmainnetExitRoot2, mrollupExitRoot2, moriginNetwork2, moriginAddress2, mdestinationNetwork2, mdestinationAddress2, mamount2, mmetadata2, msmtProofLocalExitRoot3, msmtProofRollupExitRoot3, mglobalIndex3, mmainnetExitRoot3, mrollupExitRoot3, moriginNetwork3, moriginAddress3, mdestinationNetwork3, mdestinationAddress3, mamount3, mmetadata3, msmtProofLocalExitRoot4, msmtProofRollupExitRoot4, mglobalIndex4, mmainnetExitRoot4, mrollupExitRoot4, moriginNetwork4, moriginAddress4, mdestinationNetwork4, mdestinationAddress4, mamount4, mmetadata4)
}

// UpdateParameters is a paid mutator transaction binding the contract method 0x77ff9f0b.
//
// Solidity: function updateParameters(bytes32[32] msmtProofLocalExitRoot1, bytes32[32] msmtProofRollupExitRoot1, uint256 mglobalIndex1, bytes32 mmainnetExitRoot1, bytes32 mrollupExitRoot1, uint32 moriginNetwork1, address moriginAddress1, uint32 mdestinationNetwork1, address mdestinationAddress1, uint256 mamount1, bytes mmetadata1, bytes32[32] msmtProofLocalExitRoot2, bytes32[32] msmtProofRollupExitRoot2, uint256 mglobalIndex2, bytes32 mmainnetExitRoot2, bytes32 mrollupExitRoot2, uint32 moriginNetwork2, address moriginAddress2, uint32 mdestinationNetwork2, address mdestinationAddress2, uint256 mamount2, bytes mmetadata2, bytes32[32] msmtProofLocalExitRoot3, bytes32[32] msmtProofRollupExitRoot3, uint256 mglobalIndex3, bytes32 mmainnetExitRoot3, bytes32 mrollupExitRoot3, uint32 moriginNetwork3, address moriginAddress3, uint32 mdestinationNetwork3, address mdestinationAddress3, uint256 mamount3, bytes mmetadata3, bytes32[32] msmtProofLocalExitRoot4, bytes32[32] msmtProofRollupExitRoot4, uint256 mglobalIndex4, bytes32 mmainnetExitRoot4, bytes32 mrollupExitRoot4, uint32 moriginNetwork4, address moriginAddress4, uint32 mdestinationNetwork4, address mdestinationAddress4, uint256 mamount4, bytes mmetadata4) returns()
func (_Internalclaims *InternalclaimsSession) UpdateParameters(msmtProofLocalExitRoot1 [32][32]byte, msmtProofRollupExitRoot1 [32][32]byte, mglobalIndex1 *big.Int, mmainnetExitRoot1 [32]byte, mrollupExitRoot1 [32]byte, moriginNetwork1 uint32, moriginAddress1 common.Address, mdestinationNetwork1 uint32, mdestinationAddress1 common.Address, mamount1 *big.Int, mmetadata1 []byte, msmtProofLocalExitRoot2 [32][32]byte, msmtProofRollupExitRoot2 [32][32]byte, mglobalIndex2 *big.Int, mmainnetExitRoot2 [32]byte, mrollupExitRoot2 [32]byte, moriginNetwork2 uint32, moriginAddress2 common.Address, mdestinationNetwork2 uint32, mdestinationAddress2 common.Address, mamount2 *big.Int, mmetadata2 []byte, msmtProofLocalExitRoot3 [32][32]byte, msmtProofRollupExitRoot3 [32][32]byte, mglobalIndex3 *big.Int, mmainnetExitRoot3 [32]byte, mrollupExitRoot3 [32]byte, moriginNetwork3 uint32, moriginAddress3 common.Address, mdestinationNetwork3 uint32, mdestinationAddress3 common.Address, mamount3 *big.Int, mmetadata3 []byte, msmtProofLocalExitRoot4 [32][32]byte, msmtProofRollupExitRoot4 [32][32]byte, mglobalIndex4 *big.Int, mmainnetExitRoot4 [32]byte, mrollupExitRoot4 [32]byte, moriginNetwork4 uint32, moriginAddress4 common.Address, mdestinationNetwork4 uint32, mdestinationAddress4 common.Address, mamount4 *big.Int, mmetadata4 []byte) (*types.Transaction, error) {
	return _Internalclaims.Contract.UpdateParameters(&_Internalclaims.TransactOpts, msmtProofLocalExitRoot1, msmtProofRollupExitRoot1, mglobalIndex1, mmainnetExitRoot1, mrollupExitRoot1, moriginNetwork1, moriginAddress1, mdestinationNetwork1, mdestinationAddress1, mamount1, mmetadata1, msmtProofLocalExitRoot2, msmtProofRollupExitRoot2, mglobalIndex2, mmainnetExitRoot2, mrollupExitRoot2, moriginNetwork2, moriginAddress2, mdestinationNetwork2, mdestinationAddress2, mamount2, mmetadata2, msmtProofLocalExitRoot3, msmtProofRollupExitRoot3, mglobalIndex3, mmainnetExitRoot3, mrollupExitRoot3, moriginNetwork3, moriginAddress3, mdestinationNetwork3, mdestinationAddress3, mamount3, mmetadata3, msmtProofLocalExitRoot4, msmtProofRollupExitRoot4, mglobalIndex4, mmainnetExitRoot4, mrollupExitRoot4, moriginNetwork4, moriginAddress4, mdestinationNetwork4, mdestinationAddress4, mamount4, mmetadata4)
}

// UpdateParameters is a paid mutator transaction binding the contract method 0x77ff9f0b.
//
// Solidity: function updateParameters(bytes32[32] msmtProofLocalExitRoot1, bytes32[32] msmtProofRollupExitRoot1, uint256 mglobalIndex1, bytes32 mmainnetExitRoot1, bytes32 mrollupExitRoot1, uint32 moriginNetwork1, address moriginAddress1, uint32 mdestinationNetwork1, address mdestinationAddress1, uint256 mamount1, bytes mmetadata1, bytes32[32] msmtProofLocalExitRoot2, bytes32[32] msmtProofRollupExitRoot2, uint256 mglobalIndex2, bytes32 mmainnetExitRoot2, bytes32 mrollupExitRoot2, uint32 moriginNetwork2, address moriginAddress2, uint32 mdestinationNetwork2, address mdestinationAddress2, uint256 mamount2, bytes mmetadata2, bytes32[32] msmtProofLocalExitRoot3, bytes32[32] msmtProofRollupExitRoot3, uint256 mglobalIndex3, bytes32 mmainnetExitRoot3, bytes32 mrollupExitRoot3, uint32 moriginNetwork3, address moriginAddress3, uint32 mdestinationNetwork3, address mdestinationAddress3, uint256 mamount3, bytes mmetadata3, bytes32[32] msmtProofLocalExitRoot4, bytes32[32] msmtProofRollupExitRoot4, uint256 mglobalIndex4, bytes32 mmainnetExitRoot4, bytes32 mrollupExitRoot4, uint32 moriginNetwork4, address moriginAddress4, uint32 mdestinationNetwork4, address mdestinationAddress4, uint256 mamount4, bytes mmetadata4) returns()
func (_Internalclaims *InternalclaimsTransactorSession) UpdateParameters(msmtProofLocalExitRoot1 [32][32]byte, msmtProofRollupExitRoot1 [32][32]byte, mglobalIndex1 *big.Int, mmainnetExitRoot1 [32]byte, mrollupExitRoot1 [32]byte, moriginNetwork1 uint32, moriginAddress1 common.Address, mdestinationNetwork1 uint32, mdestinationAddress1 common.Address, mamount1 *big.Int, mmetadata1 []byte, msmtProofLocalExitRoot2 [32][32]byte, msmtProofRollupExitRoot2 [32][32]byte, mglobalIndex2 *big.Int, mmainnetExitRoot2 [32]byte, mrollupExitRoot2 [32]byte, moriginNetwork2 uint32, moriginAddress2 common.Address, mdestinationNetwork2 uint32, mdestinationAddress2 common.Address, mamount2 *big.Int, mmetadata2 []byte, msmtProofLocalExitRoot3 [32][32]byte, msmtProofRollupExitRoot3 [32][32]byte, mglobalIndex3 *big.Int, mmainnetExitRoot3 [32]byte, mrollupExitRoot3 [32]byte, moriginNetwork3 uint32, moriginAddress3 common.Address, mdestinationNetwork3 uint32, mdestinationAddress3 common.Address, mamount3 *big.Int, mmetadata3 []byte, msmtProofLocalExitRoot4 [32][32]byte, msmtProofRollupExitRoot4 [32][32]byte, mglobalIndex4 *big.Int, mmainnetExitRoot4 [32]byte, mrollupExitRoot4 [32]byte, moriginNetwork4 uint32, moriginAddress4 common.Address, mdestinationNetwork4 uint32, mdestinationAddress4 common.Address, mamount4 *big.Int, mmetadata4 []byte) (*types.Transaction, error) {
	return _Internalclaims.Contract.UpdateParameters(&_Internalclaims.TransactOpts, msmtProofLocalExitRoot1, msmtProofRollupExitRoot1, mglobalIndex1, mmainnetExitRoot1, mrollupExitRoot1, moriginNetwork1, moriginAddress1, mdestinationNetwork1, mdestinationAddress1, mamount1, mmetadata1, msmtProofLocalExitRoot2, msmtProofRollupExitRoot2, mglobalIndex2, mmainnetExitRoot2, mrollupExitRoot2, moriginNetwork2, moriginAddress2, mdestinationNetwork2, mdestinationAddress2, mamount2, mmetadata2, msmtProofLocalExitRoot3, msmtProofRollupExitRoot3, mglobalIndex3, mmainnetExitRoot3, mrollupExitRoot3, moriginNetwork3, moriginAddress3, mdestinationNetwork3, mdestinationAddress3, mamount3, mmetadata3, msmtProofLocalExitRoot4, msmtProofRollupExitRoot4, mglobalIndex4, mmainnetExitRoot4, mrollupExitRoot4, moriginNetwork4, moriginAddress4, mdestinationNetwork4, mdestinationAddress4, mamount4, mmetadata4)
}

// InternalclaimsMessageReceivedIterator is returned from FilterMessageReceived and is used to iterate over the raw logs and unpacked data for MessageReceived events raised by the Internalclaims contract.
type InternalclaimsMessageReceivedIterator struct {
	Event *InternalclaimsMessageReceived // Event containing the contract specifics and raw log

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
func (it *InternalclaimsMessageReceivedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(InternalclaimsMessageReceived)
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
		it.Event = new(InternalclaimsMessageReceived)
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
func (it *InternalclaimsMessageReceivedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *InternalclaimsMessageReceivedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// InternalclaimsMessageReceived represents a MessageReceived event raised by the Internalclaims contract.
type InternalclaimsMessageReceived struct {
	DestinationAddress common.Address
	Raw                types.Log // Blockchain specific contextual infos
}

// FilterMessageReceived is a free log retrieval operation binding the contract event 0xdf9f4a3ac608a3edf2b45dafa2b30a40073df2a24c06756d4a68210b7de0a8b8.
//
// Solidity: event MessageReceived(address destinationAddress)
func (_Internalclaims *InternalclaimsFilterer) FilterMessageReceived(opts *bind.FilterOpts) (*InternalclaimsMessageReceivedIterator, error) {

	logs, sub, err := _Internalclaims.contract.FilterLogs(opts, "MessageReceived")
	if err != nil {
		return nil, err
	}
	return &InternalclaimsMessageReceivedIterator{contract: _Internalclaims.contract, event: "MessageReceived", logs: logs, sub: sub}, nil
}

// WatchMessageReceived is a free log subscription operation binding the contract event 0xdf9f4a3ac608a3edf2b45dafa2b30a40073df2a24c06756d4a68210b7de0a8b8.
//
// Solidity: event MessageReceived(address destinationAddress)
func (_Internalclaims *InternalclaimsFilterer) WatchMessageReceived(opts *bind.WatchOpts, sink chan<- *InternalclaimsMessageReceived) (event.Subscription, error) {

	logs, sub, err := _Internalclaims.contract.WatchLogs(opts, "MessageReceived")
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(InternalclaimsMessageReceived)
				if err := _Internalclaims.contract.UnpackLog(event, "MessageReceived", log); err != nil {
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
func (_Internalclaims *InternalclaimsFilterer) ParseMessageReceived(log types.Log) (*InternalclaimsMessageReceived, error) {
	event := new(InternalclaimsMessageReceived)
	if err := _Internalclaims.contract.UnpackLog(event, "MessageReceived", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// InternalclaimsUpdateParametersIterator is returned from FilterUpdateParameters and is used to iterate over the raw logs and unpacked data for UpdateParameters events raised by the Internalclaims contract.
type InternalclaimsUpdateParametersIterator struct {
	Event *InternalclaimsUpdateParameters // Event containing the contract specifics and raw log

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
func (it *InternalclaimsUpdateParametersIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(InternalclaimsUpdateParameters)
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
		it.Event = new(InternalclaimsUpdateParameters)
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
func (it *InternalclaimsUpdateParametersIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *InternalclaimsUpdateParametersIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// InternalclaimsUpdateParameters represents a UpdateParameters event raised by the Internalclaims contract.
type InternalclaimsUpdateParameters struct {
	Raw types.Log // Blockchain specific contextual infos
}

// FilterUpdateParameters is a free log retrieval operation binding the contract event 0x9d226db03d4d6614ea01926ce8a588879492a2681b9684eb655b1470d32d4b9e.
//
// Solidity: event UpdateParameters()
func (_Internalclaims *InternalclaimsFilterer) FilterUpdateParameters(opts *bind.FilterOpts) (*InternalclaimsUpdateParametersIterator, error) {

	logs, sub, err := _Internalclaims.contract.FilterLogs(opts, "UpdateParameters")
	if err != nil {
		return nil, err
	}
	return &InternalclaimsUpdateParametersIterator{contract: _Internalclaims.contract, event: "UpdateParameters", logs: logs, sub: sub}, nil
}

// WatchUpdateParameters is a free log subscription operation binding the contract event 0x9d226db03d4d6614ea01926ce8a588879492a2681b9684eb655b1470d32d4b9e.
//
// Solidity: event UpdateParameters()
func (_Internalclaims *InternalclaimsFilterer) WatchUpdateParameters(opts *bind.WatchOpts, sink chan<- *InternalclaimsUpdateParameters) (event.Subscription, error) {

	logs, sub, err := _Internalclaims.contract.WatchLogs(opts, "UpdateParameters")
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(InternalclaimsUpdateParameters)
				if err := _Internalclaims.contract.UnpackLog(event, "UpdateParameters", log); err != nil {
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
func (_Internalclaims *InternalclaimsFilterer) ParseUpdateParameters(log types.Log) (*InternalclaimsUpdateParameters, error) {
	event := new(InternalclaimsUpdateParameters)
	if err := _Internalclaims.contract.UnpackLog(event, "UpdateParameters", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}
