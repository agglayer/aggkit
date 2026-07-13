package exit_certificate

import (
	"context"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/aggchainbase"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/stretchr/testify/require"
)

// --- contract-call stub ---------------------------------------------------------------------------

// aggchainbaseABI is the parsed aggchainbase ABI used to compute selectors and pack return values for
// the stub (the bridge ABI is the package-level bridgeABI, parsed in step_g2.go's init).
var aggchainbaseABI = func() abi.ABI {
	a, err := aggchainbase.AggchainbaseMetaData.GetAbi()
	if err != nil {
		panic(err)
	}
	return *a
}()

// selectorHex returns the 4-byte method selector (hex, no 0x) for a method on the given ABI.
func selectorHex(a abi.ABI, method string) string {
	return common.Bytes2Hex(a.Methods[method].ID)
}

// packReturn ABI-encodes a method's return values (hex, no 0x) as the contract would.
func packReturn(t *testing.T, a abi.ABI, method string, vals ...any) string {
	t.Helper()
	b, err := a.Methods[method].Outputs.Pack(vals...)
	require.NoError(t, err)
	return common.Bytes2Hex(b)
}

// newContractStub serves eth_call by dispatching on the 4-byte selector: returns[selectorHex] is the
// hex-encoded return data. A selector that is absent gets a JSON-RPC error so failure paths can be
// exercised. It also answers eth_blockNumber/eth_chainId so ethclient dials and reachability checks
// succeed.
func newContractStub(t *testing.T, returns map[string]string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID     json.RawMessage   `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(body, &req)
		resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}

		switch req.Method {
		case "eth_blockNumber", "eth_chainId":
			resp["result"] = "0x1"
		case "eth_call":
			var call struct {
				Data  string `json:"data"`
				Input string `json:"input"`
			}
			_ = json.Unmarshal(req.Params[0], &call)
			callData := call.Input // go-ethereum uses "input"; fall back to "data"
			if callData == "" {
				callData = call.Data
			}
			sel := strings.TrimPrefix(callData, "0x")
			if len(sel) >= 8 {
				sel = sel[:8]
			}
			if out, ok := returns[sel]; ok {
				resp["result"] = "0x" + out
			} else {
				resp["error"] = map[string]any{"code": -32000, "message": "execution reverted"}
			}
		default:
			resp["result"] = "0x"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// --- checkL2NetworkID ----------------------------------------------------------------------------

func TestCheckL2NetworkIDMatch(t *testing.T) {
	t.Parallel()
	url := newContractStub(t, map[string]string{
		selectorHex(bridgeABI, "networkID"): packReturn(t, bridgeABI, "networkID", uint32(7)),
	})
	cfg := &Config{L2RPCURL: url, L2BridgeAddress: common.HexToAddress("0xbridge"), L2NetworkID: 7}
	result := &StepCheckResult{}
	var failures []string
	checkL2NetworkID(context.Background(), cfg, result, &failures)
	require.Empty(t, failures)
	require.Equal(t, uint32(7), result.BridgeNetworkID)
}

func TestCheckL2NetworkIDMismatch(t *testing.T) {
	t.Parallel()
	url := newContractStub(t, map[string]string{
		selectorHex(bridgeABI, "networkID"): packReturn(t, bridgeABI, "networkID", uint32(99)),
	})
	cfg := &Config{L2RPCURL: url, L2NetworkID: 7}
	result := &StepCheckResult{}
	var failures []string
	checkL2NetworkID(context.Background(), cfg, result, &failures)
	require.Len(t, failures, 1)
	require.Contains(t, failures[0], "mismatch")
}

func TestCheckL2NetworkIDCallError(t *testing.T) {
	t.Parallel()
	// no networkID selector registered → eth_call errors
	url := newContractStub(t, map[string]string{})
	cfg := &Config{L2RPCURL: url, L2NetworkID: 7}
	result := &StepCheckResult{}
	var failures []string
	checkL2NetworkID(context.Background(), cfg, result, &failures)
	require.Len(t, failures, 1)
	require.Contains(t, failures[0], "NetworkID")
}

// --- checkNativeGasToken -------------------------------------------------------------------------

func TestCheckNativeGasTokenNone(t *testing.T) {
	t.Parallel()
	url := newContractStub(t, map[string]string{
		selectorHex(bridgeABI, "gasTokenNetwork"): packReturn(t, bridgeABI, "gasTokenNetwork", uint32(0)),
		selectorHex(bridgeABI, "gasTokenAddress"): packReturn(t, bridgeABI, "gasTokenAddress", common.Address{}),
	})
	cfg := &Config{L2RPCURL: url}
	var failures []string
	checkNativeGasToken(context.Background(), cfg, &failures)
	require.Empty(t, failures)
}

func TestCheckNativeGasTokenPresent(t *testing.T) {
	t.Parallel()
	url := newContractStub(t, map[string]string{
		selectorHex(bridgeABI, "gasTokenNetwork"): packReturn(t, bridgeABI, "gasTokenNetwork", uint32(1)),
		selectorHex(bridgeABI, "gasTokenAddress"): packReturn(t, bridgeABI, "gasTokenAddress", common.HexToAddress("0xdead")),
	})
	cfg := &Config{L2RPCURL: url}
	var failures []string
	checkNativeGasToken(context.Background(), cfg, &failures)
	require.Len(t, failures, 1)
	require.Contains(t, failures[0], "gas token not supported")
}

func TestCheckNativeGasTokenError(t *testing.T) {
	t.Parallel()
	url := newContractStub(t, map[string]string{}) // gasToken selectors absent → call errors
	cfg := &Config{L2RPCURL: url}
	var failures []string
	checkNativeGasToken(context.Background(), cfg, &failures)
	require.Len(t, failures, 1)
}

// --- checkContractPrereqs ------------------------------------------------------------------------

func contractPrereqReturns(t *testing.T, bridgeAddr common.Address, aggchainType [2]byte, threshold int64) map[string]string {
	t.Helper()
	return map[string]string{
		selectorHex(aggchainbaseABI, "AGGCHAIN_TYPE"): packReturn(t, aggchainbaseABI, "AGGCHAIN_TYPE", aggchainType),
		selectorHex(aggchainbaseABI, "threshold"):     packReturn(t, aggchainbaseABI, "threshold", big.NewInt(threshold)),
		selectorHex(aggchainbaseABI, "getAggchainSignerInfos"): packReturn(t, aggchainbaseABI, "getAggchainSignerInfos",
			[]aggchainbase.IAggchainSignersSignerInfo{}),
		selectorHex(aggchainbaseABI, "bridgeAddress"): packReturn(t, aggchainbaseABI, "bridgeAddress", bridgeAddr),
		selectorHex(aggchainbaseABI, "rollupManager"): packReturn(t, aggchainbaseABI, "rollupManager", common.HexToAddress("0xr0")),
	}
}

func dialStub(t *testing.T, url string) *ethclient.Client {
	t.Helper()
	c, err := ethclient.DialContext(context.Background(), url)
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return c
}

func TestCheckContractPrereqsPP(t *testing.T) {
	t.Parallel()
	bridgeAddr := common.BytesToAddress([]byte("bridge"))
	url := newContractStub(t, contractPrereqReturns(t, bridgeAddr, [2]byte{0, 0}, 1))
	// bridgeAddress() shares its selector between aggchainbase and the rollup manager, so the stub
	// answers both the aggchainbase check and the rollup manager cross-check with bridgeAddr.
	cfg := &Config{SovereignRollupAddr: common.BytesToAddress([]byte("sov")),
		L2BridgeAddress: bridgeAddr, L1BridgeAddress: bridgeAddr}
	result := &StepCheckResult{}
	var failures []string
	checkContractPrereqs(context.Background(), cfg, dialStub(t, url), result, &failures)
	require.Empty(t, failures)
	require.Equal(t, "PP", result.NetworkType)
	require.Equal(t, uint64(1), result.Threshold)
	require.Equal(t, bridgeAddr.Hex(), result.RollupManagerBridgeAddress)
}

func TestCheckContractPrereqsFEPThresholdAndBridgeMismatch(t *testing.T) {
	t.Parallel()
	url := newContractStub(t, contractPrereqReturns(t, common.BytesToAddress([]byte("other")), [2]byte{0, 1}, 2))
	cfg := &Config{SovereignRollupAddr: common.BytesToAddress([]byte("sov")),
		L2BridgeAddress: common.BytesToAddress([]byte("bridge")),
		L1BridgeAddress: common.BytesToAddress([]byte("bridge"))}
	result := &StepCheckResult{}
	var failures []string
	checkContractPrereqs(context.Background(), cfg, dialStub(t, url), result, &failures)

	require.Equal(t, "FEP", result.NetworkType)
	joined := strings.Join(failures, "\n")
	require.Contains(t, joined, "FEP")
	require.Contains(t, joined, "threshold is 2")
	require.Contains(t, joined, "bridge address mismatch")
	require.Contains(t, joined, "rollupManager")
	require.Contains(t, joined, "set l1BridgeAddress=")
}

func TestCheckContractPrereqsAggchainTypeErrorTriggersLegacy(t *testing.T) {
	t.Parallel()
	// AGGCHAIN_TYPE selector omitted → its call errors, driving the legacy-diagnostics branch.
	rets := contractPrereqReturns(t, common.HexToAddress("0xbridge"), [2]byte{0, 0}, 1)
	delete(rets, selectorHex(aggchainbaseABI, "AGGCHAIN_TYPE"))
	url := newContractStub(t, rets)
	cfg := &Config{SovereignRollupAddr: common.HexToAddress("0xsov"),
		L2BridgeAddress: common.HexToAddress("0xbridge"), L1BridgeAddress: common.HexToAddress("0xbridge")}
	result := &StepCheckResult{}
	var failures []string
	checkContractPrereqs(context.Background(), cfg, dialStub(t, url), result, &failures)
	require.Equal(t, "unknown", result.NetworkType)
	// threshold/bridge still resolved fine, so the only failure is the AGGCHAINTYPE query
	require.Contains(t, strings.Join(failures, "\n"), "AGGCHAINTYPE")
}

// --- checkL1BridgeNetworkID ------------------------------------------------------------------------

func TestCheckL1BridgeNetworkIDIsL1(t *testing.T) {
	t.Parallel()
	url := newContractStub(t, map[string]string{
		selectorHex(bridgeABI, "networkID"): packReturn(t, bridgeABI, "networkID", uint32(0)),
	})
	cfg := &Config{L1BridgeAddress: common.HexToAddress("0xbridge")}
	result := &StepCheckResult{}
	var failures []string
	checkL1BridgeNetworkID(context.Background(), cfg, dialStub(t, url), result, &failures)
	require.Empty(t, failures)
	require.Equal(t, okStatus, result.L1BridgeAddressStatus)
}

func TestCheckL1BridgeNetworkIDNotL1(t *testing.T) {
	t.Parallel()
	// networkID()=2 → the address hosts an L2 bridge (e.g. the l2BridgeAddress default), not the L1 one.
	url := newContractStub(t, map[string]string{
		selectorHex(bridgeABI, "networkID"): packReturn(t, bridgeABI, "networkID", uint32(2)),
	})
	cfg := &Config{L1BridgeAddress: common.HexToAddress("0xbridge")}
	result := &StepCheckResult{}
	var failures []string
	checkL1BridgeNetworkID(context.Background(), cfg, dialStub(t, url), result, &failures)
	require.Len(t, failures, 1)
	require.Contains(t, failures[0], "not the L1 bridge")
	require.Contains(t, failures[0], "networkID()=2")
	require.Equal(t, "invalid (networkID()=2)", result.L1BridgeAddressStatus)
}

func TestCheckL1BridgeNetworkIDCallError(t *testing.T) {
	t.Parallel()
	// no networkID selector registered → eth_call errors (a non-bridge contract or no code at all)
	url := newContractStub(t, map[string]string{})
	cfg := &Config{L1BridgeAddress: common.HexToAddress("0xdead")}
	result := &StepCheckResult{}
	var failures []string
	checkL1BridgeNetworkID(context.Background(), cfg, dialStub(t, url), result, &failures)
	require.Len(t, failures, 1)
	require.Contains(t, failures[0], "networkID()")
	require.Equal(t, errorStatus, result.L1BridgeAddressStatus)
}

// --- RunStepCheck (failure aggregation) ----------------------------------------------------------

func TestRunStepCheckMissingL1AndSovereign(t *testing.T) {
	t.Parallel()
	// L2 stub answers networkID + gas token so those checks pass; L1 unset and sovereign unset fail.
	url := newContractStub(t, map[string]string{
		selectorHex(bridgeABI, "networkID"):       packReturn(t, bridgeABI, "networkID", uint32(1)),
		selectorHex(bridgeABI, "gasTokenNetwork"): packReturn(t, bridgeABI, "gasTokenNetwork", uint32(0)),
		selectorHex(bridgeABI, "gasTokenAddress"): packReturn(t, bridgeABI, "gasTokenAddress", common.Address{}),
	})
	cfg := &Config{L2RPCURL: url, L2NetworkID: 1} // L1RPCURL and SovereignRollupAddr left zero

	result, err := RunStepCheck(context.Background(), cfg)
	require.Error(t, err)
	require.Equal(t, uncheckedStatus, result.NetworkType)
	require.Equal(t, uncheckedStatus, result.L1BridgeAddressStatus)
	require.Contains(t, err.Error(), "l1RpcUrl is required")
	require.Contains(t, err.Error(), "l1BridgeAddress could not be verified")
	require.Contains(t, err.Error(), "sovereignRollupAddr is required")
}

func TestRunStepCheckAllReachable(t *testing.T) {
	t.Parallel()
	bridgeAddr := common.BytesToAddress([]byte("bridge"))

	// Separate L1/L2 stubs: the same networkID() selector must return 0 on the L1 bridge and the
	// configured l2NetworkId on the L2 bridge.
	l1Rets := contractPrereqReturns(t, bridgeAddr, [2]byte{0, 0}, 1)
	l1Rets[selectorHex(bridgeABI, "networkID")] = packReturn(t, bridgeABI, "networkID", uint32(0))
	l1URL := newContractStub(t, l1Rets)

	l2Rets := map[string]string{
		selectorHex(bridgeABI, "networkID"):       packReturn(t, bridgeABI, "networkID", uint32(1)),
		selectorHex(bridgeABI, "gasTokenNetwork"): packReturn(t, bridgeABI, "gasTokenNetwork", uint32(0)),
		selectorHex(bridgeABI, "gasTokenAddress"): packReturn(t, bridgeABI, "gasTokenAddress", common.Address{}),
	}
	l2URL := newContractStub(t, l2Rets)

	cfg := &Config{
		L1RPCURL: l1URL, L2RPCURL: l2URL, L2NetworkID: 1,
		L2BridgeAddress: bridgeAddr, L1BridgeAddress: bridgeAddr,
		SovereignRollupAddr: common.BytesToAddress([]byte("sov")),
	}

	result, err := RunStepCheck(context.Background(), cfg)
	// anvil presence is environment-dependent: the only acceptable failure is the anvil check.
	if err != nil {
		require.Contains(t, err.Error(), "anvil")
		require.NotContains(t, err.Error(), "l1RpcUrl")
		require.NotContains(t, err.Error(), "NetworkID")
	}
	require.Equal(t, "PP", result.NetworkType)
	require.Equal(t, uint32(1), result.BridgeNetworkID)
	require.Equal(t, okStatus, result.L1BridgeAddressStatus)
	require.Equal(t, uint64(1), result.Threshold)
}
