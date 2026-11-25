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
	"github.com/ethereum/go-ethereum/common/hexutil"
)

// Recoveries step (delta mode, `--step recoveries`).
//
// It turns the recoveries already verified through the zkevm-proof-of-ownership-kit (one `recovery.json`
// per case — legacy format or version 1, see recoveries_v1.go — optionally packed in the kit's .tgz) into the
// delta exits file of a delta run
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

// RecoveryFile is the kit's legacy-format recovery.json (no `version` field): which funds, from which locked
// contract, to which wallet. Version 1 is RecoveryFileV1 (recoveries_v1.go).
type RecoveryFile struct {
	LockedContract string         `json:"locked_contract"`
	Destination    string         `json:"destination"`
	Funds          []RecoveryFund `json:"funds"`
}

// RecoveryLedgerEntry records one applied (case, token) — legacy format — or (case, output) — version 1 — so a
// case is never applied twice. The fields added for version 1 are omitted when empty, so the entries of the
// legacy format keep their shape.
type RecoveryLedgerEntry struct {
	CaseHash string `json:"caseHash"`
	Source   string `json:"source"`
	// RecoveryHash is the sha256 of the case's recovery.json (absent in entries written by older versions).
	RecoveryHash string `json:"recoveryHash,omitempty"`
	// Version of the case's recovery.json: absent for the legacy format.
	Version int `json:"version,omitempty"`
	// LockedContract is the legacy format's locked_contract (version 1 does not name it).
	LockedContract *common.Address `json:"lockedContract,omitempty"`
	// LeafType is the version-1 output's leaf_type (Transfer / Message); absent = Transfer.
	LeafType           string         `json:"leafType,omitempty"`
	Destination        common.Address `json:"destination"`
	DestinationNetwork uint32         `json:"destinationNetwork,omitempty"`
	// Token is the L2 token whose balance the entry consumes (zero address = native, and in a Message, which
	// consumes none).
	Token common.Address `json:"token"`
	// OriginNetwork / OriginTokenAddress are the exit's token_info: the token, or a Message's sender.
	OriginNetwork      uint32         `json:"originNetwork"`
	OriginTokenAddress common.Address `json:"originTokenAddress"`
	Amount             string         `json:"amount"`
	// Metadata is the version-1 output's leaf metadata (0x hex); absent = empty.
	Metadata string `json:"metadata,omitempty"`
}

func (l *RecoveryLedgerEntry) isMessage() bool { return l.LeafType == recoveryLeafMessage }

// recoveryCase is a recovery.json plus where it came from and its case hash.
type recoveryCase struct {
	Source   string
	CaseHash string
	// RecoveryHash is the sha256 of recovery.json alone, whatever the packaging: the same case delivered as a
	// .tgz and as its extracted files has the same RecoveryHash (but a different CaseHash without move.sh).
	RecoveryHash string
	// Recovery is set for the legacy format, Exits for version 1.
	Recovery *RecoveryFile
	Exits    []recoveryExit
}

