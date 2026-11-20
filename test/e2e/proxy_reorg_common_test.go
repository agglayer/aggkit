package e2e

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgeservice/client"
	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/test/e2e/envs"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// This file holds what the aggkit-proxy reorg acceptance tests (agglayer/aggkit#1972, proxy_reorg_*_test.go)
// share: the client of the proxy's activity endpoint, senders with no previous activity, the transactions
// the scenarios mine by hand, and the reorgs they provoke on top of the helpers of reorg_utils.go.
//
// Every scenario follows the same order, so that a failure points at the right component:
//
//  1. the chain is paused and the fact under test (a bridge, a claim, a GER injection) is mined by hand in
//     a block that is not final, with one more block on top (see "What aggkit detects" in the README);
//  2. the bridge service of aggkit serves the fact, and the proxy reports it as not final;
//  3. the reorg is provoked, and aggkit must report it (GET /bridge/v1/last-reorg-event) before anything
//     is asserted on the proxy: if aggkit does not see the reorg, the proxy cannot either;
//  4. the proxy must converge to what the chain holds now.
//
// Nothing waits on time alone: every wait polls a condition with a timeout. The timeouts are generous
// because the proxy only refreshes in the background (activity: ActivityPollInterval; tracker: 10s).

const (
	// proxyReorgProxy2URL is the host URL of aggkit-proxy-002 (aggkit-proxy-001 is proxyTrackerBaseURL).
	proxyReorgProxy2URL = "http://127.0.0.1:15602"

	// proxyReorgDepth is the depth of the reorgs of the scenarios: the block with the fact under test and
	// the block mined on top of it. It is also the whole non-final window of the anvil chains.
	proxyReorgDepth uint64 = 2
	// proxyReorgBlockSpacing is the timestamp gap, in seconds, between the block with the fact under test
	// and its parent, so that the block a reorg builds in its place is never identical (see Reorg).
	proxyReorgBlockSpacing uint64 = 2

	// proxyReorgServeTimeout bounds how long aggkit takes to serve (or stop serving) a fact after its
	// block was mined or reorged: the syncers poll for new blocks every 3s.
	proxyReorgServeTimeout = 90 * time.Second
	// proxyReorgDetectTimeout bounds how long aggkit takes to report a reorg (one reorg-detector tick, 2s).
	proxyReorgDetectTimeout = 60 * time.Second
	// proxyReorgConvergeTimeout bounds how long the proxy takes to reflect a change that aggkit already
	// serves: one background refresh of the activity (ActivityPollInterval) or one tracker tick (10s),
	// plus the calls it makes.
	proxyReorgConvergeTimeout = 3 * time.Minute
	// proxyReorgMineTimeout bounds the wait for a transaction to be mined on a chain that keeps mining.
	proxyReorgMineTimeout = time.Minute

	// proxyReorgBridgeAmount is the amount of every bridge of the scenarios.
	proxyReorgBridgeAmount int64 = 1e14
	// proxyReorgGasFunds is the native balance a fresh sender gets on each chain it uses.
	proxyReorgGasFunds int64 = 1e17
	// proxyReorgTransferGas is the gas of a plain value transfer.
	proxyReorgTransferGas uint64 = 21_000

	// trackerWSBuffer is how many pushed WebSocket messages a subscription buffers between two waits.
	trackerWSBuffer = 256

	// activityMaxPageSize is the largest page the activity endpoint accepts.
	activityMaxPageSize uint32 = 200

	// emptyActivityRevision is the documented revision of an address with no bridges.
	emptyActivityRevision = "v2:0000000000000000000000000000000000000000000000000000000000000000"

	claimStatusPending          = "pending"
	claimStatusReadyToClaim     = "readyToClaim"
	claimStatusClaimed          = "claimed"
	claimStatusClaimedFinalized = "claimed_finalized"

	activitySourceBridge = "bridge"
	activitySourceRPC    = "rpc"
)

// insertGERSelector is the selector of insertGlobalExitRoot(bytes32), the aggoracle's injection call on
// the L2 GER contract.
var insertGERSelector = crypto.Keccak256([]byte("insertGlobalExitRoot(bytes32)"))[:4]

// activityRevisionFormat is the documented shape of ActivityResponse.revision.
var activityRevisionFormat = regexp.MustCompile(`^v2:[0-9a-f]{64}$`)

// errActivityNotReady is returned by fetchActivity while the proxy answers 503: the address was just
// registered and its first background refresh has not finished.
var errActivityNotReady = errors.New("activity not ready yet (503)")

// proxyActivityWarning mirrors api.ActivityWarningItem.
type proxyActivityWarning struct {
	NetworkID uint32 `json:"network_id"`
	Message   string `json:"message"`
}

// proxyActivityItem mirrors api.ActivityItem (bridgetracker/api/activity_command.go). Finalized is a
// pointer so that the tests can require the field to be always present.
type proxyActivityItem struct {
	Bridge               *bridgeservicetypes.BridgeResponse `json:"bridge"`
	BridgeNetworkID      uint32                             `json:"bridge_network_id"`
	Source               string                             `json:"source"`
	ClaimStatus          string                             `json:"claim_status"`
	Finalized            *bool                              `json:"finalized"`
	ClaimNetworkID       uint32                             `json:"claim_network_id"`
	Claim                *bridgeservicetypes.ClaimResponse  `json:"claim"`
	CreationTimestamp    uint64                             `json:"creation_timestamp"`
	LastUpdatedTimestamp uint64                             `json:"last_updated_timestamp"`
	Errors               map[string]string                  `json:"errors"`
}

// isFinalized reports the item's finalized flag (false when the server omitted it, which
// requireActivityWellFormed rejects).
func (i *proxyActivityItem) isFinalized() bool {
	return i.Finalized != nil && *i.Finalized
}

// String summarizes the item for logs and failure messages.
func (i *proxyActivityItem) String() string {
	if i.Bridge == nil {
		return "{item without bridge}"
	}
	claim := "none"
	if i.Claim != nil {
		claim = fmt.Sprintf("%s@%d", i.Claim.TxHash, i.Claim.BlockNum)
	}
	return fmt.Sprintf("{net=%d deposit=%d tx=%s block=%d amount=%s source=%s status=%s finalized=%t claim=%s}",
		i.BridgeNetworkID, i.Bridge.DepositCount, i.Bridge.TxHash, i.Bridge.BlockNum, i.Bridge.Amount,
		i.Source, i.ClaimStatus, i.isFinalized(), claim)
}

// proxyActivityResponse mirrors api.ActivityResponse.
type proxyActivityResponse struct {
	FromAddress common.Address         `json:"from_address"`
	Bridges     []proxyActivityItem    `json:"bridges"`
	Count       int                    `json:"count"`
	Revision    string                 `json:"revision"`
	Warnings    []proxyActivityWarning `json:"warnings"`
}

