package exit_certificate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/tools/exit_certificate/bridgesyncerlite"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

// Anti-drain guards of the version-1 recoveries (recoveries.go, recoveries_v1.go).
//
// A version-1 case is verified by the kit on a fork that replays every exit certified so far and then runs
// the case's move.sh, so its outputs carry the deposit count each BridgeEvent got there and its
// origin_SC_funds names the contracts that paid for them. The step checks both against our own state:
//
//   - per-contract budget: what the cases take from a contract (per token, origin_SC_funds) can never exceed
//     its balance at the frozen L2 snapshot (the first run's target block — the L2 is halted), minus what
//     earlier deltas already recovered from it (recoveries-sc-funds.json, carried delta to delta);
//   - deposit counts: each output's deposit_count must be the deposit count its exit gets in this delta (the
//     previous run's exit tree size + its position in delta-exits.json). A case built for an older tree — a
//     stale or replayed case — carries deposit counts that are already taken and is refused. Step G2 of the
//     delta checks again that the leaves it appends are, at those deposit counts, the recorded exits.

const fileRecoveriesSCFunds = "recoveries-sc-funds.json"

// RecoveredSCFunds is the cumulative amount recovered from one (contract, token) of the version-1
// origin_SC_funds, with the contract's balance at the L2 snapshot block it is bounded by.
type RecoveredSCFunds struct {
	Contract           common.Address `json:"contract"`
	OriginNetwork      uint32         `json:"originNetwork"`
	OriginTokenAddress common.Address `json:"originTokenAddress"`
	// L2Token is the token the balance was read from (zero address = native ETH, read with eth_getBalance).
	L2Token         common.Address `json:"l2Token"`
	SnapshotBalance string         `json:"snapshotBalance"`
	Recovered       string         `json:"recovered"`
}

// recoveriesEnv is what applyRecoveries needs besides the state and the cases.
type recoveriesEnv struct {
	// L2NetworkID is the exiting network: a version-1 Message comes from it; a Transfer cannot bridge a token
	// native to it.
	L2NetworkID uint32
	// NextDepositCount is the deposit count the first exit of this delta gets: the size of the previous run's
	// exit tree. Only used with version-1 cases.
	NextDepositCount uint32
	// SCBalance returns a contract's balance of an L2 token (zero = native ETH) at the L2 snapshot block. Only
	// used with version-1 cases.
	SCBalance func(contract, l2Token common.Address) (*big.Int, error)
}

// recoverySCBudgetRow is one row of the per-contract summary of a run.
type recoverySCBudgetRow struct {
	Contract                         common.Address
	Token                            tokenKey
	Snapshot, Before, Recovered, Aft *big.Int
}

type scFundsKey struct {
	Contract common.Address
	Token    tokenKey
}

