package finality

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBoundedSet(t *testing.T) {
	set := newBoundedSet[int](3)
	require.False(t, set.contains(1))

	set.add(1)
	set.add(2)
	set.add(3)
	set.add(2) // already there: no growth
	require.Equal(t, 3, set.len())

	// 1 is the least recently used one
	set.add(4)
	require.Equal(t, 3, set.len())
	require.False(t, set.contains(1))
	require.True(t, set.contains(2))
	require.True(t, set.contains(3))
	require.True(t, set.contains(4))

	// contains refreshes: 2 was used last, so 3 goes first
	require.True(t, set.contains(2))
	set.add(5)
	require.True(t, set.contains(2))
	require.False(t, set.contains(3))
}

func TestBoundedSet_Concurrent(t *testing.T) {
	set := newBoundedSet[int](10)
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 500 {
				set.add(g*1000 + i)
				set.contains(g*1000 + i/2)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, 10, set.len())
}
