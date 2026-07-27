package types

import (
	"encoding/json"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestTrackingStatusString(t *testing.T) {
	require.Equal(t, "registered", TrackingStatusRegistered.String())
	require.Equal(t, "running", TrackingStatusRunning.String())
	require.Equal(t, "error", TrackingStatusError.String())
	require.Equal(t, "finished", TrackingStatusFinished.String())
	require.Equal(t, "Unknown(99)", TrackingStatus(99).String())
}

func TestTrackingDataMarshalJSONUnresolved(t *testing.T) {
	data := TrackingData{
		TrackingStatus: TrackingStatusRegistered,
		NetworkID:      1,
		TxHash:         common.HexToHash("0x01"),
	}

	out, err := json.Marshal(data)
	require.NoError(t, err)

	require.JSONEq(t, `{
		"tracking_status": 0,
		"tracking_status_string": "registered",
		"network_id": 1,
		"tx_hash": "0x0000000000000000000000000000000000000000000000000000000000000001",
		"bridge_status": null,
		"step_index": null,
		"all_steps": null,
		"error": null
	}`, string(out))
}

func TestTrackingDataMarshalJSONError(t *testing.T) {
	data := TrackingData{
		TrackingStatus: TrackingStatusError,
		NetworkID:      1,
		TxHash:         common.HexToHash("0x01"),
		Error: &ErrorStep{
			ErrorType:   StepErrorExhausted,
			RetryCount:  3,
			Description: []string{"bridge tx not found"},
		},
	}

	out, err := json.Marshal(data)
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(out, &raw))
	require.Equal(t, "error", raw["tracking_status_string"])
	require.Nil(t, raw["bridge_status"], "the tracker never resolved the bridge at all")
	require.Nil(t, raw["step_index"])
	require.Nil(t, raw["all_steps"])
	errStep, ok := raw["error"].(map[string]any)
	require.True(t, ok, "error must be a populated object")
	require.Equal(t, "exhausted", errStep["error_type_string"])
	require.EqualValues(t, 3, errStep["retry_count"])
}

func TestTrackingDataMarshalJSONResolved(t *testing.T) {
	stepIndex := 0
	data := TrackingData{
		TrackingStatus: TrackingStatusFinished,
		NetworkID:      1,
		TxHash:         common.HexToHash("0x01"),
		BridgeStatus: &BridgeStatus{
			BridgeType:     BridgeTypeL2ToL1,
			BridgeLeafType: BridgeLeafTypeAsset,
		},
		StepIndex: &stepIndex,
		AllSteps:  []BridgeStepPath{{Step: StepClaimed, Status: StepStatusDone}},
	}

	out, err := json.Marshal(data)
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(out, &raw))
	require.EqualValues(t, 3, raw["tracking_status"])
	require.Equal(t, "finished", raw["tracking_status_string"])
	require.NotNil(t, raw["bridge_status"])
	require.EqualValues(t, 0, raw["step_index"])
	allSteps, ok := raw["all_steps"].([]any)
	require.True(t, ok, "all_steps must be a populated array once resolved")
	require.Len(t, allSteps, 1)
}
