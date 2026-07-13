package exit_certificate

// Tests for the fail-open fixes of issue #1714 (AET-05/07/24/26/36/37/39 + two untracked
// findings): every branch that used to substitute a silent default now returns an error, and
// these tests exercise those error paths — corrupted intermediate files, unparseable RPC
// responses and unwritable output files.

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// sabotageOutputFile makes saveJSON(dir, filename, ...) fail by occupying the target
// path with a directory.
func sabotageOutputFile(t *testing.T, dir, filename string) {
	t.Helper()
	require.NoError(t, os.Mkdir(filepath.Join(dir, filename), 0o755))
}

// --- AET-39: save helpers propagate write errors ------------------------------------------------

func TestSaveStepFilesWriteErrors(t *testing.T) {
	t.Parallel()

	stepB := &StepBResult{}
	stepE := &StepEResult{FinalCertificate: &agglayertypes.Certificate{}}

	cases := []struct {
		file string
		call func(dir string) error
	}{
		{fileStepBEOABalances, func(dir string) error { return saveStepB1Files(dir, &StepB1Result{}) }},
		{fileStepBAccumulated, func(dir string) error { return saveStepB1Files(dir, &StepB1Result{}) }},
		{fileStepBContractAddresses, func(dir string) error { return saveStepB1Files(dir, &StepB1Result{}) }},
		{fileStepBEOABalances, func(dir string) error { return saveStepBFiles(dir, stepB) }},
		{fileStepB2DetectedERC20s, func(dir string) error { return saveStepBFiles(dir, stepB) }},
		{fileStepB2DiscardedERC20s, func(dir string) error { return saveStepBFiles(dir, stepB) }},
		{fileStepB3ERC20Holders, func(dir string) error { return saveStepBFiles(dir, stepB) }},
		{fileStepCSCLockedValues, func(dir string) error { return saveStepCFiles(dir, &StepCResult{}) }},
		{fileStepCHolderBridges, func(dir string) error { return saveStepCFiles(dir, &StepCResult{}) }},
		{fileStepEUnclaimedBridges, func(dir string) error { return saveStepEFiles(dir, stepE) }},
		{fileStepEUnclaimedMsgs, func(dir string) error { return saveStepEFiles(dir, stepE) }},
		{fileStepECertificate, func(dir string) error { return saveStepEFiles(dir, stepE) }},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			sabotageOutputFile(t, dir, tc.file)
			err := tc.call(dir)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.file)
		})
	}
}

// --- AET-05/36: corrupted intermediate balances abort steps C and D -----------------------------

