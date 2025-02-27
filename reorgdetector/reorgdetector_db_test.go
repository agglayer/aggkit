package reorgdetector

import (
	"path"
	"testing"
	"time"

	aggkittypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/etherman"
	common "github.com/ethereum/go-ethereum/common"
	"github.com/russross/meddler"
	"github.com/stretchr/testify/require"
)

func TestInsertReorgEvent(t *testing.T) {
	// Create test DB dir
	testDir := path.Join(t.TempDir(), "reorgdetectorTest_ReorgDetector.sqlite")
	reorgDetector, err := New(nil,
		Config{
			DBPath:              testDir,
			CheckReorgsInterval: aggkittypes.NewDuration(time.Millisecond * 100),
			FinalizedBlock:      etherman.FinalizedBlock,
		}, L1)
	require.NoError(t, err)
	event := ReorgEvent{
		DetectedAt:   time.Now().Unix(),
		FromBlock:    1,
		ToBlock:      2,
		SubscriberID: "test",
		TrackedHash:  common.Hash{},
		CurrentHash:  common.Hash{},
		Version:      "1.0",
		ExtraData:    "extra",
	}

	err = reorgDetector.insertReorgEvent(event)
	require.NoError(t, err)
	eventFromDB := ReorgEvent{}
	err = meddler.QueryRow(reorgDetector.db, &eventFromDB,
		"SELECT * FROM reorg_event WHERE subscriber_id = $1;", "test")
	require.NoError(t, err)
	require.Equal(t, event, eventFromDB)
}
