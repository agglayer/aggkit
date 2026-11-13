package claimsync

// trace_prefetch_test.go exercises the bounded parallel claim-trace prefetch
// (TraceResolver) with no production RPC at all: a hand-written fake implements the
// minimal tracePrefetchClient interface and returns *real* call trees built by the
// test, so the resolver, the appender handlers and the legacy synchronous path can
// be compared on exactly the same inputs.
//
// Every assertion runs in the test goroutine. The fake only records observations
// (counters, completion order) under a mutex; it never calls require/t.Fatal, since
// its RPC methods execute on resolver worker goroutines.

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/polygonzkevmbridge"
	"github.com/agglayer/aggkit/log"
	aggsync "github.com/agglayer/aggkit/sync"
	treetypes "github.com/agglayer/aggkit/tree/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	mocks "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// traceFakeClient is a deterministic, in-memory tracePrefetchClient. It never
// touches a network: CallContext returns the call tree registered for the tx hash.
type traceFakeClient struct {
	mu sync.Mutex

	traces    map[common.Hash]*Call
	traceErrs map[common.Hash]error
	delays    map[common.Hash]time.Duration
	// emptyOK, when set for a tx hash, simulates a nil-error response with no
	// call frame (a JSON null): CallContext leaves the pre-seeded result
	// untouched instead of decoding a call tree.
	emptyOK map[common.Hash]bool

	headers    map[uint64]*aggkittypes.BlockHeader
	headerErrs map[uint64]error

	// started, when non-nil, receives each tx hash as its CallContext begins; it
	// lets a test observe concurrency/overlap without racing on the counters.
	started chan common.Hash
	// release, when non-nil, gates every CallContext until it is closed (or the
	// request context is canceled).
	release chan struct{}

	callCount   int
	inFlight    int
	maxInFlight int
	completed   []common.Hash

	headerCalls int
	headerReqs  []uint64
}

func newTraceFakeClient() *traceFakeClient {
	return &traceFakeClient{
		traces:     make(map[common.Hash]*Call),
		traceErrs:  make(map[common.Hash]error),
		delays:     make(map[common.Hash]time.Duration),
		emptyOK:    make(map[common.Hash]bool),
		headers:    make(map[uint64]*aggkittypes.BlockHeader),
		headerErrs: make(map[uint64]error),
	}
}

func (f *traceFakeClient) Call(result any, method string, args ...any) error {
	if method != DebugTraceTxEndpoint {
		return fmt.Errorf("traceFakeClient.Call: unexpected method %q", method)
	}
	txHash, ok := args[0].(common.Hash)
	if !ok {
		return fmt.Errorf("traceFakeClient.Call: unexpected tx arg type %T", args[0])
	}
	f.mu.Lock()
	root := f.traces[txHash]
	err := f.traceErrs[txHash]
	f.mu.Unlock()
	if err != nil {
		return err
	}
	if root == nil {
		return fmt.Errorf("traceFakeClient.Call: no trace registered for %s", txHash.Hex())
	}
	arg, ok := result.(*Call)
	if !ok {
		return fmt.Errorf("traceFakeClient.Call: unexpected result type %T", result)
	}
	*arg = *root
	return nil
}

// BatchCallContext is part of aggkittypes.RPCClienter; the resolver never batches.
func (f *traceFakeClient) BatchCallContext(context.Context, []rpc.BatchElem) error {
	return errors.New("traceFakeClient.BatchCallContext: unexpected call")
}

