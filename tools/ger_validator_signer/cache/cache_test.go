package cache

import (
	"errors"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

var errRejected = errors.New("rejected")

func ger(n byte) common.Hash { return common.Hash{n} }

// withClock returns a cache whose clock is controlled by the test
func withClock(maxEntries int, now *time.Time) *Cache {
	c := New(maxEntries)
	c.now = func() time.Time { return *now }
	return c
}

func TestCache_Signature(t *testing.T) {
	c := New(10)

	_, ok := c.Get(ger(1))
	require.False(t, ok)

	c.PutSignature(ger(1), []byte{1, 2, 3})
	entry, ok := c.Get(ger(1))
	require.True(t, ok)
	require.Equal(t, []byte{1, 2, 3}, entry.Signature)
	require.NoError(t, entry.Err)
}

func TestCache_SignatureIsCopied(t *testing.T) {
	c := New(10)
	signature := []byte{1, 2, 3}
	c.PutSignature(ger(1), signature)

	signature[0] = 99 // the caller modifies what it gave
	entry, _ := c.Get(ger(1))
	require.Equal(t, []byte{1, 2, 3}, entry.Signature)

	entry.Signature[1] = 99 // and what it got
	again, _ := c.Get(ger(1))
	require.Equal(t, []byte{1, 2, 3}, again.Signature)
}

func TestCache_NegativeAnswersExpire(t *testing.T) {
	now := time.Unix(1000, 0)
	c := withClock(10, &now)

	c.PutError(ger(1), errRejected, 5*time.Second)
	entry, ok := c.Get(ger(1))
	require.True(t, ok)
	require.Nil(t, entry.Signature)
	require.ErrorIs(t, entry.Err, errRejected)

	now = now.Add(4999 * time.Millisecond)
	_, ok = c.Get(ger(1))
	require.True(t, ok, "not expired yet")

	now = now.Add(time.Millisecond) // exactly the TTL
	_, ok = c.Get(ger(1))
	require.False(t, ok, "expired")
	require.Zero(t, c.Len(), "the expired entry is removed")
}

func TestCache_SignaturesNeverExpire(t *testing.T) {
	now := time.Unix(1000, 0)
	c := withClock(10, &now)
	c.PutSignature(ger(1), []byte{1})

	now = now.Add(1000 * time.Hour)
	_, ok := c.Get(ger(1))
	require.True(t, ok)
}

func TestCache_PutErrorWithoutTTLStoresNothing(t *testing.T) {
	c := New(10)
	c.PutError(ger(1), errRejected, 0)
	c.PutError(ger(2), errRejected, -time.Second)
	require.Zero(t, c.Len())
}

func TestCache_SignatureAndNegativeAnswers(t *testing.T) {
	t.Run("a signature replaces a negative answer", func(t *testing.T) {
		c := New(10)
		c.PutError(ger(1), errRejected, time.Hour)
		c.PutSignature(ger(1), []byte{7})

		entry, ok := c.Get(ger(1))
		require.True(t, ok)
		require.Equal(t, []byte{7}, entry.Signature)
		require.NoError(t, entry.Err)
		require.Equal(t, 1, c.Len())
	})

	t.Run("a negative answer never replaces a signature", func(t *testing.T) {
		c := New(10)
		c.PutSignature(ger(1), []byte{7})
		c.PutError(ger(1), errRejected, time.Hour)

		entry, ok := c.Get(ger(1))
		require.True(t, ok)
		require.Equal(t, []byte{7}, entry.Signature)
		require.NoError(t, entry.Err)
	})

	t.Run("a new negative answer replaces the previous one", func(t *testing.T) {
		c := New(10)
		other := errors.New("other")
		c.PutError(ger(1), errRejected, time.Hour)
		c.PutError(ger(1), other, time.Hour)

		entry, _ := c.Get(ger(1))
		require.ErrorIs(t, entry.Err, other)
		require.Equal(t, 1, c.Len())
	})
}

func TestCache_EvictsTheOldest(t *testing.T) {
	c := New(3)
	for n := byte(1); n <= 4; n++ {
		c.PutSignature(ger(n), []byte{n})
	}

	require.Equal(t, 3, c.Len())
	_, ok := c.Get(ger(1))
	require.False(t, ok, "the oldest one was evicted")
	for n := byte(2); n <= 4; n++ {
		_, ok := c.Get(ger(n))
		require.True(t, ok, "GER %d", n)
	}
}

func TestCache_UpdatingMakesAnEntryTheNewest(t *testing.T) {
	c := New(2)
	c.PutSignature(ger(1), []byte{1})
	c.PutSignature(ger(2), []byte{2})
	c.PutSignature(ger(1), []byte{1}) // 1 is the newest now
	c.PutSignature(ger(3), []byte{3}) // evicts the oldest: 2

	_, ok := c.Get(ger(2))
	require.False(t, ok)
	_, ok = c.Get(ger(1))
	require.True(t, ok)
	_, ok = c.Get(ger(3))
	require.True(t, ok)
}

func TestCache_Concurrency(t *testing.T) {
	c := New(50)
	done := make(chan struct{})
	for worker := range 8 {
		go func() {
			defer func() { done <- struct{}{} }()
			for i := range 500 {
				g := ger(byte((worker*31 + i) % 100))
				c.PutSignature(g, []byte{byte(i)})
				c.PutError(ger(byte(i%100)), errRejected, time.Minute)
				c.Get(g)
				c.Len()
			}
		}()
	}
	for range 8 {
		<-done
	}
	require.LessOrEqual(t, c.Len(), 50)
}