// find returns the item of the bridge (networkID, depositCount), or nil.
func (r *proxyActivityResponse) find(networkID, depositCount uint32) *proxyActivityItem {
	for i := range r.Bridges {
		item := &r.Bridges[i]
		if item.Bridge != nil && item.BridgeNetworkID == networkID && item.Bridge.DepositCount == depositCount {
			return item
		}
	}
	return nil
}

// warningFor returns the warning reported for networkID, or nil.
func (r *proxyActivityResponse) warningFor(networkID uint32) *proxyActivityWarning {
	for i := range r.Warnings {
		if r.Warnings[i].NetworkID == networkID {
			return &r.Warnings[i]
		}
	}
	return nil
}

// String summarizes the response for logs and failure messages.
func (r *proxyActivityResponse) String() string {
	items := make([]string, 0, len(r.Bridges))
	for i := range r.Bridges {
		items = append(items, r.Bridges[i].String())
	}
	return fmt.Sprintf("count=%d revision=%s warnings=%+v bridges=[%s]",
		r.Count, r.Revision, r.Warnings, strings.Join(items, " "))
}

// fetchActivityPage calls GET /tracker/v1/activity/from/{from} on the proxy at baseURL. It returns
// errActivityNotReady while the proxy answers 503.
func fetchActivityPage(
	ctx context.Context, baseURL string, from common.Address, pageNumber, pageSize uint32,
) (*proxyActivityResponse, error) {
	url := fmt.Sprintf("%s/tracker/v1/activity/from/%s?page_number=%d&page_size=%d",
		baseURL, from.Hex(), pageNumber, pageSize)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusServiceUnavailable:
		return nil, fmt.Errorf("%w: %s", errActivityNotReady, strings.TrimSpace(string(body)))
	default:
		return nil, fmt.Errorf("GET activity returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var activity proxyActivityResponse
	if err := json.Unmarshal(body, &activity); err != nil {
		return nil, fmt.Errorf("decode activity response: %w", err)
	}
	return &activity, nil
}

// fetchActivity returns the whole activity of from in one page (the scenarios use senders with a handful
// of bridges). It fails if the address has more bridges than a page holds.
func fetchActivity(ctx context.Context, baseURL string, from common.Address) (*proxyActivityResponse, error) {
	activity, err := fetchActivityPage(ctx, baseURL, from, 1, activityMaxPageSize)
	if err != nil {
		return nil, err
	}
	if activity.Count != len(activity.Bridges) {
		return nil, fmt.Errorf("activity of %s does not fit in one page: count=%d, got %d",
			from.Hex(), activity.Count, len(activity.Bridges))
	}
	return activity, nil
}

// waitForActivity polls the activity of from on the proxy at baseURL until match accepts it and returns
// that response. A request that fails (503 while the address is not ready, a transient error) is retried.
func waitForActivity(
	ctx context.Context, baseURL string, from common.Address, what string, timeout time.Duration,
	match func(*proxyActivityResponse) bool,
) (*proxyActivityResponse, error) {
	var (
		found *proxyActivityResponse
		state = "not polled yet"
	)
	err := waitUntil(ctx, timeout, fmt.Sprintf("the activity of %s on %s: %s", from.Hex(), baseURL, what),
		func() (bool, error) {
			activity, err := fetchActivity(ctx, baseURL, from)
			if err != nil {
				state = err.Error()
				return false, nil
			}
			state = activity.String()
			if !match(activity) {
				return false, nil
			}
			found = activity
			return true, nil
		},
		func() string { return state })
	return found, err
}

// requireActivityWellFormed checks what the API contract guarantees for every activity response: a
// well-formed revision, finalized always present, and no bridge listed twice.
func requireActivityWellFormed(t *testing.T, activity *proxyActivityResponse) {
	t.Helper()
	require.Regexp(t, activityRevisionFormat, activity.Revision, "revision must be v2: plus 64 hex characters")
	seen := make(map[string]bool, len(activity.Bridges))
	for i := range activity.Bridges {
		item := &activity.Bridges[i]
		require.NotNil(t, item.Bridge, "activity item without bridge")
		require.NotNil(t, item.Finalized, "finalized must always be present: %s", item)
		key := string(item.Bridge.GlobalIndex)
		require.False(t, seen[key], "bridge global_index=%s listed twice: %s", key, activity)
		seen[key] = true
		if item.ClaimStatus == claimStatusClaimedFinalized {
			require.True(t, item.isFinalized(), "claimed_finalized implies finalized: %s", item)
		}
	}
}

// requireActivityRefreshedAfter waits until the proxy has refreshed the item (networkID, depositCount)
// of from after the wall-clock instant since: last_updated_timestamp advances on every refresh of an item
// that is not settled, whether or not anything changed. It is how the tests wait for "a refresh ran and
// changed nothing" without sleeping.
func requireActivityRefreshedAfter(
	ctx context.Context, t *testing.T, baseURL string, from common.Address, networkID, depositCount uint32,
	since time.Time,
) *proxyActivityResponse {
	t.Helper()
	// last_updated_timestamp is in whole seconds: require a strictly later second.
	threshold := uint64(since.Unix())
	activity, err := waitForActivity(ctx, baseURL, from,
		fmt.Sprintf("bridge (network %d, deposit_count %d) refreshed after %d", networkID, depositCount, threshold),
		proxyReorgConvergeTimeout,
		func(a *proxyActivityResponse) bool {
			item := a.find(networkID, depositCount)
			return item != nil && item.LastUpdatedTimestamp > threshold
		})
	require.NoError(t, err)
	return activity
}

// proxyReorgSender is an account created for one scenario, so that its activity on the proxy starts empty
// and every count and revision the scenario asserts on is exact.
type proxyReorgSender struct {
	key  *ecdsa.PrivateKey
	addr common.Address
}

// newProxyReorgSender creates an account with no history.
func newProxyReorgSender(t *testing.T) *proxyReorgSender {
	t.Helper()
	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	return &proxyReorgSender{key: key, addr: crypto.PubkeyToAddress(key.PublicKey)}
}

// opts returns transact opts of the sender for the chain with chainID.
func (s *proxyReorgSender) opts(t *testing.T, chainID *big.Int) *bind.TransactOpts {
	t.Helper()
	opts, err := bind.NewKeyedTransactorWithChainID(s.key, chainID)
	require.NoError(t, err)
	return opts
}

// fundNative sends proxyReorgGasFunds of the native token of a chain from funder to the sender and waits
// for it to be mined: call it before pausing the chain.
func (s *proxyReorgSender) fundNative(
	ctx context.Context, t *testing.T, chainClient *ethclient.Client, funder *bind.TransactOpts,
) {
	t.Helper()
	nonce, err := chainClient.PendingNonceAt(ctx, funder.From)
	require.NoError(t, err)
	gasPrice, err := chainClient.SuggestGasPrice(ctx)
	require.NoError(t, err)
	tx, err := funder.Signer(funder.From, ethtypes.NewTx(&ethtypes.LegacyTx{
		Nonce: nonce, To: &s.addr, Value: big.NewInt(proxyReorgGasFunds), Gas: proxyReorgTransferGas,
		// twice the suggested price: the base fee may rise before the transfer is mined
		GasPrice: new(big.Int).Mul(gasPrice, big.NewInt(2)),
	}))
	require.NoError(t, err)
	require.NoError(t, chainClient.SendTransaction(ctx, tx), "fund %s", s.addr.Hex())
	requireMined(ctx, t, chainClient, tx, "funding of "+s.addr.Hex())
}

// requireMined waits for tx to be mined successfully on a chain that keeps mining.
func requireMined(
	ctx context.Context, t *testing.T, chainClient *ethclient.Client, tx *ethtypes.Transaction, what string,
) *ethtypes.Receipt {
	t.Helper()
	mineCtx, cancel := context.WithTimeout(ctx, proxyReorgMineTimeout)
	defer cancel()
	receipt, err := bind.WaitMined(mineCtx, chainClient, tx)
	require.NoError(t, err, "wait for the %s (%s)", what, tx.Hash().Hex())
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, receipt.Status, "the %s (%s) reverted", what, tx.Hash().Hex())
	return receipt
}