func (f *traceFakeClient) CallContext(ctx context.Context, result interface{}, method string, args ...interface{}) error {
	if method != DebugTraceTxEndpoint {
		return fmt.Errorf("traceFakeClient.CallContext: unexpected method %q", method)
	}
	txHash, ok := args[0].(common.Hash)
	if !ok {
		return fmt.Errorf("traceFakeClient.CallContext: unexpected tx arg type %T", args[0])
	}

	f.mu.Lock()
	f.callCount++
	f.inFlight++
	if f.inFlight > f.maxInFlight {
		f.maxInFlight = f.inFlight
	}
	delay := f.delays[txHash]
	f.mu.Unlock()

	defer func() {
		f.mu.Lock()
		f.inFlight--
		f.mu.Unlock()
	}()

	if f.started != nil {
		select {
		case f.started <- txHash:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if f.release != nil {
		select {
		case <-f.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	f.mu.Lock()
	root := f.traces[txHash]
	err := f.traceErrs[txHash]
	empty := f.emptyOK[txHash]
	f.mu.Unlock()
	if err != nil {
		return err
	}
	if empty {
		// A nil-error response with no call frame: leave the pre-seeded result
		// untouched, exactly as a JSON null decodes into it.
		f.mu.Lock()
		f.completed = append(f.completed, txHash)
		f.mu.Unlock()
		return nil
	}
	if root == nil {
		return fmt.Errorf("traceFakeClient.CallContext: no trace registered for %s", txHash.Hex())
	}
	arg, ok := result.(*Call)
	if !ok {
		return fmt.Errorf("traceFakeClient.CallContext: unexpected result type %T", result)
	}
	*arg = *root

	f.mu.Lock()
	f.completed = append(f.completed, txHash)
	f.mu.Unlock()
	return nil
}

func (f *traceFakeClient) CustomHeaderByNumber(
	_ context.Context, number *aggkittypes.BlockNumberFinality,
) (*aggkittypes.BlockHeader, error) {
	f.mu.Lock()
	f.headerCalls++
	num := number.Specific
	h := f.headers[num]
	err := f.headerErrs[num]
	f.headerReqs = append(f.headerReqs, num)
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if h == nil {
		return nil, ethereum.NotFound
	}
	return h, nil
}

// traceFakeSnapshot is an immutable copy of the fake's observations, read under
// the lock so the test goroutine never races with worker goroutines.
type traceFakeSnapshot struct {
	callCount   int
	maxInFlight int
	headerCalls int
	inFlight    int
	completed   []common.Hash
}

func (f *traceFakeClient) snapshot() traceFakeSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]common.Hash, len(f.completed))
	copy(out, f.completed)
	return traceFakeSnapshot{
		callCount:   f.callCount,
		maxInFlight: f.maxInFlight,
		headerCalls: f.headerCalls,
		inFlight:    f.inFlight,
		completed:   out,
	}
}

// --- fixture helpers ---------------------------------------------------------

var (
	traceBridgeAddr = common.HexToAddress("0x000000000000000000000000000000000000bEEF")
	traceCallerAddr = common.HexToAddress("0x000000000000000000000000000000000000ca11")
	traceFromAddr   = common.HexToAddress("0x0000000000000000000000000000000000000f00")
	traceProofLocal = func() [treetypes.DefaultHeight][common.HashLength]byte {
		var p [treetypes.DefaultHeight][common.HashLength]byte
		p[5] = common.HexToHash("0xbeef")
		return p
	}()
	traceProofRollup = func() [treetypes.DefaultHeight][common.HashLength]byte {
		var p [treetypes.DefaultHeight][common.HashLength]byte
		p[4] = common.HexToHash("0xa1fa")
		return p
	}()
	traceMetadata = []byte{0xde, 0xad, 0xbe, 0xef}
)

type etrogClaim struct {
	globalIndex *big.Int
	mainnet     common.Hash
	rollup      common.Hash
	originNet   uint32
	originAddr  common.Address
	destNet     uint32
	destAddr    common.Address
	amount      *big.Int
	metadata    []byte
}

func defaultEtrogClaim(globalIndex *big.Int) etrogClaim {
	return etrogClaim{
		globalIndex: globalIndex,
		mainnet:     common.HexToHash("0x5ca1e"),
		rollup:      common.HexToHash("0xdead"),
		originNet:   69,
		originAddr:  common.HexToAddress("0xffaaffaa"),
		destNet:     7,
		destAddr:    common.HexToAddress("0x123456789"),
		amount:      big.NewInt(3),
		metadata:    traceMetadata,
	}
}

// etrogClaimInput encodes a claimAsset/claimMessage calldata for the Etrog bridge ABI.
func etrogClaimInput(t *testing.T, method string, c etrogClaim) []byte {
	t.Helper()
	bridgeABI, err := agglayerbridge.AgglayerbridgeMetaData.GetAbi()
	require.NoError(t, err)
	packed, err := bridgeABI.Methods[method].Inputs.Pack(
		traceProofLocal, traceProofRollup, c.globalIndex, c.mainnet, c.rollup,
		c.originNet, c.originAddr, c.destNet, c.destAddr, c.amount, c.metadata,
	)
	require.NoError(t, err)
	methodID := claimAssetEtrogMethodID
	if method == "claimMessage" {
		methodID = claimMessageEtrogMethodID
	}
	return append(append([]byte{}, methodID...), packed...)
}

// etrogClaimEventLog packs a ClaimEvent log carrying exactly the event fields the
// handler reads for the given claim.
func etrogClaimEventLog(t *testing.T, c etrogClaim, txHash common.Hash, blockNum uint64, blockHash common.Hash) types.Log {
	t.Helper()
	bridgeABI, err := agglayerbridge.AgglayerbridgeMetaData.GetAbi()
	require.NoError(t, err)
	event, err := bridgeABI.EventByID(claimEventSignature)
	require.NoError(t, err)
	data, err := event.Inputs.Pack(c.globalIndex, c.originNet, c.originAddr, c.destAddr, c.amount)
	require.NoError(t, err)
	return types.Log{
		Address:     traceBridgeAddr,
		Topics:      []common.Hash{claimEventSignature},
		Data:        data,
		TxHash:      txHash,
		BlockNumber: blockNum,
		BlockHash:   blockHash,
	}
}

type preEtrogClaim struct {
	index      uint32
	mainnet    common.Hash
	rollup     common.Hash
	originNet  uint32
	originAddr common.Address
	destNet    uint32
	destAddr   common.Address
	amount     *big.Int
	metadata   []byte
}

func defaultPreEtrogClaim(index uint32) preEtrogClaim {
	return preEtrogClaim{
		index:      index,
		mainnet:    common.HexToHash("0x5ca1e"),
		rollup:     common.HexToHash("0xdead"),
		originNet:  69,
		originAddr: common.HexToAddress("0xeebbeebb"),
		destNet:    7,
		destAddr:   common.HexToAddress("0x2233445566"),
		amount:     big.NewInt(11),
		metadata:   traceMetadata,
	}
}

func preEtrogClaimInput(t *testing.T, method string, c preEtrogClaim) []byte {
	t.Helper()
	bridgeABI, err := polygonzkevmbridge.PolygonzkevmbridgeMetaData.GetAbi()
	require.NoError(t, err)
	packed, err := bridgeABI.Methods[method].Inputs.Pack(
		traceProofLocal, c.index, c.mainnet, c.rollup,
		c.originNet, c.originAddr, c.destNet, c.destAddr, c.amount, c.metadata,
	)
	require.NoError(t, err)
	methodID := claimAssetPreEtrogMethodID
	if method == "claimMessage" {
		methodID = claimMessagePreEtrogMethodID
	}
	return append(append([]byte{}, methodID...), packed...)
}

func preEtrogClaimEventLog(t *testing.T, c preEtrogClaim, txHash common.Hash, blockNum uint64, blockHash common.Hash) types.Log {
	t.Helper()
	bridgeABI, err := polygonzkevmbridge.PolygonzkevmbridgeMetaData.GetAbi()
	require.NoError(t, err)
	event, err := bridgeABI.EventByID(claimEventSignaturePreEtrog)
	require.NoError(t, err)
	data, err := event.Inputs.Pack(c.index, c.originNet, c.originAddr, c.destAddr, c.amount)
	require.NoError(t, err)
	return types.Log{
		Address:     traceBridgeAddr,
		Topics:      []common.Hash{claimEventSignaturePreEtrog},
		Data:        data,
		TxHash:      txHash,
		BlockNumber: blockNum,
		BlockHash:   blockHash,
	}
}

// nestedRoot wraps the given bridge call in a caller root with extra (reverted and
// non-CALL) frames, exercising the nested-call traversal.
func nestedRoot(bridgeFrame Call) *Call {
	return &Call{
		Type: CallTypeCall,
		To:   traceCallerAddr,
		From: traceFromAddr,
		Calls: []Call{
			{Type: CallTypeCall, To: traceBridgeAddr, From: traceFromAddr, Err: strPtr("execution reverted")},
			{Type: CallTypeDelegateCall, To: traceBridgeAddr, From: traceFromAddr},
			bridgeFrame,
		},
	}
}

func newTraceDeployment(t *testing.T, client aggkittypes.EthClienter) *bridgeDeployment {
	t.Helper()
	b, err := agglayerbridge.NewAgglayerbridge(traceBridgeAddr, client)
	require.NoError(t, err)
	l2, err := agglayerbridgel2.NewAgglayerbridgel2(traceBridgeAddr, client)
	require.NoError(t, err)
	return &bridgeDeployment{kind: NonSovereignChain, agglayerBridge: b, agglayerBridgeL2: l2}
}

// expectLegacyTraces makes the gomock client answer the legacy synchronous
// debug_traceTransaction call with the supplied call tree.
func expectLegacyTraces(t *testing.T, c *mocks.EthClienter, traces map[common.Hash]*Call) {
	t.Helper()
	c.EXPECT().Call(mock.Anything, DebugTraceTxEndpoint, mock.Anything, mock.Anything).
		Run(func(result any, method string, args ...any) {
			arg, ok := result.(*Call)
			require.True(t, ok)
			txHash, ok := args[0].(common.Hash)
			require.True(t, ok)
			root, ok := traces[txHash]
			require.True(t, ok, "missing legacy trace for %s", txHash.Hex())
			*arg = *root
		}).Return(nil).Maybe()
}

func claimFromBlock(t *testing.T, b *aggsync.EVMBlock, idx int) *Claim {
	t.Helper()
	require.Less(t, idx, len(b.Events))
	ev, ok := b.Events[idx].(Event)
	require.True(t, ok)
	require.NotNil(t, ev.Claim)
	return ev.Claim
}

// --- tests -------------------------------------------------------------------

// TestTracePrefetch_MatchesLegacy_FullClaim covers the ordinary and pre-Etrog
// handlers: the prefetched snapshot must resolve the exact same full Claim
// (including proof, metadata and exit roots) as the legacy synchronous path.
func TestTracePrefetch_MatchesLegacy_FullClaim(t *testing.T) {
	lg := log.WithFields("module", "test-prefetch")

	t.Run("ordinary claimAsset", func(t *testing.T) {
		globalIndex := big.NewInt(421)
		claim := defaultEtrogClaim(globalIndex)
		txHash := common.HexToHash("0xaa01")
		blockNum := uint64(1000)
		blockHash := common.HexToHash("0xb10001")
		logEntry := etrogClaimEventLog(t, claim, txHash, blockNum, blockHash)

		root := nestedRoot(Call{
			Type:  CallTypeCall,
			To:    traceBridgeAddr,
			From:  traceFromAddr,
			Input: etrogClaimInput(t, "claimAsset", claim),
		})

		legacyClient := mocks.NewEthClienter(t)
		expectLegacyTraces(t, legacyClient, map[common.Hash]*Call{txHash: root})
		legacyAppender, err := buildAppender(legacyClient, traceBridgeAddr, newTraceDeployment(t, legacyClient), lg)
		require.NoError(t, err)

		fake := newTraceFakeClient()
		fake.traces[txHash] = root
		fake.headers[blockNum] = aggkittypes.NewBlockHeader(blockNum, blockHash, 0, nil)
		// The prefetch client must never see a raw legacy Call.
		prefetchClient := mocks.NewEthClienter(t)
		resolver := NewTraceResolver(fake, traceBridgeAddr, lg, ClaimTracePrefetchConcurrency)
		prefetchAppender, err := buildAppenderWithResolver(
			prefetchClient, traceBridgeAddr, newTraceDeployment(t, prefetchClient), lg, resolver)
		require.NoError(t, err)

		require.NoError(t, resolver.EnrichLogs(t.Context(), []types.Log{logEntry}))

		legacyBlock := &aggsync.EVMBlock{EVMBlockHeader: aggsync.EVMBlockHeader{Num: blockNum, Timestamp: 42}}
		prefetchBlock := &aggsync.EVMBlock{EVMBlockHeader: aggsync.EVMBlockHeader{Num: blockNum, Timestamp: 42}}
		require.NoError(t, legacyAppender[claimEventSignature](legacyBlock, logEntry))
		require.NoError(t, prefetchAppender[claimEventSignature](prefetchBlock, logEntry))

		require.Equal(t, legacyBlock.Events, prefetchBlock.Events, "prefetch must equal legacy byte-for-byte")

		got := claimFromBlock(t, prefetchBlock, 0)
		require.Equal(t, treetypes.NewProof(traceProofLocal), got.ProofLocalExitRoot)
		require.Equal(t, treetypes.NewProof(traceProofRollup), got.ProofRollupExitRoot)
		require.Equal(t, claim.mainnet, got.MainnetExitRoot)
		require.Equal(t, claim.rollup, got.RollupExitRoot)
		require.Equal(t, crypto.Keccak256Hash(claim.mainnet.Bytes(), claim.rollup.Bytes()), got.GlobalExitRoot)
		require.Equal(t, claim.metadata, got.Metadata)
		require.Equal(t, claim.destNet, got.DestinationNetwork)
		require.Equal(t, claim.originNet, got.OriginNetwork)
		require.Equal(t, claim.destAddr, got.DestinationAddress)
		require.False(t, got.IsMessage)
		require.Equal(t, ClaimEvent, got.Type)
	})

	t.Run("ordinary claimMessage", func(t *testing.T) {
		globalIndex := big.NewInt(422)
		claim := defaultEtrogClaim(globalIndex)
		txHash := common.HexToHash("0xaa02")
		blockNum := uint64(1001)
		blockHash := common.HexToHash("0xb10002")
		logEntry := etrogClaimEventLog(t, claim, txHash, blockNum, blockHash)
		root := nestedRoot(Call{
			Type:  CallTypeCall,
			To:    traceBridgeAddr,
			From:  traceFromAddr,
			Input: etrogClaimInput(t, "claimMessage", claim),
		})

		legacyClient := mocks.NewEthClienter(t)
		expectLegacyTraces(t, legacyClient, map[common.Hash]*Call{txHash: root})
		legacyAppender, err := buildAppender(legacyClient, traceBridgeAddr, newTraceDeployment(t, legacyClient), lg)
		require.NoError(t, err)

		fake := newTraceFakeClient()
		fake.traces[txHash] = root
		fake.headers[blockNum] = aggkittypes.NewBlockHeader(blockNum, blockHash, 0, nil)
		resolver := NewTraceResolver(fake, traceBridgeAddr, lg, ClaimTracePrefetchConcurrency)
		prefetchAppender, err := buildAppenderWithResolver(
			mocks.NewEthClienter(t), traceBridgeAddr, newTraceDeployment(t, mocks.NewEthClienter(t)), lg, resolver)
		require.NoError(t, err)

		require.NoError(t, resolver.EnrichLogs(t.Context(), []types.Log{logEntry}))

		legacyBlock := &aggsync.EVMBlock{EVMBlockHeader: aggsync.EVMBlockHeader{Num: blockNum}}
		prefetchBlock := &aggsync.EVMBlock{EVMBlockHeader: aggsync.EVMBlockHeader{Num: blockNum}}
		require.NoError(t, legacyAppender[claimEventSignature](legacyBlock, logEntry))
		require.NoError(t, prefetchAppender[claimEventSignature](prefetchBlock, logEntry))

		require.Equal(t, legacyBlock.Events, prefetchBlock.Events)
		require.True(t, claimFromBlock(t, prefetchBlock, 0).IsMessage)
	})

	t.Run("pre-Etrog claimAsset", func(t *testing.T) {
		claim := defaultPreEtrogClaim(77)
		txHash := common.HexToHash("0xbb01")
		blockNum := uint64(1002)
		blockHash := common.HexToHash("0xb10003")
		logEntry := preEtrogClaimEventLog(t, claim, txHash, blockNum, blockHash)
		root := nestedRoot(Call{
			Type:  CallTypeCall,
			To:    traceBridgeAddr,
			From:  traceFromAddr,
			Input: preEtrogClaimInput(t, "claimAsset", claim),
		})

		legacyClient := mocks.NewEthClienter(t)
		expectLegacyTraces(t, legacyClient, map[common.Hash]*Call{txHash: root})
		legacyAppender, err := buildAppender(legacyClient, traceBridgeAddr, newTraceDeployment(t, legacyClient), lg)
		require.NoError(t, err)

		fake := newTraceFakeClient()
		fake.traces[txHash] = root
		fake.headers[blockNum] = aggkittypes.NewBlockHeader(blockNum, blockHash, 0, nil)
		resolver := NewTraceResolver(fake, traceBridgeAddr, lg, ClaimTracePrefetchConcurrency)
		prefetchAppender, err := buildAppenderWithResolver(
			mocks.NewEthClienter(t), traceBridgeAddr, newTraceDeployment(t, mocks.NewEthClienter(t)), lg, resolver)
		require.NoError(t, err)

		require.NoError(t, resolver.EnrichLogs(t.Context(), []types.Log{logEntry}))

		legacyBlock := &aggsync.EVMBlock{EVMBlockHeader: aggsync.EVMBlockHeader{Num: blockNum}}
		prefetchBlock := &aggsync.EVMBlock{EVMBlockHeader: aggsync.EVMBlockHeader{Num: blockNum}}
		require.NoError(t, legacyAppender[claimEventSignaturePreEtrog](legacyBlock, logEntry))
		require.NoError(t, prefetchAppender[claimEventSignaturePreEtrog](prefetchBlock, logEntry))

		require.Equal(t, legacyBlock.Events, prefetchBlock.Events)
		got := claimFromBlock(t, prefetchBlock, 0)
		require.Equal(t, new(big.Int).SetUint64(uint64(claim.index)), got.GlobalIndex)
		require.Equal(t, treetypes.NewProof(traceProofLocal), got.ProofLocalExitRoot)
		require.Equal(t, claim.metadata, got.Metadata)
		require.Equal(t, claim.mainnet, got.MainnetExitRoot)
	})
}

// TestTracePrefetch_AmbiguityMatchesLegacy verifies that an ambiguous bridge call
// tree produces the exact same error through both paths.
func TestTracePrefetch_AmbiguityMatchesLegacy(t *testing.T) {
	lg := log.WithFields("module", "test-prefetch")
	globalIndex := big.NewInt(900)
	claim := defaultEtrogClaim(globalIndex)
	txHash := common.HexToHash("0xcc01")
	blockNum := uint64(2000)
	blockHash := common.HexToHash("0xb20001")
	logEntry := etrogClaimEventLog(t, claim, txHash, blockNum, blockHash)

	other := claim
	other.mainnet = common.HexToHash("0x1111") // same globalIndex, different decoded candidate
	root := &Call{
		Type: CallTypeCall,
		To:   traceCallerAddr,
		From: traceFromAddr,
		Calls: []Call{
			{Type: CallTypeCall, To: traceBridgeAddr, From: traceFromAddr, Input: etrogClaimInput(t, "claimAsset", claim)},
			{Type: CallTypeCall, To: traceBridgeAddr, From: traceFromAddr, Input: etrogClaimInput(t, "claimAsset", other)},
		},
	}

	legacyClient := mocks.NewEthClienter(t)
	expectLegacyTraces(t, legacyClient, map[common.Hash]*Call{txHash: root})
	legacyAppender, err := buildAppender(legacyClient, traceBridgeAddr, newTraceDeployment(t, legacyClient), lg)
	require.NoError(t, err)

	fake := newTraceFakeClient()
	fake.traces[txHash] = root
	fake.headers[blockNum] = aggkittypes.NewBlockHeader(blockNum, blockHash, 0, nil)
	resolver := NewTraceResolver(fake, traceBridgeAddr, lg, ClaimTracePrefetchConcurrency)
	prefetchAppender, err := buildAppenderWithResolver(
		mocks.NewEthClienter(t), traceBridgeAddr, newTraceDeployment(t, mocks.NewEthClienter(t)), lg, resolver)
	require.NoError(t, err)
	require.NoError(t, resolver.EnrichLogs(t.Context(), []types.Log{logEntry}))

	legacyErr := legacyAppender[claimEventSignature](
		&aggsync.EVMBlock{EVMBlockHeader: aggsync.EVMBlockHeader{Num: blockNum}}, logEntry)
	prefetchErr := prefetchAppender[claimEventSignature](
		&aggsync.EVMBlock{EVMBlockHeader: aggsync.EVMBlockHeader{Num: blockNum}}, logEntry)
	require.Error(t, legacyErr)
	require.Error(t, prefetchErr)
	require.EqualError(t, prefetchErr, legacyErr.Error())
	require.Contains(t, prefetchErr.Error(), "ambiguous claim calldata")
}

// TestTracePrefetch_BoundedConcurrency verifies the pool is bounded by 8 and that
// independent traces really overlap.
func TestTracePrefetch_BoundedConcurrency(t *testing.T) {
	lg := log.WithFields("module", "test-prefetch")
	fake := newTraceFakeClient()
	fake.release = make(chan struct{})
	fake.started = make(chan common.Hash, 64)

	const n = 20
	logs := make([]types.Log, 0, n)
	for i := 0; i < n; i++ {
		txHash := common.BigToHash(big.NewInt(int64(1000 + i)))
		blockNum := uint64(3000 + i%3) // 3 distinct blocks => 3 header calls
		blockHash := common.BigToHash(big.NewInt(int64(7000 + i%3)))
		claim := defaultEtrogClaim(big.NewInt(int64(1000 + i)))
		fake.traces[txHash] = nestedRoot(Call{
			Type: CallTypeCall, To: traceBridgeAddr, From: traceFromAddr,
			Input: etrogClaimInput(t, "claimAsset", claim),
		})
		logs = append(logs, etrogClaimEventLog(t, claim, txHash, blockNum, blockHash))
	}

	resolver := NewTraceResolver(fake, traceBridgeAddr, lg, ClaimTracePrefetchConcurrency)

	// Revalidate the (3) headers first, then observe exactly 8 workers blocked.
	for i := 0; i < 3; i++ {
		blockNum := uint64(3000 + i)
		fake.headers[blockNum] = aggkittypes.NewBlockHeader(
			blockNum, common.BigToHash(big.NewInt(int64(7000+i))), 0, nil)
	}

	done := make(chan error, 1)
	go func() { done <- resolver.EnrichLogs(t.Context(), logs) }()

	for i := 0; i < ClaimTracePrefetchConcurrency; i++ {
		select {
		case <-fake.started:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of %d workers started", i, ClaimTracePrefetchConcurrency)
		}
	}
	close(fake.release)
	require.NoError(t, <-done)

	snap := fake.snapshot()
	require.Equal(t, n, snap.callCount, "every distinct target must be traced exactly once")
	require.Equal(t, ClaimTracePrefetchConcurrency, snap.maxInFlight, "pool must reach the configured bound")
	require.LessOrEqual(t, snap.maxInFlight, ClaimTracePrefetchConcurrency, "pool must never exceed the bound")
	require.GreaterOrEqual(t, snap.maxInFlight, 2, "traces must overlap, not run strictly serially")
	require.Equal(t, 6, snap.headerCalls,
		"one canonical header revalidation per distinct block both before and after the trace RPCs, not per tx")
	require.Zero(t, snap.inFlight, "all workers must have joined")
	require.Len(t, snap.completed, n)

	for _, logEntry := range logs {
		root, err := resolver.ResolveRoot(logEntry)
		require.NoError(t, err)
		require.NotNil(t, root)
	}
}

// TestTracePrefetch_DetailedSuppressionAndEmptyWindow checks that DetailedClaimEvent
// logs are never traced, that the prefer-detailed hook drops the superseded ordinary
// log before the enricher, that pre-Etrog logs are still traced, and that an empty
// window clears the previous generation.
func TestTracePrefetch_DetailedSuppressionAndEmptyWindow(t *testing.T) {
	lg := log.WithFields("module", "test-prefetch")
	fake := newTraceFakeClient()
	resolver := NewTraceResolver(fake, traceBridgeAddr, lg, ClaimTracePrefetchConcurrency)

	txHash := common.HexToHash("0xdd01")
	blockNum := uint64(4000)
	blockHash := common.HexToHash("0xb40001")
	claim := defaultEtrogClaim(big.NewInt(500))
	ordinaryLog := etrogClaimEventLog(t, claim, txHash, blockNum, blockHash)
	detailedLog := buildDetailedClaimEventLog(t, big.NewInt(500), blockNum)
	detailedLog.TxHash = txHash
	detailedLog.BlockHash = blockHash

	hook := NewPreferDetailedClaimLogsHook(lg)
	filtered := hook(t.Context(), blockNum, blockNum, []types.Log{ordinaryLog, detailedLog})
	require.Len(t, filtered, 1)
	require.Equal(t, detailedClaimEventSignature, filtered[0].Topics[0])

	// With only the Detailed log left, the enricher must not request any trace.
	require.NoError(t, resolver.EnrichLogs(t.Context(), filtered))
	snap := fake.snapshot()
	require.Zero(t, snap.callCount, "DetailedClaimEvent must never be traced")
	require.Zero(t, snap.headerCalls, "no header revalidation is needed without ordinary logs")

	// The dropped ordinary log has no published trace: a miss, never a nil success.
	root, err := resolver.ResolveRoot(ordinaryLog)
	require.Nil(t, root)
	require.ErrorIs(t, err, ErrTraceUnavailable)
	require.ErrorIs(t, err, aggkittypes.ErrTransactionInclusionMismatch)

	// A pre-Etrog log is still traced (it is a different event, not superseded).
	preClaim := defaultPreEtrogClaim(501)
	preTx := common.HexToHash("0xdd02")
	preLog := preEtrogClaimEventLog(t, preClaim, preTx, blockNum, blockHash)
	fake.traces[preTx] = nestedRoot(Call{
		Type: CallTypeCall, To: traceBridgeAddr, From: traceFromAddr,
		Input: preEtrogClaimInput(t, "claimAsset", preClaim),
	})
	fake.headers[blockNum] = aggkittypes.NewBlockHeader(blockNum, blockHash, 0, nil)
	require.NoError(t, resolver.EnrichLogs(t.Context(), []types.Log{preLog}))
	root, err = resolver.ResolveRoot(preLog)
	require.NoError(t, err)
	require.NotNil(t, root)

	// An empty window atomically replaces the snapshot: the old entry disappears.
	require.NoError(t, resolver.EnrichLogs(t.Context(), nil))
	root, err = resolver.ResolveRoot(preLog)
	require.Nil(t, root)
	require.ErrorIs(t, err, ErrTraceUnavailable)
}

// TestTracePrefetch_ErrorSemantics covers ordinary (retryable) errors, exclusion of
// the reorg sentinel for transport failures, and parent-context cancellation.
func TestTracePrefetch_ErrorSemantics(t *testing.T) {
	lg := log.WithFields("module", "test-prefetch")

	t.Run("ordinary error is retryable and never publishes", func(t *testing.T) {
		fake := newTraceFakeClient()
		resolver := NewTraceResolver(fake, traceBridgeAddr, lg, ClaimTracePrefetchConcurrency)
		txHash := common.HexToHash("0xee01")
		blockNum := uint64(5000)
		blockHash := common.HexToHash("0xb50001")
		claim := defaultEtrogClaim(big.NewInt(1))
		logEntry := etrogClaimEventLog(t, claim, txHash, blockNum, blockHash)
		fake.headers[blockNum] = aggkittypes.NewBlockHeader(blockNum, blockHash, 0, nil)
		fake.traceErrs[txHash] = errors.New("malformed trace response")

		err := resolver.EnrichLogs(t.Context(), []types.Log{logEntry})
		require.Error(t, err)
		require.ErrorContains(t, err, "malformed trace response")
		require.False(t, errors.Is(err, aggkittypes.ErrTransactionInclusionMismatch),
			"a transport failure must stay retryable, not be reported as a reorg")

		root, resolveErr := resolver.ResolveRoot(logEntry)
		require.Nil(t, root)
		require.ErrorIs(t, resolveErr, ErrTraceUnavailable)

		// A later retry that succeeds publishes the snapshot.
		fake.mu.Lock()
		delete(fake.traceErrs, txHash)
		fake.traces[txHash] = nestedRoot(Call{Type: CallTypeCall, To: traceBridgeAddr, From: traceFromAddr})
		fake.mu.Unlock()
		require.NoError(t, resolver.EnrichLogs(t.Context(), []types.Log{logEntry}))
		root, err = resolver.ResolveRoot(logEntry)
		require.NoError(t, err)
		require.NotNil(t, root)
	})

	t.Run("header transport error is retryable", func(t *testing.T) {
		fake := newTraceFakeClient()
		resolver := NewTraceResolver(fake, traceBridgeAddr, lg, ClaimTracePrefetchConcurrency)
		txHash := common.HexToHash("0xee02")
		blockNum := uint64(5001)
		claim := defaultEtrogClaim(big.NewInt(2))
		logEntry := etrogClaimEventLog(t, claim, txHash, blockNum, common.HexToHash("0xb50002"))
		fake.headerErrs[blockNum] = errors.New("header rpc timeout")

		err := resolver.EnrichLogs(t.Context(), []types.Log{logEntry})
		require.Error(t, err)
		require.False(t, errors.Is(err, aggkittypes.ErrTransactionInclusionMismatch))
	})

	t.Run("parent context cancellation joins every worker", func(t *testing.T) {
		fake := newTraceFakeClient()
		fake.release = make(chan struct{}) // never closed: forces workers to wait
		fake.started = make(chan common.Hash, 64)
		resolver := NewTraceResolver(fake, traceBridgeAddr, lg, ClaimTracePrefetchConcurrency)

		const n = 12
		logs := make([]types.Log, 0, n)
		blockNum := uint64(6000)
		blockHash := common.HexToHash("0xb60001")
		for i := 0; i < n; i++ {
			txHash := common.BigToHash(big.NewInt(int64(600 + i)))
			claim := defaultEtrogClaim(big.NewInt(int64(600 + i)))
			fake.traces[txHash] = nestedRoot(Call{Type: CallTypeCall, To: traceBridgeAddr, From: traceFromAddr})
			logs = append(logs, etrogClaimEventLog(t, claim, txHash, blockNum, blockHash))
		}
		fake.headers[blockNum] = aggkittypes.NewBlockHeader(blockNum, blockHash, 0, nil)

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- resolver.EnrichLogs(ctx, logs) }()

		for i := 0; i < ClaimTracePrefetchConcurrency; i++ {
			select {
			case <-fake.started:
			case <-time.After(5 * time.Second):
				t.Fatalf("only %d workers started before cancellation", i)
			}
		}
		cancel()

		err := <-done
		require.Error(t, err)
		require.ErrorIs(t, err, context.Canceled)
		snap := fake.snapshot()
		require.Zero(t, snap.inFlight, "cancellation must join every in-flight RPC")
		// Nothing may be published as a partial success.
		for _, logEntry := range logs {
			root, resolveErr := resolver.ResolveRoot(logEntry)
			require.Nil(t, root)
			require.ErrorIs(t, resolveErr, ErrTraceUnavailable)
		}
	})
}

