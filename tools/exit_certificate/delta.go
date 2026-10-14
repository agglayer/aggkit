package exit_certificate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"

	"github.com/agglayer/aggkit/agglayer"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	bridgetypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/tools/exit_certificate/bridgesyncerlite"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// Delta mode (options.deltaMode) builds a certificate that only carries NEW bridge exits chained on
// top of a previous, already settled run. Where the full pipeline rebuilds every exit by scanning the
// L2, the delta takes its exits from an external file (options.deltaExitsFile), skips the L2 scan
// steps (0, A, B, C, D, E) and continues the previous run's local exit tree instead of rebuilding it.
//
// Pipeline: CHECK → F → G2 → H → I → SIGN, then (explicitly) SUBMIT → WAIT as in the full pipeline.
//
// Besides the (delta-only) certificate it writes `exit-certificate-full.json`: the same certificate
// shape carrying EVERY bridge exit (previous runs + this delta) with the final new_local_exit_root.
// It is not submittable — the agglayer only takes the new exits — but it is the input of the next
// delta and of the claimer, whose proofs must be built against the latest root for old and new exits
// alike.

// DeltaExitsFile is the format of options.deltaExitsFile: the new bridge exits, using the same
// BridgeExit shape as the certificate's `bridge_exits`.
//
// One difference: `metadata`, when present, is the RAW leaf metadata (hex, no 0x prefix) — not its
// keccak256 hash as in a certificate. The tool hashes it. When absent (or empty) it is derived from
// the L2 bridge the way bridgeAsset does (name/symbol/decimals of the L2 token for ERC-20 exits, the
// gas token metadata for native exits).
type DeltaExitsFile struct {
	BridgeExits []*agglayertypes.BridgeExit `json:"bridge_exits"`
}

// DeltaManifest links a delta run to the run it is chained on. It is written next to the delta outputs
// as delta-manifest.json.
type DeltaManifest struct {
	NetworkID uint32 `json:"networkId"`
	// PreviousRun is the previous run's output dir as configured (options.previousRun).
	PreviousRun string `json:"previousRun"`
	// PreviousCertificateHash is the agglayer hash of the previous certificate (from its
	// step-wait-result.json) and PreviousCertificateHeight its height.
	PreviousCertificateHash   common.Hash `json:"previousCertificateHash"`
	PreviousCertificateHeight uint64      `json:"previousCertificateHeight"`
	// DeltaCertificateID is the CertificateID of the delta certificate (its agglayer hash is only known
	// once it is submitted, see step-submit-result.json) and DeltaCertificateHeight its height.
	DeltaCertificateID     common.Hash `json:"deltaCertificateId"`
	DeltaCertificateHeight uint64      `json:"deltaCertificateHeight"`
	// RootBefore / RootAfter are the local exit root before and after appending the delta exits.
	RootBefore common.Hash `json:"rootBefore"`
	RootAfter  common.Hash `json:"rootAfter"`
	// FirstDepositCount / LastDepositCount delimit the leaf indexes added by the delta (inclusive).
	FirstDepositCount uint32 `json:"firstDepositCount"`
	LastDepositCount  uint32 `json:"lastDepositCount"`
	// DeltaExitCount is the number of exits added; TotalExitCount the exits in exit-certificate-full.json.
	DeltaExitCount int `json:"deltaExitCount"`
	TotalExitCount int `json:"totalExitCount"`
}

// previousRun is the state of the settled run a delta is chained on.
type previousRun struct {
	Dir string
	// Certificate carries every bridge exit issued so far with the latest new_local_exit_root and height.
	Certificate *agglayertypes.Certificate
	// CertificateFile is the file Certificate was loaded from (full certificate, or the signed one of a
	// non-delta run).
	CertificateFile string
	// LiteDBPath is the previous run's lite syncer DB, holding every leaf of the exit tree so far.
	LiteDBPath string
	WaitResult *StepWaitResult
}

