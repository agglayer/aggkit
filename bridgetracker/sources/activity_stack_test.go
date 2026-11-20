package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgeservice"
	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/bridgetracker"
	"github.com/agglayer/aggkit/bridgetracker/api"
	"github.com/agglayer/aggkit/bridgetracker/db"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	finalitymocks "github.com/agglayer/aggkit/bridgetracker/finality/mocks"
	"github.com/agglayer/aggkit/bridgetracker/mocks"
	aggkitcommon "github.com/agglayer/aggkit/common"
	aggkitdb "github.com/agglayer/aggkit/db"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// This file drives the activity identity, ownership and paging cases through the whole activity stack, the way a
// client sees it: bridge services (fakeActivityBridgeService, served over HTTP) or a scripted
// scanner -> the real ActivitySource -> the real store (the in-memory ActivityCache or the SQLite
// store over a real database file) -> the real GET /tracker/v1/activity/from/{from_address}
// handler, called over HTTP. Only the claim checks (mockery) and the finality resolver (mockery,
// trusting block_finalized) are scripted.

const (
	stackMemory = "memory"
	stackSQLite = "sqlite"

	// stackOtherAddress is a second sender, next to testFromAddress
	stackOtherAddress = "0x2222222222222222222222222222222222222222"
)

var stackKinds = []string{stackMemory, stackSQLite}

// activityStack is one store behind the real HTTP handler
type activityStack struct {
	tb     testing.TB
	store  domain.ActivityRegistry
	router *gin.Engine
	// dbPath is the SQLite file (sqlite stack only)
	dbPath string
}

// newActivityStack wires scanner and claims into a store of kind and serves it through the real
// activity handler. Every bridge is final exactly when the bridge service says so
// (block_finalized), and so is every claim
func newActivityStack(
	tb testing.TB, kind string, scanner domain.ActivityBridgeScanner, claims domain.ActivityClaimChecker,
) *activityStack {
	tb.Helper()
	resolver := finalitymocks.NewResolver(tb)
	resolver.EXPECT().IsFinal(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ uint32, _ common.Hash, _ uint64, reported *bool) (bool, error) {
			return reported != nil && *reported, nil
		}).Maybe()
	supervised := bridgetracker.NewMemoryRegistry(10)
	logger := testLogger

	s := &activityStack{tb: tb}
	switch kind {
	case stackMemory:
		s.store = bridgetracker.NewActivityCache(scanner, claims, supervised, resolver, logger)
	case stackSQLite:
		s.dbPath = path.Join(tb.TempDir(), "bridgetracker.sqlite")
		store, err := db.NewSQLiteActivityStore(s.dbPath, scanner, claims, supervised, resolver, logger)
		require.NoError(tb, err)
		closer, ok := store.(interface{ Close() error })
		require.True(tb, ok)
		tb.Cleanup(func() { require.NoError(tb, closer.Close()) })
		s.store = store
	default:
		tb.Fatalf("unknown store %q", kind)
	}

	gin.SetMode(gin.TestMode)
	s.router = gin.New()
	api.NewAPI(logger, "sha1", supervised, s.store, nil, 0, 0, time.Second, aggkitcommon.CORSConfig{}, nil).
		RegisterRoutes(s.router)
	return s
}

// refresh registers addr and runs one background refresh of it, what ActivityEngine does
func (s *activityStack) refresh(addr string) {
	s.tb.Helper()
	from := common.HexToAddress(addr)
	_, err := s.store.RegisterAndAwait(from, false, 0)
	require.NoError(s.tb, err)
	require.NoError(s.tb, s.store.RefreshAddress(context.Background(), from))
}

// get calls GET /tracker/v1/activity/from/{addr}?query with the given If-None-Match header (none if
// empty) and returns the status, the decoded body (on 200) and the ETag
func (s *activityStack) get(addr, query, ifNoneMatch string) (int, api.ActivityResponse, string) {
	s.tb.Helper()
	url := api.TrackerV1Prefix + "/activity/from/" + addr
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	recorder := httptest.NewRecorder()
	s.router.ServeHTTP(recorder, req)
	var body api.ActivityResponse
	if recorder.Code == http.StatusOK {
		require.NoError(s.tb, json.Unmarshal(recorder.Body.Bytes(), &body))
	}
	return recorder.Code, body, recorder.Header().Get("ETag")
}

// mustGet is get of a request that must answer 200
func (s *activityStack) mustGet(addr, query string) (api.ActivityResponse, string) {
	s.tb.Helper()
	code, body, etag := s.get(addr, query, "")
	require.Equal(s.tb, http.StatusOK, code, "GET activity of %s?%s", addr, query)
	return body, etag
}

