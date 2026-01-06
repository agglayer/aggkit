package bridgesync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/polygonzkevmbridge"
	rpctypes "github.com/0xPolygon/cdk-rpc/types"
	bridgetypes "github.com/agglayer/aggkit/bridgeservice/types"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/agglayer/aggkit/db"
	logger "github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	treetypes "github.com/agglayer/aggkit/tree/types"
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
	detailedClaimEventSignature = crypto.Keccak256Hash([]byte(
		"DetailedClaimEvent(bytes32[32],bytes32[32]," +
			"uint256,bytes32,bytes32,uint8,uint32," +
			"address,uint32,address,uint256,bytes)",
	))
	setSovereignTokenEventSignature = crypto.Keccak256Hash([]byte(
		"SetSovereignTokenAddress(uint32,address,address,bool)",
	))
	migrateLegacyTokenEventSignature = crypto.Keccak256Hash([]byte(
		"MigrateLegacyToken(address,address,address,uint256)",
	))
	removeLegacySovereignTokenEventSignature = crypto.Keccak256Hash([]byte(
		"RemoveLegacySovereignTokenAddress(address)",
	))
	unsetClaimEventSignature = crypto.Keccak256Hash([]byte(
		"UpdatedUnsetGlobalIndexHashChain(bytes32,bytes32)",
	))
	setClaimEventSignature = crypto.Keccak256Hash([]byte(
		"SetClaim(bytes32)",
	))
	backwardLETEventSignature = crypto.Keccak256Hash([]byte("BackwardLET(uint256,bytes32,uint256,bytes32)"))

	claimAssetEtrogMethodID      = common.Hex2Bytes("ccaa2d11")
	claimMessageEtrogMethodID    = common.Hex2Bytes("f5efcd79")
	claimAssetPreEtrogMethodID   = common.Hex2Bytes("2cffd02e")
	claimMessagePreEtrogMethodID = common.Hex2Bytes("2d2c9d94")

	// bridgeAsset(uint32 destinationNetwork,address destinationAddress,uint256 amount,
	// 	address token,bool forceUpdateGlobalExitRoot,bytes permitData)
	BridgeAssetMethodID = common.Hex2Bytes("cd586579")
	// bridgeMessage(uint32 destinationNetwork,address destinationAddress,
	//  bool forceUpdateGlobalExitRoot,bytes metadata)
	BridgeMessageMethodID = common.Hex2Bytes("240ff378")
)

const (
	// debugTraceTxEndpoint is the name of the debug method used to trace a transaction.
	debugTraceTxEndpoint = "debug_traceTransaction"

	// callTracerType is the name of the call tracer
	callTracerType = "callTracer"

	// methodIDLength is the length of the method ID in bytes
	methodIDLength = 4

	bridgeLeafTypeMessage = uint8(bridgesynctypes.LeafTypeMessage)
	bridgeLeafTypeAsset   = uint8(bridgesynctypes.LeafTypeAsset)
)

const (
	// CallTypeCall is a callTracer CALL frame.
	CallTypeCall = "CALL"
	// CallTypeDelegateCall is a callTracer DELEGATECALL frame.
	CallTypeDelegateCall = "DELEGATECALL"
	// CallTypeStaticCall is a callTracer STATICCALL frame.
	CallTypeStaticCall = "STATICCALL"
	// CallTypeCallCode is a callTracer CALLCODE frame.
	CallTypeCallCode = "CALLCODE"
	// CallTypeCreate is a callTracer CREATE frame.
	CallTypeCreate = "CREATE"
	// CallTypeCreate2 is a callTracer CREATE2 frame.
	CallTypeCreate2 = "CREATE2"
	// CallTypeSelfDestruct is a callTracer SELFDESTRUCT frame.
	CallTypeSelfDestruct = "SELFDESTRUCT"
)

