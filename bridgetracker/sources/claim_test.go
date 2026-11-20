package sources

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgeservice/client"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/bridgetracker"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	domainmocks "github.com/agglayer/aggkit/bridgetracker/domain/mocks"
	finalitymocks "github.com/agglayer/aggkit/bridgetracker/finality/mocks"
	trackermocks "github.com/agglayer/aggkit/bridgetracker/mocks"
	trackertypes "github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	// mainnetBridgeGlobalIndex is the global index of the bridge of deposit count 7 sent from
	// mainnet: mainnet flag (bit 64) + deposit count, 2^64 + 7
	mainnetBridgeGlobalIndex = "18446744073709551623"
	// rollup3BridgeGlobalIndex is the global index of the bridge of deposit count 7 sent from
	// network 3 (rollup index 2): 2 << 32 + 7
	rollup3BridgeGlobalIndex = "8589934599"
)

// claimJSON is a claim of the bridge service claims endpoint. globalIndex is the raw JSON of its
// global_index (a quoted string, a bare number, null...)
func claimJSON(globalIndex string, destinationNetwork uint32, txHash string, blockNum uint64) string {
	return fmt.Sprintf(`{"tx_hash":%q,"block_num":%d,"block_timestamp":1700000000,`+
		`"global_index":%s,"destination_network":%d,"origin_network":9}`,
		txHash, blockNum, globalIndex, destinationNetwork)
}

// quoted is s as the raw JSON of a string
func quoted(s string) string { return `"` + s + `"` }

// claimsBody is the claims endpoint response listing the raw JSON entries claims (a claim, null),
// reporting count of them
func claimsBody(count int, claims ...string) string {
	return fmt.Sprintf(`{"claims":[%s],"count":%d}`, strings.Join(claims, ","), count)
}

// claimBridge is a bridge of deposit count 7 sent from originNetwork to destinationNetwork. Its
// OriginNetwork (the bridged token's) is deliberately another network
func claimBridge(originNetwork, destinationNetwork uint32) *bridgetracker.BridgeInfo {
	bridge := l1ToL2Bridge()
	bridge.NetworkID = originNetwork
	bridge.DestinationNetwork = destinationNetwork
	bridge.OriginNetwork = 9
	return bridge
}

// requireInvalidResponse asserts err rejects the response as invalid, which is transient for the
// tracker (never permanent: the step retries it)
func requireInvalidResponse(t *testing.T, err error) {
	t.Helper()
	require.ErrorIs(t, err, client.ErrInvalidResponse)
	require.False(t, domain.IsPermanent(err), "an invalid response is retried, never terminal")
}

// invalidClaimsBodies are claims responses that are not the claim of claimBridge(0, 2)
func invalidClaimsBodies() map[string]string {
	return map[string]string{
		"empty list with count 1": claimsBody(1),
		"null claim":              claimsBody(1, "null"),
		"another global index":    claimsBody(1, claimJSON(quoted("999"), 2, "0x0e", 60)),
	}
}

