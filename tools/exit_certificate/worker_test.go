package exit_certificate

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunWorkerPoolEmpty(t *testing.T) {
	t.Parallel()
	called := false
	err := runWorkerPool(context.Background(), []int{}, 4,
		func(j int) (int, error) { called = true; return j, nil },
		func(int) {}, "")
	require.NoError(t, err)
	require.False(t, called, "fn must not be called for an empty job list")
}

func TestRunWorkerPoolSuccess(t *testing.T) {
	t.Parallel()
	jobs := make([]int, 100)
	for i := range jobs {
		jobs[i] = i
	}

	var mu sync.Mutex
	var got []int
	err := runWorkerPool(context.Background(), jobs, 8,
		func(j int) (int, error) { return j * 2, nil },
		func(r int) { mu.Lock(); got = append(got, r); mu.Unlock() },
		"double")
	require.NoError(t, err)
	require.Len(t, got, len(jobs))

	sort.Ints(got)
	for i := range jobs {
		require.Equal(t, jobs[i]*2, got[i])
	}
}

func TestRunWorkerPoolPropagatesError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("boom")
	jobs := []int{1, 2, 3, 4, 5}

	err := runWorkerPool(context.Background(), jobs, 2,
		func(j int) (int, error) {
			if j == 3 {
				return 0, wantErr
			}
			return j, nil
		},
		func(int) {}, "maybe-fail")
	require.Error(t, err)
	require.ErrorIs(t, err, wantErr)
}

func TestRunWorkerPoolContextCanceled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel up front

	jobs := make([]int, 1000)
	err := runWorkerPool(ctx, jobs, 4,
		func(j int) (int, error) { return j, nil },
		func(int) {}, "cancelled")
	// Either a clean drain returning ctx.Err, or nil if everything happened to complete first.
	if err != nil {
		require.ErrorIs(t, err, context.Canceled)
	}
}
