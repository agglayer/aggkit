package trigger

import (
	"context"
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
	log                aggkitcommon.Logger
	ch                 chan types.CertificateTriggerEvent
	ctx                context.Context
	aggsenderBusyState bool
	delay              time.Duration
}

func newASAPTrigger(log aggkitcommon.Logger) *asapTrigger {
	return &asapTrigger{
		log:                log,
		aggsenderBusyState: true,
		delay:              defaultDelay,
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

// ForceTriggerEvent forces the preconf trigger to emit a synchronization event.
func (r *asapTrigger) ForceTriggerEvent() {
	r.ch <- &asapTriggerEvent{}
}

// OnAggsenderWaitingTrigger Aggsender is waiting for a trigger to generate a new certificate
func (r *asapTrigger) OnAggsenderWaitingTrigger() {
	// send a event to channel r.ch after r.delay. Also check if r.ctx is done
	r.log.Debugf("ASAP Trigger: sending a trigger in %s", r.delay.String())
	go func() {
		select {
		case <-r.ctx.Done():
			return
		case <-time.After(r.delay):
			r.ch <- &asapTriggerEvent{}
		}
	}()
}
