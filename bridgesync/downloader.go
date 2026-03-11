package bridgesync

import (
	"bytes"
	"context"
	"fmt"
	"math/big"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	rpctypes "github.com/0xPolygon/cdk-rpc/types"
	bridgetypes "github.com/agglayer/aggkit/bridgeservice/types"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/agglayer/aggkit/db"
	logger "github.com/agglayer/aggkit/log"
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
	tokenMappingEventSignature = crypto.Keccak256Hash([]byte("NewWrappedToken(uint32,address,address,bytes)"))

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
	backwardLETEventSignature = crypto.Keccak256Hash([]byte("BackwardLET(uint256,bytes32,uint256,bytes32)"))
	forwardLETEventSignature  = crypto.Keccak256Hash([]byte("ForwardLET(uint256,bytes32,uint256,bytes32,bytes)"))

	// bridgeAsset(uint32 destinationNetwork,address destinationAddress,uint256 amount,
	// 	address token,bool forceUpdateGlobalExitRoot,bytes permitData)
	BridgeAssetMethodID = common.Hex2Bytes("cd586579")
	// bridgeMessage(uint32 destinationNetwork,address destinationAddress,
	//  bool forceUpdateGlobalExitRoot,bytes metadata)
	BridgeMessageMethodID = common.Hex2Bytes("240ff378")
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

	bridgeLeafTypeMessage = uint8(bridgesynctypes.LeafTypeMessage)
	bridgeLeafTypeAsset   = uint8(bridgesynctypes.LeafTypeAsset)
)

func buildAppender(
	ctx context.Context,
	client aggkittypes.EthClienter,
	bridgeAddr common.Address,
	syncFromInBridges bool,
	bridgeDeployment *bridgeDeployment,
	logger *logger.Logger,
	claimSync ClaimsSyncProcessor,
) (sync.LogAppenderMap, error) {
	var appender sync.LogAppenderMap
	if claimSync != nil {
		appender = claimSync.BuildAppender()
	} else {
		appender = make(sync.LogAppenderMap)
	}

	// Add event handlers for the bridge contract
	appender[bridgeEventSignature] = buildBridgeEventHandler(
		ctx, bridgeDeployment.agglayerBridge, bridgeAddr, client, syncFromInBridges, logger)
	appender[tokenMappingEventSignature] = buildTokenMappingHandler(bridgeDeployment.agglayerBridge)

	if bridgeDeployment.kind == SovereignChain {
		appender[setSovereignTokenEventSignature] = buildSetSovereignTokenHandler(bridgeDeployment.agglayerBridgeL2)
		appender[migrateLegacyTokenEventSignature] = buildMigrateLegacyTokenHandler(bridgeDeployment.agglayerBridgeL2)
		appender[removeLegacySovereignTokenEventSignature] = buildRemoveLegacyTokenHandler(bridgeDeployment.agglayerBridgeL2)
		appender[backwardLETEventSignature] = buildBackwardLETEventHandler(bridgeDeployment.agglayerBridgeL2)
		appender[forwardLETEventSignature] = buildForwardLETEventHandler(bridgeDeployment.agglayerBridgeL2)

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
	err := client.Call(&tx, GetTransactionByHashEndpoint, txHash.Hex())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch transaction by hash: %w", err)
	}
	return &tx, nil
}

// ExtractTxnAddresses extracts the txn_sender, from address, and to address.
// When syncFromInBridges is false, only extracts txnSender and toAddr using standard RPC,
// and returns zero address for fromAddr (avoids expensive debug_traceTransaction).
func ExtractTxnAddresses(ctx context.Context,
	client aggkittypes.EthClienter,
	bridgeAddr common.Address,
	txHash common.Hash,
	logEvent *agglayerbridge.AgglayerbridgeBridgeEvent,
	logger *logger.Logger,
	syncFromInBridges bool) (txnSender common.Address, fromAddr *common.Address, toAddr common.Address, err error) {
	tx, err := RPCTransactionByHash(client, txHash)
	if err != nil {
		return common.Address{}, nil, common.Address{},
			fmt.Errorf("extractTxnAddresses: failed to extract txn sender from tx_hash:%s: %w", txHash.Hex(), err)
	}
	// For Message events, FromAddress comes from OriginAddress (no tracing needed)
	if logEvent.LeafType == bridgeLeafTypeMessage {
		txnSender = tx.From()
		toAddr = tx.ToAddress()
		originAddr := logEvent.OriginAddress
		return txnSender, &originAddr, toAddr, nil
	}
	// This is a improvement: if the tx is directely sent to the bridge
	// we use the txSender as the from address without doing the expensive debug_traceTransaction,
	if tx.ToAddress() == bridgeAddr {
		txnSender = tx.From()
		toAddr = tx.ToAddress()
		return txnSender, &txnSender, toAddr, nil
	}

	// FromAddress extraction for Asset events requires debug_traceTransaction
	if !syncFromInBridges {
		// Skip expensive extraction - leave FromAddress as nil (will be stored as NULL)
		txnSender = tx.From()
		toAddr = tx.ToAddress()
		logger.Debugf("Skipping FromAddress extraction for tx %s (SyncFromInBridges=false)", txHash.Hex())
		return txnSender, nil, toAddr, nil
	}

	// Extract FromAddress via debug_traceTransaction for Asset events
	// When syncFromInBridges==true, use the original behavior (get txnSender and toAddr from rootCall)
	foundCalls, rootCall, err := extractCallData(client, bridgeAddr, txHash, logger, func(c Call) (bool, error) {
		if logEvent.LeafType == bridgeLeafTypeAsset {
			return bytes.HasPrefix(c.Input, BridgeAssetMethodID), nil
		}
		return false, nil
	})
	if err != nil {
		return common.Address{}, nil, common.Address{},
			fmt.Errorf("extractTxnAddresses:failed to extract bridge event data (tx hash: %s): %w", txHash, err)
	}
	txnSender = rootCall.From
	toAddr = rootCall.To
	fromAddrValue, err := ExtractFromAddrFromCalls(foundCalls, logEvent)
	if err != nil {
		return common.Address{}, nil, common.Address{},
			fmt.Errorf("extractTxnAddresses: failed to extract fromAddr from tx_hash:%s calls: %w",
				txHash.Hex(), err)
	}

	// If extraction returned zero address, treat as nil (NULL in database)
	if fromAddrValue == (common.Address{}) {
		return txnSender, nil, toAddr, nil
	}

	return txnSender, &fromAddrValue, toAddr, nil
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
	syncFromInBridges bool,
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
			bridgeEvent, logger, syncFromInBridges)
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

// buildForwardLETEventHandler creates a handler for the ForwardLET event log
func buildForwardLETEventHandler(contract *agglayerbridgel2.Agglayerbridgel2) func(*sync.EVMBlock, types.Log) error {
	return func(b *sync.EVMBlock, l types.Log) error {
		event, err := contract.ParseForwardLET(l)
		if err != nil {
			return fmt.Errorf("error parsing ForwardLET event log %+v: %w", l, err)
		}

		b.Events = append(b.Events, Event{ForwardLET: &ForwardLET{
			BlockNum:             b.Num,
			BlockPos:             uint64(l.Index),
			BlockTimestamp:       b.Timestamp,
			TxnHash:              l.TxHash,
			PreviousDepositCount: event.PreviousDepositCount,
			PreviousRoot:         event.PreviousRoot,
			NewDepositCount:      event.NewDepositCount,
			NewRoot:              event.NewRoot,
			NewLeaves:            event.NewLeaves,
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
