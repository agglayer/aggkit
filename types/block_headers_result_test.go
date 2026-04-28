package types

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBlockHeadersResult_Success(t *testing.T) {
	t.Run("empty result is success", func(t *testing.T) {
		r := NewBlockHeadersResult()
		require.True(t, r.Success())
	})
	t.Run("result with headers and no errors is success", func(t *testing.T) {
		r := NewBlockHeadersResult()
		r.AddHeader(1, &BlockHeader{Number: 1})
		require.True(t, r.Success())
	})
	t.Run("result with any error is not success", func(t *testing.T) {
		r := NewBlockHeadersResult()
		r.AddHeader(1, &BlockHeader{Number: 1})
		r.AddError(2, errors.New("timeout"))
		require.False(t, r.Success())
	})
}

func TestBlockHeadersResult_PartialSuccess(t *testing.T) {
	t.Run("no headers is not partial success", func(t *testing.T) {
		r := NewBlockHeadersResult()
		r.AddError(1, errors.New("err"))
		require.False(t, r.PartialSuccess())
	})
	t.Run("at least one header is partial success", func(t *testing.T) {
		r := NewBlockHeadersResult()
		r.AddHeader(1, &BlockHeader{Number: 1})
		r.AddError(2, errors.New("err"))
		require.True(t, r.PartialSuccess())
	})
}

func TestBlockHeadersResult_GetOrderedHeaders(t *testing.T) {
	r := NewBlockHeadersResult()
	r.AddHeader(10, &BlockHeader{Number: 10})
	r.AddHeader(20, &BlockHeader{Number: 20})
	r.AddError(30, errors.New("err"))

	ordered := r.GetOrderedHeaders([]uint64{30, 10, 20})
	require.Len(t, ordered, 2)
	assert.Equal(t, uint64(10), ordered[0].Number)
	assert.Equal(t, uint64(20), ordered[1].Number)
}

func TestBlockHeadersResult_Merge(t *testing.T) {
	a := NewBlockHeadersResult()
	a.AddHeader(1, &BlockHeader{Number: 1})
	a.AddError(2, errors.New("err-a"))

	b := NewBlockHeadersResult()
	b.AddHeader(3, &BlockHeader{Number: 3})
	b.AddError(4, errors.New("err-b"))

	a.Merge(b)
	require.Len(t, a.Headers, 2)
	require.Len(t, a.Errors, 2)
	assert.NotNil(t, a.Headers[3])
	assert.NotNil(t, a.Errors[4])
}

func TestBlockHeadersResult_ComposeError(t *testing.T) {
	t.Run("nil when no errors", func(t *testing.T) {
		r := NewBlockHeadersResult()
		r.AddHeader(1, &BlockHeader{Number: 1})
		require.NoError(t, r.ComposeError())
	})

	t.Run("includes not-found errors", func(t *testing.T) {
		r := NewBlockHeadersResult()
		r.AddError(100, ErrNotFound)
		r.AddError(200, ErrNotFound)
		err := r.ComposeError()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Block 100")
		assert.Contains(t, err.Error(), "Block 200")
	})

	t.Run("includes non-not-found errors", func(t *testing.T) {
		r := NewBlockHeadersResult()
		r.AddError(50, errors.New("rpc timeout"))
		r.AddError(100, ErrNotFound)
		err := r.ComposeError()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Block 50")
		assert.Contains(t, err.Error(), "rpc timeout")
		assert.Contains(t, err.Error(), "Block 100")
	})

	t.Run("errors are ordered by block number", func(t *testing.T) {
		r := NewBlockHeadersResult()
		r.AddError(300, errors.New("err-300"))
		r.AddError(100, errors.New("err-100"))
		r.AddError(200, errors.New("err-200"))
		err := r.ComposeError()
		require.Error(t, err)
		msg := err.Error()
		pos100 := findPos(msg, "Block 100")
		pos200 := findPos(msg, "Block 200")
		pos300 := findPos(msg, "Block 300")
		assert.Less(t, pos100, pos200)
		assert.Less(t, pos200, pos300)
	})

	t.Run("wraps all errors for errors.Is", func(t *testing.T) {
		r := NewBlockHeadersResult()
		r.AddError(1, ErrNotFound)
		err := r.ComposeError()
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrNotFound))
	})
}

func findPos(s, substr string) int {
	for i := range s {
		if len(s[i:]) >= len(substr) && s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