func (c *recoveryCase) version() int {
	if c.Recovery != nil {
		return recoveryVersionLegacy
	}
	return recoveryVersionV1
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
	c := &recoveryCase{Source: file, CaseHash: recoveryCaseHash(recJSON, moveSh), RecoveryHash: hexSHA256(recJSON)}
	version, err := recoveryFileVersion(recJSON)
	if err == nil {
		if version == recoveryVersionLegacy {
			c.Recovery, err = parseRecoveryFile(recJSON)
		} else {
			c.Exits, err = parseRecoveryFileV1(recJSON)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return c, nil
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

// parseRecoveryFile parses and validates a legacy-format recovery.json with the same rules as the kit's
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
// in place and returns the new exits. On any error the state is left untouched. l2NetworkID is the exiting
// network (a version-1 Message comes from it; a Transfer cannot bridge a token native to it).
//
// Rules for every case: a case already in the ledger (same case hash) is skipped; a recovery.json already in
// the ledger under another case hash (the same recovery re-packaged with another move.sh) is an error; per
// token the new recoveries must fit in the pendingSCLockedBalance still left. Legacy cases: a (locked
// contract, token, destination) already recovered by a different case is an error, and the L2 token must be
// in the LBT. Version-1 cases: a Transfer's token_info must be an LBT origin (not a token native to the L2); a
// Message must come from the L2, go to another network and carries no value, so it consumes no balance.
func applyRecoveries(st *recoveriesState, l2NetworkID uint32, cases []recoveryCase) (*recoveriesOutcome, error) {
	idx := newRecoveryIndex(st)
	applied := map[string]bool{}
	byRecoveryHash := map[string]string{} // recovery.json sha256 → case hash
	recovered := map[lockedTokenKey]string{}
	inLedger := map[lockedTokenKey]bool{}
	for _, l := range st.Ledger {
		applied[l.CaseHash] = true
		if l.RecoveryHash != "" {
			byRecoveryHash[l.RecoveryHash] = l.CaseHash
		}
		if l.LockedContract == nil {
			continue // version 1: no locked contract
		}
		k := lockedTokenKey{*l.LockedContract, l.Token, l.Destination}
		recovered[k] = l.Source
		inLedger[k] = true
	}

	out := &recoveriesOutcome{}
	var errs []error
	perToken := map[tokenKey]*big.Int{}
	accrue := func(k tokenKey, amount *big.Int) {
		if perToken[k] == nil {
			perToken[k] = new(big.Int)
		}
		perToken[k].Add(perToken[k], amount)
	}
	for _, c := range cases {
		if applied[c.CaseHash] {
			out.Skipped = append(out.Skipped, c.Source)
			continue
		}
		if prev, dup := byRecoveryHash[c.RecoveryHash]; dup && c.RecoveryHash != "" {
			errs = append(errs, fmt.Errorf("%s: its %s (sha256 %s) is already in the recoveries ledger under case "+
				"hash %s: the same recovery re-packaged with another %s is not applied twice", c.Source,
				fileRecoveryJSON, c.RecoveryHash, prev, fileRecoveryMove))
			continue
		}
		applied[c.CaseHash] = true
		var entries []RecoveryLedgerEntry
		var caseErrs []error
		if c.Recovery != nil {
			entries, caseErrs = legacyRecoveryEntries(c, idx, recovered, inLedger, accrue)
		} else {
			entries, caseErrs = v1RecoveryEntries(c, idx, l2NetworkID, accrue)
		}
		errs = append(errs, caseErrs...)
		out.Applied = append(out.Applied, entries...)
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
	exits, err := buildRecoveryExits(out.Applied)
	if err != nil {
		return nil, err
	}
	out.Exits = exits
	return out, nil
}

// recoveryIndex resolves tokens against the LBT: L2 token → origin (legacy) and origin → L2 token (version 1).
type recoveryIndex struct {
	wrapped  map[common.Address]tokenKey
	byOrigin map[tokenKey]common.Address
}

func newRecoveryIndex(st *recoveriesState) *recoveryIndex {
	idx := &recoveryIndex{
		wrapped:  make(map[common.Address]tokenKey, len(st.LBT)),
		byOrigin: make(map[tokenKey]common.Address, len(st.LBT)),
	}
	for _, e := range st.LBT {
		k := tokenKey{e.OriginNetwork, e.OriginTokenAddress}
		idx.wrapped[e.WrappedTokenAddress] = k
		idx.byOrigin[k] = e.WrappedTokenAddress
	}
	return idx
}

// legacyRecoveryEntries builds the ledger entries of a legacy case: one per funds[] token.
func legacyRecoveryEntries(
	c recoveryCase, idx *recoveryIndex, recovered map[lockedTokenKey]string, inLedger map[lockedTokenKey]bool,
	accrue func(tokenKey, *big.Int),
) ([]RecoveryLedgerEntry, []error) {
	var entries []RecoveryLedgerEntry
	var errs []error
	locked := common.HexToAddress(c.Recovery.LockedContract)
	dest := common.HexToAddress(c.Recovery.Destination)
	for _, f := range c.Recovery.Funds {
		var l2Token common.Address // zero = native
		if strings.ToLower(f.Token) != recoveryNativeTok {
			l2Token = common.HexToAddress(f.Token)
		}
		origin, ok := idx.wrapped[l2Token]
		if !ok {
			errs = append(errs, fmt.Errorf("%s: token %s is not in the LBT (step-0-lbt.json)", c.Source, f.Token))
			continue
		}
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
		accrue(origin, amount)
		lockedAddr := locked
		entries = append(entries, RecoveryLedgerEntry{
			CaseHash: c.CaseHash, Source: c.Source, RecoveryHash: c.RecoveryHash, LockedContract: &lockedAddr,
			Destination: dest, Token: l2Token,
			OriginNetwork: origin.OriginNetwork, OriginTokenAddress: origin.OriginTokenAddress, Amount: f.Amount,
		})
	}
	return entries, errs
}

// v1RecoveryEntries builds the ledger entries of a version-1 case: one per output, in order.
func v1RecoveryEntries(
	c recoveryCase, idx *recoveryIndex, l2NetworkID uint32, accrue func(tokenKey, *big.Int),
) ([]RecoveryLedgerEntry, []error) {
	var entries []RecoveryLedgerEntry
	var errs []error
	for i, e := range c.Exits {
		w := fmt.Sprintf("%s: outputs[%d]", c.Source, i)
		entry := RecoveryLedgerEntry{
			CaseHash: c.CaseHash, Source: c.Source, RecoveryHash: c.RecoveryHash, Version: recoveryVersionV1,
			LeafType: e.LeafType.String(), Destination: e.DestinationAddress, DestinationNetwork: e.DestinationNetwork,
			OriginNetwork: e.TokenInfo.OriginNetwork, OriginTokenAddress: e.TokenInfo.OriginTokenAddress,
			Amount: e.Amount.String(),
		}
		if len(e.Metadata) > 0 {
			entry.Metadata = hexutil.Encode(e.Metadata)
		}
		if e.LeafType == bridgetypes.LeafTypeAsset {
			if e.TokenInfo.OriginNetwork == l2NetworkID {
				errs = append(errs, fmt.Errorf("%s: Transfer of %s, a token native to the L2 (origin network %d): a "+
					"recovery only bridges ETH and wrapped tokens", w, e.TokenInfo.OriginTokenAddress.Hex(), l2NetworkID))
				continue
			}
			l2Token, ok := idx.byOrigin[e.TokenInfo]
			if !ok {
				errs = append(errs, fmt.Errorf("%s: token %d/%s is not in the LBT (step-0-lbt.json)", w,
					e.TokenInfo.OriginNetwork, e.TokenInfo.OriginTokenAddress.Hex()))
				continue
			}
			entry.Token = l2Token
			accrue(e.TokenInfo, e.Amount)
			entries = append(entries, entry)
			continue
		}
		// Message: token_info is the sender, which the bridge always puts on the L2 network. It carries no value
		// (parseRecoveryFileV1), so it consumes no balance of this state.
		if e.TokenInfo.OriginNetwork != l2NetworkID {
			errs = append(errs, fmt.Errorf("%s: a Message's token_info.origin_network is the network that sent it "+
				"(the L2, %d), got %d", w, l2NetworkID, e.TokenInfo.OriginNetwork))
			continue
		}
		if e.DestinationNetwork == l2NetworkID {
			errs = append(errs, fmt.Errorf("%s: a Message to network %d, the exiting network itself, cannot be a "+
				"certificate exit", w, l2NetworkID))
			continue
		}
		entries = append(entries, entry)
	}
	return entries, errs
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

// buildRecoveryExits builds the delta exits of the applied entries. Legacy entries become one Transfer exit to
// L1 per (destination, origin token), summing the amounts of cases that coincide (the delta mode rejects
// identical exits), sorted for a deterministic file. Version-1 entries follow, one exit per output, field by
// field and in the order of the cases and of their outputs — the order of the BridgeEvents of the kit's
// simulated move: they are never merged nor reordered.
func buildRecoveryExits(applied []RecoveryLedgerEntry) ([]*agglayertypes.BridgeExit, error) {
	type key struct {
		Dest  common.Address
		Token tokenKey
	}
	sums := map[key]*big.Int{}
	var outputs []*agglayertypes.BridgeExit
	for _, a := range applied {
		amount, ok := new(big.Int).SetString(a.Amount, decimalBase)
		if !ok {
			return nil, fmt.Errorf("%s: amount %q is not a base-10 integer", a.Source, a.Amount)
		}
		token := tokenKey{a.OriginNetwork, a.OriginTokenAddress}
		if a.Version == recoveryVersionLegacy {
			k := key{a.Destination, token}
			if sums[k] == nil {
				sums[k] = new(big.Int)
			}
			sums[k].Add(sums[k], amount)
			continue
		}
		metadata, err := ledgerMetadata(a)
		if err != nil {
			return nil, err
		}
		leafType := bridgetypes.LeafTypeAsset
		if a.isMessage() {
			leafType = bridgetypes.LeafTypeMessage
		}
		outputs = append(outputs, &agglayertypes.BridgeExit{
			LeafType: leafType,
			TokenInfo: &agglayertypes.TokenInfo{
				OriginNetwork: token.OriginNetwork, OriginTokenAddress: token.OriginTokenAddress,
			},
			DestinationNetwork: a.DestinationNetwork,
			DestinationAddress: a.Destination,
			Amount:             amount,
			Metadata:           metadata,
		})
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
	exits := make([]*agglayertypes.BridgeExit, 0, len(keys)+len(outputs))
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
	return append(exits, outputs...), nil
}

// ledgerMetadata decodes the metadata of a ledger entry (empty for the legacy format).
func ledgerMetadata(a RecoveryLedgerEntry) ([]byte, error) {
	if a.Metadata == "" {
		return nil, nil
	}
	b, err := hexutil.Decode(a.Metadata)
	if err != nil {
		return nil, fmt.Errorf("%s: metadata %q: %w", a.Source, a.Metadata, err)
	}
	return b, nil
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
	legacy := 0
	for _, c := range cases {
		if c.version() == recoveryVersionLegacy {
			legacy++
		}
	}
	log.Infof("Recoveries: %d case(s) from %s (%d legacy format, %d version %d); state from %s (%d already applied)",
		len(cases), cfg.Options.RecoveriesPath, legacy, len(cases)-legacy, recoveryVersionV1, stateDir, len(st.Ledger))

	out, err := applyRecoveries(st, cfg.L2NetworkID, cases)
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
	transfers, messages := 0, 0
	for _, e := range out.Exits {
		if e.LeafType == bridgetypes.LeafTypeMessage {
			messages++
		} else {
			transfers++
		}
	}
	fmt.Fprintf(&b, "# Recoveries applied\n\nNew ledger entries: %d · exits: %d (Transfer: %d, Message: %d)\n\n",
		len(out.Applied), len(out.Exits), transfers, messages)
	if messages > 0 {
		fmt.Fprintf(&b, "The %d Message exit(s) carry no value: they move no balance of this state (what they trigger "+
			"on the destination network is not checked here).\n\n", messages)
	}
	b.WriteString("| origin | pending before | recovered | pending after |\n|---|---:|---:|---:|\n")
	for _, r := range out.Report {
		fmt.Fprintf(&b, "| %d/%s | %s | %s | %s |\n",
			r.Token.OriginNetwork, r.Token.OriginTokenAddress.Hex(), r.PendingBefore, r.Recovered, r.Aft)
	}
	return b.String()
}
