package trigger

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/aggsender/config"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	aggkittypes "github.com/agglayer/aggkit/types"
)

// NewCertificateSendTrigger creates and returns a new CertificateSendTrigger instance
// based on the provided configuration mode.
// It supports three trigger modes:
// - NewBridgeTriggerMode: triggers on new bridge events (preconfTrigger)
// - EpochBasedTriggerMode: triggers based on epoch progression (epochBasedTrigger)
// - ASAPTriggerMode: triggers as soon as possible (asapTrigger)
// AutoTriggerMode is resolved to one of the above based on the AggsenderMode.
func NewCertificateSendTrigger(
	ctx context.Context,
	cfg config.Config,
	log aggkitcommon.Logger,
	l1Client aggkittypes.BaseEthereumClienter,
	l2BridgeSync types.L2BridgeSyncer,
	agglayerClient agglayer.AgglayerClientInterface) (types.CertificateSendTrigger, error) {
	mode := cfg.TriggerCertMode
	if mode == types.AutoTriggerMode {
		var err error
		mode, err = defaultTriggerForAggsenderMode(cfg.Mode)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve Auto TriggerCertMode: %w", err)
		}
		log.Infof("Resolved Auto TriggerCertMode to %s based on AggsenderMode %s", mode.String(), cfg.Mode.String())
	}

	switch mode {
	case types.NewBridgeTriggerMode:
		return newPreconfTrigger(
			log,
			l2BridgeSync,
		), nil
	case types.EpochBasedTriggerMode:
		return newEpochBasedTrigger(
			ctx,
			cfg.TriggerEpochBased,
			log,
			l1Client,
			agglayerClient,
		)
	case types.ASAPTriggerMode:
		return newASAPTrigger(
			log,
		), nil
	default:
		return nil, fmt.Errorf("unsupported trigger mode: %s", mode)
	}
}

func defaultTriggerForAggsenderMode(
	mode types.AggsenderMode,
) (types.CertificateSendTriggerMode, error) {
	switch mode {
	case types.PreconfPPMode:
		return types.NewBridgeTriggerMode, nil
	case types.AggchainProofMode, types.PessimisticProofMode:
		return types.EpochBasedTriggerMode, nil
	case types.AutoMode:
		return "", fmt.Errorf("aggsender AutoMode should be resolved before calling this function")
	default:
		return "", fmt.Errorf("unknown trigger mode: %s", mode)
	}
}