// checkSCBudgets checks, per (contract, token) of the origin_SC_funds of the new version-1 cases, that the
// contract's balance at the L2 snapshot covers what earlier deltas recovered from it plus what this batch
// takes, and returns the updated cumulative state and the summary rows. It changes nothing.
func checkSCBudgets(
	st *recoveriesState, idx *recoveryIndex, env *recoveriesEnv, cases []recoveryCase,
) ([]RecoveredSCFunds, []recoverySCBudgetRow, []error) {
	batch := map[scFundsKey]*big.Int{}
	var order []scFundsKey
	for _, c := range cases {
		for _, f := range c.OriginSCFunds {
			for _, t := range f.Tokens {
				k := scFundsKey{f.Contract, t.Token}
				if batch[k] == nil {
					batch[k] = new(big.Int)
					order = append(order, k)
				}
				batch[k].Add(batch[k], t.Amount)
			}
		}
	}
	if len(order) == 0 {
		return st.SCRecovered, nil, nil
	}
	if env.SCBalance == nil {
		return nil, nil, []error{errors.New("version-1 recoveries need the contracts' balances at the L2 " +
			"snapshot block, and none can be read")}
	}
	sort.Slice(order, func(i, j int) bool { return scFundsKeyLess(order[i], order[j]) })

	updated := append([]RecoveredSCFunds(nil), st.SCRecovered...)
	pos := make(map[scFundsKey]int, len(updated))
	for i, r := range updated {
		pos[scFundsKey{r.Contract, tokenKey{r.OriginNetwork, r.OriginTokenAddress}}] = i
	}
	var errs []error
	var rows []recoverySCBudgetRow
	for _, k := range order {
		l2Token, ok := idx.byOrigin[k.Token]
		if !ok {
			errs = append(errs, fmt.Errorf("origin_SC_funds: contract %s, token %d/%s is not in the LBT "+
				"(step-0-lbt.json)", k.Contract.Hex(), k.Token.OriginNetwork, k.Token.OriginTokenAddress.Hex()))
			continue
		}
		snapshot, err := env.SCBalance(k.Contract, l2Token)
		if err != nil {
			errs = append(errs, fmt.Errorf("origin_SC_funds: balance of contract %s in token %d/%s at the L2 "+
				"snapshot: %w", k.Contract.Hex(), k.Token.OriginNetwork, k.Token.OriginTokenAddress.Hex(), err))
			continue
		}
		before := new(big.Int)
		i, seen := pos[k]
		if seen {
			if before, err = parseBigOrZero(updated[i].Recovered); err != nil {
				errs = append(errs, fmt.Errorf("%s: contract %s recovered: %w", fileRecoveriesSCFunds,
					k.Contract.Hex(), err))
				continue
			}
		}
		after := new(big.Int).Add(before, batch[k])
		if after.Cmp(snapshot) > 0 {
			errs = append(errs, fmt.Errorf("origin_SC_funds: contract %s held %s of token %d/%s at the L2 snapshot, "+
				"%s were already recovered from it and this batch takes %s: short by %s", k.Contract.Hex(), snapshot,
				k.Token.OriginNetwork, k.Token.OriginTokenAddress.Hex(), before, batch[k],
				new(big.Int).Sub(after, snapshot)))
			continue
		}
		rec := RecoveredSCFunds{
			Contract: k.Contract, OriginNetwork: k.Token.OriginNetwork, OriginTokenAddress: k.Token.OriginTokenAddress,
			L2Token: l2Token, SnapshotBalance: snapshot.String(), Recovered: after.String(),
		}
		if seen {
			updated[i] = rec
		} else {
			pos[k] = len(updated)
			updated = append(updated, rec)
		}
		rows = append(rows, recoverySCBudgetRow{
			Contract: k.Contract, Token: k.Token, Snapshot: snapshot, Before: before, Recovered: batch[k],
			Aft: new(big.Int).Sub(snapshot, after),
		})
	}
	return updated, rows, errs
}

func scFundsKeyLess(a, b scFundsKey) bool {
	if c := strings.Compare(a.Contract.Hex(), b.Contract.Hex()); c != 0 {
		return c < 0
	}
	if a.Token.OriginNetwork != b.Token.OriginNetwork {
		return a.Token.OriginNetwork < b.Token.OriginNetwork
	}
	return a.Token.OriginTokenAddress.Hex() < b.Token.OriginTokenAddress.Hex()
}

// checkRecoveryDepositCounts requires every version-1 entry's deposit_count to be the deposit count its exit
// gets in this delta: next + its position in the buildRecoveryExits order (the merged legacy exits first, then
// the version-1 outputs in case and output order).
//
// It assumes every case passed its checks: each version-1 output then has exactly one entry, in order, so an
// entry's position within its case is its outputs[] index.
func checkRecoveryDepositCounts(applied []RecoveryLedgerEntry, next uint32) error {
	type legacyKey struct {
		Dest  common.Address
		Token tokenKey
	}
	legacy := map[legacyKey]bool{}
	for _, a := range applied {
		if a.Version == recoveryVersionLegacy {
			legacy[legacyKey{a.Destination, tokenKey{a.OriginNetwork, a.OriginTokenAddress}}] = true
		}
	}
	var errs []error
	position := uint64(len(legacy))
	outputIdx := map[string]int{}
	for _, a := range applied {
		if a.Version == recoveryVersionLegacy {
			continue
		}
		i := outputIdx[a.CaseHash]
		outputIdx[a.CaseHash] = i + 1
		want := uint64(next) + position
		position++
		if a.DepositCount == nil {
			errs = append(errs, fmt.Errorf("%s: outputs[%d]: no deposit_count", a.Source, i))
			continue
		}
		if uint64(*a.DepositCount) != want {
			errs = append(errs, fmt.Errorf("%s: outputs[%d]: deposit_count %d, but this exit gets deposit count %d "+
				"in this delta (the previous run's exit tree has %d leaves, and it is exit %d of the delta): the case "+
				"was not built on the current exit tree (stale or replayed) or the cases are not in the order the "+
				"kit simulated them", a.Source, i, *a.DepositCount, want, next, want-uint64(next)))
		}
	}
	return errors.Join(errs...)
}

// --- environment of a --step recoveries run ---

