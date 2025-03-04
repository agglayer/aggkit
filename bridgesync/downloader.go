package bridgesync

import (
	"bytes"
	"fmt"
	"math/big"
	"strings"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/etrog/polygonzkevmbridge"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/l2-sovereign-chain/polygonzkevmbridgev2"
	rpcTypes "github.com/0xPolygon/cdk-rpc/types"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/sync"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/golang-collections/collections/stack"
)

var (
	bridgeEventSignature = crypto.Keccak256Hash([]byte(
		"BridgeEvent(uint8,uint32,address,uint32,address,uint256,bytes,uint32)",
	))
	claimEventSignature         = crypto.Keccak256Hash([]byte("ClaimEvent(uint256,uint32,address,address,uint256)"))
	claimEventSignaturePreEtrog = crypto.Keccak256Hash([]byte("ClaimEvent(uint32,uint32,address,address,uint256)"))
	tokenMappingEventSignature  = crypto.Keccak256Hash([]byte("NewWrappedToken(uint32,address,address,bytes)"))

	claimAssetEtrogMethodID      = common.Hex2Bytes("ccaa2d11")
	claimMessageEtrogMethodID    = common.Hex2Bytes("f5efcd79")
	claimAssetPreEtrogMethodID   = common.Hex2Bytes("2cffd02e")
	claimMessagePreEtrogMethodID = common.Hex2Bytes("2d2c9d94")
)

// EthClienter defines the methods required to interact with an Ethereum client.
type EthClienter interface {
	ethereum.LogFilterer
	ethereum.BlockNumberReader
	ethereum.ChainReader
	bind.ContractBackend
	Client() *rpc.Client
}

func buildAppender(client EthClienter, bridge common.Address, syncFullClaims bool) (sync.LogAppenderMap, error) {
	bridgeContractV1, err := polygonzkevmbridge.NewPolygonzkevmbridge(bridge, client)
	if err != nil {
		return nil, err
	}

	bridgeContractV2, err := polygonzkevmbridgev2.NewPolygonzkevmbridgev2(bridge, client)
	if err != nil {
		return nil, err
	}

	appender := make(sync.LogAppenderMap)

	appender[bridgeEventSignature] = func(b *sync.EVMBlock, l types.Log) error {
		bridge, err := bridgeContractV2.ParseBridgeEvent(l)
		if err != nil {
			return fmt.Errorf(
				"error parsing log %+v using d.bridgeContractV2.ParseBridgeEvent: %w",
				l, err,
			)
		}
		b.Events = append(b.Events, Event{Bridge: &Bridge{
			BlockNum:           b.Num,
			BlockPos:           uint64(l.Index),
			LeafType:           bridge.LeafType,
			OriginNetwork:      bridge.OriginNetwork,
			OriginAddress:      bridge.OriginAddress,
			DestinationNetwork: bridge.DestinationNetwork,
			DestinationAddress: bridge.DestinationAddress,
			Amount:             bridge.Amount,
			Metadata:           bridge.Metadata,
			DepositCount:       bridge.DepositCount,
			BlockTimestamp:     b.Timestamp,
			TxHash:             l.TxHash,
			FromAddress:        l.Address,
		}})

		return nil
	}

	appender[claimEventSignature] = func(b *sync.EVMBlock, l types.Log) error {
		claimEvent, err := bridgeContractV2.ParseClaimEvent(l)
		if err != nil {
			return fmt.Errorf(
				"error parsing log %+v using d.bridgeContractV2.ParseClaimEvent: %w",
				l, err,
			)
		}
		claim := &Claim{
			BlockNum:           b.Num,
			BlockPos:           uint64(l.Index),
			GlobalIndex:        claimEvent.GlobalIndex,
			OriginNetwork:      claimEvent.OriginNetwork,
			OriginAddress:      claimEvent.OriginAddress,
			DestinationAddress: claimEvent.DestinationAddress,
			Amount:             claimEvent.Amount,
			BlockTimestamp:     b.Timestamp,
			TxHash:             l.TxHash,
			FromAddress:        l.Address,
		}
		if syncFullClaims {
			if err := setClaimCalldata(client, bridge, l.TxHash, claim); err != nil {
				return err
			}
		}
		b.Events = append(b.Events, Event{Claim: claim})
		return nil
	}

	appender[claimEventSignaturePreEtrog] = func(b *sync.EVMBlock, l types.Log) error {
		claimEvent, err := bridgeContractV1.ParseClaimEvent(l)
		if err != nil {
			return fmt.Errorf(
				"error parsing log %+v using d.bridgeContractV1.ParseClaimEvent: %w",
				l, err,
			)
		}
		claim := &Claim{
			BlockNum:           b.Num,
			BlockPos:           uint64(l.Index),
			GlobalIndex:        big.NewInt(int64(claimEvent.Index)),
			OriginNetwork:      claimEvent.OriginNetwork,
			OriginAddress:      claimEvent.OriginAddress,
			DestinationAddress: claimEvent.DestinationAddress,
			Amount:             claimEvent.Amount,
		}
		if syncFullClaims {
			if err := setClaimCalldata(client, bridge, l.TxHash, claim); err != nil {
				return err
			}
		}
		b.Events = append(b.Events, Event{Claim: claim})
		return nil
	}

	appender[tokenMappingEventSignature] = func(b *sync.EVMBlock, l types.Log) error {
		tokenMapping, err := bridgeContractV2.ParseNewWrappedToken(l)
		if err != nil {
			return fmt.Errorf(
				"error parsing log %+v using d.bridgeContractV2.ParseNewWrappedToken: %w",
				l, err,
			)
		}

		b.Events = append(b.Events, Event{TokenMapping: &TokenMapping{
			BlockNum:            b.Num,
			BlockPos:            uint64(l.Index),
			BlockTimestamp:      b.Timestamp,
			TxHash:              l.TxHash,
			OriginNetwork:       tokenMapping.OriginNetwork,
			OriginTokenAddress:  tokenMapping.OriginTokenAddress,
			WrappedTokenAddress: tokenMapping.WrappedTokenAddress,
			Metadata:            tokenMapping.Metadata,
		}})
		return nil
	}

	return appender, nil
}

