package bridgesync

import (
	"bytes"
	"fmt"
	"math/big"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/etrog/polygonzkevmbridge"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/l2-sovereign-chain/bridgel2sovereignchain"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/l2-sovereign-chain/polygonzkevmbridgev2"
	rpcTypes "github.com/0xPolygon/cdk-rpc/types"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/golang-collections/collections/stack"
)

var (
	bridgeEventSignature = crypto.Keccak256Hash([]byte(
		"BridgeEvent(uint8,uint32,address,uint32,address,uint256,bytes,uint32)",
	))
	claimEventSignature             = crypto.Keccak256Hash([]byte("ClaimEvent(uint256,uint32,address,address,uint256)"))
	claimEventSignaturePreEtrog     = crypto.Keccak256Hash([]byte("ClaimEvent(uint32,uint32,address,address,uint256)"))
	tokenMappingEventSignature      = crypto.Keccak256Hash([]byte("NewWrappedToken(uint32,address,address,bytes)"))
	setSovereignTokenEventSignature = crypto.Keccak256Hash([]byte("SetSovereignTokenAddress(uint32,address,address,bool)"))

	claimAssetEtrogMethodID      = common.Hex2Bytes("ccaa2d11")
	claimMessageEtrogMethodID    = common.Hex2Bytes("f5efcd79")
	claimAssetPreEtrogMethodID   = common.Hex2Bytes("2cffd02e")
	claimMessagePreEtrogMethodID = common.Hex2Bytes("2d2c9d94")
)

const (
	// debugTraceTxEndpoint is the name of the debug method used to trace a transaction.
	debugTraceTxEndpoint = "debug_traceTransaction"

	// callTracerType is the name of the call tracer
	callTracerType = "callTracer"
)

