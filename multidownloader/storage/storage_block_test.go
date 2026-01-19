package storage

import (
	"testing"

	mdtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/stretchr/testify/require"
)

func TestStorage_GetBlock(t *testing.T) {
	storage := newStorageForTest(t, nil)
	// BlockBase not present
	blockHeader, _, err := storage.GetBlockHeaderByNumber(nil, 1234)
	require.NoError(t, err, "cannot get BlockHeader")
	require.Nil(t, blockHeader, "expected nil BlockHeader")
	block := aggkittypes.NewBlockHeader(1234, exampleTestHash[0], 5678, &exampleTestHash[1])
	err = storage.saveAggkitBlock(nil, block, true)
	require.NoError(t, err, "cannot insert BlockHeader")
	// Get and verify block
	readBlock, isFinal, err := storage.GetBlockHeaderByNumber(nil, 1234)
	require.NoError(t, err, "cannot get BlockHeader")
	require.NotNil(t, readBlock, "expected non-nil BlockHeader")
	require.Equal(t, block, readBlock, "BlockHeader mismatch")
	require.True(t, isFinal, "expected block to be final")

	blockNilParentHash := aggkittypes.NewBlockHeader(1235, exampleTestHash[0], 5678, nil)
	err = storage.saveAggkitBlock(nil, blockNilParentHash, true)
	require.NoError(t, err, "cannot get BlockHeader")
	readBlock, _, err = storage.GetBlockHeaderByNumber(nil, blockNilParentHash.Number)
	require.NoError(t, err, "cannot get BlockHeader")
	require.Equal(t, blockNilParentHash, readBlock, "BlockHeader mismatch")
}

func TestStorage_GetRangeBlockHeader(t *testing.T) {
	storage := newStorageForTest(t, nil)
	block := aggkittypes.NewBlockHeader(4000, exampleTestHash[5], 1630002000, nil)
	err := storage.saveAggkitBlock(nil, block, mdtypes.NotFinalized)
	require.NoError(t, err, "cannot insert BlockHeader")

	lowest, highest, err := storage.GetRangeBlockHeader(nil, mdtypes.NotFinalized)
	require.NoError(t, err, "cannot get range BlockHeader")
	require.Equal(t, block, lowest, "lowest BlockHeader mismatch")
	require.Equal(t, block, highest, "highest BlockHeader mismatch")

	lowest, highest, err = storage.GetRangeBlockHeader(nil, mdtypes.Finalized)
	require.NoError(t, err, "cannot get range BlockHeader")
	require.True(t, lowest.Empty(), "lowest BlockHeader mismatch")
	require.True(t, highest.Empty(), "highest BlockHeader mismatch")
}
