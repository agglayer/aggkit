package claimer

import (
	"encoding/json"
	"fmt"
	"os"

	exitcertificate "github.com/agglayer/aggkit/tools/exit_certificate"
)

// LoadPendingBridges reads the exit_certificate PENDING_CLAIMS step output (step-pending-l2-bridges.json).
func LoadPendingBridges(path string) ([]exitcertificate.PendingBridge, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading pending bridges %q: %w", path, err)
	}
	var pending []exitcertificate.PendingBridge
	if err := json.Unmarshal(raw, &pending); err != nil {
		return nil, fmt.Errorf("parsing pending bridges %q: %w", path, err)
	}
	return pending, nil
}