// TestTracePrefetch_RevalidateCanonical checks the header revalidation semantics:
// a changed/absent header is a reorg mismatch, while an inconsistent hash for one
// block number in the same range is rejected before any trace request.
func TestTracePrefetch_RevalidateCanonical(t *testing.T) {
	lg := log.WithFields("module", "test-prefetch")

	newResolverLog := func(txHash common.Hash, blockNum uint64, blockHash common.Hash) types.Log {
		claim := defaultEtrogClaim(big.NewInt(1))
		return etrogClaimEventLog(t, claim, txHash, blockNum, blockHash)
	}

	t.Run("changed header hash is an inclusion mismatch", func(t *testing.T) {
		fake := newTraceFakeClient()
		resolver := NewTraceResolver(fake, traceBridgeAddr, lg, ClaimTracePrefetchConcurrency)
		blockNum := uint64(7000)
		logEntry := newResolverLog(common.HexToHash("0xf001"), blockNum, common.HexToHash("0xb70001"))
		fake.headers[blockNum] = aggkittypes.NewBlockHeader(blockNum, common.HexToHash("0xdifferent"), 0, nil)

		err := resolver.EnrichLogs(t.Context(), []types.Log{logEntry})
		require.ErrorIs(t, err, aggkittypes.ErrTransactionInclusionMismatch)
		require.Zero(t, fake.snapshot().callCount, "no trace may be requested once revalidation fails")
	})

	t.Run("missing header is an inclusion mismatch", func(t *testing.T) {
		fake := newTraceFakeClient()
		resolver := NewTraceResolver(fake, traceBridgeAddr, lg, ClaimTracePrefetchConcurrency)
		blockNum := uint64(7001)
		logEntry := newResolverLog(common.HexToHash("0xf002"), blockNum, common.HexToHash("0xb70002"))
		// No header registered => ethereum.NotFound.

		err := resolver.EnrichLogs(t.Context(), []types.Log{logEntry})
		require.ErrorIs(t, err, aggkittypes.ErrTransactionInclusionMismatch)
	})

	t.Run("inconsistent hashes for one block are rejected", func(t *testing.T) {
		fake := newTraceFakeClient()
		resolver := NewTraceResolver(fake, traceBridgeAddr, lg, ClaimTracePrefetchConcurrency)
		blockNum := uint64(7002)
		logA := newResolverLog(common.HexToHash("0xf003"), blockNum, common.HexToHash("0xb70003"))
		logB := newResolverLog(common.HexToHash("0xf004"), blockNum, common.HexToHash("0xb70004"))
		fake.headers[blockNum] = aggkittypes.NewBlockHeader(blockNum, common.HexToHash("0xb70003"), 0, nil)

		err := resolver.EnrichLogs(t.Context(), []types.Log{logA, logB})
		require.ErrorIs(t, err, aggkittypes.ErrTransactionInclusionMismatch)
	})

	t.Run("synthetic zero-hash log skips revalidation", func(t *testing.T) {
		fake := newTraceFakeClient()
		resolver := NewTraceResolver(fake, traceBridgeAddr, lg, ClaimTracePrefetchConcurrency)
		txHash := common.HexToHash("0xf005")
		claim := defaultEtrogClaim(big.NewInt(1))
		logEntry := etrogClaimEventLog(t, claim, txHash, 7003, common.Hash{})
		fake.traces[txHash] = nestedRoot(Call{Type: CallTypeCall, To: traceBridgeAddr, From: traceFromAddr})

		require.NoError(t, resolver.EnrichLogs(t.Context(), []types.Log{logEntry}))
		snap := fake.snapshot()
		require.Zero(t, snap.headerCalls)
		require.Equal(t, 1, snap.callCount, "the trace is still prefetched without a header prevalidation")
	})
}

