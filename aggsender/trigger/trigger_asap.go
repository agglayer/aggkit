package trigger

import (
	"context"
	"sync/atomic"
	"time"

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
	log            aggkitcommon.Logger
	ch             chan types.CertificateTriggerEvent
	ctx            context.Context
	delay          time.Duration
	triggerRunning atomic.Bool
}

func newASAPTrigger(log aggkitcommon.Logger) *asapTrigger {
	return &asapTrigger{
		log:   log,
		delay: defaultDelay,
	}
}
func (r *asapTrigger) Setup(_ context.Context) {
}

func (r *asapTrigger) Status() string {
	return "ASAP Runner: trying to generate certs as soon as possible"
}

func (r *asapTrigger) TriggerCh(ctx context.Context) <-chan types.CertificateTriggerEvent {
	ch := make(chan types.CertificateTriggerEvent)
	r.ch = ch
	r.ctx = ctx
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
	r.log.Debugf("ASAP Trigger: sending a trigger in %s", r.delay.String())
	if r.ch == nil {
		r.log.Warnf("ASAP Trigger: channel is nil, cannot send trigger")
		return
	}
	if r.triggerRunning.Load() {
		r.log.Debugf("ASAP Trigger: trigger already running, skipping")
		return
	}
	r.triggerRunning.Store(true)
	go func() {
		select {
		case <-r.ctx.Done():
			close(r.ch)
			r.ch = nil
			r.triggerRunning.Store(false)
			return
		case <-time.After(r.delay):
			r.ch <- &asapTriggerEvent{}
			r.triggerRunning.Store(false)
		}
	}()
}
