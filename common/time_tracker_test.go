package common

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewTimeTracker(t *testing.T) {
	tracker := NewTimeTracker()

	require.NotNil(t, tracker)
	require.False(t, tracker.start.IsZero())
	require.True(t, tracker.end.IsZero())
	require.Equal(t, uint32(0), tracker.times)
	require.Equal(t, time.Duration(0), tracker.lastDuration)
	require.Equal(t, time.Duration(0), tracker.accumulated)
}

func TestTimeTracker_String(t *testing.T) {
	tracker := NewTimeTracker()
	tracker.Stop()

	str := tracker.String()
	require.Contains(t, str, "TimeTracker{times=1")
	require.Contains(t, str, "accumulated=")
}

func TestTimeTracker_Elapsed(t *testing.T) {
	tracker := NewTimeTracker()

	// Wait a small amount to ensure elapsed time > 0
	time.Sleep(1 * time.Millisecond)

	elapsed := tracker.Elapsed()
	require.Greater(t, elapsed, time.Duration(0))
}

func TestTimeTracker_ElapsedBeforeStart(t *testing.T) {
	tracker := &TimeTracker{}

	elapsed := tracker.Elapsed()
	require.Equal(t, time.Duration(0), elapsed)
}

func TestTimeTracker_Duration(t *testing.T) {
	tracker := NewTimeTracker()

	// Wait a small amount to ensure duration > 0
	time.Sleep(1 * time.Millisecond)
	tracker.Stop()

	duration := tracker.Duration()
	require.Greater(t, duration, time.Duration(0))
}

func TestTimeTracker_DurationBeforeStart(t *testing.T) {
	tracker := &TimeTracker{}

	duration := tracker.Duration()
	require.Equal(t, time.Duration(0), duration)
}

func TestTimeTracker_TotalDuration(t *testing.T) {
	tracker := NewTimeTracker()

	// First interval
	time.Sleep(1 * time.Millisecond)
	tracker.Stop()
	firstDuration := tracker.TotalDuration()
	require.Greater(t, firstDuration, time.Duration(0))

	// Second interval
	tracker.Start()
	time.Sleep(2 * time.Millisecond)
	tracker.Stop()
	totalDuration := tracker.TotalDuration()
	require.Greater(t, totalDuration, firstDuration)
}
