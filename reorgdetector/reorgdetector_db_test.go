package reorgdetector

import (
	"context"
	"math/big"
	"path"
	"testing"
	"time"

	cfgtypes "github.com/agglayer/aggkit/config/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	common "github.com/ethereum/go-ethereum/common"
	"github.com/russross/meddler"
	"github.com/stretchr/testify/require"
)

func setupReorgDetector(t *testing.T) *ReorgDetector {
	t.Helper()

	testDir := path.Join(t.TempDir(), "reorgdetectorTest_ReorgDetector.sqlite")
	reorgDetector, err := New(nil,
		Config{
			DBPath:              testDir,
			CheckReorgsInterval: cfgtypes.NewDuration(time.Millisecond * 100),
			FinalizedBlock:      aggkittypes.FinalizedBlock,
		}, L1)
	require.NoError(t, err)

	return reorgDetector
}

func TestInsertReorgEvent(t *testing.T) {
	reorgDetector := setupReorgDetector(t)
	event := ReorgEvent{
		DetectedAt:   time.Now().Unix(),
		FromBlock:    1,
		ToBlock:      2,
		SubscriberID: "test",
		TrackedHash:  common.Hash{},
		CurrentHash:  common.Hash{},
		Version:      "1.0",
	}

	err := reorgDetector.insertReorgEvent(event)
	require.NoError(t, err)
	eventFromDB := ReorgEvent{}
	err = meddler.QueryRow(reorgDetector.db, &eventFromDB,
		"SELECT * FROM reorg_event WHERE subscriber_id = $1;", "test")
	require.NoError(t, err)
	require.Equal(t, event, eventFromDB)
}

func TestGetLastReorgEvent(t *testing.T) {
	reorgDetector := setupReorgDetector(t)

	t.Run("Returns empty result when no events exist", func(t *testing.T) {
		rEvent, err := reorgDetector.GetLastReorgEvent(context.TODO())
		require.NoError(t, err)
		require.Equal(t, ReorgEvent{}, rEvent)
	})

	t.Run("Returns the latest reorg event", func(t *testing.T) {
		events := []ReorgEvent{
			{
				DetectedAt:   time.Now().Unix(),
				FromBlock:    1,
				ToBlock:      10,
				SubscriberID: "test1",
				TrackedHash:  common.Hash{},
				CurrentHash:  common.Hash{},
				Version:      "1.0",
			},
			{
				DetectedAt:   time.Now().Unix() + 2,
				FromBlock:    15,
				ToBlock:      20,
				SubscriberID: "test2",
				TrackedHash:  common.Hash{},
				CurrentHash:  common.Hash{},
				Version:      "1.0",
			},
		}

		for _, event := range events {
			require.NoError(t, reorgDetector.insertReorgEvent(event))
		}

		rEvent, err := reorgDetector.GetLastReorgEvent(context.TODO())
		require.NoError(t, err)
		require.Equal(t, events[1], rEvent)
	})
}

func TestGetLastReorgEvent_SameDetectedAt(t *testing.T) {
	reorgDetector := setupReorgDetector(t)
	ctx := context.TODO()
	detectedAt := time.Now().Unix()

	newEvent := func(detectedAt int64, subscriberID string, fromBlock, toBlock uint64) ReorgEvent {
		return ReorgEvent{
			DetectedAt:   detectedAt,
			FromBlock:    fromBlock,
			ToBlock:      toBlock,
			SubscriberID: subscriberID,
			Version:      "1.0",
		}
	}
	// The subscriber ids and the ranges are chosen so that neither the order of the primary key
	// (detected_at, subscriber_id, from_block, to_block) nor its reverse matches the insertion order
	events := []ReorgEvent{
		newEvent(detectedAt-10, "syncer-b", 1, 2),
		newEvent(detectedAt, "syncer-b", 50, 60),
		newEvent(detectedAt, "syncer-c", 40, 60),
		newEvent(detectedAt, "syncer-a", 45, 60),
	}
	for _, event := range events {
		require.NoError(t, reorgDetector.insertReorgEvent(event))
	}

	t.Run("ties in detected_at are resolved by insertion order", func(t *testing.T) {
		for range 5 {
			rEvent, err := reorgDetector.GetLastReorgEvent(ctx)
			require.NoError(t, err)
			require.Equal(t, events[3], rEvent)
		}
	})

	t.Run("an older event inserted later is not the last one", func(t *testing.T) {
		require.NoError(t, reorgDetector.insertReorgEvent(newEvent(detectedAt-5, "syncer-d", 7, 8)))

		rEvent, err := reorgDetector.GetLastReorgEvent(ctx)
		require.NoError(t, err)
		require.Equal(t, events[3], rEvent)
	})

	t.Run("filter by subscriber", func(t *testing.T) {
		for _, expected := range events[1:] {
			rEvent, err := reorgDetector.GetLastReorgEventBySubscribers(ctx, expected.SubscriberID)
			require.NoError(t, err)
			require.Equal(t, expected, rEvent)
		}
	})

	t.Run("a subscriber without events gets an empty result", func(t *testing.T) {
		rEvent, err := reorgDetector.GetLastReorgEventBySubscribers(ctx, "unknown")
		require.NoError(t, err)
		require.Equal(t, ReorgEvent{}, rEvent)

		rEvent, err = reorgDetector.GetLastReorgEventBySubscribers(ctx)
		require.NoError(t, err)
		require.Equal(t, ReorgEvent{}, rEvent)
	})
}