func buildAppender(
	ctx context.Context,
	client aggkittypes.EthClienter,
	querier BridgeQuerier,
	bridgeAddr common.Address,
	syncFullClaims bool,
	bridgeDeployment *bridgeDeployment,
	logger *logger.Logger,
) (sync.LogAppenderMap, error) {
	legacyBridge, err := polygonzkevmbridge.NewPolygonzkevmbridge(bridgeAddr, client)
	if err != nil {
		return nil, fmt.Errorf("failed to create PolygonZkEVMBridge SC binding (bridge addr: %s): %w", bridgeAddr, err)
	}

	appender := make(sync.LogAppenderMap)

	// Add event handlers for the bridge contract
	appender[bridgeEventSignature] = buildBridgeEventHandler(
		ctx, bridgeDeployment.agglayerBridge, bridgeAddr, client, logger)
	appender[claimEventSignaturePreEtrog] = buildClaimEventHandlerPreEtrog(
		legacyBridge, client, bridgeAddr, syncFullClaims, logger)
	appender[claimEventSignature] = buildClaimEventHandler(ctx, bridgeDeployment.agglayerBridge, client, querier,
		bridgeAddr, syncFullClaims, logger)
	appender[tokenMappingEventSignature] = buildTokenMappingHandler(bridgeDeployment.agglayerBridge)

	if bridgeDeployment.kind == SovereignChain {
		appender[detailedClaimEventSignature] = buildDetailedClaimEventHandler(bridgeDeployment.agglayerBridgeL2)
		appender[setSovereignTokenEventSignature] = buildSetSovereignTokenHandler(bridgeDeployment.agglayerBridgeL2)
		appender[migrateLegacyTokenEventSignature] = buildMigrateLegacyTokenHandler(bridgeDeployment.agglayerBridgeL2)
		appender[removeLegacySovereignTokenEventSignature] = buildRemoveLegacyTokenHandler(bridgeDeployment.agglayerBridgeL2)
		appender[unsetClaimEventSignature] = buildUnsetClaimEventHandler(bridgeDeployment.agglayerBridgeL2)
		appender[setClaimEventSignature] = buildSetClaimEventHandler(bridgeDeployment.agglayerBridgeL2)
		appender[backwardLETEventSignature] = buildBackwardLETEventHandler(bridgeDeployment.agglayerBridgeL2)

		return appender, nil
	}

	if bridgeDeployment.kind != NonSovereignChain {
		return nil, fmt.Errorf("unsupported bridge deployment kind: %d", bridgeDeployment.kind)
	}

	return appender, nil
}

// Transaction represents the structure of a transaction returned by eth_getTransactionByHash
type Transaction struct {
	FromRaw          string `json:"from"`
	To               string `json:"to"`
	Hash             string `json:"hash"`
	Value            string `json:"value"`
	Gas              string `json:"gas"`
	GasPrice         string `json:"gasPrice"`
	Nonce            string `json:"nonce"`
	Input            string `json:"input"`
	BlockHash        string `json:"blockHash"`
	BlockNumber      string `json:"blockNumber"`
	TransactionIndex string `json:"transactionIndex"`
}

func (t *Transaction) From() common.Address {
	return common.HexToAddress(t.FromRaw)
}

func (t *Transaction) ToAddress() common.Address {
	if t.To == "" {
		return common.Address{}
	}
	return common.HexToAddress(t.To)
}

func RPCTransactionByHash(client aggkittypes.EthClienter,
	txHash common.Hash) (*Transaction, error) {
	// Use client.Call to fetch transaction details using eth_getTransactionByHash
	var tx Transaction
	err := client.Call(&tx, "eth_getTransactionByHash", txHash.Hex())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch transaction by hash: %w", err)
	}
	return &tx, nil
}

