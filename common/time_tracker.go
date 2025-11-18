package common

import (
	"strconv"
	"time"

	"github.com/agglayer/aggkit/log"
)

type TimeTracker struct {
	start time.Time
	end   time.Time

	times        uint32
	lastDuration time.Duration
	accumulated  time.Duration
}

func (t *TimeTracker) String() string {
	return "TimeTracker{times=" + strconv.Itoa(int(t.times)) +
		"lastDuration=" + t.lastDuration.String() +
		", accumulated=" + t.accumulated.String() +
		"}"
}

func NewTimeTracker() *TimeTracker {
	return &TimeTracker{
		start: time.Now(),
	}
}

// Elapsed returns the elapsed time since Start was called
func (t *TimeTracker) Elapsed() time.Duration {
	if t.start.IsZero() {
		log.Error("TimeTracker: Elapsed called before Start")
		return 0
	}
	// If the tracker is stopped returns last duration
	if !t.end.IsZero() {
		return t.Duration()
	}
	return time.Since(t.start)
}

// Duration returns the duration between Start and Stop (must be stopeed)
func (t *TimeTracker) Duration() time.Duration {
	if t.start.IsZero() {
		log.Error("TimeTracker: Duration called before Start")
		return 0
	}
	return t.end.Sub(t.start)
}
func (t *TimeTracker) TotalDuration() time.Duration {
	return t.accumulated
}

func (t *TimeTracker) Stop() {
	if t.end.IsZero() {
		t.end = time.Now()
		t.lastDuration = t.end.Sub(t.start)
		t.accumulated += t.lastDuration
		t.times++
	}
}
func (t *TimeTracker) Start() {
	t.start = time.Now()
	t.end = time.Time{}
}
