package claimsync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/polygonzkevmbridge"
	rpctypes "github.com/0xPolygon/cdk-rpc/types"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db"
	dbtypes "github.com/agglayer/aggkit/db/types"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	treetypes "github.com/agglayer/aggkit/tree/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	gethvm "github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/golang-collections/collections/stack"
)

var (
	claimEventSignature         = crypto.Keccak256Hash([]byte("ClaimEvent(uint256,uint32,address,address,uint256)"))
	claimEventSignaturePreEtrog = crypto.Keccak256Hash([]byte("ClaimEvent(uint32,uint32,address,address,uint256)"))
	detailedClaimEventSignature = crypto.Keccak256Hash([]byte(
		"DetailedClaimEvent(bytes32[32],bytes32[32]," +
			"uint256,bytes32,bytes32,uint8,uint32," +
			"address,uint32,address,uint256,bytes)",
	))
	unsetClaimEventSignature = crypto.Keccak256Hash([]byte("UpdatedUnsetGlobalIndexHashChain(bytes32,bytes32)"))
	setClaimEventSignature   = crypto.Keccak256Hash([]byte("SetClaim(bytes32)"))

	claimAssetEtrogMethodID      = common.Hex2Bytes("ccaa2d11")
	claimMessageEtrogMethodID    = common.Hex2Bytes("f5efcd79")
	claimAssetPreEtrogMethodID   = common.Hex2Bytes("2cffd02e")
	claimMessagePreEtrogMethodID = common.Hex2Bytes("2d2c9d94")
)

const (
	// DebugTraceTxEndpoint is the name of the debug method used to trace a transaction.
	DebugTraceTxEndpoint = "debug_traceTransaction"
	// GetTransactionByHashEndpoint is the name of the method used to get transaction details by hash.
	GetTransactionByHashEndpoint = "eth_getTransactionByHash"
	// callTracerType is the name of the call tracer
	callTracerType = "callTracer"

	// methodIDLength is the length of the method ID in bytes
	methodIDLength = 4
)

// claimQuerier is used by event handlers to check the DetailedClaimEvent boundary.
type ClaimQuerier interface {
	GetBoundaryBlockForClaimType(ctx context.Context, tx dbtypes.Querier, claimType ClaimType) (uint64, error)
}

// BridgeDeployment represents the type of bridge contract deployment (sovereign vs non-sovereign).
type BridgeDeployment byte

const (
	Unknown BridgeDeployment = iota
	NonSovereignChain
	SovereignChain
)

func (b BridgeDeployment) String() string {
	switch b {
	case NonSovereignChain:
		return "NonSovereignChain"
	case SovereignChain:
		return "SovereignChain"
	default:
		return "Unknown"
	}
}

type bridgeDeployment struct {
	kind             BridgeDeployment
	agglayerBridge   *agglayerbridge.Agglayerbridge
	agglayerBridgeL2 *agglayerbridgel2.Agglayerbridgel2
}

// buildAppender creates the LogAppenderMap for claim events from the bridge contract.
func buildAppender(
	ctx context.Context,
	ethClient aggkittypes.EthClienter,
	querier ClaimQuerier,
	bridgeAddr common.Address,
	deployment *bridgeDeployment,
	log aggkitcommon.Logger,
) (sync.LogAppenderMap, error) {
	legacyBridge, err := polygonzkevmbridge.NewPolygonzkevmbridge(bridgeAddr, ethClient)
	if err != nil {
		return nil, fmt.Errorf("claimsync: failed to create PolygonZkEVMBridge binding: %w", err)
	}
	// TODO: Check syncfullclaims
	syncFullClaims := true
	appender := make(sync.LogAppenderMap)
	appender[claimEventSignaturePreEtrog] = buildClaimEventHandlerPreEtrog(
		legacyBridge, ethClient, bridgeAddr, syncFullClaims, log)

	appender[claimEventSignature] = buildClaimEventHandler(
		ctx, deployment.agglayerBridge, ethClient, querier, bridgeAddr, syncFullClaims, log)

	appender[detailedClaimEventSignature] = buildDetailedClaimEventHandler(deployment.agglayerBridgeL2)
	appender[unsetClaimEventSignature] = buildUnsetClaimEventHandler(deployment.agglayerBridgeL2)
	appender[setClaimEventSignature] = buildSetClaimEventHandler(deployment.agglayerBridgeL2)

	return appender, nil
}

