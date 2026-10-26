package bridgeservice

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/agglayer/aggkit/bridgeservice/mocks"
	bridgetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/l2gersync"
	tree "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func boolPtr(v bool) *bool { return &v }

func clearClaimerIsBlockFinalized(claimer *mocks.Claimer) {
	expected := claimer.ExpectedCalls[:0]
	for _, call := range claimer.ExpectedCalls {
		if call.Method != "IsBlockFinalized" {
			expected = append(expected, call)
		}
	}
	claimer.ExpectedCalls = expected
}

func TestBlockFinalizedInResponses(t *testing.T) {
	var (
		blockHash      = common.HexToHash("0xb10c")
		otherBlockHash = common.HexToHash("0xdead")
	)

	t.Run("bridges", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		clearIsBlockFinalized(bridgeMocks.bridgeL1)
		bridgeMocks.bridgeL1.EXPECT().IsBlockFinalized(mock.Anything, uint64(10), blockHash).Return(true, true, nil).Maybe()
		bridgeMocks.bridgeL1.EXPECT().IsBlockFinalized(mock.Anything, uint64(11), blockHash).Return(false, true, nil).Maybe()
		bridgeMocks.bridgeL1.EXPECT().
			GetBridgesPaged(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return([]*bridgesync.Bridge{
				{BlockNum: 10, BlockHash: blockHash, Amount: common.Big0},
				{BlockNum: 11, BlockHash: blockHash, Amount: common.Big0},
			}, 2, nil)
		bridgeMocks.upgradeQuerier.EXPECT().GetUpgradeBlock(mock.Anything, mock.Anything).Return(uint64(0))

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridges?%s=%d", BridgeV1Prefix, networkIDParam, mainnetNetworkID))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.BridgesResult
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.Len(t, response.Bridges, 2)
		require.Equal(t, boolPtr(true), response.Bridges[0].BlockFinalized)
		require.Equal(t, boolPtr(false), response.Bridges[1].BlockFinalized)
	})

	t.Run("bridges on L2 ask the L2 syncer, with the hash it served", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		// the L1 syncer would say it's finalized: it must not be asked
		expectBridgeBlockFinalized(bridgeMocks.bridgeL1, 10, blockHash, true)
		expectBridgeBlockFinalized(bridgeMocks.bridgeL2, 10, blockHash, false)

		bridgeMocks.bridgeL2.EXPECT().
			GetBridgesPaged(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return([]*bridgesync.Bridge{{BlockNum: 10, BlockHash: blockHash, Amount: common.Big0}}, 1, nil)
		bridgeMocks.upgradeQuerier.EXPECT().GetUpgradeBlock(mock.Anything, mock.Anything).Return(uint64(0))

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridges?%s=%d", BridgeV1Prefix, networkIDParam, l2NetworkID))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.BridgesResult
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.Len(t, response.Bridges, 1)
		require.Equal(t, boolPtr(false), response.Bridges[0].BlockFinalized)
	})

	t.Run("bridges omit block_finalized when the syncer can't tell", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		// finalized only for another hash of the same block number
		expectBridgeBlockFinalized(bridgeMocks.bridgeL1, 10, otherBlockHash, true)
		bridgeMocks.bridgeL1.EXPECT().
			GetBridgesPaged(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return([]*bridgesync.Bridge{{BlockNum: 10, BlockHash: blockHash, Amount: common.Big0}}, 1, nil)
		bridgeMocks.upgradeQuerier.EXPECT().GetUpgradeBlock(mock.Anything, mock.Anything).Return(uint64(0))

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridges?%s=%d", BridgeV1Prefix, networkIDParam, mainnetNetworkID))
		require.Equal(t, http.StatusOK, w.Code)
		require.NotContains(t, w.Body.String(), "block_finalized")
	})

	t.Run("bridges omit block_finalized when the syncer fails", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		clearIsBlockFinalized(bridgeMocks.bridgeL1)
		bridgeMocks.bridgeL1.EXPECT().IsBlockFinalized(mock.Anything, mock.Anything, mock.Anything).
			Return(false, false, errors.New("db down"))
		bridgeMocks.bridgeL1.EXPECT().
			GetBridgesPaged(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return([]*bridgesync.Bridge{{BlockNum: 10, BlockHash: blockHash, Amount: common.Big0}}, 1, nil)
		bridgeMocks.upgradeQuerier.EXPECT().GetUpgradeBlock(mock.Anything, mock.Anything).Return(uint64(0))

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridges?%s=%d", BridgeV1Prefix, networkIDParam, mainnetNetworkID))
		require.Equal(t, http.StatusOK, w.Code)
		require.NotContains(t, w.Body.String(), "block_finalized")
	})

	t.Run("claims ask the claim syncer of the network, with the hash it served", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		clearIsBlockFinalized(bridgeMocks.bridgeL2) // the bridge syncer must not be asked
		clearClaimerIsBlockFinalized(bridgeMocks.claimL2)
		claimer := bridgeMocks.claimL2
		claimer.EXPECT().IsBlockFinalized(mock.Anything, uint64(10), blockHash).Return(true, true, nil).Maybe()
		claimer.EXPECT().IsBlockFinalized(mock.Anything, uint64(11), blockHash).Return(false, true, nil).Maybe()
		claimer.EXPECT().IsBlockFinalized(mock.Anything, mock.Anything, mock.Anything).Return(false, false, nil).Maybe()
		newClaim := func(blockNum uint64, hash common.Hash) *claimsynctypes.Claim {
			return &claimsynctypes.Claim{BlockNum: blockNum, BlockHash: hash, Amount: common.Big0, GlobalIndex: common.Big0}
		}
		bridgeMocks.claimL2.EXPECT().
			GetClaimsPaged(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return([]*claimsynctypes.Claim{
				newClaim(10, blockHash), newClaim(11, blockHash), newClaim(12, blockHash), newClaim(10, common.Hash{}),
			}, 4, nil)

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/claims?%s=%d", BridgeV1Prefix, networkIDParam, l2NetworkID))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.ClaimsResult
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.Len(t, response.Claims, 4)
		require.Equal(t, boolPtr(true), response.Claims[0].BlockFinalized)
		require.Equal(t, boolPtr(false), response.Claims[1].BlockFinalized)
		require.Nil(t, response.Claims[2].BlockFinalized)
		require.Nil(t, response.Claims[3].BlockFinalized) // no hash: unknown
	})

	t.Run("claims-by-ger", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		ger := common.HexToHash("0x27ae5ba08d7291c96c8cbddcc148bf48a6d68c7974b94356f53754ef6171d757")
		clearClaimerIsBlockFinalized(bridgeMocks.claimL1)
		bridgeMocks.claimL1.EXPECT().IsBlockFinalized(mock.Anything, uint64(10), blockHash).Return(true, true, nil).Maybe()
		bridgeMocks.claimL1.EXPECT().GetClaimsByGER(mock.Anything, ger).Return(
			[]*claimsynctypes.Claim{{BlockNum: 10, BlockHash: blockHash, Amount: common.Big0, GlobalIndex: common.Big0}},
			nil)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, "0")
		queryParams.Set(globalExitRootParam, ger.Hex())
		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/claims-by-ger?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.ClaimsByGERResult
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.Len(t, response.Claims, 1)
		require.Equal(t, boolPtr(true), response.Claims[0].BlockFinalized)
	})

	t.Run("root-by-ler", func(t *testing.T) {
		ler := common.HexToHash("0x27ae5ba08d7291c96c8cbddcc148bf48a6d68c7974b94356f53754ef6171d757")
		for _, expected := range []bool{true, false} {
			bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
			expectBridgeBlockFinalized(bridgeMocks.bridgeL2, 10, blockHash, expected)
			bridgeMocks.bridgeL2.EXPECT().
				GetRootByLER(mock.Anything, ler).
				Return(&tree.Root{Index: 1, BlockNum: 10, BlockHash: blockHash}, nil)

			queryParams := url.Values{}
			queryParams.Set(networkIDParam, strconv.Itoa(int(l2NetworkID)))
			queryParams.Set(lerParam, ler.Hex())
			w := performRequest(t, bridgeMocks.router,
				fmt.Sprintf("%s/root-by-ler?%s", BridgeV1Prefix, queryParams.Encode()))
			require.Equal(t, http.StatusOK, w.Code)

			var response bridgetypes.RootByLERResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
			require.Equal(t, boolPtr(expected), response.BlockFinalized)
		}
	})

	t.Run("settlements use the l1infotreesync finality", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		expectL1BlocksFinalized(bridgeMocks.l1InfoTree, blockHash, map[uint64]bool{101: false, 100: true})
		bridgeMocks.l1InfoTree.EXPECT().
			GetVerifiedBatchesPaged(l2NetworkID, DefaultPage, DefaultPageSize).
			Return([]*l1infotreesync.VerifiedBatchWithBlockHash{
				{BlockNumber: 101, BlockHash: &blockHash},
				{BlockNumber: 100, BlockHash: &blockHash},
			}, 2, nil)

		w := performRequest(t, bridgeMocks.router, fmt.Sprintf("%s/settlements", BridgeV1Prefix))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.SettlementsResult
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.Len(t, response.Settlements, 2)
		require.Equal(t, boolPtr(false), response.Settlements[0].BlockFinalized)
		require.Equal(t, boolPtr(true), response.Settlements[1].BlockFinalized)
	})

	t.Run("injected-l1-info-leaf on L1", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		expectL1BlocksFinalized(bridgeMocks.l1InfoTree, blockHash, map[uint64]bool{101: false})
		bridgeMocks.l1InfoTree.EXPECT().
			GetInfoByIndex(mock.Anything, uint32(3)).
			Return(&l1infotreesync.L1InfoTreeLeaf{BlockNumber: 101, BlockHash: blockHash}, nil)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, "0")
		queryParams.Set(leafIndexParam, "3")
		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/injected-l1-info-leaf?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.L1InfoTreeLeafResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.Equal(t, boolPtr(false), response.BlockFinalized)
	})

	t.Run("injected-l1-info-leaf omits block_finalized when l1infotreesync can't tell", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		bridgeMocks.l1InfoTree.EXPECT().
			GetInfoByIndex(mock.Anything, uint32(3)).
			Return(&l1infotreesync.L1InfoTreeLeaf{BlockNumber: 100}, nil)

		queryParams := url.Values{}
		queryParams.Set(networkIDParam, "0")
		queryParams.Set(leafIndexParam, "3")
		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/injected-l1-info-leaf?%s", BridgeV1Prefix, queryParams.Encode()))
		require.Equal(t, http.StatusOK, w.Code)
		require.NotContains(t, w.Body.String(), "block_finalized")
	})

	t.Run("injected-l1-info-leaf on L2 needs the L1 leaf block and the L2 injection block", func(t *testing.T) {
		const (
			l1Block = uint64(101)
			l2Block = uint64(999)
		)
		unknown := (*bool)(nil)
		testCases := []struct {
			name        string
			l1Finalized *bool
			l2Finalized *bool
			expected    *bool
		}{
			{"both finalized", boolPtr(true), boolPtr(true), boolPtr(true)},
			{"L2 not finalized", boolPtr(true), boolPtr(false), boolPtr(false)},
			{"L1 not finalized", boolPtr(false), boolPtr(true), boolPtr(false)},
			{"L2 unknown, L1 finalized", boolPtr(true), unknown, unknown},
			{"L2 unknown, L1 not finalized", boolPtr(false), unknown, boolPtr(false)},
			{"L1 unknown, L2 finalized", unknown, boolPtr(true), unknown},
		}
		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
				if tc.l1Finalized != nil {
					expectL1BlocksFinalized(bridgeMocks.l1InfoTree, blockHash, map[uint64]bool{l1Block: *tc.l1Finalized})
				}
				bridgeMocks.injectedGERs.ExpectedCalls = nil
				if tc.l2Finalized != nil {
					bridgeMocks.injectedGERs.EXPECT().IsBlockFinalized(mock.Anything, l2Block, blockHash).
						Return(*tc.l2Finalized, true, nil).Maybe()
				}
				bridgeMocks.injectedGERs.EXPECT().IsBlockFinalized(mock.Anything, mock.Anything, mock.Anything).
					Return(false, false, nil).Maybe()
				bridgeMocks.injectedGERs.EXPECT().
					GetFirstGERAfterL1InfoTreeIndex(mock.Anything, uint32(3)).
					Return(l2gersync.GlobalExitRootInfo{
						L1InfoTreeIndex: 3, BlockNum: l2Block, BlockHash: blockHash,
					}, nil)
				bridgeMocks.l1InfoTree.EXPECT().
					GetInfoByIndex(mock.Anything, uint32(3)).
					Return(&l1infotreesync.L1InfoTreeLeaf{BlockNumber: l1Block, BlockHash: blockHash}, nil)

				queryParams := url.Values{}
				queryParams.Set(networkIDParam, strconv.Itoa(int(l2NetworkID)))
				queryParams.Set(leafIndexParam, "3")
				w := performRequest(t, bridgeMocks.router,
					fmt.Sprintf("%s/injected-l1-info-leaf?%s", BridgeV1Prefix, queryParams.Encode()))
				require.Equal(t, http.StatusOK, w.Code)

				var response bridgetypes.L1InfoTreeLeafResponse
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
				require.Equal(t, tc.expected, response.BlockFinalized)
			})
		}
	})
}