// TestTracePrefetch_SameTxTwoBlockHashes covers the reorg case where the same tx
// hash is re-included in a different block: the old trace must never be reused.
func TestTracePrefetch_SameTxTwoBlockHashes(t *testing.T) {
	lg := log.WithFields("module", "test-prefetch")
	fake := newTraceFakeClient()
	resolver := NewTraceResolver(fake, traceBridgeAddr, lg, ClaimTracePrefetchConcurrency)

	txHash := common.HexToHash("0xabc123")
	blockNum := uint64(8000)
	b1 := common.HexToHash("0xb80001")
	b2 := common.HexToHash("0xb80002")
	claim := defaultEtrogClaim(big.NewInt(1))

	log1 := etrogClaimEventLog(t, claim, txHash, blockNum, b1)
	log2 := etrogClaimEventLog(t, claim, txHash, blockNum, b2)

	root1 := nestedRoot(Call{Type: CallTypeCall, To: traceBridgeAddr, From: traceFromAddr, Value: nil})
	root2 := nestedRoot(Call{Type: CallTypeCall, To: traceBridgeAddr, From: common.HexToAddress("0xfeed")})

	fake.traces[txHash] = root1
	fake.headers[blockNum] = aggkittypes.NewBlockHeader(blockNum, b1, 0, nil)
	require.NoError(t, resolver.EnrichLogs(t.Context(), []types.Log{log1}))
	got, err := resolver.ResolveRoot(log1)
	require.NoError(t, err)
	require.Equal(t, root1, got)

	// Reorg: same tx hash now lives in a different block.
	fake.mu.Lock()
	fake.traces[txHash] = root2
	fake.headers[blockNum] = aggkittypes.NewBlockHeader(blockNum, b2, 0, nil)
	fake.mu.Unlock()
	require.NoError(t, resolver.EnrichLogs(t.Context(), []types.Log{log2}))

	got, err = resolver.ResolveRoot(log2)
	require.NoError(t, err)
	require.Equal(t, root2, got)

	// The old generation's key must be gone: no stale reuse.
	got, err = resolver.ResolveRoot(log1)
	require.Nil(t, got)
	require.ErrorIs(t, err, ErrTraceUnavailable)
	require.ErrorIs(t, err, aggkittypes.ErrTransactionInclusionMismatch)
}