// ExtractTxnAddresses extracts the txn_sender, from address, and to address from the transaction trace.
func ExtractTxnAddresses(ctx context.Context,
	client aggkittypes.EthClienter,
	bridgeAddr common.Address,
	txHash common.Hash,
	logEvent *agglayerbridge.AgglayerbridgeBridgeEvent,
	logger *logger.Logger) (txnSender common.Address, fromAddr common.Address, toAddr common.Address, err error) {
	// If event is a message, fromAddr is log.origin_address
	// so we only need the txn_sender that can be obtained from hash_receipt
	// and toAddr from the transaction receipt (same source as txn_sender)
	if logEvent.LeafType == bridgeLeafTypeMessage {
		tx, err := RPCTransactionByHash(client, txHash)
		if err != nil {
			return common.Address{}, common.Address{}, common.Address{},
				fmt.Errorf("extractTxnAddresses: failed to extract txn sender from tx_hash:%s: %w", txHash.Hex(), err)
		}
		txnSender = tx.From()
		toAddr = tx.ToAddress()
		return txnSender, logEvent.OriginAddress, toAddr, nil
	}
	foundCalls, rootCall, err := extractCallData(client, bridgeAddr, txHash, logger, func(c Call) (bool, error) {
		if logEvent.LeafType == bridgeLeafTypeAsset {
			return bytes.HasPrefix(c.Input, BridgeAssetMethodID), nil
		}
		return false, nil
	})
	if err != nil {
		return common.Address{}, common.Address{}, common.Address{},
			fmt.Errorf("extractTxnAddresses:failed to extract bridge event data (tx hash: %s): %w", txHash, err)
	}
	txnSender = rootCall.From
	toAddr = rootCall.To
	fromAddr, err = ExtractFromAddrFromCalls(foundCalls, logEvent)
	if err != nil {
		return common.Address{}, common.Address{}, common.Address{},
			fmt.Errorf("extractTxnAddresses: failed to extract fromAddr from tx_hash:%s calls: %w",
				txHash.Hex(), err)
	}

	return txnSender, fromAddr, toAddr, nil
}

type bridgeCallParams struct {
	LeafType           uint8
	DestinationNetwork uint32
	DestinationAddress common.Address
	Amount             *big.Int
	// can't use Token because could be a token network that native eth is a token or a wrapped token
	// in these cases the calling value doesn't match the event (event field: OriginTokenAddress)
	Token common.Address
}

func (b *bridgeCallParams) String() string {
	if b == nil {
		return "<nil>"
	}
	return fmt.Sprintf("LeafType: %d, DestinationNetwork: %d, DestinationAddress: %s, Amount: %s, Token: %s",
		b.LeafType, b.DestinationNetwork, b.DestinationAddress.Hex(), b.Amount.String(), b.Token.Hex())
}

func (b *bridgeCallParams) Equal(logEvent *agglayerbridge.AgglayerbridgeBridgeEvent) bool {
	return b.LeafType == logEvent.LeafType &&
		b.DestinationNetwork == logEvent.DestinationNetwork &&
		b.DestinationAddress == logEvent.DestinationAddress &&
		b.Amount.Cmp(logEvent.Amount) == 0
}

func ExtractParamFromCallData(callData []byte) (*bridgeCallParams, error) {
	if len(callData) < methodIDLength {
		return nil, fmt.Errorf("call data too short to extract method ID")
	}
	bridgeV2ABI, err := agglayerbridge.AgglayerbridgeMetaData.GetAbi()
	if err != nil {
		return nil, fmt.Errorf("failed to get bridge V2 ABI: %w", err)
	}
	methodID := callData[:methodIDLength]
	method, err := bridgeV2ABI.MethodById(methodID)
	if err != nil {
		return nil, fmt.Errorf("failed to get method %s by ID: %w", common.Bytes2Hex(methodID), err)
	}
	data, err := method.Inputs.Unpack(callData[methodIDLength:])
	if err != nil {
		return nil, fmt.Errorf("failed to unpack inputs call data: %w", err)
	}
	destinationNetwork, ok := data[0].(uint32)
	if !ok {
		return nil, fmt.Errorf("failed to assert destinationNetwork as uint32")
	}
	destinationAddress, ok := data[1].(common.Address)
	if !ok {
		return nil, fmt.Errorf("failed to assert destinationAddress as common.Address")
	}

	if bytes.HasPrefix(callData, BridgeMessageMethodID) {
		return &bridgeCallParams{
			LeafType:           bridgeLeafTypeMessage,
			DestinationNetwork: destinationNetwork,
			DestinationAddress: destinationAddress,
			Amount:             big.NewInt(0),
		}, nil
	}
	if bytes.HasPrefix(callData, BridgeAssetMethodID) {
		amount, ok := data[2].(*big.Int)
		if !ok {
			return nil, fmt.Errorf("failed to assert amount as *big.Int")
		}
		token, ok := data[3].(common.Address)
		if !ok {
			return nil, fmt.Errorf("failed to assert token as common.Address")
		}
		return &bridgeCallParams{
			LeafType:           bridgeLeafTypeAsset,
			DestinationNetwork: destinationNetwork,
			DestinationAddress: destinationAddress,
			Amount:             amount,
			Token:              token,
		}, nil
	}
	return nil, fmt.Errorf("unsupported call data method ID: %s (only support BridgeAssetMethodID)",
		common.Bytes2Hex(callData[:methodIDLength]))
}