// itemKey identifies an item the way a client tells bridges apart: the network that reported it
// and its deposit count there
type itemKey struct {
	network, depositCount uint32
}

func keyOfItem(item api.ActivityItem) itemKey {
	return itemKey{network: item.BridgeNetworkID, depositCount: item.Bridge.DepositCount}
}

// byKey indexes body's items by itemKey, failing on an item listed twice
func byKey(tb testing.TB, body api.ActivityResponse) map[itemKey]api.ActivityItem {
	tb.Helper()
	out := make(map[itemKey]api.ActivityItem, len(body.Bridges))
	for _, item := range body.Bridges {
		_, dup := out[keyOfItem(item)]
		require.False(tb, dup, "item %+v listed twice", keyOfItem(item))
		out[keyOfItem(item)] = item
	}
	return out
}

// unclaimedChecker answers every bridge as not claimed and not ready to claim
func unclaimedChecker(tb testing.TB) *mocks.ActivityClaimChecker {
	tb.Helper()
	claims := mocks.NewActivityClaimChecker(tb)
	claims.EXPECT().IsClaimed(mock.Anything, mock.Anything).Return(false, nil).Maybe()
	claims.EXPECT().IsReadyToClaim(mock.Anything, mock.Anything).Return(false, nil).Maybe()
	return claims
}

// stackBridge is a bridge of from emitted on networkID with deposit count depositCount, as its
// bridge service serves it (through the production converter), in block blockNum
func stackBridge(
	networkID uint32, from string, depositCount uint32, blockNum uint64, txHash string, amount int64,
) *bridgeservicetypes.BridgeResponse {
	fromAddr := common.HexToAddress(from)
	return bridgeservice.NewBridgeResponse(&bridgesync.Bridge{
		BlockNum: blockNum, BlockPos: 1, FromAddress: &fromAddr, TxHash: common.HexToHash(txHash),
		BlockTimestamp: 1_700_000_000 + blockNum, OriginNetwork: networkID, DestinationNetwork: networkID + 1,
		DestinationAddress: common.HexToAddress("0xd0"), Amount: big.NewInt(amount), DepositCount: depositCount,
	}, networkID, 0)
}

// served sets the from_address the fake bridge service filters on to the checksummed form
// ActivitySource asks for
func served(b *bridgeservicetypes.BridgeResponse) *bridgeservicetypes.BridgeResponse {
	from := bridgeservicetypes.Address(common.HexToAddress(string(*b.FromAddress)).Hex())
	b.FromAddress = &from
	return b
}

func final(b *bridgeservicetypes.BridgeResponse) *bridgeservicetypes.BridgeResponse {
	yes := true
	b.BlockFinalized = &yes
	return b
}

// --- a bridge is identified by its network and deposit count -------------

// TestActivityStack_LegacyGlobalIndexCollisionServesBothBridges serves, through the HTTP handler,
// two real bridges whose global index collides under the legacy pre-Etrog encoding (built with the
// production converter): a pre-Etrog L1 bridge to network 1 and a bridge emitted on network 1,
// both with deposit count 7, so both encode to global index 7. Both must be listed, each with its
// own network and contents, whatever the network order, in both stores
func TestActivityStack_LegacyGlobalIndexCollisionServesBothBridges(t *testing.T) {
	const etrogUpgradeBlock = 1000
	from := common.HexToAddress(testFromAddress)
	legacyL1 := served(bridgeservice.NewBridgeResponse(&bridgesync.Bridge{
		BlockNum: 100, FromAddress: &from, TxHash: common.HexToHash("0x100"), BlockTimestamp: 1_000,
		DestinationNetwork: 1, DestinationAddress: common.HexToAddress("0xd1"), Amount: big.NewInt(100),
		DepositCount: 7,
	}, 0, etrogUpgradeBlock))
	modernNet1 := served(bridgeservice.NewBridgeResponse(&bridgesync.Bridge{
		BlockNum: etrogUpgradeBlock + 1, FromAddress: &from, TxHash: common.HexToHash("0x200"), BlockTimestamp: 2_000,
		OriginNetwork: 1, DestinationNetwork: 2, DestinationAddress: common.HexToAddress("0xd2"),
		Amount: big.NewInt(200), DepositCount: 7,
	}, 1, etrogUpgradeBlock))
	require.Equal(t, bridgeservicetypes.BigIntString("7"), legacyL1.GlobalIndex)
	require.Equal(t, legacyL1.GlobalIndex, modernNet1.GlobalIndex, "the fixture must collide")

	for _, kind := range stackKinds {
		for name, networkIDs := range map[string][]uint32{"L1 first": {0, 1}, "network 1 first": {1, 0}} {
			t.Run(kind+"/"+name, func(t *testing.T) {
				svc := &fakeActivityBridgeService{}
				svc.setBridges(0, legacyL1)
				svc.setBridges(1, modernNet1)
				source := mustNewActivitySource(t, fakeNetworkLister{networkIDs: networkIDs, url: svc.start(t)}, nil,
					testLogger)
				s := newActivityStack(t, kind, source, unclaimedChecker(t))
				s.refresh(testFromAddress)

				body, _ := s.mustGet(testFromAddress, "")
				require.Equal(t, 2, body.Count, "both bridges must be listed")
				items := byKey(t, body)
				require.Len(t, items, 2)
				l1, net1 := items[itemKey{0, 7}], items[itemKey{1, 7}]
				require.NotNil(t, l1.Bridge, "the L1 legacy bridge is missing")
				require.NotNil(t, net1.Bridge, "the network-1 bridge is missing")
				require.Equal(t, legacyL1.TxHash, l1.Bridge.TxHash)
				require.Equal(t, legacyL1.Amount, l1.Bridge.Amount)
				require.Equal(t, modernNet1.TxHash, net1.Bridge.TxHash)
				require.Equal(t, modernNet1.Amount, net1.Bridge.Amount)
			})
		}
	}
}

