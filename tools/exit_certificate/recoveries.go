package exit_certificate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	bridgetypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
)

// Recoveries step (delta mode, `--step recoveries`).
//
// It turns the recoveries already verified through the zkevm-proof-of-ownership-kit (one `recovery.json`
// per case, optionally packed in the kit's .tgz) into the delta exits file of a delta run
// (options.deltaExitsFile) and keeps the balance files of the first run up to date, so the funds
// released by every recovery are accounted for exactly once:
//
//   - step-c-sc-locked-values.json: pendingSCLockedBalance (what the first certificate left behind) goes
//     down by the recovered amount and recoveredSCLockedBalance goes up.
//   - step-f-checks.json: the recovered amount moves from skippedSCLockedAmount to the certificate
//     (certificateAmount and lbtAmount, which Step F reports net of the skipped SC-locked value).
//   - recoveries-applied.json: the ledger of every applied case.
//
// The ownership/legitimacy of each case is NOT checked here: it is a manual prerequisite (the kit's
// verification). The step only checks that the money can be taken from the state we have, because the L2
// RPC no longer reflects reality and the agglayer offers no per-contract view.

const (
	// stepRecoveries is the explicit-only delta step that applies the recoveries.
	stepRecoveries = "recoveries"

	// dirRecoveries is the subdirectory of options.outputDir where the step writes the updated state. It is a
	// subdirectory because the delta pipeline itself writes step-f-checks.json in outputDir. The next
	// recoveries run reads it from <previousRun>/recoveries.
	dirRecoveries = "recoveries"

	fileRecoveriesLedger = "recoveries-applied.json"
	fileRecoveriesDelta  = "delta-exits.json"
	fileRecoveriesReport = "recoveries-report.md"

	fileRecoveryJSON  = "recovery.json"
	fileRecoveryMove  = "move.sh"
	maxRecoveryFile   = 16 << 20 // upper bound for a file read from a .tgz (untrusted input)
	recoveryNativeTok = "native"
)

var (
	recoveryAmountRe = regexp.MustCompile(`^[1-9][0-9]*$`)
	// kitPlaceholders are the template values the kit refuses for locked_contract / destination.
	kitPlaceholders = []common.Address{{}, common.HexToAddress("0x000000000000000000000000000000000000dEaD")}
)

// RecoveryFund is one `funds[]` entry of a kit recovery.json. Token is "native" or the L2 address of the
// ERC-20 (the kit works on the L2 shadow-fork).
type RecoveryFund struct {
	Token  string `json:"token"`
	Amount string `json:"amount"`
	Symbol string `json:"symbol,omitempty"`
}

// RecoveryFile is the kit's recovery.json: which funds, from which locked contract, to which wallet.
type RecoveryFile struct {
	LockedContract string         `json:"locked_contract"`
	Destination    string         `json:"destination"`
	Funds          []RecoveryFund `json:"funds"`
}

// RecoveryLedgerEntry records one applied (case, token) so a case is never applied twice.
type RecoveryLedgerEntry struct {
	CaseHash           string         `json:"caseHash"`
	Source             string         `json:"source"`
	LockedContract     common.Address `json:"lockedContract"`
	Destination        common.Address `json:"destination"`
	Token              common.Address `json:"token"` // L2 token (zero address = native)
	OriginNetwork      uint32         `json:"originNetwork"`
	OriginTokenAddress common.Address `json:"originTokenAddress"`
	Amount             string         `json:"amount"`
}

// recoveryCase is a recovery.json plus where it came from and its case hash.
type recoveryCase struct {
	Source   string
	CaseHash string
	// RecoveryHash is the sha256 of recovery.json alone, whatever the packaging: the same case delivered as a
	// .tgz and as its extracted files has the same RecoveryHash (but a different CaseHash without move.sh).
	RecoveryHash string
	Recovery     *RecoveryFile
}

// recoveriesState is the balance state the step reads and writes back.
type recoveriesState struct {
	LBT    []LBTEntry
	SC     []SCLockedValue
	Checks []TokenBalanceCheck
	Ledger []RecoveryLedgerEntry
}