// l2Bridger is a sender ready to bridge the L2-native MintableERC20 from an L2 to L1 (L2-native tokens
// bypass the Local Balance Tree underflow check of the L2 bridge, see testmain_test.go).
type l2Bridger struct {
	l2     *envs.L2Config
	sender *proxyReorgSender
	opts   *bind.TransactOpts
}

// newL2Bridger funds the sender on the L2, mints the ERC20 to it and approves the bridge. Everything is
// mined here: call it before pausing the chain.
func newL2Bridger(ctx context.Context, t *testing.T, l2 *envs.L2Config, sender *proxyReorgSender) *l2Bridger {
	t.Helper()
	sender.fundNative(ctx, t, l2.Client, l2.Transactor)
	opts := sender.opts(t, l2.ChainID)
	opts.Context = ctx

	// enough balance and allowance for every bridge a scenario sends, with slack
	amount := new(big.Int).Mul(big.NewInt(proxyReorgBridgeAmount), big.NewInt(1000))
	mintTx, err := l2.Contracts.MintableERC20.Mint(opts, sender.addr, amount)
	require.NoError(t, err)
	requireMined(ctx, t, l2.Client, mintTx, "ERC20 mint")
	approveTx, err := l2.Contracts.MintableERC20.Approve(opts, l2.Contracts.L2BridgeAddress, amount)
	require.NoError(t, err)
	requireMined(ctx, t, l2.Client, approveTx, "ERC20 approve")
	return &l2Bridger{l2: l2, sender: sender, opts: opts}
}

// send sends a bridge of amount to L1 WITHOUT waiting for it to be mined (the chain is paused: the
// scenario mines it).
func (b *l2Bridger) send(t *testing.T, amount int64) *ethtypes.Transaction {
	t.Helper()
	tx, err := b.l2.Contracts.L2Bridge.BridgeAsset(
		b.opts, 0, b.sender.addr, big.NewInt(amount), b.l2.Contracts.MintableERC20Address, true, nil)
	require.NoError(t, err, "send the L2 bridge transaction")
	return tx
}

// sign builds and signs a bridge of amount with the given nonce WITHOUT sending it, with the fees and
// twice the gas of reference (the gas cannot be estimated for a nonce that is already used, and another
// amount changes the calldata cost). It is the transaction a reorg includes in place of another one of
// the same sender.
func (b *l2Bridger) sign(t *testing.T, amount int64, nonce uint64, reference *ethtypes.Transaction) *ethtypes.Transaction {
	t.Helper()
	opts := *b.opts
	opts.NoSend = true
	opts.Nonce = new(big.Int).SetUint64(nonce)
	opts.GasLimit = 2 * reference.Gas()
	opts.GasFeeCap = reference.GasFeeCap()
	opts.GasTipCap = reference.GasTipCap()
	tx, err := b.l2.Contracts.L2Bridge.BridgeAsset(
		&opts, 0, b.sender.addr, big.NewInt(amount), b.l2.Contracts.MintableERC20Address, true, nil)
	require.NoError(t, err, "sign the L2 bridge transaction")
	return tx
}

// depositCount returns the deposit count of the BridgeEvent of a mined L2 bridge transaction.
func (b *l2Bridger) depositCount(t *testing.T, receipt *ethtypes.Receipt) uint32 {
	t.Helper()
	// an ERC20 bridge also emits a Transfer log: look for the BridgeEvent among all of them
	for _, lg := range receipt.Logs {
		if event, err := b.l2.Contracts.L2Bridge.ParseBridgeEvent(*lg); err == nil {
			return event.DepositCount
		}
	}
	require.FailNow(t, "no BridgeEvent in the logs of "+receipt.TxHash.Hex())
	return 0
}

// sendL1Bridge sends a native ETH bridge from L1 to destinationNetwork WITHOUT waiting for it to be
// mined.
func sendL1Bridge(
	t *testing.T, from *bind.TransactOpts, destinationNetwork uint32, destination common.Address, amount int64,
) *ethtypes.Transaction {
	t.Helper()
	opts := *from
	opts.Value = big.NewInt(amount)
	opts.GasLimit = l1BridgeGasLimit
	tx, err := testEnv.L1.Contracts.Bridge.BridgeAsset(
		&opts, destinationNetwork, destination, big.NewInt(amount), common.Address{}, true, nil)
	require.NoError(t, err, "send the L1 bridge transaction")
	return tx
}

// l1DepositCount returns the deposit count of the BridgeEvent of a mined L1 bridge transaction.
func l1DepositCount(t *testing.T, receipt *ethtypes.Receipt) uint32 {
	t.Helper()
	for _, lg := range receipt.Logs {
		if event, err := testEnv.L1.Contracts.Bridge.ParseBridgeEvent(*lg); err == nil {
			return event.DepositCount
		}
	}
	require.FailNow(t, "no BridgeEvent in the logs of "+receipt.TxHash.Hex())
	return 0
}

// receiptReader is the part of an RPC client the helpers below need.
type receiptReader interface {
	TransactionReceipt(ctx context.Context, txHash common.Hash) (*ethtypes.Receipt, error)
}

// mineTxPlusOne mines tx (already sent to a paused chain) in a block of its own, spaced from its parent,
// and mines one more block on top: the block of tx is then head-1, not final, and tracked by aggkit under
// a hash that only the reorg detector compares again (see "What aggkit detects" in the README). It returns
// the receipt of tx.
func mineTxPlusOne(
	ctx context.Context, t *testing.T, chain *AnvilChain, chainClient receiptReader, tx *ethtypes.Transaction,
) *ethtypes.Receipt {
	t.Helper()
	receipt := mineTx(ctx, t, chain, chainClient, tx)
	require.NoError(t, chain.Mine(ctx, 1))
	return receipt
}