// TestActivityStack_RPCDoesNotReplaceTheBridgeService has the RPC fallback report, for the same
// network and deposit count the synchronized bridge service lists, a contradictory bridge
// (another tx, block, amount and destination). The client must be served the bridge service's
// bridge, now and after another refresh, in both stores
func TestActivityStack_RPCDoesNotReplaceTheBridgeService(t *testing.T) {
	authentic := served(stackBridge(1, testFromAddress, 3, 10, "0xa1", 100))
	contradictory := stackBridge(1, testFromAddress, 3, 11, "0xf1", 999)
	contradictory.DestinationNetwork = 99

	for _, kind := range stackKinds {
		t.Run(kind, func(t *testing.T) {
			svc := &fakeActivityBridgeService{
				syncStatus: &bridgeservicetypes.SyncStatus{L2Info: &bridgeservicetypes.NetworkSyncInfo{IsSynced: true}},
			}
			svc.setBridges(1, authentic)
			rpc := stubRPCScanner{bridges: map[uint32][]*domain.ScannedBridge{
				1: {{Bridge: contradictory, NetworkID: 1, Source: domain.ActivitySourceRPC}},
			}}
			s := newActivityStack(t, kind, activitySourceWithRPC(fakeNetworkLister{networkIDs: []uint32{1}, url: svc.start(t)},
				rpc), unclaimedChecker(t))

			for range 2 {
				s.refresh(testFromAddress)
				body, _ := s.mustGet(testFromAddress, "")
				require.Equal(t, 1, body.Count)
				item := body.Bridges[0]
				require.Equal(t, authentic.TxHash, item.Bridge.TxHash, "the RPC observation replaced the bridge service's")
				require.Equal(t, authentic.Amount, item.Bridge.Amount)
				require.Equal(t, authentic.DestinationNetwork, item.Bridge.DestinationNetwork)
				require.Equal(t, "bridge", item.Source)
			}
		})
	}
}

// TestActivityStack_SameLocationCorrectionReachesTheClient caches a final bridge, then has the bridge
// service report the same network and deposit count in the same transaction and block but at
// another log position, with another amount and bridge hash. The client must be served the
// corrected bridge, and a client holding the ETag of the stale response must not be answered
// 304 Not Modified, in both stores
func TestActivityStack_SameLocationCorrectionReachesTheClient(t *testing.T) {
	for _, kind := range stackKinds {
		t.Run(kind, func(t *testing.T) {
			cached := served(final(stackBridge(1, testFromAddress, 3, 10, "0xa1", 100)))
			svc := &fakeActivityBridgeService{}
			svc.setBridges(1, cached)
			source := mustNewActivitySource(t, fakeNetworkLister{networkIDs: []uint32{1}, url: svc.start(t)}, nil,
				testLogger)
			s := newActivityStack(t, kind, source, unclaimedChecker(t))
			s.refresh(testFromAddress)
			before, etag := s.mustGet(testFromAddress, "")
			require.Equal(t, cached.Amount, before.Bridges[0].Bridge.Amount)
			require.NotEmpty(t, etag)

			corrected := *cached
			corrected.BlockPos = 2
			corrected.Amount = "150"
			corrected.BridgeHash = bridgeservicetypes.Hash("0x" + strings.Repeat("ab", 32))
			svc.setBridges(1, &corrected)
			s.refresh(testFromAddress)

			after, newETag := s.mustGet(testFromAddress, "")
			require.Equal(t, 1, after.Count)
			require.Equal(t, corrected.Amount, after.Bridges[0].Bridge.Amount, "the correction was not applied")
			require.Equal(t, corrected.BlockPos, after.Bridges[0].Bridge.BlockPos)

			code, _, _ := s.get(testFromAddress, "", etag)
			require.Equal(t, http.StatusOK, code,
				"a client holding the stale response's ETag must get the corrected one, not 304")
			require.NotEqual(t, etag, newETag, "the revision must change when a bridge is corrected")
		})
	}
}

