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

	common.TimeProvider = func() time.Time {
		return now.Add(time.Second * 2)
	}
	require.Nil(t, sut.Call("test", false))
	require.Nil(t, sut.Call("test", false))
}

func TestRateLimitSleepTime(t *testing.T) {
	now := time.Now()
	common.TimeProvider = func() time.Time {
		return now
	}
	sut := common.NewRateLimit(common.NewRateLimitConfig(2, time.Minute))
	require.Nil(t, sut.Call("test", false))
	common.TimeProvider = func() time.Time {
		return now.Add(time.Second * 55)
	}
	require.Nil(t, sut.Call("test", false))
	sleepTime := sut.Call("test", false)
	require.NotNil(t, sleepTime)
	require.Equal(t, time.Second*5, *sleepTime)
	common.TimeProvider = func() time.Time {
		return now.Add(time.Second * 55).Add(time.Second * 4)
	}
	// It sleeps 1 second
	sut.Call("test", true)
}

func TestRateLimitDisabled(t *testing.T) {
	now := time.Now()
	common.TimeProvider = func() time.Time {
		return now
	}
	sut := common.NewRateLimit(common.NewRateLimitConfig(0, time.Minute))
	require.Nil(t, sut.Call("test", false))
	for i := 1; i <= 1000; i++ {
		require.Nil(t, sut.Call("test", false))
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