// newRecoveriesEnv builds the environment of a recoveries run. The deposit count and the balance reader are
// only set up when the batch has a version-1 case: legacy-only batches need neither the previous exit tree nor
// the L2 RPC.
func newRecoveriesEnv(
	ctx context.Context, cfg *Config, st *recoveriesState, cases []recoveryCase,
) (*recoveriesEnv, error) {
	env := &recoveriesEnv{L2NetworkID: cfg.L2NetworkID}
	applied := map[string]bool{}
	for _, l := range st.Ledger {
		applied[l.CaseHash] = true
	}
	needV1 := false
	for _, c := range cases {
		if c.version() == recoveryVersionV1 && !applied[c.CaseHash] {
			needV1 = true
			break
		}
	}
	if !needV1 {
		return env, nil
	}
	if st.TargetBlock == nil {
		return nil, fmt.Errorf("version-1 recoveries read the contracts' balances at the L2 snapshot block, but the "+
			"recoveries state has no %s: copy the one of the first (non-delta) run next to the state files",
			fileStep0TargetBlock)
	}
	if cfg.L2RPCURL == "" {
		return nil, errors.New("version-1 recoveries need l2RpcUrl to read the contracts' balances at the L2 " +
			"snapshot block")
	}
	next, err := previousRunNextDepositCount(ctx, filepath.Join(cfg.Options.PreviousRun, fileStepGLiteDB))
	if err != nil {
		return nil, err
	}
	env.NextDepositCount = next
	block := *st.TargetBlock
	url := cfg.L2RPCURL
	env.SCBalance = func(contract, l2Token common.Address) (*big.Int, error) {
		return l2BalanceAt(ctx, url, contract, l2Token, block)
	}
	log.Infof("Recoveries: version-1 checks against the exit tree of %s (next deposit count %d) and the L2 "+
		"balances at block %d", cfg.Options.PreviousRun, next, block)
	return env, nil
}

// previousRunNextDepositCount returns the size of the previous run's exit tree. It reads a temporary copy of
// the lite DB: the previous run is never modified (opening SQLite in place would leave side files there).
func previousRunNextDepositCount(ctx context.Context, dbPath string) (uint32, error) {
	if !fileExists(dbPath) {
		return 0, fmt.Errorf("previous run local exit tree DB %s not found: version-1 recoveries check their "+
			"deposit counts against it", dbPath)
	}
	tmp, err := os.MkdirTemp("", "exit-cert-recoveries-")
	if err != nil {
		return 0, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)
	copyPath := filepath.Join(tmp, filepath.Base(dbPath))
	if err := copyLiteDB(dbPath, copyPath); err != nil {
		return 0, fmt.Errorf("copy previous run lite syncer DB: %w", err)
	}
	syncer, err := bridgesyncerlite.New(ctx, bridgesyncerlite.Config{DBPath: copyPath},
		log.WithFields("module", "exit-cert-bridgesyncerlite"))
	if err != nil {
		return 0, fmt.Errorf("open previous run lite syncer DB: %w", err)
	}
	defer func() {
		if cerr := syncer.Close(); cerr != nil {
			log.Warnf("error closing lite bridge syncer: %v", cerr)
		}
	}()
	next, err := syncer.NextDepositCount(ctx)
	if err != nil {
		return 0, fmt.Errorf("read the previous run's next deposit count: %w", err)
	}
	return next, nil
}

// l2BalanceAt reads a contract's balance at an L2 block: eth_getBalance for native ETH (zero l2Token),
// balanceOf(contract) on the token otherwise.
func l2BalanceAt(
	ctx context.Context, url string, contract, l2Token common.Address, block uint64,
) (*big.Int, error) {
	tag := toBlockTag(block)
	var raw json.RawMessage
	var err error
	if l2Token == (common.Address{}) {
		raw, err = singleRPC(ctx, url, "eth_getBalance", []any{contract.Hex(), tag}, defaultRetries)
	} else {
		raw, err = singleRPC(ctx, url, "eth_call", []any{
			map[string]string{"to": l2Token.Hex(), "data": encodeBalanceOf(contract)}, tag,
		}, defaultRetries)
	}
	if err != nil {
		return nil, err
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("unexpected balance result %s: %w", string(raw), err)
	}
	b, err := hexutil.DecodeBig(normalizeHexQuantity(s))
	if err != nil {
		return nil, fmt.Errorf("unexpected balance result %q: %w", s, err)
	}
	return b, nil
}

// normalizeHexQuantity turns an eth_call word (0x + 64 hex, leading zeros) into a hex quantity.
func normalizeHexQuantity(s string) string {
	t := strings.TrimLeft(strings.TrimPrefix(s, "0x"), "0")
	if t == "" {
		t = "0"
	}
	return "0x" + t
}

