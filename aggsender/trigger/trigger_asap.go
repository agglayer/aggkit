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

type asapTriggerEvent struct{}

func (e *asapTriggerEvent) String() string {
	return "ASAP Event"
}

// asapTrigger is a trigger implementation that executes operations as soon as possible.
// It tries to generate a new cert after the last cert is in a final state (settled or inError).
// An offset delay can be configured.
type asapTrigger struct {
	log aggkitcommon.Logger
	cfg config.TriggerASAPConfig
	// mut protects triggerRunning and ch
	mut            sync.Mutex
	ch             chan types.CertificateTriggerEvent
	ctx            context.Context
	triggerRunning bool
}

func newASAPTrigger(log aggkitcommon.Logger, cfg *config.TriggerASAPConfig) *asapTrigger {
	if cfg == nil {
		cfg = config.NewTriggerASAPConfigDefault()
	}
	return &asapTrigger{
		log: log,
		cfg: *cfg,
	}
}
func (r *asapTrigger) Setup(_ context.Context) {
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
	r.fulfillMinimumInterval()
	return ch
}

// ForceTriggerEvent forces to emit a synchronization event unconditionally.
func (r *asapTrigger) ForceTriggerEvent() {
	if r.ch == nil {
		r.log.Warnf("ASAP Trigger: channel is nil, cannot send trigger")
		return
	}
	r.ch <- &asapTriggerEvent{}
}

// OnAggsenderWaitingTrigger Aggsender is waiting for a trigger to generate a new certificate
func (r *asapTrigger) OnAggsenderWaitingTrigger() {
	// send a event to channel r.ch after r.delay. Also check if r.ctx is done
	r.log.Debugf("ASAP Trigger: sending a trigger in %s", r.cfg.DelayBeetweenCertificates.String())
	if r.ch == nil {
		r.log.Warnf("ASAP Trigger: channel is nil, cannot send trigger")
		return
	}

	// This call is going to set to true r.triggerRunning if it's not already set
	if r.isTriggerProgrammed(true) {
		r.mut.Unlock()
		r.log.Debugf("ASAP Trigger: trigger already running, skipping")
		return
	}

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
			r.trigger()
		}
	}()
}

func (r *asapTrigger) trigger() {
	r.mut.Lock()
	defer r.mut.Unlock()
	r.ch <- &asapTriggerEvent{}
	r.triggerRunning = false
	r.fulfillMinimumInterval()
}

// fulfillMinimumInterval try to send a trigger after cfg.MinimumNewCertificateInterval
// unless there are a new trigger already programmed
func (r *asapTrigger) fulfillMinimumInterval() {
	if r.cfg.MinimumNewCertificateInterval.Duration == 0 {
		return
	}
	go func() {
		select {
		case <-r.ctx.Done():
			return
		case <-time.After(r.cfg.MinimumNewCertificateInterval.Duration):
			// We just check if there are a trigger programmed, if not we send a new trigger that is going
			// to program a new one after the minimum interval
			if r.isTriggerProgrammed(false) {
				r.log.Debugf("ASAP Trigger: minimum interval elapsed but trigger already programmed, skipping")
				return
			}
			r.log.Infof("ASAP Trigger: minimum interval elapsed (%s), sending trigger",
				r.cfg.MinimumNewCertificateInterval.String())
			r.trigger()
		}
	}()
}

// isTriggerProgrammed checks if a trigger is already programmed (running) and set if if setIt is true
func (r *asapTrigger) isTriggerProgrammed(setIt bool) bool {
	r.mut.Lock()
	defer r.mut.Unlock()
	returnValue := r.triggerRunning
	if setIt && !r.triggerRunning {
		r.triggerRunning = true
	}
	return returnValue
}
