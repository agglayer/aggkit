package storage

import (
	"path"
	"testing"

	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/stretchr/testify/require"
)

func TestStorage_GetBlock(t *testing.T) {
	storage := newStorageForTest(t)
	// BlockBase not present
	blockBase, err := storage.GetBlockBaseByNumber(nil, 1234)
	require.NoError(t, err, "cannot get BlockBase")
	require.Nil(t, blockBase, "expected nil BlockBase")
	// BlockBase not present
	blockHeader, err := storage.GetBlockHeaderByNumber(nil, 1234)
	require.NoError(t, err, "cannot get BlockHeader")
	require.Nil(t, blockHeader, "expected nil BlockHeader")
	// Insert BlockBase
	newBlockBase := aggkittypes.NewBlockBase(1234, [32]byte{0x12}, 5678)
	err = storage.SaveBlockBase(nil, newBlockBase, true)
	require.NoError(t, err, "cannot insert BlockBase")
	// Get BlockBase
	blockBase, err = storage.GetBlockBaseByNumber(nil, newBlockBase.Number)
	require.NoError(t, err, "cannot get BlockBase")
	require.NotNil(t, blockBase, "expected non-nil BlockBase")
	require.Equal(t, newBlockBase, blockBase, "BlockBase mismatch")
}

func TestStorage_GetLogs(t *testing.T) {
	storage := newStorageForTest(t)
	// Logs not present
	_, err := storage.GetEthLogs(nil, aggkittypes.NewLogQuery(1000, 2000, nil, nil))
	require.Error(t, err, "no logs for this range")
}

func newStorageForTest(t *testing.T) *MdrSQLStorage {
	logger := log.WithFields("module", "test")
	path := path.Join(t.TempDir(), "multidownloader_Storage.sqlite")
	cfg := MultidownloaderStorageConfig{
		DBPath: path,
	}

	storage, err := NewMdrSQLStorage(logger, cfg)
	require.NoError(t, err, "cannot create storage")
	return storage
}