// loadPreviousRun loads the artifacts of a previous run: its (full) certificate, the lite exit-tree DB
// and the proof that it settled. A delta output is a valid previous run for the next delta; so is the
// output of a regular run (its signed certificate already holds every exit).
func loadPreviousRun(dir string) (*previousRun, error) {
	certFile := fileFullCertificate
	if !fileExists(filepath.Join(dir, certFile)) {
		certFile = fileSignedCertificate
	}
	var cj certificateJSON
	if err := loadJSON(dir, certFile, &cj); err != nil {
		return nil, fmt.Errorf("load previous run certificate (%s in %s): %w", certFile, dir, err)
	}
	cert, err := cj.toAgglayerCertificate()
	if err != nil {
		return nil, fmt.Errorf("parse previous run certificate %s: %w", certFile, err)
	}
	if cert.NewLocalExitRoot == (common.Hash{}) {
		return nil, fmt.Errorf("previous run certificate %s has no new_local_exit_root", certFile)
	}

	var wait StepWaitResult
	if err := loadJSON(dir, fileStepWaitResult, &wait); err != nil {
		return nil, fmt.Errorf("load previous run %s (the previous certificate must be submitted and settled): %w",
			fileStepWaitResult, err)
	}
	if !wait.FinalStatus.IsSettled() {
		return nil, fmt.Errorf("previous certificate %s is not settled (status %s)",
			wait.CertificateHash.Hex(), wait.FinalStatus)
	}

	dbPath := filepath.Join(dir, fileStepGLiteDB)
	if !fileExists(dbPath) {
		return nil, fmt.Errorf("previous run local exit tree DB %s not found", dbPath)
	}
	return &previousRun{
		Dir: dir, Certificate: cert, CertificateFile: certFile, LiteDBPath: dbPath, WaitResult: &wait,
	}, nil
}

// LoadDeltaExits reads and validates the delta exits file. Only asset (transfer) exits are accepted,
// with a positive amount and a token_info; message exits are not supported.
func LoadDeltaExits(path string, l2NetworkID uint32) ([]*agglayertypes.BridgeExit, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read delta exits file: %w", err)
	}
	var file DeltaExitsFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parse delta exits file %s: %w", path, err)
	}
	if len(file.BridgeExits) == 0 {
		return nil, fmt.Errorf("delta exits file %s has no bridge_exits", path)
	}
	for i, be := range file.BridgeExits {
		if err := validateDeltaExit(be, l2NetworkID); err != nil {
			return nil, fmt.Errorf("delta exit %d: %w", i, err)
		}
	}
	return file.BridgeExits, nil
}

func validateDeltaExit(be *agglayertypes.BridgeExit, l2NetworkID uint32) error {
	if be == nil {
		return errors.New("is null")
	}
	if err := be.Validate(); err != nil {
		return err
	}
	if be.LeafType != bridgetypes.LeafTypeAsset {
		return fmt.Errorf("leaf_type %s is not supported (only Transfer exits)", be.LeafType)
	}
	if be.Amount == nil || be.Amount.Sign() <= 0 {
		return errors.New("amount must be positive")
	}
	if be.DestinationNetwork == l2NetworkID {
		return fmt.Errorf("dest_network %d is the exiting network itself", be.DestinationNetwork)
	}
	return nil
}

