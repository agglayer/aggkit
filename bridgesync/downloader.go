package bridgesync

import (
	"bytes"
	"fmt"
	"math/big"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/fep/etrog/polygonzkevmbridge"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/bridgel2sovereignchain"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/polygonzkevmbridgev2"
	rpctypes "github.com/0xPolygon/cdk-rpc/types"
	bridgetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/golang-collections/collections/stack"
)

var (
	// non-sovereign chain contract events
	bridgeEventSignature = crypto.Keccak256Hash([]byte(
		"BridgeEvent(uint8,uint32,address,uint32,address,uint256,bytes,uint32)",
	))
	claimEventSignature         = crypto.Keccak256Hash([]byte("ClaimEvent(uint256,uint32,address,address,uint256)"))
	claimEventSignaturePreEtrog = crypto.Keccak256Hash([]byte("ClaimEvent(uint32,uint32,address,address,uint256)"))
	tokenMappingEventSignature  = crypto.Keccak256Hash([]byte("NewWrappedToken(uint32,address,address,bytes)"))

	// sovereign chain contract events
	setSovereignTokenEventSignature = crypto.Keccak256Hash([]byte(
		"SetSovereignTokenAddress(uint32,address,address,bool)",
	))
	migrateLegacyTokenEventSignature = crypto.Keccak256Hash([]byte(
		"MigrateLegacyToken(address,address,address,uint256)",
	))
	removeLegacySovereignTokenEventSignature = crypto.Keccak256Hash([]byte(
		"RemoveLegacySovereignTokenAddress(address)",
	))

	claimAssetEtrogMethodID      = common.Hex2Bytes("ccaa2d11")
	claimMessageEtrogMethodID    = common.Hex2Bytes("f5efcd79")
	claimAssetPreEtrogMethodID   = common.Hex2Bytes("2cffd02e")
	claimMessagePreEtrogMethodID = common.Hex2Bytes("2d2c9d94")
	zeroAddress                  = common.HexToAddress("0x0")
)

const (
	// debugTraceTxEndpoint is the name of the debug method used to trace a transaction.
	debugTraceTxEndpoint = "debug_traceTransaction"

	// callTracerType is the name of the call tracer
	callTracerType = "callTracer"
)

func buildAppender(client aggkittypes.EthClienter,
	bridgeAddr common.Address, syncFullClaims bool) (sync.LogAppenderMap, error) {
	logger := log.WithFields("module", "bridge_downloader")
	logger.Infof("building appender for bridge at %s with syncFullClaims=%v", bridgeAddr.String(), syncFullClaims)

	bridgeContractV1, err := polygonzkevmbridge.NewPolygonzkevmbridge(bridgeAddr, client)
	if err != nil {
		return nil, fmt.Errorf("failed to create PolygonZkEVMBridge SC binding (bridge addr: %s): %w", bridgeAddr, err)
	}

	bridgeContractV2, err := polygonzkevmbridgev2.NewPolygonzkevmbridgev2(bridgeAddr, client)
	if err != nil {
		return nil, fmt.Errorf("failed to create PolygonZkEVMBridgeV2 SC binding (bridge addr: %s): %w",
			bridgeAddr, err)
	}

	bridgeSovereignChain, err := bridgel2sovereignchain.NewBridgel2sovereignchain(bridgeAddr, client)
	if err != nil {
		return nil, fmt.Errorf("failed to create BridgeL2SovereignChain SC binding (bridge addr: %s): %w",
			bridgeAddr, err)
	}

	gasTokenAddress, err := bridgeContractV2.GasTokenAddress(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get gas token address: %w", err)
	}

	appender := make(sync.LogAppenderMap)

	// Add event handlers for the bridge contract
	appender[bridgeEventSignature] = buildBridgeEventHandler(bridgeContractV2, client, bridgeAddr, gasTokenAddress)
	appender[claimEventSignature] = buildClaimEventHandler(bridgeContractV2, client, bridgeAddr, syncFullClaims)
	appender[claimEventSignaturePreEtrog] = buildClaimEventHandlerPreEtrog(
		bridgeContractV1, client,
		bridgeAddr, syncFullClaims)
	appender[tokenMappingEventSignature] = buildTokenMappingHandler(bridgeContractV2, client, bridgeAddr)
	appender[setSovereignTokenEventSignature] = buildSetSovereignTokenHandler(bridgeSovereignChain, client, bridgeAddr)
	appender[migrateLegacyTokenEventSignature] = buildMigrateLegacyTokenHandler(bridgeSovereignChain, client, bridgeAddr)
	appender[removeLegacySovereignTokenEventSignature] = buildRemoveLegacyTokenHandler(bridgeSovereignChain)

	return appender, nil
}

