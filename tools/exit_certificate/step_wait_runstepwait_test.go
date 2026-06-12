package exit_certificate

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/agglayer/aggkit/agglayer/mocks"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestRunStepWaitSuccess drives runStepWait end-to-end: the certificate settles (via the mock client)
// and the L1 settlement is confirmed against an L1 RPC stub serving the VerifyBatchesTrustedAggregator
// event and the accompanying L1 info tree updates.
func TestRunStepWaitSuccess(t *testing.T) {
	t.Parallel()
	certHash := common.HexToHash("0xc0ffee")
	exitRoot := common.HexToHash("0xexit")
	settlementTx := common.HexToHash("0x5e771e")
	verifyTx := common.HexToHash("0xverify")

	l1 := newRPCStub(t, func(method string, params []any) (json.RawMessage, *jsonRPCError) {
		switch method {
		case rpcMethodEthGetBlockByNumber:
			return json.RawMessage(`{"number":"0xa"}`), nil
		case rpcMethodEthGetLogs:
			switch getLogsTopic0(t, params) {
			case verifyBatchesTrustedAggregatorTopic.Hex():
				return logsResult(t, 7, verifyTx, verifyBatchesData(1, common.HexToHash("0xstate"), exitRoot)), nil
			case updateL1InfoTreeTopic.Hex():
				return topicLogsResult(t, common.HexToHash("0xv1"),
					[]common.Hash{updateL1InfoTreeTopic, common.HexToHash("0x1111"), common.HexToHash("0x2222")}, nil), nil
			case updateL1InfoTreeV2TopicWait.Hex():
				return topicLogsResult(t, common.HexToHash("0xv2"),
					[]common.Hash{updateL1InfoTreeV2TopicWait, common.BytesToHash([]byte{0x05})},
					v2Data(common.HexToHash("0xroot"), common.HexToHash("0xbh"), 12345)), nil
			}
			return json.RawMessage(`[]`), nil
		default:
			return quoted("0x"), nil
		}
	})

	client := mocks.NewAgglayerClientMock(t)
	client.EXPECT().GetCertificateHeader(mock.Anything, certHash).Return(&agglayertypes.CertificateHeader{
		Status:           agglayertypes.Settled,
		NewLocalExitRoot: exitRoot,
		SettlementTxHash: &settlementTx,
	}, nil)

	cfg := &Config{
		L1RPCURL:                l1.URL,
		RollupManagerAddress:    common.HexToAddress("0x3333333333333333333333333333333333333333"),
		L1GlobalExitRootAddress: common.HexToAddress("0x4444444444444444444444444444444444444444"),
		L2NetworkID:             1,
		Options:                 Options{BlockRange: 5000},
	}

	res, err := runStepWait(context.Background(), cfg, client, &StepSubmitResult{CertificateHash: certHash})
	require.NoError(t, err)
	require.True(t, res.FinalStatus.IsSettled())
	require.Equal(t, &settlementTx, res.SettlementTxHash)
	require.Equal(t, uint64(7), res.VerifyBatchesL1Block)
	require.NotNil(t, res.UpdateL1InfoTree)
}

func TestRunStepWaitInError(t *testing.T) {
	t.Parallel()
	certHash := common.HexToHash("0xbad")

	client := mocks.NewAgglayerClientMock(t)
	client.EXPECT().GetCertificateHeader(mock.Anything, certHash).Return(&agglayertypes.CertificateHeader{
		Status: agglayertypes.InError,
	}, nil)

	_, err := runStepWait(context.Background(), &Config{L2NetworkID: 1}, client, &StepSubmitResult{CertificateHash: certHash})
	require.ErrorContains(t, err, "is in error")
}
