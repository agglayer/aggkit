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
	r := NewRetryHandler(
		[]types.Duration{{Duration: 10 * time.Millisecond}},
		3)

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
	r := NewRetryHandler([]types.Duration{{Duration: 10 * time.Millisecond}, {Duration: 10 * time.Millisecond}}, 3)

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
	r := NewRetryHandler([]types.Duration{{Duration: 5 * time.Millisecond}}, 2)

	attempts := 0
	fn := func() (int, error) {
		attempts++
		return 0, errors.New("fail")
	}
	result, err := Execute(r, t.Context(), log.Infof, "test-max-attempts", fn)
	require.Equal(t, 3, attempts, "first call + 2 retries")
	require.ErrorIs(t, err, ErrExecutionFails)
	require.Equal(t, 0, result)
}

func TestExecute_ContextCancelled(t *testing.T) {
	r := NewRetryHandler([]types.Duration{{Duration: 50 * time.Millisecond}}, 3)
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
	returnErr := errors.New("fail")
	attempts := 0
	fn := func() (int, error) {
		attempts++
		return 0, returnErr
	}
	result, err := Execute(nil, ctx, logger.Infof, "test-nil-retrydelays", fn)
	require.ErrorIs(t, err, returnErr)
	require.Equal(t, 0, result)
	require.Equal(t, 1, attempts) // should only call once since no retry delays are defined
}

func TestExecute_MaxRetriesZero(t *testing.T) {
	r := NewRetryHandler([]types.Duration{}, 0)
	returnErr := errors.New("fail")
	attempts := 0
	fn := func() (int, error) {
		attempts++
		return 0, returnErr
	}
	require.NoError(t, r.Validate())
	result, err := Execute(r, t.Context(), log.Infof, "test-nil-retrydelays", fn)
	require.Equal(t, 1, attempts) // should only call once since no retry delays are defined
	require.ErrorIs(t, err, returnErr)
	require.Equal(t, 0, result)
}

func TestExecute_NilLogger(t *testing.T) {
	r := NewRetryHandler([]types.Duration{{Duration: 1 * time.Millisecond}}, 1)
	fn := func() (string, error) {
		return "ok", nil
	}
	result, err := Execute(r, t.Context(), nil, "test-nil-logger", fn)
	require.NoError(t, err)
	require.Equal(t, "ok", result)
}

func TestExecute_ErrAbort(t *testing.T) {
	r := NewRetryHandler(
		[]types.Duration{{Duration: 10 * time.Millisecond}, {Duration: 1 * time.Millisecond}},
		30)
	attempts := 0
	fn := func() (string, error) {
		attempts++
		if attempts == 2 {
			return "", fmt.Errorf("%w: custom abort", ErrAbort)
		}
		return "", errors.New("fail")
	}
	result, err := Execute(r, t.Context(), log.Infof, "test-err-abort", fn)
	require.ErrorIs(t, err, ErrAbort)
	require.Equal(t, "", result)
	require.Equal(t, 2, attempts)
}

func TestRetryDelays_BadConfig(t *testing.T) {
	r := NewRetryHandler([]types.Duration{}, -1)
	err := r.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "retry delays cannot be empty if there are retries")
}
