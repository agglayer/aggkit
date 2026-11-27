package exit_certificate

import (
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	bridgetypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/agglayer/aggkit/tools/exit_certificate/bridgesyncerlite"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// v1Funds renders an origin_SC_funds section.
func v1Funds(entries ...string) string { return `[` + strings.Join(entries, ",") + `]` }

func v1FundsEntry(contract string, tokens ...string) string {
	return `{"address":"` + contract + `","tokens":[` + strings.Join(tokens, ",") + `]}`
}

func v1FundsToken(originNetwork string, origin common.Address, amount string) string {
	return `{"token_info":{"origin_network":` + originNetwork + `,"origin_token_address":"` + origin.Hex() +
		`"},"amount":` + amount + `}`
}

// v1DocFunds renders a version-1 document with the given outputs (deposit counts 0, 1, ...) and a hand-written
// origin_SC_funds section ("" leaves the field out).
func v1DocFunds(funds string, outputs ...string) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(v1Doc(outputs...)), &m); err != nil {
		panic(err)
	}
	delete(m, "origin_SC_funds")
	if funds != "" {
		m["origin_SC_funds"] = json.RawMessage(funds)
	}
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestParseRecoveryFileV1_OriginSCFunds(t *testing.T) {
	const c1, c2 = "0x00000000000000000000000000000000000000c5", "0x00000000000000000000000000000000000000c6"
	transfer := v1Output("Transfer", 0, recOrigin, 0, recDest1, "50", "0x")
	ethTransfer := v1Output("Transfer", 0, common.Address{}, 0, recDest1, "5", "0x")
	messageETH := v1Output("Message", recL2Network, recSender, 0, recL1Target, "2", "0xbeef")
	message0 := v1Output("Message", recL2Network, recSender, 0, recL1Target, "0", "0xbeee")
	tok := func(amount string) string { return v1FundsToken("0", recOrigin, `"`+amount+`"`) }
	eth := func(amount string) string { return v1FundsToken("0", common.Address{}, `"`+amount+`"`) }

	// split over two contracts, a Message's ETH counted with the ETH Transfer, a value-less Message skipped.
	rec, err := parseRecoveryFileV1([]byte(v1DocFunds(v1Funds(
		v1FundsEntry(c1, tok("30"), eth("7")),
		v1FundsEntry(c2, tok("20"))), transfer, ethTransfer, messageETH, message0)))
	require.NoError(t, err)
	require.Len(t, rec.OriginSCFunds, 2)
	require.Equal(t, common.HexToAddress(c1), rec.OriginSCFunds[0].Contract)
	require.Equal(t, recoverySCToken{Token: tokenKey{}, Amount: big.NewInt(7)}, rec.OriginSCFunds[0].Tokens[1])

	// outputs that move nothing need no contract.
	_, err = parseRecoveryFileV1([]byte(v1DocFunds(`[]`, message0)))
	require.NoError(t, err)

	tests := map[string]struct{ doc, want string }{
		"missing":       {v1DocFunds("", transfer), "missing field: origin_SC_funds"},
		"not a list":    {v1DocFunds(`{}`, transfer), "origin_SC_funds"},
		"unknown field": {v1DocFunds(`[{"address":"`+c1+`","tokens":[`+tok("50")+`],"x":1}]`, transfer), "unknown field"},
		"unknown token field": {
			v1DocFunds(`[{"address":"`+c1+`","tokens":[{"token_info":{"origin_network":0,"origin_token_address":"`+
				recOrigin.Hex()+`"},"amount":"50","x":1}]}]`, transfer), "unknown field"},
		"unknown token_info field": {
			v1DocFunds(`[{"address":"`+c1+`","tokens":[{"token_info":{"origin_network":0,"origin_token_address":"`+
				recOrigin.Hex()+`","x":1},"amount":"50"}]}]`, transfer), "unknown field"},
		"missing address":    {v1DocFunds(`[{"tokens":[`+tok("50")+`]}]`, transfer), "origin_SC_funds[0]: missing field: address"},
		"bad address":        {v1DocFunds(v1Funds(v1FundsEntry("0x12", tok("50"))), transfer), "must be an address"},
		"no tokens":          {v1DocFunds(`[{"address":"`+c1+`"}]`, transfer), "tokens: must be a non-empty list"},
		"empty tokens":       {v1DocFunds(v1Funds(v1FundsEntry(c1)), transfer), "tokens: must be a non-empty list"},
		"missing token_info": {v1DocFunds(`[{"address":"`+c1+`","tokens":[{"amount":"50"}]}]`, transfer), "token_info: must be"},
		"missing origin": {
			v1DocFunds(`[{"address":"`+c1+`","tokens":[{"token_info":{"origin_token_address":"`+recOrigin.Hex()+
				`"},"amount":"50"}]}]`, transfer), "token_info: must be"},
		"zero amount":    {v1DocFunds(v1Funds(v1FundsEntry(c1, tok("0"))), transfer), "must be a positive integer"},
		"numeric amount": {v1DocFunds(v1Funds(v1FundsEntry(c1, v1FundsToken("0", recOrigin, "50"))), transfer), "amount"},
		"leading zero":   {v1DocFunds(v1Funds(v1FundsEntry(c1, tok("050"))), transfer), "must be a positive integer"},
		"missing amount": {
			v1DocFunds(`[{"address":"`+c1+`","tokens":[{"token_info":{"origin_network":0,"origin_token_address":"`+
				recOrigin.Hex()+`"}}]}]`, transfer), "must be a positive integer"},
		"contract twice (any case)": {
			v1DocFunds(v1Funds(v1FundsEntry(c1, tok("25")), v1FundsEntry("0x"+strings.ToUpper(c1[2:]),
				tok("25"))), transfer), "is listed more than once"},
		"token twice in a contract": {
			v1DocFunds(v1Funds(v1FundsEntry(c1, tok("25"), tok("25"))), transfer), "is listed more than once"},
		"totals short": {v1DocFunds(v1Funds(v1FundsEntry(c1, tok("49"))), transfer), "totals 49 over the contracts, " +
			"but the outputs bridge 50"},
		"token the outputs do not bridge": {
			v1DocFunds(v1Funds(v1FundsEntry(c1, tok("50"), eth("1"))), transfer), "totals 1 over the contracts, " +
				"but the outputs bridge 0"},
		"message ETH not covered": {
			v1DocFunds(v1Funds(v1FundsEntry(c1, tok("50"))), transfer, messageETH), "outputs bridge 2"},
		"funds for a value-less message": {v1DocFunds(v1Funds(v1FundsEntry(c1, eth("1"))), message0), "totals 1"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := parseRecoveryFileV1([]byte(tc.doc))
			require.ErrorContains(t, err, tc.want)
		})
	}
}

