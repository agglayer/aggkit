package reorgdetector

import (
	"context"
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
