package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgeservice/client"
	"github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/test/e2e/envs"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/stretchr/testify/require"
)

// fetchBridgeServiceConfig performs a raw GET against baseURL's GET /bridge/v1/config and decodes
// the body as a types.PublicConfigResponse. There is no bridgeservice/client wrapper for this
// endpoint today, so this mirrors fetchBridgeServiceHealth's direct-HTTP approach (health_test.go).
func fetchBridgeServiceConfig(ctx context.Context, baseURL string) (*types.PublicConfigResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/bridge/v1/config", nil)
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
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"GET %s/bridge/v1/config returned %s: %s", baseURL, resp.Status, strings.TrimSpace(string(body)),
		)
	}
	var cfg types.PublicConfigResponse
	if err := json.Unmarshal(body, &cfg); err != nil {
		return nil, fmt.Errorf("decode config response %q: %w", strings.TrimSpace(string(body)), err)
	}
	return &cfg, nil
}

// syncerPresence records, for one bridge-service instance, which of the four syncers that GET
// /bridge/v1/config actually reports are configured on it. The two claim syncers are not part of
// that response (see TestBridgeServiceSyncStatusAllSyncers's doc comment), so they are not
// tracked here: this test hardcodes them as always present instead.
type syncerPresence struct {
	l1, l2, l2GER, l1InfoTree bool
}

// fetchSyncerPresence reads GET /bridge/v1/config on baseURL and reports which syncers it says
// are configured.
func fetchSyncerPresence(ctx context.Context, baseURL string) (syncerPresence, error) {
	cfg, err := fetchBridgeServiceConfig(ctx, baseURL)
	if err != nil {
		return syncerPresence{}, err
	}
	return syncerPresence{
		l1:         cfg.Components.BridgeL1Sync != nil,
		l2:         cfg.Components.BridgeL2Sync != nil,
		l2GER:      cfg.Components.L2GERSync != nil,
		l1InfoTree: cfg.Components.L1InfoTreeSync != nil,
	}, nil
}

// syncStatusTarget is one bridge-service instance to exercise: its REST client (used for
// GET /bridge/v1/sync-status and GET /health, which bridgeservice/client already wraps) and its
// raw base URL (used for GET /bridge/v1/config, which it doesn't).
type syncStatusTarget struct {
	label   string
	client  *client.Client
	baseURL string
}

// syncStatusTargets returns every bridge-service instance to exercise: the primary network (L2A,
// served by aggkit-001) always, plus the secondary network (L2B) when the env is multi-chain --
// the same condition Env.CheckEnv (test/e2e/envs/checks.go) uses to decide whether to also
// validate L2B.
func syncStatusTargets(env *envs.Env) []syncStatusTarget {
	targets := []syncStatusTarget{
		{label: "L2A", client: env.Clients.BridgeService, baseURL: bridgeServiceBaseURL},
	}
	if env.L2B != nil {
		targets = append(targets, syncStatusTarget{
			label:   "L2B",
			client:  env.L2B.BridgeService,
			baseURL: env.L2B.BridgeServiceURL,
		})
	}
	return targets
}

// networkSyncInfoHealthy reports whether a legacy NetworkSyncInfo entry (l1_info/l2_info) matches
// the expected running-vs-not-configured shape: active, not halted and error-free when
// configured; {is_active:false,is_halted:false,error:""} (never omitted) when not.
func networkSyncInfoHealthy(info *types.NetworkSyncInfo, configured bool) bool {
	if info == nil {
		return false
	}
	if !configured {
		return !info.IsActive && !info.IsHalted && info.Error == ""
	}
	return info.IsActive && !info.IsHalted && info.Error == ""
}

// l2gerFields extracts the fields this test cares about from an L2GERSyncInfo entry, treating a
// nil (not configured) entry as inactive/not-halted/error-free.
func l2gerFields(info *types.L2GERSyncInfo) (active, halted bool, errStr string) {
	if info == nil {
		return false, false, ""
	}
	return info.IsActive, info.IsHalted, info.Error
}

