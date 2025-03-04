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
	tree "github.com/agglayer/aggkit/tree/types"
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
	methodIDClaimAsset          = common.Hex2Bytes("ccaa2d11")
	methodIDClaimMessage        = common.Hex2Bytes("f5efcd79")
	// TODO: get the correct method IDs
	methodIDBridgeAsset   = common.Hex2Bytes("ccaa2d11")
	methodIDBridgeMessage = common.Hex2Bytes("ccaa2d11")
)

// EthClienter defines the methods required to interact with an Ethereum client.
type EthClienter interface {
	ethereum.LogFilterer
	ethereum.BlockNumberReader
	ethereum.ChainReader
	bind.ContractBackend
	Client() *rpc.Client
}

func buildAppender(client EthClienter, bridgeAddr common.Address, syncFullClaims bool) (sync.LogAppenderMap, error) {
	bridgeContractV1, err := polygonzkevmbridge.NewPolygonzkevmbridge(bridgeAddr, client)
	if err != nil {
		return nil, err
	}

	bridgeContractV2, err := polygonzkevmbridgev2.NewPolygonzkevmbridgev2(bridgeAddr, client)
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
		bridge := &Bridge{
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
		}
		calldata, err := getBridgeCalldata(client, bridgeAddr, l.TxHash)
		if err != nil {
			return fmt.Errorf(
				"error getting calldata for bridge event %+v: %w",
				l, err,
			)
		}
		bridge.Calldata = calldata
		b.Events = append(b.Events, Event{Bridge: bridge})

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
			if err := setClaimCalldata(client, bridgeAddr, l.TxHash, claim); err != nil {
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
			if err := setClaimCalldata(client, bridgeAddr, l.TxHash, claim); err != nil {
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

func getBridgeCalldata(
	client EthClienter,
	bridgeAddr common.Address,
	txHash common.Hash,
) ([]byte, error) {
	c := &call{}
	err := client.Client().Call(c, "debug_traceTransaction", txHash, tracerCfg{Tracer: "callTracer"})
	if err != nil {
		return nil, err
	}

	// find the claim linked to the event using DFS
	callStack := stack.New()
	callStack.Push(*c)
	for {
		if callStack.Len() == 0 {
			break
		}

		currentCallInterface := callStack.Pop()
		currentCall, ok := currentCallInterface.(call)
		if !ok {
			return nil, fmt.Errorf("unexpected type for 'currentCall'. Expected 'call', got '%T'", currentCallInterface)
		}

		// TODO: check for valid check
		if currentCall.To == bridgeAddr {
			return currentCall.Input, nil
		}
		for _, c := range currentCall.Calls {
			callStack.Push(c)
		}
	}
	return nil, db.ErrNotFound
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
	for {
		if callStack.Len() == 0 {
			break
		}

		currentCallInterface := callStack.Pop()
		currentCall, ok := currentCallInterface.(call)
		if !ok {
			return fmt.Errorf("unexpected type for 'currentCall'. Expected 'call', got '%T'", currentCallInterface)
		}

		if currentCall.To == bridge {
			found, err := setClaimIfFoundOnInput(
				currentCall.Input,
				claim,
			)
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

func setClaimIfFoundOnInput(input []byte, claim *Claim) (bool, error) {
	smcAbi, err := abi.JSON(strings.NewReader(polygonzkevmbridgev2.Polygonzkevmbridgev2ABI))
	if err != nil {
		return false, err
	}
	methodID := input[:4]
	// Recover Method from signature and ABI
	method, err := smcAbi.MethodById(methodID)
	if err != nil {
		return false, err
	}
	data, err := method.Inputs.Unpack(input[4:])
	if err != nil {
		return false, err
	}
	// Ignore other methods
	if bytes.Equal(methodID, methodIDClaimAsset) || bytes.Equal(methodID, methodIDClaimMessage) {
		found, err := decodeClaimCallDataAndSetIfFound(data, claim)
		if err != nil {
			return false, err
		}
		if found {
			if bytes.Equal(methodID, methodIDClaimMessage) {
				claim.IsMessage = true
			}
			return true, nil
		}
		return false, nil
	} else {
		return false, nil
	}
	// TODO: support both claim asset & message, check if previous versions need special treatment
}

func decodeClaimCallDataAndSetIfFound(data []interface{}, claim *Claim) (bool, error) {
	/* Unpack method inputs. Note that both claimAsset and claimMessage have the same interface
	for the relevant parts
	claimAsset(
		0: smtProofLocalExitRoot,
		1: smtProofRollupExitRoot,
		2: globalIndex,
		3: mainnetExitRoot,
		4: rollupExitRoot,
		5: originNetwork,
		6: originTokenAddress,
		7: destinationNetwork,
		8: destinationAddress,
		9: amount,
		10: metadata,
	)
	claimMessage(
		0: smtProofLocalExitRoot,
		1: smtProofRollupExitRoot,
		2: globalIndex,
		3: mainnetExitRoot,
		4: rollupExitRoot,
		5: originNetwork,
		6: originAddress,
		7: destinationNetwork,
		8: destinationAddress,
		9: amount,
		10: metadata,
	)
	*/
	actualGlobalIndex, ok := data[2].(*big.Int)
	if !ok {
		return false, fmt.Errorf("unexpected type for actualGlobalIndex, expected *big.Int got '%T'", data[2])
	}
	if actualGlobalIndex.Cmp(claim.GlobalIndex) != 0 {
		// not the claim we're looking for
		return false, nil
	} else {
		proofLER := [tree.DefaultHeight]common.Hash{}
		proofLERBytes, ok := data[0].([32][32]byte)
		if !ok {
			return false, fmt.Errorf("unexpected type for proofLERBytes, expected [32][32]byte got '%T'", data[0])
		}

		proofRER := [tree.DefaultHeight]common.Hash{}
		proofRERBytes, ok := data[1].([32][32]byte)
		if !ok {
			return false, fmt.Errorf("unexpected type for proofRERBytes, expected [32][32]byte got '%T'", data[1])
		}

		for i := 0; i < int(tree.DefaultHeight); i++ {
			proofLER[i] = proofLERBytes[i]
			proofRER[i] = proofRERBytes[i]
		}
		claim.ProofLocalExitRoot = proofLER
		claim.ProofRollupExitRoot = proofRER

		claim.MainnetExitRoot, ok = data[3].([32]byte)
		if !ok {
			return false, fmt.Errorf("unexpected type for 'MainnetExitRoot'. Expected '[32]byte', got '%T'", data[3])
		}

		claim.RollupExitRoot, ok = data[4].([32]byte)
		if !ok {
			return false, fmt.Errorf("unexpected type for 'RollupExitRoot'. Expected '[32]byte', got '%T'", data[4])
		}

		claim.DestinationNetwork, ok = data[7].(uint32)
		if !ok {
			return false, fmt.Errorf("unexpected type for 'DestinationNetwork'. Expected 'uint32', got '%T'", data[7])
		}

		claim.Metadata, ok = data[10].([]byte)
		if !ok {
			return false, fmt.Errorf("unexpected type for 'claim Metadata'. Expected '[]byte', got '%T'", data[10])
		}

		claim.GlobalExitRoot = crypto.Keccak256Hash(claim.MainnetExitRoot.Bytes(), claim.RollupExitRoot.Bytes())

		return true, nil
	}
}