// TestClaimSource_ClaimFor_ValidatesTheResponse feeds ClaimFor, through the real client, the
// claims responses a bridge service can answer with
func TestClaimSource_ClaimFor_ValidatesTheResponse(t *testing.T) {
	const destination = uint32(2)
	bridge := claimBridge(0, destination)
	right := claimJSON(quoted(mainnetBridgeGlobalIndex), destination, "0x0d", 50)
	other := claimJSON(quoted("999"), destination, "0x0e", 60)

	tests := []struct {
		name string
		body string
		// wantTx is the claim tx expected, zero for no claim
		wantTx common.Hash
		// wantInvalid is whether the response must be rejected as invalid
		wantInvalid bool
	}{
		{name: "valid claim", body: claimsBody(1, right), wantTx: common.HexToHash("0x0d")},
		{name: "no claim", body: claimsBody(0)},
		{name: "no claim, null list", body: `{"claims":null,"count":0}`},
		{name: "empty list with count 1", body: claimsBody(1), wantInvalid: true},
		{name: "null claim", body: claimsBody(1, "null"), wantInvalid: true},
		{name: "null claim after a valid one", body: claimsBody(2, right, "null"), wantInvalid: true},
		{name: "null claim before a valid one", body: claimsBody(2, "null", right), wantInvalid: true},
		{name: "global index of another claim", body: claimsBody(1, other), wantInvalid: true},
		{name: "requested claim is not the first", body: claimsBody(2, other, right), wantTx: common.HexToHash("0x0d")},
		{name: "count is not trusted", body: claimsBody(0, right), wantTx: common.HexToHash("0x0d")},
		{
			name:   "global index as a bare JSON number",
			body:   claimsBody(1, claimJSON(mainnetBridgeGlobalIndex, destination, "0x0d", 50)),
			wantTx: common.HexToHash("0x0d"),
		},
		{
			name:   "global index in hex",
			body:   claimsBody(1, claimJSON(quoted("0x10000000000000007"), destination, "0x0d", 50)),
			wantTx: common.HexToHash("0x0d"),
		},
		{
			name:        "right global index on another destination network",
			body:        claimsBody(1, claimJSON(quoted(mainnetBridgeGlobalIndex), 5, "0x0d", 50)),
			wantInvalid: true,
		},
		{
			name:        "global index is not a number",
			body:        claimsBody(1, claimJSON(quoted("abc"), destination, "0x0d", 50)),
			wantInvalid: true,
		},
		{
			name:        "global index is null",
			body:        claimsBody(1, claimJSON("null", destination, "0x0d", 50)),
			wantInvalid: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeBridgeService{claimsBody: tt.body}
			source := NewClaimSource(fake.startAt(t, destination))

			claim, err := source.ClaimFor(t.Context(), bridge)

			switch {
			case tt.wantInvalid:
				requireInvalidResponse(t, err)
				require.Nil(t, claim)
			case tt.wantTx == (common.Hash{}):
				require.NoError(t, err)
				require.Nil(t, claim)
			default:
				require.NoError(t, err)
				require.NotNil(t, claim)
				require.Equal(t, tt.wantTx, claim.ClaimTx)
			}
		})
	}
}

// TestClaimSource_ClaimFor_ReturnsTheSelectedClaimData verifies the claim returned is the one
// selected by global index, with its own block data, whatever its position in the listing and its
// own origin network (the bridged token's)
func TestClaimSource_ClaimFor_ReturnsTheSelectedClaimData(t *testing.T) {
	const destination = uint32(2)
	other := claimJSON(quoted("999"), destination, "0x0e", 60)
	right := claimJSON(quoted(mainnetBridgeGlobalIndex), destination, "0x0d", 50)
	fake := &fakeBridgeService{claimsBody: claimsBody(2, other, right)}
	source := NewClaimSource(fake.startAt(t, destination))

	claim, err := source.ClaimFor(t.Context(), claimBridge(0, destination))
	require.NoError(t, err)
	require.Equal(t, &trackertypes.ClaimResult{
		ClaimTx: common.HexToHash("0x0d"), BlockNumber: 50, BlockTimestamp: 1700000000,
	}, claim)
	require.Equal(t, mainnetBridgeGlobalIndex, fake.lastGlobalIndexQuery)
}

