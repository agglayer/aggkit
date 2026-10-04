package common

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type loggedLine struct {
	level string
	msg   string
}

// captureLogger implements Logger, recording only the Warn/Debug lines used by RepeatedErrorLogger.
type captureLogger struct {
	mu    sync.Mutex
	lines []loggedLine
}

func (c *captureLogger) add(level, format string, args ...interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, loggedLine{level, fmt.Sprintf(format, args...)})
}

func (c *captureLogger) levels() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	res := make([]string, 0, len(c.lines))
	for _, l := range c.lines {
		res = append(res, l.level)
	}
	return res
}

func (c *captureLogger) Panicf(string, ...interface{}) {}
func (c *captureLogger) Fatalf(string, ...interface{}) {}
func (c *captureLogger) Info(...interface{})           {}
func (c *captureLogger) Infof(string, ...interface{})  {}
func (c *captureLogger) Error(...interface{})          {}
func (c *captureLogger) Errorf(string, ...interface{}) {}
func (c *captureLogger) Warn(args ...interface{})      { c.add("warn", "%s", fmt.Sprint(args...)) }
func (c *captureLogger) Warnf(f string, a ...interface{}) {
	c.add("warn", f, a...)
}
func (c *captureLogger) Debug(args ...interface{}) { c.add("debug", "%s", fmt.Sprint(args...)) }
func (c *captureLogger) Debugf(f string, a ...interface{}) {
	c.add("debug", f, a...)
}

func TestRepeatedErrorLogger_LevelSequence(t *testing.T) {
	logger := &captureLogger{}
	now := time.Unix(1000, 0)
	rl := NewRepeatedErrorLogger(logger, "step", time.Minute).WithClock(func() time.Time { return now })
	errA := errors.New("boom")

	// First occurrence: Warn; repeats inside the window: Debug.
	rl.Log(errA)
	for i := 0; i < 3; i++ {
		now = now.Add(10 * time.Second)
		rl.Log(errA)
	}
	require.Equal(t, []string{"warn", "debug", "debug", "debug"}, logger.levels())

	// Window elapsed: summary Warn with the count (3 suppressed + this one).
	now = now.Add(40 * time.Second)
	rl.Log(errA)
	levels := logger.levels()
	require.Equal(t, "warn", levels[len(levels)-1])
	require.True(t, strings.Contains(logger.lines[len(logger.lines)-1].msg, "repeated 4 times in the last 1m10s"),
		logger.lines[len(logger.lines)-1].msg)

	// Next repeat is Debug again.
	now = now.Add(time.Second)
	rl.Log(errA)
	require.Equal(t, "debug", logger.levels()[len(logger.lines)-1])

	// A distinct message is a Warn immediately.
	rl.Log(errors.New("other"))
	require.Equal(t, "warn", logger.levels()[len(logger.lines)-1])

	// After Reset the same message is a Warn again, and Reset itself is silent.
	n := len(logger.lines)
	rl.Reset()
	require.Len(t, logger.lines, n)
	rl.Log(errors.New("other"))
	require.Equal(t, "warn", logger.levels()[n])
}

func TestRepeatedErrorLogger_WarnCountEqualsWindows(t *testing.T) {
	logger := &captureLogger{}
	now := time.Unix(0, 0)
	rl := NewRepeatedErrorLogger(logger, "p", time.Minute).WithClock(func() time.Time { return now })
	err := errors.New("same")
	for i := 0; i < 301; i++ { // 5 minutes at one call per second
		rl.Log(err)
		now = now.Add(time.Second)
	}
	warns := 0
	for _, l := range logger.levels() {
		if l == "warn" {
			warns++
		}
	}
	require.Equal(t, 1+5, warns)
}

func TestRepeatedErrorLogger_NilCases(t *testing.T) {
	logger := &captureLogger{}
	NewRepeatedErrorLogger(logger, "p", time.Minute).Log(nil)
	require.Empty(t, logger.lines)

	require.NotPanics(t, func() {
		NewRepeatedErrorLogger(nil, "p", time.Minute).Log(errors.New("x"))
	})
}

func TestRepeatedErrorLogger_VaryingNumbersAreTheSameError(t *testing.T) {
	logger := &captureLogger{}
	now := time.Unix(0, 0)
	rl := NewRepeatedErrorLogger(logger, "p", time.Minute).WithClock(func() time.Time { return now })

	rl.Log(errors.New("cannot update ToBlock: block 123457 (0xabc1) not found"))
	now = now.Add(time.Second)
	rl.Log(errors.New("cannot update ToBlock: block 123458 (0xdef2) not found"))
	now = now.Add(time.Second)
	rl.Log(errors.New("cannot update ToBlock: block 123459 (0x1234) not found"))
	require.Equal(t, []string{"warn", "debug", "debug"}, logger.levels())

	// A really different message is still a Warn.
	rl.Log(errors.New("connection refused"))
	require.Equal(t, "warn", logger.levels()[3])
}

func TestRepeatedErrorLogger_ResetEmitsSummaryOfSuppressed(t *testing.T) {
	logger := &captureLogger{}
	now := time.Unix(0, 0)
	rl := NewRepeatedErrorLogger(logger, "p", time.Minute).WithClock(func() time.Time { return now })
	err := errors.New("boom")

	rl.Log(err) // warn
	rl.Log(err) // debug (suppressed 1)
	rl.Log(err) // debug (suppressed 2)
	n := len(logger.lines)
	now = now.Add(5 * time.Second)
	rl.Reset()
	require.Len(t, logger.lines, n+1)
	require.Equal(t, "warn", logger.levels()[n])
	require.Contains(t, logger.lines[n].msg, "repeated 2 more times")

	// Nothing suppressed: Reset stays silent.
	rl.Log(err)
	n = len(logger.lines)
	rl.Reset()
	require.Len(t, logger.lines, n)
}