// runDelta runs the delta pipeline (CHECK → F → G2 → H → I → SIGN) and writes the delta certificate,
// the full certificate and the manifest to options.outputDir.
func runDelta(ctx context.Context, cfg *Config) error {
	dir := cfg.Options.OutputDir
	if err := os.MkdirAll(dir, dirPermissions); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	if sameDir(dir, cfg.Options.PreviousRun) {
		return fmt.Errorf("options.outputDir and options.previousRun must be different directories (%s)", dir)
	}

	prev, err := loadPreviousRun(cfg.Options.PreviousRun)
	if err != nil {
		return err
	}
	log.Infof("Previous run: %s (certificate %s: height %d, %d exits, new LER %s)", prev.Dir,
		prev.CertificateFile, prev.Certificate.Height, len(prev.Certificate.BridgeExits),
		prev.Certificate.NewLocalExitRoot.Hex())

	exits, err := LoadDeltaExits(cfg.Options.DeltaExitsFile, cfg.L2NetworkID)
	if err != nil {
		return err
	}
	log.Infof("Delta exits: %d (%s)", len(exits), cfg.Options.DeltaExitsFile)

	checkResult, err := RunStepCheckDelta(ctx, cfg, prev)
	if err != nil {
		return fmt.Errorf("step CHECK: %w", err)
	}
	if err := saveJSON(dir, fileStepCheckResult, checkResult); err != nil {
		return err
	}

	certificate := &agglayertypes.Certificate{NetworkID: cfg.L2NetworkID, BridgeExits: exits}
	certificate, err = runDeltaStepF(ctx, cfg, dir, certificate)
	if err != nil {
		return err
	}

	gResult, err := RunStepG2Delta(ctx, cfg, prev, certificate)
	if err != nil {
		return fmt.Errorf("step G2: %w", err)
	}
	if err := saveJSON(dir, fileStepGNewLocalExitRoot, gResult); err != nil {
		return err
	}
	if err := saveJSON(dir, fileStepGReorderedCertificate, certificate); err != nil {
		return err
	}

	hResult, err := RunStepHDelta(ctx, cfg, prev)
	if err != nil {
		return fmt.Errorf("step H: %w", err)
	}
	if err := saveJSON(dir, fileStepHPreviousLocalExitRoot, hResult); err != nil {
		return err
	}

	if err := RunStepI(ctx, cfg, certificate, gResult, hResult); err != nil {
		return fmt.Errorf("step I: %w", err)
	}
	if err := saveJSON(dir, fileFinalCertificate, certificate); err != nil {
		return err
	}

	submittable := certificate
	if cfg.SignerConfig.Method != "" {
		signed, err := RunStepSign(ctx, cfg, certificate)
		if err != nil {
			return fmt.Errorf("step SIGN: %w", err)
		}
		if err := saveJSON(dir, fileSignedCertificate, signed); err != nil {
			return err
		}
		submittable = signed
	}

	full, manifest := buildFullCertificate(cfg, prev, submittable, gResult)
	if err := saveJSON(dir, fileFullCertificate, full); err != nil {
		return err
	}
	if err := saveJSON(dir, fileDeltaManifest, manifest); err != nil {
		return err
	}

	log.Info("")
	log.Info("╔═══════════════════════════════════════════╗")
	log.Info("║          Delta Pipeline Complete           ║")
	log.Info("╚═══════════════════════════════════════════╝")
	log.Infof("New bridge exits:    %d (leaf indexes %d..%d)", manifest.DeltaExitCount,
		manifest.FirstDepositCount, manifest.LastDepositCount)
	log.Infof("Total bridge exits:  %d", manifest.TotalExitCount)
	log.Infof("Local exit root:     %s → %s", manifest.RootBefore.Hex(), manifest.RootAfter.Hex())
	log.Infof("Output directory:    %s", dir)
	return nil
}

// runDeltaSteps runs the delta pipeline for --step all, or the post-generation steps (sign, submit,
// wait) that work off the delta certificate in the output dir. The L2 scan steps do not exist in delta
// mode.
func runDeltaSteps(ctx context.Context, cfg *Config, step string) error {
	if step == "" || step == "all" {
		return runDelta(ctx, cfg)
	}
	steps, err := parseStepList(step)
	if err != nil {
		return err
	}
	for _, s := range steps {
		switch s {
		case lastAutoStep, "submit", "wait": // lastAutoStep is "sign"
		default:
			return fmt.Errorf("step %q is not available with options.deltaMode (only all, sign, submit, wait)", s)
		}
	}
	for _, s := range steps {
		if err := runSingleStep(ctx, s, cfg); err != nil {
			return err
		}
	}
	return nil
}

func sameDir(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return absA == absB
}

