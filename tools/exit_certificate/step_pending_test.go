package exit_certificate

import (
	"context"
	"encoding/json"
	"math/big"
	"path/filepath"
	"testing"

	"github.com/agglayer/aggkit/tools/exit_certificate/bridgesyncerlite"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func pendingTestLeaf(depositCount uint32, leafType uint8, destNetwork uint32) bridgesyncerlite.BridgeLeaf {
	return bridgesyncerlite.BridgeLeaf{
		BlockNum:           uint64(depositCount) + 10,
		LeafType:           leafType,
		OriginAddress:      common.HexToAddress("0xa1"),
		DestinationNetwork: destNetwork,
		DestinationAddress: common.HexToAddress("0xd1"),
		Amount:             big.NewInt(int64(depositCount) + 100),
		Metadata:           []byte{byte(depositCount)},
		DepositCount:       depositCount,
	}
}

func TestSelectPendingCandidates(t *testing.T) {
	t.Parallel()

	bridges := []bridgesyncerlite.BridgeLeaf{
		pendingTestLeaf(3, 0, 0), // asset to L1 → candidate
		pendingTestLeaf(1, 1, 0), // message → skipped
		pendingTestLeaf(2, 0, 2), // asset to another network → skipped
		pendingTestLeaf(0, 0, 0), // asset to L1 → candidate
		pendingTestLeaf(4, 0, 0), // certificate exit → skipped
		pendingTestLeaf(5, 0, 0), // certificate exit → skipped
	}

	candidates, err := selectPendingCandidates(bridges, 2)
	require.NoError(t, err)
	require.Len(t, candidates, 2)
	// Ordered by deposit count, whatever the input order.
	require.Equal(t, uint32(0), candidates[0].DepositCount)
	require.Equal(t, uint32(3), candidates[1].DepositCount)
	require.Equal(t, big.NewInt(103), candidates[1].Amount)
	require.Equal(t, []byte{3}, []byte(candidates[1].Metadata))

	_, err = selectPendingCandidates(bridges, uint64(len(bridges))+1)
	require.ErrorContains(t, err, "run step g first")
}

func TestFilterPendingBridges(t *testing.T) {
	t.Parallel()

	candidates := []PendingBridge{{DepositCount: 0}, {DepositCount: 3}, {DepositCount: 7}}
	pending := filterPendingBridges(candidates, map[uint32]struct{}{3: {}})
	require.Equal(t, []PendingBridge{{DepositCount: 0}, {DepositCount: 7}}, pending)

	require.Empty(t, filterPendingBridges(nil, nil))
}

func TestRunStepPending(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	dbPath := filepath.Join(t.TempDir(), fileStepGLiteDB)
	syncer, err := bridgesyncerlite.New(ctx, bridgesyncerlite.Config{DBPath: dbPath}, nil)
	require.NoError(t, err)
	// 0 and 3 are pre-certificate assets to L1, 1 is a message, 2 goes to another network and
	// 4 is the certificate's only exit.
	require.NoError(t, syncer.StoreBridges(ctx, []bridgesyncerlite.BridgeLeaf{
		pendingTestLeaf(0, 0, 0), pendingTestLeaf(1, 1, 0), pendingTestLeaf(2, 0, 2),
		pendingTestLeaf(3, 0, 0), pendingTestLeaf(4, 0, 0),
	}))
	require.NoError(t, syncer.Close())

	// Deposit 0 is claimed; the isClaimed call must use the L2 network id as sourceBridgeNetwork.
	const l2NetworkID = 7
	url := newBatchRPCServer(t, func(method string, params []json.RawMessage) any {
		require.Equal(t, rpcMethodEthCall, method)
		var call struct {
			To   string `json:"to"`
			Data string `json:"data"`
		}
		require.NoError(t, json.Unmarshal(params[0], &call))
		require.Equal(t, common.HexToAddress("0xb1").Hex(), common.HexToAddress(call.To).Hex())
		raw := common.FromHex(call.Data)
		require.Equal(t, uint64(l2NetworkID), new(big.Int).SetBytes(raw[36:68]).Uint64())
		if new(big.Int).SetBytes(raw[4:36]).Uint64() == 0 {
			return "0x0000000000000000000000000000000000000000000000000000000000000001"
		}
		return "0x0000000000000000000000000000000000000000000000000000000000000000"
	})
	cfg := &Config{
		L1RPCURL: url, L1BridgeAddress: common.HexToAddress("0xb1"), L2NetworkID: l2NetworkID,
		Options: Options{RPCBatchSize: 10, ConcurrencyLimit: 2},
	}

	pending, err := RunStepPending(ctx, cfg, dbPath, &StepGResult{BridgeExitCount: 1})
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, uint32(3), pending[0].DepositCount)
	require.Equal(t, common.HexToAddress("0xd1"), pending[0].DestinationAddress)
	require.Equal(t, big.NewInt(103), pending[0].Amount)
}

func TestRunStepPendingRequiresL1RPC(t *testing.T) {
	t.Parallel()
	_, err := RunStepPending(context.Background(), &Config{}, "unused.sqlite", &StepGResult{})
	require.ErrorContains(t, err, "l1RpcUrl")
}

func TestPendingBridgeJSONRoundTrip(t *testing.T) {
	t.Parallel()

	in := PendingBridge{
		DepositCount: 9, BlockNumber: 5, OriginAddress: common.HexToAddress("0xa1"),
		DestinationAddress: common.HexToAddress("0xd1"), Metadata: []byte{0xab, 0xcd},
		Amount: new(big.Int).Lsh(big.NewInt(1), 100), // beyond uint64
	}
	raw, err := json.Marshal(in)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"metadata":"0xabcd"`)

	var out PendingBridge
	require.NoError(t, json.Unmarshal(raw, &out))
	require.Equal(t, in, out)
}
