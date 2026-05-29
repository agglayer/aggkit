package exit_certificate

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestRunStepB3_EmptyConfig_Skipped(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		L2RPCURL: "http://unused",
		Options: Options{
			RPCBatchSize:     200,
			ConcurrencyLimit: 5,
		},
	}
	result, err := RunStepB3(context.Background(), cfg, 0, nil, &StepB2Result{})
	require.NoError(t, err)
	require.Empty(t, result.Breakdowns)
}

func TestRunStepB3_DetectedFieldPopulated_FromB2(t *testing.T) {
	t.Parallel()

	contractAddr := common.HexToAddress("0xAAAA000000000000000000000000000000000001")
	eoa1 := common.HexToAddress("0x1111111111111111111111111111111111111111")

	server := newEthCallServer(t, func(tc rpcTestCall) (json.RawMessage, *jsonRPCError) {
		if tc.To == addrLow(contractAddr) && tc.Selector == balanceOfSelector {
			if strings.ToLower(eoaFromData(tc.FullData)) == addrLow(eoa1) {
				return abiUint256(big.NewInt(150)), nil
			}
		}
		return abiZero(), nil
	})
	defer server.Close()

	b2Result := &StepB2Result{
		DetectedERC20s: []DetectedERC20{
			{Address: contractAddr, Name: "StakedToken", Symbol: "ST", TotalSupply: "1000"},
		},
	}

	cfg := &Config{
		L2RPCURL: server.URL,
		Options: Options{
			RPCBatchSize:        200,
			ConcurrencyLimit:    5,
			ExtraERC20Contracts: []common.Address{contractAddr},
		},
	}
	result, err := RunStepB3(context.Background(), cfg, 0, []common.Address{eoa1}, b2Result)
	require.NoError(t, err)
	require.Len(t, result.Breakdowns, 1)
	bd := result.Breakdowns[0]
	require.Len(t, bd.Holders, 1)
	require.Equal(t, "150", bd.Holders[0].Balance)
	require.NotNil(t, bd.Detected, "collateral info must be populated when contract is in B2 detected list")
	require.Equal(t, "StakedToken", bd.Detected.Name)
	require.Equal(t, "ST", bd.Detected.Symbol)
}

func TestRunStepB3_FetchesHolders_NotInB2(t *testing.T) {
	t.Parallel()

	contractAddr := common.HexToAddress("0xCCCC000000000000000000000000000000000001")
	eoa1 := common.HexToAddress("0x1111111111111111111111111111111111111111")
	eoa2 := common.HexToAddress("0x2222222222222222222222222222222222222222")

	server := newEthCallServer(t, func(tc rpcTestCall) (json.RawMessage, *jsonRPCError) {
		if tc.To == addrLow(contractAddr) && tc.Selector == balanceOfSelector {
			queried := strings.ToLower(eoaFromData(tc.FullData))
			switch queried {
			case addrLow(eoa1):
				return abiUint256(big.NewInt(400)), nil
			case addrLow(eoa2):
				return abiZero(), nil // zero balance → not in result
			}
		}
		return abiZero(), nil
	})
	defer server.Close()

	cfg := &Config{
		L2RPCURL: server.URL,
		Options: Options{
			RPCBatchSize:        200,
			ConcurrencyLimit:    5,
			ExtraERC20Contracts: []common.Address{contractAddr},
		},
	}
	result, err := RunStepB3(context.Background(), cfg, 0, []common.Address{eoa1, eoa2}, &StepB2Result{})
	require.NoError(t, err)
	require.Len(t, result.Breakdowns, 1)

	bd := result.Breakdowns[0]
	require.Equal(t, contractAddr, bd.Address)
	require.Nil(t, bd.Detected, "no collateral info when contract was not in B2 detected list")
	require.Len(t, bd.Holders, 1, "only eoa1 has non-zero balance")
	require.Equal(t, eoa1, bd.Holders[0].Address)
	require.Equal(t, "400", bd.Holders[0].Balance)
}

func TestRunStepB3_NoEOAs_EmptyHolders(t *testing.T) {
	t.Parallel()

	contractAddr := common.HexToAddress("0xCCCC000000000000000000000000000000000001")
	server := newEthCallServer(t, func(_ rpcTestCall) (json.RawMessage, *jsonRPCError) {
		return abiZero(), nil
	})
	defer server.Close()

	cfg := &Config{
		L2RPCURL: server.URL,
		Options: Options{
			RPCBatchSize:        200,
			ConcurrencyLimit:    5,
			ExtraERC20Contracts: []common.Address{contractAddr},
		},
	}
	result, err := RunStepB3(context.Background(), cfg, 0, nil, &StepB2Result{})
	require.NoError(t, err)
	require.Len(t, result.Breakdowns, 1)
	require.Empty(t, result.Breakdowns[0].Holders)
}

func TestRunStepB3_RPCError_ReturnsError(t *testing.T) {
	t.Parallel()

	contractAddr := common.HexToAddress("0xCCCC000000000000000000000000000000000001")
	eoa1 := common.HexToAddress("0x1111111111111111111111111111111111111111")

	// Server always returns HTTP 500. The context timeout cuts the backoff short
	// so the test finishes in milliseconds instead of waiting for all retries.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := &Config{
		L2RPCURL: server.URL,
		Options: Options{
			RPCBatchSize:        1,
			ConcurrencyLimit:    1,
			ExtraERC20Contracts: []common.Address{contractAddr},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := RunStepB3(ctx, cfg, 0, []common.Address{eoa1}, &StepB2Result{})
	require.Error(t, err)
	require.Contains(t, err.Error(), contractAddr.Hex())
}

func TestRunStepB3_MixedContracts(t *testing.T) {
	t.Parallel()

	// addr1: in B2 detected list → Detected != nil
	// addr2: not in B2            → Detected == nil
	addr1 := common.HexToAddress("0xAAAA000000000000000000000000000000000001")
	addr2 := common.HexToAddress("0xBBBB000000000000000000000000000000000002")
	eoa1 := common.HexToAddress("0x1111111111111111111111111111111111111111")

	server := newEthCallServer(t, func(tc rpcTestCall) (json.RawMessage, *jsonRPCError) {
		if tc.Selector == balanceOfSelector {
			return abiUint256(big.NewInt(50)), nil
		}
		return abiZero(), nil
	})
	defer server.Close()

	b2Result := &StepB2Result{
		DetectedERC20s: []DetectedERC20{
			{Address: addr1, Name: "TokenA"},
		},
	}

	cfg := &Config{
		L2RPCURL: server.URL,
		Options: Options{
			RPCBatchSize:        200,
			ConcurrencyLimit:    5,
			ExtraERC20Contracts: []common.Address{addr1, addr2},
		},
	}
	result, err := RunStepB3(context.Background(), cfg, 0, []common.Address{eoa1}, b2Result)
	require.NoError(t, err)
	require.Len(t, result.Breakdowns, 2)

	byAddr := make(map[common.Address]ERC20HolderBreakdown, 2)
	for _, bd := range result.Breakdowns {
		byAddr[bd.Address] = bd
	}

	require.NotNil(t, byAddr[addr1].Detected)
	require.Equal(t, "TokenA", byAddr[addr1].Detected.Name)
	require.Len(t, byAddr[addr1].Holders, 1)

	require.Nil(t, byAddr[addr2].Detected)
	require.Len(t, byAddr[addr2].Holders, 1)
}
