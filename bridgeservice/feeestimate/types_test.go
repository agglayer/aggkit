package feeestimate

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/stretchr/testify/require"
)

// TestFeeEstimateEnumWireValues pins the wire values of the three string enums: they are part
// of the API contract both fee estimate endpoints publish and must never change silently.
func TestFeeEstimateEnumWireValues(t *testing.T) {
	require.Equal(t, "wrapped_erc20", string(WrappedERC20))
	require.Equal(t, "origin_native_erc20", string(OriginNativeERC20))
	require.Equal(t, "gas_token", string(GasToken))
	require.Equal(t, "first_claim", string(FirstClaim))
	require.Equal(t, "repeat_claim", string(RepeatClaim))
	require.Equal(t, "native", string(Native))
	require.Equal(t, "erc20_lock", string(ERC20Lock))
	require.Equal(t, "wrapped_burn", string(WrappedBurn))
}

// wei returns a pointer to a decimal-string wei value, the shape GasPriceInfo.BaseFee and
// GasPriceInfo.PriorityFee take in the JSON.
func wei(v string) *types.BigIntString {
	b := types.BigIntString(v)
	return &b
}

// boolPtr returns a pointer to b, the shape ClaimFeeEstimateResponse.WrapperExists takes.
func boolPtr(b bool) *bool {
	return &b
}

// eip1559GasPrice is a gas price snapshot of an EIP-1559 chain: base fee and priority fee set.
func eip1559GasPrice() GasPriceInfo {
	return GasPriceInfo{
		BaseFee:     wei("15000000000"),
		PriorityFee: wei("2000000000"),
		GasPrice:    "17000000000",
		BlockNumber: 123,
	}
}

// legacyGasPrice is a gas price snapshot of a chain without EIP-1559: only eth_gasPrice.
func legacyGasPrice() GasPriceInfo {
	return GasPriceInfo{
		GasPrice:    "2000000000",
		BlockNumber: 55,
	}
}