// mineTx mines tx (already sent to a paused chain) in the next block, spaced from its parent, and returns
// its receipt. The block of tx is the head.
func mineTx(
	ctx context.Context, t *testing.T, chain *AnvilChain, chainClient receiptReader, tx *ethtypes.Transaction,
) *ethtypes.Receipt {
	t.Helper()
	require.NoError(t, chain.MineSpaced(ctx, 1, proxyReorgBlockSpacing))
	receipt, err := chainClient.TransactionReceipt(ctx, tx.Hash())
	require.NoError(t, err, "transaction %s not mined by the manual block of %s", tx.Hash().Hex(), chain.Name())
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, receipt.Status, "transaction %s reverted", tx.Hash().Hex())
	log.Infof("[%s] tx %s mined at block %d", chain.Name(), tx.Hash().Hex(), receipt.BlockNumber.Uint64())
	return receipt
}

// requireNonFinal fails unless block is above the finalized block of chain.
func requireNonFinal(ctx context.Context, t *testing.T, chain *AnvilChain, block uint64) {
	t.Helper()
	finalized, err := chain.FinalizedHead(ctx)
	require.NoError(t, err)
	require.Less(t, finalized.Number, block, "block %d of %s must not be finalized yet", block, chain.Name())
}

// requireAnvilChains skips the test unless every chain answers the anvil_* RPCs (env anvil-2chains).
func requireAnvilChains(ctx context.Context, t *testing.T, chains ...*AnvilChain) {
	t.Helper()
	for _, chain := range chains {
		supported, err := chain.Supported(ctx)
		require.NoError(t, err, "probe the anvil_* RPCs of %s", chain.Name())
		if !supported {
			t.Skipf("%s is not an anvil node: the reorg scenarios need the anvil_* RPCs (env anvil-2chains)", chain.Name())
		}
	}
}

// requireProxyReorgEnv is the common preamble of the proxy reorg tests: it skips in short mode and on envs
// without a second chain or without anvil nodes, and waits for the aggkit-proxy to answer.
func requireProxyReorgEnv(ctx context.Context, t *testing.T, chains ...*AnvilChain) {
	t.Helper()
	require.NotNil(t, testEnv, "testEnv must be set by TestMain")
	if testEnv.L2B == nil {
		t.Skip("the proxy reorg scenarios need a multi-chain env with aggkit-proxy (L2B must be non-nil)")
	}
	requireAnvilChains(ctx, t, chains...)
	require.NoError(t, waitForTrackerReady(ctx), "aggkit-proxy health check never succeeded")
}

// aggsenderConfigAnchor is a setting of the [AggSender] section of aggkit-001's config, as is, after
// which holdAggsender adds MaxL2BlockNumber (the section does not set it: 0, no limit).
const aggsenderConfigAnchor = "\nTriggerCertMode = \"ASAP\"\n"

const (
	// aggkit001Service and its compose variable for a config rendered outside the repo.
	aggkit001Service   = "aggkit-001"
	aggkit001ConfigEnv = "AGGKIT_001_CONFIG"
	// renderedConfigDirMode / renderedConfigFileMode let the user of any container read a rendered config.
	renderedConfigDirMode  os.FileMode = 0o755
	renderedConfigFileMode os.FileMode = 0o644
)

// renderConfig writes content as name in a new directory outside the repo, readable from any container,
// and returns its path. The directory is removed when the test ends. The tests never write to the config
// files tracked in the repo: a test that is killed must not leave them modified.
func renderConfig(t *testing.T, name, content string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "aggkit-e2e-config-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	require.NoError(t, os.Chmod(dir, renderedConfigDirMode))
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), renderedConfigFileMode))
	require.NoError(t, os.Chmod(path, renderedConfigFileMode))
	return path
}

// holdAggsender keeps the aggsender of aggkit-001 from certifying the L2 blocks the test mines, until the
// test ends: it runs aggkit-001 with a copy of its config that sets [AggSender] MaxL2BlockNumber to the
// current L2 head, and puts it back on its own config in a cleanup. The copy lives outside the repo.
//
// aggkit-001 syncs its L2 bridges at LatestBlock, which is what lets it serve non-final data, and its
// aggsender builds certificates from that same data as soon as there is a bridge or a claim. Left alone,
// it settles a certificate that carries a bridge of a non-final block; when the scenario then reorgs that
// bridge out, the settled local exit root no longer exists on the L2 and the aggsender cannot build
// another certificate, ever ("failed to get local exit root by hash ... not found"). The network 1 -> L1
// settlement is then dead for the rest of the run, which the post-test bridge check of TestMain reports.
// Nothing the scenarios assert needs a certificate, so the aggsender waits until the chain is settled.
func holdAggsender(ctx context.Context, t *testing.T, l2 *AnvilChain) {
	t.Helper()
	original, err := os.ReadFile(testEnv.GetAggkitConfigPath())
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(original), aggsenderConfigAnchor),
		"the [AggSender] section of aggkit-001's config must set TriggerCertMode exactly once")
	require.NotContains(t, string(original), "\nMaxL2BlockNumber", "aggkit-001's config already sets MaxL2BlockNumber")
	head, err := l2.Head(ctx)
	require.NoError(t, err)
	held := renderConfig(t, "aggkit-config.toml", strings.Replace(string(original), aggsenderConfigAnchor,
		fmt.Sprintf("%sMaxL2BlockNumber = %d\n", aggsenderConfigAnchor, head.Number), 1))

	recreate := func(ctx context.Context, composeEnv ...string) error {
		if err := testEnv.RecreateService(ctx, aggkit001Service, composeEnv...); err != nil {
			return err
		}
		if err := testEnv.WaitForBridgeServiceOfAggkit(ctx); err != nil {
			return err
		}
		for _, networkID := range []uint32{0, testEnv.L2.NetworkID} {
			if err := WaitForBridgeSynced(ctx, testEnv.L2.BridgeService, networkID, proxyReorgConvergeTimeout); err != nil {
				return err
			}
		}
		return nil
	}
	t.Cleanup(func() {
		restoreCtx, cancel := context.WithTimeout(context.Background(), 2*proxyReorgConvergeTimeout)
		defer cancel()
		if err := recreate(restoreCtx); err != nil {
			t.Errorf("put aggkit-001 back on its own config: %v", err)
		}
	})
	require.NoError(t, recreate(ctx, aggkit001ConfigEnv+"="+held),
		"run aggkit-001 with the aggsender held at L2 block %d", head.Number)
	log.Infof("aggsender of aggkit-001 held at L2 block %d until the test ends", head.Number)
}

// reorgDropping reorgs the last proxyReorgDepth blocks of chain, re-including every transaction they held
// except drop, and checks that the dropped transactions are gone from the chain.
func reorgDropping(
	ctx context.Context, t *testing.T, chain *AnvilChain, chainClient receiptReader, drop ...common.Hash,
) {
	t.Helper()
	require.NoError(t, chain.ReorgKeepingTxs(ctx, proxyReorgDepth, drop...))
	for _, hash := range drop {
		_, err := chainClient.TransactionReceipt(ctx, hash)
		require.ErrorIs(t, err, ethereum.NotFound, "the reorg must drop transaction %s", hash.Hex())
	}
	log.Infof("[%s] reorg of depth %d dropped %d transaction(s)", chain.Name(), proxyReorgDepth, len(drop))
}

