package trigger

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/agglayer/aggkit/aggsender/config"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
)

// defaultDelay is the default delay before sending a trigger event
const defaultDelay = 1 * time.Second

type asapTriggerEvent struct {
	ID     uint
	Source string
	Parent *asapTriggerEvent
}

func (e *asapTriggerEvent) String() string {
	if e == nil {
		return "ASAP Event{nil}"
	}
	res := fmt.Sprintf("ASAP Event{%d/%s", e.ID, e.Source)
	if e.Parent != nil {
		res += fmt.Sprintf(" <- %d/%s}", e.Parent.ID, e.Parent.Source)
	} else {
		res += "}"
	}
	return res
}

// asapTrigger is a trigger implementation that executes operations as soon as possible.
// It tries to generate a new cert after the last cert is in a final state (settled or inError).
// An offset delay can be configured.
type asapTrigger struct {
	log          aggkitcommon.Logger
	l2BridgeSync types.L2BridgeSyncer
	cfg          config.TriggerASAPConfig
	// mut protects triggerRunning, ch, eventID, and lastEventTime
	mut            sync.Mutex
	ch             chan types.CertificateTriggerEvent
	ctx            context.Context
	triggerRunning bool
	eventID        uint
	lastEventTime  time.Time
}

func newASAPTrigger(log aggkitcommon.Logger, cfg *config.TriggerASAPConfig,
	l2BridgeSync types.L2BridgeSyncer) (*asapTrigger, error) {
	if cfg == nil {
		cfg = config.NewTriggerASAPConfigDefault()
	}
	if cfg.OnNewL2Bridge && l2BridgeSync == nil {
		return nil, fmt.Errorf("L2 Bridge Syncer must be provided when OnNewL2Bridge is enabled in ASAP Trigger config")
	}
	return &asapTrigger{
		log:          log,
		cfg:          *cfg,
		l2BridgeSync: l2BridgeSync,
	}, nil
}
func (r *asapTrigger) Setup(ctx context.Context) {
}

func (r *asapTrigger) Status() string {
	return fmt.Sprintf("ASAP Runner: cfg: %s", r.cfg.String())
}

func (r *asapTrigger) TriggerCh(ctx context.Context) <-chan types.CertificateTriggerEvent {
	r.mut.Lock()
	defer r.mut.Unlock()
	ch := make(chan types.CertificateTriggerEvent)
	r.ch = ch
	r.ctx = ctx
	// Initialize lastEventTime to now so the minimum interval starts from TriggerCh creation
	r.lastEventTime = time.Now()
	r.fulfillMinimumInterval(nil)
	r.subscribeNewBridge(ctx)
	return ch
}

func (r *asapTrigger) subscribeNewBridge(ctx context.Context) {
	if !r.cfg.OnNewL2Bridge || r.l2BridgeSync == nil {
		return
	}
	r.log.Infof("ASAP Trigger: subscribing to new L2 bridge events")
	bridgeSub := r.l2BridgeSync.SubscribeToNewBridge("aggsender-asap-trigger")
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case bridgeEvent := <-bridgeSub:
				r.onNewBridge(bridgeEvent)
			}
		}
	}()
}

// ForceTriggerEvent forces to emit a synchronization event unconditionally.
func (r *asapTrigger) ForceTriggerEvent() {
	if r.ch == nil {
		r.log.Warnf("ASAP Trigger: channel is nil, cannot send trigger")
		return
	}
	r.mut.Lock()
	event := r.createEvent("ForceTrigger", nil)
	r.lastEventTime = time.Now()
	r.mut.Unlock()
	r.ch <- event
}
func (r *asapTrigger) onNewBridge(blockNum uint64) {
	// send an event to channel r.ch after r.delay. Also check if r.ctx is done
	if r.ch == nil {
		r.log.Warnf("ASAP Trigger: channel is nil, cannot send trigger")
		return
	}

	// This call will set r.triggerRunning to true if it's not already set
	if r.isTriggerProgrammed(true) {
		r.log.Debugf("ASAP Trigger: trigger already programmed, skipping new bridge event at block %d", blockNum)
		return
	}
	r.log.Debugf("ASAP Trigger: sending a trigger due new bridge event at block %d", blockNum)
	go func() {
		r.trigger("newBridge", nil)
	}()
}