// recoveryTokenReport is one row of the before/after summary.
type recoveryTokenReport struct {
	Token                         tokenKey
	PendingBefore, Recovered, Aft *big.Int
}

// recoveriesOutcome is the result of applying the cases to a state.
type recoveriesOutcome struct {
	Applied []RecoveryLedgerEntry
	Skipped []string // already-applied cases (idempotent re-runs)
	Exits   []*agglayertypes.BridgeExit
	Report  []recoveryTokenReport
}

// --- loading cases ---

// loadRecoveryCases reads the cases found at path: a .tgz of the kit, a recovery.json, or a directory
// searched recursively for both.
func loadRecoveryCases(p string) ([]recoveryCase, error) {
	info, err := os.Stat(p)
	if err != nil {
		return nil, fmt.Errorf("recoveries path: %w", err)
	}
	files := []string{p}
	if info.IsDir() {
		files = nil
		err = filepath.WalkDir(p, func(f string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && (strings.HasSuffix(f, ".tgz") || d.Name() == fileRecoveryJSON) {
				files = append(files, f)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk %s: %w", p, err)
		}
		sort.Strings(files)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no .tgz or %s found in %s", fileRecoveryJSON, p)
	}
	cases := make([]recoveryCase, 0, len(files))
	bySource := make(map[string]string, len(files)) // recovery.json sha256 → file
	for _, f := range files {
		c, err := loadRecoveryCase(f)
		if err != nil {
			return nil, err
		}
		if other, dup := bySource[c.RecoveryHash]; dup {
			return nil, fmt.Errorf("%s and %s carry the same %s (sha256 %s): provide each case once (a directory "+
				"holding a kit .tgz together with its extracted files counts it twice)", other, f, fileRecoveryJSON,
				c.RecoveryHash)
		}
		bySource[c.RecoveryHash] = f
		cases = append(cases, *c)
	}
	return cases, nil
}

func loadRecoveryCase(file string) (*recoveryCase, error) {
	var recJSON, moveSh []byte
	if strings.HasSuffix(file, ".tgz") {
		var err error
		recJSON, moveSh, err = readKitTarball(file)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
	} else {
		var err error
		if recJSON, err = os.ReadFile(file); err != nil {
			return nil, fmt.Errorf("read %s: %w", file, err)
		}
		// A missing move.sh next to a loose recovery.json is fine (hash falls back to recovery.json only).
		if mv, err := os.ReadFile(filepath.Join(filepath.Dir(file), fileRecoveryMove)); err == nil {
			moveSh = mv
		}
	}
	rec, err := parseRecoveryFile(recJSON)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return &recoveryCase{
		Source: file, CaseHash: recoveryCaseHash(recJSON, moveSh), RecoveryHash: hexSHA256(recJSON), Recovery: rec,
	}, nil
}

// readKitTarball extracts recovery.json (required) and move.sh (optional) from a kit .tgz. The archive is
// untrusted: nothing is written to disk and only those two files are read, each capped in size. An archive
// is one case: a second recovery.json or move.sh (at any depth) is an error, not something to ignore.
func readKitTarball(file string) (recJSON, moveSh []byte, err error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, nil, fmt.Errorf("not a gzip file: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var recName, moveName string
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("read tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		switch path.Base(hdr.Name) {
		case fileRecoveryJSON:
			if recName != "" {
				return nil, nil, fmt.Errorf("the archive has more than one %s (%s and %s): one case per archive",
					fileRecoveryJSON, recName, hdr.Name)
			}
			recName = hdr.Name
			if recJSON, err = readCapped(tr); err != nil {
				return nil, nil, err
			}
		case fileRecoveryMove:
			if moveName != "" {
				return nil, nil, fmt.Errorf("the archive has more than one %s (%s and %s): one case per archive",
					fileRecoveryMove, moveName, hdr.Name)
			}
			moveName = hdr.Name
			if moveSh, err = readCapped(tr); err != nil {
				return nil, nil, err
			}
		}
	}
	if recJSON == nil {
		return nil, nil, fmt.Errorf("the archive has no %s", fileRecoveryJSON)
	}
	return recJSON, moveSh, nil
}

func readCapped(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxRecoveryFile+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxRecoveryFile {
		return nil, fmt.Errorf("file larger than %d bytes", maxRecoveryFile)
	}
	return data, nil
}

// recoveryCaseHash is the kit's case hash (ownership-proof/case-hash.sh): the sha256 of the lines
// "<sha256 recovery.json>  recovery.json" and "<sha256 move.sh>  move.sh". Without a move.sh it falls
// back to the sha256 of recovery.json.
func recoveryCaseHash(recJSON, moveSh []byte) string {
	if moveSh == nil {
		return hexSHA256(recJSON)
	}
	lines := fmt.Sprintf("%s  %s\n%s  %s\n", hexSHA256(recJSON), fileRecoveryJSON, hexSHA256(moveSh), fileRecoveryMove)
	return hexSHA256([]byte(lines))
}

func hexSHA256(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// parseRecoveryFile parses and validates a recovery.json with the same rules as the kit's
// recovery-tx/validate-recovery.sh: no unknown fields, real addresses (not the zero/dead placeholders),
// destination != locked_contract, a non-empty funds list, positive base-unit amounts as strings, and no
// token listed twice.
func parseRecoveryFile(data []byte) (*RecoveryFile, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var rec RecoveryFile
	if err := dec.Decode(&rec); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", fileRecoveryJSON, err)
	}
	var errs []error
	locked, errLocked := recoveryAddress("locked_contract", rec.LockedContract)
	dest, errDest := recoveryAddress("destination", rec.Destination)
	errs = append(errs, errLocked, errDest)
	if errLocked == nil && errDest == nil && locked == dest {
		errs = append(errs, errors.New("destination must differ from locked_contract"))
	}
	if len(rec.Funds) == 0 {
		errs = append(errs, errors.New("funds must be a non-empty list"))
	}
	seen := map[string]bool{}
	for i, f := range rec.Funds {
		tok := strings.ToLower(f.Token)
		if tok != recoveryNativeTok {
			if _, err := recoveryAddress(fmt.Sprintf("funds[%d].token", i), f.Token); err != nil {
				errs = append(errs, err)
			}
		}
		if !recoveryAmountRe.MatchString(f.Amount) {
			errs = append(errs, fmt.Errorf("funds[%d].amount must be a positive integer in base units, got %q", i, f.Amount))
		}
		if seen[tok] {
			errs = append(errs, fmt.Errorf("token %s is listed more than once", f.Token))
		}
		seen[tok] = true
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return &rec, nil
}

func recoveryAddress(field, v string) (common.Address, error) {
	if !common.IsHexAddress(v) || !strings.HasPrefix(v, "0x") {
		return common.Address{}, fmt.Errorf("%s must be an address (0x + 40 hex chars), got %q", field, v)
	}
	a := common.HexToAddress(v)
	for _, p := range kitPlaceholders {
		if a == p {
			return common.Address{}, fmt.Errorf("%s is a template placeholder (%s)", field, a.Hex())
		}
	}
	return a, nil
}

// --- applying cases ---

// lockedTokenKey is what a recovery consumes: the funds of one token that a locked contract holds for one
// destination wallet. Several owners of the same vault/pool/multisig recover with different destinations; the
// same destination twice for the same contract+token is what the ledger refuses (a re-packaged copy).
type lockedTokenKey struct {
	Locked common.Address
	Token  common.Address
	Dest   common.Address
}

// applyRecoveries checks the cases against the state and, only if every check passes, updates the state
// in place and returns the new exits. On any error the state is left untouched.
//
// Rules: a case already in the ledger (same case hash) is skipped; a (locked contract, token, destination)
// already recovered by a different case is an error; per token the new recoveries must fit in the
// pendingSCLockedBalance still left; and a token must be known in the LBT.
func applyRecoveries(st *recoveriesState, cases []recoveryCase) (*recoveriesOutcome, error) {
	wrapped := make(map[common.Address]tokenKey, len(st.LBT))
	for _, e := range st.LBT {
		wrapped[e.WrappedTokenAddress] = tokenKey{e.OriginNetwork, e.OriginTokenAddress}
	}
	applied := map[string]bool{}
	recovered := map[lockedTokenKey]string{}
	inLedger := map[lockedTokenKey]bool{}
	for _, l := range st.Ledger {
		applied[l.CaseHash] = true
		k := lockedTokenKey{l.LockedContract, l.Token, l.Destination}
		recovered[k] = l.Source
		inLedger[k] = true
	}

	out := &recoveriesOutcome{}
	var errs []error
	perToken := map[tokenKey]*big.Int{}
	for _, c := range cases {
		if applied[c.CaseHash] {
			out.Skipped = append(out.Skipped, c.Source)
			continue
		}
		applied[c.CaseHash] = true
		locked := common.HexToAddress(c.Recovery.LockedContract)
		for _, f := range c.Recovery.Funds {
			var l2Token common.Address // zero = native
			if strings.ToLower(f.Token) != recoveryNativeTok {
				l2Token = common.HexToAddress(f.Token)
			}
			origin, ok := wrapped[l2Token]
			if !ok {
				errs = append(errs, fmt.Errorf("%s: token %s is not in the LBT (step-0-lbt.json)", c.Source, f.Token))
				continue
			}
			dest := common.HexToAddress(c.Recovery.Destination)
			k := lockedTokenKey{locked, l2Token, dest}
			if prev, dup := recovered[k]; dup {
				if inLedger[k] {
					errs = append(errs, fmt.Errorf("%s: %s already recovered %s for %s in %s (recoveries ledger); a "+
						"contract+token+destination is recovered once", c.Source, locked.Hex(), f.Token, dest.Hex(), prev))
				} else {
					errs = append(errs, fmt.Errorf("%s: %s and %s are two different cases recovering %s from %s for "+
						"%s in this same batch (a contract+token+destination is recovered once) — if they are the "+
						"same case in two packagings, supply it once", c.Source, prev, c.Source, f.Token,
						locked.Hex(), dest.Hex()))
				}
				continue
			}
			recovered[k] = c.Source
			amount, _ := new(big.Int).SetString(f.Amount, decimalBase) // validated by parseRecoveryFile
			if perToken[origin] == nil {
				perToken[origin] = new(big.Int)
			}
			perToken[origin].Add(perToken[origin], amount)
			out.Applied = append(out.Applied, RecoveryLedgerEntry{
				CaseHash: c.CaseHash, Source: c.Source, LockedContract: locked,
				Destination: common.HexToAddress(c.Recovery.Destination), Token: l2Token,
				OriginNetwork: origin.OriginNetwork, OriginTokenAddress: origin.OriginTokenAddress, Amount: f.Amount,
			})
		}
	}

	report, capErrs := checkPendingCapacity(st, perToken)
	errs = append(errs, capErrs...)
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	out.Report = report
	if len(out.Applied) == 0 {
		return out, nil
	}
	applyToState(st, perToken)
	st.Ledger = append(st.Ledger, out.Applied...)
	out.Exits = buildRecoveryExits(out.Applied)
	return out, nil
}

// checkPendingCapacity verifies, per token, that the new recoveries fit in pendingSCLockedBalance and in
// the skippedSCLockedAmount of Step F (when it has one for the token), and builds the before/after report.
func checkPendingCapacity(st *recoveriesState, perToken map[tokenKey]*big.Int) ([]recoveryTokenReport, []error) {
	var errs []error
	var report []recoveryTokenReport
	for _, k := range sortedTokenKeys(perToken) {
		total := perToken[k]
		sc := findSCLocked(st.SC, k)
		if sc == nil {
			errs = append(errs, fmt.Errorf("token %d/%s: no entry in step-c-sc-locked-values.json",
				k.OriginNetwork, k.OriginTokenAddress.Hex()))
			continue
		}
		pending, err := parseBigOrZero(sc.PendingSCLockedBalance)
		if err != nil {
			errs = append(errs, fmt.Errorf("token %d/%s pendingSCLockedBalance: %w",
				k.OriginNetwork, k.OriginTokenAddress.Hex(), err))
			continue
		}
		if total.Cmp(pending) > 0 {
			errs = append(errs, fmt.Errorf("token %d/%s: recovering %s exceeds pendingSCLockedBalance %s (short by %s)",
				k.OriginNetwork, k.OriginTokenAddress.Hex(), total, pending, new(big.Int).Sub(total, pending)))
			continue
		}
		if chk := findCheck(st.Checks, k); chk != nil && chk.SkippedSCLockedAmount != "" {
			skipped, err := parseBigOrZero(chk.SkippedSCLockedAmount)
			if err != nil || total.Cmp(skipped) > 0 {
				errs = append(errs, fmt.Errorf("token %d/%s: step-f-checks skippedSCLockedAmount %q cannot cover %s "+
					"(step C and step F state are inconsistent)", k.OriginNetwork, k.OriginTokenAddress.Hex(),
					chk.SkippedSCLockedAmount, total))
				continue
			}
		}
		report = append(report, recoveryTokenReport{
			Token: k, PendingBefore: pending, Recovered: total, Aft: new(big.Int).Sub(pending, total),
		})
	}
	return report, errs
}

// applyToState moves each token's recovered amount from "pending/skipped" to "recovered/certified". It
// assumes checkPendingCapacity passed.
func applyToState(st *recoveriesState, perToken map[tokenKey]*big.Int) {
	for k, total := range perToken {
		sc := findSCLocked(st.SC, k)
		pending, _ := parseBigOrZero(sc.PendingSCLockedBalance)
		already, _ := parseBigOrZero(sc.RecoveredSCLockedBalance)
		sc.PendingSCLockedBalance = new(big.Int).Sub(pending, total).String()
		sc.RecoveredSCLockedBalance = new(big.Int).Add(already, total).String()

		chk := findCheck(st.Checks, k)
		if chk == nil {
			continue
		}
		// Step F reports lbtAmount already net of the skipped SC-locked value (== certificateAmount), so the
		// recovered amount raises both and lowers the skipped one.
		cert, _ := parseBigOrZero(chk.CertificateAmount)
		chk.CertificateAmount = new(big.Int).Add(cert, total).String()
		if chk.LBTAmount != "" {
			lbt, _ := parseBigOrZero(chk.LBTAmount)
			chk.LBTAmount = new(big.Int).Add(lbt, total).String()
		}
		if chk.SkippedSCLockedAmount != "" {
			skipped, _ := parseBigOrZero(chk.SkippedSCLockedAmount)
			chk.SkippedSCLockedAmount = new(big.Int).Sub(skipped, total).String()
		}
	}
}

// buildRecoveryExits builds one Transfer exit to L1 per (destination, origin token), summing the amounts
// of cases that coincide (the delta mode rejects identical exits). Sorted for a deterministic file.
func buildRecoveryExits(applied []RecoveryLedgerEntry) []*agglayertypes.BridgeExit {
	type key struct {
		Dest  common.Address
		Token tokenKey
	}
	sums := map[key]*big.Int{}
	for _, a := range applied {
		k := key{a.Destination, tokenKey{a.OriginNetwork, a.OriginTokenAddress}}
		if sums[k] == nil {
			sums[k] = new(big.Int)
		}
		amount, _ := new(big.Int).SetString(a.Amount, decimalBase)
		sums[k].Add(sums[k], amount)
	}
	keys := make([]key, 0, len(sums))
	for k := range sums {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if c := bytes.Compare(keys[i].Dest[:], keys[j].Dest[:]); c != 0 {
			return c < 0
		}
		if keys[i].Token.OriginNetwork != keys[j].Token.OriginNetwork {
			return keys[i].Token.OriginNetwork < keys[j].Token.OriginNetwork
		}
		return bytes.Compare(keys[i].Token.OriginTokenAddress[:], keys[j].Token.OriginTokenAddress[:]) < 0
	})
	exits := make([]*agglayertypes.BridgeExit, 0, len(keys))
	for _, k := range keys {
		exits = append(exits, &agglayertypes.BridgeExit{
			LeafType: bridgetypes.LeafTypeAsset,
			TokenInfo: &agglayertypes.TokenInfo{
				OriginNetwork: k.Token.OriginNetwork, OriginTokenAddress: k.Token.OriginTokenAddress,
			},
			DestinationNetwork: 0,
			DestinationAddress: k.Dest,
			Amount:             sums[k],
		})
	}
	return exits
}

func findSCLocked(sc []SCLockedValue, k tokenKey) *SCLockedValue {
	for i := range sc {
		if sc[i].OriginNetwork == k.OriginNetwork && sc[i].OriginTokenAddress == k.OriginTokenAddress {
			return &sc[i]
		}
	}
	return nil
}

func findCheck(checks []TokenBalanceCheck, k tokenKey) *TokenBalanceCheck {
	for i := range checks {
		if checks[i].OriginNetwork == k.OriginNetwork &&
			common.HexToAddress(checks[i].OriginTokenAddress) == k.OriginTokenAddress {
			return &checks[i]
		}
	}
	return nil
}

func sortedTokenKeys(m map[tokenKey]*big.Int) []tokenKey {
	keys := make([]tokenKey, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].OriginNetwork != keys[j].OriginNetwork {
			return keys[i].OriginNetwork < keys[j].OriginNetwork
		}
		return bytes.Compare(keys[i].OriginTokenAddress[:], keys[j].OriginTokenAddress[:]) < 0
	})
	return keys
}