// TestTracePrefetch_PostValidationReorgDuringPrefetch covers the window the
// pre-validation alone cannot close: the block is canonical when the range is
// pre-validated, but it is re-included under a different hash while the trace RPC
// is in flight. The post-validation read must turn that into an inclusion
// mismatch, purge the previously published snapshot (no stale trace may be
// served) and let a fresh generation against the new canonical header succeed.
func TestTracePrefetch_PostValidationReorgDuringPrefetch(t *testing.T) {
	lg := log.WithFields("module", "test-prefetch")
	fake := newTraceFakeClient()
	resolver := NewTraceResolver(fake, traceBridgeAddr, lg, ClaimTracePrefetchConcurrency)

	txHash := common.HexToHash("0x9a01")
	blockNum := uint64(9200)
	oldHash := common.HexToHash("0xb92000")
	newHash := common.HexToHash("0xb92001")
	claim := defaultEtrogClaim(big.NewInt(1))
	logEntry := etrogClaimEventLog(t, claim, txHash, blockNum, oldHash)

	fake.traces[txHash] = nestedRoot(Call{
		Type: CallTypeCall, To: traceBridgeAddr, From: traceFromAddr,
		Input: etrogClaimInput(t, "claimAsset", claim),
	})
	fake.headers[blockNum] = aggkittypes.NewBlockHeader(blockNum, oldHash, 0, nil)

	// Generation 1 publishes a trace for the old canonical inclusion.
	require.NoError(t, resolver.EnrichLogs(t.Context(), []types.Log{logEntry}))
	published, err := resolver.ResolveRoot(logEntry)
	require.NoError(t, err)
	require.NotNil(t, published)

	// Generation 2 re-runs on the same log, with the trace RPC gated so the reorg
	// lands after the pre-validation but before the post-validation.
	fake.mu.Lock()
	fake.release = make(chan struct{})
	fake.started = make(chan common.Hash, 4)
	fake.mu.Unlock()

	done := make(chan error, 1)
	go func() { done <- resolver.EnrichLogs(t.Context(), []types.Log{logEntry}) }()

	select {
	case <-fake.started:
	case <-time.After(5 * time.Second):
		t.Fatal("trace RPC never started")
	}

	// Reorg while the trace is in flight: same block number, new canonical hash
	// and a different execution for the same tx hash.
	newRoot := nestedRoot(Call{
		Type: CallTypeCall, To: traceBridgeAddr, From: traceFromAddr,
		Input: etrogClaimInput(t, "claimMessage", claim),
	})
	fake.mu.Lock()
	fake.headers[blockNum] = aggkittypes.NewBlockHeader(blockNum, newHash, 0, nil)
	fake.traces[txHash] = newRoot
	fake.mu.Unlock()
	close(fake.release)

	require.ErrorIs(t, <-done, aggkittypes.ErrTransactionInclusionMismatch)

	// The old block key must not resolve to the trace published by generation 1.
	stale, resolveErr := resolver.ResolveRoot(logEntry)
	require.Nil(t, stale)
	require.ErrorIs(t, resolveErr, ErrTraceUnavailable)

	// A fresh generation against the new canonical header succeeds.
	newLog := etrogClaimEventLog(t, claim, txHash, blockNum, newHash)
	require.NoError(t, resolver.EnrichLogs(t.Context(), []types.Log{newLog}))
	got, err := resolver.ResolveRoot(newLog)
	require.NoError(t, err)
	require.NotNil(t, got)
}