// resolveBridgeDeployment resolves which bridge contract flavor is deployed:
// AgglayerBridge => NonSovereign bridge
// AgglayerBridgeL2 => Sovereign bridge
func resolveBridgeDeployment(
	ctx context.Context,
	bridgeAddr common.Address,
	backend bind.ContractBackend,
) (*bridgeDeployment, error) {
	agglayerBridge, err := agglayerbridge.NewAgglayerbridge(bridgeAddr, backend)
	if err != nil {
		return nil, fmt.Errorf("claimsync: failed to create AgglayerBridge binding (%s): %w", bridgeAddr, err)
	}

	agglayerBridgeL2, err := agglayerbridgel2.NewAgglayerbridgel2(bridgeAddr, backend)
	if err != nil {
		return nil, fmt.Errorf("claimsync: failed to create AgglayerBridgeL2 binding (%s): %w", bridgeAddr, err)
	}

	callOpts := &bind.CallOpts{Pending: false, Context: ctx}

	// 1. Try calling bridgeManager function — only exists on AgglayerBridgeL2
	if _, err := agglayerBridgeL2.BridgeManager(callOpts); err == nil {
		return &bridgeDeployment{
			kind:             SovereignChain,
			agglayerBridge:   agglayerBridge,
			agglayerBridgeL2: agglayerBridgeL2,
		}, nil
	} else if !strings.Contains(err.Error(), gethvm.ErrExecutionReverted.Error()) {
		return nil, fmt.Errorf("claimsync: unexpected error querying AgglayerBridgeL2.BridgeManager (%s): %w",
			bridgeAddr.Hex(), err)
	}

	// 2. If that failed, try lastUpdatedDepositCount function — exists on base AgglayerBridge
	if _, err := agglayerBridge.LastUpdatedDepositCount(callOpts); err == nil {
		return &bridgeDeployment{
			kind:             NonSovereignChain,
			agglayerBridge:   agglayerBridge,
			agglayerBridgeL2: agglayerBridgeL2,
		}, nil
	} else if !strings.Contains(err.Error(), gethvm.ErrExecutionReverted.Error()) {
		return nil, fmt.Errorf("claimsync: unexpected error querying AgglayerBridge.lastUpdatedDepositCount (%s): %w",
			bridgeAddr.Hex(), err)
	}
	// It can't be determined if the bridge is non-sovereign or sovereign
	return &bridgeDeployment{
		kind:             Unknown,
		agglayerBridge:   agglayerBridge,
		agglayerBridgeL2: agglayerBridgeL2,
	}, nil
}

// buildClaimEventHandler creates a handler for the ClaimEvent log.
func buildClaimEventHandler(
	ctx context.Context,
	contract *agglayerbridge.Agglayerbridge,
	client aggkittypes.EthClienter,
	querier ClaimQuerier,
	bridgeAddr common.Address,
	syncFullClaims bool,
	log aggkitcommon.Logger,
) func(*sync.EVMBlock, types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		// Skip if DetailedClaimEvent indexing has already started at this block
		boundaryBlock, err := querier.GetBoundaryBlockForClaimType(ctx, nil, DetailedClaimEvent)
		if err != nil && !errors.Is(err, db.ErrNotFound) {
			return fmt.Errorf("claimsync: failed checking DetailedClaimEvent boundary: %w", err)
		}
		if err == nil && l.BlockNumber >= boundaryBlock {
			log.Debugf("claimsync: skipping ClaimEvent at block %d; DetailedClaimEvent started at %d",
				l.BlockNumber, boundaryBlock)
			return nil
		}

		// Skip if a DetailedClaimEvent for the same tx is already in the block's events
		for _, raw := range b.Events {
			if e, ok := raw.(Event); ok && e.Claim != nil &&
				e.Claim.Type == DetailedClaimEvent && e.Claim.TxHash == l.TxHash {
				log.Debugf("claimsync: skipping ClaimEvent at block %d tx %s; DetailedClaimEvent already present",
					l.BlockNumber, l.TxHash.Hex())
				return nil
			}
		}

		claimEvent, err := contract.ParseClaimEvent(l)
		if err != nil {
			return fmt.Errorf("claimsync: error parsing ClaimEvent log: %w", err)
		}

		claim := &Claim{
			BlockNum:           b.Num,
			BlockPos:           uint64(l.Index),
			BlockTimestamp:     b.Timestamp,
			TxHash:             l.TxHash,
			GlobalIndex:        claimEvent.GlobalIndex,
			OriginNetwork:      claimEvent.OriginNetwork,
			OriginAddress:      claimEvent.OriginAddress,
			DestinationAddress: claimEvent.DestinationAddress,
			Amount:             claimEvent.Amount,
			Type:               ClaimEvent,
		}

		// Extract root call for txn_sender and error checking
		_, rootCall, err := extractCallData(client, bridgeAddr, l.TxHash, log, nil)
		if err != nil {
			return fmt.Errorf("failed to extract claim event tx sender (tx hash: %s): %w", l.TxHash, err)
		}
		// Check if the root call was successful
		if rootCall.Err != nil {
			return fmt.Errorf("execution reverted in root call (block %d, tx hash: %s): %s", b.Num, l.TxHash, *rootCall.Err)
		}

		if syncFullClaims {
			if err := setClaimCalldataFromRoot(claim, rootCall, bridgeAddr, log); err != nil {
				return err
			}
		}

		b.Events = append(b.Events, Event{Claim: claim})
		return nil
	}
}