// reorgMoving reorgs the last proxyReorgDepth blocks of chain, re-including every transaction they held in
// the block offset it had, except move, which goes to the block at offset toOffset. It returns the new
// receipt of move.
func reorgMoving(
	ctx context.Context, t *testing.T, chain *AnvilChain, chainClient receiptReader, move common.Hash, toOffset uint64,
) *ethtypes.Receipt {
	t.Helper()
	txs, err := chain.TxsInLastBlocks(ctx, proxyReorgDepth)
	require.NoError(t, err)
	// the moved transaction goes last, so that it never lands before another transaction of the same
	// sender that it depends on
	reordered := make([]ReorgTx, 0, len(txs))
	var moved *ReorgTx
	for i := range txs {
		if txs[i].Hash == move {
			moved = &txs[i]
			continue
		}
		reordered = append(reordered, txs[i])
	}
	require.NotNil(t, moved, "transaction %s is not in the last %d blocks of %s", move.Hex(), proxyReorgDepth, chain.Name())
	require.NotEqual(t, toOffset, moved.BlockOffset, "the transaction is already at offset %d", toOffset)
	moved.BlockOffset = toOffset
	reordered = append(reordered, *moved)
	require.NoError(t, chain.Reorg(ctx, proxyReorgDepth, reordered...))

	receipt, err := chainClient.TransactionReceipt(ctx, move)
	require.NoError(t, err, "the reorg must re-include transaction %s", move.Hex())
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, receipt.Status, "transaction %s reverted after the reorg", move.Hex())
	log.Infof("[%s] reorg of depth %d moved tx %s to block %d",
		chain.Name(), proxyReorgDepth, move.Hex(), receipt.BlockNumber.Uint64())
	return receipt
}

// reorgReplacing reorgs the last proxyReorgDepth blocks of chain, re-including every transaction they held
// except drop, whose place (same block offset) is taken by replacement, a signed transaction that was
// never sent. It returns the receipt of replacement.
func reorgReplacing(
	ctx context.Context, t *testing.T, chain *AnvilChain, chainClient receiptReader, drop common.Hash,
	replacement *ethtypes.Transaction,
) *ethtypes.Receipt {
	t.Helper()
	txs, err := chain.TxsInLastBlocks(ctx, proxyReorgDepth)
	require.NoError(t, err)
	raw, err := replacement.MarshalBinary()
	require.NoError(t, err)
	replaced := false
	for i := range txs {
		if txs[i].Hash == drop {
			txs[i] = ReorgTx{Hash: replacement.Hash(), Raw: hexutil.Bytes(raw), BlockOffset: txs[i].BlockOffset}
			replaced = true
		}
	}
	require.True(t, replaced, "transaction %s is not in the last %d blocks of %s", drop.Hex(), proxyReorgDepth, chain.Name())
	require.NoError(t, chain.Reorg(ctx, proxyReorgDepth, txs...))

	_, err = chainClient.TransactionReceipt(ctx, drop)
	require.ErrorIs(t, err, ethereum.NotFound, "the reorg must drop transaction %s", drop.Hex())
	receipt, err := chainClient.TransactionReceipt(ctx, replacement.Hash())
	require.NoError(t, err, "the reorg must include the replacement transaction %s", replacement.Hash().Hex())
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, receipt.Status, "the replacement transaction reverted")
	log.Infof("[%s] reorg of depth %d replaced tx %s with %s (block %d)",
		chain.Name(), proxyReorgDepth, drop.Hex(), replacement.Hash().Hex(), receipt.BlockNumber.Uint64())
	return receipt
}

// reorgBaseline reads the last reorg event aggkit reports for networkID: read it BEFORE provoking a reorg
// and hand it to requireReorgDetected.
func reorgBaseline(ctx context.Context, t *testing.T, bs *client.Client, networkID uint32) bridgesync.LastReorg {
	t.Helper()
	baseline, err := LastReorgEvent(ctx, bs, networkID)
	require.NoError(t, err)
	return baseline
}

// requireReorgDetected is the first assertion after every reorg: aggkit (asked directly, not through the
// proxy) must report a reorg event newer than baseline. If it does not, the failure is in the syncers or
// the reorg detector, and nothing can be concluded about the proxy.
func requireReorgDetected(
	ctx context.Context, t *testing.T, bs *client.Client, networkID uint32, baseline bridgesync.LastReorg,
) bridgesync.LastReorg {
	t.Helper()
	event, err := WaitForReorgEvent(ctx, bs, networkID, baseline, proxyReorgDetectTimeout)
	require.NoError(t, err,
		"aggkit did not report the reorg of network %d: a syncer / reorg detector problem, not a proxy one", networkID)
	log.Infof("aggkit reported the reorg of network %d: %+v", networkID, event)
	return event
}

// requireBridgeServed waits until the bridge service serves the bridge (networkID, depositCount) at the
// block and transaction of receipt, with block_finalized == finalized.
func requireBridgeServed(
	ctx context.Context, t *testing.T, bs *client.Client, networkID, depositCount uint32,
	receipt *ethtypes.Receipt, finalized bool,
) {
	t.Helper()
	require.NoError(t, WaitForBridgeStable(ctx, bs, networkID, depositCount,
		func(b *bridgeservicetypes.BridgeResponse) bool {
			return b.BlockNum == receipt.BlockNumber.Uint64() &&
				common.HexToHash(string(b.TxHash)) == receipt.TxHash &&
				b.BlockFinalized != nil && *b.BlockFinalized == finalized
		}, 0, proxyReorgServeTimeout),
		"aggkit must serve bridge (network %d, deposit_count %d) at block %d with block_finalized=%t",
		networkID, depositCount, receipt.BlockNumber.Uint64(), finalized)
}

// itemMatchesReceipt reports whether an activity item is the bridge mined by receipt.
func itemMatchesReceipt(item *proxyActivityItem, receipt *ethtypes.Receipt) bool {
	return item != nil && common.HexToHash(string(item.Bridge.TxHash)) == receipt.TxHash &&
		item.Bridge.BlockNum == receipt.BlockNumber.Uint64()
}

// l1ToL2Bridge is a native ETH bridge from L1 to network 1 sent by a fresh sender, the route the claim
// scenarios use: its claim and its GER injection happen on the L2 that aggkit-001 serves at LatestBlock.
type l1ToL2Bridge struct {
	sender       *proxyReorgSender
	receipt      *ethtypes.Receipt
	depositCount uint32
	// result is what sendClaimL1ToL2 needs to build the claim
	result *bridgeResult
}

// startL1ToL2Bridge funds a fresh sender on L1, bridges to destination on network 1 and waits until aggkit
// serves the bridge and its L1 info tree leaf (which needs the L1 block to be final). L1 must be mining. It
// does NOT wait for the GER injection on L2: see waitInjected and captureGERInjection.
func startL1ToL2Bridge(ctx context.Context, t *testing.T, destination common.Address) *l1ToL2Bridge {
	t.Helper()
	sender := newProxyReorgSender(t)
	funder := *testEnv.L1.Transactor
	sender.fundNative(ctx, t, testEnv.Clients.L1, &funder)
	return startL1ToL2BridgeFrom(ctx, t, sender, destination)
}