// TestClaimSource_ClaimFor_LegacyZkEVMGlobalIndex verifies the bare deposit count global index of
// the pre-Etrog claims of the legacy zkEVM rollup is accepted for the mainnet -> legacy zkEVM
// route only
func TestClaimSource_ClaimFor_LegacyZkEVMGlobalIndex(t *testing.T) {
	const legacy = uint32(1)
	tests := []struct {
		name   string
		bridge *bridgetracker.BridgeInfo
		// claimGlobalIndex is the raw JSON of the claim's global index
		claimGlobalIndex string
		wantAccepted     bool
	}{
		{
			name: "mainnet to the legacy zkEVM, bare deposit count", bridge: claimBridge(0, legacy),
			claimGlobalIndex: quoted("7"), wantAccepted: true,
		},
		{
			name: "mainnet to the legacy zkEVM, encoded index", bridge: claimBridge(0, legacy),
			claimGlobalIndex: quoted(mainnetBridgeGlobalIndex), wantAccepted: true,
		},
		{
			name: "mainnet to the legacy zkEVM, bare deposit count of another bridge", bridge: claimBridge(0, legacy),
			claimGlobalIndex: quoted("8"),
		},
		{
			name: "mainnet to another rollup, bare deposit count", bridge: claimBridge(0, 2),
			claimGlobalIndex: quoted("7"),
		},
		{
			name: "another rollup to the legacy zkEVM, bare deposit count", bridge: claimBridge(3, legacy),
			claimGlobalIndex: quoted("7"),
		},
		{
			name: "another rollup to the legacy zkEVM, encoded index", bridge: claimBridge(3, legacy),
			claimGlobalIndex: quoted(rollup3BridgeGlobalIndex), wantAccepted: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			destination := tt.bridge.DestinationNetwork
			fake := &fakeBridgeService{
				claimsBody: claimsBody(1, claimJSON(tt.claimGlobalIndex, destination, "0x0d", 50)),
			}
			source := NewClaimSource(fake.startAt(t, destination))

			claim, err := source.ClaimFor(t.Context(), tt.bridge)
			if tt.wantAccepted {
				require.NoError(t, err)
				require.NotNil(t, claim)
				require.Equal(t, common.HexToHash("0x0d"), claim.ClaimTx)
				return
			}
			requireInvalidResponse(t, err)
			require.Nil(t, claim)
		})
	}

	t.Run("an invalid global index never matches the deposit count 0", func(t *testing.T) {
		bridge := claimBridge(0, legacy)
		bridge.DepositCount = 0
		fake := &fakeBridgeService{claimsBody: claimsBody(1, claimJSON(quoted("garbage"), legacy, "0x0d", 50))}
		source := NewClaimSource(fake.startAt(t, legacy))

		claim, err := source.ClaimFor(t.Context(), bridge)
		requireInvalidResponse(t, err)
		require.Nil(t, claim)
	})
}

// TestClaimedResolver_DoesNotCompleteOnAnInvalidResponse verifies the claimed step, fed by the
// real claim source, is neither completed by a response that is not the claim of the bridge, nor
// failed for good by it
func TestClaimedResolver_DoesNotCompleteOnAnInvalidResponse(t *testing.T) {
	const destination = uint32(2)
	tracking := domain.NewTrackingData(domain.TrackingID{NetworkID: 0, TxHash: testTxHash},
		domain.TrackingBridgeTx{Info: claimBridge(0, destination)}, nil)

	for name, body := range invalidClaimsBodies() {
		t.Run(name, func(t *testing.T) {
			fake := &fakeBridgeService{claimsBody: body}
			resolver := domain.NewClaimedResolver(NewClaimSource(fake.startAt(t, destination)))

			result, err := resolver.Resolve(testLogger, t.Context(), tracking, 0)
			requireInvalidResponse(t, err)
			require.NotErrorIs(t, err, domain.ErrStepPending)
			require.Nil(t, result)
		})
	}

	t.Run("no claim yet keeps the step pending", func(t *testing.T) {
		fake := &fakeBridgeService{claimsBody: claimsBody(0)}
		resolver := domain.NewClaimedResolver(NewClaimSource(fake.startAt(t, destination)))

		_, err := resolver.Resolve(testLogger, t.Context(), tracking, 0)
		require.ErrorIs(t, err, domain.ErrStepPending)
	})
}

// claimedChecker is a ClaimChecker answering that every bridge is claimed
func claimedChecker(t *testing.T) *trackermocks.ClaimChecker {
	t.Helper()
	checker := trackermocks.NewClaimChecker(t)
	checker.EXPECT().IsClaimed(mock.Anything, mock.Anything).Return(true, nil).Maybe()
	return checker
}

// reportedFinality is a finality resolver trusting the finality the bridge service reported, and
// proving nothing otherwise
func reportedFinality(t *testing.T) *finalitymocks.Resolver {
	t.Helper()
	resolver := finalitymocks.NewResolver(t)
	resolver.EXPECT().IsFinal(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ uint32, _ common.Hash, _ uint64, reported *bool) (bool, error) {
			return reported != nil && *reported, nil
		}).Maybe()
	return resolver
}

