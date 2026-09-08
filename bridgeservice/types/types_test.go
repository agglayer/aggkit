package types

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBigIntString_ToBigIntRoundTrip verifies BigIntString("18446744073709551616").ToBigInt()
// (2^64, an L1-origin global index that exceeds JavaScript's Number.MAX_SAFE_INTEGER) equals the
// expected *big.Int with no precision loss in either direction: string -> BigIntString ->
// *big.Int, and *big.Int -> string (as produced by NewBridgeResponse) -> BigIntString ->
// *big.Int.
func TestBigIntString_ToBigIntRoundTrip(t *testing.T) {
	const globalIndexDecimal = "18446744073709551616" // 2^64

	expected := new(big.Int)
	_, ok := expected.SetString(globalIndexDecimal, 10)
	require.True(t, ok, "test fixture itself must parse as a valid decimal")

	require.Equal(t, 0, expected.Cmp(big.NewInt(0).Lsh(big.NewInt(1), 64)),
		"test fixture must equal 2^64")

	actual := BigIntString(globalIndexDecimal).ToBigInt()
	require.Equal(t, expected, actual)
	require.Equal(t, globalIndexDecimal, actual.String(), "no precision lost converting back to decimal")

	// Round-trip the other direction: a *big.Int formatted to decimal (as NewBridgeResponse
	// does via globalIndex.String()), wrapped in BigIntString, and converted back.
	roundTripped := BigIntString(expected.String()).ToBigInt()
	require.Equal(t, expected, roundTripped)
}
