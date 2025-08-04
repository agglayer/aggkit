package reorgdetector

import (
	"context"
	"testing"

	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoOpReorgDetector(t *testing.T) {
	detector := NewNoOpReorgDetector()

	// Test GetFinalizedBlockType
	assert.Equal(t, aggkittypes.FinalizedBlock, detector.GetFinalizedBlockType())

	// Test String
	assert.Equal(t, "NoOpReorgDetector", detector.String())

	// Test Subscribe
	sub, err := detector.Subscribe("test-id")
	require.NoError(t, err)
	assert.NotNil(t, sub)
	assert.NotNil(t, sub.ReorgedBlock)
	assert.NotNil(t, sub.ReorgProcessed)

	// Test AddBlockToTrack (should do nothing)
	err = detector.AddBlockToTrack(context.Background(), "test-id", 123, common.Hash{})
	assert.NoError(t, err)

	// Test GetLastReorgEvent (should return empty event)
	event, err := detector.GetLastReorgEvent(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, ReorgEvent{}, event)
}
