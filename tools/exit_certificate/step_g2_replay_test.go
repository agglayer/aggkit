package exit_certificate

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// --- mock fork backend / launcher ----------------------------------------------------------------

// mockForkBackend is a programmable forkBackend. Each method delegates to its function field when
// set; otherwise a cooperative default is used: SendBridgeAssetTx assigns the next deposit count and
// returns a deterministic hash, and WaitForReceipt returns a BridgeEvent receipt for that hash. This
// lets the happy path run with zero configuration while individual tests override single methods to
// inject errors, reverts or timeouts.
type mockForkBackend struct {
	localExitRoot func(ctx context.Context, blockTag string) (common.Hash, error)
	tokenWrapped  func(ctx context.Context, net uint32, addr common.Address) (common.Address, error)
	tokenMeta     func(ctx context.Context, l2Token common.Address) ([]byte, error)
	gasTokenMeta  func(ctx context.Context) ([]byte, error)
	setBalance    func(ctx context.Context, sender common.Address) error
	prepareERC20  func(ctx context.Context, sender, token common.Address) error
	sendTx        func(ctx context.Context, e *agglayertypes.BridgeExit, isNative bool, token common.Address) (common.Hash, error)
	waitReceipt   func(ctx context.Context, hash common.Hash) ([]rpcLog, error)

	mu              sync.Mutex
	nextDeposit     uint32
	hashToDeposit   map[common.Hash]uint32
	setBalanceCalls []common.Address
	prepareCalls    []common.Address
}

func newMockBackend() *mockForkBackend {
	return &mockForkBackend{hashToDeposit: map[common.Hash]uint32{}}
}

func (m *mockForkBackend) LocalExitRoot(ctx context.Context, blockTag string) (common.Hash, error) {
	if m.localExitRoot != nil {
		return m.localExitRoot(ctx, blockTag)
	}
	return common.Hash{}, nil
}

func (m *mockForkBackend) TokenWrappedAddress(
	ctx context.Context, net uint32, addr common.Address,
) (common.Address, error) {
	if m.tokenWrapped != nil {
		return m.tokenWrapped(ctx, net, addr)
	}
	return common.Address{}, nil
}

func (m *mockForkBackend) TokenMetadata(ctx context.Context, l2Token common.Address) ([]byte, error) {
	if m.tokenMeta != nil {
		return m.tokenMeta(ctx, l2Token)
	}
	return nil, nil
}

func (m *mockForkBackend) GasTokenMetadata(ctx context.Context) ([]byte, error) {
	if m.gasTokenMeta != nil {
		return m.gasTokenMeta(ctx)
	}
	return nil, nil
}

func (m *mockForkBackend) SetSenderBalance(ctx context.Context, sender common.Address) error {
	m.mu.Lock()
	m.setBalanceCalls = append(m.setBalanceCalls, sender)
	m.mu.Unlock()
	if m.setBalance != nil {
		return m.setBalance(ctx, sender)
	}
	return nil
}

func (m *mockForkBackend) PrepareERC20Token(ctx context.Context, sender, token common.Address) error {
	m.mu.Lock()
	m.prepareCalls = append(m.prepareCalls, token)
	m.mu.Unlock()
	if m.prepareERC20 != nil {
		return m.prepareERC20(ctx, sender, token)
	}
	return nil
}

func (m *mockForkBackend) SendBridgeAssetTx(
	ctx context.Context, e *agglayertypes.BridgeExit, isNative bool, token common.Address,
) (common.Hash, error) {
	if m.sendTx != nil {
		return m.sendTx(ctx, e, isNative, token)
	}
	// default: assign the next deposit count and a deterministic hash for it
	m.mu.Lock()
	defer m.mu.Unlock()
	dc := m.nextDeposit
	m.nextDeposit++
	hash := common.BigToHash(big.NewInt(int64(dc) + 1))
	m.hashToDeposit[hash] = dc
	return hash, nil
}

func (m *mockForkBackend) WaitForReceipt(ctx context.Context, hash common.Hash) ([]rpcLog, error) {
	if m.waitReceipt != nil {
		return m.waitReceipt(ctx, hash)
	}
	m.mu.Lock()
	dc := m.hashToDeposit[hash]
	m.mu.Unlock()
	return bridgeEventReceipt(dc, uint64(dc)+1, uint64(dc)), nil
}

type mockForkLauncher struct {
	backend forkBackend
	err     error
	started bool
}