// startL1ToL2BridgeFrom is startL1ToL2Bridge for a sender that already has funds on L1 (a second bridge of
// the same sender).
func startL1ToL2BridgeFrom(
	ctx context.Context, t *testing.T, sender *proxyReorgSender, destination common.Address,
) *l1ToL2Bridge {
	t.Helper()
	opts := sender.opts(t, testEnv.L1.ChainID)
	opts.Context = ctx
	tx := sendL1Bridge(t, opts, testEnv.L2.NetworkID, destination, proxyReorgBridgeAmount)
	receipt := requireMined(ctx, t, testEnv.Clients.L1, tx, "L1 bridge")
	depositCount := l1DepositCount(t, receipt)
	bs := testEnv.L2.BridgeService

	var served *bridgeservicetypes.BridgeResponse
	state := "not polled yet"
	require.NoError(t, waitUntil(ctx, proxyReorgServeTimeout,
		fmt.Sprintf("aggkit to serve the L1 bridge deposit_count=%d", depositCount),
		func() (bool, error) {
			bridge, err := bs.GetBridgeByDepositCount(ctx, 0, depositCount)
			if err != nil {
				state = err.Error()
				return false, nil
			}
			served = bridge
			return common.HexToHash(string(bridge.TxHash)) == tx.Hash(), nil
		}, func() string { return state }))

	var l1InfoTreeIndex uint32
	require.NoError(t, waitUntil(ctx, proxyReorgConvergeTimeout,
		fmt.Sprintf("the L1 info tree leaf of the L1 bridge deposit_count=%d", depositCount),
		func() (bool, error) {
			index, err := bs.GetL1InfoTreeIndex(ctx, 0, int(depositCount))
			if err != nil {
				state = err.Error()
				return false, nil
			}
			l1InfoTreeIndex = index
			return true, nil
		}, func() string { return state }))
	log.Infof("L1->L2 bridge of %s: tx=%s deposit_count=%d l1_info_tree_index=%d",
		sender.addr.Hex(), tx.Hash().Hex(), depositCount, l1InfoTreeIndex)

	return &l1ToL2Bridge{
		sender: sender, receipt: receipt, depositCount: depositCount,
		result: &bridgeResult{
			Bridge: served, DepositCount: depositCount, L1InfoTreeIndex: l1InfoTreeIndex,
			GlobalIndex: served.GlobalIndex.ToBigInt(), DestinationAddr: destination,
			BridgeAmount: big.NewInt(proxyReorgBridgeAmount),
		},
	}
}

// injected reports whether aggkit serves a GER injected on network 1 that covers the bridge's leaf.
func (b *l1ToL2Bridge) injected(ctx context.Context) (bool, error) {
	_, err := testEnv.L2.BridgeService.GetInjectedL1InfoLeaf(ctx, int(testEnv.L2.NetworkID), int(b.result.L1InfoTreeIndex))
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, client.ErrNotFound):
		return false, nil
	default:
		return false, err
	}
}

// waitInjected waits until the aggoracle injected, on network 1, a GER that covers the bridge (the bridge
// is then ready to claim). The L2 must be mining.
func (b *l1ToL2Bridge) waitInjected(ctx context.Context, t *testing.T) {
	t.Helper()
	state := "not polled yet"
	require.NoError(t, waitUntil(ctx, proxyReorgConvergeTimeout,
		fmt.Sprintf("the GER of L1 info tree leaf %d to be injected on network 1", b.result.L1InfoTreeIndex),
		func() (bool, error) {
			ok, err := b.injected(ctx)
			if err != nil {
				state = err.Error()
				return false, nil
			}
			state = fmt.Sprintf("injected=%t", ok)
			return ok, nil
		}, func() string { return state }))
}

// waitForStatus waits until the proxy at baseURL lists the bridge with claim_status status (and extra
// accepting its item, when given) and returns the activity and the item.
func (b *l1ToL2Bridge) waitForStatus(
	ctx context.Context, t *testing.T, baseURL, status string, extra func(*proxyActivityItem) bool,
) (*proxyActivityResponse, *proxyActivityItem) {
	t.Helper()
	activity, err := waitForActivity(ctx, baseURL, b.sender.addr,
		fmt.Sprintf("L1 bridge deposit_count=%d with claim_status %s", b.depositCount, status), proxyReorgConvergeTimeout,
		func(a *proxyActivityResponse) bool {
			item := a.find(0, b.depositCount)
			return item != nil && item.ClaimStatus == status && (extra == nil || extra(item))
		})
	require.NoError(t, err)
	requireActivityWellFormed(t, activity)
	return activity, activity.find(0, b.depositCount)
}

// sendClaim sends the claim of the bridge on network 1 from claimer WITHOUT waiting for it to be mined.
func (b *l1ToL2Bridge) sendClaim(ctx context.Context, t *testing.T, claimer *bind.TransactOpts) *ethtypes.Transaction {
	t.Helper()
	opts := *claimer
	opts.Context = ctx
	tx, err := sendClaimL1ToL2(ctx, testEnv, &opts, b.result)
	require.NoError(t, err, "send the claim of L1 bridge deposit_count=%d", b.depositCount)
	return tx
}

// requireClaimServed waits until aggkit serves the claim of the bridge at the block and transaction of
// receipt, with block_finalized == finalized.
func (b *l1ToL2Bridge) requireClaimServed(ctx context.Context, t *testing.T, receipt *ethtypes.Receipt, finalized bool) {
	t.Helper()
	bs := testEnv.L2.BridgeService
	state := "not polled yet"
	require.NoError(t, waitUntil(ctx, proxyReorgServeTimeout,
		fmt.Sprintf("aggkit to serve the claim %s at block %d with block_finalized=%t",
			receipt.TxHash.Hex(), receipt.BlockNumber.Uint64(), finalized),
		func() (bool, error) {
			claims, err := bs.GetClaims(ctx, client.GetClaimsParams{NetworkID: testEnv.L2.NetworkID, GlobalIndex: b.result.GlobalIndex})
			if err != nil {
				state = err.Error()
				return false, nil
			}
			if len(claims.Claims) == 0 {
				state = "claim not served"
				return false, nil
			}
			claim := claims.Claims[0]
			state = fmt.Sprintf("claim served: tx=%s block=%d block_finalized=%s", claim.TxHash, claim.BlockNum,
				formatBoolPtr(claim.BlockFinalized))
			return common.HexToHash(string(claim.TxHash)) == receipt.TxHash && claim.BlockNum == receipt.BlockNumber.Uint64() &&
				claim.BlockFinalized != nil && *claim.BlockFinalized == finalized, nil
		}, func() string { return state }))
}