func TestGetLastReorgEventBySubscribers_MergedRange(t *testing.T) {
	const (
		bridgeSyncer = "L2BridgeSyncer"
		claimSyncer  = "L2ClaimSyncer"
		otherSyncer  = "L2GERSyncer"
	)
	ctx := context.TODO()
	detectedAt := time.Now().Unix()
	newEvent := func(detectedAt int64, subscriberID string, fromBlock, toBlock uint64) ReorgEvent {
		return ReorgEvent{
			DetectedAt:   detectedAt,
			FromBlock:    fromBlock,
			ToBlock:      toBlock,
			SubscriberID: subscriberID,
			TrackedHash:  common.BigToHash(new(big.Int).SetUint64(fromBlock)),
			CurrentHash:  common.BigToHash(new(big.Int).SetUint64(toBlock)),
			Version:      subscriberID + "-version",
		}
	}
	// merged returns the event newest with the given range
	merged := func(newest ReorgEvent, fromBlock, toBlock uint64) ReorgEvent {
		newest.FromBlock = fromBlock
		newest.ToBlock = toBlock
		return newest
	}

	tests := []struct {
		name     string
		events   []ReorgEvent
		expected func(events []ReorgEvent) ReorgEvent
	}{
		{
			name: "both syncers in the same pass with different ranges",
			events: []ReorgEvent{
				newEvent(detectedAt-10, bridgeSyncer, 1, 100), // older pass: not merged
				newEvent(detectedAt, claimSyncer, 40, 55),
				newEvent(detectedAt, otherSyncer, 5, 90), // not one of the requested subscribers
				newEvent(detectedAt, bridgeSyncer, 45, 60),
			},
			expected: func(events []ReorgEvent) ReorgEvent { return merged(events[3], 40, 60) },
		},
		{
			name: "both syncers in the same pass, the other insertion order",
			events: []ReorgEvent{
				newEvent(detectedAt, bridgeSyncer, 45, 60),
				newEvent(detectedAt, claimSyncer, 40, 55),
			},
			expected: func(events []ReorgEvent) ReorgEvent { return merged(events[1], 40, 60) },
		},
		{
			name: "one range contains the other",
			events: []ReorgEvent{
				newEvent(detectedAt, bridgeSyncer, 50, 52),
				newEvent(detectedAt, claimSyncer, 40, 60),
			},
			expected: func(events []ReorgEvent) ReorgEvent { return events[1] },
		},
		{
			name: "only the claim syncer in the last pass",
			events: []ReorgEvent{
				newEvent(detectedAt-10, bridgeSyncer, 1, 100),
				newEvent(detectedAt, otherSyncer, 5, 90),
				newEvent(detectedAt, claimSyncer, 40, 55),
			},
			expected: func(events []ReorgEvent) ReorgEvent { return events[2] },
		},
		{
			name: "only the bridge syncer in the last pass",
			events: []ReorgEvent{
				newEvent(detectedAt-10, claimSyncer, 1, 100),
				newEvent(detectedAt, bridgeSyncer, 45, 60),
				newEvent(detectedAt, otherSyncer, 5, 90),
			},
			expected: func(events []ReorgEvent) ReorgEvent { return events[1] },
		},
		{
			name: "an older pass inserted later is not the last one",
			events: []ReorgEvent{
				newEvent(detectedAt, bridgeSyncer, 45, 60),
				newEvent(detectedAt-5, claimSyncer, 1, 100),
			},
			expected: func(events []ReorgEvent) ReorgEvent { return events[0] },
		},
		{
			name: "only other syncers",
			events: []ReorgEvent{
				newEvent(detectedAt, otherSyncer, 5, 90),
			},
			expected: func([]ReorgEvent) ReorgEvent { return ReorgEvent{} },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reorgDetector := setupReorgDetector(t)
			for _, event := range tt.events {
				require.NoError(t, reorgDetector.insertReorgEvent(event))
			}

			// the answer doesn't depend on the order of the subscribers nor on the call
			for range 3 {
				rEvent, err := reorgDetector.GetLastReorgEventBySubscribers(ctx, bridgeSyncer, claimSyncer)
				require.NoError(t, err)
				require.Equal(t, tt.expected(tt.events), rEvent)

				rEvent, err = reorgDetector.GetLastReorgEventBySubscribers(ctx, claimSyncer, bridgeSyncer)
				require.NoError(t, err)
				require.Equal(t, tt.expected(tt.events), rEvent)
			}
		})
	}
}
