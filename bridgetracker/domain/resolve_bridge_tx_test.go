package domain

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeBridgeEventSource is a minimal BridgeEventSource double returning a canned result/error
type fakeBridgeEventSource struct {
	info *BridgeInfo
	err  error
}

// FindBridge implements BridgeEventSource
func (f *fakeBridgeEventSource) FindBridge(_ context.Context, _ TrackingID) (*BridgeInfo, error) {
	return f.info, f.err
}

// resolveBridgeTxTestID is the TrackingID used across ResolveBridgeTx tests
var resolveBridgeTxTestID = TrackingID{NetworkID: 2}

// TestResolveBridgeTxRedactsTransientErrorDescription pins that a transient FindBridge error
// whose message embeds a backend URL is stored raw in the tx-level Error.Description - the string
// the tracker also logs, where operators need the real endpoint - and reaches the client redacted,
// through the same types.ErrorStep.MarshalJSON layer every other client-visible error goes through
func TestResolveBridgeTxRedactsTransientErrorDescription(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	rawErr := errors.New(
		`claim status: fetching claims of global index 123 on network 2: do request: ` +
			`Get "http://10.0.0.5:5577/bridge/v1/claims?network_id=2&global_index=123": ` +
			`dial tcp 10.0.0.5:5577: connect: connection refused`,
	)
	expectedOnTheWire := `claim status: fetching claims of global index 123 on network 2: do request: ` +
		`Get <redacted-url>: dial tcp <redacted-host>: connect: connection refused`

	tracking := NewTrackingData(resolveBridgeTxTestID, TrackingBridgeTx{}, nil)
	source := &fakeBridgeEventSource{err: rawErr}

	result, err := ResolveBridgeTx(context.Background(), source, tracking, time.Hour, now)

	require.ErrorIs(t, err, rawErr)
	require.NotNil(t, result.BridgeTx().Error)
	require.Equal(t, []string{rawErr.Error()}, result.BridgeTx().Error.Description,
		"the in-memory description keeps the raw error, for the logs")

	data, err := json.Marshal(result.BridgeTx().Error)
	require.NoError(t, err)

	var wire struct {
		Description []string `json:"description"`
	}
	require.NoError(t, json.Unmarshal(data, &wire))
	require.Equal(t, []string{expectedOnTheWire}, wire.Description, "description served to the client")
	require.NotContains(t, string(data), "://")
	require.NotContains(t, string(data), "10.0.0.5")
}
