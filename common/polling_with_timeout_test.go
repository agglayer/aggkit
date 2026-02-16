package common

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPollingWithTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		pollingPeriod      time.Duration
		timeoutPeriod      time.Duration
		setupCheckFunction func() func() (bool, error)
		setupContext       func() context.Context
		expectedResult     bool
		expectedError      error
		expectedErrorMsg   string
	}{
		{
			name:          "condition met immediately",
			pollingPeriod: 10 * time.Millisecond,
			timeoutPeriod: 100 * time.Millisecond,
			setupCheckFunction: func() func() (bool, error) {
				return func() (bool, error) {
					return true, nil
				}
			},
			setupContext:   context.Background,
			expectedResult: true,
			expectedError:  nil,
		},
		{
			name:          "condition met after several attempts",
			pollingPeriod: 10 * time.Millisecond,
			timeoutPeriod: 200 * time.Millisecond,
			setupCheckFunction: func() func() (bool, error) {
				attempts := 0
				return func() (bool, error) {
					attempts++
					if attempts >= 3 {
						return true, nil
					}
					return false, nil
				}
			},
			setupContext:   context.Background,
			expectedResult: true,
			expectedError:  nil,
		},
		{
			name:          "timeout reached",
			pollingPeriod: 10 * time.Millisecond,
			timeoutPeriod: 50 * time.Millisecond,
			setupCheckFunction: func() func() (bool, error) {
				return func() (bool, error) {
					return false, nil
				}
			},
			setupContext:     context.Background,
			expectedResult:   false,
			expectedError:    ErrTimeoutReached,
			expectedErrorMsg: "pollingWithTimeout: condition not met after waiting",
		},
		{
			name:          "context cancelled",
			pollingPeriod: 10 * time.Millisecond,
			timeoutPeriod: 500 * time.Millisecond,
			setupCheckFunction: func() func() (bool, error) {
				return func() (bool, error) {
					return false, nil
				}
			},
			setupContext: func() context.Context {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
				// Don't cancel here, let the test run and timeout naturally
				_ = cancel
				return ctx
			},
			expectedResult:   false,
			expectedError:    context.DeadlineExceeded,
			expectedErrorMsg: "context done while waiting for condition to be met",
		},
		{
			name:          "check function returns error",
			pollingPeriod: 10 * time.Millisecond,
			timeoutPeriod: 100 * time.Millisecond,
			setupCheckFunction: func() func() (bool, error) {
				testErr := errors.New("check function error")
				return func() (bool, error) {
					return false, testErr
				}
			},
			setupContext:     context.Background,
			expectedResult:   false,
			expectedErrorMsg: "check function error",
		},
		{
			name:          "condition met on last attempt before timeout",
			pollingPeriod: 20 * time.Millisecond,
			timeoutPeriod: 100 * time.Millisecond,
			setupCheckFunction: func() func() (bool, error) {
				attempts := 0
				return func() (bool, error) {
					attempts++
					// Meet condition after ~80ms (4 attempts * 20ms)
					if attempts >= 4 {
						return true, nil
					}
					return false, nil
				}
			},
			setupContext:   context.Background,
			expectedResult: true,
			expectedError:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := tt.setupContext()
			checkFunc := tt.setupCheckFunction()

			result, err := PollingWithTimeout(ctx, tt.pollingPeriod, tt.timeoutPeriod, checkFunc)

			require.Equal(t, tt.expectedResult, result)

			if tt.expectedError != nil {
				require.Error(t, err)
				require.ErrorIs(t, err, tt.expectedError)
			}

			if tt.expectedErrorMsg != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.expectedErrorMsg)
			}

			if tt.expectedError == nil && tt.expectedErrorMsg == "" {
				require.NoError(t, err)
			}
		})
	}
}

func TestPollingWithTimeout_Timing(t *testing.T) {
	t.Parallel()

	t.Run("respects polling period", func(t *testing.T) {
		t.Parallel()

		pollingPeriod := 50 * time.Millisecond
		timeoutPeriod := 500 * time.Millisecond
		attempts := 0
		start := time.Now()

		checkFunc := func() (bool, error) {
			attempts++
			if attempts >= 3 {
				return true, nil
			}
			return false, nil
		}

		result, err := PollingWithTimeout(context.Background(), pollingPeriod, timeoutPeriod, checkFunc)

		elapsed := time.Since(start)

		require.NoError(t, err)
		require.True(t, result)
		require.Equal(t, 3, attempts)
		// Should take at least 2 polling periods (between attempt 1 and 3)
		require.GreaterOrEqual(t, elapsed, 2*pollingPeriod)
		// But not more than timeout
		require.Less(t, elapsed, timeoutPeriod)
	})

	t.Run("timeout is enforced", func(t *testing.T) {
		t.Parallel()

		pollingPeriod := 20 * time.Millisecond
		timeoutPeriod := 100 * time.Millisecond
		start := time.Now()

		checkFunc := func() (bool, error) {
			return false, nil
		}

		result, err := PollingWithTimeout(context.Background(), pollingPeriod, timeoutPeriod, checkFunc)

		elapsed := time.Since(start)

		require.Error(t, err)
		require.False(t, result)
		require.ErrorIs(t, err, ErrTimeoutReached)
		// Should take approximately the timeout period
		require.GreaterOrEqual(t, elapsed, timeoutPeriod)
		// Allow some margin for timing variance (20ms)
		require.Less(t, elapsed, timeoutPeriod+20*time.Millisecond)
	})
}
