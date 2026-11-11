package exit_certificate

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

var (
	recWrapped = common.HexToAddress("0x00000000000000000000000000000000000000b1") // L2 address of the ERC-20
	recOrigin  = common.HexToAddress("0x00000000000000000000000000000000000000a1") // its L1 origin
	recLocked1 = "0x1111111111111111111111111111111111111111"
	recLocked2 = "0x4444444444444444444444444444444444444444"
	recDest1   = "0x2222222222222222222222222222222222222222"
	recDest2   = "0x3333333333333333333333333333333333333333"
)

const (
	recNativePending = "1000" // pending native SC-locked
	recTokenPending  = "500"  // pending ERC-20 SC-locked
)

// newRecoveriesState builds a state shaped like the first run's files: native + one ERC-20 token, each
// with a pendingSCLockedBalance and the matching Step F check (lbt == certificate, skipped == pending).
func newRecoveriesState() *recoveriesState {
	return &recoveriesState{
		LBT: []LBTEntry{
			{WrappedTokenAddress: common.Address{}, OriginNetwork: 0, OriginTokenAddress: common.Address{}, Balance: "5000"},
			{WrappedTokenAddress: recWrapped, OriginNetwork: 0, OriginTokenAddress: recOrigin, Balance: "800"},
		},
		SC: []SCLockedValue{
			{OriginNetwork: 0, OriginTokenAddress: common.Address{}, PendingSCLockedBalance: recNativePending},
			{WrappedTokenAddress: recWrapped, OriginNetwork: 0, OriginTokenAddress: recOrigin,
				PendingSCLockedBalance: recTokenPending},
		},
		Checks: []TokenBalanceCheck{
			{OriginNetwork: 0, OriginTokenAddress: common.Address{}.Hex(), LBTAmount: "4000", CertificateAmount: "4000",
				Match: true, SkippedSCLockedAmount: recNativePending},
			{OriginNetwork: 0, OriginTokenAddress: recOrigin.Hex(), LBTAmount: "300", CertificateAmount: "300",
				Match: true, SkippedSCLockedAmount: recTokenPending},
		},
	}
}

func recCase(source, hash, locked, dest string, funds ...RecoveryFund) recoveryCase {
	return recoveryCase{Source: source, CaseHash: hash,
		Recovery: &RecoveryFile{LockedContract: locked, Destination: dest, Funds: funds}}
}

func TestParseRecoveryFile(t *testing.T) {
	valid := `{"locked_contract":"` + recLocked1 + `","destination":"` + recDest1 +
		`","funds":[{"token":"native","amount":"10"},{"token":"` + recWrapped.Hex() + `","amount":"5","symbol":"X"}]}`
	rec, err := parseRecoveryFile([]byte(valid))
	require.NoError(t, err)
	require.Len(t, rec.Funds, 2)

	tests := map[string]string{
		"unknown field": `{"locked_contract":"` + recLocked1 + `","destination":"` + recDest1 +
			`","funds":[{"token":"native","amount":"1"}],"extra":1}`,
		"zero placeholder": `{"locked_contract":"0x0000000000000000000000000000000000000000","destination":"` + recDest1 +
			`","funds":[{"token":"native","amount":"1"}]}`,
		"dead placeholder": `{"locked_contract":"` + recLocked1 + `","destination":"0x000000000000000000000000000000000000dEaD",` +
			`"funds":[{"token":"native","amount":"1"}]}`,
		"same address": `{"locked_contract":"` + recLocked1 + `","destination":"` + recLocked1 +
			`","funds":[{"token":"native","amount":"1"}]}`,
		"no funds": `{"locked_contract":"` + recLocked1 + `","destination":"` + recDest1 + `","funds":[]}`,
		"zero amount": `{"locked_contract":"` + recLocked1 + `","destination":"` + recDest1 +
			`","funds":[{"token":"native","amount":"0"}]}`,
		"numeric amount": `{"locked_contract":"` + recLocked1 + `","destination":"` + recDest1 +
			`","funds":[{"token":"native","amount":1}]}`,
		"repeated token": `{"locked_contract":"` + recLocked1 + `","destination":"` + recDest1 +
			`","funds":[{"token":"native","amount":"1"},{"token":"NATIVE","amount":"2"}]}`,
		"bad token": `{"locked_contract":"` + recLocked1 + `","destination":"` + recDest1 +
			`","funds":[{"token":"eth","amount":"1"}]}`,
	}
	for name, doc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := parseRecoveryFile([]byte(doc))
			require.Error(t, err)
		})
	}
}