// TestClaimFeeEstimateResponse_MarshalJSON_GoalsShape pins the claim-fee-estimate response
// shape against the plan's "Goals" section, field names, types and nullability included, and
// round-trips each variant through json.Unmarshal back into the struct:
//   - repeat applicable: the wrapper exists, so repeat_claim holds the estimate and the
//     non-applicable first_claim is null with its reason in Unavailable;
//   - first applicable: the wrapper does not exist yet, so first_claim holds the estimate;
//   - gas token on a chain without EIP-1559: no wrapper applies, so wrapper_exists is null,
//     and the gas price snapshot carries null base/priority fees.
func TestClaimFeeEstimateResponse_MarshalJSON_GoalsShape(t *testing.T) {
	tests := []struct {
		name         string
		response     ClaimFeeEstimateResponse
		expectedJSON string
	}{
		{
			name: "wrapper exists: repeat claim applicable, first claim null with reason",
			response: ClaimFeeEstimateResponse{
				NetworkID:          1,
				OriginNetwork:      0,
				OriginTokenAddress: "0x1234567890abcdef1234567890abcdef12345678",
				TokenClass:         WrappedERC20,
				WrapperExists:      boolPtr(true),
				Applicable:         RepeatClaim,
				GasPrice:           eip1559GasPrice(),
				FirstClaim:         nil,
				RepeatClaim: &FeeEstimate{
					GasUnits:    110000,
					Cost:        "1870000000000000",
					SampleCount: 37,
					Source:      "class",
				},
				Unavailable: map[Applicable]string{
					FirstClaim: "wrapper already deployed; the first-claim estimate does not apply",
				},
				ComputedAt: 1760000000,
			},
			expectedJSON: `{
				"network_id": 1,
				"origin_network": 0,
				"origin_token_address": "0x1234567890abcdef1234567890abcdef12345678",
				"token_class": "wrapped_erc20",
				"wrapper_exists": true,
				"applicable": "repeat_claim",
				"gas_price": {
					"base_fee": "15000000000",
					"priority_fee": "2000000000",
					"gas_price": "17000000000",
					"block_number": 123
				},
				"first_claim": null,
				"repeat_claim": {
					"gas_units": 110000,
					"cost": "1870000000000000",
					"sample_count": 37,
					"source": "class"
				},
				"unavailable": {
					"first_claim": "wrapper already deployed; the first-claim estimate does not apply"
				},
				"computed_at": 1760000000
			}`,
		},
		{
			name: "wrapper missing: first claim applicable",
			response: ClaimFeeEstimateResponse{
				NetworkID:          1,
				OriginNetwork:      0,
				OriginTokenAddress: "0x1234567890abcdef1234567890abcdef12345678",
				TokenClass:         WrappedERC20,
				WrapperExists:      boolPtr(false),
				Applicable:         FirstClaim,
				GasPrice:           eip1559GasPrice(),
				FirstClaim: &FeeEstimate{
					GasUnits:    250000,
					Cost:        "4250000000000000",
					SampleCount: 3,
					Source:      "token",
				},
				RepeatClaim: nil,
				Unavailable: map[Applicable]string{
					RepeatClaim: "wrapper does not exist yet; the repeat-claim estimate does not apply",
				},
				ComputedAt: 1760000000,
			},
			expectedJSON: `{
				"network_id": 1,
				"origin_network": 0,
				"origin_token_address": "0x1234567890abcdef1234567890abcdef12345678",
				"token_class": "wrapped_erc20",
				"wrapper_exists": false,
				"applicable": "first_claim",
				"gas_price": {
					"base_fee": "15000000000",
					"priority_fee": "2000000000",
					"gas_price": "17000000000",
					"block_number": 123
				},
				"first_claim": {
					"gas_units": 250000,
					"cost": "4250000000000000",
					"sample_count": 3,
					"source": "token"
				},
				"repeat_claim": null,
				"unavailable": {
					"repeat_claim": "wrapper does not exist yet; the repeat-claim estimate does not apply"
				},
				"computed_at": 1760000000
			}`,
		},
		{
			name: "gas token on a chain without EIP-1559: wrapper_exists null",
			response: ClaimFeeEstimateResponse{
				NetworkID:          1,
				OriginNetwork:      0,
				OriginTokenAddress: "0x0000000000000000000000000000000000000000",
				TokenClass:         GasToken,
				WrapperExists:      nil,
				Applicable:         RepeatClaim,
				GasPrice:           legacyGasPrice(),
				FirstClaim:         nil,
				RepeatClaim: &FeeEstimate{
					GasUnits:    90000,
					Cost:        "180000000000",
					SampleCount: 5,
					Source:      "class",
				},
				Unavailable: map[Applicable]string{
					FirstClaim: "no wrapper is deployed for the gas token; the first-claim estimate does not apply",
				},
				ComputedAt: 1760000000,
			},
			expectedJSON: `{
				"network_id": 1,
				"origin_network": 0,
				"origin_token_address": "0x0000000000000000000000000000000000000000",
				"token_class": "gas_token",
				"wrapper_exists": null,
				"applicable": "repeat_claim",
				"gas_price": {
					"base_fee": null,
					"priority_fee": null,
					"gas_price": "2000000000",
					"block_number": 55
				},
				"first_claim": null,
				"repeat_claim": {
					"gas_units": 90000,
					"cost": "180000000000",
					"sample_count": 5,
					"source": "class"
				},
				"unavailable": {
					"first_claim": "no wrapper is deployed for the gas token; the first-claim estimate does not apply"
				},
				"computed_at": 1760000000
			}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(tt.response)
			require.NoError(t, err)
			require.JSONEq(t, tt.expectedJSON, string(raw))

			var roundTripped ClaimFeeEstimateResponse
			require.NoError(t, json.Unmarshal(raw, &roundTripped))
			require.Equal(t, tt.response, roundTripped)
		})
	}
}

// TestClaimFeeEstimateResponse_ZeroValueMarshalsNulls pins that the claim response's key set
// is stable: every field is always present in the JSON, and the nullable ones (wrapper_exists,
// first_claim, repeat_claim, unavailable, and the gas price fees) serialize as null, never as
// omitted keys.
func TestClaimFeeEstimateResponse_ZeroValueMarshalsNulls(t *testing.T) {
	raw, err := json.Marshal(ClaimFeeEstimateResponse{})
	require.NoError(t, err)
	require.JSONEq(t, `{
		"network_id": 0,
		"origin_network": 0,
		"origin_token_address": "",
		"token_class": "",
		"wrapper_exists": null,
		"applicable": "",
		"gas_price": {
			"base_fee": null,
			"priority_fee": null,
			"gas_price": "",
			"block_number": 0
		},
		"first_claim": null,
		"repeat_claim": null,
		"unavailable": null,
		"computed_at": 0
	}`, string(raw))
}

// TestBridgeFeeEstimateResponse_MarshalJSON_GoalsShape pins the bridge-fee-estimate response
// shape against the plan's "Goals" section and round-trips it through json.Unmarshal. One
// estimate per mechanism, with the gas price nullability of both an EIP-1559 chain and a
// chain without one.
func TestBridgeFeeEstimateResponse_MarshalJSON_GoalsShape(t *testing.T) {
	tests := []struct {
		name         string
		response     BridgeFeeEstimateResponse
		expectedJSON string
	}{
		{
			name: "native gas token on an EIP-1559 chain",
			response: BridgeFeeEstimateResponse{
				NetworkID:    1,
				TokenAddress: "0x0000000000000000000000000000000000000000",
				Mechanism:    Native,
				GasPrice:     eip1559GasPrice(),
				Estimate: FeeEstimate{
					GasUnits:    65000,
					Cost:        "1105000000000000",
					SampleCount: 42,
					Source:      "mechanism",
				},
				ComputedAt: 1760000000,
			},
			expectedJSON: `{
				"network_id": 1,
				"token_address": "0x0000000000000000000000000000000000000000",
				"mechanism": "native",
				"gas_price": {
					"base_fee": "15000000000",
					"priority_fee": "2000000000",
					"gas_price": "17000000000",
					"block_number": 123
				},
				"estimate": {
					"gas_units": 65000,
					"cost": "1105000000000000",
					"sample_count": 42,
					"source": "mechanism"
				},
				"computed_at": 1760000000
			}`,
		},
		{
			name: "erc20 lock on a chain without EIP-1559",
			response: BridgeFeeEstimateResponse{
				NetworkID:    10,
				TokenAddress: "0x1234567890abcdef1234567890abcdef12345678",
				Mechanism:    ERC20Lock,
				GasPrice:     legacyGasPrice(),
				Estimate: FeeEstimate{
					GasUnits:    80000,
					Cost:        "160000000000",
					SampleCount: 12,
					Source:      "mechanism",
				},
				ComputedAt: 1760000000,
			},
			expectedJSON: `{
				"network_id": 10,
				"token_address": "0x1234567890abcdef1234567890abcdef12345678",
				"mechanism": "erc20_lock",
				"gas_price": {
					"base_fee": null,
					"priority_fee": null,
					"gas_price": "2000000000",
					"block_number": 55
				},
				"estimate": {
					"gas_units": 80000,
					"cost": "160000000000",
					"sample_count": 12,
					"source": "mechanism"
				},
				"computed_at": 1760000000
			}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(tt.response)
			require.NoError(t, err)
			require.JSONEq(t, tt.expectedJSON, string(raw))

			var roundTripped BridgeFeeEstimateResponse
			require.NoError(t, json.Unmarshal(raw, &roundTripped))
			require.Equal(t, tt.response, roundTripped)
		})
	}
}

