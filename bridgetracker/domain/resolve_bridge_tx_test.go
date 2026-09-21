package domain

import (
	"context"
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
// whose message embeds a backend URL (vector V10 from the plan's redaction vector table) is
// redacted before being stored in the tx-level Error.Description, the same choke point every
// other client-visible error string goes through (see aggkitcommon.RedactError)
func TestResolveBridgeTxRedactsTransientErrorDescription(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	rawErr := errors.New(
		`claim status: fetching claims of global index 123 on network 2: do request: ` +
			`Get "http://10.0.0.5:5577/bridge/v1/claims?network_id=2&global_index=123": ` +
			`dial tcp 10.0.0.5:5577: connect: connection refused`,
	)
	expected := `claim status: fetching claims of global index 123 on network 2: do request: ` +
		`Get <redacted-url>: dial tcp <redacted-host>: connect: connection refused`

	tracking := NewTrackingData(resolveBridgeTxTestID, TrackingBridgeTx{}, nil)
	source := &fakeBridgeEventSource{err: rawErr}

	result, err := ResolveBridgeTx(context.Background(), source, tracking, time.Hour, now)

	require.ErrorIs(t, err, rawErr)
	require.NotNil(t, result.BridgeTx().Error)
	require.Equal(t, []string{expected}, result.BridgeTx().Error.Description)
	require.NotContains(t, result.BridgeTx().Error.Description[0], "://")
	require.NotContains(t, result.BridgeTx().Error.Description[0], "10.0.0.5")
}
