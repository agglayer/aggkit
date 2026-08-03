package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTrackingStatusString(t *testing.T) {
	require.Equal(t, "registered", TrackingStatusRegistered.String())
	require.Equal(t, "running", TrackingStatusRunning.String())
	require.Equal(t, "error", TrackingStatusError.String())
	require.Equal(t, "finished", TrackingStatusFinished.String())
	require.Equal(t, "Unknown(99)", TrackingStatus(99).String())
}