// TestReverifySteps_WithTheClaimSource verifies the reorg re-verification of a claimed tracking,
// fed by the real claim source: an invalid response keeps the tracking as it is (retried on the
// next tick, nothing moved), and a valid one still refreshes the claim and closes the tracking
func TestReverifySteps_WithTheClaimSource(t *testing.T) {
	const destination = uint32(2)
	storedClaim := &trackertypes.ClaimResult{
		ClaimTx: common.HexToHash("0x0d"), BlockNumber: 50, BlockTimestamp: 1700000000,
	}
	newTracking := func() *domain.TrackingData {
		return domain.NewTrackingData(domain.TrackingID{NetworkID: 0, TxHash: testTxHash},
			domain.TrackingBridgeTx{Info: claimBridge(0, destination)},
			[]domain.BridgeStepPath{
				{Step: trackertypes.StepWaitingClaim, Status: trackertypes.StepStatusDone},
				{Step: trackertypes.StepClaimed, Status: trackertypes.StepStatusDone, ResultClaim: storedClaim},
			})
	}
	checkersFor := func(t *testing.T, body string) domain.ReorgCheckers {
		t.Helper()
		fake := &fakeBridgeService{claimsBody: body}
		return domain.ReorgCheckers{
			ClaimChecker: claimedChecker(t),
			Claims:       NewClaimSource(fake.startAt(t, destination)),
			Finality:     reportedFinality(t),
		}
	}

	for name, body := range invalidClaimsBodies() {
		t.Run(name, func(t *testing.T) {
			tracking := newTracking()

			updated := domain.ReverifySteps(t.Context(), testLogger, checkersFor(t, body), tracking, time.Now())
			require.Same(t, tracking, updated)
		})
	}

	t.Run("a re-included claim reported final replaces the stored one and closes the tracking", func(t *testing.T) {
		reincluded := strings.Replace(
			claimJSON(quoted(mainnetBridgeGlobalIndex), destination, "0x0f", 51), "{", `{"block_finalized":true,`, 1)
		tracking := newTracking()

		updated := domain.ReverifySteps(t.Context(), testLogger,
			checkersFor(t, claimsBody(1, reincluded)), tracking, time.Now())
		require.NotSame(t, tracking, updated)
		steps := updated.AllSteps()
		require.Equal(t, common.HexToHash("0x0f"), steps[1].ResultClaim.ClaimTx)
		require.Equal(t, uint64(51), steps[1].ResultClaim.BlockNumber)
		require.True(t, steps[1].BlockFinalized)
	})
}

