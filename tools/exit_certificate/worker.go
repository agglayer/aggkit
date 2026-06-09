package exit_certificate

import (
	"context"
	"errors"
	"sync"

	"github.com/agglayer/aggkit/log"
)

const (
	workerPoolChannelCap    = 10000
	resultChannelMultiplier = 2
	logGranularity          = 20
	percentMultiplier       = 100
)

type workerResult[R any] struct {
	val R
	err error
}

// runWorkerPool fans out work across `concurrency` goroutines.
// It feeds `jobs` into a channel, workers call `fn` for each job, and results
// are collected via `collect`. Progress is logged at ~5% intervals.
// When ctx is cancelled, the feeder and workers stop immediately and
// collectResults returns as soon as the last in-flight result is received.
//
// This is the single concurrency primitive used by all steps, replacing
// duplicated goroutine+channel boilerplate.
func runWorkerPool[J any, R any](
	ctx context.Context,
	jobs []J,
	concurrency int,
	fn func(J) (R, error),
	collect func(R),
	label string,
) error {
	if len(jobs) == 0 {
		return nil
	}

	resultCh := startWorkers(ctx, jobs, concurrency, fn)
	return collectResults(ctx, resultCh, len(jobs), collect, label)
}

func startWorkers[J any, R any](
	ctx context.Context,
	jobs []J,
	concurrency int,
	fn func(J) (R, error),
) <-chan workerResult[R] {
	jobCh := make(chan J, min(len(jobs), workerPoolChannelCap))
	go func() {
		defer close(jobCh)
		for _, j := range jobs {
			select {
			case jobCh <- j:
			case <-ctx.Done():
				return
			}
		}
	}()

	resultCh := make(chan workerResult[R], concurrency*resultChannelMultiplier)
	var wg sync.WaitGroup
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case j, ok := <-jobCh:
					if !ok {
						return
					}
					val, err := fn(j)
					resultCh <- workerResult[R]{val: val, err: err}
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	return resultCh
}

func collectResults[R any](
	ctx context.Context,
	resultCh <-chan workerResult[R],
	total int,
	collect func(R),
	label string,
) error {
	logInterval := total / logGranularity
	if logInterval < 1 {
		logInterval = 1
	}

	processed := 0
	var firstErr error
	for {
		select {
		case <-ctx.Done():
			// Drain resultCh synchronously so all in-flight workers finish before we return.
			// A background drain would let workers mutate captured state after the caller returns.
			for range resultCh {
			}
			if firstErr != nil {
				return firstErr
			}
			return ctx.Err()
		case r, ok := <-resultCh:
			if !ok {
				return firstErr
			}
			processed++
			if r.err != nil {
				if firstErr == nil {
					firstErr = r.err
				}
				// Skip context.Canceled: it's the expected fallout of cancelling the pool after a
				// real failure (the root-cause error is kept in firstErr), not noise worth logging.
				if !errors.Is(r.err, context.Canceled) {
					log.Warnf("%s job failed: %v req: %+v", label, r.err, r.val)
				}
			} else {
				collect(r.val)
				if label != "" && (processed%logInterval == 0 || processed == total) {
					pct := float64(processed) / float64(total) * percentMultiplier
					log.Infof("  %s: %d/%d [%.0f%%]", label, processed, total, pct)
				}
			}
		}
	}
}
