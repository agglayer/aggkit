package common

import (
	"context"
	"fmt"
	"time"
)

var (
	ErrTimeoutReached = fmt.Errorf("timeout reached")
)

// It execute 'checkCondition' each pollingPeriod, until either the condition is met,
// the timeoutPeriod is reached, or the context is done.
// It returns true if the condition is met, false if timeout is reached, or an error.
func PollingWithTimeout(
	ctx context.Context,
	pollingPeriod, timeoutPeriod time.Duration,
	checkCondition func() (bool, error)) (bool, error) {
	timeoutTimer := time.NewTimer(timeoutPeriod)
	defer timeoutTimer.Stop()
	waitingForCondition := true
	for waitingForCondition {
		pollingTimer := time.NewTimer(pollingPeriod)
		conditionMet, err := checkCondition()
		if err != nil {
			return false, err
		}
		if conditionMet {
			waitingForCondition = false
			pollingTimer.Stop()
			return true, nil
		}
		select {
		case <-pollingTimer.C:
			pollingTimer.Stop()
			// Loop continues to check condition

		case <-timeoutTimer.C:
			pollingTimer.Stop()
			return false, fmt.Errorf("pollingWithTimeout: condition not met after waiting %s: %w",
				timeoutPeriod.String(), ErrTimeoutReached)
		case <-ctx.Done():
			pollingTimer.Stop()
			return false, fmt.Errorf("pollingWithTimeout: "+
				"context done while waiting for condition to be met: %w",
				ctx.Err())
		}
	}
	return false, nil
}
