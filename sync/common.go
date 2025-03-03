package sync

import (
	"log"
	"sync"
	"time"
)

var LogFatalf = log.Fatalf

type RetryHandler struct {
	RetryAfterErrorPeriod      time.Duration
	MaxRetryAttemptsAfterError int
}

// Handle is a method that handles retries
// If reach max retry attempts, it will log.Fatalf
// Otherwise, it will sleep for RetryAfterErrorPeriod
// For be able to test it, the Fatalf function can be override
// with var LogFatalf and change it for a panic that be catched by the test
func (h *RetryHandler) Handle(funcName string, attempts int) {
	if h.MaxRetryAttemptsAfterError > -1 && attempts >= h.MaxRetryAttemptsAfterError {
		LogFatalf(
			"%s failed too many times (%d)",
			funcName, h.MaxRetryAttemptsAfterError,
		)
	}
	time.Sleep(h.RetryAfterErrorPeriod)
}

func UnhaltIfAffectedRows(halted *bool, haltedReason *string, mu *sync.RWMutex, rowsAffected int64) {
	if rowsAffected > 0 {
		mu.Lock()
		defer mu.Unlock()
		*halted = false
		*haltedReason = ""
	}
}