func buildAppender(client aggkittypes.EthClienter, bridgeAddr common.Address,
	syncFullClaims bool) (sync.LogAppenderMap, error) {
	bridgeContractV1, err := polygonzkevmbridge.NewPolygonzkevmbridge(bridgeAddr, client)
	if err != nil {
		return nil, err
	}

	bridgeContractV2, err := polygonzkevmbridgev2.NewPolygonzkevmbridgev2(bridgeAddr, client)
	if err != nil {
		return nil, err
	}

	bridgeSovereignChain, err := bridgel2sovereignchain.NewBridgel2sovereignchain(bridgeAddr, client)
	if err != nil {
		return nil, err
	}

	appender := make(sync.LogAppenderMap)

	appender[bridgeEventSignature] = func(b *sync.EVMBlock, l types.Log) error {
		bridgeEvent, err := bridgeContractV2.ParseBridgeEvent(l)
		if err != nil {
			return fmt.Errorf(
				"error parsing log %+v using d.bridgeContractV2.ParseBridgeEvent: %w",
				l, err,
			)
		}

		calldata, err := extractCalldata(client, bridgeAddr, l.TxHash)
		if err != nil {
			return fmt.Errorf("failed to extract the bridge event calldata (tx hash: %s): %w", l.TxHash, err)
		}

		b.Events = append(b.Events, Event{Bridge: &Bridge{
			BlockNum:           b.Num,
			BlockPos:           uint64(l.Index),
			LeafType:           bridgeEvent.LeafType,
			OriginNetwork:      bridgeEvent.OriginNetwork,
			OriginAddress:      bridgeEvent.OriginAddress,
			DestinationNetwork: bridgeEvent.DestinationNetwork,
			DestinationAddress: bridgeEvent.DestinationAddress,
			Amount:             bridgeEvent.Amount,
			Metadata:           bridgeEvent.Metadata,
			DepositCount:       bridgeEvent.DepositCount,
			BlockTimestamp:     b.Timestamp,
			TxHash:             l.TxHash,
			FromAddress:        l.Address,
			Calldata:           calldata,
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
			if err := claim.setClaimCalldata(client, bridgeAddr, l.TxHash); err != nil {
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
			if err := claim.setClaimCalldata(client, bridgeAddr, l.TxHash); err != nil {
				return err
			}
		}
		b.Events = append(b.Events, Event{Claim: claim})
		return nil
	}

	appender[tokenMappingEventSignature] = func(b *sync.EVMBlock, l types.Log) error {
		tokenMappingEvent, err := bridgeContractV2.ParseNewWrappedToken(l)
		if err != nil {
			return fmt.Errorf(
				"error parsing log %+v using d.bridgeContractV2.ParseNewWrappedToken: %w",
				l, err,
			)
		}

		calldata, err := extractCalldata(client, bridgeAddr, l.TxHash)
		if err != nil {
			return fmt.Errorf("failed to extract the NewWrappedToken event calldata (tx hash: %s): %w", l.TxHash, err)
		}

		b.Events = append(b.Events, Event{TokenMapping: &TokenMapping{
			BlockNum:            b.Num,
			BlockPos:            uint64(l.Index),
			BlockTimestamp:      b.Timestamp,
			TxHash:              l.TxHash,
			OriginNetwork:       tokenMappingEvent.OriginNetwork,
			OriginTokenAddress:  tokenMappingEvent.OriginTokenAddress,
			WrappedTokenAddress: tokenMappingEvent.WrappedTokenAddress,
			Metadata:            tokenMappingEvent.Metadata,
			Calldata:            calldata,
			Type:                WrappedToken,
		}})
		return nil
	}

	appender[setSovereignTokenEventSignature] = func(b *sync.EVMBlock, l types.Log) error {
		setSovereignTokenEvent, err := bridgeSovereignChain.ParseSetSovereignTokenAddress(l)
		if err != nil {
			return fmt.Errorf(
				"error parsing log %+v using d.bridgeSovereignChain.ParseSetSovereignTokenAddress: %w",
				l, err,
			)
		}

		calldata, err := extractCalldata(client, bridgeAddr, l.TxHash)
		if err != nil {
			return fmt.Errorf("failed to extract the SetSovereignTokenAddress event calldata (tx hash: %s): %w", l.TxHash, err)
		}

		b.Events = append(b.Events, Event{TokenMapping: &TokenMapping{
			BlockNum:            b.Num,
			BlockPos:            uint64(l.Index),
			BlockTimestamp:      b.Timestamp,
			TxHash:              l.TxHash,
			OriginNetwork:       setSovereignTokenEvent.OriginNetwork,
			OriginTokenAddress:  setSovereignTokenEvent.OriginTokenAddress,
			WrappedTokenAddress: setSovereignTokenEvent.SovereignTokenAddress,
			IsNotMintable:       setSovereignTokenEvent.IsNotMintable,
			Calldata:            calldata,
			Type:                SovereignToken,
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

// findCalldata traverses the call trace using DFS and either returns calldata or stops when a callback succeeds.
func findCalldata(rootCall call, targetAddr common.Address, callback func([]byte) (bool, error)) ([]byte, error) {
	callStack := stack.New()
	callStack.Push(rootCall)

	for callStack.Len() > 0 {
		currentCallInterface := callStack.Pop()
		currentCall, ok := currentCallInterface.(call)
		if !ok {
			return nil, fmt.Errorf("unexpected type for 'currentCall'. Expected 'call', got '%T'", currentCallInterface)
		}

		if currentCall.To == targetAddr {
			if callback != nil {
				found, err := callback(currentCall.Input)
				if err != nil {
					return nil, err
				}
				if found {
					return currentCall.Input, nil
				}
			} else {
				return currentCall.Input, nil
			}
		}

		for _, c := range currentCall.Calls {
			callStack.Push(c)
		}
	}
	return nil, db.ErrNotFound
}

// extractCalldata tries to extract the calldata for the transaction indentified by transaction hash.
// It relies on debug_traceTransaction JSON RPC function.
func extractCalldata(client aggkittypes.RPCClienter, contractAddr common.Address, txHash common.Hash) ([]byte, error) {
	c := &call{To: contractAddr}
	err := client.Call(c, debugTraceTxEndpoint, txHash, tracerCfg{Tracer: callTracerType})
	if err != nil {
		return nil, err
	}

	return findCalldata(*c, contractAddr, nil)
}

// setClaimCalldata traces the transaction to find and decode calldata for the given bridge address.
//
// Parameters:
// - client: RPC client to fetch the transaction trace.
// - bridge: Target contract address.
// - txHash: Transaction hash to trace.
//
// Returns an error if tracing fails or calldata isn't found.
func (c *Claim) setClaimCalldata(client aggkittypes.RPCClienter, bridge common.Address, txHash common.Hash) error {
	callFrame := &call{}
	err := client.Call(callFrame, debugTraceTxEndpoint, txHash, tracerCfg{Tracer: callTracerType})
	if err != nil {
		return err
	}

	_, err = findCalldata(*callFrame, bridge,
		func(data []byte) (bool, error) {
			return c.tryDecodeClaimCalldata(data)
		})

	return err
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
		bridgeV2ABI, err := polygonzkevmbridgev2.Polygonzkevmbridgev2MetaData.GetAbi()
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
		}

		return found, nil

	case bytes.Equal(methodID, claimAssetPreEtrogMethodID):
		fallthrough
	case bytes.Equal(methodID, claimMessagePreEtrogMethodID):
		bridgeABI, err := polygonzkevmbridge.PolygonzkevmbridgeMetaData.GetAbi()
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
		}

		return found, nil

	default:
		return false, nil
	}
}
