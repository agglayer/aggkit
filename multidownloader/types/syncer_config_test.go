package types

import (
	"testing"

	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestContractConfigs_EmptySet(t *testing.T) {
	var set *SetSyncerConfig
	configs := set.ContractConfigs()
	require.Empty(t, configs)

	set2 := NewSetSyncerConfig()
	configs2 := set2.ContractConfigs()
	require.Empty(t, configs2)
}

func TestContractConfigs_SingleSyncerSingleContract(t *testing.T) {
	addr := common.HexToAddress("0x1")
	set := NewSetSyncerConfig()
	set.Add(aggkittypes.SyncerConfig{
		SyncerID:          "syncer1",
		ContractAddresses: []common.Address{addr},
		FromBlock:         10,
		ToBlock:           aggkittypes.FinalizedBlock,
	})

	configs := set.ContractConfigs()
	require.Len(t, configs, 1)
	cc := configs[0]
	require.Equal(t, addr, cc.Address)
	require.Equal(t, uint64(10), cc.FromBlock)
	require.Equal(t, aggkittypes.FinalizedBlock, cc.ToBlock)
	require.Contains(t, cc.Syncers, "syncer1")
}

func TestContractConfigs_MultipleSyncersSameContract(t *testing.T) {
	addr := common.HexToAddress("0x2")
	set := NewSetSyncerConfig()
	set.Add(aggkittypes.SyncerConfig{
		SyncerID:          "syncer1",
		ContractAddresses: []common.Address{addr},
		FromBlock:         15,
		ToBlock:           aggkittypes.FinalizedBlock,
	})
	set.Add(aggkittypes.SyncerConfig{
		SyncerID:          "syncer2",
		ContractAddresses: []common.Address{addr},
		FromBlock:         5,
		ToBlock:           aggkittypes.LatestBlock,
	})

	configs := set.ContractConfigs()
	require.Len(t, configs, 1)
	cc := configs[0]
	require.Equal(t, addr, cc.Address)
	// FromBlock should be the minimum
	require.Equal(t, uint64(5), cc.FromBlock)
	// ToBlock should be the earliest/most permissive (LatestBlock < FinalizedBlock)
	require.Equal(t, aggkittypes.LatestBlock, cc.ToBlock)
	require.Equal(t, []SyncerID{"syncer1", "syncer2"}, cc.Syncers)
}

func TestContractConfigs_MultipleSyncersMultipleContracts(t *testing.T) {
	addr1 := common.HexToAddress("0x3")
	addr2 := common.HexToAddress("0x4")
	set := NewSetSyncerConfig()
	set.Add(aggkittypes.SyncerConfig{
		SyncerID:          "syncer1",
		ContractAddresses: []common.Address{addr1, addr2},
		FromBlock:         1,
		ToBlock:           aggkittypes.FinalizedBlock,
	})
	set.Add(aggkittypes.SyncerConfig{
		SyncerID:          "syncer2",
		ContractAddresses: []common.Address{addr2},
		FromBlock:         2,
		ToBlock:           aggkittypes.LatestBlock,
	})

	configs := set.ContractConfigs()
	require.Len(t, configs, 2)
	var found1, found2 bool
	for _, cc := range configs {
		switch cc.Address {
		case addr1:
			found1 = true
			require.Equal(t, uint64(1), cc.FromBlock)
			require.Equal(t, aggkittypes.FinalizedBlock, cc.ToBlock)
			require.Equal(t, []SyncerID{"syncer1"}, cc.Syncers)
		case addr2:
			found2 = true
			require.Equal(t, uint64(1), cc.FromBlock)
			require.Equal(t, aggkittypes.LatestBlock, cc.ToBlock)
			require.Equal(t, []SyncerID{"syncer1", "syncer2"}, cc.Syncers)
		}
	}
	require.True(t, found1)
	require.True(t, found2)
}
func TestContractConfig_Update_FromBlock(t *testing.T) {
	cc := &ContractConfig{
		Address:   common.HexToAddress("0x1"),
		FromBlock: 10,
		ToBlock:   aggkittypes.FinalizedBlock,
		Syncers:   []SyncerID{"syncer1"},
	}

	// Update with lower FromBlock
	cc.Update(aggkittypes.SyncerConfig{
		SyncerID:          "syncer2",
		ContractAddresses: []common.Address{common.HexToAddress("0x1")},
		FromBlock:         5,
		ToBlock:           aggkittypes.FinalizedBlock,
	})

	require.Equal(t, uint64(5), cc.FromBlock)
	require.Equal(t, []SyncerID{"syncer1", "syncer2"}, cc.Syncers)

	// Update with higher FromBlock (should not change)
	cc.Update(aggkittypes.SyncerConfig{
		SyncerID:          "syncer3",
		ContractAddresses: []common.Address{common.HexToAddress("0x1")},
		FromBlock:         15,
		ToBlock:           aggkittypes.FinalizedBlock,
	})

	require.Equal(t, uint64(5), cc.FromBlock)
	require.Equal(t, []SyncerID{"syncer1", "syncer2", "syncer3"}, cc.Syncers)
}

func TestContractConfig_Update_ToBlock(t *testing.T) {
	cc := &ContractConfig{
		Address:   common.HexToAddress("0x1"),
		FromBlock: 10,
		ToBlock:   aggkittypes.FinalizedBlock,
		Syncers:   []SyncerID{"syncer1"},
	}

	// Update with less final ToBlock (LatestBlock < FinalizedBlock)
	cc.Update(aggkittypes.SyncerConfig{
		SyncerID:          "syncer2",
		ContractAddresses: []common.Address{common.HexToAddress("0x1")},
		FromBlock:         15,
		ToBlock:           aggkittypes.LatestBlock,
	})

	require.Equal(t, aggkittypes.LatestBlock, cc.ToBlock)
	require.Equal(t, []SyncerID{"syncer1", "syncer2"}, cc.Syncers)

	// Update with more final ToBlock (should not change)
	cc.Update(aggkittypes.SyncerConfig{
		SyncerID:          "syncer3",
		ContractAddresses: []common.Address{common.HexToAddress("0x1")},
		FromBlock:         20,
		ToBlock:           aggkittypes.SafeBlock,
	})

	require.Equal(t, aggkittypes.LatestBlock, cc.ToBlock)
	require.Equal(t, []SyncerID{"syncer1", "syncer2", "syncer3"}, cc.Syncers)
}

func TestContractConfig_Update_Syncers(t *testing.T) {
	cc := &ContractConfig{
		Address:   common.HexToAddress("0x1"),
		FromBlock: 10,
		ToBlock:   aggkittypes.FinalizedBlock,
		Syncers:   []SyncerID{"syncer1", "syncer3"},
	}

	// Add new syncer
	cc.Update(aggkittypes.SyncerConfig{
		SyncerID:          "syncer2",
		ContractAddresses: []common.Address{common.HexToAddress("0x1")},
		FromBlock:         15,
		ToBlock:           aggkittypes.FinalizedBlock,
	})

	require.Equal(t, []SyncerID{"syncer1", "syncer2", "syncer3"}, cc.Syncers)

	// Add existing syncer (should not duplicate)
	cc.Update(aggkittypes.SyncerConfig{
		SyncerID:          "syncer2",
		ContractAddresses: []common.Address{common.HexToAddress("0x1")},
		FromBlock:         20,
		ToBlock:           aggkittypes.FinalizedBlock,
	})

	require.Equal(t, []SyncerID{"syncer1", "syncer2", "syncer3"}, cc.Syncers)
}

func TestContractConfig_Update_Combined(t *testing.T) {
	cc := &ContractConfig{
		Address:   common.HexToAddress("0x1"),
		FromBlock: 10,
		ToBlock:   aggkittypes.FinalizedBlock,
		Syncers:   []SyncerID{"syncer1"},
	}

	// Update all fields at once
	cc.Update(aggkittypes.SyncerConfig{
		SyncerID:          "syncer2",
		ContractAddresses: []common.Address{common.HexToAddress("0x1")},
		FromBlock:         5,
		ToBlock:           aggkittypes.LatestBlock,
	})

	require.Equal(t, uint64(5), cc.FromBlock)
	require.Equal(t, aggkittypes.LatestBlock, cc.ToBlock)
	require.Equal(t, []SyncerID{"syncer1", "syncer2"}, cc.Syncers)
}

func TestContractConfig_Update_Brief(t *testing.T) {
	t.Run("brief with valid config", func(t *testing.T) {
		sut := NewSetSyncerConfig()
		sut.Add(aggkittypes.SyncerConfig{
			SyncerID:          "syncer1",
			ContractAddresses: []common.Address{common.HexToAddress("0x1")},
			FromBlock:         10,
			ToBlock:           aggkittypes.FinalizedBlock,
		})
		sut.Add(aggkittypes.SyncerConfig{
			SyncerID:          "syncer2",
			ContractAddresses: []common.Address{common.HexToAddress("0x1")},
			FromBlock:         5,
			ToBlock:           aggkittypes.LatestBlock,
		})

		expected := "SetSyncerConfig{(syncer1 -> [10 - FinalizedBlock]) (syncer2 -> [5 - LatestBlock])}"
		require.Equal(t, expected, sut.Brief())
	})

	t.Run("brief with nil config", func(t *testing.T) {
		var cc *SetSyncerConfig
		expected := "SetSyncerConfig{<nil>}"
		require.Equal(t, expected, cc.Brief())
	})
}