// parseBigOrZero parses a base-10 integer; the empty string is 0.
func parseBigOrZero(s string) (*big.Int, error) {
	if s == "" {
		return new(big.Int), nil
	}
	v, ok := new(big.Int).SetString(s, decimalBase)
	if !ok {
		return nil, fmt.Errorf("%q is not a base-10 integer", s)
	}
	return v, nil
}

// --- step orchestration ---

// recoveriesStateDir returns where the state this run starts from lives: <previousRun>/recoveries when a
// previous recoveries run left it there, otherwise the previous run's own files — but only when that run
// is a first (non-delta) run, whose step-c / step-f files are the original ones. A previous delta without
// recoveries state would make us start from stale balances, so that is an error.
func recoveriesStateDir(previousRun string) (string, error) {
	sub := filepath.Join(previousRun, dirRecoveries)
	if fileExists(filepath.Join(sub, fileStepCSCLockedValues)) {
		return sub, nil
	}
	if fileExists(filepath.Join(previousRun, fileDeltaManifest)) {
		return "", fmt.Errorf("previous run %s is a delta run without %s/ state: the balances it would start from "+
			"are stale; run --step recoveries on every delta (or restore that directory)", previousRun, dirRecoveries)
	}
	return previousRun, nil
}

func loadRecoveriesState(dir string) (*recoveriesState, error) {
	st := &recoveriesState{}
	if err := loadJSON(dir, fileStep0LBT, &st.LBT); err != nil {
		return nil, err
	}
	if err := loadJSON(dir, fileStepCSCLockedValues, &st.SC); err != nil {
		return nil, err
	}
	if err := loadJSON(dir, fileStepFChecks, &st.Checks); err != nil {
		return nil, err
	}
	if err := loadOptionalJSON(dir, fileRecoveriesLedger, &st.Ledger); err != nil {
		return nil, err
	}
	return st, nil
}

