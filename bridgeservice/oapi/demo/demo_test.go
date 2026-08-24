package demo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/agglayer/aggkit/bridgeservice/oapi"
	"github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/stretchr/testify/require"
)

// l1OriginGlobalIndex is 2^64+5 -- the global index of the first canned row.
// Rounded to the nearest float64 it becomes 18446744073709551616, so any
// consumer that parses the response with a JSON parser backed by doubles
// (every JavaScript runtime, by default) reads back a different bridge.
const l1OriginGlobalIndex = "18446744073709551621"

// canned amount for the first row, 10^18. Also past 2^53.
const l1OriginAmount = "1000000000000000000"

// The assertions below deliberately run against the raw response bytes.
// Unmarshalling first would defeat the purpose: Go's encoding/json happily
// decodes 18446744073709551621 into a *big.Int with no loss, so a decoded
// comparison would report both endpoints as correct and hide the defect that
// only exists in the serialised form.

func TestCurrentServiceEmitsGlobalIndexAsABareNumber(t *testing.T) {
	body := get(t, "/bridge/v1/bridges?network_id=0")

	require.Regexp(t, regexp.MustCompile(`"global_index":`+l1OriginGlobalIndex+`\b`), string(body),
		"the shipped BridgeResponse types the field as *big.Int, which encoding/json writes as a bare number")
	require.NotContains(t, string(body), `"global_index":"`+l1OriginGlobalIndex+`"`)

	// amount is the control: its sibling field already uses the
	// types.BigIntString wrapper and is therefore quoted on the same response.
	require.Contains(t, string(body), `"amount":"`+l1OriginAmount+`"`,
		"amount uses types.BigIntString, so only global_index is inconsistent")
}

func TestSpecFirstServerEmitsGlobalIndexAsAQuotedString(t *testing.T) {
	body := get(t, SpecFirstPrefix+"/bridge/v1/bridges?network_id=0")

	require.Contains(t, string(body), `"global_index":"`+l1OriginGlobalIndex+`"`)
	require.Contains(t, string(body), `"amount":"`+l1OriginAmount+`"`)
	require.NotRegexp(t, regexp.MustCompile(`"global_index":`+l1OriginGlobalIndex+`\b`), string(body))
}

// TestBothEndpointsAgreeOnTheValue guards the demonstration itself: if the two
// mounted servers ever served different bridges, the wire-format comparison
// above would be meaningless.
func TestBothEndpointsAgreeOnTheValue(t *testing.T) {
	var current struct {
		Bridges []struct {
			GlobalIndex  json.Number `json:"global_index"`
			DepositCount uint32      `json:"deposit_count"`
		} `json:"bridges"`
	}
	require.NoError(t, json.Unmarshal(get(t, "/bridge/v1/bridges?network_id=0"), &current))

	var specFirst oapi.BridgesResult
	require.NoError(t, json.Unmarshal(get(t, SpecFirstPrefix+"/bridge/v1/bridges?network_id=0"), &specFirst))

	require.Len(t, current.Bridges, len(CannedBridges()))
	require.Len(t, specFirst.Bridges, len(CannedBridges()))
	for i := range current.Bridges {
		require.Equal(t, current.Bridges[i].GlobalIndex.String(), string(specFirst.Bridges[i].GlobalIndex))
		require.Equal(t, current.Bridges[i].DepositCount, uint32(specFirst.Bridges[i].DepositCount)) //nolint:gosec // fixture
	}
}

// TestGeneratedTypesRoundTripTheQuotedForm covers the other half of the
// contract. x-go-type: big.Int would have produced a field that serialises as a
// number AND rejects a quoted string on the way back in, so a client sending
// the documented format would get a 400. types.BigIntString accepts it.
func TestGeneratedTypesRoundTripTheQuotedForm(t *testing.T) {
	const wire = `{"bridges":[{"block_num":1234,"block_pos":1,"tx_hash":"0x01",` +
		`"global_index":"` + l1OriginGlobalIndex + `","block_timestamp":1684500000,"leaf_type":0,` +
		`"origin_network":0,"origin_address":"0x02","destination_network":10,"destination_address":"0x03",` +
		`"amount":"` + l1OriginAmount + `","metadata":"0x","deposit_count":5,"bridge_hash":"0x04",` +
		`"txn_sender":"0x05","to_address":"0x06"}],"count":1}`

	var decoded oapi.BridgesResult
	require.NoError(t, json.Unmarshal([]byte(wire), &decoded))

	require.Len(t, decoded.Bridges, 1)
	require.Equal(t, types.BigIntString(l1OriginGlobalIndex), decoded.Bridges[0].GlobalIndex)
	require.Equal(t, l1OriginGlobalIndex, decoded.Bridges[0].GlobalIndex.ToBigInt().String(),
		"the wrapper keeps full precision -- ToBigInt parses the decimal string")

	reencoded, err := json.Marshal(decoded)
	require.NoError(t, err)
	require.Contains(t, string(reencoded), `"global_index":"`+l1OriginGlobalIndex+`"`,
		"encode(decode(x)) == x for the documented wire format")
}

func get(t *testing.T, path string) []byte {
	t.Helper()

	recorder := httptest.NewRecorder()
	NewRouter(CannedBridges()).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	return recorder.Body.Bytes()
}
