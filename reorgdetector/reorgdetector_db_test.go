package reorgdetector

import (
	"path"
	"testing"
	"time"

	aggkittypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/etherman"
	common "github.com/ethereum/go-ethereum/common"
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
	row := reorgDetector.db.QueryRow("SELECT * FROM reorg_event WHERE subscriber_id = $1", "test")
	var detectedAt int64
	var fromBlock uint64
	var toBlock uint64
	var subscriberID string
	var trackedHash string
	var currentHash string
	var version string
	var extraData string
	err = row.Scan(&detectedAt, &fromBlock, &toBlock, &subscriberID, &trackedHash, &currentHash, &version, &extraData)
	require.NoError(t, err)
	require.Equal(t, event.DetectedAt, detectedAt)
	require.Equal(t, event.FromBlock, fromBlock)
	require.Equal(t, event.ToBlock, toBlock)
	require.Equal(t, event.SubscriberID, subscriberID)
	require.Equal(t, event.TrackedHash.String(), trackedHash)
	require.Equal(t, event.CurrentHash.String(), currentHash)
	require.Equal(t, event.Version, version)
	require.Equal(t, event.ExtraData, extraData)
}