func TestRunStepCCorruptedInputs(t *testing.T) {
	t.Parallel()

	token := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	validLBT := []LBTEntry{{WrappedTokenAddress: token, OriginNetwork: 0, OriginTokenAddress: token, Balance: "100"}}

	t.Run("corrupt accumulated EOA balance", func(t *testing.T) {
		t.Parallel()
		_, err := RunStepC(validLBT, &StepBResult{
			Accumulated: []AccumulatedBalance{{WrappedTokenAddress: token, TotalBalance: "not-a-number"}},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "accumulated EOA balance")
	})

	t.Run("corrupt native contract-locked balance", func(t *testing.T) {
		t.Parallel()
		_, err := RunStepC(validLBT, &StepBResult{NativeContractLocked: "garbage"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "native contract-locked balance")
	})

	t.Run("corrupt vault token balance", func(t *testing.T) {
		t.Parallel()
		_, err := RunStepC(validLBT, &StepBResult{
			ERC20HolderBreakdowns: []ERC20HolderBreakdown{{
				Address: common.HexToAddress("0xdead"),
				Detected: &DetectedERC20{
					WrappedTokenBalances: []WrappedTokenBalance{{
						Token:   WrappedToken{WrappedTokenAddress: token},
						Balance: "xx",
					}},
				},
			}},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "vault")
	})

	t.Run("corrupt vault holder balance", func(t *testing.T) {
		t.Parallel()
		_, err := RunStepC(validLBT, &StepBResult{
			ERC20HolderBreakdowns: []ERC20HolderBreakdown{{
				Address: common.HexToAddress("0xdead"),
				Holders: []ERC20Holder{{Address: common.HexToAddress("0xbeef"), Balance: "??"}},
				Detected: &DetectedERC20{
					WrappedTokenBalances: []WrappedTokenBalance{{
						Token:   WrappedToken{WrappedTokenAddress: token},
						Balance: "50",
					}},
				},
			}},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "holder")
	})

	t.Run("corrupt LBT balance", func(t *testing.T) {
		t.Parallel()
		badLBT := []LBTEntry{{WrappedTokenAddress: token, Balance: "1e18"}}
		_, err := RunStepC(badLBT, &StepBResult{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "LBT balance")
	})
}

func TestRunStepDCorruptedInputs(t *testing.T) {
	t.Parallel()

	cfg := &Config{ExitAddress: common.HexToAddress("0xe817"), DestinationNetwork: 0}
	eoa := common.HexToAddress("0x00000000000000000000000000000000000000bb")

	t.Run("corrupt EOA ETH balance", func(t *testing.T) {
		t.Parallel()
		_, err := RunStepD(cfg, &StepBResult{
			EOABalances: []EOABalance{{Address: eoa, ETHBalance: "not-a-number"}},
		}, &StepCResult{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "ETH balance")
	})

	t.Run("corrupt EOA token balance", func(t *testing.T) {
		t.Parallel()
		_, err := RunStepD(cfg, &StepBResult{
			EOABalances: []EOABalance{{
				Address:    eoa,
				ETHBalance: "0",
				Tokens:     []EOATokenBalance{{Balance: "xx"}},
			}},
		}, &StepCResult{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "balance of token")
	})

	t.Run("corrupt holder bridge amount", func(t *testing.T) {
		t.Parallel()
		_, err := RunStepD(cfg, &StepBResult{}, &StepCResult{
			HolderBridges: []HolderBridge{{HolderAddress: eoa, Amount: "??"}},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "holder bridge amount")
	})

	t.Run("corrupt pending SC-locked balance", func(t *testing.T) {
		t.Parallel()
		_, err := RunStepD(cfg, &StepBResult{}, &StepCResult{
			SCLockedValues: []SCLockedValue{{PendingSCLockedBalance: "garbage"}},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "SC-locked balance")
	})
}

// --- AET-26: invalid hex block numbers abort instead of parsing as garbage ----------------------

func TestResolveL1EndBlockInvalidHex(t *testing.T) {
	t.Parallel()
	srv := newRPCStub(t, func(string, []any) (json.RawMessage, *jsonRPCError) {
		return quoted("0xzz"), nil
	})
	_, err := resolveL1EndBlock(context.Background(), &Config{L1RPCURL: srv.URL})
	require.Error(t, err)
	require.Contains(t, err.Error(), "parse L1 latest block")
}

func TestDecodeBridgeEventInvalidBlockNumber(t *testing.T) {
	t.Parallel()
	data := make([]byte, 9*32)
	new(big.Int).SetInt64(256).FillBytes(data[192:224]) // metadataOffset
	_, err := decodeBridgeEvent("0x"+common.Bytes2Hex(data), "0xzz", "0x1234")
	require.Error(t, err)
	require.Contains(t, err.Error(), "blockNumber")
}

func TestFetchBridgeEventsInRangeDecodeError(t *testing.T) {
	t.Parallel()
	srv := newRPCStub(t, func(string, []any) (json.RawMessage, *jsonRPCError) {
		out, err := json.Marshal([]map[string]string{{
			"data":            "0x0000", // too short to be a BridgeEvent payload
			"blockNumber":     "0x1",
			"transactionHash": common.HexToHash("0xdead").Hex(),
		}})
		require.NoError(t, err)
		return out, nil
	})
	_, err := fetchBridgeEventsInRange(
		context.Background(), srv.URL, common.HexToAddress("0xbridge"), 1, 0, 10,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "decode BridgeEvent")
}

func TestQueryVerifyBatchesInvalidBlockNumber(t *testing.T) {
	t.Parallel()
	exitRoot := common.HexToHash("0xabc123")
	srv := newRPCStub(t, func(string, []any) (json.RawMessage, *jsonRPCError) {
		out, err := json.Marshal([]map[string]string{{
			"blockNumber":     "0xnothex",
			"transactionHash": common.HexToHash("0xdead").Hex(),
			"data":            "0x" + common.Bytes2Hex(verifyBatchesData(7, common.Hash{}, exitRoot)),
		}})
		require.NoError(t, err)
		return out, nil
	})
	_, _, _, err := queryVerifyBatches( //nolint:dogsled
		context.Background(), srv.URL, common.HexToAddress("0xabc"), 5, exitRoot, 0, 100)
	require.Error(t, err)
	require.Contains(t, err.Error(), "block number")
}

func TestReplayedLeafFromReceiptInvalidBlockFields(t *testing.T) {
	t.Parallel()

	badBlock := bridgeEventReceipt(1, 5, 0)
	badBlock[0].BlockNumber = "0xzz"
	_, err := replayedLeafFromReceipt(badBlock, common.HexToHash("0x1"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "block number")

	badIndex := bridgeEventReceipt(1, 5, 0)
	badIndex[0].LogIndex = "0xzz"
	_, err = replayedLeafFromReceipt(badIndex, common.HexToHash("0x1"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "log index")
}

// --- AET-05: step F aborts on unparseable agglayer/LBT amounts ----------------------------------

func TestCompareTokenBalancesBadAmounts(t *testing.T) {
	t.Parallel()
	addr := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")

	_, err := compareTokenBalances(nil, []agglayerTokenEntry{
		{OriginNetwork: 0, OriginTokenAddress: addr, Amount: "not-a-number"},
	}, nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "agglayer amount")

	_, err = compareTokenBalances(nil, nil, []LBTEntry{
		{OriginNetwork: 0, OriginTokenAddress: addr, Balance: "xx"},
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "LBT balance")

	_, err = compareCertificateToLBT(nil, []LBTEntry{
		{OriginNetwork: 0, OriginTokenAddress: addr, Balance: "xx"},
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "LBT balance")
}

func TestRunStepFBadAgglayerAmountAborts(t *testing.T) {
	t.Parallel()
	srv := newRPCStub(t, func(string, []any) (json.RawMessage, *jsonRPCError) {
		return json.RawMessage(`{"balances":[{"originNetwork":0,` +
			`"originTokenAddress":"0x0000000000000000000000000000000000000000","amount":"garbage"}]}`), nil
	})
	cfg := &Config{Options: Options{AgglayerAdminURL: srv.URL, UseAgglayerAdminToStepFCheck: true}}
	_, err := RunStepF(context.Background(), cfg, &agglayertypes.Certificate{}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "agglayer amount")
}

func TestRunStepFOfflineBadLBTAborts(t *testing.T) {
	t.Parallel()
	cfg := &Config{Options: Options{UseAgglayerAdminToStepFCheck: false}}
	_, err := RunStepF(context.Background(), cfg, &agglayertypes.Certificate{}, []LBTEntry{
		{Balance: "not-a-number"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "LBT balance")
}

func TestRunStepFAgglayerDumpWriteError(t *testing.T) {
	t.Parallel()
	srv := newRPCStub(t, func(string, []any) (json.RawMessage, *jsonRPCError) {
		return json.RawMessage(`{"balances":[]}`), nil
	})
	dir := t.TempDir()
	sabotageOutputFile(t, dir, fileStepFAgglayerLBT)
	cfg := &Config{Options: Options{
		AgglayerAdminURL: srv.URL, UseAgglayerAdminToStepFCheck: true, OutputDir: dir,
	}}
	_, err := RunStepF(context.Background(), cfg, &agglayertypes.Certificate{}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), fileStepFAgglayerLBT)
}

// --- untracked: gas token lookup failures propagate (no standard-ETH fallback) ------------------

func TestGasTokenLookupFailurePropagates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cfg := &Config{L2RPCURL: "http://127.0.0.1:1"} // unreachable
	cert := &agglayertypes.Certificate{BridgeExits: []*agglayertypes.BridgeExit{
		nativeAssetExit(common.HexToAddress("0x1"), 1),
	}}

	_, _, err := fetchL2GasTokenInfo(ctx, cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "gas token info")

	_, err = generateMetadata(ctx, newMockBackend(), cfg, cert, nil)
	require.ErrorContains(t, err, "gas token info")

	_, _, _, err = runStepG2ShadowFork(ctx, cfg, newMockBackend(), cert, nil) //nolint:dogsled
	require.ErrorContains(t, err, "gas token info")

	_, _, err = runStepG2BuildLocalExitTree(ctx, cfg, 100, cert, nil)
	require.ErrorContains(t, err, "gas token info")
}

func TestSaveFailedExitWriteErrorDoesNotPanic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sabotageOutputFile(t, dir, fileStepGFailedExit)
	job := exitJob{bridge: nativeAssetExit(common.HexToAddress("0x1"), 1)}
	require.NotPanics(t, func() { saveFailedExit(dir, job, errors.New("replay failed")) })
}

// --- AET-07/39: single-step runners reject corrupted inputs and report write errors -------------

func TestRunSingleCCorruptOptionalB3(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustSaveJSON(t, dir, fileStepBAccumulated, []AccumulatedBalance{})
	mustSaveJSON(t, dir, fileStep0LBT, []LBTEntry{{Balance: "1"}})
	require.NoError(t, os.WriteFile(filepath.Join(dir, fileStepB3ERC20Holders), []byte("{bad}"), 0o600))

	err := runSingleC(context.Background(), &Config{}, dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "load step B3 output")
}

func TestRunSingleDCorruptOptionalHolderBridges(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustSaveJSON(t, dir, fileStepBEOABalances, []EOABalance{})
	mustSaveJSON(t, dir, fileStepCSCLockedValues, []SCLockedValue{})
	require.NoError(t, os.WriteFile(filepath.Join(dir, fileStepCHolderBridges), []byte("{bad}"), 0o600))

	err := runSingleD(&Config{}, dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "load step C holder bridges")
}

// corruptCertFixture writes a step certificate whose bridge_exits section is not an array, so
// toAgglayerCertificate must fail instead of loading a certificate with zero exits (AET-07).
func corruptCertFixture(t *testing.T, dir, filename string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, filename),
		[]byte(`{"network_id":1,"bridge_exits":{"bad":1}}`), 0o600))
}

func TestRunSingleStepsRejectCorruptCertificates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("step E", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		corruptCertFixture(t, dir, fileStepDCertificate)
		err := runSingleE(ctx, &Config{L1RPCURL: "http://127.0.0.1:1"}, dir)
		require.Error(t, err)
		require.Contains(t, err.Error(), "load step D certificate")
	})

	t.Run("step F", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		corruptCertFixture(t, dir, fileStepDCertificate)
		err := runSingleF(ctx, &Config{}, dir)
		require.Error(t, err)
		require.Contains(t, err.Error(), "load step D certificate")
	})

	t.Run("step G2", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		mustSaveJSON(t, dir, fileStepG1ShadowForkBlock, StepG1Result{ShadowForkBlock: 100})
		corruptCertFixture(t, dir, fileStepECertificate)
		err := runSingleG2(ctx, &Config{}, dir)
		require.Error(t, err)
		require.Contains(t, err.Error(), "load certificate for step G2")
	})

	t.Run("step I", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		corruptCertFixture(t, dir, fileStepGReorderedCertificate)
		mustSaveJSON(t, dir, fileStepGNewLocalExitRoot, StepGResult{})
		mustSaveJSON(t, dir, fileStepHPreviousLocalExitRoot, StepHResult{})
		err := runSingleI(ctx, &Config{}, dir)
		require.Error(t, err)
		require.Contains(t, err.Error(), "load step G reordered certificate")
	})
}

func TestRunSingleFSaveErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("checks file write error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		mustSaveJSON(t, dir, fileStepDCertificate, &certificateJSON{NetworkID: 1})
		sabotageOutputFile(t, dir, fileStepFChecks)
		// Offline mode with no LBT file → benign skip result, then the checks write fails.
		err := runSingleF(ctx, &Config{Options: Options{UseAgglayerAdminToStepFCheck: false}}, dir)
		require.Error(t, err)
		require.Contains(t, err.Error(), fileStepFChecks)
	})

	t.Run("token balances write error", func(t *testing.T) {
		t.Parallel()
		srv := newRPCStub(t, func(string, []any) (json.RawMessage, *jsonRPCError) {
			return json.RawMessage(`{"balances":[]}`), nil
		})
		dir := t.TempDir()
		mustSaveJSON(t, dir, fileStepDCertificate, &certificateJSON{NetworkID: 1})
		sabotageOutputFile(t, dir, fileStepFTokenBalances)
		cfg := &Config{Options: Options{
			AgglayerAdminURL: srv.URL, UseAgglayerAdminToStepFCheck: true, OutputDir: dir,
		}}
		err := runSingleF(ctx, cfg, dir)
		require.Error(t, err)
		require.Contains(t, err.Error(), fileStepFTokenBalances)
	})
}

// --- AET-39: runAll step wrappers propagate write errors ----------------------------------------

func TestRunAllStepWrappersSaveErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	token := common.HexToAddress("0x00000000000000000000000000000000000000aa")

	t.Run("step C sc-locked write error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		sabotageOutputFile(t, dir, fileStepCSCLockedValues)
		lbt := []LBTEntry{{WrappedTokenAddress: token, Balance: "100"}}
		_, err := runAllStepC(ctx, &Config{}, dir, lbt, &StepBResult{})
		require.Error(t, err)
		require.Contains(t, err.Error(), fileStepCSCLockedValues)
	})

	t.Run("step D certificate write error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		sabotageOutputFile(t, dir, fileStepDCertificate)
		_, err := runAllStepD(&Config{}, dir, &StepBResult{}, &StepCResult{})
		require.Error(t, err)
		require.Contains(t, err.Error(), fileStepDCertificate)
	})

	t.Run("step F checks write error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		sabotageOutputFile(t, dir, fileStepFChecks)
		cfg := &Config{Options: Options{UseAgglayerAdminToStepFCheck: false}}
		cert := &agglayertypes.Certificate{}
		_, err := runAllStepF(ctx, cfg, dir, nil, cert, cert)
		require.Error(t, err)
		require.Contains(t, err.Error(), fileStepFChecks)
	})

	t.Run("step E unclaimed bridges write error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		sabotageOutputFile(t, dir, fileStepEUnclaimedBridges)
		_, err := runAllStepE(ctx, stepEConfig(emptyStepEStub(t)), dir, emptyCert())
		require.Error(t, err)
		require.Contains(t, err.Error(), fileStepEUnclaimedBridges)
	})

	t.Run("step F token balances write error", func(t *testing.T) {
		t.Parallel()
		srv := newRPCStub(t, func(string, []any) (json.RawMessage, *jsonRPCError) {
			return json.RawMessage(`{"balances":[]}`), nil
		})
		dir := t.TempDir()
		sabotageOutputFile(t, dir, fileStepFTokenBalances)
		// The agglayer dump goes to OutputDir (a separate writable dir); the wrapper's
		// token-balances copy goes to the sabotaged dir.
		cfg := &Config{Options: Options{
			AgglayerAdminURL: srv.URL, UseAgglayerAdminToStepFCheck: true, OutputDir: t.TempDir(),
		}}
		cert := &agglayertypes.Certificate{}
		_, err := runAllStepF(ctx, cfg, dir, nil, cert, cert)
		require.Error(t, err)
		require.Contains(t, err.Error(), fileStepFTokenBalances)
	})

	t.Run("step F capped final certificate write error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		sabotageOutputFile(t, dir, fileStepFCappedCertificate)
		cfg := &Config{Options: Options{
			UseAgglayerAdminToStepFCheck: false,
			IgnoreBalanceMismatch:        true,
			CapMode:                      CapModeByAppearance,
		}}
		// Certificate sum (1000) exceeds the LBT total (500) → mismatch → capped certificate.
		cert := &agglayertypes.Certificate{BridgeExits: []*agglayertypes.BridgeExit{
			MakeBridgeExit(0, token, 0, common.HexToAddress("0x1"), big.NewInt(1000)),
		}}
		lbt := []LBTEntry{{WrappedTokenAddress: token, OriginTokenAddress: token, Balance: "500"}}
		_, err := runAllStepF(ctx, cfg, dir, lbt, cert, cert)
		require.Error(t, err)
		require.Contains(t, err.Error(), fileStepFCappedCertificate)
	})
}

