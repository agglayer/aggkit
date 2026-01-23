package trigger

import (
	"context"

	"github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	aggkitsync "github.com/agglayer/aggkit/sync"
)

// preconfTrigger handles preconfirmation operations by listening to L2 bridge synchronization
// and maintaining subscription state to the synchronized L2 bridge events.
type preconfTrigger struct {
	log          aggkitcommon.Logger
	l2BridgeSync types.L2BridgeSyncer
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
	r.ch = ch

	go func() {
		for {
			select {
			case <-ctx.Done():
				r.ch = nil
				close(ch)

				return
			case epochEvent := <-syncSub:
				ch <- epochEvent
			}
		}
	}()

	return ch
}

// ForceTriggerEvent forces the preconf trigger to emit a synchronization event.
func (r *preconfTrigger) ForceTriggerEvent() {
	blockNumber, err := r.l2BridgeSync.GetLastProcessedBlock(context.Background())
	if err != nil {
		r.log.Errorf("ForceTriggerEvent: Failed to get last processed block: %v", err)
		return
	}
	if r.ch == nil {
		return
	}
	r.ch <- aggkitsync.Block{Num: blockNumber}
}

// OnIdle Aggsender is waiting for a trigger to generate a new certificate
func (r *preconfTrigger) OnIdle() {
}
