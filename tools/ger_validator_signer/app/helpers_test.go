package app

import (
	"io"
	"log/slog"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
)

// bigInt is an alias to keep the assertions short
type bigInt = big.Int

// stringField returns a field of a decoded JSON object, that must be a string
func stringField(t *testing.T, object map[string]any, key string) string {
	t.Helper()
	value, ok := object[key].(string)
	require.True(t, ok, "the field %q is not a string: %v", key, object[key])
	return value
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
