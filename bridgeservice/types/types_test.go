package types

import (
	"encoding/json"
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

// TestBigIntString_UnmarshalJSON_AcceptsStringAndNumber pins the 4-way matrix of wire forms
// BigIntString.UnmarshalJSON must accept: {quoted string, bare number} x {small value, value
// >= 2^64}. Pre-fix aggkit releases emitted global_index (and amount) as a bare JSON number, and
// a bridge service fans out to remotes upgraded on their own schedule, so a decoder built from
// this version must still read the old wire form, not just the new quoted one.
//
// The large-value cases are the ones that matter: 18446744073709551618 (2^64 + 2) overflows
// int64 and loses precision through float64 (float64 rounds it down to 18446744073709551616).
// Routing the bare-number path through json.Number instead keeps the literal exact -- this test
// fails if a future refactor swaps json.Number for either of those.
func TestBigIntString_UnmarshalJSON_AcceptsStringAndNumber(t *testing.T) {
	const smallValue = "12345"
	const largeValue = "18446744073709551618" // 2^64 + 2: overflows int64, rounds under float64.

	tests := []struct {
		name     string
		wire     string
		expected BigIntString
	}{
		{name: "quoted string, small value", wire: `"12345"`, expected: BigIntString(smallValue)},
		{name: "quoted string, value >= 2^64", wire: `"18446744073709551618"`, expected: BigIntString(largeValue)},
		{name: "bare number, small value", wire: `12345`, expected: BigIntString(smallValue)},
		{name: "bare number, value >= 2^64", wire: `18446744073709551618`, expected: BigIntString(largeValue)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var actual BigIntString
			err := json.Unmarshal([]byte(tt.wire), &actual)
			require.NoError(t, err)
			require.Equal(t, tt.expected, actual)
			require.Equal(t, string(tt.expected), string(actual), "the literal must be preserved exactly")
		})
	}
}

// TestBigIntString_UnmarshalJSON_Null verifies that unmarshaling a JSON null leaves the
// BigIntString at its zero value and returns no error, matching the tolerant behavior expected
// of an optional/omittable field.
func TestBigIntString_UnmarshalJSON_Null(t *testing.T) {
	var actual BigIntString
	err := json.Unmarshal([]byte(`null`), &actual)
	require.NoError(t, err)
	require.Equal(t, BigIntString(""), actual)
}

// TestBigIntString_UnmarshalJSON_InvalidJSON verifies that malformed JSON (neither a string nor
// a number) is rejected with an error rather than silently accepted.
func TestBigIntString_UnmarshalJSON_InvalidJSON(t *testing.T) {
	var actual BigIntString
	err := json.Unmarshal([]byte(`{"not": "a scalar"}`), &actual)
	require.Error(t, err)
}

// TestBridgesResult_UnmarshalJSON_PreFixBareNumberGlobalIndex is the cross-version regression
// case: it decodes a realistic pre-fix /bridges response payload -- one where global_index (and
// amount) are still bare JSON numbers, exactly what an older, independently-deployed aggkit
// bridge service emits -- directly into types.BridgesResult, the same target type
// bridgeservice/client/client.go uses. Before UnmarshalJSON was made tolerant, this failed with
// "json: cannot unmarshal number ... into Go struct field ... of type BigIntString", breaking
// every activity scan against a remote that has not yet upgraded past this PR.
func TestBridgesResult_UnmarshalJSON_PreFixBareNumberGlobalIndex(t *testing.T) {
	const preFixPayload = `{
		"bridges": [
			{
				"block_num": 1234,
				"block_pos": 1,
				"tx_hash": "0xdef",
				"global_index": 18446744073709551618,
				"block_timestamp": 1684500000,
				"leaf_type": 1,
				"origin_network": 0,
				"origin_address": "0xabc",
				"destination_network": 1,
				"destination_address": "0xdef",
				"amount": 1000000000000000000,
				"metadata": "0xdeadbeef",
				"deposit_count": 0,
				"bridge_hash": "0xabc",
				"txn_sender": "0xabc",
				"to_address": "0xdef"
			}
		],
		"count": 1
	}`

	var result BridgesResult
	err := json.Unmarshal([]byte(preFixPayload), &result)
	require.NoError(t, err)
	require.Len(t, result.Bridges, 1)
	require.Equal(t, BigIntString("18446744073709551618"), result.Bridges[0].GlobalIndex)
	require.Equal(t, BigIntString("1000000000000000000"), result.Bridges[0].Amount)
}