func haveCommonFromForCalls(calls []*Call) (common.Address, bool) {
	if len(calls) == 0 {
		return common.Address{}, false
	}

	commonFrom := calls[0].From
	for _, call := range calls[1:] {
		if call.From != commonFrom {
			return common.Address{}, false
		}
	}

	return commonFrom, true
}

func ExtractFromAddrFromCalls(foundCalls []*Call,
	logEvent *agglayerbridge.AgglayerbridgeBridgeEvent) (common.Address, error) {
	switch len(foundCalls) {
	case 0:
		return common.Address{}, fmt.Errorf("extractFromAddrFromCalls: no calls found")
	case 1:
		return foundCalls[0].From, nil
	default:
		// If all calls have same From we don't need to dig further
		commonFrom, ok := haveCommonFromForCalls(foundCalls)
		if ok {
			return commonFrom, nil
		}
		// Multiple calls found, try to find addr
		var candidate *Call
		var candidateCallParams *bridgeCallParams
		for _, call := range foundCalls {
			callParams, err := ExtractParamFromCallData(call.Input)
			if err != nil {
				return common.Address{}, fmt.Errorf("extractFromAddrFromCalls: failed to extract bridge call params: %w", err)
			}
			if callParams.Equal(logEvent) {
				if candidate != nil {
					// Desperate try: to match by token address
					if candidateCallParams.Token.Hex() == logEvent.OriginAddress.Hex() {
						continue
					}
					if callParams.Token.Hex() != logEvent.OriginAddress.Hex() {
						return common.Address{}, fmt.Errorf("extractFromAddrFromCalls: multiple matching "+
							"calls found to extract txn sender. Previous: %s, Current: %s Event: OriginAddress: %s",
							candidateCallParams.String(), callParams.String(), logEvent.OriginAddress.Hex())
					}
				}
				candidate = call
				candidateCallParams = callParams
			}
		}
		if candidate == nil || candidateCallParams == nil {
			return common.Address{}, fmt.Errorf("extractFromAddrFromCalls: no matching call found")
		}
		return candidate.From, nil
	}
}

