package common

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShouldLogRetryAtError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		attempt int
		want    bool
	}{
		{name: "attempt 0 is not logged at error", attempt: 0, want: false},
		{name: "negative attempt is not logged at error", attempt: -1, want: false},
		{name: "attempt 1 is within the initial burst", attempt: 1, want: true},
		{name: "attempt 5 is the last of the initial burst", attempt: 5, want: true},
		{name: "attempt 6 is past the initial burst", attempt: 6, want: false},
		{name: "attempt 99 is not a multiple of the interval", attempt: 99, want: false},
		{name: "attempt 100 matches the interval", attempt: 100, want: true},
		{name: "attempt 199 is not a multiple of the interval", attempt: 199, want: false},
		{name: "attempt 200 matches the interval", attempt: 200, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, ShouldLogRetryAtError(tt.attempt))
		})
	}
}
