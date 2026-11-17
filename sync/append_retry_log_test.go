package sync

import (
	"errors"
	"fmt"
	"testing"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/stretchr/testify/require"
)

type levelRecorder struct {
	levels []string
}

func (r *levelRecorder) Debugf(string, ...interface{}) { r.levels = append(r.levels, "DEBUG") }
func (r *levelRecorder) Warnf(string, ...interface{})  { r.levels = append(r.levels, "WARN") }
func (r *levelRecorder) Errorf(string, ...interface{}) { r.levels = append(r.levels, "ERROR") }

func TestLogAppendRetry(t *testing.T) {
	notReady := fmt.Errorf("GER not in the l1 info tree (%w): not found", ErrAppendNotReadyYet)
	other := errors.New("db error")

	tests := []struct {
		name     string
		attempts int
		err      error
		expected string
	}{
		{name: "expected race is WARN", attempts: 1, err: notReady, expected: "WARN"},
		{name: "other errors are ERROR", attempts: 1, err: other, expected: "ERROR"},
		{name: "past the burst is DEBUG (not ready)", attempts: aggkitcommon.RetryErrorLogBurst + 1, err: notReady,
			expected: "DEBUG"},
		{name: "past the burst is DEBUG (other)", attempts: aggkitcommon.RetryErrorLogBurst + 1, err: other,
			expected: "DEBUG"},
		{name: "every interval is logged again", attempts: aggkitcommon.RetryErrorLogInterval, err: other,
			expected: "ERROR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &levelRecorder{}
			LogAppendRetry(r, tt.attempts, tt.err)
			require.Equal(t, []string{tt.expected}, r.levels)
		})
	}
}