// OnIdle Aggsender is waiting for a trigger to generate a new certificate
func (r *asapTrigger) OnIdle() {
	// send an event to channel r.ch after r.delay. Also check if r.ctx is done
	if r.ch == nil {
		r.log.Warnf("ASAP Trigger: channel is nil, cannot send trigger")
		return
	}

	// This call will set r.triggerRunning to true if it's not already set
	if r.isTriggerProgrammed(true) {
		r.log.Debugf("ASAP Trigger: trigger already running, skipping")
		return
	}
	r.log.Debugf("ASAP Trigger: sending a trigger in %s", r.cfg.DelayBeetweenCertificates.String())

	go func() {
		select {
		case <-r.ctx.Done():
			r.mut.Lock()
			defer r.mut.Unlock()
			close(r.ch)
			r.ch = nil
			r.triggerRunning = false
			return
		case <-time.After(r.cfg.DelayBeetweenCertificates.Duration):
			r.trigger("Idle", nil)
		}
	}()
}

func (r *asapTrigger) trigger(source string, parent *asapTriggerEvent) {
	r.mut.Lock()
	r.triggerRunning = false
	event := r.createEvent(source, parent)
	r.lastEventTime = time.Now()
	r.fulfillMinimumInterval(event)
	ch := r.ch

	r.mut.Unlock()
	ch <- event
}

// createEvent creates a new trigger event with an auto-incremental ID and the specified source.
// Must be called with r.mut locked.
func (r *asapTrigger) createEvent(source string, parent *asapTriggerEvent) *asapTriggerEvent {
	r.eventID++
	return &asapTriggerEvent{
		ID:     r.eventID,
		Source: source,
		Parent: parent,
	}
}

// fulfillMinimumInterval tries to send a trigger after cfg.MinimumNewCertificateInterval
// unless there is a new trigger already programmed
func (r *asapTrigger) fulfillMinimumInterval(event *asapTriggerEvent) {
	if r.cfg.MinimumNewCertificateInterval.Duration == 0 {
		return
	}
	go func() {
		select {
		case <-r.ctx.Done():
			return
		case <-time.After(r.cfg.MinimumNewCertificateInterval.Duration):
			// Check if a trigger is already programmed
			if r.isTriggerProgrammed(false) {
				r.log.Debugf("ASAP Trigger(%s): minimum interval elapsed but trigger already programmed, skipping",
					event.String())
				return
			}

			// Check if enough time has passed since the last event
			r.mut.Lock()
			timeSinceLastEvent := time.Since(r.lastEventTime)
			r.mut.Unlock()

			if timeSinceLastEvent < r.cfg.MinimumNewCertificateInterval.Duration {
				r.log.Debugf("ASAP Trigger(%s): minimum interval not yet elapsed (elapsed: %s, required: %s), skipping",
					event.String(), timeSinceLastEvent, r.cfg.MinimumNewCertificateInterval.Duration)
				return
			}

			r.log.Infof("ASAP Trigger(%s): minimum interval elapsed (%s), sending trigger from event: %s",
				event.String(), r.cfg.MinimumNewCertificateInterval.String(), event.String())
			r.trigger("MinimalTime", event)
		}
	}()
}

// isTriggerProgrammed checks if a trigger is already programmed (running) and sets it if setIt is true
func (r *asapTrigger) isTriggerProgrammed(setIt bool) bool {
	r.mut.Lock()
	defer r.mut.Unlock()
	returnValue := r.triggerRunning
	if setIt && !r.triggerRunning {
		r.triggerRunning = true
	}
	return returnValue
}
