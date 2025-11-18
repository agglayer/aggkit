package multidownloader

import (
	"fmt"
	"testing"
	"time"

	"github.com/agglayer/aggkit/common"
	"github.com/stretchr/testify/require"
)

func TestStatistics_FinishSyncing(t *testing.T) {
	t.Run("should stop time tracker when called", func(t *testing.T) {
		stats := NewStatistics()

		// Start syncing first
		stats.StartSyncing()

		// Wait a bit to ensure some time passes
		time.Sleep(10 * time.Millisecond)

		// Finish syncing
		stats.FinishSyncing()

		// Verify elapsed time is captured
		elapsed := stats.ElapsedSyncing()
		require.Greater(t, elapsed, time.Duration(0), "elapsed time should be greater than 0")
	})

	t.Run("should be thread safe", func(t *testing.T) {
		stats := NewStatistics()
		stats.StartSyncing()

		done := make(chan bool, 2)

		// Start two goroutines that call FinishSyncing simultaneously
		go func() {
			stats.FinishSyncing()
			done <- true
		}()

		go func() {
			stats.FinishSyncing()
			done <- true
		}()

		// Wait for both goroutines to complete
		<-done
		<-done

		// Should not panic or cause race conditions
		elapsed := stats.ElapsedSyncing()
		require.GreaterOrEqual(t, elapsed, time.Duration(0))
	})

	t.Run("should work when called without StartSyncing", func(t *testing.T) {
		stats := NewStatistics()

		// Call FinishSyncing without StartSyncing first
		require.NotPanics(t, func() {
			stats.FinishSyncing()
		})

		// Elapsed time should be 0 since no start was called
		elapsed := stats.ElapsedSyncing()
		require.Equal(t, time.Duration(0), elapsed)
	})

	t.Run("should work when called multiple times", func(t *testing.T) {
		stats := NewStatistics()
		stats.StartSyncing()

		// Call FinishSyncing multiple times
		stats.FinishSyncing()
		firstElapsed := stats.ElapsedSyncing()

		stats.FinishSyncing()
		secondElapsed := stats.ElapsedSyncing()

		// Elapsed time should remain the same after multiple calls
		require.Equal(t, firstElapsed, secondElapsed)
	})
}

func TestStatistics_ETA(t *testing.T) {
	sut := NewStatistics()
	require.Equal(t, time.Duration(0), sut.ETA(10), "No blocks synced yey, ETA should be 0")
	sut.totalBlocksSynced = 5
	now := time.Now()
	sut.timeTrackerTotal = *common.NewTimeTrackerValues(
		now.Add(-10*time.Second),
		now,
		1,
	)
	require.Equal(t, 40*time.Second, sut.ETA(20))
}

func TestStatistics_Show(t *testing.T) {
	sut := NewStatistics()
	logs := []string{}
	logFunc := func(format string, args ...interface{}) {
		line := fmt.Sprintf(format, args...)
		logs = append(logs, line)
	}
	sut.StartSyncing()

	sut.StartDBOperation()
	sut.FinishDBOperation(nil)
	sut.LaunchedEthCall()
	sut.FinishEthCall(nil, 10, 100)
	sut.FinishSyncing()
	// Test with zero values
	sut.Show(logFunc, 1)
	require.Len(t, logs, 6)
	require.Contains(t, logs[0], "Statistics: time Total=")
	require.Contains(t, logs[2], "Statistics: time EthCalls=")
	require.Contains(t, logs[3], "Statistics: time Database=")
	require.Contains(t, logs[4], "Statistics: totalLogsSynced=10")
	require.Contains(t, logs[5], "Statistics: totalBlocksSynced=100")
}