// TestFeeEstimateSentinelErrors pins the two sentinel errors the handlers map to HTTP
// statuses: they are distinct, and implementations may wrap them with fmt.Errorf without
// losing the errors.Is match.
func TestFeeEstimateSentinelErrors(t *testing.T) {
	require.NotErrorIs(t, ErrFeeEstimateUnavailable, ErrFeeEstimateBadRequest)
	require.NotErrorIs(t, ErrFeeEstimateBadRequest, ErrFeeEstimateUnavailable)
	require.ErrorIs(t,
		fmt.Errorf("not configured for network 7: %w", ErrFeeEstimateUnavailable),
		ErrFeeEstimateUnavailable)
	require.ErrorIs(t,
		fmt.Errorf("origin_network is not a valid uint32: %w", ErrFeeEstimateBadRequest),
		ErrFeeEstimateBadRequest)
}

// TestFeeEstimateErrorBodyShape pins the body both fee estimate endpoints answer with on
// failure: the bridge service's existing {"error": "..."} shape (types.ErrorResponse), here
// for a 503 where the estimator is not configured for the network.
func TestFeeEstimateErrorBodyShape(t *testing.T) {
	raw, err := json.Marshal(types.ErrorResponse{
		Error: "fee estimation not configured for network 7",
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"error":"fee estimation not configured for network 7"}`, string(raw))

	// The sentinels themselves never surface verbatim as the body: the handler composes the
	// message, so the sentinel text must not be relied on by clients.
	require.Equal(t, "fee estimate unavailable", ErrFeeEstimateUnavailable.Error())
	require.Equal(t, "fee estimate bad request", ErrFeeEstimateBadRequest.Error())
	require.False(t, errors.Is(errors.New("unrelated"), ErrFeeEstimateUnavailable))
}