// buildBridgeEventHandler creates a handler for the Bridge event log.
func buildBridgeEventHandler(contract *polygonzkevmbridgev2.Polygonzkevmbridgev2,
	client aggkittypes.EthClienter,
	bridgeAddr common.Address, gasTokenAddress common.Address) func(*sync.EVMBlock, types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		bridgeEvent, err := contract.ParseBridgeEvent(l)
		if err != nil {
			return fmt.Errorf("error parsing BridgeEvent log %+v: %w", l, err)
		}

		foundCall, err := extractCall(client, bridgeAddr, l.TxHash)
		if err != nil {
			return fmt.Errorf("failed to extract bridge event calldata (tx hash: %s): %w", l.TxHash, err)
		}

		isNativeToken := bridgeEvent.OriginAddress == gasTokenAddress || bridgeEvent.OriginAddress == zeroAddress

		b.Events = append(b.Events, Event{Bridge: &Bridge{
			BlockNum:           b.Num,
			BlockPos:           uint64(l.Index),
			FromAddress:        foundCall.From,
			TxHash:             l.TxHash,
			Calldata:           foundCall.Input,
			BlockTimestamp:     b.Timestamp,
			LeafType:           bridgeEvent.LeafType,
			OriginNetwork:      bridgeEvent.OriginNetwork,
			OriginAddress:      bridgeEvent.OriginAddress,
			DestinationNetwork: bridgeEvent.DestinationNetwork,
			DestinationAddress: bridgeEvent.DestinationAddress,
			Amount:             bridgeEvent.Amount,
			Metadata:           bridgeEvent.Metadata,
			DepositCount:       bridgeEvent.DepositCount,
			IsNativeToken:      isNativeToken,
		}})
		return nil
	}
}

// buildClaimEventHandler creates a handler for the Claim event log.
func buildClaimEventHandler(contract *polygonzkevmbridgev2.Polygonzkevmbridgev2,
	client aggkittypes.EthClienter, bridgeAddr common.Address, syncFullClaims bool) func(*sync.EVMBlock, types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		claimEvent, err := contract.ParseClaimEvent(l)
		if err != nil {
			return fmt.Errorf("error parsing Claim event log %+v: %w", l, err)
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
}

// buildClaimEventHandlerPreEtrog creates a handler for the Claim event log for pre-Etrog contracts.
func buildClaimEventHandlerPreEtrog(contract *polygonzkevmbridge.Polygonzkevmbridge,
	client aggkittypes.EthClienter, bridgeAddr common.Address, syncFullClaims bool) func(*sync.EVMBlock, types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		claimEvent, err := contract.ParseClaimEvent(l)
		if err != nil {
			return fmt.Errorf("error parsing Claim event log %+v: %w", l, err)
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
}

// buildTokenMappingHandler creates a handler for the NewWrappedToken event log.
//
//nolint:dupl
func buildTokenMappingHandler(contract *polygonzkevmbridgev2.Polygonzkevmbridgev2,
	client aggkittypes.EthClienter, bridgeAddr common.Address) func(*sync.EVMBlock, types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		tokenMappingEvent, err := contract.ParseNewWrappedToken(l)
		if err != nil {
			return fmt.Errorf("error parsing NewWrappedToken event log %+v: %w", l, err)
		}

		foundCall, err := extractCall(client, bridgeAddr, l.TxHash)
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
			Calldata:            foundCall.Input,
			Type:                bridgetypes.WrappedToken,
		}})
		return nil
	}
}

// buildSetSovereignTokenHandler creates a handler for the SetSovereignTokenAddress event log.
//
//nolint:dupl
func buildSetSovereignTokenHandler(contract *bridgel2sovereignchain.Bridgel2sovereignchain,
	client aggkittypes.EthClienter, bridgeAddr common.Address) func(*sync.EVMBlock, types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		event, err := contract.ParseSetSovereignTokenAddress(l)
		if err != nil {
			return fmt.Errorf("error parsing SetSovereignTokenAddress event log %+v: %w", l, err)
		}

		foundCall, err := extractCall(client, bridgeAddr, l.TxHash)
		if err != nil {
			return fmt.Errorf("failed to extract the SetSovereignTokenAddress event calldata (tx hash: %s): %w", l.TxHash, err)
		}

		b.Events = append(b.Events, Event{TokenMapping: &TokenMapping{
			BlockNum:            b.Num,
			BlockPos:            uint64(l.Index),
			BlockTimestamp:      b.Timestamp,
			TxHash:              l.TxHash,
			OriginNetwork:       event.OriginNetwork,
			OriginTokenAddress:  event.OriginTokenAddress,
			WrappedTokenAddress: event.SovereignTokenAddress,
			IsNotMintable:       event.IsNotMintable,
			Calldata:            foundCall.Input,
			Type:                bridgetypes.SovereignToken,
		}})
		return nil
	}
}

