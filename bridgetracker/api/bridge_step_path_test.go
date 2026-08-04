package api

import (
	"encoding/json"
	"testing"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// TestNewBridgeStepPaths pins that the conversion stamps each entry's StepIndex with its
// position in the list, and that a nil path stays nil (TrackingData.AllSteps must stay nil,
// not an empty array, while the bridge is unresolved)
func TestNewBridgeStepPaths(t *testing.T) {
	require.Nil(t, newBridgeStepPaths(nil))

	steps := []domain.BridgeStepPath{
		{Step: types.StepWaitingGERUpdate, Status: types.StepStatusDone},
		{Step: types.StepWaitingGERInjection, Status: types.StepStatusInProgress},
		{Step: types.StepWaitingClaim, Status: types.StepStatusPending},
	}

	wire := newBridgeStepPaths(steps)
	require.Len(t, wire, 3)
	for i, w := range wire {
		require.Equal(t, i, w.StepIndex)
		require.Equal(t, steps[i].Step.String(), w.StepName)
		require.Equal(t, steps[i].Status.String(), w.Status)
	}
}

func TestBridgeStepPathMarshalJSON(t *testing.T) {
	path := BridgeStepPath{
		StepIndex:        3,
		StepName:         types.StepCertificatePending.String(),
		Status:           types.StepStatusInProgress.String(),
		ExpectedDuration: types.NewDuration(10 * time.Minute),
	}
	data, err := json.Marshal(path)
	require.NoError(t, err)

	require.JSONEq(t, `{
		"step_index": 3,
		"step_name": "CertificatePending",
		"status": "inProgress",
		"expected_duration": "10m0s"
	}`, string(data))

	var decoded BridgeStepPath
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, path, decoded)
}

func TestBridgeStepPathErrorMarshalJSON(t *testing.T) {
	path := BridgeStepPath{
		StepName: types.StepWaitingGERUpdate.String(),
		Status:   types.StepStatusError.String(),
		Error: &types.ErrorStep{
			ErrorType:   types.StepErrorPermanent,
			RetryCount:  3,
			Description: []string{"rpc unreachable"},
		},
	}
	data, err := json.Marshal(path)
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	require.Equal(t, "error", raw["status"])
	require.NotNil(t, raw["error"])

	var decoded BridgeStepPath
	require.NoError(t, json.Unmarshal(data, &decoded))
	expected := path
	expected.Error.ErrorTypeString = path.Error.ErrorType.String()
	require.Equal(t, expected, decoded)
}

func TestBridgeStepPathResultMarshalJSON(t *testing.T) {
	testCases := []struct {
		name     string
		result   any
		expected string
	}{
		{
			name:   "GER update result",
			result: &types.GERUpdateResult{GER: common.HexToHash("0x0a"), BlockNumber: 100},
			expected: `{
				"l1_info_tree_index":0,
				"ger":"0x000000000000000000000000000000000000000000000000000000000000000a",
				"mer":"0x0000000000000000000000000000000000000000000000000000000000000000",
				"rer":"0x0000000000000000000000000000000000000000000000000000000000000000",
				"block_number":100,
				"block_timestamp":0,
				"log_index":0
			}`,
		},
		{
			name:     "LER update result",
			result:   &types.LERUpdateResult{NetworkID: 1, LER: common.HexToHash("0x0b"), BlockNumber: 200},
			expected: `{"network_id":1,"ler":"0x000000000000000000000000000000000000000000000000000000000000000b","block_number":200}`,
		},
		{
			name: "pending inclusion result",
			result: &types.PendingInclusionResult{
				CertificateID: common.HexToHash("0x0f"), NewLER: common.HexToHash("0x10"),
			},
			expected: `{
				"certificate_id":"0x000000000000000000000000000000000000000000000000000000000000000f",
				"new_ler":"0x0000000000000000000000000000000000000000000000000000000000000010"
			}`,
		},
		{
			name: "pending inclusion result with previous LER",
			result: &types.PendingInclusionResult{
				CertificateID: common.HexToHash("0x0f"), NewLER: common.HexToHash("0x10"),
				PreviousLER: func() *common.Hash { h := common.HexToHash("0x11"); return &h }(),
			},
			expected: `{
				"certificate_id":"0x000000000000000000000000000000000000000000000000000000000000000f",
				"new_ler":"0x0000000000000000000000000000000000000000000000000000000000000010",
				"previous_ler":"0x0000000000000000000000000000000000000000000000000000000000000011"
			}`,
		},
		{
			name: "certificate data result",
			result: &types.CertificateData{
				CertificateID: common.HexToHash("0x01"),
				Status:        agglayertypes.Settled,
			},
			expected: `{"certificate_id":"0x0000000000000000000000000000000000000000000000000000000000000001","status":4,"status_string":"Settled"}`,
		},
		{
			name:     "claim result",
			result:   &types.ClaimResult{ClaimTx: common.HexToHash("0x0c"), BlockNumber: 300},
			expected: `{"claim_tx":"0x000000000000000000000000000000000000000000000000000000000000000c","block_number":300}`,
		},
		{
			name: "L1 settled GER result",
			result: &types.L1SettledGERResult{
				TxHash: common.HexToHash("0x0d"), BlockNumber: 400, GER: common.HexToHash("0x0e"),
				HasVerifyBatchesTrustedAggregator: true, HasUpdateL1InfoTree: true,
			},
			expected: `{
				"tx_hash":"0x000000000000000000000000000000000000000000000000000000000000000d",
				"block_number":400,
				"ger":"0x000000000000000000000000000000000000000000000000000000000000000e",
				"has_verify_batches_trusted_aggregator":true,
				"has_update_l1_info_tree":true,
				"has_update_l1_info_tree_v2":false
			}`,
		},
		{
			name:     "no result",
			result:   nil,
			expected: `{}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			path := BridgeStepPath{
				StepName: types.StepWaitingClaim.String(), Status: types.StepStatusDone.String(), Result: tc.result,
			}
			data, err := json.Marshal(path)
			require.NoError(t, err)

			var raw map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(data, &raw))
			result, ok := raw["result"]
			if tc.result == nil {
				require.False(t, ok)
				return
			}
			require.JSONEq(t, tc.expected, string(result))
		})
	}
}