type call struct {
	To    common.Address    `json:"to"`
	Value *rpcTypes.ArgBig  `json:"value"`
	Err   *string           `json:"error"`
	Input rpcTypes.ArgBytes `json:"input"`
	Calls []call            `json:"calls"`
}

type tracerCfg struct {
	Tracer string `json:"tracer"`
}

func setClaimCalldata(client EthClienter, bridge common.Address, txHash common.Hash, claim *Claim) error {
	c := &call{}
	err := client.Client().Call(c, "debug_traceTransaction", txHash, tracerCfg{Tracer: "callTracer"})
	if err != nil {
		return err
	}

	// find the claim linked to the event using DFS
	callStack := stack.New()
	callStack.Push(*c)
	for callStack.Len() > 0 {
		currentCallInterface := callStack.Pop()
		currentCall, ok := currentCallInterface.(call)
		if !ok {
			return fmt.Errorf("unexpected type for 'currentCall'. Expected 'call', got '%T'", currentCallInterface)
		}

		if currentCall.To == bridge {
			found, err := claim.tryDecodeClaimCalldata(currentCall.Input)
			if err != nil {
				return err
			}
			if found {
				return nil
			}
		}
		for _, c := range currentCall.Calls {
			callStack.Push(c)
		}
	}
	return db.ErrNotFound
}

// tryDecodeClaimCalldata attempts to find and decode the claim calldata from the provided input bytes.
// It checks if the method ID corresponds to either the claim asset or claim message methods.
// If a match is found, it decodes the calldata using the ABI of the bridge contract and updates the claim object.
// Returns true if the calldata is successfully decoded and matches the expected format, otherwise returns false.
func (c *Claim) tryDecodeClaimCalldata(input []byte) (bool, error) {
	methodID := input[:4]
	switch {
	case bytes.Equal(methodID, claimAssetEtrogMethodID):
		fallthrough
	case bytes.Equal(methodID, claimMessageEtrogMethodID):
		bridgeV2ABI, err := abi.JSON(strings.NewReader(polygonzkevmbridgev2.Polygonzkevmbridgev2ABI))
		if err != nil {
			return false, err
		}
		// Recover Method from signature and ABI
		method, err := bridgeV2ABI.MethodById(methodID)
		if err != nil {
			return false, err
		}
		data, err := method.Inputs.Unpack(input[4:])
		if err != nil {
			return false, err
		}

		found, err := c.decodeEtrogCalldata(data)
		if err != nil {
			return false, err
		}
		if found {
			c.IsMessage = bytes.Equal(methodID, claimMessageEtrogMethodID)
			return true, nil
		}
		return false, nil

	case bytes.Equal(methodID, claimAssetPreEtrogMethodID):
		fallthrough
	case bytes.Equal(methodID, claimMessagePreEtrogMethodID):
		bridgeABI, err := abi.JSON(strings.NewReader(polygonzkevmbridge.PolygonzkevmbridgeABI))
		if err != nil {
			return false, err
		}
		// Recover Method from signature and ABI
		method, err := bridgeABI.MethodById(methodID)
		if err != nil {
			return false, err
		}
		data, err := method.Inputs.Unpack(input[4:])
		if err != nil {
			return false, err
		}

		found, err := c.decodePreEtrogCalldata(data)
		if err != nil {
			return false, err
		}
		if found {
			c.IsMessage = bytes.Equal(methodID, claimMessagePreEtrogMethodID)
			return true, nil
		}
		return false, nil

	default:
		return false, nil
	}
}