// buildDetailedClaimEventHandler creates a handler for the DetailedClaimEvent log (sovereign chains).
func buildDetailedClaimEventHandler(
	contract *agglayerbridgel2.Agglayerbridgel2,
) func(*sync.EVMBlock, types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		claimEvent, err := contract.ParseDetailedClaimEvent(l)
		if err != nil {
			return fmt.Errorf("claimsync: error parsing DetailedClaimEvent log: %w", err)
		}

		claim := &Claim{
			BlockNum:            b.Num,
			BlockPos:            uint64(l.Index),
			BlockTimestamp:      b.Timestamp,
			TxHash:              l.TxHash,
			GlobalIndex:         claimEvent.GlobalIndex,
			OriginNetwork:       claimEvent.OriginNetwork,
			OriginAddress:       claimEvent.OriginTokenAddress,
			DestinationNetwork:  claimEvent.DestinationNetwork,
			DestinationAddress:  claimEvent.DestinationAddress,
			Amount:              claimEvent.Amount,
			Metadata:            claimEvent.Metadata,
			MainnetExitRoot:     claimEvent.MainnetExitRoot,
			RollupExitRoot:      claimEvent.RollupExitRoot,
			ProofLocalExitRoot:  treetypes.NewProof(claimEvent.SmtProofLocalExitRoot),
			ProofRollupExitRoot: treetypes.NewProof(claimEvent.SmtProofRollupExitRoot),
			GlobalExitRoot:      crypto.Keccak256Hash(claimEvent.MainnetExitRoot[:], claimEvent.RollupExitRoot[:]),
			IsMessage:           claimEvent.LeafType == uint8(bridgesynctypes.LeafTypeMessage),
			Type:                DetailedClaimEvent,
		}

		// Remove any ClaimEvent for the same tx (DetailedClaimEvent takes precedence)
		newEvents := make([]interface{}, 0, len(b.Events))
		for _, raw := range b.Events {
			if e, ok := raw.(Event); ok && e.Claim != nil &&
				e.Claim.Type == ClaimEvent && e.Claim.TxHash == l.TxHash {
				continue
			}
			newEvents = append(newEvents, raw)
		}
		b.Events = newEvents
		b.Events = append(b.Events, Event{Claim: claim})
		return nil
	}
}