// buildFullCertificate assembles the full certificate (every exit: previous runs + delta, with the
// final new_local_exit_root) and the manifest. The delta certificate's exits already carry their
// metadata hash, the same form the previous exits have.
func buildFullCertificate(
	cfg *Config, prev *previousRun, delta *agglayertypes.Certificate, gResult *StepGResult,
) (*agglayertypes.Certificate, *DeltaManifest) {
	all := make([]*agglayertypes.BridgeExit, 0, len(prev.Certificate.BridgeExits)+len(delta.BridgeExits))
	all = append(all, prev.Certificate.BridgeExits...)
	all = append(all, delta.BridgeExits...)

	full := &agglayertypes.Certificate{
		NetworkID:           delta.NetworkID,
		Height:              delta.Height,
		PrevLocalExitRoot:   delta.PrevLocalExitRoot,
		NewLocalExitRoot:    delta.NewLocalExitRoot,
		BridgeExits:         all,
		L1InfoTreeLeafCount: delta.L1InfoTreeLeafCount,
	}
	manifest := &DeltaManifest{
		NetworkID:                 delta.NetworkID,
		PreviousRun:               cfg.Options.PreviousRun,
		PreviousCertificateHash:   prev.WaitResult.CertificateHash,
		PreviousCertificateHeight: prev.Certificate.Height,
		DeltaCertificateID:        delta.CertificateID(),
		DeltaCertificateHeight:    delta.Height,
		RootBefore:                prev.Certificate.NewLocalExitRoot,
		RootAfter:                 delta.NewLocalExitRoot,
		FirstDepositCount:         gResult.FirstDepositCount,
		LastDepositCount:          gResult.FirstDepositCount + uint32(len(delta.BridgeExits)) - 1,
		DeltaExitCount:            len(delta.BridgeExits),
		TotalExitCount:            len(all),
	}
	return full, manifest
}

// --- Step F (delta) ---

// runDeltaStepF runs the delta Step F, persists its outputs and returns the certificate to continue
// with (the capped one when exits had to be trimmed).
func runDeltaStepF(
	ctx context.Context, cfg *Config, dir string, certificate *agglayertypes.Certificate,
) (*agglayertypes.Certificate, error) {
	result, err := RunStepFDelta(ctx, cfg, certificate)
	if err != nil {
		return nil, fmt.Errorf("step F: %w", err)
	}
	if result.TokenBalances != nil {
		if err := saveJSON(dir, fileStepFTokenBalances, result.TokenBalances); err != nil {
			return nil, err
		}
	}
	if err := saveJSON(dir, fileStepFChecks, result.Checks); err != nil {
		return nil, err
	}
	if result.CappedCertificate != nil {
		if err := saveJSON(dir, fileStepFCappedCertificate, result.CappedCertificate); err != nil {
			return nil, err
		}
		return result.CappedCertificate, nil
	}
	return certificate, nil
}

