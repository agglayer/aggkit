package trigger

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/aggsender/config"
	triggertypes "github.com/agglayer/aggkit/aggsender/trigger/types"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	etherman "github.com/agglayer/aggkit/etherman/block_notifier"
	ethermantypes "github.com/agglayer/aggkit/etherman/types"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
)

// epochBasedTrigger is a trigger implementation that executes operations based on epoch transitions.
// It listens to both epoch and block notifications to coordinate execution timing with the
// underlying blockchain's epoch boundaries. This trigger is suitable for operations that need
// to be synchronized with epoch changes, such as aggregation tasks that must complete within
// specific epoch windows.
type epochBasedTrigger struct {
	epochNotifier triggertypes.EpochNotifier
	blockNotifier ethermantypes.BlockNotifier
}

// newEpochBasedTrigger creates and initializes a new epochBasedTrigger instance.
// It sets up a block notifier that polls L1 for new blocks with latest block finality,
// configures an epoch notifier based on agglayer settings using the provided epoch
// notification percentage, and returns a trigger that combines both notifiers.

// Returns:
//   - *epochBasedTrigger: Configured trigger with epoch and block notifiers
//   - error: Any error encountered during initialization
//
// The function will return an error if:
//   - Block notifier initialization fails
//   - Epoch notifier configuration generation fails
//   - Epoch notifier creation fails
func newEpochBasedTrigger(
	ctx context.Context,
	cfg config.TriggerEpochBasedConfig,
	log aggkitcommon.Logger,
	l1Client aggkittypes.BaseEthereumClienter,
	agglayerClient agglayer.AgglayerClientInterface) (*epochBasedTrigger, error) {
	// Create block notifier that polls L1 for new blocks
	blockNotifier, err := etherman.NewBlockNotifierPolling(
		l1Client,
		etherman.ConfigBlockNotifierPolling{
			BlockFinalityType:     aggkittypes.LatestBlock,
			CheckNewBlockInterval: etherman.AutomaticBlockInterval,
		}, log, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize block notifier: %w", err)
	}

	// Create epoch notifier config based on agglayer settings
	notifierCfg, err := NewConfigEpochNotifierPerBlock(ctx,
		agglayerClient, cfg.EpochNotificationPercentage)
	if err != nil {
		return nil, fmt.Errorf("failed to generate Epoch Notifier config. Reason: %w", err)
	}

	// Create epoch notifier that wraps the block notifier
	epochNotifier, err := NewEpochNotifierPerBlock(
		blockNotifier,
		log,
		*notifierCfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create epoch notifier: %w", err)
	}

	return &epochBasedTrigger{
		epochNotifier: epochNotifier,
		blockNotifier: blockNotifier,
	}, nil
}

// Status returns the current status of the epoch-based trigger as a string.
// It retrieves the epoch status from the epoch notifier and converts it to its string representation.
func (r *epochBasedTrigger) Status() string {
	return r.epochNotifier.GetEpochStatus().String()
}

// TriggerCh returns a read-only channel of events produced by the epoch notifier.
// Values sent through this channel are types.EpochEvent (which implement CertificateTriggerEvent).
// The returned channel will be closed when the provided context is canceled.
func (r *epochBasedTrigger) TriggerCh(ctx context.Context) <-chan types.CertificateTriggerEvent {
	ch := make(chan types.CertificateTriggerEvent)
	epochSub := r.epochNotifier.Subscribe("aggsender")
	go func() {
		for {
			select {
			case <-ctx.Done():
				close(ch)
				return
			case epochEvent := <-epochSub:
				ch <- epochEvent
			}
		}
	}()

	return ch
}

// Setup starts the internal block and epoch notifiers asynchronously so they can
// begin emitting events. This should typically be called once during component
// initialization and will return immediately after spawning the background tasks.
func (r *epochBasedTrigger) Setup(ctx context.Context) {
	log.Infof("Starting blockNotifier: %s", r.blockNotifier.String())
	go r.blockNotifier.Start(ctx)
	log.Infof("Starting epochNotifier: %s", r.epochNotifier.String())
	go r.epochNotifier.Start(ctx)
}

// ForceTriggerEvent forces the epoch notifier to publish an epoch event immediately.
func (r *epochBasedTrigger) ForceTriggerEvent() {
	r.epochNotifier.ForcePublishEpochEvent()
}

// OnAggsenderWaitingTrigger Aggsender is waiting for a trigger to generate a new certificate
func (r *epochBasedTrigger) OnAggsenderWaitingTrigger() {
}