// TestActivityStack_ReplacementDoesNotInheritTheClaim settles a bridge (final, claimed, final
// claim), then has the bridge service report another bridge under the same network and deposit
// count, in the same transaction and block (another log position and amount). The replacement
// must be checked again (isClaimed() answers false for it) and served as such, not as
// claimed_finalized with the previous bridge's claim, in both stores
func TestActivityStack_ReplacementDoesNotInheritTheClaim(t *testing.T) {
	for _, kind := range stackKinds {
		t.Run(kind, func(t *testing.T) {
			settled := served(final(stackBridge(1, testFromAddress, 3, 10, "0xa1", 100)))
			replacement := *settled
			replacement.BlockPos = 2
			replacement.Amount = "999"
			replacement.BridgeHash = bridgeservicetypes.Hash("0x" + strings.Repeat("cd", 32))

			yes := true
			claims := mocks.NewActivityClaimChecker(t)
			claims.EXPECT().IsClaimed(mock.Anything, mock.Anything).
				RunAndReturn(func(_ context.Context, b *domain.ScannedBridge) (bool, error) {
					return b.Bridge.Amount == settled.Amount, nil
				})
			claims.EXPECT().ClaimInfo(mock.Anything, mock.Anything).
				Return(&bridgeservicetypes.ClaimResponse{TxHash: "0xc1a1", BlockFinalized: &yes}, nil).Maybe()
			claims.EXPECT().IsReadyToClaim(mock.Anything, mock.Anything).Return(false, nil).Maybe()

			svc := &fakeActivityBridgeService{}
			svc.setBridges(1, settled)
			source := mustNewActivitySource(t, fakeNetworkLister{networkIDs: []uint32{1}, url: svc.start(t)}, nil,
				testLogger)
			s := newActivityStack(t, kind, source, claims)
			s.refresh(testFromAddress)
			before, _ := s.mustGet(testFromAddress, "")
			require.Equal(t, "claimed_finalized", before.Bridges[0].ClaimStatus)

			svc.setBridges(1, &replacement)
			s.refresh(testFromAddress)
			after, _ := s.mustGet(testFromAddress, "")
			require.Equal(t, 1, after.Count)
			item := after.Bridges[0]
			require.Equal(t, replacement.Amount, item.Bridge.Amount, "the replacement was not applied")
			require.Equal(t, "pending", item.ClaimStatus, "the replacement inherited the previous bridge's claim")
			require.Nil(t, item.Claim)
		})
	}
}

// --- every address owns its own rows -------------------------------------

// stackTwoSenders serves, on network 1, a bridge of each of two senders under the same deposit
// count (and so the same global index): contradictory data, but each address's listing holds
// only its own one. The returned bridges are A's and B's
func stackTwoSenders(svc *fakeActivityBridgeService, finalA bool) (*bridgeservicetypes.BridgeResponse,
	*bridgeservicetypes.BridgeResponse) {
	a := served(stackBridge(1, testFromAddress, 42, 100, "0xaaa1", 100))
	if finalA {
		final(a)
	}
	b := served(stackBridge(1, stackOtherAddress, 42, 101, "0xbbb1", 999))
	b.DestinationNetwork = 99
	svc.setBridges(1, a, b)
	return a, b
}

// requireOwnBridge requires addr's activity to be exactly want
func (s *activityStack) requireOwnBridge(addr string, want *bridgeservicetypes.BridgeResponse) {
	s.tb.Helper()
	body, _ := s.mustGet(addr, "")
	require.Equal(s.tb, 1, body.Count, "activity of %s: %+v", addr, body.Bridges)
	require.Len(s.tb, body.Bridges, 1)
	got := body.Bridges[0].Bridge
	require.Equal(s.tb, want.TxHash, got.TxHash, "activity of %s holds another address's bridge", addr)
	require.Equal(s.tb, want.Amount, got.Amount)
	require.Equal(s.tb, want.DestinationNetwork, got.DestinationNetwork)
}

