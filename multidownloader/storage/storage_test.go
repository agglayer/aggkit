package storage

import (
	"encoding/json"
	"path"
	"testing"

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

func TestStorage_LogQuery(t *testing.T) {
	t.Run("returns empty response when no logs exist", func(t *testing.T) {
		storage := newStorageForTest(t, nil)
		query := mdrtypes.NewLogQuery(1000, 2000, []common.Address{exampleAddr1})

		response, err := storage.LogQuery(nil, query)

		require.NoError(t, err)
		require.Empty(t, response.Blocks)
		require.Equal(t, query.BlockRange, response.ResponseRange)
	})

	t.Run("returns logs grouped by blocks with correct ordering", func(t *testing.T) {
		storage := newStorageForTest(t, nil)
		tx, err := storage.NewTx(t.Context())
		require.NoError(t, err)

		// Create block headers
		blockHeaders := []*aggkittypes.BlockHeader{
			aggkittypes.NewBlockHeader(1000, exampleTestHash[0], 1630000000, nil),
			aggkittypes.NewBlockHeader(1001, exampleTestHash[1], 1630000060, &exampleTestHash[0]),
			aggkittypes.NewBlockHeader(1002, exampleTestHash[2], 1630000120, &exampleTestHash[1]),
		}

		// Create logs - multiple logs per block and across different blocks
		logs := []types.Log{
			{
				Address:        exampleAddr1,
				BlockNumber:    1000,
				BlockHash:      exampleTestHash[0],
				BlockTimestamp: 1630000000,
				Topics:         []common.Hash{exampleTestHash[3]},
				Data:           []byte{0x01},
				TxHash:         exampleTestHash[5],
				TxIndex:        0,
				Index:          0,
			},
			{
				Address:        exampleAddr1,
				BlockNumber:    1000,
				BlockHash:      exampleTestHash[0],
				BlockTimestamp: 1630000000,
				Topics:         []common.Hash{exampleTestHash[4]},
				Data:           []byte{0x02},
				TxHash:         exampleTestHash[5],
				TxIndex:        1,
				Index:          1,
			},
			{
				Address:        exampleAddr2,
				BlockNumber:    1001,
				BlockHash:      exampleTestHash[1],
				BlockTimestamp: 1630000060,
				Topics:         []common.Hash{exampleTestHash[6]},
				Data:           []byte{0x03},
				TxHash:         exampleTestHash[7],
				TxIndex:        0,
				Index:          0,
			},
			{
				Address:        exampleAddr1,
				BlockNumber:    1002,
				BlockHash:      exampleTestHash[2],
				BlockTimestamp: 1630000120,
				Topics:         []common.Hash{exampleTestHash[8]},
				Data:           []byte{0x04},
				TxHash:         exampleTestHash[9],
				TxIndex:        0,
				Index:          0,
			},
		}

		err = storage.SaveEthLogsWithHeaders(tx, blockHeaders, logs, true)
		require.NoError(t, err)
		err = tx.Commit()
		require.NoError(t, err)

		// Query for logs from both addresses
		query := mdrtypes.NewLogQuery(1000, 1002, []common.Address{exampleAddr1, exampleAddr2})
		response, err := storage.LogQuery(nil, query)

		require.NoError(t, err)
		require.Equal(t, query.BlockRange, response.ResponseRange)
		require.Len(t, response.Blocks, 3, "expected 3 blocks")

		// Verify first block (block 1000) - has 2 logs from exampleAddr1
		require.Equal(t, uint64(1000), response.Blocks[0].Header.Number)
		require.Equal(t, exampleTestHash[0], response.Blocks[0].Header.Hash)
		require.Equal(t, uint64(1630000000), response.Blocks[0].Header.Time)
		require.True(t, response.Blocks[0].IsFinal)
		require.Len(t, response.Blocks[0].Logs, 2)
		require.Equal(t, exampleAddr1, response.Blocks[0].Logs[0].Address)
		require.Equal(t, uint(0), response.Blocks[0].Logs[0].Index)
		require.Equal(t, exampleAddr1, response.Blocks[0].Logs[1].Address)
		require.Equal(t, uint(1), response.Blocks[0].Logs[1].Index)

		// Verify second block (block 1001) - has 1 log from exampleAddr2
		require.Equal(t, uint64(1001), response.Blocks[1].Header.Number)
		require.Equal(t, exampleTestHash[1], response.Blocks[1].Header.Hash)
		require.True(t, response.Blocks[1].IsFinal)
		require.Len(t, response.Blocks[1].Logs, 1)
		require.Equal(t, exampleAddr2, response.Blocks[1].Logs[0].Address)

		// Verify third block (block 1002) - has 1 log from exampleAddr1
		require.Equal(t, uint64(1002), response.Blocks[2].Header.Number)
		require.Equal(t, exampleTestHash[2], response.Blocks[2].Header.Hash)
		require.True(t, response.Blocks[2].IsFinal)
		require.Len(t, response.Blocks[2].Logs, 1)
		require.Equal(t, exampleAddr1, response.Blocks[2].Logs[0].Address)
	})

	t.Run("filters logs by single address", func(t *testing.T) {
		storage := newStorageForTest(t, nil)
		tx, err := storage.NewTx(t.Context())
		require.NoError(t, err)

		blockHeaders := []*aggkittypes.BlockHeader{
			aggkittypes.NewBlockHeader(2000, exampleTestHash[0], 1630001000, nil),
		}

		logs := []types.Log{
			{
				Address:        exampleAddr1,
				BlockNumber:    2000,
				BlockHash:      exampleTestHash[0],
				BlockTimestamp: 1630001000,
				Topics:         []common.Hash{exampleTestHash[1]},
				Data:           []byte{0xAA},
				Index:          0,
			},
			{
				Address:        exampleAddr2,
				BlockNumber:    2000,
				BlockHash:      exampleTestHash[0],
				BlockTimestamp: 1630001000,
				Topics:         []common.Hash{exampleTestHash[2]},
				Data:           []byte{0xBB},
				Index:          1,
			},
		}

		err = storage.SaveEthLogsWithHeaders(tx, blockHeaders, logs, false)
		require.NoError(t, err)
		err = tx.Commit()
		require.NoError(t, err)

		// Query only for exampleAddr1
		query := mdrtypes.NewLogQuery(2000, 2000, []common.Address{exampleAddr1})
		response, err := storage.LogQuery(nil, query)

		require.NoError(t, err)
		require.Len(t, response.Blocks, 1)
		require.Len(t, response.Blocks[0].Logs, 1)
		require.Equal(t, exampleAddr1, response.Blocks[0].Logs[0].Address)
		require.Equal(t, []byte{0xAA}, response.Blocks[0].Logs[0].Data)
		require.False(t, response.Blocks[0].IsFinal, "expected block to not be final")
	})

	t.Run("respects block range boundaries", func(t *testing.T) {
		storage := newStorageForTest(t, nil)
		tx, err := storage.NewTx(t.Context())
		require.NoError(t, err)

		blockHeaders := []*aggkittypes.BlockHeader{
			aggkittypes.NewBlockHeader(3000, exampleTestHash[0], 1630002000, nil),
			aggkittypes.NewBlockHeader(3001, exampleTestHash[1], 1630002060, &exampleTestHash[0]),
			aggkittypes.NewBlockHeader(3002, exampleTestHash[2], 1630002120, &exampleTestHash[1]),
		}

		logs := []types.Log{
			{
				Address:        exampleAddr1,
				BlockNumber:    3000,
				BlockHash:      exampleTestHash[0],
				BlockTimestamp: 1630002000,
				Topics:         []common.Hash{},
				Index:          0,
			},
			{
				Address:        exampleAddr1,
				BlockNumber:    3001,
				BlockHash:      exampleTestHash[1],
				BlockTimestamp: 1630002060,
				Topics:         []common.Hash{},
				Index:          0,
			},
			{
				Address:        exampleAddr1,
				BlockNumber:    3002,
				BlockHash:      exampleTestHash[2],
				BlockTimestamp: 1630002120,
				Topics:         []common.Hash{},
				Index:          0,
			},
		}

		err = storage.SaveEthLogsWithHeaders(tx, blockHeaders, logs, true)
		require.NoError(t, err)
		err = tx.Commit()
		require.NoError(t, err)

		// Query for middle block only
		query := mdrtypes.NewLogQuery(3001, 3001, []common.Address{exampleAddr1})
		response, err := storage.LogQuery(nil, query)

		require.NoError(t, err)
		require.Len(t, response.Blocks, 1, "expected only 1 block in range")
		require.Equal(t, uint64(3001), response.Blocks[0].Header.Number)
	})

	t.Run("preserves log field values correctly", func(t *testing.T) {
		storage := newStorageForTest(t, nil)
		tx, err := storage.NewTx(t.Context())
		require.NoError(t, err)

		parentHash := exampleTestHash[9]
		blockHeaders := []*aggkittypes.BlockHeader{
			aggkittypes.NewBlockHeader(4000, exampleTestHash[0], 1630003000, &parentHash),
		}

		expectedTopics := []common.Hash{exampleTestHash[1], exampleTestHash[2], exampleTestHash[3]}
		expectedData := []byte{0xDE, 0xAD, 0xBE, 0xEF}
		expectedTxHash := exampleTestHash[5]

		logs := []types.Log{
			{
				Address:        exampleAddr1,
				BlockNumber:    4000,
				BlockHash:      exampleTestHash[0],
				BlockTimestamp: 1630003000,
				Topics:         expectedTopics,
				Data:           expectedData,
				TxHash:         expectedTxHash,
				TxIndex:        42,
				Index:          7,
			},
		}

		err = storage.SaveEthLogsWithHeaders(tx, blockHeaders, logs, true)
		require.NoError(t, err)
		err = tx.Commit()
		require.NoError(t, err)

		query := mdrtypes.NewLogQuery(4000, 4000, []common.Address{exampleAddr1})
		response, err := storage.LogQuery(nil, query)

		require.NoError(t, err)
		require.Len(t, response.Blocks, 1)
		require.Len(t, response.Blocks[0].Logs, 1)

		log := response.Blocks[0].Logs[0]
		require.Equal(t, exampleAddr1, log.Address)
		require.Equal(t, expectedTopics, log.Topics)
		require.Equal(t, expectedData, log.Data)
		require.Equal(t, expectedTxHash, log.TxHash)
		require.Equal(t, uint(42), log.TxIndex)
		require.Equal(t, uint(7), log.Index)
		require.Equal(t, uint64(4000), log.BlockNumber)
		require.Equal(t, uint64(1630003000), log.BlockTimestamp)
		require.False(t, log.Removed)

		// Verify block header fields
		header := response.Blocks[0].Header
		require.Equal(t, uint64(4000), header.Number)
		require.Equal(t, exampleTestHash[0], header.Hash)
		require.Equal(t, uint64(1630003000), header.Time)
		require.NotNil(t, header.ParentHash)
		require.Equal(t, parentHash, *header.ParentHash)
	})
}

func TestStorage_UpdateIsFinal(t *testing.T) {
	storage := newStorageForTest(t, nil)
	block := aggkittypes.NewBlockHeader(4000, exampleTestHash[5], 1630002000, nil)
	err := storage.saveAggkitBlock(nil, block, false)
	require.NoError(t, err, "cannot insert BlockHeader")

	readBlock, isFinal, err := storage.GetBlockHeaderByNumber(nil, block.Number)
	require.NoError(t, err, "cannot get BlockHeader")
	require.NotNil(t, readBlock, "expected non-nil BlockHeader")
	require.Equal(t, block, readBlock, "BlockHeader mismatch")
	require.False(t, isFinal, "expected block to not be final")

	err = storage.UpdateBlockToFinalized(nil, []uint64{})
	require.NoError(t, err, "if no blocks provided, should be no-op")

	err = storage.UpdateBlockToFinalized(nil, []uint64{block.Number})
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
	row := blockRow{
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

func populateLogsAndBlocksForTest(t *testing.T, storage *MultidownloaderStorage,
	startingBlock uint64, numBlocks int, logsPerBlock int) {
	t.Helper()
	var blocks []*aggkittypes.BlockHeader
	var logs []types.Log
	for i := 0; i < numBlocks; i++ {
		blockNumber := startingBlock + uint64(i)
		blockHash := exampleTestHash[i%len(exampleTestHash)]
		var parentHash *common.Hash
		if i > 0 {
			parentHash = &exampleTestHash[(i-1)%len(exampleTestHash)]
		}
		block := aggkittypes.NewBlockHeader(blockNumber, blockHash, 1630000000+uint64(i*60), parentHash)
		blocks = append(blocks, block)

		for j := 0; j < logsPerBlock; j++ {
			logEntry := types.Log{
				Address:        exampleAddr1,
				BlockNumber:    blockNumber,
				BlockHash:      blockHash,
				BlockTimestamp: 1630000000 + uint64(i*60),
				Topics: []common.Hash{
					exampleTestHash[j%len(exampleTestHash)],
				},
				Data:    []byte{0x01, 0x02, byte(j)},
				TxHash:  exampleTestHash[(i+j)%len(exampleTestHash)],
				TxIndex: uint(100 + j),
				Index:   uint(10 + j),
			}
			logs = append(logs, logEntry)
		}
	}

	err := storage.SaveEthLogsWithHeaders(nil, blocks, logs, true)
	require.NoError(t, err, "cannot populate logs and blocks")
}

func TestNewLogRowFromEthLog(t *testing.T) {
	ethLog := types.Log{
		Address:        exampleAddr1,
		BlockNumber:    1234,
		BlockHash:      exampleTestHash[0],
		BlockTimestamp: 1630000000,
		Topics: []common.Hash{
			exampleTestHash[1],
			exampleTestHash[2],
		},
		Data:    []byte{0xDE, 0xAD, 0xBE, 0xEF},
		TxHash:  exampleTestHash[3],
		TxIndex: 42,
		Index:   7,
	}

	row := NewLogRowFromEthLog(ethLog)

	require.NotNil(t, row)
	require.Equal(t, ethLog.Address, row.Address)
	require.Equal(t, ethLog.BlockNumber, row.BlockNumber)
	require.Equal(t, ethLog.Data, row.Data)
	require.Equal(t, ethLog.TxHash, row.TxHash)
	require.Equal(t, ethLog.TxIndex, row.TxIndex)
	require.Equal(t, ethLog.Index, row.Index)

	// Verify topics are correctly marshaled as JSON
	var topics []common.Hash
	err := json.Unmarshal([]byte(row.Topics), &topics)
	require.NoError(t, err)
	require.Equal(t, ethLog.Topics, topics)
}

func TestNewLogRowsFromEthLogs(t *testing.T) {
	ethLogs := []types.Log{
		{
			Address:        exampleAddr1,
			BlockNumber:    1000,
			BlockHash:      exampleTestHash[0],
			BlockTimestamp: 1630000000,
			Topics:         []common.Hash{exampleTestHash[1]},
			Data:           []byte{0x01},
			TxHash:         exampleTestHash[2],
			TxIndex:        10,
			Index:          0,
		},
		{
			Address:        exampleAddr2,
			BlockNumber:    1001,
			BlockHash:      exampleTestHash[1],
			BlockTimestamp: 1630000060,
			Topics:         []common.Hash{exampleTestHash[3], exampleTestHash[4]},
			Data:           []byte{0x02, 0x03},
			TxHash:         exampleTestHash[5],
			TxIndex:        20,
			Index:          1,
		},
	}

	rows := NewLogRowsFromEthLogs(ethLogs)

	require.Len(t, rows, 2)
	require.Equal(t, ethLogs[0].Address, rows[0].Address)
	require.Equal(t, ethLogs[0].BlockNumber, rows[0].BlockNumber)
	require.Equal(t, ethLogs[1].Address, rows[1].Address)
	require.Equal(t, ethLogs[1].BlockNumber, rows[1].BlockNumber)
}

func TestNewBlockRowFromEthLog(t *testing.T) {
	ethLog := types.Log{
		Address:        exampleAddr1,
		BlockNumber:    5000,
		BlockHash:      exampleTestHash[0],
		BlockTimestamp: 1630002000,
		Topics:         []common.Hash{exampleTestHash[1]},
		Data:           []byte{0x01},
	}

	row := NewBlockRowFromEthLog(ethLog, true)

	require.NotNil(t, row)
	require.Equal(t, ethLog.BlockNumber, row.BlockNumber)
	require.Equal(t, ethLog.BlockHash, row.BlockHash)
	require.Equal(t, ethLog.BlockTimestamp, row.BlockTimestamp)
	require.Nil(t, row.BlockParentHash)
	require.True(t, row.IsFinal)

	rowNotFinal := NewBlockRowFromEthLog(ethLog, false)
	require.False(t, rowNotFinal.IsFinal)
}

func TestNewBlockRowFromAggkitBlock(t *testing.T) {
	parentHash := exampleTestHash[0]
	block := aggkittypes.NewBlockHeader(3000, exampleTestHash[1], 1630003000, &parentHash)

	row := newBlockRowFromAggkitBlock(block, true)

	require.NotNil(t, row)
	require.Equal(t, block.Number, row.BlockNumber)
	require.Equal(t, block.Hash, row.BlockHash)
	require.Equal(t, block.Time, row.BlockTimestamp)
	require.NotNil(t, row.BlockParentHash)
	require.Equal(t, parentHash, *row.BlockParentHash)
	require.True(t, row.IsFinal)
}

func TestNewBlockRowsFromLogs(t *testing.T) {
	logs := []types.Log{
		{
			Address:        exampleAddr1,
			BlockNumber:    1000,
			BlockHash:      exampleTestHash[0],
			BlockTimestamp: 1630000000,
			Topics:         []common.Hash{exampleTestHash[1]},
			Data:           []byte{0x01},
		},
		{
			Address:        exampleAddr1,
			BlockNumber:    1000,
			BlockHash:      exampleTestHash[0],
			BlockTimestamp: 1630000000,
			Topics:         []common.Hash{exampleTestHash[2]},
			Data:           []byte{0x02},
		},
		{
			Address:        exampleAddr2,
			BlockNumber:    1001,
			BlockHash:      exampleTestHash[1],
			BlockTimestamp: 1630000060,
			Topics:         []common.Hash{exampleTestHash[3]},
			Data:           []byte{0x03},
		},
	}

	blockRows := NewBlockRowsFromLogs(logs, true)

	require.Len(t, blockRows, 2, "expected 2 unique blocks")
	require.NotNil(t, blockRows[1000])
	require.Equal(t, uint64(1000), blockRows[1000].BlockNumber)
	require.Equal(t, exampleTestHash[0], blockRows[1000].BlockHash)
	require.True(t, blockRows[1000].IsFinal)
	require.NotNil(t, blockRows[1001])
	require.Equal(t, uint64(1001), blockRows[1001].BlockNumber)
	require.Equal(t, exampleTestHash[1], blockRows[1001].BlockHash)
	require.True(t, blockRows[1001].IsFinal)
}

func TestNewBlockRowsFromAggkitBlock(t *testing.T) {
	parentHash1 := exampleTestHash[0]
	parentHash2 := exampleTestHash[1]
	blockHeaders := aggkittypes.ListBlockHeaders{
		aggkittypes.NewBlockHeader(2000, exampleTestHash[1], 1630001000, &parentHash1),
		aggkittypes.NewBlockHeader(2001, exampleTestHash[2], 1630001060, &parentHash2),
	}

	blockRows := NewBlockRowsFromAggkitBlock(blockHeaders, false)

	require.Len(t, blockRows, 2)
	require.NotNil(t, blockRows[2000])
	require.Equal(t, uint64(2000), blockRows[2000].BlockNumber)
	require.Equal(t, exampleTestHash[1], blockRows[2000].BlockHash)
	require.NotNil(t, blockRows[2000].BlockParentHash)
	require.Equal(t, parentHash1, *blockRows[2000].BlockParentHash)
	require.False(t, blockRows[2000].IsFinal)

	require.NotNil(t, blockRows[2001])
	require.Equal(t, uint64(2001), blockRows[2001].BlockNumber)
	require.NotNil(t, blockRows[2001].BlockParentHash)
	require.Equal(t, parentHash2, *blockRows[2001].BlockParentHash)
	require.False(t, blockRows[2001].IsFinal)
}