// buildClaimEventHandlerPreEtrog creates a handler for the pre-Etrog ClaimEvent log.
func buildClaimEventHandlerPreEtrog(
	contract *polygonzkevmbridge.Polygonzkevmbridge,
	client aggkittypes.EthClienter,
	bridgeAddr common.Address,
	syncFullClaims bool,
	logger aggkitcommon.Logger,
) func(*sync.EVMBlock, types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		claimEvent, err := contract.ParseClaimEvent(l)
		if err != nil {
			return fmt.Errorf("claimsync: error parsing pre-Etrog ClaimEvent log: %w", err)
		}

		log.Debugf("claimsync: parsed pre-Etrog ClaimEvent: index %d block %d", claimEvent.Index, b.Num)
		claim := &Claim{
			BlockNum:           b.Num,
			BlockPos:           uint64(l.Index),
			BlockTimestamp:     b.Timestamp,
			TxHash:             l.TxHash,
			GlobalIndex:        new(big.Int).SetUint64(uint64(claimEvent.Index)),
			OriginNetwork:      claimEvent.OriginNetwork,
			OriginAddress:      claimEvent.OriginAddress,
			DestinationAddress: claimEvent.DestinationAddress,
			Amount:             claimEvent.Amount,
		}
		// Extract root call for txn_sender and error checking
		_, rootCall, err := extractCallData(client, bridgeAddr, l.TxHash, logger, nil)
		if err != nil {
			return fmt.Errorf("failed to extract claim event tx sender (tx hash: %s): %w", l.TxHash, err)
		}
		// Check if the root call was successful
		if rootCall.Err != nil {
			return fmt.Errorf("execution reverted in root call (block %d, tx hash: %s): %s", b.Num, l.TxHash, *rootCall.Err)
		}

		if syncFullClaims {
			if err := setClaimCalldataFromRoot(claim, rootCall, bridgeAddr, logger); err != nil {
				return err
			}
		}

		b.Events = append(b.Events, Event{Claim: claim})
		return nil
	}
}

// buildUnsetClaimEventHandler creates a handler for the UpdatedUnsetGlobalIndexHashChain log.
func buildUnsetClaimEventHandler(
	contract *agglayerbridgel2.Agglayerbridgel2,
) func(*sync.EVMBlock, types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		event, err := contract.ParseUpdatedUnsetGlobalIndexHashChain(l)
		if err != nil {
			return fmt.Errorf("claimsync: error parsing UpdatedUnsetGlobalIndexHashChain log: %w", err)
		}

		b.Events = append(b.Events, Event{UnsetClaim: &UnsetClaim{
			BlockNum:                  b.Num,
			BlockPos:                  uint64(l.Index),
			TxHash:                    l.TxHash,
			GlobalIndex:               new(big.Int).SetBytes(event.UnsetGlobalIndex[:]),
			UnsetGlobalIndexHashChain: event.NewUnsetGlobalIndexHashChain,
		}})
		return nil
	}
}

// buildSetClaimEventHandler creates a handler for the SetClaim log.
func buildSetClaimEventHandler(
	contract *agglayerbridgel2.Agglayerbridgel2,
) func(*sync.EVMBlock, types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		event, err := contract.ParseSetClaim(l)
		if err != nil {
			return fmt.Errorf("claimsync: error parsing SetClaim log: %w", err)
		}

		b.Events = append(b.Events, Event{SetClaim: &SetClaim{
			BlockNum:    b.Num,
			BlockPos:    uint64(l.Index),
			TxHash:      l.TxHash,
			GlobalIndex: new(big.Int).SetBytes(event.GlobalIndex[:]),
		}})
		return nil
	}
}

type Call struct {
	From  common.Address    `json:"from"`
	To    common.Address    `json:"to"`
	Value *rpctypes.ArgBig  `json:"value"`
	Err   *string           `json:"error"`
	Input rpctypes.ArgBytes `json:"input"`
	Calls []Call            `json:"calls"`
}

type tracerCfg struct {
	Tracer string `json:"tracer"`
}

// findCall traverses the call trace using DFS and either returns the call or stops when a callback succeeds.
func findCall(rootCall Call,
	targetAddr common.Address,
	callback func(Call) (bool, error),
	logger aggkitcommon.Logger,
) ([]*Call, error) {
	callStack := stack.New()
	callStack.Push(rootCall)
	matchingCalls := []*Call{}
	for callStack.Len() > 0 {
		currentCallInterface := callStack.Pop()
		currentCall, ok := currentCallInterface.(Call)
		if !ok {
			return nil, fmt.Errorf("unexpected type for 'currentCall'. Expected 'call', got '%T'", currentCallInterface)
		}

		// Skip reverted calls
		if currentCall.Err != nil {
			logger.Debugf("skipping reverted call to %s from %s: %s",
				currentCall.To.Hex(), currentCall.From.Hex(), *currentCall.Err)
			continue
		}

		if currentCall.To == targetAddr {
			if callback != nil {
				found, err := callback(currentCall)
				if err != nil {
					return nil, err
				}
				if found {
					matchingCalls = append(matchingCalls, &currentCall)
				}
			} else {
				matchingCalls = append(matchingCalls, &currentCall)
			}
		}

		// Add non-reverted calls to the stack
		for _, c := range currentCall.Calls {
			if c.Err == nil {
				callStack.Push(c)
			}
		}
	}
	if len(matchingCalls) > 0 {
		return matchingCalls, nil
	}
	return nil, db.ErrNotFound
}