// emptyStepEStub serves the two RPCs a depositless Step E makes: the latest-block probe and the
// BridgeEvent log scan (always empty).
func emptyStepEStub(t *testing.T) string {
	t.Helper()
	return newBatchRPCServer(t, func(method string, _ []json.RawMessage) any {
		switch method {
		case rpcMethodEthBlockNumber:
			return "0x10"
		case rpcMethodEthGetLogs:
			return []map[string]string{}
		}
		t.Fatalf("unexpected method %s", method)
		return nil
	})
}

func TestSaveStepEFilesNilResult(t *testing.T) {
	t.Parallel()
	require.NoError(t, saveStepEFiles(t.TempDir(), nil))
}

func TestRunSingleESaveError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustSaveJSON(t, dir, fileStepDCertificate, &certificateJSON{NetworkID: 1})
	sabotageOutputFile(t, dir, fileStepEUnclaimedBridges)

	err := runSingleE(context.Background(), stepEConfig(emptyStepEStub(t)), dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), fileStepEUnclaimedBridges)
}

func TestRunSingleFCappedCertificateWriteError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	token := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	exits, err := json.Marshal([]map[string]any{{
		"leaf_type": "Transfer",
		"token_info": map[string]any{
			"origin_network":       0,
			"origin_token_address": token.Hex(),
		},
		"dest_network": 0,
		"dest_address": "0x1111111111111111111111111111111111111111",
		"amount":       "1000",
	}})
	require.NoError(t, err)
	mustSaveJSON(t, dir, fileStepDCertificate, &certificateJSON{NetworkID: 1, BridgeExits: exits})
	mustSaveJSON(t, dir, fileStep0LBT,
		[]LBTEntry{{WrappedTokenAddress: token, OriginTokenAddress: token, Balance: "500"}})
	sabotageOutputFile(t, dir, fileStepFCappedCertificate)

	cfg := &Config{Options: Options{
		UseAgglayerAdminToStepFCheck: false,
		IgnoreBalanceMismatch:        true,
		CapMode:                      CapModeByAppearance,
	}}
	err = runSingleF(context.Background(), cfg, dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), fileStepFCappedCertificate)
}

