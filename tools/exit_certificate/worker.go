package exit_certificate

import (
	"sync"

	"github.com/agglayer/aggkit/log"
)

const workerPoolChannelCap = 10000

// runWorkerPool fans out work across `concurrency` goroutines.
// It feeds `jobs` into a channel, workers call `fn` for each job, and results
// are collected via `collect`. Progress is logged at ~5% intervals.
//
// This is the single concurrency primitive used by all steps, replacing
// duplicated goroutine+channel boilerplate.
func runWorkerPool[J any, R any](
	jobs []J,
	concurrency int,
	fn func(J) (R, error),
	collect func(R),
	label string,
) error {
	if len(jobs) == 0 {
		return nil
	}

	type result struct {
		val R
		err error
	}

	jobCh := make(chan J, min(len(jobs), workerPoolChannelCap))
	go func() {
		for _, j := range jobs {
			jobCh <- j
		}
		close(jobCh)
	}()

	resultCh := make(chan result, concurrency*2)
	var wg sync.WaitGroup
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobCh {
				val, err := fn(j)
				resultCh <- result{val: val, err: err}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	total := len(jobs)
	logInterval := total / 20
	if logInterval < 1 {
		logInterval = 1
	}

	processed := 0
	var firstErr error
	for r := range resultCh {
		processed++
		if r.err != nil {
			if firstErr == nil {
				firstErr = r.err
			}
			log.Warnf("%s job failed: %v", label, r.err)
			continue
		}
		collect(r.val)

		if processed%logInterval == 0 || processed == total {
			pct := float64(processed) / float64(total) * 100
			log.Infof("  %s: %d/%d [%.0f%%]", label, processed, total, pct)
		}
	}

	return firstErr
}