func TestApplyRecoveries_UpdatesBalances(t *testing.T) {
	st := newRecoveriesState()
	out, err := applyRecoveries(st, []recoveryCase{
		recCase("c1", "h1", recLocked1, recDest1,
			RecoveryFund{Token: "native", Amount: "100"}, RecoveryFund{Token: recWrapped.Hex(), Amount: "50"}),
		recCase("c2", "h2", recLocked2, recDest1, RecoveryFund{Token: "native", Amount: "30"}),
	})
	require.NoError(t, err)

	// step C: pending down, recovered up.
	require.Equal(t, "870", st.SC[0].PendingSCLockedBalance)
	require.Equal(t, "130", st.SC[0].RecoveredSCLockedBalance)
	require.Equal(t, "450", st.SC[1].PendingSCLockedBalance)
	require.Equal(t, "50", st.SC[1].RecoveredSCLockedBalance)
	// step F: lbt and certificate up (they stay equal), skipped down.
	require.Equal(t, "4130", st.Checks[0].CertificateAmount)
	require.Equal(t, "4130", st.Checks[0].LBTAmount)
	require.Equal(t, "870", st.Checks[0].SkippedSCLockedAmount)
	require.Equal(t, "350", st.Checks[1].CertificateAmount)
	require.Equal(t, "450", st.Checks[1].SkippedSCLockedAmount)
	// the ledger holds every (case, token).
	require.Len(t, st.Ledger, 3)
	require.Len(t, out.Applied, 3)
	// one exit per (destination, token): the two native recoveries to recDest1 are merged.
	require.Len(t, out.Exits, 2)
	for _, e := range out.Exits {
		if e.TokenInfo.OriginTokenAddress == (common.Address{}) {
			require.Equal(t, "130", e.Amount.String())
		} else {
			require.Equal(t, "50", e.Amount.String())
		}
		require.Equal(t, common.HexToAddress(recDest1), e.DestinationAddress)
		require.Zero(t, e.DestinationNetwork)
	}
}

func TestApplyRecoveries_Idempotent(t *testing.T) {
	st := newRecoveriesState()
	c := recCase("c1", "h1", recLocked1, recDest1, RecoveryFund{Token: "native", Amount: "100"})
	_, err := applyRecoveries(st, []recoveryCase{c})
	require.NoError(t, err)

	out, err := applyRecoveries(st, []recoveryCase{c}) // same case hash, again
	require.NoError(t, err)
	require.Equal(t, []string{"c1"}, out.Skipped)
	require.Empty(t, out.Applied)
	require.Equal(t, "900", st.SC[0].PendingSCLockedBalance, "applying twice must not move the balance twice")
	require.Len(t, st.Ledger, 1)
}

func TestApplyRecoveries_SameCaseTwiceInBatch(t *testing.T) {
	st := newRecoveriesState()
	c := recCase("c1", "h1", recLocked1, recDest1, RecoveryFund{Token: "native", Amount: "100"})
	dup := c
	dup.Source = "c1-copy"
	out, err := applyRecoveries(st, []recoveryCase{c, dup})
	require.NoError(t, err)
	require.Equal(t, []string{"c1-copy"}, out.Skipped)
	require.Equal(t, "900", st.SC[0].PendingSCLockedBalance)
}