// runRecoveries is the `--step recoveries` entry point: it reads the state of options.previousRun and the
// cases of options.recoveriesPath, and writes the updated state, the ledger and the delta exits file to
// <outputDir>/recoveries. It never modifies the previous run.
func runRecoveries(cfg *Config) error {
	if cfg.Options.RecoveriesPath == "" {
		return fmt.Errorf("step %s requires options.recoveriesPath", stepRecoveries)
	}
	if sameDir(cfg.Options.OutputDir, cfg.Options.PreviousRun) {
		return fmt.Errorf("options.outputDir and options.previousRun must be different directories (%s)",
			cfg.Options.OutputDir)
	}
	outDir := filepath.Join(cfg.Options.OutputDir, dirRecoveries)
	if fileExists(filepath.Join(outDir, fileRecoveriesLedger)) || fileExists(filepath.Join(outDir, fileRecoveriesDelta)) {
		return fmt.Errorf("%s already holds the recoveries of an earlier --step recoveries run for this delta: "+
			"running it again would overwrite them and drop those cases (the state is always read from "+
			"options.previousRun). Put every case of this delta in options.recoveriesPath and remove %s to "+
			"regenerate, or use a new options.outputDir", outDir, outDir)
	}
	stateDir, err := recoveriesStateDir(cfg.Options.PreviousRun)
	if err != nil {
		return err
	}
	st, err := loadRecoveriesState(stateDir)
	if err != nil {
		return fmt.Errorf("load recoveries state from %s: %w", stateDir, err)
	}
	cases, err := loadRecoveryCases(cfg.Options.RecoveriesPath)
	if err != nil {
		return err
	}
	log.Infof("Recoveries: %d case(s) from %s; state from %s (%d already applied)",
		len(cases), cfg.Options.RecoveriesPath, stateDir, len(st.Ledger))

	out, err := applyRecoveries(st, cases)
	if err != nil {
		return fmt.Errorf("recoveries not applied (nothing written): %w", err)
	}
	for _, s := range out.Skipped {
		log.Infof("skipped %s: case already applied (same case hash)", s)
	}
	for _, r := range out.Report {
		log.Infof("token %d/%s: pending %s - recovered %s = %s",
			r.Token.OriginNetwork, r.Token.OriginTokenAddress.Hex(), r.PendingBefore, r.Recovered, r.Aft)
	}
	if len(out.Applied) == 0 {
		log.Info("nothing to apply: every case was already in the ledger")
		return nil
	}
	return writeRecoveriesOutput(outDir, st, out)
}