// RunStepFDelta verifies that, per token, the sum of the delta exits does not exceed the balance the
// agglayer still holds for it (admin_getTokenBalance, which already reflects the previous
// certificates). Unlike the full pipeline there is no LBT equality to check: the delta only recovers
// part of what is left. options.ignoreBalanceMismatch and options.capMode keep their meaning: on an
// excess the step fails, or — with ignoreBalanceMismatch and a trimming capMode — caps the exits to
// the remaining balance.
func RunStepFDelta(
	ctx context.Context, cfg *Config, certificate *agglayertypes.Certificate,
) (*StepFResult, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP F (delta) — Agglayer remaining balance check")
	log.Info("═══════════════════════════════════════════")

	if !cfg.Options.UseAgglayerAdminToStepFCheck {
		log.Warn("STEP F skipped: useAgglayerAdminToStepFCheck=false — the delta exits are NOT checked " +
			"against the agglayer remaining balance")
		return &StepFResult{AllMatch: true}, nil
	}
	if cfg.Options.AgglayerAdminURL == "" {
		return nil, fmt.Errorf("step F requires agglayerAdminURL to be set in options")
	}

	raw, err := queryAgglayerTokenBalance(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if cfg.Options.OutputDir != "" {
		if err := saveJSON(cfg.Options.OutputDir, fileStepFAgglayerLBT, raw); err != nil {
			return nil, err
		}
	}
	var resp agglayerBalanceResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parse admin_getTokenBalance response: %w", err)
	}

	checks, err := compareDeltaBalances(groupBridgeExitsByToken(certificate), resp.Balances)
	if err != nil {
		return nil, err
	}
	allMatch := true
	for _, c := range checks {
		if c.Match {
			log.Infof("✅ (network=%d addr=%s): delta=%s ≤ agglayer remaining=%s",
				c.OriginNetwork, c.OriginTokenAddress, c.CertificateAmount, c.AgglayerAmount)
			continue
		}
		allMatch = false
		log.Warnf("❌ EXCEEDS REMAINING BALANCE (network=%d addr=%s): delta=%s  "+
			"agglayer remaining=%s  (delta−agglayer=%s)",
			c.OriginNetwork, c.OriginTokenAddress, c.CertificateAmount, c.AgglayerAmount,
			amountDiff(c.CertificateAmount, c.AgglayerAmount))
	}
	result := &StepFResult{AllMatch: allMatch, TokenBalances: raw, Checks: checks}
	if allMatch {
		log.Infof("All %d token deltas are within the agglayer remaining balance ✅", len(checks))
		log.Info("STEP F (delta) complete")
		return result, nil
	}

	if !cfg.Options.IgnoreBalanceMismatch {
		return result, fmt.Errorf("delta exits exceed the agglayer remaining balance " +
			"(set options.ignoreBalanceMismatch=true with a trimming options.capMode to cap them)")
	}
	log.Warn("Delta exceeds the remaining balance — capping (ignoreBalanceMismatch=true)")
	cappedExits, err := capCertificateExits(certificate.BridgeExits, checks, cfg.Options.CapMode)
	if err != nil {
		return result, fmt.Errorf("delta exceeds the remaining balance and requires capping: %w", err)
	}
	capped := *certificate
	capped.BridgeExits = cappedExits
	result.CappedCertificate = &capped
	log.Infof("🔧 Capped delta certificate: %d → %d bridge exits", len(certificate.BridgeExits), len(cappedExits))
	log.Info("STEP F (delta) complete")
	return result, nil
}

// compareDeltaBalances builds the per-token check for the delta: Match when the exits' sum is within
// the agglayer's remaining balance (a token the agglayer holds nothing of has balance 0). The
// remaining balance is also the cap budget (RemainingBalance).
func compareDeltaBalances(
	groups map[tokenKey][]*agglayertypes.BridgeExit, agglayerEntries []agglayerTokenEntry,
) ([]TokenBalanceCheck, error) {
	balances := make(map[tokenKey]*big.Int, len(agglayerEntries))
	for _, e := range agglayerEntries {
		amount, ok := new(big.Int).SetString(e.Amount, decimalBase)
		if !ok {
			return nil, fmt.Errorf("parse agglayer amount %q for token (network=%d addr=%s)",
				e.Amount, e.OriginNetwork, e.OriginTokenAddress.Hex())
		}
		balances[tokenKey{e.OriginNetwork, e.OriginTokenAddress}] = amount
	}

	checks := make([]TokenBalanceCheck, 0, len(groups))
	for k, exits := range groups {
		sum := new(big.Int)
		for _, e := range exits {
			sum.Add(sum, e.Amount)
		}
		balance := balances[k]
		if balance == nil {
			balance = new(big.Int)
		}
		check := TokenBalanceCheck{
			OriginNetwork:      k.OriginNetwork,
			OriginTokenAddress: k.OriginTokenAddress.Hex(),
			CertificateAmount:  sum.String(),
			AgglayerAmount:     balance.String(),
			Match:              sum.Cmp(balance) <= 0,
			RemainingBalance:   new(big.Int).Set(balance),
		}
		if !check.Match {
			check.CertificateEntries = make([]CertificateEntry, len(exits))
			for i, e := range exits {
				check.CertificateEntries[i] = CertificateEntry{
					DestinationNetwork: e.DestinationNetwork,
					DestinationAddress: e.DestinationAddress.Hex(),
					Amount:             e.Amount.String(),
				}
			}
		}
		checks = append(checks, check)
	}
	sortTokenBalanceChecks(checks)
	return checks, nil
}

// --- Step G2 (delta) ---