// --- Step G2 (delta) re-check ---

// verifyRecoveryDepositCounts checks, when this delta's exits come from --step recoveries, that the leaves
// Step G2 appends are, at the deposit counts the recoveries step recorded for its version-1 exits, exactly
// those exits — so a delta exits file reordered or edited after --step recoveries cannot drift from the
// ledger. The ledger is the one next to options.deltaExitsFile, else <outputDir>/recoveries; its entries of
// this delta are those whose case is not in the previous run's ledger. Without a ledger there is nothing to
// check (a delta exits file written by hand).
func verifyRecoveryDepositCounts(
	cfg *Config, prev *previousRun, first uint32, leaves []bridgesyncerlite.BridgeLeaf,
) error {
	var ledgerDir string
	for _, d := range []string{filepath.Dir(cfg.Options.DeltaExitsFile),
		filepath.Join(cfg.Options.OutputDir, dirRecoveries)} {
		if d != "" && fileExists(filepath.Join(d, fileRecoveriesLedger)) {
			ledgerDir = d
			break
		}
	}
	if ledgerDir == "" {
		log.Infof("No %s next to the delta exits file or in %s: deposit counts of recoveries not cross-checked",
			fileRecoveriesLedger, filepath.Join(cfg.Options.OutputDir, dirRecoveries))
		return nil
	}
	var ledger, previous []RecoveryLedgerEntry
	if err := loadJSON(ledgerDir, fileRecoveriesLedger, &ledger); err != nil {
		return err
	}
	if prev != nil {
		if err := loadOptionalJSON(filepath.Join(prev.Dir, dirRecoveries), fileRecoveriesLedger, &previous); err != nil {
			return err
		}
	}
	old := map[string]bool{}
	for _, l := range previous {
		old[l.CaseHash] = true
	}
	var errs []error
	for _, l := range ledger {
		if l.DepositCount == nil || old[l.CaseHash] {
			continue
		}
		dc := *l.DepositCount
		if dc < first || uint64(dc-first) >= uint64(len(leaves)) {
			errs = append(errs, fmt.Errorf("%s: the recoveries ledger (%s) records deposit count %d for an exit of "+
				"this delta, but the delta appends deposit counts %d..%d", l.Source, ledgerDir, dc, first,
				uint64(first)+uint64(len(leaves))-1))
			continue
		}
		if err := ledgerEntryMatchesLeaf(l, leaves[dc-first]); err != nil {
			errs = append(errs, fmt.Errorf("%s: deposit count %d: %w (the delta exits file no longer matches the "+
				"recoveries ledger in %s)", l.Source, dc, err, ledgerDir))
		}
	}
	return errors.Join(errs...)
}

// ledgerEntryMatchesLeaf compares a version-1 ledger entry with the leaf appended at its deposit count.
func ledgerEntryMatchesLeaf(l RecoveryLedgerEntry, leaf bridgesyncerlite.BridgeLeaf) error {
	leafType := uint8(0)
	if l.isMessage() {
		leafType = 1
	}
	amount, ok := new(big.Int).SetString(l.Amount, decimalBase)
	if !ok {
		return fmt.Errorf("amount %q is not a base-10 integer", l.Amount)
	}
	metadata, err := ledgerMetadata(l)
	if err != nil {
		return err
	}
	switch {
	case leaf.LeafType != leafType:
		return fmt.Errorf("the leaf has leaf type %d, the ledger %s", leaf.LeafType, l.LeafType)
	case leaf.OriginNetwork != l.OriginNetwork || leaf.OriginAddress != l.OriginTokenAddress:
		return fmt.Errorf("the leaf has origin %d/%s, the ledger %d/%s", leaf.OriginNetwork,
			leaf.OriginAddress.Hex(), l.OriginNetwork, l.OriginTokenAddress.Hex())
	case leaf.DestinationNetwork != l.DestinationNetwork || leaf.DestinationAddress != l.Destination:
		return fmt.Errorf("the leaf goes to %d/%s, the ledger to %d/%s", leaf.DestinationNetwork,
			leaf.DestinationAddress.Hex(), l.DestinationNetwork, l.Destination.Hex())
	case leaf.Amount == nil || leaf.Amount.Cmp(amount) != 0:
		return fmt.Errorf("the leaf has amount %v, the ledger %s", leaf.Amount, l.Amount)
	case len(metadata) > 0 && !bytes.Equal(leaf.Metadata, metadata):
		return errors.New("the leaf's metadata differs from the ledger's")
	}
	return nil
}