// TestActivityStack_AddressesSharingABridgeKeyKeepTheirOwn refreshes two addresses whose bridge
// service listings hold a bridge each under the same network, deposit count and global index, in
// both orders and both stores: each address must keep seeing its own bridge, after every refresh
// of the other one
func TestActivityStack_AddressesSharingABridgeKeyKeepTheirOwn(t *testing.T) {
	for _, kind := range stackKinds {
		for name, order := range map[string][]string{
			"A first": {testFromAddress, stackOtherAddress}, "B first": {stackOtherAddress, testFromAddress},
		} {
			t.Run(kind+"/"+name, func(t *testing.T) {
				svc := &fakeActivityBridgeService{}
				a, b := stackTwoSenders(svc, false)
				source := mustNewActivitySource(t, fakeNetworkLister{networkIDs: []uint32{1}, url: svc.start(t)}, nil,
					testLogger)
				s := newActivityStack(t, kind, source, unclaimedChecker(t))

				for range 2 {
					for _, addr := range order {
						s.refresh(addr)
					}
					s.requireOwnBridge(testFromAddress, a)
					s.requireOwnBridge(stackOtherAddress, b)
				}
			})
		}
	}
}

// TestActivityStack_SettledBridgeOfOneAddressDoesNotHideAnother settles A's bridge (final, claimed,
// final claim) before B, whose listing holds another bridge under the same key, is refreshed: B
// must see its own bridge, and A keeps its settled one, in both stores
func TestActivityStack_SettledBridgeOfOneAddressDoesNotHideAnother(t *testing.T) {
	for _, kind := range stackKinds {
		t.Run(kind, func(t *testing.T) {
			svc := &fakeActivityBridgeService{}
			a, b := stackTwoSenders(svc, true)
			yes := true
			claims := mocks.NewActivityClaimChecker(t)
			claims.EXPECT().IsClaimed(mock.Anything, mock.Anything).
				RunAndReturn(func(_ context.Context, sb *domain.ScannedBridge) (bool, error) {
					return sb.Bridge.TxHash == a.TxHash, nil
				})
			claims.EXPECT().ClaimInfo(mock.Anything, mock.Anything).
				Return(&bridgeservicetypes.ClaimResponse{TxHash: "0xc1a1", BlockFinalized: &yes}, nil).Maybe()
			claims.EXPECT().IsReadyToClaim(mock.Anything, mock.Anything).Return(false, nil).Maybe()
			source := mustNewActivitySource(t, fakeNetworkLister{networkIDs: []uint32{1}, url: svc.start(t)}, nil,
				testLogger)
			s := newActivityStack(t, kind, source, claims)

			s.refresh(testFromAddress)
			settled, _ := s.mustGet(testFromAddress, "")
			require.Equal(t, "claimed_finalized", settled.Bridges[0].ClaimStatus)

			s.refresh(stackOtherAddress)
			s.requireOwnBridge(stackOtherAddress, b)
			s.requireOwnBridge(testFromAddress, a)
			body, _ := s.mustGet(stackOtherAddress, "")
			require.Equal(t, "pending", body.Bridges[0].ClaimStatus, "B's claim state is its own")
		})
	}
}

// TestActivityStack_LegacyCollisionAcrossAddresses gives A a pre-Etrog L1 bridge and B a bridge
// emitted on network 1, both encoding to global index 7: each address must see its own, in both
// stores
func TestActivityStack_LegacyCollisionAcrossAddresses(t *testing.T) {
	const etrogUpgradeBlock = 1000
	fromA, fromB := common.HexToAddress(testFromAddress), common.HexToAddress(stackOtherAddress)
	legacyL1 := served(bridgeservice.NewBridgeResponse(&bridgesync.Bridge{
		BlockNum: 100, FromAddress: &fromA, TxHash: common.HexToHash("0x100"), BlockTimestamp: 1_000,
		DestinationNetwork: 1, DestinationAddress: common.HexToAddress("0xd1"), Amount: big.NewInt(100),
		DepositCount: 7,
	}, 0, etrogUpgradeBlock))
	modernNet1 := served(bridgeservice.NewBridgeResponse(&bridgesync.Bridge{
		BlockNum: etrogUpgradeBlock + 1, FromAddress: &fromB, TxHash: common.HexToHash("0x200"), BlockTimestamp: 2_000,
		OriginNetwork: 1, DestinationNetwork: 2, DestinationAddress: common.HexToAddress("0xd2"),
		Amount: big.NewInt(200), DepositCount: 7,
	}, 1, etrogUpgradeBlock))
	require.Equal(t, legacyL1.GlobalIndex, modernNet1.GlobalIndex, "the fixture must collide")

	for _, kind := range stackKinds {
		t.Run(kind, func(t *testing.T) {
			svc := &fakeActivityBridgeService{}
			svc.setBridges(0, legacyL1)
			svc.setBridges(1, modernNet1)
			source := mustNewActivitySource(t, fakeNetworkLister{networkIDs: []uint32{0, 1}, url: svc.start(t)}, nil,
				testLogger)
			s := newActivityStack(t, kind, source, unclaimedChecker(t))
			s.refresh(testFromAddress)
			s.refresh(stackOtherAddress)
			s.requireOwnBridge(testFromAddress, legacyL1)
			s.requireOwnBridge(stackOtherAddress, modernNet1)
		})
	}
}

