package aggsender

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/aggsender/config"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
)

// NewRunner creates and returns a new Runner instance based on the provided configuration mode.
// It supports two modes: PreconfPPMode which returns a preconfRunner, and all other modes
// which return an epochBasedRunner.
func NewRunner(
	ctx context.Context,
	cfg config.Config,
	log aggkitcommon.Logger,
	l1Client aggkittypes.BaseEthereumClienter,
	l2BridgeSync types.L2BridgeSyncer,
	agglayerClient agglayer.AgglayerClientInterface) (types.Runner, error) {
	switch cfg.Mode {
	case types.PreconfPPMode:
		return newPreconfRunner(
			log,
			l2BridgeSync,
		), nil
	default:
		return newEpochBasedRunner(
			ctx,
			cfg,
			log,
			l1Client,
			agglayerClient,
		)
	}
}

// epochBasedRunner is a runner implementation that executes operations based on epoch transitions.
// It listens to both epoch and block notifications to coordinate execution timing with the
// underlying blockchain's epoch boundaries. This runner is suitable for operations that need
// to be synchronized with epoch changes, such as aggregation tasks that must complete within
// specific epoch windows.
type epochBasedRunner struct {
	epochNotifier types.EpochNotifier
	blockNotifier types.BlockNotifier
}

// newEpochBasedRunner creates and initializes a new epochBasedRunner instance.
// It sets up a block notifier that polls L1 for new blocks with latest block finality,
// configures an epoch notifier based on agglayer settings using the provided epoch
// notification percentage, and returns a runner that combines both notifiers.
//
// Returns:
//   - *epochBasedRunner: Configured runner with epoch and block notifiers
//   - error: Any error encountered during initialization
//
// The function will return an error if:
//   - Block notifier initialization fails
//   - Epoch notifier configuration generation fails
//   - Epoch notifier creation fails
func newEpochBasedRunner(
	ctx context.Context,
	cfg config.Config,
	log aggkitcommon.Logger,
	l1Client aggkittypes.BaseEthereumClienter,
	agglayerClient agglayer.AgglayerClientInterface) (*epochBasedRunner, error) {
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

	return &epochBasedRunner{
		epochNotifier: epochNotifier,
		blockNotifier: blockNotifier,
	}, nil
}

// Status returns the current status of the epoch-based runner as a string.
// It retrieves the epoch status from the epoch notifier and converts it to its string representation.
func (r *epochBasedRunner) Status() string {
	return r.epochNotifier.GetEpochStatus().String()
}

// Run starts the epoch-based runner by initializing both block and epoch notifiers
// in separate goroutines and begins sending epoch-based certificates through the
// provided certificate sender. The method starts the block notifier first, followed
// by the epoch notifier, and then delegates certificate sending to the certSender
// with the epoch notifier and a starting epoch of 0.
//
// The method does not return until the certificate sending process completes or
// the context is cancelled.
func (r *epochBasedRunner) Run(ctx context.Context, certSender types.CertificateSender) {
	log.Infof("Starting blockNotifier: %s", r.blockNotifier.String())
	go r.blockNotifier.Start(ctx)
	log.Infof("Starting epochNotifier: %s", r.epochNotifier.String())
	go r.epochNotifier.Start(ctx)

	certSender.SendEpochBasedCertificates(ctx, r.epochNotifier, 0)
}

// preconfRunner handles preconfirmation operations by listening to L2 bridge synchronization
// and maintaining subscription state to the synchronized L2 bridge events.
type preconfRunner struct {
	log          aggkitcommon.Logger
	l2BridgeSync types.L2BridgeSyncer
	subscription *sync.Subscription
}

// newPreconfRunner creates and initializes a new preconfRunner instance.
// It sets up the runner with the provided logger and L2 bridge synchronizer,
// and establishes a subscription to the sync events with a buffer size of 10.
//
// Parameters:
//   - log: Logger instance for logging operations
//   - l2BridgeSync: L2 bridge synchronizer for handling bridge synchronization
//
// Returns:
//   - *preconfRunner: A new preconfRunner instance ready for use
func newPreconfRunner(
	log aggkitcommon.Logger,
	l2BridgeSync types.L2BridgeSyncer,
) *preconfRunner {
	return &preconfRunner{
		log:          log,
		l2BridgeSync: l2BridgeSync,
		subscription: l2BridgeSync.SubscribeToSync("aggsender", 10), // TODO make buffer size configurable
	}
}

// Status returns a human-readable string describing the current state of the preconf runner.
// It indicates that the runner is actively listening for bridge synchronization events.
func (r *preconfRunner) Status() string {
	return "PreconfPP Runner: listening to bridge sync events"
}

func (r *preconfRunner) Run(ctx context.Context, certSender types.CertificateSender) {
	r.log.Info("PreconfPP mode: listening to bridge sync events")

	for {
		select {
		case blockNotification := <-r.subscription.BlockCh:
			r.log.Infof("PreconfPP: received block %d with %d events",
				blockNotification.Block.Num, len(blockNotification.Block.Events))
			// TODO build preconf request and send it
		case <-ctx.Done():
			r.log.Info("PreconfPP runner stopped")
			return
		}
	}
}