// l2GERSyncInfoHealthy is l2_ger_info's equivalent of networkSyncInfoHealthy: like l1_info/
// l2_info, it is a legacy entry that is never omitted, so "not configured" is the zero-valued
// {is_active:false,is_halted:false,error:""} shape, not a nil/absent entry.
func l2GERSyncInfoHealthy(info *types.L2GERSyncInfo, configured bool) bool {
	if info == nil {
		return false
	}
	active, halted, errStr := l2gerFields(info)
	if !configured {
		return !active && !halted && errStr == ""
	}
	return active && !halted && errStr == ""
}

// syncerFields extracts the fields this test cares about from a SyncerSyncInfo entry
// (l1_info_tree_info/claim_l1_info/claim_l2_info), treating a nil (not configured) entry as
// inactive/not-halted/error-free.
func syncerFields(info *types.SyncerSyncInfo) (active, halted bool, errStr string) {
	if info == nil {
		return false, false, ""
	}
	return info.IsActive, info.IsHalted, info.Error
}

// syncerSyncInfoHealthy is the SyncerSyncInfo equivalent of l2GERSyncInfoHealthy: omitted when
// not configured, active/not-halted/error-free when configured.
func syncerSyncInfoHealthy(info *types.SyncerSyncInfo, configured bool) bool {
	if !configured {
		return info == nil
	}
	active, halted, errStr := syncerFields(info)
	return info != nil && active && !halted && errStr == ""
}

// syncStatusEntriesHealthy reports whether every entry of status matches the running-vs-not-
// configured shape implied by presence, with the two claim syncers hardcoded as configured (see
// TestBridgeServiceSyncStatusAllSyncers's doc comment).
func syncStatusEntriesHealthy(status *types.SyncStatus, presence syncerPresence) bool {
	if !networkSyncInfoHealthy(status.L1Info, presence.l1) || !networkSyncInfoHealthy(status.L2Info, presence.l2) {
		return false
	}
	if !l2GERSyncInfoHealthy(status.L2GERInfo, presence.l2GER) {
		return false
	}
	if !syncerSyncInfoHealthy(status.L1InfoTreeInfo, presence.l1InfoTree) {
		return false
	}
	return syncerSyncInfoHealthy(status.ClaimL1Info, true) && syncerSyncInfoHealthy(status.ClaimL2Info, true)
}

// requireNetworkSyncInfoRunning asserts a legacy NetworkSyncInfo entry's shape with clear failure
// messages, mirroring networkSyncInfoHealthy's rule.
func requireNetworkSyncInfoRunning(t *testing.T, label, field string, info *types.NetworkSyncInfo, configured bool) {
	t.Helper()
	require.NotNil(t, info, "%s: %s must always be present", label, field)
	require.Equal(t, configured, info.IsActive, "%s: %s.is_active", label, field)
	require.False(t, info.IsHalted, "%s: %s.is_halted", label, field)
	require.Empty(t, info.Error, "%s: %s.error", label, field)
}

// requireL2GERSyncInfoRunning asserts l2_ger_info's shape with clear failure messages, mirroring
// l2GERSyncInfoHealthy's rule: like l1_info/l2_info, this entry is never omitted, so "not
// configured" means the zero-valued {is_active:false,is_halted:false} shape, not a nil entry.
func requireL2GERSyncInfoRunning(t *testing.T, label string, info *types.L2GERSyncInfo, configured bool) {
	t.Helper()
	require.NotNil(t, info, "%s: l2_ger_info must always be present", label)
	require.Equal(t, configured, info.IsActive, "%s: l2_ger_info.is_active", label)
	require.False(t, info.IsHalted, "%s: l2_ger_info.is_halted", label)
	require.Empty(t, info.Error, "%s: l2_ger_info.error", label)
}