func TestRunSingle0SaveErrors(t *testing.T) {
	t.Parallel()
	for _, file := range []string{fileStep0TargetBlock, fileStep0LBT} {
		t.Run(file, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			sabotageOutputFile(t, dir, file)
			cfg := step0StubConfig(t)
			err := runSingle0(context.Background(), cfg, dir)
			require.Error(t, err)
			require.Contains(t, err.Error(), file)
		})
	}
}

func TestResolveOrGenerateLBTSaveErrors(t *testing.T) {
	t.Parallel()
	for _, file := range []string{fileStep0TargetBlock, fileStep0LBT} {
		t.Run(file, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			sabotageOutputFile(t, dir, file)
			_, _, _, err := resolveOrGenerateLBT(context.Background(), step0StubConfig(t), dir)
			require.Error(t, err)
			require.Contains(t, err.Error(), file)
		})
	}
}

// step0StubConfig wires a Config to a stub serving every RPC RunStep0 makes (see step0Stub).
func step0StubConfig(t *testing.T) *Config {
	t.Helper()
	origin := common.BytesToAddress([]byte("origin"))
	wrapped := common.BytesToAddress([]byte("wrapped"))
	return &Config{
		L2RPCURL:        step0Stub(t, makeWrappedTokenData(1, origin, wrapped)),
		L2BridgeAddress: common.BytesToAddress([]byte("bridge")),
		TargetBlock:     *aggkittypes.NewBlockNumber(100),
		Options:         Options{BlockRange: 50, ConcurrencyLimit: 2, RPCBatchSize: 10},
	}
}