// TestActivityStack_FlushOfOneAddressKeepsAnother flushes B's cache (flush_cache=true) after both
// addresses were refreshed: A's bridge stays, in both stores
func TestActivityStack_FlushOfOneAddressKeepsAnother(t *testing.T) {
	for _, kind := range stackKinds {
		t.Run(kind, func(t *testing.T) {
			svc := &fakeActivityBridgeService{}
			a, _ := stackTwoSenders(svc, false)
			source := mustNewActivitySource(t, fakeNetworkLister{networkIDs: []uint32{1}, url: svc.start(t)}, nil,
				testLogger)
			s := newActivityStack(t, kind, source, unclaimedChecker(t))
			s.refresh(testFromAddress)
			s.refresh(stackOtherAddress)

			code, _, _ := s.get(stackOtherAddress, "flush_cache=true", "")
			require.Equal(t, http.StatusServiceUnavailable, code, "B is not ready again until its next refresh")
			s.requireOwnBridge(testFromAddress, a)
		})
	}
}

// --- the store pages ------------------------------------------------------

// pagedActivity is n bridges of testFromAddress, emitted on networks 0 to 2, with block timestamps
// tied in groups (so the global index breaks the ties) and global indexes of every encoding:
// small ones that sort differently as text, rollup ones and mainnet ones above 2^64. The claim
// state cycles with the deposit count: claimed (final claim), ready to claim, pending, and the
// isClaimed() check failing (error)
type pagedActivity struct {
	bridges []*domain.ScannedBridge
}

func newPagedActivity(n int) *pagedActivity {
	p := &pagedActivity{}
	for i := range n {
		network := uint32(i % 3)
		b := &bridgeservicetypes.BridgeResponse{
			BlockNum: uint64(i + 1), BlockPos: uint64(i % 4), BlockTimestamp: 1_700_000_000 + uint64(i%37),
			TxHash: bridgeservicetypes.Hash(fmt.Sprintf("0x%064x", i+1)), DepositCount: uint32(i),
			OriginNetwork: network, DestinationNetwork: network + 1, Amount: bridgeservicetypes.BigIntString(fmt.Sprint(i + 1)),
			GlobalIndex: bridgeservicetypes.BigIntString(bridgesync.GenerateGlobalIndexForNetworkID(network, uint32(i)).String()),
		}
		yes := i%5 != 0
		b.BlockFinalized = &yes
		p.bridges = append(p.bridges, &domain.ScannedBridge{
			Bridge: b, NetworkID: network, Source: domain.ActivitySourceBridgeService,
		})
	}
	return p
}

// status is the claim_status the stack must serve for b
func pagedStatus(b *domain.ScannedBridge) string {
	switch b.Bridge.DepositCount % 4 {
	case 0:
		if *b.Bridge.BlockFinalized {
			return "claimed_finalized"
		}
		return "claimed"
	case 1:
		return "readyToClaim"
	case 2:
		return "pending"
	default:
		return "error"
	}
}

func (p *pagedActivity) claims(tb testing.TB) *mocks.ActivityClaimChecker {
	tb.Helper()
	yes := true
	claims := mocks.NewActivityClaimChecker(tb)
	claims.EXPECT().IsClaimed(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, b *domain.ScannedBridge) (bool, error) {
			if b.Bridge.DepositCount%4 == 3 {
				return false, fmt.Errorf("isClaimed() failed")
			}
			return b.Bridge.DepositCount%4 == 0, nil
		}).Maybe()
	claims.EXPECT().ClaimInfo(mock.Anything, mock.Anything).
		Return(&bridgeservicetypes.ClaimResponse{TxHash: "0xc1a1", BlockFinalized: &yes}, nil).Maybe()
	claims.EXPECT().IsReadyToClaim(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, b *domain.ScannedBridge) (bool, error) {
			return b.Bridge.DepositCount%4 == 1, nil
		}).Maybe()
	return claims
}

