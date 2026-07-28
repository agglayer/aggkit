package claimer

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/agglayer/aggkit/l1infotreesync"
	exitcertificate "github.com/agglayer/aggkit/tools/exit_certificate"
	"github.com/ethereum/go-ethereum/common"
)

// LoadStepWaitResult reads and parses step-wait-result.json produced by the exit_certificate WAIT
// step. It records the certificate's L1 settlement — the VerifyBatchesTrustedAggregator event and
// the accompanying L1 Info Tree update — identifying the exact L1 info tree leaf the certificate
// settled at.
func LoadStepWaitResult(path string) (*exitcertificate.StepWaitResult, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading wait result %q: %w", path, err)
	}

	var result exitcertificate.StepWaitResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parsing wait result %q: %w", path, err)
	}
	return &result, nil
}

// SettlementGER derives the Global Exit Root the certificate settled at, from the WAIT step's
// UpdateL1InfoTree event — keccak256(mainnetExitRoot, rollupExitRoot), the same hashing the L1
// GlobalExitRoot contract uses. It errors when the wait result did not capture that event.
func SettlementGER(result *exitcertificate.StepWaitResult) (common.Hash, error) {
	if result.UpdateL1InfoTree == nil {
		return common.Hash{}, fmt.Errorf(
			"wait result has no updateL1InfoTree event; cannot derive the settlement GER")
	}
	return l1infotreesync.CalculateGER(
		result.UpdateL1InfoTree.MainnetExitRoot,
		result.UpdateL1InfoTree.RollupExitRoot,
	), nil
}