func TestApplyRecoveries_Errors_LeaveStateUntouched(t *testing.T) {
	tests := map[string]struct {
		cases []recoveryCase
		want  string
	}{
		"exceeds pending": {
			cases: []recoveryCase{recCase("c", "h", recLocked1, recDest1, RecoveryFund{Token: "native", Amount: "1001"})},
			want:  "exceeds pendingSCLockedBalance",
		},
		"sum of cases exceeds pending": {
			cases: []recoveryCase{
				recCase("a", "h1", recLocked1, recDest1, RecoveryFund{Token: "native", Amount: "600"}),
				recCase("b", "h2", recLocked2, recDest2, RecoveryFund{Token: "native", Amount: "401"}),
			},
			want: "short by 1",
		},
		"unknown token": {
			cases: []recoveryCase{recCase("c", "h", recLocked1, recDest1,
				RecoveryFund{Token: "0x00000000000000000000000000000000000000ff", Amount: "1"})},
			want: "not in the LBT",
		},
		"same contract and token in another case": {
			cases: []recoveryCase{
				recCase("a", "h1", recLocked1, recDest1, RecoveryFund{Token: "native", Amount: "1"}),
				recCase("b", "h2", recLocked1, recDest2, RecoveryFund{Token: "native", Amount: "2"}),
			},
			want: "recovered once",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			st := newRecoveriesState()
			_, err := applyRecoveries(st, tc.cases)
			require.ErrorContains(t, err, tc.want)
			require.Equal(t, newRecoveriesState(), st, "state must not change on error (all or nothing)")
		})
	}
}

func TestApplyRecoveries_RejectsContractAlreadyInLedger(t *testing.T) {
	st := newRecoveriesState()
	_, err := applyRecoveries(st, []recoveryCase{
		recCase("first", "h1", recLocked1, recDest1, RecoveryFund{Token: "native", Amount: "1"})})
	require.NoError(t, err)
	_, err = applyRecoveries(st, []recoveryCase{
		recCase("second", "h2", recLocked1, recDest2, RecoveryFund{Token: "native", Amount: "1"})})
	require.ErrorContains(t, err, "already recovered")
	require.ErrorContains(t, err, "first")
}

func TestApplyRecoveries_InconsistentStepF(t *testing.T) {
	st := newRecoveriesState()
	st.Checks[0].SkippedSCLockedAmount = "10" // less than the pending in step C
	_, err := applyRecoveries(st, []recoveryCase{
		recCase("c", "h", recLocked1, recDest1, RecoveryFund{Token: "native", Amount: "100"})})
	require.ErrorContains(t, err, "inconsistent")
}

func writeKitTgz(t *testing.T, dir, name string, files map[string]string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	require.NoError(t, err)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for n, c := range files {
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: "case/" + n, Mode: 0o600, Size: int64(len(c)), Typeflag: tar.TypeReg}))
		_, err := tw.Write([]byte(c))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	require.NoError(t, f.Close())
	return p
}

var recDoc = `{"locked_contract":"` + recLocked1 + `","destination":"` + recDest1 +
	`","funds":[{"token":"native","amount":"100"}]}`

func TestLoadRecoveryCases_TgzAndCaseHash(t *testing.T) {
	dir := t.TempDir()
	writeKitTgz(t, dir, "a.tgz", map[string]string{"recovery.json": recDoc, "move.sh": "#!/bin/bash\n", "signatures.json": "[]"})
	cases, err := loadRecoveryCases(dir)
	require.NoError(t, err)
	require.Len(t, cases, 1)
	// the kit's case hash: sha256 of the two "<sha256>  <name>" lines.
	require.Equal(t, recoveryCaseHash([]byte(recDoc), []byte("#!/bin/bash\n")), cases[0].CaseHash)
	require.NotEqual(t, hexSHA256([]byte(recDoc)), cases[0].CaseHash)

	// changing move.sh changes the hash (as it invalidates the kit's signatures).
	require.NotEqual(t, recoveryCaseHash([]byte(recDoc), []byte("#!/bin/bash\n")),
		recoveryCaseHash([]byte(recDoc), []byte("#!/bin/bash\n# x\n")))

	// The same recovery.json delivered twice — the .tgz and its extracted file (which, without move.sh, would
	// get another case hash) — is refused instead of tripping the contract+token guard later.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "recovery.json"), []byte(recDoc), 0o600))
	_, err = loadRecoveryCases(dir)
	require.ErrorContains(t, err, "carry the same recovery.json")
}

