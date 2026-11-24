package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgetracker/types"
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

	// nil resolvers: FindBridge fails here, so PendingPath (the only consumer of resolvers, see
	// ResolveBridgeTx) is never reached on this path
	result, err := ResolveBridgeTx(context.Background(), source, nil, tracking, time.Hour, now)

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

// recordingBridgeEventSource is a BridgeEventSource double recording the id it was asked for
type recordingBridgeEventSource struct {
	fakeBridgeEventSource
	asked []TrackingID
}

// FindBridge implements BridgeEventSource
func (f *recordingBridgeEventSource) FindBridge(ctx context.Context, id TrackingID) (*BridgeInfo, error) {
	f.asked = append(f.asked, id)
	return f.fakeBridgeEventSource.FindBridge(ctx, id)
}

// TestResolveBridgeTxAmbiguousTxFailsPermanently pins per-event tracking at the domain level: a
// tx-only id whose tx emitted several BridgeEvents ends in a permanent tx-level error carrying the
// source's description (with the candidate log indexes) instead of any event's facts, while an id
// selecting one of them is resolved through FindBridge with its selector intact
func TestResolveBridgeTxAmbiguousTxFailsPermanently(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

	ambiguous := fmt.Errorf("network=2/tx=0x00 emitted BridgeEvents at log_index [3 9]: %w", ErrAmbiguousBridgeTx)
	source := &recordingBridgeEventSource{fakeBridgeEventSource: fakeBridgeEventSource{err: ambiguous}}
	result, err := ResolveBridgeTx(context.Background(), source, nil,
		NewTrackingData(resolveBridgeTxTestID, TrackingBridgeTx{}, nil), time.Hour, now)
	require.ErrorIs(t, err, ErrAmbiguousBridgeTx)
	require.Nil(t, result.Info())
	require.True(t, result.BridgeTx().IsInPermanentError())
	require.Equal(t, []string{ambiguous.Error()}, result.BridgeTx().Error.Description)
	require.Equal(t, types.TrackingStatusError, result.TrackingStatus())

	selected := TrackingID{NetworkID: 2, Log: LogIndex(9)}
	source = &recordingBridgeEventSource{fakeBridgeEventSource: fakeBridgeEventSource{info: &BridgeInfo{
		NetworkID: 2, DestinationNetwork: 1, LogIndex: 9,
	}}}
	result, err = ResolveBridgeTx(context.Background(), source, nil,
		NewTrackingData(selected, TrackingBridgeTx{}, nil), time.Hour, now)
	require.NoError(t, err)
	require.Equal(t, []TrackingID{selected}, source.asked)
	require.Equal(t, selected, result.ID())
	require.Equal(t, uint32(9), result.Info().LogIndex)
}
