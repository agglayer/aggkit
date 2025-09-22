package common_test

import (
	"testing"
	"time"

	"github.com/agglayer/aggkit/common"
	"github.com/stretchr/testify/require"
)

func TestRateLimit(t *testing.T) {
	now := time.Now()
	common.TimeProvider = func() time.Time {
		return now
	}
	sut := common.NewRateLimit(common.NewRateLimitConfig(2, time.Second))
	require.Nil(t, sut.Call("test", false))
	require.Nil(t, sut.Call("test", false))
	sleepTime := sut.Call("test", false)
	require.NotNil(t, sleepTime)
	require.Equal(t, time.Second, *sleepTime)

	// Advance time by 2 seconds to reset the rate limit window
	common.TimeProvider = func() time.Time {
		return now.Add(time.Second * 2)
	}

	// These calls should succeed again
	sut.Call("test", false)
	sut.Call("test", false)
}

func TestRateLimitSleepTime(t *testing.T) {
	now := time.Now()
	common.TimeProvider = func() time.Time {
		return now
	}
	sut := common.NewRateLimit(common.NewRateLimitConfig(2, time.Minute))

	require.Nil(t, sut.Call("test", false))

	// Advance time by 55 seconds
	common.TimeProvider = func() time.Time {
		return now.Add(time.Second * 55)
	}

	require.Nil(t, sut.Call("test", false))
	sleepTime := sut.Call("test", false)
	require.NotNil(t, sleepTime)
	require.Equal(t, time.Second*5, *sleepTime)
	common.TimeProvider = func() time.Time {
		return now.Add(time.Second * 59)
	}
	start := time.Now()
	sleepTime = sut.Call("test", true)
	elapsed := time.Since(start)
	require.Nil(t, sleepTime)
	// The call should have slept for approximately 5 seconds
	require.True(t, elapsed >= time.Second*1, "Expected to sleep for at least 1 seconds (1minute - 59 seconds), but slept for %v", elapsed)
	require.True(t, elapsed <= time.Second*2, "Expected to sleep for at most 2 seconds (error margin of 1 second), but slept for %v", elapsed)
}

func TestRateLimitDisabled(t *testing.T) {
	now := time.Now()
	common.TimeProvider = func() time.Time {
		return now
	}
	sut := common.NewRateLimit(common.NewRateLimitConfig(0, time.Minute))

	// With rate limiting disabled (NumRequests=0), all calls should succeed
	sut.Call("test", false)
	for i := 1; i <= 1000; i++ {
		sut.Call("test", false)
	}
}

func TestRateLimitString(t *testing.T) {
	rateLimit := &common.RateLimit{}
	require.Equal(t, "RateLimit{cfg: RateLimitConfig{Unlimited}, bucket len: 0}", rateLimit.String())

	var empty *common.RateLimit
	require.Equal(t, "RateLimit{nil}", empty.String())
}

func TestRateLimitConfigString(t *testing.T) {
	cfg := common.NewRateLimitConfig(2, time.Minute)
	require.Equal(t, "RateLimitConfig{NumRequests: 2, Period: 1m0s}", cfg.String())
}
