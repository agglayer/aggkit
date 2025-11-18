package storage

import (
	"path"
	"testing"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/log"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

var (
	exampleAddr1    = common.HexToAddress("0x2968d6d736178f8fe7393cc33c87f29d9c287e78")
	exampleAddr2    = common.HexToAddress("0xe2ef6215adc132df6913c8dd16487abf118d1764")
	exampleTestHash = []common.Hash{
		common.HexToHash("0xabcdeffedcba1234567890abcdef1234567890abcdef1234567890abcdef1234"),
		common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcd"),
		common.HexToHash("01f688256b7201998ef9bb4cecf15fda77fce525354f7f1528309316c49c5ca1"),
		common.HexToHash("0ad8434439289755cdf02ab748085de083c57d9c4c81ac3cedf0119fddf5cea1"),
		common.HexToHash("856086411ae73dde934f7ec6f586b73b00d0bde1a9c32c7dfd1811b146cbd8bf"),
		common.HexToHash("ecafdcfc171ad8f67f5e016ab7d59a2f3f632cf5482ceb5ddff19fee044babcd"),
		common.HexToHash("68df1560f320725fbdae9f42afc474c34aef7a20f60699fddfa1ec65be07d7df"),
		common.HexToHash("827886425bd385235eff8468bab33d1b0f1638cbc2e7a98cf44653e8956945c6"),
		common.HexToHash("845bff0149574456a1bb452c475133a6f8c13a3c1c6c71c0848d1df75fd19503"),
		common.HexToHash("f1dc2c2d1ae08a8024f3bfd07025a119b7823c34523bc9f66da8729f9bdf1500"),
		common.HexToHash("d71e92ca50ea43b367b18ed2f755e3f72f2ae60bdaf18d3d3d073f5b9994a6e8"),
	}
)

func TestStorage_Exploratory(t *testing.T) {
	t.Skip("exploratory test, not a real unit test")
	dbFile := "/tmp/mdr_test.sqlite"
	storage := newStorageForTest(t, &dbFile)
	logs, err := storage.GetEthLogs(nil, mdrtypes.NewLogQuery(5157574, 5157574+2000, []common.Address{exampleAddr2}))
	require.NoError(t, err)
	log.Infof("Retrieved %d logs", len(logs))
	for i, lg := range logs {
		log.Infof("Log %d: %+v", i, lg)
	}
	block, isFinal, err := storage.GetBlockHeaderByNumber(nil, 5157912)
	require.NoError(t, err)
	require.NotNil(t, block, "expected non-nil block")
	require.True(t, isFinal, "expected block to be final")
	log.Infof("Retrieved block: %+v", block)
}

func TestStorage_GetBlock(t *testing.T) {
	storage := newStorageForTest(t, nil)
	// BlockBase not present
	blockHeader, _, err := storage.GetBlockHeaderByNumber(nil, 1234)
	require.NoError(t, err, "cannot get BlockHeader")
	require.Nil(t, blockHeader, "expected nil BlockHeader")
	block := aggkittypes.NewBlockHeader(1234, exampleTestHash[0], 5678, &exampleTestHash[1])
	err = storage.SaveBlockAggkitBlock(nil, block, true)
	require.NoError(t, err, "cannot insert BlockHeader")
	// Get and verify block
	readBlock, isFinal, err := storage.GetBlockHeaderByNumber(nil, 1234)
	require.NoError(t, err, "cannot get BlockHeader")
	require.NotNil(t, readBlock, "expected non-nil BlockHeader")
	require.Equal(t, block, readBlock, "BlockHeader mismatch")
	require.True(t, isFinal, "expected block to be final")

	blockNilParentHash := aggkittypes.NewBlockHeader(1235, exampleTestHash[0], 5678, nil)
	err = storage.SaveBlockAggkitBlock(nil, blockNilParentHash, true)
	require.NoError(t, err, "cannot get BlockHeader")
	readBlock, _, err = storage.GetBlockHeaderByNumber(nil, blockNilParentHash.Number)
	require.NoError(t, err, "cannot get BlockHeader")
	require.Equal(t, blockNilParentHash, readBlock, "BlockHeader mismatch")
}

func TestStorage_GetLogs(t *testing.T) {
	storage := newStorageForTest(t, nil)
	// Logs not present
	logs, err := storage.GetEthLogs(nil, mdrtypes.NewLogQuery(1000, 2000, []common.Address{exampleAddr1}))
	require.NoError(t, err)
	require.Empty(t, logs, "expected no logs")
	// Insert logs
	logsToInsert := []types.Log{
		{
			Address:        exampleAddr1,
			BlockNumber:    1500,
			BlockHash:      exampleTestHash[0],
			BlockTimestamp: 1630000000,
			Topics: []common.Hash{
				exampleTestHash[0],
			},
			Data:    []byte{0x01, 0x02},
			TxHash:  exampleTestHash[4],
			TxIndex: 123,
			Index:   34,
		},
		{
			Address:        exampleAddr1,
			BlockNumber:    1500,
			BlockHash:      exampleTestHash[0],
			BlockTimestamp: 1630000000,
			Topics: []common.Hash{
				exampleTestHash[0],
			},
			Data:    []byte{0x01, 0x02},
			TxHash:  exampleTestHash[4],
			TxIndex: 124,
			Index:   35,
		},
		{
			Address:     exampleAddr1,
			BlockNumber: 1800,
			BlockHash:   exampleTestHash[1],
			Topics: []common.Hash{
				exampleTestHash[0],
			},
			Data:  []byte{0x03, 0x04},
			Index: 1,
		},
		{
			Address:     exampleAddr2,
			BlockNumber: 1600,
			BlockHash:   exampleTestHash[2],
			Topics: []common.Hash{
				exampleTestHash[0],
			},
			Data:  []byte{0x05, 0x06},
			Index: 0,
		},
	}
	err = storage.SaveEthLogs(nil, logsToInsert, true)
	require.NoError(t, err, "cannot insert logs")
	// Get logs for exampleAddr1
	readLogs, err := storage.GetEthLogs(nil, mdrtypes.NewLogQuery(1000, 2000, []common.Address{exampleAddr1}))
	require.NoError(t, err, "cannot get logs")
	require.Len(t, readLogs, 3, "expected 2 logs for exampleAddr1")
	require.Equal(t, logsToInsert[0], readLogs[0], "log 0 mismatch")
	require.Equal(t, logsToInsert[1], readLogs[1], "log 1 mismatch")
	require.Equal(t, logsToInsert[2], readLogs[2], "log 2 mismatch")
	// Get logs for exampleAddr2
	readLogs, err = storage.GetEthLogs(nil, mdrtypes.NewLogQuery(1000, 2000, []common.Address{exampleAddr2}))
	require.NoError(t, err, "cannot get logs")
	require.Len(t, readLogs, 1, "expected 1 log for exampleAddr2")
	// Get logs for both addresses
	readLogs, err = storage.GetEthLogs(nil, mdrtypes.NewLogQuery(1000, 2000, []common.Address{exampleAddr1, exampleAddr2}))
	require.NoError(t, err, "cannot get logs")
	require.Len(t, readLogs, 4, "expected 3 logs for both addresses")
}

func TestStorage_SaveEthLogsWithHeaders(t *testing.T) {
	storage := newStorageForTest(t, nil)
	tx, err := storage.NewTx(t.Context())
	require.NoError(t, err)
	blockHeaders := []*aggkittypes.BlockHeader{
		aggkittypes.NewBlockHeader(2000, exampleTestHash[3], 1630001000, nil),
		aggkittypes.NewBlockHeader(2001, exampleTestHash[4], 1630001060, &exampleTestHash[3]),
	}
	logs := []types.Log{
		{
			Address:        exampleAddr1,
			BlockNumber:    2000,
			BlockHash:      exampleTestHash[3],
			BlockTimestamp: 1630001000,
			Topics: []common.Hash{
				exampleTestHash[0],
			},
		},
		{
			Address:        exampleAddr2,
			BlockNumber:    2001,
			BlockHash:      exampleTestHash[4],
			BlockTimestamp: 1630001060,
			Topics: []common.Hash{
				exampleTestHash[1],
			},
		},
	}
	err = storage.SaveEthLogsWithHeaders(tx,
		blockHeaders,
		logs,
		true)
	require.NoError(t, err)
	err = tx.Commit()
	require.NoError(t, err)

	block1, _, err := storage.GetBlockHeaderByNumber(nil, blockHeaders[0].Number)
	require.NoError(t, err)
	require.Equal(t, blockHeaders[0], block1)

	block2, _, err := storage.GetBlockHeaderByNumber(nil, blockHeaders[1].Number)
	require.NoError(t, err)
	require.Equal(t, blockHeaders[1], block2)

	readLogs, err := storage.GetEthLogs(nil, mdrtypes.NewLogQuery(2000, 2001, []common.Address{exampleAddr1, exampleAddr2}))
	require.NoError(t, err)
	require.Len(t, readLogs, 2)
	require.Equal(t, logs[0], readLogs[0])
	require.Equal(t, logs[1], readLogs[1])
}

func TestStorage_GetSyncedBlockRangePerContract(t *testing.T) {
	storage := newStorageForTest(t, nil)
	data, err := storage.GetSyncedBlockRangePerContract(nil)
	require.NoError(t, err)
	require.Equal(t, "SetSyncSegment: ", data.String())
}

func TestStorage_UpsertSyncerConfigs(t *testing.T) {
	storage := newStorageForTest(t, nil)
	configs := []mdrtypes.ContractConfig{
		{
			Address:   exampleAddr1,
			FromBlock: 1000,
			ToBlock:   aggkittypes.FinalizedBlock,
		},
		{
			Address:   exampleAddr2,
			FromBlock: 2000,
			ToBlock:   aggkittypes.LatestBlock,
		},
	}
	err := storage.UpsertSyncerConfigs(nil, configs)
	require.NoError(t, err)

	// Upsert again with different start block
	configsUpdated := []mdrtypes.ContractConfig{
		{
			Address:   exampleAddr1,
			FromBlock: 1300,
			ToBlock:   aggkittypes.FinalizedBlock,
		},
		{
			Address:   exampleAddr2,
			FromBlock: 1600,
			ToBlock:   aggkittypes.FinalizedBlock,
		},
	}
	err = storage.UpsertSyncerConfigs(nil, configsUpdated)
	require.NoError(t, err)

	syncSegments, err := storage.GetSyncedBlockRangePerContract(nil)
	require.NoError(t, err)
	require.Equal(t, 2, len(syncSegments.GetAddressesForBlockRange(
		aggkitcommon.NewBlockRange(0, 10000),
	)))
	seg1 := syncSegments.GetByContract(exampleAddr1)
	require.NotNil(t, seg1)
	require.Equal(t, aggkittypes.FinalizedBlock, seg1.TargetToBlock)

	seg2 := syncSegments.GetByContract(exampleAddr2)
	require.NotNil(t, seg2)
	require.Equal(t, aggkittypes.FinalizedBlock, seg2.TargetToBlock)
}

func TestStorage_UpdateSyncingStatus_ErrorNoUpdate(t *testing.T) {
	storage := newStorageForTest(t, nil)
	logQuery := &mdrtypes.LogQuery{
		Addrs:      []common.Address{exampleAddr1},
		BlockRange: aggkitcommon.NewBlockRange(1000, 2000),
	}
	err := storage.UpdateSyncingStatus(nil, logQuery)
	require.Error(t, err)
}

func TestStorage_UpdateSyncingStatusUpdate(t *testing.T) {
	storage := newStorageForTest(t, nil)
	configs := []mdrtypes.ContractConfig{
		{
			Address:   exampleAddr1,
			FromBlock: 1100,
			ToBlock:   aggkittypes.FinalizedBlock,
		},
		{
			Address:   exampleAddr2,
			FromBlock: 3000,
			ToBlock:   aggkittypes.LatestBlock,
		},
	}
	err := storage.UpsertSyncerConfigs(nil, configs)
	require.NoError(t, err)

	logQuery := &mdrtypes.LogQuery{
		Addrs:      []common.Address{exampleAddr1},
		BlockRange: aggkitcommon.NewBlockRange(1000, 2000),
	}
	err = storage.UpdateSyncingStatus(nil, logQuery)
	require.NoError(t, err)

	logQuery = &mdrtypes.LogQuery{
		Addrs:      []common.Address{exampleAddr1},
		BlockRange: aggkitcommon.NewBlockRange(800, 1300),
	}
	err = storage.UpdateSyncingStatus(nil, logQuery)
	require.NoError(t, err)

	syncSegments, err := storage.GetSyncedBlockRangePerContract(nil)
	require.NoError(t, err)
	seg1 := syncSegments.GetByContract(exampleAddr1)
	require.Equal(t, "SyncSegment{ contracts:0x2968D6d736178f8FE7393CC33C87f29D9C287e78 range:From: 800, To: 2000 (1201) blockHeader:false}",
		seg1.String())
}

func TestStorage_UpdateIsFinal(t *testing.T) {
	storage := newStorageForTest(t, nil)
	block := aggkittypes.NewBlockHeader(4000, exampleTestHash[5], 1630002000, nil)
	err := storage.SaveBlockAggkitBlock(nil, block, false)
	require.NoError(t, err, "cannot insert BlockHeader")

	readBlock, isFinal, err := storage.GetBlockHeaderByNumber(nil, block.Number)
	require.NoError(t, err, "cannot get BlockHeader")
	require.NotNil(t, readBlock, "expected non-nil BlockHeader")
	require.Equal(t, block, readBlock, "BlockHeader mismatch")
	require.False(t, isFinal, "expected block to not be final")

	err = storage.UpdateIsFinal(nil, []uint64{block.Number})
	require.NoError(t, err, "cannot update IsFinal")

	readBlock, isFinal, err = storage.GetBlockHeaderByNumber(nil, block.Number)
	require.NoError(t, err, "cannot get BlockHeader")
	require.NotNil(t, readBlock, "expected non-nil BlockHeader")
	require.Equal(t, block, readBlock, "BlockHeader mismatch")
	require.True(t, isFinal, "expected block to be final")
}

func TestStorage_logRow_String(t *testing.T) {
	row := logRow{
		Address:     exampleAddr1,
		BlockNumber: 1500,
		Topics:      "",
		Data:        []byte{0x01, 0x02},
		TxHash:      exampleTestHash[4],
		TxIndex:     123,
		Index:       34,
	}
	str := row.String()
	require.Equal(t, "logRow{Address: 0x2968D6d736178f8FE7393CC33C87f29D9C287e78, "+
		"Topics: , DataLen: 2, BlockNumber: 1500, "+
		"TxHash: 0x856086411ae73dde934f7ec6f586b73b00d0bde1a9c32c7dfd1811b146cbd8bf, "+
		"TxIndex: 123, Index: 34}", str)

	var rowNil *logRow
	require.Equal(t, "logRow{nil}", rowNil.String())
}

func TestStorage_BlockRow_String(t *testing.T) {
	row := BlockRow{
		BlockNumber:     1234,
		BlockHash:       exampleTestHash[0],
		BlockTimestamp:  5678,
		BlockParentHash: &exampleTestHash[1],
		IsFinal:         true,
	}
	str := row.String()
	require.Equal(t, "BlockRow{BlockNumber: 1234, "+
		"BlockHash: 0xabcdeffedcba1234567890abcdef1234567890abcdef1234567890abcdef1234, BlockTimestamp: 5678, "+
		"BlockParentHash: 0x001234567890abcdef1234567890abcdef1234567890abcdef1234567890abcd, IsFinal: true}", str)
}

func newStorageForTest(t *testing.T, dbFileFullPath *string) *MultidownloaderStorage {
	t.Helper()
	logger := log.WithFields("module", "test")
	var dbPath string
	if dbFileFullPath == nil {
		dbPath = path.Join(t.TempDir(), "multidownloader_Storage.sqlite")
	} else {
		dbPath = *dbFileFullPath
	}
	cfg := MultidownloaderStorageConfig{
		DBPath: dbPath,
	}

	storage, err := NewMultidownloaderStorage(logger, cfg)
	require.NoError(t, err, "cannot create storage")
	return storage
}
