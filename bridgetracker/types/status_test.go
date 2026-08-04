package types

import (
	"encoding/json"
	"testing"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestBridgeTypeString(t *testing.T) {
	require.Equal(t, "L1->L2", BridgeTypeL1ToL2.String())
	require.Equal(t, "L2->L1", BridgeTypeL2ToL1.String())
	require.Equal(t, "L2->L2", BridgeTypeL2ToL2.String())
	require.Equal(t, "Unknown(99)", BridgeType(99).String())
}

func TestBridgeStepString(t *testing.T) {
	require.Equal(t, "WaitingGERUpdate", StepWaitingGERUpdate.String())
	require.Equal(t, "WaitingLERUpdate", StepWaitingLERUpdate.String())
	require.Equal(t, "PendingInclusion", StepPendingInclusion.String())
	require.Equal(t, "CertificatePending", StepCertificatePending.String())
	require.Equal(t, "WaitL1SettledGER", StepWaitL1SettledGER.String())
	require.Equal(t, "WaitingGERInjection", StepWaitingGERInjection.String())
	require.Equal(t, "WaitingClaim", StepWaitingClaim.String())
	require.Equal(t, "Claimed", StepClaimed.String())
	require.Equal(t, "Unknown(99)", BridgeStep(99).String())
}

func TestStepStatusString(t *testing.T) {
	require.Equal(t, "pending", StepStatusPending.String())
	require.Equal(t, "inProgress", StepStatusInProgress.String())
	require.Equal(t, "done", StepStatusDone.String())
	require.Equal(t, "error", StepStatusError.String())
	require.Equal(t, "Unknown(99)", StepStatus(99).String())
}

func TestStepErrorTypeString(t *testing.T) {
	require.Equal(t, "transient", StepErrorTransient.String())
	require.Equal(t, "permanent", StepErrorPermanent.String())
	require.Equal(t, "exhausted", StepErrorExhausted.String())
	require.Equal(t, "Unknown(99)", StepErrorType(99).String())
}

func TestErrorStepMarshalJSON(t *testing.T) {
	errStep := ErrorStep{
		ErrorType:   StepErrorTransient,
		RetryCount:  2,
		Description: []string{"timeout waiting for GER update", "timeout waiting for GER update"},
	}
	data, err := json.Marshal(errStep)
	require.NoError(t, err)

	require.JSONEq(t, `{
		"error_type": 0,
		"error_type_string": "transient",
		"retry_count": 2,
		"description": ["timeout waiting for GER update", "timeout waiting for GER update"]
	}`, string(data))

	var decoded ErrorStep
	require.NoError(t, json.Unmarshal(data, &decoded))
	expected := errStep
	expected.ErrorTypeString = errStep.ErrorType.String()
	require.Equal(t, expected, decoded)
}

func TestLERTypeString(t *testing.T) {
	require.Equal(t, "NA", LERTypeNA.String())
	require.Equal(t, "Mainnet", LERTypeMainnet.String())
	require.Equal(t, "Local", LERTypeLocal.String())
	require.Equal(t, "Unknown(99)", LERType(99).String())
}

func TestDurationJSONRoundTrip(t *testing.T) {
	d := NewDuration(5 * time.Minute)
	data, err := json.Marshal(d)
	require.NoError(t, err)
	require.JSONEq(t, `"5m0s"`, string(data))

	var decoded Duration
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, d.Duration, decoded.Duration)

	require.Error(t, json.Unmarshal([]byte(`"not-a-duration"`), &decoded))
	require.Error(t, json.Unmarshal([]byte(`123`), &decoded))
}

func TestBridgeStepPathMarshalJSON(t *testing.T) {
	// the *_string fields are intentionally left empty: MarshalJSON must auto-populate them
	path := BridgeStepPath{
		Step:             StepCertificatePending,
		Status:           StepStatusInProgress,
		ExpectedDuration: NewDuration(10 * time.Minute),
	}
	data, err := json.Marshal(path)
	require.NoError(t, err)

	require.JSONEq(t, `{
		"step": 3,
		"step_string": "CertificatePending",
		"status": 1,
		"status_string": "inProgress",
		"expected_duration": "10m0s"
	}`, string(data))

	var decoded BridgeStepPath
	require.NoError(t, json.Unmarshal(data, &decoded))
	expected := path
	expected.StepString = path.Step.String()
	expected.StatusString = path.Status.String()
	require.Equal(t, expected, decoded)
}

