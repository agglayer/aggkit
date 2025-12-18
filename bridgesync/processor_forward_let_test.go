package bridgesync

import (
	"fmt"
	"math/big"
	"path/filepath"
	"testing"

	aggkitabi "github.com/agglayer/aggkit/abi"
	"github.com/agglayer/aggkit/bridgesync/migrations"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db"
	dbtypes "github.com/agglayer/aggkit/db/types"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/russross/meddler"
	"github.com/stretchr/testify/require"
)

func TestHandleForwardLETEvent(t *testing.T) {
	t.Run("successfully process single leaf with no archived bridge", func(t *testing.T) {
		p, tx := setupProcessorWithTransaction(t)
		defer tx.Rollback() //nolint:errcheck

		// Setup: Insert initial leaves to establish previous root (indices 0-4)
		var initialRoot common.Hash
		var err error
		// Insert block rows for initial leaves
		for i := uint32(0); i <= 4; i++ {
			_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, 10+uint64(i))
			require.NoError(t, err)
		}
		for i := uint32(0); i <= 4; i++ {
			leaf := types.Leaf{Index: i, Hash: common.HexToHash(fmt.Sprintf("0x%d", i))}
			initialRoot, err = p.exitTree.PutLeaf(tx, 10+uint64(i), 0, leaf)
			require.NoError(t, err)
		}
		initialDepositCount := uint32(4) // Last index inserted

		// Insert block for the ForwardLET event
		_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, uint64(100))
		require.NoError(t, err)

		// Create forward LET event with one leaf
		leaves := []LeafData{
			{
				LeafType:           1,
				OriginNetwork:      5,
				OriginAddress:      common.HexToAddress("0x1234567890123456789012345678901234567890"),
				DestinationNetwork: 10,
				DestinationAddress: common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"),
				Amount:             big.NewInt(1000000),
				Metadata:           []byte("test metadata"),
			},
		}
		encodedLeaves := encodeLeafDataArrayForTest(t, leaves)

		event := &ForwardLET{
			BlockNum:             100,
			BlockPos:             5,
			BlockTimestamp:       1234567890,
			TxnHash:              common.HexToHash("0xabc123"),
			PreviousDepositCount: big.NewInt(int64(initialDepositCount)),
			PreviousRoot:         initialRoot,
			NewDepositCount:      big.NewInt(int64(initialDepositCount + 1)),
			NewLeaves:            encodedLeaves,
		}

		// Calculate the expected root that will result from processing these leaves
		event.NewRoot = calculateExpectedRootAfterForwardLET(t, initialDepositCount, leaves, event)

		// Test: Process the forward LET event
		blockPos := event.BlockPos
		newBlockPos, err := p.handleForwardLETEvent(tx, event, &blockPos)
		require.NoError(t, err)
		require.Equal(t, event.BlockPos+1, newBlockPos)

		// Verify: Bridge was inserted
		var bridges []*Bridge
		err = meddler.QueryAll(tx, &bridges, "SELECT * FROM bridge WHERE block_num = $1", event.BlockNum)
		require.NoError(t, err)
		require.Len(t, bridges, 1)

		bridge := bridges[0]
		require.Equal(t, event.BlockNum, bridge.BlockNum)
		require.Equal(t, event.BlockPos, bridge.BlockPos)
		require.Equal(t, leaves[0].LeafType, bridge.LeafType)
		require.Equal(t, leaves[0].OriginNetwork, bridge.OriginNetwork)
		require.Equal(t, leaves[0].OriginAddress, bridge.OriginAddress)
		require.Equal(t, leaves[0].DestinationNetwork, bridge.DestinationNetwork)
		require.Equal(t, leaves[0].DestinationAddress, bridge.DestinationAddress)
		require.Equal(t, 0, leaves[0].Amount.Cmp(bridge.Amount))
		require.Equal(t, leaves[0].Metadata, bridge.Metadata)
		require.Equal(t, initialDepositCount+1, bridge.DepositCount)
		require.Equal(t, event.TxnHash, bridge.TxHash)
		require.Equal(t, aggkitcommon.ZeroAddress, bridge.TxnSender)
		require.Equal(t, aggkitcommon.ZeroAddress, bridge.FromAddress)
		require.Equal(t, BridgeSourceForwardLET, bridge.Source)

		// Verify: ForwardLET event was inserted
		var forwardLETs []*ForwardLET
		err = meddler.QueryAll(tx, &forwardLETs, "SELECT * FROM forward_let WHERE block_num = $1", event.BlockNum)
		require.NoError(t, err)
		require.Len(t, forwardLETs, 1)
		require.Equal(t, event.BlockNum, forwardLETs[0].BlockNum)
	})

	t.Run("successfully process multiple leaves", func(t *testing.T) {
		p, tx := setupProcessorWithTransaction(t)
		defer tx.Rollback() //nolint:errcheck

		// Setup: Insert initial leaves (indices 0-9)
		var initialRoot common.Hash
		var err error
		// Insert block rows for initial leaves
		for i := uint32(0); i <= 9; i++ {
			_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, 20+uint64(i))
			require.NoError(t, err)
		}
		for i := uint32(0); i <= 9; i++ {
			leaf := types.Leaf{Index: i, Hash: common.HexToHash(fmt.Sprintf("0x%d", i))}
			initialRoot, err = p.exitTree.PutLeaf(tx, 20+uint64(i), 0, leaf)
			require.NoError(t, err)
		}
		initialDepositCount := uint32(9) // Last index inserted

		// Insert block for the ForwardLET event
		_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, uint64(200))
		require.NoError(t, err)

		// Create forward LET event with three leaves
		leaves := []LeafData{
			{
				LeafType:           0,
				OriginNetwork:      1,
				OriginAddress:      common.HexToAddress("0x1111111111111111111111111111111111111111"),
				DestinationNetwork: 2,
				DestinationAddress: common.HexToAddress("0x2222222222222222222222222222222222222222"),
				Amount:             big.NewInt(100),
				Metadata:           []byte("first"),
			},
			{
				LeafType:           1,
				OriginNetwork:      3,
				OriginAddress:      common.HexToAddress("0x3333333333333333333333333333333333333333"),
				DestinationNetwork: 4,
				DestinationAddress: common.HexToAddress("0x4444444444444444444444444444444444444444"),
				Amount:             big.NewInt(200),
				Metadata:           []byte("second"),
			},
			{
				LeafType:           2,
				OriginNetwork:      5,
				OriginAddress:      common.HexToAddress("0x5555555555555555555555555555555555555555"),
				DestinationNetwork: 6,
				DestinationAddress: common.HexToAddress("0x6666666666666666666666666666666666666666"),
				Amount:             big.NewInt(300),
				Metadata:           []byte("third"),
			},
		}
		encodedLeaves := encodeLeafDataArrayForTest(t, leaves)

		event := &ForwardLET{
			BlockNum:             200,
			BlockPos:             10,
			BlockTimestamp:       1234567900,
			TxnHash:              common.HexToHash("0xdef456"),
			PreviousDepositCount: big.NewInt(int64(initialDepositCount)),
			PreviousRoot:         initialRoot,
			NewDepositCount:      big.NewInt(int64(initialDepositCount + uint32(len(leaves)))),
			NewLeaves:            encodedLeaves,
		}

		// Calculate the expected root that will result from processing these leaves
		event.NewRoot = calculateExpectedRootAfterForwardLET(t, initialDepositCount, leaves, event)

		// Test: Process the forward LET event
		blockPos := event.BlockPos
		newBlockPos, err := p.handleForwardLETEvent(tx, event, &blockPos)
		require.NoError(t, err)
		require.Equal(t, event.BlockPos+uint64(len(leaves)), newBlockPos)

		// Verify: All bridges were inserted
		var bridges []*Bridge
		err = meddler.QueryAll(tx, &bridges, "SELECT * FROM bridge WHERE block_num = $1 ORDER BY block_pos", event.BlockNum)
		require.NoError(t, err)
		require.Len(t, bridges, 3)

		// Verify each bridge
		for i, bridge := range bridges {
			require.Equal(t, event.BlockNum, bridge.BlockNum)
			require.Equal(t, event.BlockPos+uint64(i), bridge.BlockPos)
			require.Equal(t, leaves[i].LeafType, bridge.LeafType)
			require.Equal(t, leaves[i].OriginNetwork, bridge.OriginNetwork)
			require.Equal(t, initialDepositCount+uint32(i)+1, bridge.DepositCount)
			require.Equal(t, BridgeSourceForwardLET, bridge.Source)
		}
	})

	t.Run("process leaf with matching archived bridge", func(t *testing.T) {
		p, tx := setupProcessorWithTransaction(t)
		defer tx.Rollback() //nolint:errcheck

		// Setup: Insert initial leaves (indices 0-14)
		var initialRoot common.Hash
		var err error
		// Insert block rows for initial leaves
		for i := uint32(0); i <= 14; i++ {
			_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, 30+uint64(i))
			require.NoError(t, err)
		}
		for i := uint32(0); i <= 14; i++ {
			leaf := types.Leaf{Index: i, Hash: common.HexToHash(fmt.Sprintf("0x%d", i))}
			initialRoot, err = p.exitTree.PutLeaf(tx, 30+uint64(i), 0, leaf)
			require.NoError(t, err)
		}
		initialDepositCount := uint32(14) // Last index inserted

		// Insert blocks for the archived bridge and ForwardLET event
		_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1), ($2)`, uint64(50), uint64(300))
		require.NoError(t, err)

		// Setup: Create and archive a bridge that will match the forward LET leaf
		archivedTxHash := common.HexToHash("0xoriginal123")
		archivedTxnSender := common.HexToAddress("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		archivedFromAddr := common.HexToAddress("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")

		archivedBridge := &Bridge{
			BlockNum:           50,
			BlockPos:           0,
			LeafType:           1,
			OriginNetwork:      7,
			OriginAddress:      common.HexToAddress("0x7777777777777777777777777777777777777777"),
			DestinationNetwork: 8,
			DestinationAddress: common.HexToAddress("0x8888888888888888888888888888888888888888"),
			Amount:             big.NewInt(500000),
			Metadata:           []byte("archived metadata"),
			DepositCount:       20,
			TxHash:             archivedTxHash,
			TxnSender:          archivedTxnSender,
			FromAddress:        archivedFromAddr,
			// Don't set Source - bridge_archive table doesn't have this column
		}
		// Insert manually to avoid Source field
		err = meddler.Insert(tx, "bridge_archive", archivedBridge)
		require.NoError(t, err)

		// Create forward LET event with matching leaf
		leaves := []LeafData{
			{
				LeafType:           archivedBridge.LeafType,
				OriginNetwork:      archivedBridge.OriginNetwork,
				OriginAddress:      archivedBridge.OriginAddress,
				DestinationNetwork: archivedBridge.DestinationNetwork,
				DestinationAddress: archivedBridge.DestinationAddress,
				Amount:             archivedBridge.Amount,
				Metadata:           archivedBridge.Metadata,
			},
		}
		encodedLeaves := encodeLeafDataArrayForTest(t, leaves)

		event := &ForwardLET{
			BlockNum:             300,
			BlockPos:             20,
			BlockTimestamp:       1234567950,
			TxnHash:              common.HexToHash("0xforward789"),
			PreviousDepositCount: big.NewInt(int64(initialDepositCount)),
			PreviousRoot:         initialRoot,
			NewDepositCount:      big.NewInt(int64(initialDepositCount + 1)),
			NewLeaves:            encodedLeaves,
		}

		// Calculate expected new root using helper (which will query for archived bridge)
		event.NewRoot = calculateExpectedRootAfterForwardLET(t, initialDepositCount, leaves, event, archivedBridge)

		// Test: Process the forward LET event
		blockPos := event.BlockPos
		newBlockPos, err := p.handleForwardLETEvent(tx, event, &blockPos)
		require.NoError(t, err)
		require.Equal(t, event.BlockPos+1, newBlockPos)

		// Verify: Bridge was inserted with archived tx info
		var bridges []*Bridge
		err = meddler.QueryAll(tx, &bridges, "SELECT * FROM bridge WHERE block_num = $1", event.BlockNum)
		require.NoError(t, err)
		require.Len(t, bridges, 1)

		bridge := bridges[0]
		require.Equal(t, archivedTxHash, bridge.TxHash, "Should use archived tx hash")
		require.Equal(t, archivedTxnSender, bridge.TxnSender, "Should use archived txn sender")
		require.Equal(t, archivedFromAddr, bridge.FromAddress, "Should use archived from address")
		require.Equal(t, BridgeSourceForwardLET, bridge.Source)
	})

	t.Run("process leaf with multiple matching archived bridges", func(t *testing.T) {
		p, tx := setupProcessorWithTransaction(t)
		defer tx.Rollback() //nolint:errcheck

		// Setup: Insert initial leaves (indices 0-24)
		var initialRoot common.Hash
		var err error
		// Insert block rows for initial leaves
		for i := uint32(0); i <= 24; i++ {
			_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, 40+uint64(i))
			require.NoError(t, err)
		}
		for i := uint32(0); i <= 24; i++ {
			leaf := types.Leaf{Index: i, Hash: common.HexToHash(fmt.Sprintf("0x%d", i))}
			initialRoot, err = p.exitTree.PutLeaf(tx, 40+uint64(i), 0, leaf)
			require.NoError(t, err)
		}
		initialDepositCount := uint32(24) // Last index inserted

		// Insert blocks for archived bridges (60, 61 already exist from initial leaves) and ForwardLET event
		_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, uint64(400))
		require.NoError(t, err)

		// Setup: Create two archived bridges with identical LeafData fields
		commonLeafData := LeafData{
			LeafType:           1,
			OriginNetwork:      9,
			OriginAddress:      common.HexToAddress("0x9999999999999999999999999999999999999999"),
			DestinationNetwork: 11,
			DestinationAddress: common.HexToAddress("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
			Amount:             big.NewInt(750000),
			Metadata:           []byte("duplicate metadata"),
		}

		archivedBridge1 := &Bridge{
			BlockNum:           60,
			BlockPos:           0,
			LeafType:           commonLeafData.LeafType,
			OriginNetwork:      commonLeafData.OriginNetwork,
			OriginAddress:      commonLeafData.OriginAddress,
			DestinationNetwork: commonLeafData.DestinationNetwork,
			DestinationAddress: commonLeafData.DestinationAddress,
			Amount:             commonLeafData.Amount,
			Metadata:           commonLeafData.Metadata,
			DepositCount:       30,
			TxHash:             common.HexToHash("0xfirst111"),
			TxnSender:          common.HexToAddress("0x1111111111111111111111111111111111111111"),
			FromAddress:        common.HexToAddress("0x2222222222222222222222222222222222222222"),
		}

		archivedBridge2 := &Bridge{
			BlockNum:           61,
			BlockPos:           0,
			LeafType:           commonLeafData.LeafType,
			OriginNetwork:      commonLeafData.OriginNetwork,
			OriginAddress:      commonLeafData.OriginAddress,
			DestinationNetwork: commonLeafData.DestinationNetwork,
			DestinationAddress: commonLeafData.DestinationAddress,
			Amount:             commonLeafData.Amount,
			Metadata:           commonLeafData.Metadata,
			DepositCount:       31,
			TxHash:             common.HexToHash("0xsecond222"),
			TxnSender:          common.HexToAddress("0x3333333333333333333333333333333333333333"),
			FromAddress:        common.HexToAddress("0x4444444444444444444444444444444444444444"),
		}

		// Insert both archived bridges manually (to avoid Source column)
		for _, archived := range []*Bridge{archivedBridge1, archivedBridge2} {
			err = meddler.Insert(tx, "bridge_archive", archived)
			require.NoError(t, err)
		}

		// Create forward LET event with the common leaf
		leaves := []LeafData{commonLeafData}
		encodedLeaves := encodeLeafDataArrayForTest(t, leaves)

		event := &ForwardLET{
			BlockNum:             400,
			BlockPos:             30,
			BlockTimestamp:       1234567999,
			TxnHash:              common.HexToHash("0xforward999"),
			PreviousDepositCount: big.NewInt(int64(initialDepositCount)),
			PreviousRoot:         initialRoot,
			NewDepositCount:      big.NewInt(int64(initialDepositCount + 1)),
			NewLeaves:            encodedLeaves,
		}

		// Calculate expected new root using helper (with no archived bridge info since multiple matches)
		event.NewRoot = calculateExpectedRootAfterForwardLET(t, initialDepositCount, leaves, event)

		// Test: Process the forward LET event
		blockPos := event.BlockPos
		newBlockPos, err := p.handleForwardLETEvent(tx, event, &blockPos)
		require.NoError(t, err)
		require.Equal(t, event.BlockPos+1, newBlockPos)

		// Verify: Bridge was inserted with event's tx hash and empty addresses
		var bridges []*Bridge
		err = meddler.QueryAll(tx, &bridges, "SELECT * FROM bridge WHERE block_num = $1", event.BlockNum)
		require.NoError(t, err)
		require.Len(t, bridges, 1)

		bridge := bridges[0]
		require.Equal(t, event.TxnHash, bridge.TxHash, "Should use event's tx hash when multiple archived bridges match")
		require.Equal(t, common.Address{}, bridge.TxnSender, "TxnSender should be empty with multiple matches")
		require.Equal(t, common.Address{}, bridge.FromAddress, "FromAddress should be empty with multiple matches")
		require.Equal(t, BridgeSourceForwardLET, bridge.Source)
	})

	t.Run("error on previous root mismatch", func(t *testing.T) {
		p, tx := setupProcessorWithTransaction(t)
		defer tx.Rollback() //nolint:errcheck

		// Setup: Insert initial leaves (indices 0-4)
		var initialRoot common.Hash
		var err error
		for i := uint32(0); i <= 4; i++ {
			leaf := types.Leaf{Index: i, Hash: common.HexToHash(fmt.Sprintf("0x%d", i))}
			initialRoot, err = p.exitTree.PutLeaf(tx, 10+uint64(i), 0, leaf)
			require.NoError(t, err)
		}
		initialDepositCount := uint32(4) // Last index inserted

		// Create forward LET event with WRONG previous root
		leaves := []LeafData{
			{
				LeafType:           1,
				OriginNetwork:      5,
				OriginAddress:      common.HexToAddress("0x1234567890123456789012345678901234567890"),
				DestinationNetwork: 10,
				DestinationAddress: common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"),
				Amount:             big.NewInt(1000000),
				Metadata:           []byte("test"),
			},
		}
		encodedLeaves := encodeLeafDataArrayForTest(t, leaves)

		event := &ForwardLET{
			BlockNum:             100,
			BlockPos:             5,
			BlockTimestamp:       1234567890,
			TxnHash:              common.HexToHash("0xabc123"),
			PreviousDepositCount: big.NewInt(int64(initialDepositCount)),
			PreviousRoot:         common.HexToHash("0xWRONG"), // Wrong root
			NewDepositCount:      big.NewInt(int64(initialDepositCount + 1)),
			NewRoot:              common.HexToHash("0x999"),
			NewLeaves:            encodedLeaves,
		}

		// Test: Should fail with root mismatch
		blockPos := event.BlockPos
		_, err = p.handleForwardLETEvent(tx, event, &blockPos)
		require.Error(t, err)
		require.Contains(t, err.Error(), "local exit root mismatch")
		require.Contains(t, err.Error(), initialRoot.String())
	})

	t.Run("error on new root mismatch", func(t *testing.T) {
		p, tx := setupProcessorWithTransaction(t)
		defer tx.Rollback() //nolint:errcheck

		// Setup: Insert initial leaves (indices 0-4)
		var initialRoot common.Hash
		var err error
		// Insert block rows for initial leaves
		for i := uint32(0); i <= 4; i++ {
			_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, 10+uint64(i))
			require.NoError(t, err)
		}
		for i := uint32(0); i <= 4; i++ {
			leaf := types.Leaf{Index: i, Hash: common.HexToHash(fmt.Sprintf("0x%d", i))}
			initialRoot, err = p.exitTree.PutLeaf(tx, 10+uint64(i), 0, leaf)
			require.NoError(t, err)
		}
		initialDepositCount := uint32(4) // Last index inserted

		// Insert block for the ForwardLET event
		_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, uint64(100))
		require.NoError(t, err)

		// Create forward LET event
		leaves := []LeafData{
			{
				LeafType:           1,
				OriginNetwork:      5,
				OriginAddress:      common.HexToAddress("0x1234567890123456789012345678901234567890"),
				DestinationNetwork: 10,
				DestinationAddress: common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"),
				Amount:             big.NewInt(1000000),
				Metadata:           []byte("test"),
			},
		}
		encodedLeaves := encodeLeafDataArrayForTest(t, leaves)

		event := &ForwardLET{
			BlockNum:             100,
			BlockPos:             5,
			BlockTimestamp:       1234567890,
			TxnHash:              common.HexToHash("0xabc123"),
			PreviousDepositCount: big.NewInt(int64(initialDepositCount)),
			PreviousRoot:         initialRoot,
			NewDepositCount:      big.NewInt(int64(initialDepositCount + 1)),
			NewRoot:              common.HexToHash("0xWRONG"), // Wrong new root
			NewLeaves:            encodedLeaves,
		}

		// Test: Should fail with new root mismatch after processing
		blockPos := event.BlockPos
		_, err = p.handleForwardLETEvent(tx, event, &blockPos)
		require.Error(t, err)
		require.Contains(t, err.Error(), "local exit root mismatch")
	})

	t.Run("error on invalid encoded leaves", func(t *testing.T) {
		p, tx := setupProcessorWithTransaction(t)
		defer tx.Rollback() //nolint:errcheck

		// Setup: Insert initial leaves (indices 0-4)
		var initialRoot common.Hash
		var err error
		for i := uint32(0); i <= 4; i++ {
			leaf := types.Leaf{Index: i, Hash: common.HexToHash(fmt.Sprintf("0x%d", i))}
			initialRoot, err = p.exitTree.PutLeaf(tx, 10+uint64(i), 0, leaf)
			require.NoError(t, err)
		}
		initialDepositCount := uint32(4) // Last index inserted

		event := &ForwardLET{
			BlockNum:             100,
			BlockPos:             5,
			BlockTimestamp:       1234567890,
			TxnHash:              common.HexToHash("0xabc123"),
			PreviousDepositCount: big.NewInt(int64(initialDepositCount)),
			PreviousRoot:         initialRoot,
			NewDepositCount:      big.NewInt(int64(initialDepositCount + 1)),
			NewRoot:              common.Hash{},
			NewLeaves:            []byte("invalid data"), // Invalid encoding
		}

		// Test: Should fail to decode leaves
		blockPos := event.BlockPos
		_, err = p.handleForwardLETEvent(tx, event, &blockPos)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to decode new leaves")
	})

	t.Run("process with nil blockPos parameter", func(t *testing.T) {
		p, tx := setupProcessorWithTransaction(t)
		defer tx.Rollback() //nolint:errcheck

		// Setup: Insert initial leaves (indices 0-4)
		var initialRoot common.Hash
		var err error
		// Insert block rows for initial leaves
		for i := uint32(0); i <= 4; i++ {
			_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, 10+uint64(i))
			require.NoError(t, err)
		}
		for i := uint32(0); i <= 4; i++ {
			leaf := types.Leaf{Index: i, Hash: common.HexToHash(fmt.Sprintf("0x%d", i))}
			initialRoot, err = p.exitTree.PutLeaf(tx, 10+uint64(i), 0, leaf)
			require.NoError(t, err)
		}
		initialDepositCount := uint32(4) // Last index inserted

		// Insert block for the ForwardLET event
		_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, uint64(100))
		require.NoError(t, err)

		leaves := []LeafData{
			{
				LeafType:           1,
				OriginNetwork:      5,
				OriginAddress:      common.HexToAddress("0x1234567890123456789012345678901234567890"),
				DestinationNetwork: 10,
				DestinationAddress: common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"),
				Amount:             big.NewInt(1000000),
				Metadata:           []byte("test"),
			},
		}
		encodedLeaves := encodeLeafDataArrayForTest(t, leaves)

		event := &ForwardLET{
			BlockNum:             100,
			BlockPos:             5,
			BlockTimestamp:       1234567890,
			TxnHash:              common.HexToHash("0xabc123"),
			PreviousDepositCount: big.NewInt(int64(initialDepositCount)),
			PreviousRoot:         initialRoot,
			NewDepositCount:      big.NewInt(int64(initialDepositCount + 1)),
			NewLeaves:            encodedLeaves,
		}

		// Calculate expected root using helper
		event.NewRoot = calculateExpectedRootAfterForwardLET(t, initialDepositCount, leaves, event)

		// Test: Process with nil blockPos (should use event.BlockPos)
		newBlockPos, err := p.handleForwardLETEvent(tx, event, nil)
		require.NoError(t, err)
		require.Equal(t, event.BlockPos+1, newBlockPos)

		// Verify: Bridge uses event.BlockPos
		var bridges []*Bridge
		err = meddler.QueryAll(tx, &bridges, "SELECT * FROM bridge WHERE block_num = $1", event.BlockNum)
		require.NoError(t, err)
		require.Len(t, bridges, 1)
		require.Equal(t, event.BlockPos, bridges[0].BlockPos)
	})
}

// setupProcessorWithTransaction creates a processor and begins a transaction for testing
func setupProcessorWithTransaction(t *testing.T) (*processor, dbtypes.Txer) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "test_forward_let.db")
	err := migrations.RunMigrations(dbPath)
	require.NoError(t, err)

	logger := log.WithFields("module", "test")
	p, err := newProcessor(dbPath, "test", logger, dbQueryTimeout)
	require.NoError(t, err)

	tx, err := db.NewTx(t.Context(), p.db)
	require.NoError(t, err)

	return p, tx
}

// calculateExpectedRootAfterForwardLET calculates what the tree root will be after processing ForwardLET leaves
// It does this using a completely separate processor to avoid affecting the test state
// archivedBridges: optional map from leaf index (in leaves slice) to archived bridge info
func calculateExpectedRootAfterForwardLET(t *testing.T, initialDepositCount uint32,
	leaves []LeafData, event *ForwardLET, archivedBridges ...*Bridge) common.Hash {
	t.Helper()

	// Build a map for quick lookup of archived bridge info by leaf data
	archivedByLeaf := make(map[int]*Bridge)
	for i, archived := range archivedBridges {
		if archived != nil {
			archivedByLeaf[i] = archived
		}
	}

	// Create a temporary processor with its own database
	tempDBPath := filepath.Join(t.TempDir(), "temp_calc.db")
	err := migrations.RunMigrations(tempDBPath)
	require.NoError(t, err)

	logger := log.WithFields("module", "test-calc")
	tempP, err := newProcessor(tempDBPath, "test-calc", logger, dbQueryTimeout)
	require.NoError(t, err)

	tempTx, err := db.NewTx(t.Context(), tempP.db)
	require.NoError(t, err)
	defer tempTx.Rollback() //nolint:errcheck

	// Insert block rows for the setup leaves
	for i := uint32(0); i <= initialDepositCount; i++ {
		_, err = tempTx.Exec(`INSERT INTO block (num) VALUES ($1)`, 10+uint64(i))
		require.NoError(t, err)
	}

	// Insert block row for the ForwardLET event
	_, err = tempTx.Exec(`INSERT INTO block (num) VALUES ($1)`, event.BlockNum)
	require.NoError(t, err)

	// Insert archived bridges if provided
	for _, archived := range archivedBridges {
		if archived != nil {
			_, err = tempTx.Exec(`INSERT INTO block (num) VALUES ($1)`, archived.BlockNum)
			require.NoError(t, err)

			_, err = tempTx.Exec(`
				INSERT INTO bridge_archive (
					block_num, block_pos, leaf_type, origin_network, origin_address,
					destination_network, destination_address, amount, metadata, deposit_count,
					tx_hash, block_timestamp, from_address, txn_sender
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 0, $12, $13)
			`, archived.BlockNum, archived.BlockPos, archived.LeafType,
				archived.OriginNetwork, archived.OriginAddress.Hex(),
				archived.DestinationNetwork, archived.DestinationAddress.Hex(),
				archived.Amount.String(), archived.Metadata, archived.DepositCount,
				archived.TxHash.Hex(), archived.FromAddress.Hex(), archived.TxnSender.Hex())
			require.NoError(t, err)
		}
	}

	// Rebuild tree state up to initialDepositCount
	for i := uint32(0); i <= initialDepositCount; i++ {
		leaf := types.Leaf{Index: i, Hash: common.HexToHash(fmt.Sprintf("0x%d", i))}
		_, err = tempP.exitTree.PutLeaf(tempTx, 10+uint64(i), 0, leaf)
		require.NoError(t, err)
	}

	// Now add the ForwardLET leaves (will query for archived bridges)
	currentDepositCount := initialDepositCount + 1
	var newRoot common.Hash
	for i, leaf := range leaves {
		// Try to get archived bridge info if available
		var txHash common.Hash
		var txnSender, fromAddr common.Address
		if archived, found := archivedByLeaf[i]; found {
			txHash = archived.TxHash
			txnSender = archived.TxnSender
			fromAddr = archived.FromAddress
		} else {
			txHash = event.TxnHash
			// txnSender and fromAddr remain zero
		}

		bridge := leaf.ToBridge(
			event.BlockNum,
			event.BlockPos+uint64(i),
			event.BlockTimestamp,
			currentDepositCount,
			txHash,
			txnSender,
			fromAddr,
		)
		newRoot, err = tempP.exitTree.PutLeaf(tempTx, event.BlockNum, event.BlockPos+uint64(i), types.Leaf{
			Index: currentDepositCount,
			Hash:  bridge.Hash(),
		})
		require.NoError(t, err)
		currentDepositCount++
	}

	return newRoot
}

// encodeLeafDataArrayForTest encodes a slice of LeafData using ABI encoding
func encodeLeafDataArrayForTest(t *testing.T, leaves []LeafData) []byte {
	t.Helper()

	encodedBytes, err := aggkitabi.EncodeABIStructArray(leaves)
	require.NoError(t, err)

	return encodedBytes
}