// extractRootCall extracts the root call for a transaction using debug_traceTransaction.
func extractRootCall(client aggkittypes.RPCClienter, contractAddr common.Address, txHash common.Hash) (*Call, error) {
	rootCall := &Call{To: contractAddr}
	err := client.Call(rootCall, DebugTraceTxEndpoint, txHash, tracerCfg{Tracer: callTracerType})
	if err != nil {
		return nil, err
	}
	return rootCall, nil
}

func extractCallData(
	client aggkittypes.RPCClienter,
	bridgeAddr common.Address,
	txHash common.Hash,
	logger aggkitcommon.Logger,
	callback func(c Call) (bool, error),
) (foundCalls []*Call, rootCall *Call, err error) {
	// Extract root call first
	rootCall, err = extractRootCall(client, bridgeAddr, txHash)
	if err != nil {
		return nil, nil, err
	}

	// Find the specific call to the bridge contract
	foundCalls, err = findCall(*rootCall, bridgeAddr, callback, logger)
	if err != nil {
		return nil, nil, err
	}

	return foundCalls, rootCall, nil
}

// setClaimCalldataFromRoot finds and decodes calldata for the given bridge address using an already traced root call.
//
// Parameters:
// - rootCall: Already traced root call.
// - bridge: Target contract address.
// - logger: Logger instance for debug logging.
//
// Returns an error if calldata isn't found.
func setClaimCalldataFromRoot(
	c *Claim,
	rootCall *Call,
	bridge common.Address,
	logger aggkitcommon.Logger,
) error {
	_, err := findCall(*rootCall, bridge,
		func(call Call) (bool, error) {
			// Skip reverted calls
			if call.Err != nil {
				return false, nil
			}
			return tryDecodeClaimCalldata(c, call.Input, logger)
		}, logger)

	return err
}

// tryDecodeClaimCalldata attempts to find and decode the claim calldata from the provided input bytes.
// It checks if the method ID corresponds to either the claim asset or claim message methods.
// If a match is found, it decodes the calldata using the ABI of the bridge contract and updates the claim object.
// Returns true if the calldata is successfully decoded and matches the expected format, otherwise returns false.
func tryDecodeClaimCalldata(c *Claim, input []byte, logger aggkitcommon.Logger) (bool, error) {
	if len(input) < methodIDLength {
		return false, fmt.Errorf("input too short: %d bytes", len(input))
	}
	methodID := input[:methodIDLength]
	switch {
	case bytes.Equal(methodID, claimAssetEtrogMethodID):
		fallthrough
	case bytes.Equal(methodID, claimMessageEtrogMethodID):
		bridgeV2ABI, err := agglayerbridge.AgglayerbridgeMetaData.GetAbi()
		if err != nil {
			return false, err
		}
		// Recover Method from signature and ABI
		method, err := bridgeV2ABI.MethodById(methodID)
		if err != nil {
			return false, err
		}

		data, err := method.Inputs.Unpack(input[methodIDLength:])
		if err != nil {
			return false, err
		}

		found, err := c.DecodeEtrogCalldata(data)
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

		data, err := method.Inputs.Unpack(input[methodIDLength:])
		if err != nil {
			return false, err
		}

		found, err := c.DecodePreEtrogCalldata(data)
		if err != nil {
			return false, err
		}

		if found {
			c.IsMessage = bytes.Equal(methodID, claimMessagePreEtrogMethodID)
		}

		return found, nil

	default:
		// Log unrecognized method ID for debugging but returns false to continue searching (DFS)
		logger.Debugf("unrecognized method ID encountered during claim calldata extraction: %x", methodID)
		return false, nil
	}
}