// A Message may carry ETH: it is charged to the ETH pending balance (and to its contract's ETH budget).
func TestApplyRecoveries_V1MessageWithValue(t *testing.T) {
	st := newRecoveriesState()
	out, err := applyRecoveries(st, testRecEnv(), []recoveryCase{v1Case(t, "m", "h", v1Doc(
		v1Output("Message", recL2Network, recSender, 0, recL1Target, "30", "0xbeef")))})
	require.NoError(t, err)
	require.Equal(t, "970", st.SC[0].PendingSCLockedBalance)
	require.Equal(t, "30", st.SC[0].RecoveredSCLockedBalance)
	require.Len(t, out.Exits, 1)
	require.Equal(t, bridgetypes.LeafTypeMessage, out.Exits[0].LeafType)
	require.Equal(t, "30", out.Exits[0].Amount.String())
	require.Equal(t, []RecoveredSCFunds{{Contract: recSCContract, SnapshotBalance: new(big.Int).Lsh(big.NewInt(1),
		128).String(), Recovered: "30"}}, st.SCRecovered)

	// and it cannot take more ETH than is pending.
	st = newRecoveriesState()
	_, err = applyRecoveries(st, testRecEnv(), []recoveryCase{v1Case(t, "m", "h", v1Doc(
		v1Output("Message", recL2Network, recSender, 0, recL1Target, "1001", "0xbeef")))})
	require.ErrorContains(t, err, "exceeds pendingSCLockedBalance")
	require.Equal(t, newRecoveriesState(), st)
}