// buildBridgeEventHandler creates a handler for the Bridge event log.
func buildBridgeEventHandler(
	ctx context.Context,
	contract *agglayerbridge.Agglayerbridge,
	bridgeAddr common.Address,
	client aggkittypes.EthClienter,
	logger *logger.Logger,
) func(*sync.EVMBlock, types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		bridgeEvent, err := contract.ParseBridgeEvent(l)
		if err != nil {
			return fmt.Errorf("error parsing BridgeEvent log %+v: %w", l, err)
		}
		logger.Debugf("Parsed BridgeEvent: LeafType: %d, OriginNetwork:%d, OriginAddress: %s\n"+
			"DestinationNetwork: %d, DestinationAddress: %s, DepositCount: %d, Amount: %s, ",
			bridgeEvent.LeafType, bridgeEvent.OriginNetwork, bridgeEvent.OriginAddress.Hex(), bridgeEvent.DestinationNetwork,
			bridgeEvent.DestinationAddress.Hex(), bridgeEvent.DepositCount, bridgeEvent.Amount.String())
		txnSender, fromAddress, toAddress, err := ExtractTxnAddresses(ctx, client, bridgeAddr, l.TxHash,
			bridgeEvent, logger)
		if err != nil {
			return fmt.Errorf("failed to extract bridge event data (tx hash: %s): %w", l.TxHash, err)
		}

		b.Events = append(b.Events, Event{Bridge: &Bridge{
			BlockNum:           b.Num,
			BlockPos:           uint64(l.Index),
			FromAddress:        fromAddress,
			TxHash:             l.TxHash,
			BlockTimestamp:     b.Timestamp,
			LeafType:           bridgeEvent.LeafType,
			OriginNetwork:      bridgeEvent.OriginNetwork,
			OriginAddress:      bridgeEvent.OriginAddress,
			DestinationNetwork: bridgeEvent.DestinationNetwork,
			DestinationAddress: bridgeEvent.DestinationAddress,
			Amount:             bridgeEvent.Amount,
			Metadata:           bridgeEvent.Metadata,
			DepositCount:       bridgeEvent.DepositCount,
			TxnSender:          txnSender,
			ToAddress:          toAddress,
		}})
		return nil
	}
}

// buildClaimEventHandler creates a handler for the Claim event log.
func buildClaimEventHandler(ctx context.Context, agglayerBridge *agglayerbridge.Agglayerbridge,
	client aggkittypes.EthClienter, querier BridgeQuerier, bridgeAddr common.Address,
	syncFullClaims bool, logger *logger.Logger,
) func(*sync.EVMBlock, types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		// check if we already have passed the block which started indexing DetailedClaimEvent
		boundaryBlock, err := querier.GetBoundaryBlockForClaimType(ctx, DetailedClaimEvent)
		if err != nil && !errors.Is(err, db.ErrNotFound) {
			return fmt.Errorf("failed checking DetailedClaimEvent boundary: %w", err)
		}

		if err == nil && l.BlockNumber >= boundaryBlock {
			logger.Debugf(
				"Skipping ClaimEvent at block %d; DetailedClaimEvent indexing already started at block %d",
				l.BlockNumber, boundaryBlock,
			)
			return nil
		}

		// Skip if a DetailedClaimEvent for the same transaction is already in this block's events.
		// Check early to avoid the expensive extractCallData RPC call.
		for _, raw := range b.Events {
			if e, ok := raw.(Event); ok && e.Claim != nil && e.Claim.Type == DetailedClaimEvent && e.Claim.TxHash == l.TxHash {
				logger.Debugf(
					"Skipping ClaimEvent at block %d tx %s; DetailedClaimEvent already present in block",
					l.BlockNumber, l.TxHash.Hex(),
				)
				return nil
			}
		}

		claimEvent, err := agglayerBridge.ParseClaimEvent(l)
		if err != nil {
			return fmt.Errorf("error parsing Claim event log %+v: %w", l, err)
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
		_, rootCall, err := extractCallData(client, bridgeAddr, l.TxHash, logger, nil)
		if err != nil {
			return fmt.Errorf("failed to extract claim event tx sender (tx hash: %s): %w", l.TxHash, err)
		}
		// Check if the root call was successful
		if rootCall.Err != nil {
			return fmt.Errorf("execution reverted in root call (block %d, tx hash: %s): %s", b.Num, l.TxHash, *rootCall.Err)
		}

		if syncFullClaims {
			if err := claim.setClaimCalldataFromRoot(rootCall, bridgeAddr, logger); err != nil {
				return err
			}
		}

		b.Events = append(b.Events, Event{Claim: claim})
		return nil
	}
}