// RunStepG2Delta computes the delta's NewLocalExitRoot by continuing the previous run's exit tree:
// it copies the previous lite DB, appends the certificate's exits as leaves continuing the deposit
// counts, and returns the resulting root. The certificate's exits get their Metadata set to the
// keccak256 of their raw leaf metadata, as the full pipeline's Step G2 does.
//
// The leaf encoding is the same code path as the full pipeline's off-chain mode
// (liteLeavesFromExits). There is no shadow-fork verification: the L2 bridge never saw the previous
// exits, so its getRoot() cannot anchor the tree; the previous tree's root is instead checked against
// the previous certificate's new_local_exit_root before appending.
func RunStepG2Delta(
	ctx context.Context, cfg *Config, prev *previousRun, certificate *agglayertypes.Certificate,
) (*StepGResult, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP G2 (delta) - Calculate NewLocalExitRoot")
	log.Info("═══════════════════════════════════════════")

	if certificate == nil || len(certificate.BridgeExits) == 0 {
		return nil, fmt.Errorf("the delta certificate has no bridge exits")
	}

	rawMetadata, err := deltaRawMetadata(ctx, cfg, certificate.BridgeExits)
	if err != nil {
		return nil, err
	}
	if err := rejectDuplicateExits(prev.Certificate.BridgeExits, certificate.BridgeExits, rawMetadata); err != nil {
		return nil, err
	}
	gasTokenNetwork, gasTokenAddress, err := fetchL2GasTokenInfo(ctx, cfg)
	if err != nil {
		return nil, err
	}

	dbPath := g2LiteDBPath(cfg)
	if err := copyLiteDB(prev.LiteDBPath, dbPath); err != nil {
		return nil, fmt.Errorf("copy previous run lite syncer DB: %w", err)
	}
	log.Infof("Copied previous lite syncer DB %s → %s", prev.LiteDBPath, dbPath)

	syncer, err := bridgesyncerlite.New(ctx, bridgesyncerlite.Config{DBPath: dbPath},
		log.WithFields("module", "exit-cert-bridgesyncerlite"))
	if err != nil {
		return nil, fmt.Errorf("open lite syncer DB: %w", err)
	}
	defer func() {
		if cerr := syncer.Close(); cerr != nil {
			log.Warnf("error closing lite bridge syncer: %v", cerr)
		}
	}()

	rootBefore, err := syncer.LocalExitRoot()
	if err != nil {
		return nil, err
	}
	if rootBefore != prev.Certificate.NewLocalExitRoot {
		return nil, fmt.Errorf("previous run is inconsistent: its local exit tree root %s differs from the "+
			"%s new_local_exit_root %s", rootBefore.Hex(), prev.CertificateFile,
			prev.Certificate.NewLocalExitRoot.Hex())
	}
	firstDepositCount, err := syncer.NextDepositCount(ctx)
	if err != nil {
		return nil, err
	}
	blockNum, err := syncer.NextBlockNum(ctx)
	if err != nil {
		return nil, err
	}

	leaves := liteLeavesFromExits(certificate.BridgeExits, blockNum, firstDepositCount,
		gasTokenNetwork, gasTokenAddress, rawMetadata)
	newLER, err := syncer.AppendBridges(ctx, leaves)
	if err != nil {
		return nil, fmt.Errorf("append delta leaves to the exit tree: %w", err)
	}

	for i, be := range certificate.BridgeExits {
		be.Metadata = crypto.Keccak256(rawMetadata[i])
	}

	result := &StepGResult{
		InitialLocalExitRoot: rootBefore,
		NewLocalExitRoot:     newLER,
		BridgeExitCount:      uint64(len(certificate.BridgeExits)),
		BridgeExitMetadata:   rawMetadata,
		FirstDepositCount:    firstDepositCount,
	}
	log.Infof("Delta bridge exits appended: %d (leaf indexes %d..%d)", result.BridgeExitCount,
		firstDepositCount, firstDepositCount+uint32(len(leaves))-1)
	log.Infof("Local exit root: %s → %s", rootBefore.Hex(), newLER.Hex())
	log.Info("STEP G2 (delta) complete")
	return result, nil
}