func TestApplyRecoveries_SCBudget(t *testing.T) {
	other := common.HexToAddress("0x00000000000000000000000000000000000000c6")
	balances := map[common.Address]map[common.Address]int64{
		recSCContract: {recWrapped: 100, {}: 50},
		other:         {recWrapped: 10},
	}
	env := func() *recoveriesEnv {
		e := testRecEnv()
		e.SCBalance = func(contract, l2Token common.Address) (*big.Int, error) {
			return big.NewInt(balances[contract][l2Token]), nil
		}
		return e
	}
	transfer := func(amount string) string { return v1Output("Transfer", 0, recOrigin, 0, recDest1, amount, "0x") }

	// within the balance: recorded with the snapshot it is bounded by.
	st := newRecoveriesState()
	out, err := applyRecoveries(st, env(), []recoveryCase{v1Case(t, "a", "h-a", v1Doc(transfer("60")))})
	require.NoError(t, err)
	require.Equal(t, []RecoveredSCFunds{{Contract: recSCContract, OriginNetwork: 0, OriginTokenAddress: recOrigin,
		L2Token: recWrapped, SnapshotBalance: "100", Recovered: "60"}}, st.SCRecovered)
	require.Len(t, out.SCBudget, 1)
	require.Equal(t, "40", out.SCBudget[0].Aft.String())

	// a later delta: what is left is 40, so 41 is refused and the state is untouched; 40 fits.
	before := *st
	before.SC = append([]SCLockedValue(nil), st.SC...)
	_, err = applyRecoveries(st, env(), []recoveryCase{v1Case(t, "b", "h-b", v1Doc(transfer("41")))})
	require.ErrorContains(t, err, "contract "+recSCContract.Hex()+" held 100 of token 0/"+recOrigin.Hex()+
		" at the L2 snapshot, 60 were already recovered from it and this batch takes 41: short by 1")
	require.Equal(t, before.SCRecovered, st.SCRecovered)
	require.Equal(t, before.SC, st.SC)
	_, err = applyRecoveries(st, env(), []recoveryCase{v1Case(t, "b", "h-b", v1Doc(transfer("40")))})
	require.NoError(t, err)
	require.Equal(t, "100", st.SCRecovered[0].Recovered)

	// two cases of one batch from the same contract add up.
	st = newRecoveriesState()
	_, err = applyRecoveries(st, env(), []recoveryCase{
		v1Case(t, "a", "h-a", v1Doc(transfer("60"))),
		v1Case(t, "b", "h-b", v1DocAt(1, v1Output("Transfer", 0, recOrigin, 0, recDest2, "41", "0x"))),
	})
	require.ErrorContains(t, err, "this batch takes 101: short by 1")
	require.Equal(t, newRecoveriesState(), st)

	// per contract: another contract's balance does not count.
	st = newRecoveriesState()
	doc := v1DocFunds(v1Funds(v1FundsEntry(other.Hex(), v1FundsToken("0", recOrigin, `"11"`))), transfer("11"))
	_, err = applyRecoveries(st, env(), []recoveryCase{v1Case(t, "c", "h-c", doc)})
	require.ErrorContains(t, err, "contract "+other.Hex()+" held 10")

	// a Message's ETH is charged to the contract's ETH balance.
	_, err = applyRecoveries(newRecoveriesState(), env(), []recoveryCase{v1Case(t, "m", "h-m", v1Doc(
		v1Output("Message", recL2Network, recSender, 0, recL1Target, "51", "0xbeef")))})
	require.ErrorContains(t, err, "held 50 of token 0/"+common.Address{}.Hex())

	// a token not in the LBT, and a balance that cannot be read, are errors.
	notInLBT := common.HexToAddress("0x00000000000000000000000000000000000000f1")
	doc = v1DocFunds(v1Funds(v1FundsEntry(recSCContract.Hex(), v1FundsToken("0", recOrigin, `"1"`),
		v1FundsToken("0", notInLBT, `"1"`))), transfer("1"), v1Output("Transfer", 0, notInLBT, 0, recDest2, "1", "0x"))
	_, err = applyRecoveries(newRecoveriesState(), env(), []recoveryCase{v1Case(t, "x", "h-x", doc)})
	require.ErrorContains(t, err, "not in the LBT")
	broken := env()
	broken.SCBalance = func(common.Address, common.Address) (*big.Int, error) { return nil, errors.New("rpc down") }
	_, err = applyRecoveries(newRecoveriesState(), broken, []recoveryCase{v1Case(t, "a", "h-a", v1Doc(transfer("1")))})
	require.ErrorContains(t, err, "rpc down")
	noReader := env()
	noReader.SCBalance = nil
	_, err = applyRecoveries(newRecoveriesState(), noReader, []recoveryCase{v1Case(t, "a", "h-a", v1Doc(transfer("1")))})
	require.ErrorContains(t, err, "none can be read")
}

