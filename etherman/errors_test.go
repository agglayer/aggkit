package etherman

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTryParseWithExactMatch(t *testing.T) {
	expected := ErrTimestampMustBeInsideRange
	smartContractErr := expected

	actualErr, ok := TryParseError(smartContractErr)

	assert.ErrorIs(t, actualErr, expected)
	assert.True(t, ok)
}

func TestTryParseWithContains(t *testing.T) {
	expected := ErrTimestampMustBeInsideRange
	smartContractErr := fmt.Errorf(" execution reverted: ProofOfEfficiency::sequenceBatches: %w", expected)

	actualErr, ok := TryParseError(smartContractErr)

	assert.ErrorIs(t, actualErr, expected)
	assert.True(t, ok)
}

func TestTryParseWithNonExistingErr(t *testing.T) {
	smartContractErr := fmt.Errorf("some non-existing err")

	actualErr, ok := TryParseError(smartContractErr)

	assert.Nil(t, actualErr)
	assert.False(t, ok)
}

func TestIsErrNotFound(t *testing.T) {
	t.Run("returns false when error is nil", func(t *testing.T) {
		result := IsErrNotFound(nil)
		require.False(t, result)
	})

	t.Run("returns true when error is ErrNotFound", func(t *testing.T) {
		result := IsErrNotFound(ErrNotFound)
		require.True(t, result)
	})

	t.Run("returns true when error is wrapped with ErrNotFound", func(t *testing.T) {
		wrappedErr := fmt.Errorf("some context: %w", ErrNotFound)
		result := IsErrNotFound(wrappedErr)
		require.True(t, result)
	})

	t.Run("returns true when error has same message as ErrNotFound", func(t *testing.T) {
		sameMessageErr := errors.New("not found")
		result := IsErrNotFound(sameMessageErr)
		require.True(t, result)
	})

	t.Run("returns false when error is different", func(t *testing.T) {
		differentErr := errors.New("some other error")
		result := IsErrNotFound(differentErr)
		require.False(t, result)
	})

	t.Run("returns false when error message is different", func(t *testing.T) {
		differentErr := ErrMissingTrieNode
		result := IsErrNotFound(differentErr)
		require.False(t, result)
	})
}