// claimIs reports whether item carries the claim record mined by receipt.
func claimIs(item *proxyActivityItem, receipt *ethtypes.Receipt) bool {
	return item.Claim != nil && common.HexToHash(string(item.Claim.TxHash)) == receipt.TxHash &&
		item.Claim.BlockNum == receipt.BlockNumber.Uint64()
}

// rpcBlockTxs is the part of an eth_getBlockByNumber answer (full transactions) the helpers read.
type rpcBlockTxs struct {
	Transactions []struct {
		Hash  common.Hash     `json:"hash"`
		To    *common.Address `json:"to"`
		Input hexutil.Bytes   `json:"input"`
	} `json:"transactions"`
}

// gerInjection is what captureGERInjection mined.
type gerInjection struct {
	// txs are the aggoracle's insertGlobalExitRoot transactions of the block (every one that was waiting in
	// the pool, so that a reorg can drop them all) and block the L2 block that holds them
	txs   []common.Hash
	block uint64
	// ger is the injected GER that covers the bridge, with the highest L1 info tree leaf, and leafIndex that
	// leaf: the one a claim proof is built against
	ger       common.Hash
	leafIndex uint32
}

// gerInjectionsOf returns the GERs of the insertGlobalExitRoot transactions of a block of the L2 (a number,
// or "pending" for the ones waiting in the pool), by transaction hash.
func gerInjectionsOf(
	ctx context.Context, l2 *envs.L2Config, gerAddr common.Address, block string,
) (map[common.Hash]common.Hash, error) {
	var pending rpcBlockTxs
	if err := l2.Client.Client().CallContext(ctx, &pending, "eth_getBlockByNumber", block, true); err != nil {
		return nil, fmt.Errorf("eth_getBlockByNumber %s: %w", block, err)
	}
	injections := make(map[common.Hash]common.Hash)
	for _, tx := range pending.Transactions {
		if tx.To == nil || *tx.To != gerAddr || len(tx.Input) != len(insertGERSelector)+common.HashLength ||
			!bytes.HasPrefix(tx.Input, insertGERSelector) {
			continue
		}
		injections[tx.Hash] = common.BytesToHash(tx.Input[len(insertGERSelector):])
	}
	return injections, nil
}

// captureGERInjection mines, by hand, the block in which the aggoracle injects on network 1 a GER that
// makes the bridge claimable, and returns it: the injection is then in the head block, not final, and a
// reorg can remove it. The L2 must be PAUSED since before the GER was created on L1 (so its injection
// cannot have been mined already) and L1 must be mining.
//
// The aggoracle sends insertGlobalExitRoot(ger) to the paused L2, where it waits in the pool. The GER of
// each waiting injection is looked up in the L1 info tree (a fact of L1, served by aggkit from final L1
// blocks): once one of them has a leaf that covers the bridge's, the pool is mined in one block. Every
// injection of that block is returned, covering or not, so that the reorg leaves none behind.
//
// aggkit does not serve an injection of the head block: its GER syncer processes a block once there is
// another one on top (measured on this env). Mine one more block and use waitInjected for that.
func captureGERInjection(ctx context.Context, t *testing.T, chain *AnvilChain, b *l1ToL2Bridge) gerInjection {
	t.Helper()
	l2 := &testEnv.L2
	gerAddr, err := l2.Contracts.L2Bridge.GlobalExitRootManager(&bind.CallOpts{Context: ctx})
	require.NoError(t, err)
	already, err := b.injected(ctx)
	require.NoError(t, err)
	require.False(t, already, "a GER covering the bridge is already injected: the L2 must be paused before it is created")

	var captured gerInjection
	state := "not polled yet"
	require.NoError(t, waitUntil(ctx, proxyReorgConvergeTimeout,
		fmt.Sprintf("the aggoracle to send the injection of a GER covering L1 info tree leaf %d", b.result.L1InfoTreeIndex),
		func() (bool, error) {
			pending, err := gerInjectionsOf(ctx, l2, gerAddr, "pending")
			if err != nil {
				return false, err
			}
			state = fmt.Sprintf("%d injection(s) in the pool, none covering the leaf", len(pending))
			captured = gerInjection{}
			covered := false
			for txHash, ger := range pending {
				captured.txs = append(captured.txs, txHash)
				leaf, err := l2.BridgeService.GetL1InfoTreeLeafByGER(ctx, ger.Hex())
				if err != nil {
					state = fmt.Sprintf("leaf of GER %s: %v", ger.Hex(), err)
					continue
				}
				if leaf.L1InfoTreeIndex >= b.result.L1InfoTreeIndex && (!covered || leaf.L1InfoTreeIndex > captured.leafIndex) {
					covered = true
					captured.ger, captured.leafIndex = ger, leaf.L1InfoTreeIndex
				}
			}
			return covered, nil
		}, func() string { return state }))

	require.NoError(t, chain.MineSpaced(ctx, 1, proxyReorgBlockSpacing))
	head, err := chain.Head(ctx)
	require.NoError(t, err)
	captured.block = head.Number
	// the injections are read from the block itself, not from the last look at the pool: one more may
	// have arrived before the block was mined
	mined, err := gerInjectionsOf(ctx, l2, gerAddr, hexutil.EncodeUint64(head.Number))
	require.NoError(t, err)
	captured.txs = captured.txs[:0]
	covering := false
	for txHash, ger := range mined {
		captured.txs = append(captured.txs, txHash)
		covering = covering || ger == captured.ger
	}
	require.True(t, covering, "the injection of GER %s is not in block %d", captured.ger.Hex(), head.Number)
	timestamp, err := l2.Contracts.GlobalExitRoot.GlobalExitRootMap(&bind.CallOpts{Context: ctx}, captured.ger)
	require.NoError(t, err)
	require.Positive(t, timestamp.Sign(), "GER %s must be in the L2 GER contract after the injection", captured.ger.Hex())
	log.Infof("[%s] injection of GER %s (L1 info tree leaf %d, covering leaf %d) mined at block %d (injections: %v)",
		chain.Name(), captured.ger.Hex(), captured.leafIndex, b.result.L1InfoTreeIndex, captured.block, captured.txs)
	return captured
}

// forceNewGER makes L1 produce a new GER (and L1 info tree leaf) with a bridge of a throwaway sender, and
// waits for the leaf. The aggoracle then has a GER to inject that covers every earlier bridge.
//
// It is how the scenarios recover a bridge whose GER injection a reorg removed. The aggoracle does not
// inject the same GER twice: its transaction manager keeps the first injection as "mined" and answers
// "inject GER transaction already exists in monitoring DB" (agglayer/aggkit#1992), so the bridge stays
// unclaimable until the next GER is injected. This works around #1992: once it is fixed, the scenarios
// can wait for the same GER to be injected again instead.
func forceNewGER(ctx context.Context, t *testing.T) {
	t.Helper()
	bridge := startL1ToL2Bridge(ctx, t, testEnv.L2.Transactor.From)
	log.Infof("new GER forced on L1 by bridge deposit_count=%d (L1 info tree leaf %d)",
		bridge.depositCount, bridge.result.L1InfoTreeIndex)
}