// TestTracePrefetch_EmptyRootResponseRetried verifies that a nil-error response
// with no call frame is rejected before it can be cached: the enrichment fails
// with a retryable error (never an inclusion mismatch) and nothing is published,
// so the downloader re-runs the enrichment instead of retrying the appender
// against an empty root forever. A healthy response on the next attempt is
// published and decoded through the prefetched path.
func TestTracePrefetch_EmptyRootResponseRetried(t *testing.T) {
	lg := log.WithFields("module", "test-prefetch")
	fake := newTraceFakeClient()
	resolver := NewTraceResolver(fake, traceBridgeAddr, lg, ClaimTracePrefetchConcurrency)

	txHash := common.HexToHash("0x9b01")
	blockNum := uint64(9300)
	blockHash := common.HexToHash("0xb93000")
	claim := defaultEtrogClaim(big.NewInt(7))
	logEntry := etrogClaimEventLog(t, claim, txHash, blockNum, blockHash)
	fake.headers[blockNum] = aggkittypes.NewBlockHeader(blockNum, blockHash, 0, nil)
	fake.emptyOK[txHash] = true

	err := resolver.EnrichLogs(t.Context(), []types.Log{logEntry})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unusable root call")
	require.False(t, errors.Is(err, aggkittypes.ErrTransactionInclusionMismatch),
		"an unusable root must stay retryable, not be reported as a reorg")

	root, resolveErr := resolver.ResolveRoot(logEntry)
	require.Nil(t, root)
	require.ErrorIs(t, resolveErr, ErrTraceUnavailable)

	// The next attempt gets a healthy frame and publishes the decoded claim.
	healthy := nestedRoot(Call{
		Type: CallTypeCall, To: traceBridgeAddr, From: traceFromAddr,
		Input: etrogClaimInput(t, "claimAsset", claim),
	})
	fake.mu.Lock()
	delete(fake.emptyOK, txHash)
	fake.traces[txHash] = healthy
	fake.mu.Unlock()

	require.NoError(t, resolver.EnrichLogs(t.Context(), []types.Log{logEntry}))

	prefetchAppender, err := buildAppenderWithResolver(
		mocks.NewEthClienter(t), traceBridgeAddr, newTraceDeployment(t, mocks.NewEthClienter(t)), lg, resolver)
	require.NoError(t, err)
	block := &aggsync.EVMBlock{EVMBlockHeader: aggsync.EVMBlockHeader{Num: blockNum}}
	require.NoError(t, prefetchAppender[claimEventSignature](block, logEntry))
	require.Len(t, block.Events, 1)
	require.False(t, claimFromBlock(t, block, 0).IsMessage)
}

