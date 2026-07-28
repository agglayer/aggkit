package bridgeservicefinder

import (
	"context"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	configtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/test/contracts/aggchainrollupmock"
	"github.com/agglayer/aggkit/test/contracts/rollupmanagermock"
	"github.com/agglayer/aggkit/test/helpers"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient/simulated"
	"github.com/stretchr/testify/require"
)

const (
	testChainID          = int64(1337)
	testBlockGasLimit    = uint64(999999999999999999)
	testPollInterval     = 30 * time.Millisecond
	testHealthTimeout    = 500 * time.Millisecond
	testBlockChunkSize   = uint64(1000)
	testEventuallyWait   = 10 * time.Second
	testEventuallyTick   = 20 * time.Millisecond
	testSeedTickMultiple = 4
)

// newTestBackend spins up a funded simulated backend and returns it along with a keyed
// TransactOpts for the funded account, mirroring test/contracts/bridgeservicefinder_mocks_test.go's
// newSmokeTestBackend pattern.
func newTestBackend(t *testing.T) (*simulated.Backend, *bind.TransactOpts) {
	t.Helper()

	pk, err := crypto.GenerateKey()
	require.NoError(t, err)

	auth, err := bind.NewKeyedTransactorWithChainID(pk, big.NewInt(testChainID))
	require.NoError(t, err)

	balance, ok := new(big.Int).SetString("100000000000000000000000000", 10)
	require.True(t, ok)

	backend := simulated.NewBackend(map[common.Address]types.Account{
		auth.From: {Balance: balance},
	}, simulated.WithBlockGasLimit(testBlockGasLimit))
	backend.Commit()

	return backend, auth
}

// testRollup bundles a deployed AggchainRollupMock instance with its address and rollupID, so
// tests can call its setters to control which sources are populated for that network.
type testRollup struct {
	rollupID uint32
	addr     common.Address
	contract *aggchainrollupmock.Aggchainrollupmock
}

// deployRollupManagerWithRollups deploys a RollupManagerMock and n AggchainRollupMock instances,
// registering rollup IDs 1..n on the manager via SetRollupContract. It returns the manager's
// address and a slice of testRollup handles indexed by rollupID-1.
func deployRollupManagerWithRollups(
	t *testing.T, backend *simulated.Backend, auth *bind.TransactOpts, n int,
) (common.Address, []testRollup) {
	t.Helper()

	client := backend.Client()

	mgrAddr, _, mgrContract, err := rollupmanagermock.DeployRollupmanagermock(auth, client)
	require.NoError(t, err)
	backend.Commit()

	rollups := make([]testRollup, 0, n)
	for i := 1; i <= n; i++ {
		rollupID := uint32(i)

		addr, _, contract, err := aggchainrollupmock.DeployAggchainrollupmock(auth, client)
		require.NoError(t, err)
		backend.Commit()

		_, err = mgrContract.SetRollupContract(auth, rollupID, addr)
		require.NoError(t, err)
		backend.Commit()

		rollups = append(rollups, testRollup{rollupID: rollupID, addr: addr, contract: contract})
	}

	return mgrAddr, rollups
}

// newRollupManagerContract re-binds an already-deployed RollupManagerMock so a test can drive its
// event-emitting setters (e.g. EmitCreateNewRollup) after the finder has started, exercising live
// rollup discovery.
func newRollupManagerContract(
	t *testing.T, backend *simulated.Backend, mgrAddr common.Address,
) *rollupmanagermock.Rollupmanagermock {
	t.Helper()

	c, err := rollupmanagermock.NewRollupmanagermock(mgrAddr, backend.Client())
	require.NoError(t, err)

	return c
}

// deployStandaloneRollup deploys an AggchainRollupMock WITHOUT registering it on any manager, for
// tests that attach it to the manager later via a lifecycle event to exercise live discovery. The
// returned handle carries the rollupID the test intends to announce it under.
func deployStandaloneRollup(
	t *testing.T, backend *simulated.Backend, auth *bind.TransactOpts, rollupID uint32,
) testRollup {
	t.Helper()

	addr, _, contract, err := aggchainrollupmock.DeployAggchainrollupmock(auth, backend.Client())
	require.NoError(t, err)
	backend.Commit()

	return testRollup{rollupID: rollupID, addr: addr, contract: contract}
}

