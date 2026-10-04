package common

import (
	"regexp"
	"sync"
	"time"

	"github.com/agglayer/aggkit/log"
)

// reVariablePart matches the parts of an error message that usually change between attempts of the same
// failure (block numbers, ranges, hashes, request ids), so they do not make each retry a "distinct" error.
var reVariablePart = regexp.MustCompile(`0x[0-9a-fA-F]+|\d+`)

// RepeatedErrorLogger bounds the log volume of an error that may repeat indefinitely in a retry loop.
// Messages are compared after replacing numbers and hex strings, so errors that only differ in them are
// considered the same. The first occurrence of a distinct message is logged at Warn, repeats at Debug,
// and a Warn summary carrying the number of occurrences is emitted once per window.
type RepeatedErrorLogger struct {
	log        Logger
	prefix     string
	window     time.Duration
	now        func() time.Time
	mu         sync.Mutex
	lastMsg    string // normalised key of the last error
	lastErr    string // text of the last error, for the Reset summary
	hasLast    bool
	suppressed int
	lastWarn   time.Time
}

// NewRepeatedErrorLogger creates a RepeatedErrorLogger. A nil logger is replaced by a no-op logger.
func NewRepeatedErrorLogger(logger Logger, prefix string, window time.Duration) *RepeatedErrorLogger {
	if logger == nil {
		logger = log.NewLoggerNil()
	}
	return &RepeatedErrorLogger{
		log:    logger,
		prefix: prefix,
		window: window,
		now:    time.Now,
	}
}

// WithClock replaces the clock (tests); it returns the receiver.
func (l *RepeatedErrorLogger) WithClock(now func() time.Time) *RepeatedErrorLogger {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now != nil {
		l.now = now
	}
	return l
}

// Log records one occurrence of err. A nil err is ignored.
func (l *RepeatedErrorLogger) Log(err error) {
	if err == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	key := reVariablePart.ReplaceAllString(err.Error(), "#")
	now := l.now()
	l.lastErr = err.Error()
	switch {
	case !l.hasLast || key != l.lastMsg:
		l.log.Warnf("%s: %v", l.prefix, err)
		l.lastMsg, l.hasLast = key, true
		l.suppressed = 0
		l.lastWarn = now
	case now.Sub(l.lastWarn) >= l.window:
		l.log.Warnf("%s: %v (repeated %d times in the last %s)",
			l.prefix, err, l.suppressed+1, now.Sub(l.lastWarn).Truncate(time.Second))
		l.suppressed = 0
		l.lastWarn = now
	default:
		l.log.Debugf("%s: %v", l.prefix, err)
		l.suppressed++
	}
}

// Reset forgets the current error (call after a success) so the next error is logged at Warn again.
// If occurrences were suppressed since the last Warn, it emits one Warn summary with their count;
// otherwise it logs nothing.
func (l *RepeatedErrorLogger) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.hasLast && l.suppressed > 0 {
		l.log.Warnf("%s: %q repeated %d more times in the last %s before recovering",
			l.prefix, l.lastErr, l.suppressed, l.now().Sub(l.lastWarn).Truncate(time.Second))
	}
	l.hasLast = false
	l.lastMsg = ""
	l.suppressed = 0
}