func TestBridgeStepPathErrorMarshalJSON(t *testing.T) {
	path := BridgeStepPath{
		Step:   StepWaitingGERUpdate,
		Status: StepStatusError,
		Error: &ErrorStep{
			ErrorType:   StepErrorPermanent,
			RetryCount:  3,
			Description: []string{"rpc unreachable"},
		},
	}
	data, err := json.Marshal(path)
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	require.EqualValues(t, 3, raw["status"])
	require.Equal(t, "error", raw["status_string"])
	require.NotNil(t, raw["error"])

	var decoded BridgeStepPath
	require.NoError(t, json.Unmarshal(data, &decoded))
	expected := path
	expected.StepString = path.Step.String()
	expected.StatusString = path.Status.String()
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
			result: &GERUpdateResult{GER: common.HexToHash("0x0a"), BlockNumber: 100},
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
			result:   &LERUpdateResult{NetworkID: 1, LER: common.HexToHash("0x0b"), BlockNumber: 200},
			expected: `{"network_id":1,"ler":"0x000000000000000000000000000000000000000000000000000000000000000b","block_number":200}`,
		},
		{
			name: "pending inclusion result",
			result: &PendingInclusionResult{
				CertificateID: common.HexToHash("0x0f"), NewLER: common.HexToHash("0x10"),
			},
			expected: `{
				"certificate_id":"0x000000000000000000000000000000000000000000000000000000000000000f",
				"new_ler":"0x0000000000000000000000000000000000000000000000000000000000000010"
			}`,
		},
		{
			name: "pending inclusion result with previous LER",
			result: &PendingInclusionResult{
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
			result: &CertificateData{
				CertificateID: common.HexToHash("0x01"),
				Status:        agglayertypes.Settled,
			},
			expected: `{"certificate_id":"0x0000000000000000000000000000000000000000000000000000000000000001","status":4,"status_string":"Settled"}`,
		},
		{
			name:     "claim result",
			result:   &ClaimResult{ClaimTx: common.HexToHash("0x0c"), BlockNumber: 300},
			expected: `{"claim_tx":"0x000000000000000000000000000000000000000000000000000000000000000c","block_number":300}`,
		},
		{
			name: "L1 settled GER result",
			result: &L1SettledGERResult{
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
			path := BridgeStepPath{Step: StepWaitingClaim, Status: StepStatusDone, Result: tc.result}
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

func TestGERDataMarshalJSON(t *testing.T) {
	ger := common.HexToHash("0x0a")
	gerData := GERData{
		NetworkID: 1,
		GER:       &ger,
		LERType:   LERTypeLocal,
	}
	data, err := json.Marshal(gerData)
	require.NoError(t, err)

	require.JSONEq(t, `{
		"network_id": 1,
		"ger": "0x000000000000000000000000000000000000000000000000000000000000000a",
		"ler_type": 2,
		"ler_type_string": "Local"
	}`, string(data))

	var decoded GERData
	require.NoError(t, json.Unmarshal(data, &decoded))
	expected := gerData
	expected.LERTypeString = gerData.LERType.String()
	require.Equal(t, expected, decoded)
}

func TestCertificateDataMarshalJSON(t *testing.T) {
	settlementTxHash := common.HexToHash("0x02")
	cert := CertificateData{
		CertificateID:    common.HexToHash("0x01"),
		Status:           agglayertypes.Settled,
		SettlementTxHash: &settlementTxHash,
	}
	data, err := json.Marshal(cert)
	require.NoError(t, err)

	require.JSONEq(t, `{
		"certificate_id": "0x0000000000000000000000000000000000000000000000000000000000000001",
		"status": 4,
		"status_string": "Settled",
		"settlement_tx_hash": "0x0000000000000000000000000000000000000000000000000000000000000002"
	}`, string(data))

	var decoded CertificateData
	require.NoError(t, json.Unmarshal(data, &decoded))
	expected := cert
	expected.StatusString = cert.Status.String()
	require.Equal(t, expected, decoded)
}