func (l *mockForkLauncher) Start(
	_ context.Context, _ string, _ uint64, _ common.Address,
) (forkBackend, func(), error) {
	if l.err != nil {
		return nil, nil, l.err
	}
	l.started = true
	return l.backend, func() {}, nil
}

// bridgeEventReceipt builds a receipt carrying a single BridgeEvent log with the given deposit count.
func bridgeEventReceipt(depositCount uint32, blockNum, logIndex uint64) []rpcLog {
	data, err := bridgeABI.Events["BridgeEvent"].Inputs.Pack(
		uint8(0), uint32(0), common.Address{}, uint32(0), common.Address{}, big.NewInt(0), []byte{}, depositCount,
	)
	if err != nil {
		panic(err)
	}
	return []rpcLog{{
		Topics:      []string{bridgeEventTopicHash.Hex()},
		Data:        "0x" + common.Bytes2Hex(data),
		BlockNumber: fmt.Sprintf("0x%x", blockNum),
		LogIndex:    fmt.Sprintf("0x%x", logIndex),
	}}
}

func replayTestConfig(t *testing.T) *Config {
	t.Helper()
	return &Config{Options: Options{
		OutputDir:                             t.TempDir(),
		ConcurrencyLimit:                      2,
		VerifyNewLocalExitRootUsingShadowFork: true, // these tests exercise the shadow-fork orchestration
	}}
}

// nativeAssetExit builds a native (gas-token) exit the way step_d does: a non-nil TokenInfo with a
// zero origin address. The replay path dereferences BridgeExit.TokenInfo, so production never carries
// a nil one — this mirrors that shape (unlike the order-test nativeExit helper, which uses nil).
func nativeAssetExit(dest common.Address, amount int64) *agglayertypes.BridgeExit {
	return &agglayertypes.BridgeExit{
		TokenInfo:          &agglayertypes.TokenInfo{},
		DestinationNetwork: 0,
		DestinationAddress: dest,
		Amount:             big.NewInt(amount),
	}
}

