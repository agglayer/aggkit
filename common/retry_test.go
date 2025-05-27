package common

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRetryWithExponentialBackoff(t *testing.T) {
	tests := []struct {
		name           string
		maxRetries     uint
		initialDelay   time.Duration
		ctxTimeout     time.Duration
		callback       func() func() error
		expectedErrMsg string
	}{
		{
			name:         "succeeds immediately",
			maxRetries:   3,
			initialDelay: 10 * time.Millisecond,
			callback: func() func() error {
				return func() error { return nil }
			},
		},
		{
			name:         "succeeds after retries",
			maxRetries:   3,
			initialDelay: 10 * time.Millisecond,
			callback: func() func() error {
				attempts := 0
				return func() error {
					attempts++
					if attempts < 3 {
						return errors.New("temporary failure")
					}
					return nil
				}
			},
		},
		{
			name:         "fails with retryable errors",
			maxRetries:   3,
			initialDelay: 5 * time.Millisecond,
			callback: func() func() error {
				return func() error {
					return errors.New("always fails")
				}
			},
			expectedErrMsg: "operation failed after 3 retries",
		},
		{
			name:         "aborts on non-retryable error",
			maxRetries:   5,
			initialDelay: 5 * time.Millisecond,
			callback: func() func() error {
				attempts := 0
				return func() error {
					attempts++
					if attempts == 2 {
						return fmt.Errorf("wrapper: %w", ErrNonRetryable)
					}
					return errors.New("transient")
				}
			},
			expectedErrMsg: "non-retryable error after 2 attempt(s)",
		},
		{
			name:         "context cancelled before completion",
			maxRetries:   5,
			initialDelay: 100 * time.Millisecond,
			ctxTimeout:   50 * time.Millisecond,
			callback: func() func() error {
				return func() error {
					return errors.New("will timeout")
				}
			},
			expectedErrMsg: "retry cancelled after",
		},
		{
			name:           "nil callback returns error",
			maxRetries:     3,
			initialDelay:   10 * time.Millisecond,
			callback:       nil,
			expectedErrMsg: "retry callback cannot be nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ctx context.Context
			if tt.ctxTimeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(context.Background(), tt.ctxTimeout)
				defer cancel()
			} else {
				ctx = context.Background()
			}

			var fn func() error
			if tt.callback != nil {
				fn = tt.callback()
			}

			err := RetryWithExponentialBackoff(ctx, tt.maxRetries, tt.initialDelay, fn)

			if tt.expectedErrMsg != "" {
				require.ErrorContains(t, err, tt.expectedErrMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