// buildMigrateLegacyTokenHandler creates a handler for the MigrateLegacyToken event log.
func buildMigrateLegacyTokenHandler(contract *bridgel2sovereignchain.Bridgel2sovereignchain,
	client aggkittypes.EthClienter, bridgeAddr common.Address) func(*sync.EVMBlock, types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		event, err := contract.ParseMigrateLegacyToken(l)
		if err != nil {
			return fmt.Errorf("error parsing MigrateLegacyToken event log %+v: %w", l, err)
		}

		foundCall, err := extractCall(client, bridgeAddr, l.TxHash)
		if err != nil {
			return fmt.Errorf("failed to extract the MigrateLegacyToken event calldata (tx hash: %s): %w", l.TxHash, err)
		}

		b.Events = append(b.Events, Event{LegacyTokenMigration: &LegacyTokenMigration{
			BlockNum:            b.Num,
			BlockPos:            uint64(l.Index),
			BlockTimestamp:      b.Timestamp,
			TxHash:              l.TxHash,
			Sender:              event.Sender,
			LegacyTokenAddress:  event.LegacyTokenAddress,
			UpdatedTokenAddress: event.UpdatedTokenAddress,
			Amount:              event.Amount,
			Calldata:            foundCall.Input,
		}})
		return nil
	}
}

// buildRemoveLegacyTokenHandler creates a handler for the RemoveLegacySovereignTokenAddress event log.
func buildRemoveLegacyTokenHandler(contract *bridgel2sovereignchain.Bridgel2sovereignchain) func(*sync.EVMBlock,
	types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		event, err := contract.ParseRemoveLegacySovereignTokenAddress(l)
		if err != nil {
			return fmt.Errorf("error parsing RemoveLegacySovereignTokenAddress event log %+v: %w", l, err)
		}

		b.Events = append(b.Events, Event{RemoveLegacyToken: &RemoveLegacyToken{
			BlockNum:           b.Num,
			BlockPos:           uint64(l.Index),
			BlockTimestamp:     b.Timestamp,
			TxHash:             l.TxHash,
			LegacyTokenAddress: event.SovereignTokenAddress,
		}})
		return nil
	}
}

type call struct {
	From  common.Address    `json:"from"`
	To    common.Address    `json:"to"`
	Value *rpctypes.ArgBig  `json:"value"`
	Err   *string           `json:"error"`
	Input rpctypes.ArgBytes `json:"input"`
	Calls []call            `json:"calls"`
}

type tracerCfg struct {
	Tracer string `json:"tracer"`
}

// findCall traverses the call trace using DFS and either returns the call or stops when a callback succeeds.
func findCall(rootCall call, targetAddr common.Address, callback func(call) (bool, error)) (*call, error) {
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
				found, err := callback(currentCall)
				if err != nil {
					return nil, err
				}
				if found {
					return &currentCall, nil
				}
			} else {
				return &currentCall, nil
			}
		}

		for _, c := range currentCall.Calls {
			callStack.Push(c)
		}
	}
	return nil, db.ErrNotFound
}

// extractCall tries to extract the call for the transaction identified by transaction hash.
// It relies on debug_traceTransaction JSON RPC function.
func extractCall(client aggkittypes.RPCClienter, contractAddr common.Address, txHash common.Hash) (*call, error) {
	c := &call{To: contractAddr}
	err := client.Call(c, debugTraceTxEndpoint, txHash, tracerCfg{Tracer: callTracerType})
	if err != nil {
		return nil, err
	}

	return findCall(*c, contractAddr, nil)
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

	_, err = findCall(*callFrame, bridge,
		func(call call) (bool, error) {
			return c.tryDecodeClaimCalldata(call.From, call.Input)
		})

	return err
}

// tryDecodeClaimCalldata attempts to find and decode the claim calldata from the provided input bytes.
// It checks if the method ID corresponds to either the claim asset or claim message methods.
// If a match is found, it decodes the calldata using the ABI of the bridge contract and updates the claim object.
// Returns true if the calldata is successfully decoded and matches the expected format, otherwise returns false.
func (c *Claim) tryDecodeClaimCalldata(senderAddr common.Address, input []byte) (bool, error) {
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

		found, err := c.decodeEtrogCalldata(senderAddr, data)
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

		found, err := c.decodePreEtrogCalldata(senderAddr, data)
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