func TestApplyRecoveries_DepositCounts(t *testing.T) {
	transfer := v1Output("Transfer", 0, recOrigin, 0, recDest1, "10", "0x")
	message := v1Output("Message", recL2Network, recSender, 0, recL1Target, "0", "0xbeef")
	env := func(next uint32) *recoveriesEnv {
		e := testRecEnv()
		e.NextDepositCount = next
		return e
	}

	// cases in order continue each other's deposit counts, after the previous tree (40 leaves).
	st := newRecoveriesState()
	_, err := applyRecoveries(st, env(40), []recoveryCase{
		v1Case(t, "a", "h-a", v1DocAt(40, transfer, message)),
		v1Case(t, "b", "h-b", v1DocAt(42, v1Output("Message", recL2Network, recSender, 0, recL1Target, "0", "0xbeee"))),
	})
	require.NoError(t, err)
	require.Equal(t, uint32(42), *st.Ledger[2].DepositCount)

	for name, tc := range map[string]struct {
		cases func(t *testing.T) []recoveryCase
		want  string
	}{
		"stale: built on an older tree": {
			func(t *testing.T) []recoveryCase {
				t.Helper()
				return []recoveryCase{v1Case(t, "a", "h-a", v1DocAt(38, transfer, message))}
			},
			"a: outputs[0]: deposit_count 38, but this exit gets deposit count 40",
		},
		"second case not built on the first": {
			func(t *testing.T) []recoveryCase {
				t.Helper()
				return []recoveryCase{
					v1Case(t, "a", "h-a", v1DocAt(40, transfer)),
					v1Case(t, "b", "h-b", v1DocAt(40, message)),
				}
			},
			"b: outputs[0]: deposit_count 40, but this exit gets deposit count 41",
		},
		"legacy exits come first": {
			func(t *testing.T) []recoveryCase {
				t.Helper()
				return []recoveryCase{
					v1Case(t, "a", "h-a", v1DocAt(40, transfer)),
					recCase("legacy", "h-l", recLocked1, recDest2, RecoveryFund{Token: "native", Amount: "1"}),
				}
			},
			"a: outputs[0]: deposit_count 40, but this exit gets deposit count 41",
		},
		"outputs out of order": {
			func(t *testing.T) []recoveryCase {
				t.Helper()
				return []recoveryCase{v1Case(t, "a", "h-a", v1Doc(
					strings.Replace(transfer, `"amount"`, `"deposit_count":41,"amount"`, 1),
					strings.Replace(message, `"amount"`, `"deposit_count":40,"amount"`, 1)))}
			},
			"a: outputs[1]: deposit_count 40, but this exit gets deposit count 41",
		},
	} {
		t.Run(name, func(t *testing.T) {
			st := newRecoveriesState()
			_, err := applyRecoveries(st, env(40), tc.cases(t))
			require.ErrorContains(t, err, tc.want)
			require.Equal(t, newRecoveriesState(), st)
		})
	}
}