// rejectDuplicateExits fails when a delta exit has the same leaf hash (token, destination, amount and
// metadata) as a previous exit or as another delta exit. Both would be distinct tree leaves with the
// same hash, and the claimer indexes leaves by hash: the later one would overwrite the earlier
// deposit count, so the earlier exit could no longer be claimed. Merge such exits (sum the amounts)
// instead.
func rejectDuplicateExits(
	previous, delta []*agglayertypes.BridgeExit, rawMetadata [][]byte,
) error {
	seen := make(map[common.Hash]string, len(previous)+len(delta))
	for i, be := range previous {
		if h := be.Hash(); h != (common.Hash{}) {
			seen[h] = fmt.Sprintf("previous exit %d", i)
		}
	}
	for i, be := range delta {
		hashed := *be
		hashed.Metadata = crypto.Keccak256(rawMetadata[i])
		h := hashed.Hash()
		if h == (common.Hash{}) {
			continue
		}
		if other, dup := seen[h]; dup {
			return fmt.Errorf("delta exit %d (token %s, dest %s, amount %s) duplicates %s: identical exits "+
				"share a leaf hash and the claimer could not tell them apart — merge them into one exit",
				i, be.TokenInfo, be.DestinationAddress.Hex(), be.Amount, other)
		}
		seen[h] = fmt.Sprintf("delta exit %d", i)
	}
	return nil
}

// deltaRawMetadata returns each exit's raw leaf metadata, aligned by index: the metadata the delta file
// carries, or — for the exits without it — the one the L2 bridge would embed (generateMetadata).
func deltaRawMetadata(
	ctx context.Context, cfg *Config, exits []*agglayertypes.BridgeExit,
) ([][]byte, error) {
	raw := make([][]byte, len(exits))
	var missing []int
	missingExits := make([]*agglayertypes.BridgeExit, 0, len(exits))
	for i, be := range exits {
		if len(be.Metadata) > 0 {
			raw[i] = be.Metadata
			continue
		}
		missing = append(missing, i)
		missingExits = append(missingExits, be)
	}
	if len(missing) == 0 {
		return raw, nil
	}

	log.Infof("Generating metadata from the L2 bridge for %d of %d delta exits", len(missing), len(exits))
	backend := &anvilForkBackend{url: cfg.L2RPCURL, bridgeAddr: cfg.L2BridgeAddress}
	generated, err := generateMetadata(ctx, backend, cfg,
		&agglayertypes.Certificate{BridgeExits: missingExits}, nil)
	if err != nil {
		return nil, fmt.Errorf("generate metadata for delta exits without it: %w", err)
	}
	for j, i := range missing {
		raw[i] = generated[j]
	}
	return raw, nil
}

// --- Step H (delta) ---

// RunStepHDelta chains the delta certificate on the previous one: PreviousLocalExitRoot is the previous
// certificate's new_local_exit_root and Height the previous height + 1. It cross-checks both against
// the agglayer (a pending certificate, a different settled LER or a different settled height abort).
func RunStepHDelta(ctx context.Context, cfg *Config, prev *previousRun) (*StepHResult, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP H (delta) - Chain on the previous certificate")
	log.Info("═══════════════════════════════════════════")

	agglayerClientCfg := cfg.Options.AgglayerClient
	if agglayerClientCfg.GRPC == nil || agglayerClientCfg.GRPC.URL == "" {
		return nil, fmt.Errorf("agglayerClient.grpc.url is required for step H")
	}
	client, err := agglayer.NewAgglayerClient(agglayerClientCfg, log.GetDefaultLogger())
	if err != nil {
		return nil, fmt.Errorf("create agglayer client: %w", err)
	}
	return runStepHDelta(ctx, cfg, client, prev)
}