// stack is a stack of kind holding p, refreshed once
func (p *pagedActivity) stack(tb testing.TB, kind string) *activityStack {
	tb.Helper()
	scanner := mocks.NewActivityBridgeScanner(tb)
	scanner.EXPECT().BridgesFrom(mock.Anything, mock.Anything, mock.Anything).Return(p.bridges, nil, nil, nil).Once()
	s := newActivityStack(tb, kind, scanner, p.claims(tb))
	s.refresh(testFromAddress)
	return s
}

// expected is the bridges of p whose status is in statuses (all if empty), in the documented
// order: creation_timestamp descending, then global index descending, numerically
func (p *pagedActivity) expected(statuses ...string) []itemKey {
	var matching []*domain.ScannedBridge
	for _, b := range p.bridges {
		if len(statuses) == 0 || slices.Contains(statuses, pagedStatus(b)) {
			matching = append(matching, b)
		}
	}
	slices.SortFunc(matching, func(a, b *domain.ScannedBridge) int {
		if a.Bridge.BlockTimestamp != b.Bridge.BlockTimestamp {
			if a.Bridge.BlockTimestamp > b.Bridge.BlockTimestamp {
				return -1
			}
			return 1
		}
		return b.Bridge.GlobalIndex.ToBigInt().Cmp(a.Bridge.GlobalIndex.ToBigInt())
	})
	keys := make([]itemKey, 0, len(matching))
	for _, b := range matching {
		keys = append(keys, itemKey{network: b.NetworkID, depositCount: b.Bridge.DepositCount})
	}
	return keys
}

// pageThrough reads every page of size pageSize of query and returns the items in the order
// served, checking every page reports the same total and that the page after the last is empty
func (s *activityStack) pageThrough(query string, pageSize int, wantTotal int) []itemKey {
	s.tb.Helper()
	var keys []itemKey
	for page := 1; ; page++ {
		body, _ := s.mustGet(testFromAddress, fmt.Sprintf("%spage_number=%d&page_size=%d", query, page, pageSize))
		require.Equal(s.tb, wantTotal, body.Count, "page %d reports another total", page)
		require.NotNil(s.tb, body.Bridges)
		if len(body.Bridges) == 0 {
			require.Equal(s.tb, (wantTotal+pageSize-1)/pageSize+1, page, "an empty page before the end")
			return keys
		}
		require.LessOrEqual(s.tb, len(body.Bridges), pageSize)
		if page*pageSize < wantTotal {
			require.Len(s.tb, body.Bridges, pageSize, "only the last page can be short (page %d)", page)
		}
		for _, item := range body.Bridges {
			keys = append(keys, keyOfItem(item))
		}
	}
}

// TestActivityStack_PaginationIsCompleteAndStable pages through 500 bridges over HTTP with several
// page sizes, in both stores: the pages, concatenated, are every bridge exactly once (no duplicate
// nor gap between pages), in the documented order, every page reports the real total, the page
// after the last is empty, and both stores serve exactly the same pages
func TestActivityStack_PaginationIsCompleteAndStable(t *testing.T) {
	p := newPagedActivity(500)
	want := p.expected()
	servedPages := map[string][]itemKey{}
	for _, kind := range stackKinds {
		t.Run(kind, func(t *testing.T) {
			s := p.stack(t, kind)
			for _, pageSize := range []int{7, 50, 200} {
				got := s.pageThrough("", pageSize, len(want))
				require.Equal(t, want, got, "page size %d", pageSize)
				servedPages[fmt.Sprintf("%s/%d", kind, pageSize)] = got
			}
			// the same page twice is the same page
			first, _ := s.mustGet(testFromAddress, "page_number=3&page_size=7")
			again, _ := s.mustGet(testFromAddress, "page_number=3&page_size=7")
			require.Equal(t, first.Bridges, again.Bridges)
		})
	}
	require.Equal(t, servedPages[stackMemory+"/7"], servedPages[stackSQLite+"/7"])
}

