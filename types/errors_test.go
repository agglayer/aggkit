package types

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsErrNotFound(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "exact ErrNotFound sentinel",
			err:      ErrNotFound,
			expected: true,
		},
		{
			name:     "wrapped ErrNotFound via fmt.Errorf",
			err:      fmt.Errorf("context: %w", ErrNotFound),
			expected: true,
		},
		{
			name:     "errors.New with exact 'not found' message",
			err:      errors.New("not found"),
			expected: true,
		},
		{
			name:     "error containing 'not found' substring",
			err:      errors.New("block not found"),
			expected: true,
		},
		{
			name:     "wrapped error containing 'not found' substring",
			err:      fmt.Errorf("rpc error: %w", errors.New("block not found")),
			expected: true,
		},
		{
			name:     "unrelated error",
			err:      errors.New("connection timeout"),
			expected: false,
		},
		{
			name:     "error with 'Not Found' different case",
			err:      errors.New("Not Found"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, IsErrNotFound(tt.err))
		})
	}
}