// requireSyncerSyncInfoRunning asserts a SyncerSyncInfo entry's shape with clear failure
// messages, mirroring syncerSyncInfoHealthy's rule.
func requireSyncerSyncInfoRunning(t *testing.T, label, field string, info *types.SyncerSyncInfo, configured bool) {
	t.Helper()
	if !configured {
		require.Nil(t, info, "%s: %s must be omitted when its syncer is not configured", label, field)
		return
	}
	require.NotNil(t, info, "%s: %s must be present when its syncer is configured", label, field)
	require.True(t, info.IsActive, "%s: %s.is_active", label, field)
	require.False(t, info.IsHalted, "%s: %s.is_halted", label, field)
	require.Empty(t, info.Error, "%s: %s.error", label, field)
}

// componentMismatch reports whether a /health details entry disagrees with the is_active/
// is_halted a configured syncer's /sync-status entry has, or is present/absent against what
// configured says it should be.
func componentMismatch(health *types.ComponentHealth, wantActive, wantHalted, configured bool) bool {
	if !configured {
		return health != nil
	}
	return health == nil || health.IsActive != wantActive || health.IsHalted != wantHalted
}

// healthMatchesSyncStatus reports whether every /health details entry's is_active/is_halted
// agrees with the matching /bridge/v1/sync-status entry already fetched into status, given which
// syncers presence says are configured (claim syncers hardcoded configured).
func healthMatchesSyncStatus(details types.HealthCheckDetails, status *types.SyncStatus, presence syncerPresence) bool {
	if componentMismatch(details.L1, status.L1Info.IsActive, status.L1Info.IsHalted, presence.l1) {
		return false
	}
	if componentMismatch(details.L2, status.L2Info.IsActive, status.L2Info.IsHalted, presence.l2) {
		return false
	}
	l2gerActive, l2gerHalted, _ := l2gerFields(status.L2GERInfo)
	if componentMismatch(details.L2GER, l2gerActive, l2gerHalted, presence.l2GER) {
		return false
	}
	l1itActive, l1itHalted, _ := syncerFields(status.L1InfoTreeInfo)
	if componentMismatch(details.L1InfoTree, l1itActive, l1itHalted, presence.l1InfoTree) {
		return false
	}
	claimL1Active, claimL1Halted, _ := syncerFields(status.ClaimL1Info)
	if componentMismatch(details.ClaimL1, claimL1Active, claimL1Halted, true) {
		return false
	}
	claimL2Active, claimL2Halted, _ := syncerFields(status.ClaimL2Info)
	return !componentMismatch(details.ClaimL2, claimL2Active, claimL2Halted, true)
}

// requireComponentMatchesEntry asserts one /health details entry against the matching
// /sync-status entry's fields, with clear failure messages.
func requireComponentMatchesEntry(
	t *testing.T, label, key string, health *types.ComponentHealth,
	wantActive, wantHalted bool, wantErr string, configured bool,
) {
	t.Helper()
	if !configured {
		require.Nil(t, health, "%s: health details.%s must be omitted when not configured", label, key)
		return
	}
	require.NotNil(t, health, "%s: health details.%s must be present when configured", label, key)
	require.Equal(t, wantActive, health.IsActive, "%s: details.%s.is_active vs sync-status", label, key)
	require.Equal(t, wantHalted, health.IsHalted, "%s: details.%s.is_halted vs sync-status", label, key)
	require.Equal(t, wantErr, health.Error, "%s: details.%s.error vs sync-status", label, key)
}

