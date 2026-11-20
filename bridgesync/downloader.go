package bridgesync

import (
	"bytes"
	"context"
	"errors"
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

const (
	// maxFromAddressRPCAttempts is how many times in a row a bridge event is retried when the RPC calls that
	// resolve its from address fail. After that the event is stored with a NULL from address, so a tx that the
	// node cannot trace (or a node without the debug API) does not halt the syncer.
	maxFromAddressRPCAttempts = 5
)

// ErrFromAddressRPC is returned (wrapped) by ExtractTxnAddresses when an RPC call needed to resolve the from
// address of an asset bridge event fails. The from address is optional, so callers may retry a bounded number
// of times and then store the event without it.
var ErrFromAddressRPC = errors.New("from address RPC failure")

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
	bridgeAddr common.Address,
	syncFromInBridges bool,
	bridgeDeployment *bridgeDeployment,
	logger *logger.Logger,
	resolvers ...bridgeTxnResolver,
) (sync.LogAppenderMap, error) {
	appender := make(sync.LogAppenderMap)

	var resolver bridgeTxnResolver
	if len(resolvers) > 0 {
		resolver = resolvers[0]
	}

	// Add event handlers for the bridge contract
	appender[bridgeEventSignature] = buildBridgeEventHandler(
		ctx, bridgeDeployment.agglayerBridge, bridgeAddr, client, syncFromInBridges, logger, resolver)
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

// buildContextualAppender builds the contextual appenders installed on the bridge downloader.
//
// Only the BridgeEvent handler needs context-bound work: it resolves the transaction that produced
// the log (through the shared provider on the multidownloader path, or eth_getTransactionByHash) and
// may trace it (debug_traceTransaction); any of those can block. Receiving the active download context
// (instead of a constructor-time context) means a cancelled download generation unblocks it. All the
// other bridge events are synchronous and keep their legacy appenders, so the returned map contains
// only bridgeEventSignature.
func buildContextualAppender(
	bridgeAddr common.Address,
	client aggkittypes.EthClienter,
	syncFromInBridges bool,
	bridgeDeployment *bridgeDeployment,
	logger *logger.Logger,
	resolver bridgeTxnResolver,
) sync.ContextualLogAppenderMap {
	return sync.ContextualLogAppenderMap{
		bridgeEventSignature: buildBridgeEventAppender(
			bridgeDeployment.agglayerBridge, bridgeAddr, client, syncFromInBridges, logger, resolver),
	}
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

// RPCTransactionByHash fetches the transaction details using eth_getTransactionByHash. It is not
// bound to any context; callers that run inside a download use rpcTransactionByHashContext.
func RPCTransactionByHash(client aggkittypes.EthClienter,
	txHash common.Hash) (*Transaction, error) {
	return rpcTransactionByHashContext(context.Background(), client, txHash)
}

// rpcTransactionByHashContext fetches the transaction details using eth_getTransactionByHash with
// ctx, so a cancelled download generation never stays blocked on a stalled RPC.
func rpcTransactionByHashContext(ctx context.Context, client aggkittypes.EthClienter,
	txHash common.Hash) (*Transaction, error) {
	var tx Transaction
	err := client.CallContext(ctx, &tx, GetTransactionByHashEndpoint, txHash.Hex())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch transaction by hash: %w", err)
	}
	return &tx, nil
}

// legacyTransactionToRPCTransaction adapts the legacy bridgesync transaction DTO to the shared
// aggkittypes.RPCTransaction representation, so both the legacy RPC path and the shared transaction
// provider path can share the same address-extraction logic. It preserves the exact From()/ToAddress()
// semantics of the legacy DTO.
func legacyTransactionToRPCTransaction(tx *Transaction) *aggkittypes.RPCTransaction {
	if tx == nil {
		return nil
	}
	rpcTx := &aggkittypes.RPCTransaction{
		FromRaw:     tx.FromRaw,
		HashRaw:     tx.Hash,
		ValueRaw:    tx.Value,
		GasRaw:      tx.Gas,
		GasPriceRaw: tx.GasPrice,
		NonceRaw:    tx.Nonce,
		InputRaw:    tx.Input,
	}
	if tx.To != "" {
		to := tx.To
		rpcTx.To = &to
	}
	if tx.BlockHash != "" {
		blockHash := tx.BlockHash
		rpcTx.BlockHashRaw = &blockHash
	}
	if tx.BlockNumber != "" {
		blockNumber := tx.BlockNumber
		rpcTx.BlockNumberRaw = &blockNumber
	}
	if tx.TransactionIndex != "" {
		txIndex := tx.TransactionIndex
		rpcTx.TransactionIndexRaw = &txIndex
	}
	return rpcTx
}

// ExtractTxnAddresses extracts the txn_sender, from address, and to address.
// When syncFromInBridges is false, only extracts txnSender and toAddr using standard RPC,
// and returns zero address for fromAddr (avoids expensive debug_traceTransaction).
//
// This is the legacy entry point: it always resolves the transaction through
// eth_getTransactionByHash (RPCTransactionByHash). Callers using the shared multidownloader must
// resolve the transaction through aggkittypes.TransactionProvider.TransactionByLog and call
// extractTxnAddressesWithTx instead.
func ExtractTxnAddresses(ctx context.Context,
	client aggkittypes.EthClienter,
	bridgeAddr common.Address,
	txHash common.Hash,
	logEvent *agglayerbridge.AgglayerbridgeBridgeEvent,
	logger *logger.Logger,
	syncFromInBridges bool) (txnSender common.Address, fromAddr *common.Address, toAddr common.Address, err error) {
	tx, err := rpcTransactionByHashContext(ctx, client, txHash)
	if err != nil {
		return common.Address{}, nil, common.Address{},
			fmt.Errorf("extractTxnAddresses: failed to extract txn sender from tx_hash:%s: %w", txHash.Hex(), err)
	}
	return extractTxnAddressesWithTx(ctx, client, bridgeAddr, txHash, legacyTransactionToRPCTransaction(tx),
		logEvent, logger, syncFromInBridges)
}

// extractTxnAddressesWithTx extracts the txn_sender, from address and to address from an
// already-resolved transaction. It is the shared implementation used by both the legacy
// eth_getTransactionByHash path and the multidownloader transaction-provider path. The debug
// trace (debug_traceTransaction) is always performed through the passed client, which is unchanged
// by the transaction source.
func extractTxnAddressesWithTx(ctx context.Context,
	client aggkittypes.EthClienter,
	bridgeAddr common.Address,
	txHash common.Hash,
	tx *aggkittypes.RPCTransaction,
	logEvent *agglayerbridge.AgglayerbridgeBridgeEvent,
	logger *logger.Logger,
	syncFromInBridges bool) (txnSender common.Address, fromAddr *common.Address, toAddr common.Address, err error) {
	if tx == nil {
		return common.Address{}, nil, common.Address{},
			fmt.Errorf("extractTxnAddresses: transaction is not available for tx_hash:%s", txHash.Hex())
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

	// Extract FromAddress via debug_traceTransaction for Asset events.
	// FromAddress is optional metadata: only RPC failures are returned (wrapped in ErrFromAddressRPC) so the
	// caller can retry them. Anything the tx itself makes unresolvable leaves FromAddress nil (NULL) instead of
	// failing, because the same tx would fail the same way forever and halt the syncer.
	rootCall, err := extractRootCall(ctx, client, bridgeAddr, txHash)
	if err != nil {
		return common.Address{}, nil, common.Address{},
			fmt.Errorf("extractTxnAddresses: %w: failed to trace tx_hash:%s: %w", ErrFromAddressRPC, txHash.Hex(), err)
	}
	txnSender = rootCall.From
	toAddr = rootCall.To

	var foundCalls []*Call
	if logEvent.LeafType == bridgeLeafTypeAsset {
		foundCalls, err = findCall(*rootCall, bridgeAddr, func(c Call) (bool, error) {
			return bytes.HasPrefix(c.Input, BridgeAssetMethodID), nil
		}, logger)
		if err != nil && !errors.Is(err, db.ErrNotFound) {
			return common.Address{}, nil, common.Address{},
				fmt.Errorf("extractTxnAddresses: failed to find bridge calls in tx_hash:%s trace: %w", txHash.Hex(), err)
		}
	}

	fromAddr, err = resolveFromAddr(ctx, client, bridgeAddr, txHash, foundCalls, logEvent, logger)
	if err != nil {
		return common.Address{}, nil, common.Address{}, err
	}
	return txnSender, fromAddr, toAddr, nil
}

// resolveFromAddr picks the bridgeAsset call, among the non-reverted calls to the bridge found in the trace,
// that emitted logEvent, and returns its caller. It returns nil when the caller cannot be determined, and an
// error (wrapping ErrFromAddressRPC) only when an RPC call fails.
func resolveFromAddr(ctx context.Context,
	client aggkittypes.EthClienter,
	bridgeAddr common.Address,
	txHash common.Hash,
	foundCalls []*Call,
	logEvent *agglayerbridge.AgglayerbridgeBridgeEvent,
	logger *logger.Logger) (*common.Address, error) {
	if len(foundCalls) == 0 {
		logger.Warnf("no bridgeAsset call to the bridge found in tx %s, storing a NULL from address", txHash.Hex())
		return nil, nil
	}
	if from, ok := haveCommonFromForCalls(foundCalls); ok {
		return nonZeroAddress(from), nil
	}

	// Several callers: pair the event with its call by position. Only bridgeAsset emits an asset BridgeEvent,
	// once per successful call, and it is non-reentrant, so the N-th asset BridgeEvent of the tx was emitted by
	// the N-th non-reverted bridgeAsset call in execution order. This needs the event's log index, which is only
	// known when the event comes from a log of this tx.
	// This relies on bridgeAsset being the only function of the bridge (agglayerbridge and agglayerbridgel2) that
	// emits an asset-leaf BridgeEvent: revisit it if a contract upgrade adds another one. A count mismatch between
	// the receipt and the trace falls back to matching the call parameters, and then to a NULL from address.
	if logEvent.LeafType == bridgeLeafTypeAsset && logEvent.Raw.TxHash == txHash {
		position, total, err := assetBridgeEventPosition(ctx, client, bridgeAddr, txHash, logEvent.Raw.Index)
		if err != nil {
			return nil, fmt.Errorf("extractTxnAddresses: %w: failed to get receipt of tx_hash:%s: %w",
				ErrFromAddressRPC, txHash.Hex(), err)
		}
		if position >= 0 && total == len(foundCalls) {
			return nonZeroAddress(foundCalls[position].From), nil
		}
		logger.Warnf("tx %s has %d asset bridge events (this one at position %d) but %d bridgeAsset calls, "+
			"falling back to matching the call parameters", txHash.Hex(), total, position, len(foundCalls))
	}

	from, err := ExtractFromAddrFromCalls(foundCalls, logEvent)
	if err != nil {
		logger.Warnf("cannot determine the from address of the bridge event in tx %s, storing NULL: %v",
			txHash.Hex(), err)
		return nil, nil
	}
	return nonZeroAddress(from), nil
}

// assetBridgeEventPosition returns the position of the log at logIndex among the asset-leaf BridgeEvent logs that
// bridgeAddr emitted in the tx (-1 if it is not one of them), and how many there are.
func assetBridgeEventPosition(ctx context.Context,
	client aggkittypes.EthClienter,
	bridgeAddr common.Address,
	txHash common.Hash,
	logIndex uint) (position, total int, err error) {
	receipt, err := client.TransactionReceipt(ctx, txHash)
	if err != nil {
		return 0, 0, err
	}
	if receipt == nil {
		return 0, 0, fmt.Errorf("empty receipt")
	}
	position = -1
	for _, l := range receipt.Logs {
		if l == nil || l.Address != bridgeAddr || len(l.Topics) == 0 || l.Topics[0] != bridgeEventSignature {
			continue
		}
		// leafType is the first, non-indexed, field: the last byte of the first data word
		if len(l.Data) < common.HashLength || l.Data[common.HashLength-1] != bridgeLeafTypeAsset {
			continue
		}
		if l.Index == logIndex {
			position = total
		}
		total++
	}
	return position, total, nil
}

// nonZeroAddress returns nil for the zero address (stored as NULL) and a pointer to addr otherwise.
func nonZeroAddress(addr common.Address) *common.Address {
	if addr == (common.Address{}) {
		return nil
	}
	return &addr
}

// bridgeTxnResolver resolves the transaction that produced a bridge log. It decouples the
// transaction lookup from the tracing client: the legacy path resolves by hash through the RPC
// client, while the multidownloader path resolves through the shared TransactionProvider using the
// exact original log (including its block hash, number and transaction index) so the provider can
// validate canonical inclusion.
type bridgeTxnResolver interface {
	resolveTxn(ctx context.Context, l types.Log) (*aggkittypes.RPCTransaction, error)
}

// providerTxnResolver resolves transactions through the shared multidownloader transaction provider.
// It forwards the exact original log so the provider can match it against its downloaded data and
// return aggkittypes.ErrTransactionInclusionMismatch when the canonical inclusion is stale/reorged.
type providerTxnResolver struct {
	provider aggkittypes.TransactionProvider
}

var _ bridgeTxnResolver = (*providerTxnResolver)(nil)

func (r *providerTxnResolver) resolveTxn(ctx context.Context, l types.Log) (*aggkittypes.RPCTransaction, error) {
	return r.provider.TransactionByLog(ctx, l)
}

// bridgeLogsEnricher warms the multidownloader transaction cache for the bridge logs of a fetched
// range. It filters the bridgeEventSignature logs and asks the provider to resolve the transaction
// of each one using the active Download context, so the appender can later read them from the cache
// without an extra network round trip. Any error (in particular
// aggkittypes.ErrTransactionInclusionMismatch) is returned to the caller so the sync layer can
// refetch the whole affected range.
func bridgeLogsEnricher(provider aggkittypes.TransactionProvider) sync.LogsEnricherFunc {
	return func(ctx context.Context, logs []types.Log) error {
		for i := range logs {
			l := logs[i]
			if len(l.Topics) == 0 || l.Topics[0] != bridgeEventSignature {
				continue
			}
			if _, err := provider.TransactionByLog(ctx, l); err != nil {
				return fmt.Errorf("bridgesync: transaction prefetch failed for log block %d tx %s: %w",
					l.BlockNumber, l.TxHash, err)
			}
		}
		return nil
	}
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

// logFailureCounter counts the consecutive failures of the same log. The downloader calls the handler
// sequentially and retries a failing log until it succeeds before moving to the next one, so tracking the last
// log is enough.
type logFailureCounter struct {
	txHash   common.Hash
	logIndex uint
	failures int
}

// add records a failure of l and returns the number of consecutive failures of l.
func (c *logFailureCounter) add(l types.Log) int {
	if c.failures == 0 || c.txHash != l.TxHash || c.logIndex != l.Index {
		c.txHash, c.logIndex, c.failures = l.TxHash, l.Index, 0
	}
	c.failures++
	return c.failures
}

func (c *logFailureCounter) reset() {
	c.failures = 0
}

// buildBridgeEventHandler creates the legacy LogAppenderMap handler for the Bridge event log.
//
// It wraps the context-aware appender (buildBridgeEventAppender) with the constructor context, so
// the legacy API and its behaviour are preserved. Callers that run inside the shared multidownloader
// must use buildBridgeEventAppender (through buildContextualAppender) so the appender receives the
// active download context instead of this constructor-time context.
func buildBridgeEventHandler(
	ctx context.Context,
	contract *agglayerbridge.Agglayerbridge,
	bridgeAddr common.Address,
	client aggkittypes.EthClienter,
	syncFromInBridges bool,
	logger *logger.Logger,
	resolvers ...bridgeTxnResolver,
) func(*sync.EVMBlock, types.Log) error {
	appender := buildBridgeEventAppender(contract, bridgeAddr, client, syncFromInBridges, logger, resolvers...)
	return func(b *sync.EVMBlock, l types.Log) error {
		return appender(ctx, b, l)
	}
}

// buildBridgeEventAppender creates the context-aware handler for the Bridge event log. The context
// is taken from the call (the active download context) instead of being captured at construction
// time, so a cancelled download generation can unblock a resolver whose shared flight is still
// running. It is used by the shared multidownloader path.
//
// When no resolver is provided it preserves the legacy behaviour and resolves the transaction
// through eth_getTransactionByHash. When a resolver is provided (multidownloader path) the
// transaction is resolved through it using the exact original log.
func buildBridgeEventAppender(
	contract *agglayerbridge.Agglayerbridge,
	bridgeAddr common.Address,
	client aggkittypes.EthClienter,
	syncFromInBridges bool,
	logger *logger.Logger,
	resolvers ...bridgeTxnResolver,
) sync.ContextualLogAppender {
	rpcFailures := &logFailureCounter{}
	var resolver bridgeTxnResolver
	if len(resolvers) > 0 {
		resolver = resolvers[0]
	}
	return func(ctx context.Context, b *sync.EVMBlock, l types.Log) error {
		bridgeEvent, err := contract.ParseBridgeEvent(l)
		if err != nil {
			return fmt.Errorf("error parsing BridgeEvent log %+v: %w", l, err)
		}
		logger.Debugf("Parsed BridgeEvent: LeafType: %d, OriginNetwork:%d, OriginAddress: %s\n"+
			"DestinationNetwork: %d, DestinationAddress: %s, DepositCount: %d, Amount: %s, ",
			bridgeEvent.LeafType, bridgeEvent.OriginNetwork, bridgeEvent.OriginAddress.Hex(), bridgeEvent.DestinationNetwork,
			bridgeEvent.DestinationAddress.Hex(), bridgeEvent.DepositCount, bridgeEvent.Amount.String())

		var tx *aggkittypes.RPCTransaction
		if resolver != nil {
			var resolveErr error
			tx, resolveErr = resolver.resolveTxn(ctx, l)
			if resolveErr != nil {
				return fmt.Errorf("failed to resolve transaction for bridge event (tx hash: %s): %w",
					l.TxHash, resolveErr)
			}
		}
		extractAddresses := func(syncFrom bool) (common.Address, *common.Address, common.Address, error) {
			if resolver != nil {
				return extractTxnAddressesWithTx(ctx, client, bridgeAddr, l.TxHash, tx, bridgeEvent, logger, syncFrom)
			}
			return ExtractTxnAddresses(ctx, client, bridgeAddr, l.TxHash, bridgeEvent, logger, syncFrom)
		}

		txnSender, fromAddress, toAddress, err := extractAddresses(syncFromInBridges)
		if err != nil && errors.Is(err, ErrFromAddressRPC) && rpcFailures.add(l) >= maxFromAddressRPCAttempts {
			logger.Warnf("giving up on the from address of the bridge event in tx %s (log index %d) after %d "+
				"attempts, storing NULL: %v", l.TxHash.Hex(), l.Index, maxFromAddressRPCAttempts, err)
			txnSender, fromAddress, toAddress, err = extractAddresses(false)
		}
		if err != nil {
			return fmt.Errorf("failed to extract bridge event data (tx hash: %s): %w", l.TxHash, err)
		}
		rpcFailures.reset()

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

// findCall traverses the call trace depth first and returns, in execution order, the non-reverted calls to
// targetAddr accepted by the callback.
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

		// Add non-reverted calls to the stack, last first, so they are visited in execution order
		for i := len(currentCall.Calls) - 1; i >= 0; i-- {
			if c := currentCall.Calls[i]; c.Err == nil {
				callStack.Push(c)
			}
		}
	}
	if len(matchingCalls) > 0 {
		return matchingCalls, nil
	}
	return nil, db.ErrNotFound
}

// extractRootCall extracts the root call for a transaction using debug_traceTransaction. The call is
// bound to ctx so a cancelled download generation never stays blocked on a stalled trace.
func extractRootCall(ctx context.Context, client aggkittypes.RPCClienter, contractAddr common.Address,
	txHash common.Hash) (*Call, error) {
	rootCall := &Call{To: contractAddr}
	err := client.CallContext(ctx, rootCall, DebugTraceTxEndpoint, txHash, tracerCfg{Tracer: callTracerType})
	if err != nil {
		return nil, err
	}
	return rootCall, nil
}

func extractCallData(
	ctx context.Context,
	client aggkittypes.RPCClienter,
	bridgeAddr common.Address,
	txHash common.Hash,
	logger *logger.Logger,
	callback func(c Call) (bool, error),
) (foundCalls []*Call, rootCall *Call, err error) {
	// Extract root call first
	rootCall, err = extractRootCall(ctx, client, bridgeAddr, txHash)
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