// TestTracePrefetch_ValidationAcceptsLegitimateRoots guards the validation scope:
// a reverted root is a legitimate callTracer frame and must be cached (the claim
// extraction then reports ErrNotFound exactly as the legacy path does), and a
// router fallback whose own input is empty must still be cached because its
// nested call carries the bridge claim.
func TestTracePrefetch_ValidationAcceptsLegitimateRoots(t *testing.T) {
	lg := log.WithFields("module", "test-prefetch")

	t.Run("reverted root is cached", func(t *testing.T) {
		fake := newTraceFakeClient()
		resolver := NewTraceResolver(fake, traceBridgeAddr, lg, ClaimTracePrefetchConcurrency)
		txHash := common.HexToHash("0x9c01")
		blockNum := uint64(9400)
		blockHash := common.HexToHash("0xb94000")
		claim := defaultEtrogClaim(big.NewInt(9))
		fake.traces[txHash] = &Call{
			Type: CallTypeCall, To: traceBridgeAddr, From: traceFromAddr,
			Err: strPtr("execution reverted"),
		}
		fake.headers[blockNum] = aggkittypes.NewBlockHeader(blockNum, blockHash, 0, nil)
		logEntry := etrogClaimEventLog(t, claim, txHash, blockNum, blockHash)

		require.NoError(t, resolver.EnrichLogs(t.Context(), []types.Log{logEntry}))
		root, err := resolver.ResolveRoot(logEntry)
		require.NoError(t, err)
		require.NotNil(t, root)
		require.NotNil(t, root.Err)
	})

	t.Run("empty-input router fallback with nested claim is cached", func(t *testing.T) {
		fake := newTraceFakeClient()
		resolver := NewTraceResolver(fake, traceBridgeAddr, lg, ClaimTracePrefetchConcurrency)
		txHash := common.HexToHash("0x9c02")
		blockNum := uint64(9401)
		blockHash := common.HexToHash("0xb94001")
		claim := defaultEtrogClaim(big.NewInt(10))
		fake.traces[txHash] = nestedRoot(Call{
			Type: CallTypeCall, To: traceBridgeAddr, From: traceFromAddr,
			Input: etrogClaimInput(t, "claimAsset", claim),
		})
		fake.headers[blockNum] = aggkittypes.NewBlockHeader(blockNum, blockHash, 0, nil)
		logEntry := etrogClaimEventLog(t, claim, txHash, blockNum, blockHash)

		require.NoError(t, resolver.EnrichLogs(t.Context(), []types.Log{logEntry}))
		root, err := resolver.ResolveRoot(logEntry)
		require.NoError(t, err)
		require.NotNil(t, root)
		require.Zero(t, len(root.Input))
	})
}