// buildDetailedClaimEventHandler creates a handler for the DetailedClaimEvent event log.
func buildDetailedClaimEventHandler(contract *agglayerbridgel2.Agglayerbridgel2,
) func(*sync.EVMBlock, types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		claimEvent, err := contract.ParseDetailedClaimEvent(l)
		if err != nil {
			return fmt.Errorf("error parsing DetailedClaimEvent event log %+v: %w", l, err)
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

		// Remove any ClaimEvent for the same transaction already collected in this block.
		// Both ClaimEvent and DetailedClaimEvent are emitted on sovereign chains; DetailedClaimEvent takes precedence.
		newEvents := make([]interface{}, 0, len(b.Events))
		for _, raw := range b.Events {
			if e, ok := raw.(Event); ok && e.Claim != nil && e.Claim.Type == ClaimEvent && e.Claim.TxHash == l.TxHash {
				continue
			}
			newEvents = append(newEvents, raw)
		}
		b.Events = newEvents

		b.Events = append(b.Events, Event{Claim: claim})
		return nil
	}
}

// buildClaimEventHandlerPreEtrog creates a handler for the Claim event log for pre-Etrog contracts.
func buildClaimEventHandlerPreEtrog(contract *polygonzkevmbridge.Polygonzkevmbridge,
	client aggkittypes.EthClienter, bridgeAddr common.Address, syncFullClaims bool, logger *logger.Logger,
) func(*sync.EVMBlock, types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		claimEvent, err := contract.ParseClaimEvent(l)
		if err != nil {
			return fmt.Errorf("error parsing Claim event log %+v: %w", l, err)
		}

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
			if err := claim.setClaimCalldataFromRoot(rootCall, bridgeAddr, logger); err != nil {
				return err
			}
		}

		b.Events = append(b.Events, Event{Claim: claim})
		return nil
	}
}

// buildTokenMappingHandler creates a handler for the NewWrappedToken event log.
func buildTokenMappingHandler(contract *agglayerbridge.Agglayerbridge,
) func(*sync.EVMBlock, types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		tokenMappingEvent, err := contract.ParseNewWrappedToken(l)
		if err != nil {
			return fmt.Errorf("error parsing NewWrappedToken event log %+v: %w", l, err)
		}

		b.Events = append(b.Events, Event{
			TokenMapping: &TokenMapping{
				BlockNum:            b.Num,
				BlockPos:            uint64(l.Index),
				BlockTimestamp:      b.Timestamp,
				TxHash:              l.TxHash,
				OriginNetwork:       tokenMappingEvent.OriginNetwork,
				OriginTokenAddress:  tokenMappingEvent.OriginTokenAddress,
				WrappedTokenAddress: tokenMappingEvent.WrappedTokenAddress,
				Metadata:            tokenMappingEvent.Metadata,
				Type:                bridgetypes.WrappedToken,
			}})
		return nil
	}
}

// buildSetSovereignTokenHandler creates a handler for the SetSovereignTokenAddress event log.
func buildSetSovereignTokenHandler(contract *agglayerbridgel2.Agglayerbridgel2,
) func(*sync.EVMBlock, types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		event, err := contract.ParseSetSovereignTokenAddress(l)
		if err != nil {
			return fmt.Errorf("error parsing SetSovereignTokenAddress event log %+v: %w", l, err)
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
			Type:                bridgetypes.SovereignToken,
		}})
		return nil
	}
}

// buildMigrateLegacyTokenHandler creates a handler for the MigrateLegacyToken event log.
func buildMigrateLegacyTokenHandler(contract *agglayerbridgel2.Agglayerbridgel2,
) func(*sync.EVMBlock, types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		event, err := contract.ParseMigrateLegacyToken(l)
		if err != nil {
			return fmt.Errorf("error parsing MigrateLegacyToken event log %+v: %w", l, err)
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
		}})
		return nil
	}
}