// A kit archive is one case: more than one recovery.json / move.sh is an error, not silently ignored.
func TestReadKitTarball_OneCasePerArchive(t *testing.T) {
	dir := t.TempDir()
	two := writeKitTgz(t, dir, "two.tgz", map[string]string{"a/recovery.json": recDoc, "b/recovery.json": recDoc})
	_, err := loadRecoveryCases(two)
	require.ErrorContains(t, err, "more than one recovery.json")

	moves := writeKitTgz(t, dir, "moves.tgz", map[string]string{"recovery.json": recDoc, "a/move.sh": "1", "b/move.sh": "2"})
	_, err = loadRecoveryCases(moves)
	require.ErrorContains(t, err, "more than one move.sh")
}

func TestLoadRecoveryCases_Errors(t *testing.T) {
	dir := t.TempDir()
	_, err := loadRecoveryCases(dir)
	require.ErrorContains(t, err, "no .tgz")

	bad := writeKitTgz(t, dir, "nojson.tgz", map[string]string{"move.sh": "x"})
	_, err = loadRecoveryCases(bad)
	require.ErrorContains(t, err, "no recovery.json")

	require.NoError(t, os.Remove(bad))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "recovery.json"), []byte(`{"funds":[]}`), 0o600))
	_, err = loadRecoveryCases(dir)
	require.Error(t, err)
}

func writeJSONFile(t *testing.T, dir, name string, v any) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	b, err := json.Marshal(v)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), b, 0o600))
}

func TestRunRecoveries_EndToEndAndChaining(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "output-first")
	st := newRecoveriesState()
	writeJSONFile(t, first, fileStep0LBT, st.LBT)
	writeJSONFile(t, first, fileStepCSCLockedValues, st.SC)
	writeJSONFile(t, first, fileStepFChecks, st.Checks)

	casesDir := filepath.Join(root, "cases")
	require.NoError(t, os.MkdirAll(casesDir, 0o755))
	writeKitTgz(t, casesDir, "a.tgz", map[string]string{"recovery.json": recDoc, "move.sh": "m1"})

	cfg := &Config{L2NetworkID: 7, Options: Options{
		DeltaMode: true, PreviousRun: first, OutputDir: filepath.Join(root, "output-delta-1"), RecoveriesPath: casesDir,
	}}
	require.NoError(t, runRecoveries(cfg))

	out1 := filepath.Join(cfg.Options.OutputDir, dirRecoveries)
	// the previous run is untouched.
	var firstSC []SCLockedValue
	require.NoError(t, loadJSON(first, fileStepCSCLockedValues, &firstSC))
	require.Equal(t, recNativePending, firstSC[0].PendingSCLockedBalance)
	// the updated state and the delta exits file are written, and the latter is a valid deltaExitsFile.
	var sc []SCLockedValue
	require.NoError(t, loadJSON(out1, fileStepCSCLockedValues, &sc))
	require.Equal(t, "900", sc[0].PendingSCLockedBalance)
	exits, err := LoadDeltaExits(filepath.Join(out1, fileRecoveriesDelta), 7)
	require.NoError(t, err)
	require.Len(t, exits, 1)
	require.Equal(t, "100", exits[0].Amount.String())
	require.FileExists(t, filepath.Join(out1, fileRecoveriesReport))

	// re-running the same case on the same state is idempotent (nothing to apply).
	cfgAgain := *cfg
	cfgAgain.Options.OutputDir = filepath.Join(root, "output-delta-1b")
	cfgAgain.Options.PreviousRun = cfg.Options.OutputDir // chained on delta 1 (which carries recoveries/ state)
	// delta 1 is not a settled delta here, but recoveriesStateDir only needs its recoveries/ subdir.
	require.NoError(t, runRecoveries(&cfgAgain))
	require.NoFileExists(t, filepath.Join(cfgAgain.Options.OutputDir, dirRecoveries, fileRecoveriesDelta),
		"an already-applied case produces no new exits")

	// a second, different case chains on the first one's state and respects the remaining pending.
	writeKitTgz(t, casesDir, "b.tgz", map[string]string{"recovery.json": `{"locked_contract":"` + recLocked2 +
		`","destination":"` + recDest2 + `","funds":[{"token":"native","amount":"901"}]}`, "move.sh": "m2"})
	cfg2 := *cfg
	cfg2.Options.PreviousRun = cfg.Options.OutputDir
	cfg2.Options.OutputDir = filepath.Join(root, "output-delta-2")
	require.ErrorContains(t, runRecoveries(&cfg2), "exceeds pendingSCLockedBalance") // 901 > 900 left
}