// TestTracePrefetch_EventOrderPreserved verifies that events are appended in the
// order of the input logs even when the prefetch completes in a different order.
func TestTracePrefetch_EventOrderPreserved(t *testing.T) {
	lg := log.WithFields("module", "test-prefetch")
	fake := newTraceFakeClient()
	resolver := NewTraceResolver(fake, traceBridgeAddr, lg, ClaimTracePrefetchConcurrency)

	blockNum := uint64(9000)
	blockHash := common.HexToHash("0xb90001")
	fake.headers[blockNum] = aggkittypes.NewBlockHeader(blockNum, blockHash, 0, nil)

	txHashes := []common.Hash{
		common.HexToHash("0xa001"),
		common.HexToHash("0xa002"),
		common.HexToHash("0xa003"),
	}
	// Reverse completion order: the first log's trace finishes last.
	fake.delays[txHashes[0]] = 60 * time.Millisecond
	fake.delays[txHashes[1]] = 30 * time.Millisecond
	fake.delays[txHashes[2]] = 0

	logs := make([]types.Log, 0, len(txHashes))
	for i, txHash := range txHashes {
		claim := defaultEtrogClaim(big.NewInt(int64(900 + i)))
		fake.traces[txHash] = nestedRoot(Call{
			Type: CallTypeCall, To: traceBridgeAddr, From: traceFromAddr,
			Input: etrogClaimInput(t, "claimAsset", claim),
		})
		logs = append(logs, etrogClaimEventLog(t, claim, txHash, blockNum, blockHash))
	}

	require.NoError(t, resolver.EnrichLogs(t.Context(), logs))
	snap := fake.snapshot()
	require.Equal(t, []common.Hash{txHashes[2], txHashes[1], txHashes[0]}, snap.completed,
		"prefetch must complete out of the input order for this assertion to be meaningful")

	prefetchAppender, err := buildAppenderWithResolver(
		mocks.NewEthClienter(t), traceBridgeAddr, newTraceDeployment(t, mocks.NewEthClienter(t)), lg, resolver)
	require.NoError(t, err)

	block := &aggsync.EVMBlock{EVMBlockHeader: aggsync.EVMBlockHeader{Num: blockNum}}
	for _, logEntry := range logs {
		require.NoError(t, prefetchAppender[claimEventSignature](block, logEntry))
	}
	require.Len(t, block.Events, len(logs))
	for i, logEntry := range logs {
		require.Equal(t, logEntry.TxHash, claimFromBlock(t, block, i).TxHash)
	}
}

// TestTraceResolver_ClampWorkers checks the constructor clamps the pool size.
func TestTraceResolver_ClampWorkers(t *testing.T) {
	fake := newTraceFakeClient()
	require.Equal(t, 1, NewTraceResolver(fake, traceBridgeAddr, nil, 0).workers)
	require.Equal(t, 1, NewTraceResolver(fake, traceBridgeAddr, nil, -5).workers)
	require.Equal(t, 3, NewTraceResolver(fake, traceBridgeAddr, nil, 3).workers)
}

// TestTraceUnavailableError_Is guards the error identity contract used by the
// downloader to decide between "retry" and "refetch the whole range".
func TestTraceUnavailableError_Is(t *testing.T) {
	err := &traceUnavailableError{key: traceKey{
		BlockHash: common.HexToHash("0x1"), TxHash: common.HexToHash("0x2"),
	}}
	require.ErrorIs(t, err, ErrTraceUnavailable)
	require.ErrorIs(t, err, aggkittypes.ErrTransactionInclusionMismatch)
	require.NotErrorIs(t, err, errors.New("something else"))
	require.Contains(t, err.Error(), common.HexToHash("0x1").Hex())
	require.Contains(t, err.Error(), common.HexToHash("0x2").Hex())
}

// TestCollectTraceTargets covers the target selection/deduplication rules.
func TestCollectTraceTargets(t *testing.T) {
	h := common.HexToHash("0xblock")
	logs := []types.Log{
		{}, // no topics
		{Topics: []common.Hash{detailedClaimEventSignature}, TxHash: common.HexToHash("0x1"), BlockHash: h},
		{Topics: []common.Hash{claimEventSignature}, TxHash: common.HexToHash("0x2"), BlockHash: h, BlockNumber: 5},
		{Topics: []common.Hash{claimEventSignature}, TxHash: common.HexToHash("0x2"), BlockHash: h, BlockNumber: 5}, // dup
		{Topics: []common.Hash{claimEventSignaturePreEtrog}, TxHash: common.HexToHash("0x3"), BlockHash: h, BlockNumber: 6},
		{Topics: []common.Hash{setClaimEventSignature}, TxHash: common.HexToHash("0x4"), BlockHash: h},
	}
	targets := collectTraceTargets(logs)
	require.Len(t, targets, 2)
	require.Equal(t, traceKey{BlockHash: h, TxHash: common.HexToHash("0x2")}, targets[0].key)
	require.Equal(t, traceKey{BlockHash: h, TxHash: common.HexToHash("0x3")}, targets[1].key)
	require.Equal(t, uint64(6), targets[1].blockNum)
}

// compile-time assertion that the fake satisfies the resolver dependency.
var _ tracePrefetchClient = (*traceFakeClient)(nil)
