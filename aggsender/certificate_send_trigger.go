package aggsender

import (
	"context"
	"fmt"
	"sync"

	"github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/aggsender/config"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/log"
	aggkitsync "github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
)

// NewCertificateSendTrigger creates and returns a new CertificateSendTrigger instance
// based on the provided configuration mode.
// It supports two modes: PreconfPPMode which returns a preconfTrigger, and all other modes
// which return an epochBasedTrigger.
func NewCertificateSendTrigger(
	ctx context.Context,
	cfg config.Config,
	log aggkitcommon.Logger,
	l1Client aggkittypes.BaseEthereumClienter,
	l2BridgeSync types.L2BridgeSyncer,
	agglayerClient agglayer.AgglayerClientInterface) (types.CertificateSendTrigger, error) {
	switch cfg.Mode {
	case types.PreconfPPMode:
		return newPreconfTrigger(
			log,
			l2BridgeSync,
		), nil
	default:
		return newEpochBasedTrigger(
			ctx,
			cfg,
			log,
			l1Client,
			agglayerClient,
		)
	}
}

// epochBasedTrigger is a trigger implementation that executes operations based on epoch transitions.
// It listens to both epoch and block notifications to coordinate execution timing with the
// underlying blockchain's epoch boundaries. This trigger is suitable for operations that need
// to be synchronized with epoch changes, such as aggregation tasks that must complete within
// specific epoch windows.
type epochBasedTrigger struct {
	epochNotifier types.EpochNotifier
	blockNotifier types.BlockNotifier
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
	cfg config.Config,
	log aggkitcommon.Logger,
	l1Client aggkittypes.BaseEthereumClienter,
	agglayerClient agglayer.AgglayerClientInterface) (*epochBasedTrigger, error) {
	// Create block notifier that polls L1 for new blocks
	blockNotifier, err := NewBlockNotifierPolling(
		l1Client,
		ConfigBlockNotifierPolling{
			BlockFinalityType:     aggkittypes.LatestBlock,
			CheckNewBlockInterval: AutomaticBlockInterval,
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

// preconfTrigger handles preconfirmation operations by listening to L2 bridge synchronization
// and maintaining subscription state to the synchronized L2 bridge events.
type preconfTrigger struct {
	log          aggkitcommon.Logger
	l2BridgeSync types.L2BridgeSyncer
	mutex        sync.Mutex
	ch           chan types.CertificateTriggerEvent
}

// newPreconfTrigger creates and initializes a new preconfTrigger instance.
// It sets up the trigger with the provided logger and L2 bridge synchronizer,
// and establishes a subscription to the sync events with a buffer size of 10.
//
// Parameters:
//   - log: Logger instance for logging operations
//   - l2BridgeSync: L2 bridge synchronizer for handling bridge synchronization
//
// Returns:
//   - *preconfTrigger: A new preconfTrigger instance ready for use
func newPreconfTrigger(
	log aggkitcommon.Logger,
	l2BridgeSync types.L2BridgeSyncer,
) *preconfTrigger {
	return &preconfTrigger{
		log:          log,
		l2BridgeSync: l2BridgeSync,
	}
}

// Status returns a human-readable string describing the current state of the preconf trigger.
// It indicates that the trigger is actively listening for bridge synchronization events.
func (r *preconfTrigger) Status() string {
	return "PreconfPP Runner: listening to bridge sync events"
}

func (r *preconfTrigger) Setup(ctx context.Context) {
	// The preconf trigger does not have a blocking operation in this implementation.
	// It relies on the TriggerCh method to provide synchronization events.
}

// TriggerCh returns a read-only channel that forwards bridge sync block
// notifications. Each value is a sync.Block (which implements CertificateTriggerEvent).
// The returned channel will be closed when the provided context is canceled.
func (r *preconfTrigger) TriggerCh(ctx context.Context) <-chan types.CertificateTriggerEvent {
	syncSub := r.l2BridgeSync.SubscribeToSync("aggsender")

	ch := make(chan types.CertificateTriggerEvent)
	go func() {
		for {
			select {
			case <-ctx.Done():
				r.mutex.Lock()
				r.ch = nil
				r.mutex.Unlock()
				close(ch)

				return
			case epochEvent := <-syncSub:
				ch <- epochEvent
			}
		}
	}()
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.ch = ch
	return ch
}

// ForceTriggerEvent forces the preconf trigger to emit a synchronization event.
func (r *preconfTrigger) ForceTriggerEvent() {
	blockNumber, err := r.l2BridgeSync.GetLastProcessedBlock(context.Background())
	if err != nil {
		r.log.Errorf("ForceTriggerEvent: Failed to get last processed block: %v", err)
		return
	}
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.ch == nil {
		return
	}
	r.ch <- aggkitsync.Block{Num: blockNumber}
}