// runStepHDelta is the client-injectable core of RunStepHDelta.
func runStepHDelta(
	ctx context.Context, cfg *Config, client agglayer.AgglayerClientInterface, prev *previousRun,
) (*StepHResult, error) {
	settledLER, nextHeight, err := fetchSettledNetworkState(ctx, cfg, client)
	if err != nil {
		return nil, err
	}
	if err := verifyChainsOnPreviousRun(prev, settledLER, nextHeight); err != nil {
		if err := suppressUnsettledExitsError(cfg, err); err != nil {
			return nil, err
		}
	} else {
		log.Info("✅ agglayer settled LER and height match the previous certificate")
	}

	result := &StepHResult{
		PreviousLocalExitRoot: prev.Certificate.NewLocalExitRoot,
		Height:                prev.Certificate.Height + 1,
	}
	log.Infof("PreviousLocalExitRoot (previous certificate): %s", result.PreviousLocalExitRoot.Hex())
	log.Infof("Certificate height: %d", result.Height)
	log.Info("STEP H (delta) complete")
	return result, nil
}

// verifyChainsOnPreviousRun is the delta's AET-11: the agglayer's settled LER must be the previous
// certificate's new_local_exit_root (not the L2 bridge's getRoot(), which never saw those exits) and
// the next height must follow the previous certificate's. A mismatch wraps errUnsettledBridgeExits.
func verifyChainsOnPreviousRun(prev *previousRun, settledLER common.Hash, nextHeight uint64) error {
	if settledLER != prev.Certificate.NewLocalExitRoot {
		return fmt.Errorf("%w: the agglayer settled LER %s is not the previous certificate's "+
			"new_local_exit_root %s (previous certificate %s, height %d): it is not the latest settled "+
			"certificate, or it is not settled yet", errUnsettledBridgeExits, settledLER.Hex(),
			prev.Certificate.NewLocalExitRoot.Hex(), prev.WaitResult.CertificateHash.Hex(), prev.Certificate.Height)
	}
	if nextHeight != prev.Certificate.Height+1 {
		return fmt.Errorf("%w: the agglayer next certificate height is %d but the previous certificate "+
			"has height %d", errUnsettledBridgeExits, nextHeight, prev.Certificate.Height)
	}
	return nil
}

// checkDeltaUnsettledExits is Step CHECK's check 9 in delta mode: no pending certificate, and the
// agglayer's settled LER / height are those of the previous certificate (verifyChainsOnPreviousRun).
func checkDeltaUnsettledExits(
	ctx context.Context, cfg *Config, prev *previousRun, result *StepCheckResult, failures *[]string,
) {
	agglayerClientCfg := cfg.Options.AgglayerClient
	if agglayerClientCfg.GRPC == nil || agglayerClientCfg.GRPC.URL == "" {
		result.UnsettledExitsStatus = uncheckedStatus
		reportUnsettledExitsFailure(cfg, failures,
			"agglayerClient.grpc.url is required (needed to verify the previous certificate is settled, "+
				"and later by step H)")
		return
	}
	client, err := agglayer.NewAgglayerClient(agglayerClientCfg, log.GetDefaultLogger())
	if err != nil {
		result.UnsettledExitsStatus = errorStatus
		reportUnsettledExitsFailure(cfg, failures, fmt.Sprintf("create agglayer client: %v", err))
		return
	}
	checkDeltaUnsettledExitsWith(ctx, cfg, client, prev, result, failures)
}

// checkDeltaUnsettledExitsWith is the client-injectable core of checkDeltaUnsettledExits.
func checkDeltaUnsettledExitsWith(
	ctx context.Context, cfg *Config, client agglayer.AgglayerClientInterface, prev *previousRun,
	result *StepCheckResult, failures *[]string,
) {
	settledLER, nextHeight, err := fetchSettledNetworkState(ctx, cfg, client)
	if err != nil {
		result.UnsettledExitsStatus = errorStatus
		reportUnsettledExitsFailure(cfg, failures, err.Error())
		return
	}
	result.SettledLER = settledLER.Hex()
	if err := verifyChainsOnPreviousRun(prev, settledLER, nextHeight); err != nil {
		result.UnsettledExitsStatus = "previous certificate is not the latest settled"
		reportUnsettledExitsFailure(cfg, failures, err.Error())
		return
	}
	result.UnsettledExitsStatus = okStatus
	log.Infof("✅ agglayer settled LER %s is the previous certificate's new_local_exit_root", settledLER.Hex())
}
