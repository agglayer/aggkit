package types

import (
	"testing"

	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// Use the real BlockNumberFinality interface/type from your codebase.
// Replace this import path with the actual one if different.

// Ensure your BlockNumberFinality implementation is available for use in tests.

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
	set.Add(SyncerConfig{
		SyncerID:      "syncer1",
		ContractsAddr: []common.Address{addr},
		FromBlock:     10,
		ToBlock:       aggkittypes.FinalizedBlock,
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
	set.Add(SyncerConfig{
		SyncerID:      "syncer1",
		ContractsAddr: []common.Address{addr},
		FromBlock:     15,
		ToBlock:       aggkittypes.FinalizedBlock,
	})
	set.Add(SyncerConfig{
		SyncerID:      "syncer2",
		ContractsAddr: []common.Address{addr},
		FromBlock:     5,
		ToBlock:       aggkittypes.LatestBlock,
	})

	configs := set.ContractConfigs()
	require.Len(t, configs, 1)
	cc := configs[0]
	require.Equal(t, addr, cc.Address)
	// FromBlock should be the minimum
	require.Equal(t, uint64(5), cc.FromBlock)
	// ToBlock should be the "less final" (minimum)
	require.Equal(t, aggkittypes.LatestBlock, cc.ToBlock)
	require.Equal(t, []SyncerID{"syncer1", "syncer2"}, cc.Syncers)
}

func TestContractConfigs_MultipleSyncersMultipleContracts(t *testing.T) {
	addr1 := common.HexToAddress("0x3")
	addr2 := common.HexToAddress("0x4")
	set := NewSetSyncerConfig()
	set.Add(SyncerConfig{
		SyncerID:      "syncer1",
		ContractsAddr: []common.Address{addr1, addr2},
		FromBlock:     1,
		ToBlock:       aggkittypes.FinalizedBlock,
	})
	set.Add(SyncerConfig{
		SyncerID:      "syncer2",
		ContractsAddr: []common.Address{addr2},
		FromBlock:     2,
		ToBlock:       aggkittypes.LatestBlock,
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