func TestVerifyReplayMetadata(t *testing.T) {
	t.Parallel()

	exits := []*agglayertypes.BridgeExit{
		{DestinationAddress: common.HexToAddress("0x1"), Metadata: nil},
		{DestinationAddress: common.HexToAddress("0x2"), Metadata: []byte{0xab, 0xcd}},
	}

	// nil and an empty slice compare equal; matching metadata passes.
	require.NoError(t, verifyReplayMetadata(exits, [][]byte{{}, {0xab, 0xcd}}))

	// a per-exit divergence names the offending exit and both metadata blobs.
	err := verifyReplayMetadata(exits, [][]byte{{}, {0xab, 0xff}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "bridge exit 1")
	require.Contains(t, err.Error(), "abcd")
	require.Contains(t, err.Error(), "abff")
}

// --- resolveTokenAddresses -----------------------------------------------------------------------

func TestResolveTokenAddresses(t *testing.T) {
	t.Parallel()
	const l2NetworkID = uint32(10)
	gasNet, gasAddr := uint32(0), common.Address{}

	l2NativeTok := common.BytesToAddress([]byte("l2native"))
	lbtOrigin := common.BytesToAddress([]byte("lbtOrigin"))
	lbtWrapped := common.BytesToAddress([]byte("lbtWrapped"))
	contractOrigin := common.BytesToAddress([]byte("contractOrigin"))
	contractWrapped := common.BytesToAddress([]byte("contractWrapped"))

	exits := []*agglayertypes.BridgeExit{
		nativeAssetExit(common.BytesToAddress([]byte("dest0")), 1),          // native → skipped
		erc20Exit(l2NetworkID, l2NativeTok, common.HexToAddress("0xd1"), 2), // L2-native → maps to self
		erc20Exit(1, lbtOrigin, common.HexToAddress("0xd2"), 3),             // resolved from LBT
		erc20Exit(1, contractOrigin, common.HexToAddress("0xd3"), 4),        // resolved from contract
	}
	lbtMap := map[tokenOriginKey]common.Address{{network: 1, addr: lbtOrigin}: lbtWrapped}

	backend := newMockBackend()
	var wrappedCalls int
	backend.tokenWrapped = func(_ context.Context, net uint32, addr common.Address) (common.Address, error) {
		wrappedCalls++
		require.Equal(t, uint32(1), net)
		require.Equal(t, contractOrigin, addr)
		return contractWrapped, nil
	}

	got, err := resolveTokenAddresses(context.Background(), backend, exits, l2NetworkID, gasNet, gasAddr, lbtMap)
	require.NoError(t, err)
	require.Equal(t, l2NativeTok, got[tokenOriginKey{l2NetworkID, l2NativeTok}])
	require.Equal(t, lbtWrapped, got[tokenOriginKey{1, lbtOrigin}])
	require.Equal(t, contractWrapped, got[tokenOriginKey{1, contractOrigin}])
	require.Equal(t, 1, wrappedCalls, "only the non-LBT external token hits the contract")
}

func TestResolveTokenAddressesContractErrors(t *testing.T) {
	t.Parallel()
	origin := common.BytesToAddress([]byte("origin"))
	exits := []*agglayertypes.BridgeExit{erc20Exit(1, origin, common.HexToAddress("0xd"), 1)}

	// zero wrapped address → error
	backend := newMockBackend()
	backend.tokenWrapped = func(context.Context, uint32, common.Address) (common.Address, error) {
		return common.Address{}, nil
	}
	_, err := resolveTokenAddresses(context.Background(), backend, exits, 10, 0, common.Address{}, nil)
	require.Error(t, err)

	// contract call fails → error
	backend.tokenWrapped = func(context.Context, uint32, common.Address) (common.Address, error) {
		return common.Address{}, errors.New("rpc down")
	}
	_, err = resolveTokenAddresses(context.Background(), backend, exits, 10, 0, common.Address{}, nil)
	require.Error(t, err)
}

// --- replayBridgeExits ---------------------------------------------------------------------------

func TestReplayBridgeExitsHappyPath(t *testing.T) {
	t.Parallel()
	exits := []*agglayertypes.BridgeExit{
		nativeAssetExit(common.HexToAddress("0x01"), 10),
		nativeAssetExit(common.HexToAddress("0x02"), 20),
		nativeAssetExit(common.HexToAddress("0x03"), 30),
	}
	backend := newMockBackend()

	leaves, err := replayBridgeExits(context.Background(), replayTestConfig(t), backend,
		exits, nil, 0, common.Address{})
	require.NoError(t, err)
	require.Len(t, leaves, len(exits))

	seen := map[uint32]bool{}
	for _, l := range leaves {
		require.NotEqual(t, common.Hash{}, l.TxHash)
		seen[l.DepositCount] = true
	}
	require.Len(t, seen, len(exits), "each exit got a distinct deposit count")
	require.Len(t, backend.setBalanceCalls, 3, "balance set once per sender")
}

func TestReplayBridgeExitsERC20Prepares(t *testing.T) {
	t.Parallel()
	token := common.BytesToAddress([]byte("tok"))
	exits := []*agglayertypes.BridgeExit{erc20Exit(1, common.BytesToAddress([]byte("orig")), common.HexToAddress("0x01"), 5)}
	l2Tokens := map[tokenOriginKey]common.Address{{network: 1, addr: common.BytesToAddress([]byte("orig"))}: token}

	backend := newMockBackend()
	leaves, err := replayBridgeExits(context.Background(), replayTestConfig(t), backend,
		exits, l2Tokens, 0, common.Address{})
	require.NoError(t, err)
	require.Len(t, leaves, 1)
	require.Equal(t, []common.Address{token}, backend.prepareCalls)
}

func TestReplayBridgeExitsUnresolvedTokenFails(t *testing.T) {
	t.Parallel()
	// ERC-20 exit whose token is absent from the resolved map → findTokenAddress fails up front.
	exits := []*agglayertypes.BridgeExit{erc20Exit(1, common.BytesToAddress([]byte("orig")), common.HexToAddress("0x01"), 5)}
	_, err := replayBridgeExits(context.Background(), replayTestConfig(t), newMockBackend(),
		exits, nil, 0, common.Address{})
	require.Error(t, err)
}

func TestReplayBridgeExitsSendErrorFailsFast(t *testing.T) {
	t.Parallel()
	exits := []*agglayertypes.BridgeExit{nativeAssetExit(common.HexToAddress("0x01"), 1)}
	backend := newMockBackend()
	backend.sendTx = func(context.Context, *agglayertypes.BridgeExit, bool, common.Address) (common.Hash, error) {
		return common.Hash{}, errors.New("send failed")
	}
	_, err := replayBridgeExits(context.Background(), replayTestConfig(t), backend,
		exits, nil, 0, common.Address{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "send failed")
}

func TestReplayBridgeExitsRevertFailsFast(t *testing.T) {
	t.Parallel()
	exits := []*agglayertypes.BridgeExit{nativeAssetExit(common.HexToAddress("0x01"), 1)}
	backend := newMockBackend()
	backend.waitReceipt = func(context.Context, common.Hash) ([]rpcLog, error) {
		return nil, errors.New("transaction reverted")
	}
	_, err := replayBridgeExits(context.Background(), replayTestConfig(t), backend,
		exits, nil, 0, common.Address{})
	require.Error(t, err)
}

func TestReplayBridgeExitsMissingBridgeEventFailsFast(t *testing.T) {
	t.Parallel()
	exits := []*agglayertypes.BridgeExit{nativeAssetExit(common.HexToAddress("0x01"), 1)}
	backend := newMockBackend()
	backend.waitReceipt = func(context.Context, common.Hash) ([]rpcLog, error) {
		return []rpcLog{{Topics: []string{common.HexToHash("0xunrelated").Hex()}}}, nil
	}
	_, err := replayBridgeExits(context.Background(), replayTestConfig(t), backend,
		exits, nil, 0, common.Address{})
	require.Error(t, err)
}

func TestReplayBridgeExitsDeferredThenRetried(t *testing.T) {
	t.Parallel()
	exits := []*agglayertypes.BridgeExit{nativeAssetExit(common.HexToAddress("0x01"), 1)}
	backend := newMockBackend()

	var mu sync.Mutex
	calls := map[common.Hash]int{}
	backend.waitReceipt = func(_ context.Context, hash common.Hash) ([]rpcLog, error) {
		mu.Lock()
		calls[hash]++
		n := calls[hash]
		mu.Unlock()
		if n == 1 {
			return nil, errReceiptTimeout // first poll times out → deferred
		}
		return bridgeEventReceipt(0, 1, 0), nil // retry pass finds the receipt
	}

	leaves, err := replayBridgeExits(context.Background(), replayTestConfig(t), backend,
		exits, nil, 0, common.Address{})
	require.NoError(t, err)
	require.Len(t, leaves, 1)
	require.NotEqual(t, common.Hash{}, leaves[0].TxHash)
}

// --- retryDeferredExit ---------------------------------------------------------------------------

func newSentTx() sentTx {
	exit := nativeAssetExit(common.HexToAddress("0x01"), 1)
	return sentTx{
		index: 0,
		hash:  common.HexToHash("0xaaa"),
		job:   exitJob{index: 0, bridge: exit, isNative: true},
	}
}

func TestRetryDeferredExitImmediate(t *testing.T) {
	t.Parallel()
	backend := newMockBackend()
	backend.waitReceipt = func(context.Context, common.Hash) ([]rpcLog, error) {
		return bridgeEventReceipt(7, 5, 1), nil
	}
	leaf, err := retryDeferredExit(context.Background(), backend, newSentTx())
	require.NoError(t, err)
	require.Equal(t, uint32(7), leaf.DepositCount)
}

func TestRetryDeferredExitResendThenSucceeds(t *testing.T) {
	t.Parallel()
	backend := newMockBackend()
	var polls int
	backend.waitReceipt = func(context.Context, common.Hash) ([]rpcLog, error) {
		polls++
		if polls == 1 {
			return nil, errReceiptTimeout // still not mined → triggers a re-send
		}
		return bridgeEventReceipt(3, 2, 0), nil
	}
	var resent bool
	backend.sendTx = func(context.Context, *agglayertypes.BridgeExit, bool, common.Address) (common.Hash, error) {
		resent = true
		return common.HexToHash("0xbbb"), nil
	}
	leaf, err := retryDeferredExit(context.Background(), backend, newSentTx())
	require.NoError(t, err)
	require.Equal(t, uint32(3), leaf.DepositCount)
	require.True(t, resent)
}

func TestRetryDeferredExitRevertIsTerminal(t *testing.T) {
	t.Parallel()
	backend := newMockBackend()
	backend.waitReceipt = func(context.Context, common.Hash) ([]rpcLog, error) {
		return nil, errors.New("transaction reverted")
	}
	_, err := retryDeferredExit(context.Background(), backend, newSentTx())
	require.Error(t, err)
}

func TestRetryDeferredExitResendError(t *testing.T) {
	t.Parallel()
	backend := newMockBackend()
	backend.waitReceipt = func(context.Context, common.Hash) ([]rpcLog, error) {
		return nil, errReceiptTimeout
	}
	backend.sendTx = func(context.Context, *agglayertypes.BridgeExit, bool, common.Address) (common.Hash, error) {
		return common.Hash{}, errors.New("resend failed")
	}
	_, err := retryDeferredExit(context.Background(), backend, newSentTx())
	require.Error(t, err)
	require.Contains(t, err.Error(), "re-send")
}

// --- runStepG2ShadowFork via mock launcher -------------------------------------------------------

func TestRunStepG2ShadowForkLauncherError(t *testing.T) {
	t.Parallel()
	cfg := replayTestConfig(t)
	launcher := &mockForkLauncher{err: errors.New("anvil not found")}
	_, err := runStepG2(context.Background(), cfg, launcher, 100,
		&agglayertypes.Certificate{BridgeExits: []*agglayertypes.BridgeExit{nativeAssetExit(common.HexToAddress("0x1"), 1)}}, nil)
	require.Error(t, err)
}

// genesisForkRootOf computes the lite tree root of a genesis→fork DB holding `genesis` bridges (as
// makeG1LiteDB creates them), using a scratch config so the caller's G2 DB is untouched.
func genesisForkRootOf(t *testing.T, genesis int) common.Hash {
	t.Helper()
	cfg := replayTestConfig(t)
	makeG1LiteDB(t, cfg, genesis)
	root, err := buildLiteTreeWithReplayed(context.Background(), cfg, nil)
	require.NoError(t, err)
	return root
}

// certOrderRootOf computes the lite tree root of `genesis` genesis→fork bridges followed by the
// given exits (empty metadata) in the given order, using a scratch config.
func certOrderRootOf(t *testing.T, genesis int, exits []*agglayertypes.BridgeExit) common.Hash {
	t.Helper()
	cfg := replayTestConfig(t)
	makeG1LiteDB(t, cfg, genesis)
	root, _, err := buildLiteTreeFromCertificate(
		context.Background(), cfg, &agglayertypes.Certificate{BridgeExits: exits},
		100, 0, common.Address{}, make([][]byte, len(exits)),
	)
	require.NoError(t, err)
	return root
}

// initialLERThen returns a localExitRoot mock: the first call (the initial LER, read before the
// replay) returns initial; every later call (getRoot after the replay) returns afterReplay.
func initialLERThen(initial, afterReplay common.Hash) func(context.Context, string) (common.Hash, error) {
	calls := 0
	return func(context.Context, string) (common.Hash, error) {
		calls++
		if calls == 1 {
			return initial, nil
		}
		return afterReplay, nil
	}
}

// TestRunStepG2ShadowForkInitialLERMismatchAborts verifies that a divergence between the
// genesis→fork lite tree and the contract's LER at the fork block is a hard error.
func TestRunStepG2ShadowForkInitialLERMismatchAborts(t *testing.T) {
	t.Parallel()
	cfg := replayTestConfig(t)
	makeG1LiteDB(t, cfg, 2)

	backend := newMockBackend()
	backend.nextDeposit = 2 // replayed deposit counts continue after the 2 genesis→fork bridges
	backend.localExitRoot = func(context.Context, string) (common.Hash, error) {
		return common.HexToHash("0xdeadbeef"), nil // never matches the rebuilt lite tree
	}
	launcher := &mockForkLauncher{backend: backend}

	cert := &agglayertypes.Certificate{BridgeExits: []*agglayertypes.BridgeExit{
		nativeAssetExit(common.HexToAddress("0x01"), 100),
	}}
	_, err := runStepG2(context.Background(), cfg, launcher, 100, cert, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not match contract initial LER")
	require.True(t, launcher.started)
}

func TestRunStepG2ShadowForkGetRootMismatchAborts(t *testing.T) {
	t.Parallel()
	cfg := replayTestConfig(t)
	makeG1LiteDB(t, cfg, 2)

	backend := newMockBackend()
	backend.nextDeposit = 2 // replayed deposit counts continue after the 2 genesis→fork bridges
	// The initial LER matches the genesis→fork tree, but getRoot after the replay never matches.
	backend.localExitRoot = initialLERThen(genesisForkRootOf(t, 2), common.HexToHash("0xdeadbeef"))
	launcher := &mockForkLauncher{backend: backend}

	cert := &agglayertypes.Certificate{BridgeExits: []*agglayertypes.BridgeExit{
		nativeAssetExit(common.HexToAddress("0x01"), 100),
	}}
	_, err := runStepG2(context.Background(), cfg, launcher, 100, cert, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not match contract getRoot")
	require.True(t, launcher.started)
}

// TestRunStepG2ShadowForkRootMismatchAbortsEvenWithIgnoreOption verifies that a root mismatch is a
// hard error regardless of ignoreUnsupportedL2Events: that option only affects Step G1's lite syncer,
// not the Step G2 root cross-check against the contract.
func TestRunStepG2ShadowForkRootMismatchAbortsEvenWithIgnoreOption(t *testing.T) {
	t.Parallel()
	cfg := replayTestConfig(t)
	cfg.Options.IgnoreUnsupportedL2Events = true
	makeG1LiteDB(t, cfg, 2)

	backend := newMockBackend()
	backend.nextDeposit = 2 // replayed deposit counts continue after the 2 genesis→fork bridges
	backend.localExitRoot = initialLERThen(genesisForkRootOf(t, 2), common.HexToHash("0xdeadbeef"))
	launcher := &mockForkLauncher{backend: backend}

	cert := &agglayertypes.Certificate{BridgeExits: []*agglayertypes.BridgeExit{
		nativeAssetExit(common.HexToAddress("0x01"), 100),
		nativeAssetExit(common.HexToAddress("0x02"), 200),
	}}
	_, err := runStepG2(context.Background(), cfg, launcher, 100, cert, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not match contract getRoot")
}

// TestRunStepG2ShadowForkDeterministicOrder is the AET regression test for the non-deterministic
// certificate ordering: the parallel replay assigns on-chain deposit counts in whatever order the
// txs happen to execute, and that order must NOT leak into the certificate. Two runs whose replays
// execute in opposite orders must produce the same exit order (the incoming one) and the same
// NewLocalExitRoot (the cert-order lite tree root), while each run's getRoot() — which does depend
// on the replay order — is still verified against a lite tree in that replay order.
func TestRunStepG2ShadowForkDeterministicOrder(t *testing.T) {
	t.Parallel()

	destA := common.HexToAddress("0x0a")
	destB := common.HexToAddress("0x0b")
	newExits := func() []*agglayertypes.BridgeExit {
		return []*agglayertypes.BridgeExit{
			nativeAssetExit(destA, 100),
			nativeAssetExit(destB, 200),
		}
	}

	genesisRoot := genesisForkRootOf(t, 2)
	// certOrderRoot is the tree for [A, B]; replayReversedRoot the one for [B, A].
	certOrderRoot := certOrderRootOf(t, 2, newExits())
	replayReversedRoot := certOrderRootOf(t, 2, []*agglayertypes.BridgeExit{newExits()[1], newExits()[0]})
	require.NotEqual(t, certOrderRoot, replayReversedRoot, "amounts must differ so order changes the root")

	run := func(depositCounts map[common.Address]uint32, getRoot common.Hash) (*StepGResult, []*agglayertypes.BridgeExit) {
		cfg := replayTestConfig(t)
		makeG1LiteDB(t, cfg, 2)

		backend := newMockBackend()
		backend.localExitRoot = initialLERThen(genesisRoot, getRoot)
		backend.sendTx = func(_ context.Context, e *agglayertypes.BridgeExit, _ bool, _ common.Address) (common.Hash, error) {
			backend.mu.Lock()
			defer backend.mu.Unlock()
			dc := depositCounts[e.DestinationAddress]
			hash := common.BigToHash(big.NewInt(int64(dc) + 1))
			backend.hashToDeposit[hash] = dc
			return hash, nil
		}
		launcher := &mockForkLauncher{backend: backend}

		cert := &agglayertypes.Certificate{BridgeExits: newExits()}
		result, err := runStepG2(context.Background(), cfg, launcher, 100, cert, nil)
		require.NoError(t, err)
		return result, cert.BridgeExits
	}

	// Run 1: the replay happens to execute in certificate order (A gets deposit count 2, B gets 3).
	inOrder, exitsInOrder := run(map[common.Address]uint32{destA: 2, destB: 3}, certOrderRoot)
	// Run 2: the replay executes in the opposite order (B gets 2, A gets 3) — getRoot differs.
	reversed, exitsReversed := run(map[common.Address]uint32{destA: 3, destB: 2}, replayReversedRoot)

	// Both runs keep the deterministic incoming exit order and compute the same NewLocalExitRoot.
	for _, exits := range [][]*agglayertypes.BridgeExit{exitsInOrder, exitsReversed} {
		require.Equal(t, destA, exits[0].DestinationAddress)
		require.Equal(t, destB, exits[1].DestinationAddress)
	}
	require.Equal(t, certOrderRoot, inOrder.NewLocalExitRoot)
	require.Equal(t, certOrderRoot, reversed.NewLocalExitRoot)
	require.Equal(t, uint64(2), inOrder.BridgeExitCount)
}
