package exit_certificate

import (
	"fmt"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/log"
)

// RunStepI assembles the final certificate by applying the NewLocalExitRoot from Step G
// and the PreviousLocalExitRoot from Step H.
func RunStepI(certificate *agglayertypes.Certificate, gResult *StepGResult, hResult *StepHResult) error {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP I - Assemble final certificate")
	log.Info("═══════════════════════════════════════════")

	if certificate == nil {
		return fmt.Errorf("certificate is nil")
	}
	if gResult == nil {
		return fmt.Errorf("step G result is nil")
	}

	certificate.NewLocalExitRoot = gResult.NewLocalExitRoot
	log.Infof("NewLocalExitRoot:      %s", certificate.NewLocalExitRoot.Hex())

	if hResult != nil {
		certificate.PrevLocalExitRoot = hResult.PreviousLocalExitRoot
		log.Infof("PreviousLocalExitRoot: %s", certificate.PrevLocalExitRoot.Hex())
	}

	log.Info("STEP I complete")
	return nil
}