// TestActivityStack_FiltersCombineWithPagination pages through every filterBridges value over HTTP,
// in both stores: each serves exactly the bridges with that claim_status, in order, with the
// filtered total on every page
func TestActivityStack_FiltersCombineWithPagination(t *testing.T) {
	p := newPagedActivity(300)
	filters := map[string][]string{
		"claimed":      {"claimed", "claimed_finalized"},
		"pending":      {"pending"},
		"readyToClaim": {"readyToClaim"},
		"error":        {"error"},
	}
	for _, kind := range stackKinds {
		t.Run(kind, func(t *testing.T) {
			s := p.stack(t, kind)
			for filter, statuses := range filters {
				want := p.expected(statuses...)
				require.NotEmpty(t, want, filter)
				got := s.pageThrough("filterBridges="+filter+"&", 9, len(want))
				require.Equal(t, want, got, "filterBridges=%s", filter)
				body, _ := s.mustGet(testFromAddress, "filterBridges="+filter+"&page_size=200")
				for _, item := range body.Bridges {
					require.Contains(t, statuses, item.ClaimStatus, "filterBridges=%s", filter)
				}
			}
		})
	}
}

// TestActivityStack_SQLiteReadsOnlyThePage holds 5000 bridges in the SQLite store and makes every
// row undecodable except the ones of the requested page, through another connection to the same
// database file: the request must still be answered with that page and the real total, which it
// can only do if it reads (decodes) no other row. Repeated with a filter, and for a page past the
// end (no row decoded at all)
func TestActivityStack_SQLiteReadsOnlyThePage(t *testing.T) {
	const n = 5000
	p := newPagedActivity(n)
	s := p.stack(t, stackSQLite)
	globalIndexOf := make(map[itemKey]string, n)
	for _, b := range p.bridges {
		globalIndexOf[itemKey{b.NetworkID, b.Bridge.DepositCount}] = string(b.Bridge.GlobalIndex)
	}

	conn, err := aggkitdb.NewSQLiteDB(s.dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	_, err = conn.Exec("CREATE TABLE stack_backup AS SELECT global_index, data FROM activity_bridge")
	require.NoError(t, err)
	_, err = conn.Exec("CREATE UNIQUE INDEX stack_backup_global_index ON stack_backup (global_index)")
	require.NoError(t, err)
	// corruptAllBut makes every row but those of keep undecodable; the cleanup restores them
	corruptAllBut := func(keep []itemKey) {
		t.Helper()
		_, err := conn.Exec("UPDATE activity_bridge SET data = x'00'")
		require.NoError(t, err)
		for _, k := range keep {
			_, err := conn.Exec(`UPDATE activity_bridge SET data = (SELECT data FROM stack_backup b
				WHERE b.global_index = activity_bridge.global_index) WHERE global_index = ?`, globalIndexOf[k])
			require.NoError(t, err)
		}
	}
	restore := func() {
		t.Helper()
		_, err := conn.Exec(`UPDATE activity_bridge SET data = (SELECT data FROM stack_backup b
			WHERE b.global_index = activity_bridge.global_index)`)
		require.NoError(t, err)
	}

	// control: a decoded corrupted row fails the request
	corruptAllBut(nil)
	code, _, _ := s.get(testFromAddress, "page_number=1&page_size=10", "")
	restore()
	require.Equal(t, http.StatusInternalServerError, code, "the corruption must be detectable")

	for _, tc := range []struct {
		query    string
		statuses []string
		page     int
	}{
		{query: "", page: 3},
		{query: "filterBridges=pending&", statuses: []string{"pending"}, page: 7},
		{query: "", page: n/10 + 1}, // past the end
	} {
		want := p.expected(tc.statuses...)
		start := min((tc.page-1)*10, len(want))
		wantPage := want[start:min(start+10, len(want))]
		corruptAllBut(wantPage)
		code, body, _ := s.get(testFromAddress, fmt.Sprintf("%spage_number=%d&page_size=10", tc.query, tc.page), "")
		restore()
		require.Equal(t, http.StatusOK, code, "%q page %d: a row outside the page was decoded", tc.query, tc.page)
		require.Equal(t, len(want), body.Count)
		got := make([]itemKey, 0, len(body.Bridges))
		for _, item := range body.Bridges {
			got = append(got, keyOfItem(item))
		}
		require.Equal(t, wantPage, got)
	}
}

// BenchmarkActivityStackPage measures one GET of the first 10 entries of an address with 5000
// bridges, in each store: with the store applying the page it should not grow with the history
func BenchmarkActivityStackPage(b *testing.B) {
	p := newPagedActivity(5000)
	for _, kind := range stackKinds {
		b.Run(kind, func(b *testing.B) {
			s := p.stack(b, kind)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				code, _, _ := s.get(testFromAddress, "page_number=1&page_size=10", "")
				if code != http.StatusOK {
					b.Fatalf("GET answered %d", code)
				}
			}
		})
	}
}