// newTestEthClient builds an aggkittypes.BaseEthereumClienter (also satisfying
// bridgeservicefinder.LogFilterer) backed by the given simulated backend, reusing
// test/helpers.TestClient rather than reinventing the wrapper.
func newTestEthClient(backend *simulated.Backend) *helpers.TestClient {
	return helpers.NewTestClient(backend.Client())
}

// baseTestConfig returns a Config with small, deterministic timings suitable for fast tests: a
// short poll interval, small health-check timeout, and a small block chunk size.
//
// BlockFinality is explicitly set to LatestBlock rather than left at the package default
// (FinalizedBlock). On go-ethereum's simulated backend (github.com/ethereum/go-ethereum@this
// module's pinned version), the "finalized" and "safe" RPC tags stay pinned at the genesis block
// and do NOT advance as backend.Commit() mines new blocks - only "latest" does. Using LatestBlock
// here is what makes backend.Commit() immediately visible to the listener's finalizedUpperBound on
// its next tick, which is what the live-update tests rely on.
func baseTestConfig(rollupManagerAddr common.Address) Config {
	return Config{
		RollupManagerAddr:        rollupManagerAddr,
		URLs:                     map[uint32]string{},
		BlockFinality:            aggkittypes.LatestBlock,
		PollInterval:             configtypes.Duration{Duration: testPollInterval},
		BlockChunkSize:           testBlockChunkSize,
		HealthCheckTimeout:       configtypes.Duration{Duration: testHealthTimeout},
		RequireAllHealthyOnStart: false,
	}
}

// atomicHealthServer is an httptest.NewServer-backed fake bridge service exposing a controllable
// /health endpoint. Flip Healthy to change the status code returned to subsequent probes.
type atomicHealthServer struct {
	Healthy atomic.Bool
	Server  *httptest.Server
}

// newHealthServer starts an httptest server whose /health (or cfg default path) handler returns
// 200 while healthy is true (the initial value) and 503 otherwise.
func newHealthServer(t *testing.T, initialHealthy bool) *atomicHealthServer {
	t.Helper()

	h := &atomicHealthServer{}
	h.Healthy.Store(initialHealthy)

	h.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if h.Healthy.Load() {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))

	t.Cleanup(h.Server.Close)

	return h
}

// closedServerURL starts and immediately closes an httptest server, returning a URL that is
// guaranteed to be unreachable (connection refused) for use as a "dead service" test fixture.
func closedServerURL(t *testing.T) string {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	return url
}

// mapHealthChecker is a simple HealthChecker test double keyed by exact baseURL, for tests that
// prefer to avoid juggling multiple httptest servers. Unlisted URLs are considered unhealthy.
type mapHealthChecker struct {
	healthy map[string]bool
}

func newMapHealthChecker(healthy map[string]bool) *mapHealthChecker {
	return &mapHealthChecker{healthy: healthy}
}

func (m *mapHealthChecker) IsHealthy(_ context.Context, baseURL string) bool {
	return m.healthy[baseURL]
}

// sleepPastSeedTick sleeps long enough for the listener's first ("seeding") tick to have already
// happened, so a subsequently emitted on-chain event will be picked up by a later tick rather than
// being folded into the seed. See listener.go's scanOnce seeding behaviour.
func sleepPastSeedTick(pollInterval time.Duration) {
	time.Sleep(testSeedTickMultiple * pollInterval)
}

// testLogger returns a quiet logger for tests.
func testLogger() *log.Logger {
	return log.WithFields("module", moduleName+"_test")
}

// dummyLog returns a zero-value types.Log suitable for passing to listener.applyUpdate in tests
// that exercise the priority/health-gating logic directly without a real on-chain log.
func dummyLog() types.Log {
	return types.Log{}
}