// Step G2 of the delta re-checks the leaves against the deposit counts the recoveries ledger recorded.
func TestVerifyRecoveryDepositCounts(t *testing.T) {
	st := newRecoveriesState()
	env := testRecEnv()
	env.NextDepositCount = 3
	out, err := applyRecoveries(st, env, []recoveryCase{v1Case(t, "a", "h-a", v1DocAt(3,
		v1Output("Message", recL2Network, recSender, 0, recL1Target, "0", "0xbeef"),
		v1Output("Transfer", 0, recOrigin, 0, recDest1, "10", "0xaa")))})
	require.NoError(t, err)
	recDir := filepath.Join(t.TempDir(), dirRecoveries)
	require.NoError(t, writeRecoveriesOutput(recDir, st, out))
	cfg := &Config{Options: Options{DeltaExitsFile: filepath.Join(recDir, fileRecoveriesDelta), OutputDir: t.TempDir()}}
	leavesOf := func(exits []*agglayertypes.BridgeExit) []bridgesyncerlite.BridgeLeaf {
		raw := make([][]byte, len(exits))
		for i, e := range exits {
			raw[i] = e.Metadata
		}
		return liteLeavesFromExits(exits, 10, 3, 0, common.Address{}, raw)
	}

	require.NoError(t, verifyRecoveryDepositCounts(cfg, &previousRun{Dir: t.TempDir()}, 3, leavesOf(out.Exits)))

	// the delta exits file reordered after --step recoveries.
	swapped := []*agglayertypes.BridgeExit{out.Exits[1], out.Exits[0]}
	err = verifyRecoveryDepositCounts(cfg, &previousRun{Dir: t.TempDir()}, 3, leavesOf(swapped))
	require.ErrorContains(t, err, "deposit count 3: the leaf has leaf type 0, the ledger Message")
	// an exit edited.
	edited := *out.Exits[1]
	edited.Amount = big.NewInt(11)
	err = verifyRecoveryDepositCounts(cfg, &previousRun{Dir: t.TempDir()}, 3,
		leavesOf([]*agglayertypes.BridgeExit{out.Exits[0], &edited}))
	require.ErrorContains(t, err, "the leaf has amount 11, the ledger 10")
	// appended after another tree: the recorded deposit counts fall outside the delta.
	err = verifyRecoveryDepositCounts(cfg, &previousRun{Dir: t.TempDir()}, 5, leavesOf(out.Exits))
	require.ErrorContains(t, err, "records deposit count 3 for an exit of this delta, but the delta appends deposit "+
		"counts 5..6")
	// an exit dropped.
	err = verifyRecoveryDepositCounts(cfg, &previousRun{Dir: t.TempDir()}, 3, leavesOf(out.Exits[:1]))
	require.ErrorContains(t, err, "records deposit count 4")

	// entries of earlier deltas (in the previous run's ledger) are not this delta's.
	prevDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(prevDir, dirRecoveries), 0o755))
	require.NoError(t, saveJSON(filepath.Join(prevDir, dirRecoveries), fileRecoveriesLedger, st.Ledger))
	require.NoError(t, verifyRecoveryDepositCounts(cfg, &previousRun{Dir: prevDir}, 9, leavesOf(out.Exits[:1])))

	// no ledger (a delta exits file written by hand): nothing to check.
	require.NoError(t, verifyRecoveryDepositCounts(&Config{Options: Options{
		DeltaExitsFile: filepath.Join(t.TempDir(), "x.json"), OutputDir: t.TempDir()}}, nil, 0, leavesOf(swapped)))
}