func TestRunSingleG1SaveError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustSaveJSON(t, dir, fileStep0TargetBlock, uint64(100))
	sabotageOutputFile(t, dir, fileStepG1ShadowForkBlock)

	cfg := testConfig(t)
	cfg.Options.BlockRange = 100
	cfg.Options.ConcurrencyLimit = 2
	cfg.L2RPCURL = newEmptyLogsRPCServer(t)

	err := runSingleG1(context.Background(), cfg, dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), fileStepG1ShadowForkBlock)
}

func TestRunSingleB2SaveErrors(t *testing.T) {
	t.Parallel()
	for _, file := range []string{fileStepB2DetectedERC20s, fileStepB2DiscardedERC20s} {
		t.Run(file, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			// No contract addresses → RunStepB2 succeeds without any RPC.
			mustSaveJSON(t, dir, fileStepBContractAddresses, []common.Address{})
			mustSaveJSON(t, dir, fileStepAAddresses, []common.Address{})
			mustSaveJSON(t, dir, fileStep0TargetBlock, uint64(100))
			mustSaveJSON(t, dir, fileStep0LBT, []LBTEntry{{Balance: "1"}})
			sabotageOutputFile(t, dir, file)

			err := runSingleB2(context.Background(), &Config{}, dir)
			require.Error(t, err)
			require.Contains(t, err.Error(), file)
		})
	}
}