// checkHealthConsistency polls GET /health until its details agree with the already-settled
// status's per-entry is_active/is_halted (allowing for the health-check cache TTL to expire), then
// asserts the agreement explicitly and that sync_status never reports "error".
func checkHealthConsistency(
	t *testing.T, ctx context.Context, target syncStatusTarget, presence syncerPresence, status *types.SyncStatus,
) {
	t.Helper()

	var health *types.HealthCheckResponse
	err := pollWithBackoff(ctx, 30*time.Second, backoffInitial, backoffMax, target.label+"/health",
		func() (bool, error) {
			h, ferr := target.client.HealthCheck(ctx)
			if ferr != nil {
				return false, nil //nolint:nilerr // transient, keep polling
			}
			if !healthMatchesSyncStatus(h.Details, status, presence) {
				return false, nil
			}
			health = h
			return true, nil
		})
	require.NoError(t, err, "%s: /health never converged with /bridge/v1/sync-status", target.label)

	require.Contains(t,
		[]types.HealthSyncStatus{types.HealthSyncStatusDone, types.HealthSyncStatusPending},
		health.SyncStatus, "%s: /health sync_status must never be \"error\" in a healthy env", target.label,
	)

	l2gerActive, l2gerHalted, l2gerErr := l2gerFields(status.L2GERInfo)
	l1itActive, l1itHalted, l1itErr := syncerFields(status.L1InfoTreeInfo)
	claimL1Active, claimL1Halted, claimL1Err := syncerFields(status.ClaimL1Info)
	claimL2Active, claimL2Halted, claimL2Err := syncerFields(status.ClaimL2Info)

	requireComponentMatchesEntry(t, target.label, "l1", health.Details.L1,
		status.L1Info.IsActive, status.L1Info.IsHalted, status.L1Info.Error, presence.l1)
	requireComponentMatchesEntry(t, target.label, "l2", health.Details.L2,
		status.L2Info.IsActive, status.L2Info.IsHalted, status.L2Info.Error, presence.l2)
	requireComponentMatchesEntry(t, target.label, "l2_ger", health.Details.L2GER,
		l2gerActive, l2gerHalted, l2gerErr, presence.l2GER)
	requireComponentMatchesEntry(t, target.label, "l1_info_tree", health.Details.L1InfoTree,
		l1itActive, l1itHalted, l1itErr, presence.l1InfoTree)
	requireComponentMatchesEntry(t, target.label, "claim_l1", health.Details.ClaimL1,
		claimL1Active, claimL1Halted, claimL1Err, true)
	requireComponentMatchesEntry(t, target.label, "claim_l2", health.Details.ClaimL2,
		claimL2Active, claimL2Halted, claimL2Err, true)
}

