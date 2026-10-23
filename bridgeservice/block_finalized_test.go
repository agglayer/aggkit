package bridgeservice

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	bridgetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	"github.com/agglayer/aggkit/l1infotreesync"
	tree "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func boolPtr(v bool) *bool { return &v }

func TestBlockFinalityIsFinalized(t *testing.T) {
	finalizedBlock := uint64(100)
	finality := blockFinality{finalizedBlock: &finalizedBlock}

	require.Equal(t, boolPtr(true), finality.isFinalized(99))
	require.Equal(t, boolPtr(true), finality.isFinalized(100))
	require.Equal(t, boolPtr(false), finality.isFinalized(101))
	require.Nil(t, blockFinality{}.isFinalized(1))
}

func TestAndFinalized(t *testing.T) {
	require.Equal(t, boolPtr(true), andFinalized(boolPtr(true), boolPtr(true)))
	require.Equal(t, boolPtr(false), andFinalized(boolPtr(true), boolPtr(false)))
	require.Equal(t, boolPtr(false), andFinalized(boolPtr(false), boolPtr(true)))
	require.Nil(t, andFinalized(nil, boolPtr(true)))
	require.Nil(t, andFinalized(boolPtr(true), nil))
}

func TestBlockFinalizedInResponses(t *testing.T) {
	const finalizedBlock = uint64(100)

	t.Run("bridges", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		expectFinalizedBlock(bridgeMocks.bridgeL1, finalizedBlock, nil)

		newBridge := func(blockNum uint64) *bridgesync.Bridge {
			return &bridgesync.Bridge{BlockNum: blockNum, Amount: common.Big0}
		}
		bridgeMocks.bridgeL1.EXPECT().
			GetBridgesPaged(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return([]*bridgesync.Bridge{newBridge(finalizedBlock), newBridge(finalizedBlock + 1)}, 2, nil)
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

	t.Run("bridges on L2 use the L2 finalized block", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		// L1 finalized block would make the bridge finalized, the L2 one must be the one used
		expectFinalizedBlock(bridgeMocks.bridgeL1, finalizedBlock*10, nil)
		expectFinalizedBlock(bridgeMocks.bridgeL2, finalizedBlock, nil)

		bridgeMocks.bridgeL2.EXPECT().
			GetBridgesPaged(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return([]*bridgesync.Bridge{{BlockNum: finalizedBlock + 1, Amount: common.Big0}}, 1, nil)
		bridgeMocks.upgradeQuerier.EXPECT().GetUpgradeBlock(mock.Anything, mock.Anything).Return(uint64(0))

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridges?%s=%d", BridgeV1Prefix, networkIDParam, l2NetworkID))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.BridgesResult
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.Len(t, response.Bridges, 1)
		require.Equal(t, boolPtr(false), response.Bridges[0].BlockFinalized)
	})

	t.Run("bridges omit block_finalized when the finalized block is unavailable", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		expectFinalizedBlock(bridgeMocks.bridgeL1, 0, errors.New("rpc down"))

		bridgeMocks.bridgeL1.EXPECT().
			GetBridgesPaged(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return([]*bridgesync.Bridge{{BlockNum: 1, Amount: common.Big0}}, 1, nil)
		bridgeMocks.upgradeQuerier.EXPECT().GetUpgradeBlock(mock.Anything, mock.Anything).Return(uint64(0))

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/bridges?%s=%d", BridgeV1Prefix, networkIDParam, mainnetNetworkID))
		require.Equal(t, http.StatusOK, w.Code)
		require.NotContains(t, w.Body.String(), "block_finalized")
	})

	t.Run("claims", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		expectFinalizedBlock(bridgeMocks.bridgeL2, finalizedBlock, nil)

		newClaim := func(blockNum uint64) *claimsynctypes.Claim {
			return &claimsynctypes.Claim{BlockNum: blockNum, Amount: common.Big0, GlobalIndex: common.Big0}
		}
		bridgeMocks.claimL2.EXPECT().
			GetClaimsPaged(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return([]*claimsynctypes.Claim{newClaim(finalizedBlock - 1), newClaim(finalizedBlock + 1)}, 2, nil)

		w := performRequest(t, bridgeMocks.router,
			fmt.Sprintf("%s/claims?%s=%d", BridgeV1Prefix, networkIDParam, l2NetworkID))
		require.Equal(t, http.StatusOK, w.Code)

		var response bridgetypes.ClaimsResult
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.Len(t, response.Claims, 2)
		require.Equal(t, boolPtr(true), response.Claims[0].BlockFinalized)
		require.Equal(t, boolPtr(false), response.Claims[1].BlockFinalized)
	})

	t.Run("root-by-ler", func(t *testing.T) {
		ler := common.HexToHash("0x27ae5ba08d7291c96c8cbddcc148bf48a6d68c7974b94356f53754ef6171d757")
		for blockNum, expected := range map[uint64]bool{finalizedBlock: true, finalizedBlock + 1: false} {
			bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
			expectFinalizedBlock(bridgeMocks.bridgeL2, finalizedBlock, nil)
			bridgeMocks.bridgeL2.EXPECT().
				GetRootByLER(mock.Anything, ler).
				Return(&tree.Root{Index: 1, BlockNum: blockNum}, nil)

			queryParams := url.Values{}
			queryParams.Set(networkIDParam, strconv.Itoa(int(l2NetworkID)))
			queryParams.Set(lerParam, ler.Hex())
			w := performRequest(t, bridgeMocks.router,
				fmt.Sprintf("%s/root-by-ler?%s", BridgeV1Prefix, queryParams.Encode()))
			require.Equal(t, http.StatusOK, w.Code)

			var response bridgetypes.RootByLERResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
			require.Equal(t, boolPtr(expected), response.BlockFinalized, "block %d", blockNum)
		}
	})

	t.Run("settlements use the L1 finalized block", func(t *testing.T) {
		bridgeMocks := newBridgeWithMocks(t, l2NetworkID)
		expectFinalizedBlock(bridgeMocks.bridgeL1, finalizedBlock, nil)
		bridgeMocks.l1InfoTree.EXPECT().
			GetVerifiedBatchesPaged(l2NetworkID, DefaultPage, DefaultPageSize).
			Return([]*l1infotreesync.VerifiedBatchWithBlockHash{
				{BlockNumber: finalizedBlock + 1},
				{BlockNumber: finalizedBlock},
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
		expectFinalizedBlock(bridgeMocks.bridgeL1, finalizedBlock, nil)
		bridgeMocks.l1InfoTree.EXPECT().
			GetInfoByIndex(mock.Anything, uint32(3)).
			Return(&l1infotreesync.L1InfoTreeLeaf{BlockNumber: finalizedBlock + 1}, nil)

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
}