// sendClaimAtLeaf is sendClaim with the proof built against the L1 info tree leaf leafIndex, for a claim
// sent before aggkit serves the injection of that leaf's GER (the claim only needs the GER on chain).
func (b *l1ToL2Bridge) sendClaimAtLeaf(
	ctx context.Context, t *testing.T, claimer *bind.TransactOpts, leafIndex uint32,
) *ethtypes.Transaction {
	t.Helper()
	opts := *claimer
	opts.Context = ctx
	tx, err := sendClaimL1ToL2AtLeaf(ctx, testEnv, &opts, b.result, leafIndex)
	require.NoError(t, err, "send the claim of L1 bridge deposit_count=%d", b.depositCount)
	return tx
}

// String summarizes a tracker answer for logs and failure messages.
func (d *trackerTrackingData) String() string {
	step := "none"
	if d.StepIndex != nil && *d.StepIndex < len(d.AllSteps) {
		step = d.AllSteps[*d.StepIndex].StepName + "/" + d.AllSteps[*d.StepIndex].Status
	}
	bridge := "unresolved"
	if d.BridgeStatus != nil {
		bridge = fmt.Sprintf("block=%d block_finalized=%s", d.BridgeStatus.BlockNumber, formatBoolPtr(d.BridgeStatus.BlockFinalized))
	}
	failure := ""
	if d.Error != nil {
		failure = " error=" + strings.Join(d.Error.Description, "; ")
	}
	return fmt.Sprintf("tracking_status=%s claim_status=%s step=%s bridge={%s}%s",
		d.TrackingStatus, d.ClaimStatus, step, bridge, failure)
}

// currentStep returns the name of the step step_index points at, or "" while the bridge is unresolved.
func (d *trackerTrackingData) currentStep() string {
	if d.StepIndex == nil || *d.StepIndex >= len(d.AllSteps) {
		return ""
	}
	return d.AllSteps[*d.StepIndex].StepName
}

// waitForTracking polls GET /tracker/v1/network/{networkID}/tx/{txHash} on the proxy at baseURL (the first
// call registers the transaction) until match accepts the answer, and returns it.
func waitForTracking(
	ctx context.Context, t *testing.T, baseURL string, networkID uint32, txHash common.Hash, what string,
	match func(*trackerTrackingData) bool,
) *trackerTrackingData {
	t.Helper()
	var found *trackerTrackingData
	state := "not polled yet"
	require.NoError(t, waitUntil(ctx, proxyReorgConvergeTimeout,
		fmt.Sprintf("the tracking of tx %s (network %d) on %s: %s", txHash.Hex(), networkID, baseURL, what),
		func() (bool, error) {
			data, err := fetchTrackingDataFrom(ctx, baseURL, networkID, txHash)
			if err != nil {
				state = err.Error()
				return false, nil
			}
			state = data.String()
			if !match(data) {
				return false, nil
			}
			found = data
			return true, nil
		}, func() string { return state }))
	return found
}

// waitForProxyReady polls the health endpoint of the aggkit-proxy at baseURL until it answers 200 OK.
func waitForProxyReady(ctx context.Context, baseURL string) error {
	state := "not polled yet"
	return waitUntil(ctx, proxyReorgConvergeTimeout, "the aggkit-proxy at "+baseURL+" to be healthy",
		func() (bool, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/tracker/v1/health", nil)
			if err != nil {
				return false, err
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				state = err.Error()
				return false, nil
			}
			resp.Body.Close()
			state = resp.Status
			return resp.StatusCode == http.StatusOK, nil
		}, func() string { return state })
}

// trackerWSMessage mirrors the envelope of the tracker WebSocket messages; only "status" carries data the
// tests read.
type trackerWSMessage struct {
	Type string              `json:"type"`
	Data trackerTrackingData `json:"data"`
}

// trackerWS is a subscription to GET /tracker/v1/network/{id}/tx/{hash}/ws: a goroutine reads every message
// the server pushes into messages, and closes it when the connection ends (closeErr says how).
type trackerWS struct {
	conn     *websocket.Conn
	messages chan trackerWSMessage
	closeErr error
}

// dialTrackerWS subscribes to the tracking of (networkID, txHash) on the proxy at baseURL. The connection
// is closed when the test ends.
func dialTrackerWS(ctx context.Context, t *testing.T, baseURL string, networkID uint32, txHash common.Hash) *trackerWS {
	t.Helper()
	url := fmt.Sprintf("ws%s/tracker/v1/network/%d/tx/%s/ws", strings.TrimPrefix(baseURL, "http"), networkID, txHash.Hex())
	conn, resp, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	require.NoError(t, err, "subscribe to %s", url)
	resp.Body.Close()
	ws := &trackerWS{conn: conn, messages: make(chan trackerWSMessage, trackerWSBuffer)}
	done := make(chan struct{})
	t.Cleanup(func() {
		close(done) // releases the reader if nobody reads its messages anymore
		conn.Close()
	})
	go func() {
		defer close(ws.messages)
		for {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				ws.closeErr = err // read by the test only after messages is closed
				return
			}
			var message trackerWSMessage
			if err := json.Unmarshal(payload, &message); err != nil {
				ws.closeErr = fmt.Errorf("decode WebSocket message %s: %w", payload, err)
				return
			}
			select {
			case ws.messages <- message:
			case <-done:
				return
			}
		}
	}()
	return ws
}

// waitFor reads the pushed messages until match accepts one and returns its TrackingData. It fails if the
// server closes the connection, or nothing matching arrives within the convergence timeout.
func (ws *trackerWS) waitFor(t *testing.T, what string, match func(*trackerTrackingData) bool) *trackerTrackingData {
	t.Helper()
	timeout := time.NewTimer(proxyReorgConvergeTimeout)
	defer timeout.Stop()
	last := "no message received"
	for {
		select {
		case message, open := <-ws.messages:
			if !open { // closeErr is only read once the reader has closed the channel
				require.FailNow(t, "the WebSocket was closed", "(%v) while waiting for %s; last message: %s", ws.closeErr, what, last)
			}
			require.Equal(t, "status", message.Type)
			last = message.Data.String()
			log.Infof("WebSocket status: %s", last)
			if match(&message.Data) {
				return &message.Data
			}
		case <-timeout.C:
			require.FailNow(t, "timed out waiting for a WebSocket message", "%s; last message: %s", what, last)
		}
	}
}

// requireClosedNormally waits for the server to close the connection with code 1000, as it does once the
// tracking is final. Messages still buffered are discarded.
func (ws *trackerWS) requireClosedNormally(t *testing.T) {
	t.Helper()
	timeout := time.NewTimer(proxyReorgConvergeTimeout)
	defer timeout.Stop()
	for {
		select {
		case _, open := <-ws.messages:
			if !open {
				require.True(t, websocket.IsCloseError(ws.closeErr, websocket.CloseNormalClosure),
					"the WebSocket must be closed with code 1000, got: %v", ws.closeErr)
				return
			}
		case <-timeout.C:
			require.FailNow(t, "the server did not close the WebSocket of a final tracking")
		}
	}
}
