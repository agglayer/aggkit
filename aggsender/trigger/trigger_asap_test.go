package trigger

import (
	"context"
	"testing"
	"time"

	"github.com/agglayer/aggkit/log"
	"github.com/stretchr/testify/require"
)

func TestASAPTrigger_DefaultDelay(t *testing.T) {
	require.Equal(t, 1*time.Second, defaultDelay, "defaultDelay should be 1 second")
}

func TestASAPTrigger_ForceTriggerEvent(t *testing.T) {
	logger := log.WithFields("aggsender-test", "ut")
	trigger := newASAPTrigger(logger)

	// Test when channel is nil
	trigger.ForceTriggerEvent() // Should log a warning, but no panic

	// Test with a valid channel
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := trigger.TriggerCh(ctx)

	go func() {
		trigger.ForceTriggerEvent()
	}()

	select {
	case event := <-ch:
		require.NotNil(t, event, "Expected a trigger event")
		require.Equal(t, "ASAP Event", event.String(), "Unexpected event string")
	case <-time.After(2 * time.Second):
		t.Fatal("Expected a trigger event, but none received")
	}
}

func TestASAPTrigger_OnAggsenderWaitingTrigger(t *testing.T) {
	logger := log.WithFields("aggsender-test", "ut")
	trigger := newASAPTrigger(logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := trigger.TriggerCh(ctx)

	// Test sending a trigger
	trigger.OnAggsenderWaitingTrigger()

	select {
	case event := <-ch:
		require.NotNil(t, event, "Expected a trigger event")
		require.Equal(t, "ASAP Event", event.String(), "Unexpected event string")
	case <-time.After(2 * time.Second):
		t.Fatal("Expected a trigger event, but none received")
	}

	// Test when trigger is already running
	trigger.OnAggsenderWaitingTrigger() // Should skip sending another trigger
}
func TestASAPTrigger_Status(t *testing.T) {
	logger := log.WithFields("aggsender-test", "ut")
	trigger := newASAPTrigger(logger)

	status := trigger.Status()
	require.Equal(t, "ASAP Runner: trying to generate certs as soon as possible", status, "Unexpected status message")
}
