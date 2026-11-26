package main

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeLegacyReorgUnsubscriber struct {
	unsubscribed []string
}

func (f *fakeLegacyReorgUnsubscriber) Unsubscribe(id string) error {
	f.unsubscribed = append(f.unsubscribed, id)
	return nil
}

func TestStopLegacyReorgTracking(t *testing.T) {
	t.Run("removes the syncer from the reorg detector", func(t *testing.T) {
		reorgDetector := &fakeLegacyReorgUnsubscriber{}
		stopLegacyReorgTracking(reorgDetector, "L1BridgeSyncer")
		require.Equal(t, []string{"L1BridgeSyncer"}, reorgDetector.unsubscribed)
	})

	t.Run("ignores a reorg detector that can't unsubscribe", func(t *testing.T) {
		require.NotPanics(t, func() {
			stopLegacyReorgTracking(nil, "L1BridgeSyncer")
			stopLegacyReorgTracking(errors.New("not a reorg detector"), "L1BridgeSyncer")
		})
	})
}