// TestEngine_InvalidClaimsResponseKeepsTheBridgeTracked runs the tracking engine, with the real
// claim source, over a claimed L2 -> L1 bridge whose destination bridge service answers the claims
// query with a response that is not the claim of the bridge: the claimed step is left with a
// transient error wrapping client.ErrInvalidResponse, never completed with it, and the engine keeps
// retrying it on the next ticks. A valid response completes the step with the claim selected
func TestEngine_InvalidClaimsResponseKeepsTheBridgeTracked(t *testing.T) {
	const destination = uint32(0)
	id := bridgetracker.TrackingID{NetworkID: 1, TxHash: testTxHash}
	// the bridge of deposit count 7 sent from network 1 (rollup index 0): global index 7
	bridge := claimBridge(1, destination)
	const bridgeGlobalIndex = "7"

	// startEngine starts an engine over a store tracking id, every step before the claimed one
	// skipped (the bridge is claimed, its LER update is not found), and returns the store updates
	startEngine := func(t *testing.T, claimsBody string) <-chan *domain.TrackingData {
		t.Helper()
		fake := &fakeBridgeService{claimsBody: claimsBody}

		bridges := trackermocks.NewBridgeEventSource(t)
		bridges.EXPECT().FindBridge(mock.Anything, id).Return(bridge, nil).Maybe()
		lers := trackermocks.NewLERSource(t)
		lers.EXPECT().OriginLER(mock.Anything, mock.Anything).
			Return(nil, fmt.Errorf("LER update not available")).Maybe()
		origin := domainmocks.NewOriginBlockSource(t)
		origin.EXPECT().CanonicalBlockHash(mock.Anything, id.NetworkID, bridge.BlockNumber).
			Return(bridge.BlockHash, nil).Maybe()
		finality := finalitymocks.NewResolver(t)
		finality.EXPECT().IsFinal(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(false, nil).Maybe()

		store := bridgetracker.NewMemoryRegistry(0)
		engine, err := bridgetracker.NewEngine(bridgetracker.EngineConfig{PollInterval: 10 * time.Millisecond},
			testLogger, store, bridgetracker.EngineSources{
				Bridges:           bridges,
				Certificates:      trackermocks.NewCertificateSource(t),
				GERs:              trackermocks.NewGERSource(t),
				LERs:              lers,
				ClaimChecker:      claimedChecker(t),
				Claims:            NewClaimSource(fake.startAt(t, destination)),
				Settlement:        trackermocks.NewSettlementSource(t),
				SettlementHistory: domainmocks.NewSettlementHistorySource(t),
				OriginBlocks:      origin,
				Finality:          finality,
				InjectedGERs:      domainmocks.NewInjectedGERChecker(t),
			})
		require.NoError(t, err)

		_, err = store.Get(id, true)
		require.NoError(t, err)
		updates, unsubscribe, err := store.Subscribe(id)
		require.NoError(t, err)
		t.Cleanup(unsubscribe)

		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)
		engine.Start(ctx)
		return updates
	}
	// awaitClaimedStep returns the claimed step of the first update satisfying done
	awaitClaimedStep := func(
		t *testing.T, updates <-chan *domain.TrackingData, done func(domain.BridgeStepPath) bool,
	) domain.BridgeStepPath {
		t.Helper()
		timeout := time.After(5 * time.Second)
		var last *domain.BridgeStepPath
		for {
			select {
			case tracking := <-updates:
				for _, step := range tracking.AllSteps() {
					if step.Step != trackertypes.StepClaimed {
						continue
					}
					last = &step
					if done(step) {
						return step
					}
				}
			case <-timeout:
				require.FailNow(t, "the claimed step did not reach the expected state", "last: %+v", last)
			}
		}
	}

	// the global index the fake answers with is checked against the one the bridge is claimed by
	require.Equal(t, bridgeGlobalIndex,
		bridgesync.GenerateGlobalIndexForNetworkID(bridge.NetworkID, bridge.DepositCount).String())

	for name, body := range map[string]string{
		"empty list with count 1": claimsBody(1),
		"null claim":              claimsBody(1, "null"),
		"another global index":    claimsBody(1, claimJSON(quoted("999"), destination, "0x0e", 60)),
	} {
		t.Run(name, func(t *testing.T) {
			updates := startEngine(t, body)

			step := awaitClaimedStep(t, updates, func(step domain.BridgeStepPath) bool {
				return step.Error != nil && step.Error.RetryCount >= 2
			})
			require.Equal(t, trackertypes.StepStatusError, step.Status, "never completed with the response")
			require.Nil(t, step.ResultClaim)
			require.Equal(t, trackertypes.StepErrorTransient, step.Error.ErrorType)
			require.Contains(t, step.Error.Description[len(step.Error.Description)-1],
				client.ErrInvalidResponse.Error())
		})
	}

	t.Run("valid claim listed after another one", func(t *testing.T) {
		updates := startEngine(t, claimsBody(2,
			claimJSON(quoted("999"), destination, "0x0e", 60),
			claimJSON(quoted(bridgeGlobalIndex), destination, "0x0d", 50)))

		step := awaitClaimedStep(t, updates, func(step domain.BridgeStepPath) bool {
			return step.Status == trackertypes.StepStatusDone
		})
		require.NotNil(t, step.ResultClaim)
		require.Equal(t, common.HexToHash("0x0d"), step.ResultClaim.ClaimTx)
		require.Equal(t, uint64(50), step.ResultClaim.BlockNumber)
	})
}
