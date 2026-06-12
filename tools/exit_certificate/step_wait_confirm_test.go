package exit_certificate

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// TestConfirmVerifyBatchesOnL1Success drives the full L1 settlement-confirmation flow against a stub:
// it resolves the finalized block, finds the VerifyBatchesTrustedAggregator event matching the
// rollupID + exit root, then reads the accompanying L1 info tree updates in that block. This exercises
// waitForVerifyBatchesOnL1 / scanVerifyBatches / queryVerifyBatches / resolveFinalizedBlock and the
// fetchGERUpdatesInBlock helpers end-to-end.
func TestConfirmVerifyBatchesOnL1Success(t *testing.T) {
	t.Parallel()
	exitRoot := common.HexToHash("0xexit")
	verifyTx := common.HexToHash("0xverify")
	v1Tx := common.HexToHash("0xv1")
	v2Tx := common.HexToHash("0xv2")
	mainnetExitRoot := common.HexToHash("0x1111")
	rollupExitRoot := common.HexToHash("0x2222")

	srv := newRPCStub(t, func(method string, params []any) (json.RawMessage, *jsonRPCError) {
		switch method {
		case rpcMethodEthGetBlockByNumber:
			return json.RawMessage(`{"number":"0xa"}`), nil // finalized block 10
		case rpcMethodEthGetLogs:
			switch getLogsTopic0(t, params) {
			case verifyBatchesTrustedAggregatorTopic.Hex():
				return logsResult(t, 7, verifyTx, verifyBatchesData(1, common.HexToHash("0xstate"), exitRoot)), nil
			case updateL1InfoTreeTopic.Hex():
				return topicLogsResult(t, v1Tx,
					[]common.Hash{updateL1InfoTreeTopic, mainnetExitRoot, rollupExitRoot}, nil), nil
			case updateL1InfoTreeV2TopicWait.Hex():
				return topicLogsResult(t, v2Tx,
					[]common.Hash{updateL1InfoTreeV2TopicWait, common.BytesToHash([]byte{0x05})},
					v2Data(common.HexToHash("0xroot"), common.HexToHash("0xbh"), 12345)), nil
			}
			return json.RawMessage(`[]`), nil
		default:
			return quoted("0x"), nil
		}
	})

	cfg := &Config{
		L1RPCURL:                srv.URL,
		RollupManagerAddress:    common.HexToAddress("0x3333333333333333333333333333333333333333"),
		L1GlobalExitRootAddress: common.HexToAddress("0x4444444444444444444444444444444444444444"),
		L2NetworkID:             1,
		Options:                 Options{BlockRange: 5000},
	}
	result := &StepWaitResult{}
	err := confirmVerifyBatchesOnL1(context.Background(), cfg,
		&StepSubmitResult{L1LatestBlockBeforeSubmittingCertificate: 0}, exitRoot, result)
	require.NoError(t, err)
	require.Equal(t, uint64(7), result.VerifyBatchesL1Block)
	require.Equal(t, &verifyTx, result.VerifyBatchesTxHash)
	require.NotNil(t, result.UpdateL1InfoTree)
	require.Equal(t, mainnetExitRoot, result.UpdateL1InfoTree.MainnetExitRoot)
	require.NotNil(t, result.UpdateL1InfoTreeV2)
	require.Equal(t, uint32(5), result.UpdateL1InfoTreeV2.LeafCount)
}

func TestConfirmVerifyBatchesOnL1RequiresL1RPC(t *testing.T) {
	t.Parallel()
	err := confirmVerifyBatchesOnL1(context.Background(), &Config{}, &StepSubmitResult{}, common.Hash{}, &StepWaitResult{})
	require.ErrorContains(t, err, "l1RpcUrl is required")
}

func TestConfirmVerifyBatchesOnL1NoRollupManager(t *testing.T) {
	t.Parallel()
	// L1 RPC set but neither rollupManagerAddress nor sovereignRollupAddr → cannot resolve.
	cfg := &Config{L1RPCURL: "http://127.0.0.1:1"}
	err := confirmVerifyBatchesOnL1(context.Background(), cfg, &StepSubmitResult{}, common.Hash{}, &StepWaitResult{})
	require.ErrorContains(t, err, "set rollupManagerAddress")
}