// checkSyncStatusAllSyncers exercises one bridge-service instance (target): it learns which of
// the four config-visible syncers are configured, polls /bridge/v1/sync-status until every entry
// is in the expected running/absent shape and the last_processed_block values that must advance
// (per before, the snapshot taken before the test forced new chain activity) have, then asserts
// that explicitly, and finally checks /health agrees.
func checkSyncStatusAllSyncers(t *testing.T, ctx context.Context, target syncStatusTarget, before *types.SyncStatus) {
	t.Helper()

	presence, err := fetchSyncerPresence(ctx, target.baseURL)
	require.NoError(t, err, "%s: GET /bridge/v1/config", target.label)
	if presence.l1InfoTree {
		require.NotNil(t, before.L1InfoTreeInfo, "%s: l1_info_tree_info missing from the pre-forcing snapshot", target.label)
	}

	var status *types.SyncStatus
	err = pollWithBackoff(ctx, 3*time.Minute, backoffInitial, backoffMax, target.label+"/sync-status",
		func() (bool, error) {
			s, ferr := target.client.GetSyncStatus(ctx)
			if ferr != nil {
				return false, nil //nolint:nilerr // transient, keep polling
			}
			if !syncStatusEntriesHealthy(s, presence) {
				return false, nil
			}
			if presence.l1InfoTree && s.L1InfoTreeInfo.LastProcessedBlock <= before.L1InfoTreeInfo.LastProcessedBlock {
				return false, nil
			}
			if s.ClaimL1Info.LastProcessedBlock <= before.ClaimL1Info.LastProcessedBlock {
				return false, nil
			}
			if s.ClaimL2Info.LastProcessedBlock <= before.ClaimL2Info.LastProcessedBlock {
				return false, nil
			}
			status = s
			return true, nil
		})
	require.NoError(t, err, "%s: sync-status never reached the expected, fully-advanced, error-free state", target.label)

	// Legacy entries: present+healthy when configured, {is_active:false,is_halted:false} (never
	// omitted) when not.
	requireNetworkSyncInfoRunning(t, target.label, "l1_info", status.L1Info, presence.l1)
	requireNetworkSyncInfoRunning(t, target.label, "l2_info", status.L2Info, presence.l2)
	requireL2GERSyncInfoRunning(t, target.label, status.L2GERInfo, presence.l2GER)

	// l1infotreesync: present+healthy when configured (omitted otherwise), and its
	// last_processed_block must be non-zero and have strictly advanced past the pre-forcing
	// snapshot.
	requireSyncerSyncInfoRunning(t, target.label, "l1_info_tree_info", status.L1InfoTreeInfo, presence.l1InfoTree)
	if presence.l1InfoTree {
		require.Greater(t, status.L1InfoTreeInfo.LastProcessedBlock, uint64(0),
			"%s: l1_info_tree_info.last_processed_block", target.label)
		require.Greater(t, status.L1InfoTreeInfo.LastProcessedBlock, before.L1InfoTreeInfo.LastProcessedBlock,
			"%s: l1_info_tree_info.last_processed_block did not advance", target.label)
	}

	// Claim syncers are hardcoded present -- see TestBridgeServiceSyncStatusAllSyncers's doc
	// comment for why GET /bridge/v1/config can't be used to learn this instead.
	requireSyncerSyncInfoRunning(t, target.label, "claim_l1_info", status.ClaimL1Info, true)
	requireSyncerSyncInfoRunning(t, target.label, "claim_l2_info", status.ClaimL2Info, true)
	require.Greater(t, status.ClaimL1Info.LastProcessedBlock, uint64(0),
		"%s: claim_l1_info.last_processed_block", target.label)
	require.Greater(t, status.ClaimL2Info.LastProcessedBlock, uint64(0),
		"%s: claim_l2_info.last_processed_block", target.label)

	// claimsync's processor inserts a block row for every processed block, whether or not it
	// carried a claim event: ProcessBlock (claimsync/processor.go) calls storage.InsertBlock
	// unconditionally, before it ever looks at block.Events, and the downloader driving it
	// (sync/evmdownloader.go) reports and processes block ranges with no matching logs the same
	// way a range with matches gets processed. So claim_l1_info/claim_l2_info's
	// last_processed_block advances on every new block of the chain each one tracks, exactly like
	// l1infotreesync, not just when an actual claim happens. claim_l1 tracks the shared L1 chain,
	// which the forcing step below always advances; claim_l2 tracks each instance's own L2 chain,
	// which the forcing step advances directly with a trivial per-target transaction. Both are
	// therefore asserted to strictly advance on every target.
	require.Greater(t, status.ClaimL1Info.LastProcessedBlock, before.ClaimL1Info.LastProcessedBlock,
		"%s: claim_l1_info.last_processed_block did not advance", target.label)
	require.Greater(t, status.ClaimL2Info.LastProcessedBlock, before.ClaimL2Info.LastProcessedBlock,
		"%s: claim_l2_info.last_processed_block did not advance", target.label)

	checkHealthConsistency(t, ctx, target, presence, status)
}