// Two different cases for the same contract+token inside one batch (typically one case in two packagings)
// get a message that says so, not the ledger one.
func TestApplyRecoveries_SameContractTokenInBatchMessage(t *testing.T) {
	st := newRecoveriesState()
	_, err := applyRecoveries(st, []recoveryCase{
		recCase("x.tgz", "h1", recLocked1, recDest1, RecoveryFund{Token: "native", Amount: "1"}),
		recCase("y/recovery.json", "h2", recLocked1, recDest1, RecoveryFund{Token: "native", Amount: "1"}),
	})
	require.ErrorContains(t, err, "two different cases")
	require.ErrorContains(t, err, "in this same batch")
}

// Running --step recoveries again into the same outputDir would overwrite its state (which is always read
// from previousRun) and drop the earlier cases: it must fail instead.
func TestRunRecoveries_RefusesToOverwriteEarlierOutput(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "output-first")
	st := newRecoveriesState()
	writeJSONFile(t, first, fileStep0LBT, st.LBT)
	writeJSONFile(t, first, fileStepCSCLockedValues, st.SC)
	writeJSONFile(t, first, fileStepFChecks, st.Checks)
	casesDir := filepath.Join(root, "cases")
	require.NoError(t, os.MkdirAll(casesDir, 0o755))
	writeKitTgz(t, casesDir, "a.tgz", map[string]string{"recovery.json": recDoc, "move.sh": "m1"})
	cfg := &Config{L2NetworkID: 7, Options: Options{
		DeltaMode: true, PreviousRun: first, OutputDir: filepath.Join(root, "output-delta-1"), RecoveriesPath: casesDir,
	}}
	require.NoError(t, runRecoveries(cfg))
	exitsFile := filepath.Join(cfg.Options.OutputDir, dirRecoveries, fileRecoveriesDelta)
	before, err := os.ReadFile(exitsFile)
	require.NoError(t, err)

	// a second batch arrives before the delta is generated: re-running into the same dir is refused ...
	writeKitTgz(t, casesDir, "b.tgz", map[string]string{"recovery.json": `{"locked_contract":"` + recLocked2 +
		`","destination":"` + recDest2 + `","funds":[{"token":"native","amount":"5"}]}`, "move.sh": "m2"})
	require.ErrorContains(t, runRecoveries(cfg), "already holds the recoveries of an earlier")
	after, err := os.ReadFile(exitsFile)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after), "the earlier output is left untouched")

	// ... and removing the directory regenerates it with every case of the delta.
	require.NoError(t, os.RemoveAll(filepath.Join(cfg.Options.OutputDir, dirRecoveries)))
	require.NoError(t, runRecoveries(cfg))
	exits, err := LoadDeltaExits(exitsFile, 7)
	require.NoError(t, err)
	require.Len(t, exits, 2)
}

func TestRecoveriesStateDir(t *testing.T) {
	dir := t.TempDir()
	// first (non-delta) run: its own files.
	got, err := recoveriesStateDir(dir)
	require.NoError(t, err)
	require.Equal(t, dir, got)

	// a delta run without recoveries/ state is refused.
	require.NoError(t, os.WriteFile(filepath.Join(dir, fileDeltaManifest), []byte("{}"), 0o600))
	_, err = recoveriesStateDir(dir)
	require.ErrorContains(t, err, "stale")

	// ... and with it, it is used.
	writeJSONFile(t, filepath.Join(dir, dirRecoveries), fileStepCSCLockedValues, []SCLockedValue{})
	got, err = recoveriesStateDir(dir)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, dirRecoveries), got)
}

func TestRunRecoveries_RequiresPathAndDistinctDirs(t *testing.T) {
	require.ErrorContains(t, runRecoveries(&Config{Options: Options{}}), "recoveriesPath")
	d := t.TempDir()
	require.ErrorContains(t,
		runRecoveries(&Config{Options: Options{PreviousRun: d, OutputDir: d, RecoveriesPath: d}}), "different directories")
}
