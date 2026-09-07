package api

import (
	"encoding/json"
	"testing"

	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestTrackingDataMarshalJSONUnresolved(t *testing.T) {
	data := TrackingData{
		TrackingStatus: types.TrackingStatusRegistered.String(),
		ClaimStatus:    types.TrackerClaimStatusPending.String(),
		NetworkID:      1,
		TxHash:         common.HexToHash("0x01"),
	}

	out, err := json.Marshal(data)
	require.NoError(t, err)

	require.JSONEq(t, `{
		"tracking_status": "registered",
		"claim_status": "pending",
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
		TrackingStatus: types.TrackingStatusError.String(),
		ClaimStatus:    types.TrackerClaimStatusError.String(),
		NetworkID:      1,
		TxHash:         common.HexToHash("0x01"),
		Error: &types.ErrorStep{
			ErrorType:   types.StepErrorExhausted,
			RetryCount:  3,
			Description: []string{"bridge tx not found"},
		},
	}

	out, err := json.Marshal(data)
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(out, &raw))
	require.Equal(t, "error", raw["tracking_status"])
	require.Equal(t, "error", raw["claim_status"])
	require.Nil(t, raw["bridge_status"], "the tracker never resolved the bridge at all")
	require.Nil(t, raw["step_index"])
	require.Nil(t, raw["all_steps"])
	errStep, ok := raw["error"].(map[string]any)
	require.True(t, ok, "error must be a populated object")
	require.Equal(t, "exhausted", errStep["error_type_string"])
	require.EqualValues(t, 3, errStep["retry_count"])
}

// TestTrackingDataFromExposesTransientError pins that trackingDataFrom always carries over
// whatever domain.TrackingData.Error reports, even while the bridge is not Failed: a
// transient FindBridge failure still being retried must be visible to clients too, not only a
// terminal give-up
func TestTrackingDataFromExposesTransientError(t *testing.T) {
	id := domain.TrackingID{NetworkID: 1, TxHash: common.HexToHash("0x01")}
	tracking := domain.NewTrackingData(id, domain.TrackingBridgeTx{
		Error: &types.ErrorStep{
			ErrorType:   types.StepErrorTransient,
			RetryCount:  2,
			Description: []string{"context deadline exceeded"},
		},
	}, nil)

	data := trackingDataFrom(tracking)
	require.Equal(t, types.TrackingStatusRegistered.String(), data.TrackingStatus,
		"a transient error must not fail the bridge")
	require.NotNil(t, data.Error)
	require.Equal(t, types.StepErrorTransient, data.Error.ErrorType)
	require.Equal(t, 2, data.Error.RetryCount)
}

// TestTrackingDataFromNoErrorIsNil pins that a healthy, never-failed bridge reports a nil
// Error instead of an empty ErrorStep object
func TestTrackingDataFromNoErrorIsNil(t *testing.T) {
	id := domain.TrackingID{NetworkID: 1, TxHash: common.HexToHash("0x01")}
	tracking := domain.NewTrackingData(id, domain.TrackingBridgeTx{}, nil)

	data := trackingDataFrom(tracking)
	require.Nil(t, data.Error)
}

// TestTrackingDataFromClaimStatus pins that trackingDataFrom carries over
// domain.TrackingData.ClaimStatus as its string representation
func TestTrackingDataFromClaimStatus(t *testing.T) {
	id := domain.TrackingID{NetworkID: 1, TxHash: common.HexToHash("0x01")}
	info := &domain.BridgeInfo{NetworkID: 1}

	testCases := []struct {
		name     string
		allSteps []domain.BridgeStepPath
		expected string
	}{
		{
			name:     "resolved bridge without steps yet -> PENDING",
			expected: types.TrackerClaimStatusPending.String(),
		},
		{
			name: "current step WaitingClaim -> READY_TO_CLAIM",
			allSteps: []domain.BridgeStepPath{
				{Step: types.StepWaitingClaim, Status: types.StepStatusInProgress},
			},
			expected: types.TrackerClaimStatusReadyToClaim.String(),
		},
		{
			name: "current step Claimed -> CLAIMED",
			allSteps: []domain.BridgeStepPath{
				{Step: types.StepClaimed, Status: types.StepStatusDone},
			},
			expected: types.TrackerClaimStatusClaimed.String(),
		},
		{
			name: "step in error -> ERROR",
			allSteps: []domain.BridgeStepPath{
				{Step: types.StepWaitingClaim, Status: types.StepStatusError},
			},
			expected: types.TrackerClaimStatusError.String(),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tracking := domain.NewTrackingData(id, domain.TrackingBridgeTx{Info: info}, tc.allSteps)
			data := trackingDataFrom(tracking)
			require.Equal(t, tc.expected, data.ClaimStatus)
		})
	}
}

func TestTrackingDataMarshalJSONResolved(t *testing.T) {
	stepIndex := 0
	data := TrackingData{
		TrackingStatus: types.TrackingStatusFinished.String(),
		ClaimStatus:    types.TrackerClaimStatusClaimed.String(),
		NetworkID:      1,
		TxHash:         common.HexToHash("0x01"),
		BridgeStatus: &BridgeStatus{
			BridgeType: types.BridgeTypeL2ToL1.String(),
			Event:      BridgeEventData{LeafType: types.BridgeLeafTypeAsset.String()},
		},
		StepIndex: &stepIndex,
		AllSteps: []BridgeStepPath{
			{StepName: types.StepClaimed.String(), Status: types.StepStatusDone.String()},
		},
	}

	out, err := json.Marshal(data)
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(out, &raw))
	require.Equal(t, "finished", raw["tracking_status"])
	require.Equal(t, "claimed", raw["claim_status"])
	require.NotNil(t, raw["bridge_status"])
	require.EqualValues(t, 0, raw["step_index"])
	allSteps, ok := raw["all_steps"].([]any)
	require.True(t, ok, "all_steps must be a populated array once resolved")
	require.Len(t, allSteps, 1)
}