// TestBridgeServiceSyncStatusAllSyncers exercises issue #1861 end to end: every syncer backing a
// bridge-service instance -- not just the two bridge syncers -- must show up on
// GET /bridge/v1/sync-status and GET /health, with is_active/is_halted telling a configured,
// running syncer apart from one that simply isn't configured on this instance.
//
// GET /bridge/v1/config's components only reports four of the six syncers (l1infotreesync, bridge
// L1, bridge L2, l2gersync); it has no fields for the two claim syncers (a pre-existing gap, out
// of scope here). So this test hardcodes that claim_l1_info/claim_l2_info must be present instead
// of inferring it from /config: every e2e bridge-service node runs the "bridge" component (see the
// aggkit command lines in test/e2e/envs/*/docker-compose.yml), and cmd/run.go's
// runClaimSyncL1IfNeeded and runClaimSyncL2IfNeeded both start their claim syncer whenever "bridge"
// is among the running components.
//
// The halted path is not exercised here: there is no way to induce a real syncer halt from
// outside the docker-compose env. It is covered in-process instead, by
// TestBridgeServiceReportsL1InfoTreeHalt (l1infotreesync package) against a real, non-mocked
// l1infotreesync processor, and by the bridgeservice unit test matrix (TestSyncStatusAllSyncers,
// bridgeservice/bridge_test.go) against every syncer, mocked.
func TestBridgeServiceSyncStatusAllSyncers(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	require.NotNil(t, testEnv, "testEnv must be set by TestMain")

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	targets := syncStatusTargets(testEnv)

	// Snapshot l1infotreesync/claimsync progress on every target before forcing any new chain
	// activity below, so the checks can prove last_processed_block actually advances rather than
	// merely being non-zero (it may already be non-zero from earlier test activity in the shared
	// e2e suite run). The snapshot itself is polled until every entry is in its healthy,
	// error-free shape: taking it at a moment when an entry carries a transient error would leave
	// that entry's last_processed_block at 0, making the later strict-advance check below pass
	// trivially instead of proving anything.
	before := make(map[string]*types.SyncStatus, len(targets))
	for _, target := range targets {
		presence, err := fetchSyncerPresence(ctx, target.baseURL)
		require.NoError(t, err, "%s: GET /bridge/v1/config", target.label)

		var status *types.SyncStatus
		err = pollWithBackoff(ctx, 3*time.Minute, backoffInitial, backoffMax, target.label+"/sync-status (before)",
			func() (bool, error) {
				s, ferr := target.client.GetSyncStatus(ctx)
				if ferr != nil {
					return false, nil //nolint:nilerr // transient, keep polling
				}
				if !syncStatusEntriesHealthy(s, presence) {
					return false, nil
				}
				status = s
				return true, nil
			})
		require.NoError(t, err, "%s: pre-forcing sync-status snapshot never reached a healthy, error-free state",
			target.label)
		// Fail clearly here, rather than with a nil-pointer panic further down, if the hardcoded
		// "claim syncers are always present" assumption (see the test's doc comment) ever turns
		// out to be wrong for this env.
		require.NotNil(t, status.ClaimL1Info, "%s: claim_l1_info must be present", target.label)
		require.NotNil(t, status.ClaimL2Info, "%s: claim_l2_info must be present", target.label)
		before[target.label] = status
	}

	// Force new blocks on the shared L1 chain -- every target's l1infotreesync and claim_l1 track
	// L1 regardless of which L2 network they otherwise belong to -- and, via the aggoracle's
	// GER-injection transaction on the destination network, on L2A's own chain too. This lets the
	// per-target checks below prove strict advancement for L2A's l1infotreesync/claim_l1/claim_l2
	// and for L2B's l1infotreesync/claim_l1.
	l1Opts, l1Key, err := testEnv.Keys.L1Keys.Checkout()
	require.NoError(t, err)
	defer testEnv.Keys.L1Keys.Return(l1Key)
	l2Opts, l2Key, err := testEnv.Keys.L2Keys.Checkout()
	require.NoError(t, err)
	defer testEnv.Keys.L2Keys.Return(l2Key)
	_, err = BridgeL1NoClaim(ctx, testEnv, l1Opts, l2Opts, big.NewInt(1e14), "sync-status-progress")
	require.NoError(t, err, "force an L1->L2A bridge to advance l1infotreesync/claimsync")

	// Force a new block on L2B's own chain too, with a trivial mint (0 value would still spend gas
	// and mine a block; the amount itself is irrelevant here): nothing above touches L2B's chain,
	// and claim_l2 only advances when its own tracked chain produces a new block.
	if testEnv.L2B != nil {
		l2bOpts := *testEnv.L2B.Transactor
		mintTx, err := testEnv.L2B.Contracts.MintableERC20.Mint(&l2bOpts, l2bOpts.From, big.NewInt(1))
		require.NoError(t, err, "force a trivial L2B tx to advance claim_l2_info")
		_, err = bind.WaitMined(ctx, testEnv.L2B.Client, mintTx)
		require.NoError(t, err, "wait for the trivial L2B tx to be mined")
	}

	for _, target := range targets {
		t.Run(target.label, func(t *testing.T) {
			checkSyncStatusAllSyncers(t, ctx, target, before[target.label])
		})
	}
}