// buildRemoveLegacyTokenHandler creates a handler for the RemoveLegacySovereignTokenAddress event log.
func buildRemoveLegacyTokenHandler(contract *agglayerbridgel2.Agglayerbridgel2) func(*sync.EVMBlock,
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

// buildUnsetClaimEventHandler creates a handler for the UpdatedUnsetGlobalIndexHashChain event log
func buildUnsetClaimEventHandler(contract *agglayerbridgel2.Agglayerbridgel2) func(*sync.EVMBlock,
	types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		event, err := contract.ParseUpdatedUnsetGlobalIndexHashChain(l)
		if err != nil {
			return fmt.Errorf("error parsing UpdatedUnsetGlobalIndexHashChain event log %+v: %w", l, err)
		}

		// Convert bytes32 to big.Int
		globalIndex := new(big.Int).SetBytes(event.UnsetGlobalIndex[:])

		b.Events = append(b.Events, Event{UnsetClaim: &UnsetClaim{
			BlockNum:                  b.Num,
			BlockPos:                  uint64(l.Index),
			TxHash:                    l.TxHash,
			GlobalIndex:               globalIndex,
			UnsetGlobalIndexHashChain: event.NewUnsetGlobalIndexHashChain,
		}})
		return nil
	}
}

// buildSetClaimEventHandler creates a handler for the SetClaim event log
func buildSetClaimEventHandler(contract *agglayerbridgel2.Agglayerbridgel2) func(*sync.EVMBlock, types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		event, err := contract.ParseSetClaim(l)
		if err != nil {
			return fmt.Errorf("error parsing SetClaim event log %+v: %w", l, err)
		}

		// Convert bytes32 to big.Int
		globalIndex := new(big.Int).SetBytes(event.GlobalIndex[:])

		b.Events = append(b.Events, Event{SetClaim: &SetClaim{
			BlockNum:    b.Num,
			BlockPos:    uint64(l.Index),
			TxHash:      l.TxHash,
			GlobalIndex: globalIndex,
		}})
		return nil
	}
}

// buildBackwardLETEventHandler creates a handler for the BackwardLET event log
func buildBackwardLETEventHandler(contract *agglayerbridgel2.Agglayerbridgel2) func(*sync.EVMBlock, types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		event, err := contract.ParseBackwardLET(l)
		if err != nil {
			return fmt.Errorf("error parsing BackwardLET event log %+v: %w", l, err)
		}

		b.Events = append(b.Events, Event{BackwardLET: &BackwardLET{
			BlockNum:             b.Num,
			BlockPos:             uint64(l.Index),
			PreviousDepositCount: event.PreviousDepositCount,
			PreviousRoot:         event.PreviousRoot,
			NewDepositCount:      event.NewDepositCount,
			NewRoot:              event.NewRoot,
		}})
		return nil
	}
}

type Call struct {
	Type  string            `json:"type"`
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
func findCall(rootCall Call, targetAddr common.Address, callback func(Call) (bool, error), logger *logger.Logger,
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

		if currentCall.To == targetAddr && currentCall.Type == CallTypeCall {
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
	err := client.Call(rootCall, debugTraceTxEndpoint, txHash, tracerCfg{Tracer: callTracerType})
	if err != nil {
		return nil, err
	}
	return rootCall, nil
}

func extractCallData(
	client aggkittypes.RPCClienter,
	bridgeAddr common.Address,
	txHash common.Hash,
	logger *logger.Logger,
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
func (c *Claim) setClaimCalldataFromRoot(
	rootCall *Call,
	bridge common.Address,
	logger *logger.Logger,
) error {
	_, err := findCall(*rootCall, bridge,
		func(call Call) (bool, error) {
			// Skip reverted calls
			if call.Err != nil {
				return false, nil
			}
			return c.tryDecodeClaimCalldata(call.Input, logger)
		}, logger)

	return err
}

// tryDecodeClaimCalldata attempts to find and decode the claim calldata from the provided input bytes.
// It checks if the method ID corresponds to either the claim asset or claim message methods.
// If a match is found, it decodes the calldata using the ABI of the bridge contract and updates the claim object.
// Returns true if the calldata is successfully decoded and matches the expected format, otherwise returns false.
func (c *Claim) tryDecodeClaimCalldata(input []byte, logger *logger.Logger) (bool, error) {
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

		data, err := method.Inputs.Unpack(input[methodIDLength:])
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
		// Log unrecognized method ID for debugging but returns false to continue searching (DFS)
		logger.Debugf("unrecognized method ID encountered during claim calldata extraction: %x", methodID)
		return false, nil
	}
}
