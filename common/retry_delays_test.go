package common

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/log"
	"github.com/stretchr/testify/require"
)

func TestExecute_SuccessFirstTry(t *testing.T) {
	r := &RetryDelays{
		Delays:      []types.Duration{{Duration: 10 * time.Millisecond}},
		MaxAttempts: 3,
	}
	logger := log.WithFields("module", "ut")
	ctx := context.Background()
	fn := func() (int, error) {
		return 42, nil
	}
	result, err := Execute(r, ctx, logger.Infof, "test-success", fn)
	require.NoError(t, err)
	require.Equal(t, 42, result)
}

func TestExecute_RetryAndSuccess(t *testing.T) {
	r := &RetryDelays{
		Delays:      []types.Duration{{Duration: 10 * time.Millisecond}, {Duration: 10 * time.Millisecond}},
		MaxAttempts: 3,
	}
	logger := log.WithFields("module", "ut")
	ctx := context.Background()
	attempts := 0
	fn := func() (string, error) {
		attempts++
		if attempts < 2 {
			return "", errors.New("fail")
		}
		return "ok", nil
	}
	result, err := Execute(r, ctx, logger.Infof, "test-retry-success", fn)
	require.NoError(t, err)
	require.Equal(t, "ok", result)
	require.Equal(t, 2, attempts)
}

func TestExecute_ExceedMaxAttempts(t *testing.T) {
	r := &RetryDelays{
		Delays:      []types.Duration{{Duration: 5 * time.Millisecond}},
		MaxAttempts: 2,
	}
	logger := log.WithFields("module", "ut")
	ctx := context.Background()
	fn := func() (int, error) {
		return 0, errors.New("fail")
	}
	result, err := Execute(r, ctx, logger.Infof, "test-max-attempts", fn)
	require.ErrorIs(t, err, ErrExecutionFails)
	require.Equal(t, 0, result)
}

func TestExecute_ContextCancelled(t *testing.T) {
	r := &RetryDelays{
		Delays:      []types.Duration{{Duration: 50 * time.Millisecond}},
		MaxAttempts: 3,
	}
	logger := log.WithFields("module", "ut")
	ctx, cancel := context.WithCancel(context.Background())
	fn := func() (int, error) {
		cancel() // cancel context on first call
		return 0, errors.New("fail")
	}
	result, err := Execute(r, ctx, logger.Infof, "test-context-cancel", fn)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 0, result)
}

func TestExecute_NilRetryDelays(t *testing.T) {
	logger := log.WithFields("module", "ut")
	ctx := context.Background()
	fn := func() (int, error) {
		return 1, nil
	}
	result, err := Execute[int](nil, ctx, logger.Infof, "test-nil-retrydelays", fn)
	require.ErrorIs(t, err, ErrInvalidConfig)
	require.Equal(t, 0, result)
}

func TestExecute_NilLogger(t *testing.T) {
	r := &RetryDelays{
		Delays:      []types.Duration{{Duration: 1 * time.Millisecond}},
		MaxAttempts: 1,
	}
	ctx := context.Background()
	fn := func() (string, error) {
		return "ok", nil
	}
	result, err := Execute(r, ctx, nil, "test-nil-logger", fn)
	require.NoError(t, err)
	require.Equal(t, "ok", result)
}
func TestExecute_ErrAbort(t *testing.T) {
	r := &RetryDelays{
		Delays:      []types.Duration{{Duration: 10 * time.Millisecond}, {Duration: 10 * time.Millisecond}},
		MaxAttempts: 30,
	}
	logger := log.WithFields("module", "ut")
	ctx := context.Background()
	attempts := 0
	fn := func() (string, error) {
		attempts++
		if attempts == 2 {
			return "", fmt.Errorf("%w: custom abort", ErrAbort)
		}
		return "", errors.New("fail")
	}
	result, err := Execute(r, ctx, logger.Infof, "test-err-abort", fn)
	require.ErrorIs(t, err, ErrAbort)
	require.Equal(t, "", result)
	require.Equal(t, 2, attempts)
}