func writeRecoveriesOutput(dir string, st *recoveriesState, out *recoveriesOutcome) error {
	if err := os.MkdirAll(dir, dirPermissions); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	for _, w := range []struct {
		name string
		data any
	}{
		{fileStep0LBT, st.LBT}, // unchanged: copied so this dir is a complete state for the next run
		{fileStepCSCLockedValues, st.SC},
		{fileStepFChecks, st.Checks},
		{fileRecoveriesLedger, st.Ledger},
		{fileRecoveriesDelta, &DeltaExitsFile{BridgeExits: out.Exits}},
	} {
		if err := saveJSON(dir, w.name, w.data); err != nil {
			return err
		}
	}
	report := recoveriesReport(out)
	path := filepath.Join(dir, fileRecoveriesReport)
	if err := os.WriteFile(path, []byte(report), filePermissions); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	log.Infof("Written: %s", path)
	log.Infof("Delta exits file for options.deltaExitsFile: %s", filepath.Join(dir, fileRecoveriesDelta))
	return nil
}

func recoveriesReport(out *recoveriesOutcome) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Recoveries applied\n\nNew (case, token) entries: %d · exits: %d\n\n",
		len(out.Applied), len(out.Exits))
	b.WriteString("| origin | pending before | recovered | pending after |\n|---|---:|---:|---:|\n")
	for _, r := range out.Report {
		fmt.Fprintf(&b, "| %d/%s | %s | %s | %s |\n",
			r.Token.OriginNetwork, r.Token.OriginTokenAddress.Hex(), r.PendingBefore, r.Recovered, r.Aft)
	}
	return b.String()
}
