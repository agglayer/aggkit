package types

import (
	"testing"

	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

var (
	txReqAddr       = common.HexToAddress("0x1111111111111111111111111111111111111111")
	txReqOtherAddr  = common.HexToAddress("0x2222222222222222222222222222222222222222")
	txReqTopicA     = common.HexToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	txReqTopicB     = common.HexToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	txReqTopicOther = common.HexToHash("0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")
)

func TestContractTransactionRequirements_ZeroValueDisabled(t *testing.T) {
	set := NewSetSyncerConfig()
	set.Add(aggkittypes.SyncerConfig{
		SyncerID:          "s1",
		ContractAddresses: []common.Address{txReqAddr},
		FromBlock:         10,
	})

	// Zero value must not produce any requirement.
	require.Empty(t, set.ContractTransactionRequirements())
	_, ok := set.RequiresTransaction(txReqAddr, 100, []common.Hash{txReqTopicA})
	require.False(t, ok)
}

// TestContractTransactionRequirements_NoLossyMerge pins that each
// (syncer, address) registration keeps its own FromBlock/Topic0/ToBlock: no
// per-address min(FromBlock) + union(Topic0) merge is performed.
func TestContractTransactionRequirements_NoLossyMerge(t *testing.T) {
	set := NewSetSyncerConfig()
	set.Add(aggkittypes.SyncerConfig{
		SyncerID:          "s1",
		ContractAddresses: []common.Address{txReqAddr},
		FromBlock:         10,
		TransactionRequirements: aggkittypes.TransactionRequirement{
			Enabled:   true,
			FromBlock: 50,
			Topic0:    []common.Hash{txReqTopicA},
		},
	})
	set.Add(aggkittypes.SyncerConfig{
		SyncerID:          "s2",
		ContractAddresses: []common.Address{txReqAddr},
		FromBlock:         20,
		TransactionRequirements: aggkittypes.TransactionRequirement{
			Enabled:   true,
			FromBlock: 30,
			Topic0:    []common.Hash{txReqTopicB, txReqTopicA},
		},
	})

	reqs := set.ContractTransactionRequirements()
	require.Len(t, reqs, 2, "one requirement per registration, never merged")

	// Deterministic order: same address/ToBlock, so ascending FromBlock.
	require.Equal(t, txReqAddr, reqs[0].Address)
	require.Equal(t, uint64(30), reqs[0].FromBlock)
	require.Equal(t, []common.Hash{txReqTopicB, txReqTopicA}, reqs[0].Topic0)
	require.Equal(t, []SyncerID{"s2"}, reqs[0].Syncers)

	require.Equal(t, txReqAddr, reqs[1].Address)
	require.Equal(t, uint64(50), reqs[1].FromBlock)
	require.Equal(t, []common.Hash{txReqTopicA}, reqs[1].Topic0)
	require.Equal(t, []SyncerID{"s1"}, reqs[1].Syncers)
}

// TestContractTransactionRequirements_PerRegistrationPairing reproduces the
// confirmed bug: A topicA from 100 plus B topicB from 200 must not demand B at
// block 100.
func TestContractTransactionRequirements_PerRegistrationPairing(t *testing.T) {
	set := NewSetSyncerConfig()
	set.Add(aggkittypes.SyncerConfig{
		SyncerID:          "A",
		ContractAddresses: []common.Address{txReqAddr},
		TransactionRequirements: aggkittypes.TransactionRequirement{
			Enabled:   true,
			FromBlock: 100,
			Topic0:    []common.Hash{txReqTopicA},
		},
	})
	set.Add(aggkittypes.SyncerConfig{
		SyncerID:          "B",
		ContractAddresses: []common.Address{txReqAddr},
		TransactionRequirements: aggkittypes.TransactionRequirement{
			Enabled:   true,
			FromBlock: 200,
			Topic0:    []common.Hash{txReqTopicB},
		},
	})

	// B's topic at A's frontier is not required.
	_, ok := set.RequiresTransaction(txReqAddr, 100, []common.Hash{txReqTopicB})
	require.False(t, ok, "topicB at block 100 must not be demanded by A's frontier")

	// Each topic is required only from its own frontier.
	req, ok := set.RequiresTransaction(txReqAddr, 150, []common.Hash{txReqTopicA})
	require.True(t, ok)
	require.Equal(t, []SyncerID{"A"}, req.Syncers)

	req, ok = set.RequiresTransaction(txReqAddr, 200, []common.Hash{txReqTopicB})
	require.True(t, ok)
	require.Equal(t, []SyncerID{"B"}, req.Syncers)

	_, ok = set.RequiresTransaction(txReqAddr, 199, []common.Hash{txReqTopicB})
	require.False(t, ok)
}

// TestContractTransactionRequirements_EmptySelectorNotNarrowed pins that an
// empty Topic0 ("any") is preserved and is never narrowed by unioning it with a
// restricted selector from another syncer.
func TestContractTransactionRequirements_EmptySelectorNotNarrowed(t *testing.T) {
	set := NewSetSyncerConfig()
	set.Add(aggkittypes.SyncerConfig{
		SyncerID:          "any",
		ContractAddresses: []common.Address{txReqAddr},
		TransactionRequirements: aggkittypes.TransactionRequirement{
			Enabled:   true,
			FromBlock: 100,
		},
	})
	set.Add(aggkittypes.SyncerConfig{
		SyncerID:          "restricted",
		ContractAddresses: []common.Address{txReqAddr},
		TransactionRequirements: aggkittypes.TransactionRequirement{
			Enabled:   true,
			FromBlock: 200,
			Topic0:    []common.Hash{txReqTopicB},
		},
	})

	reqs := set.ContractTransactionRequirements()
	require.Len(t, reqs, 2)
	// The "any" registration keeps an empty selector.
	require.Empty(t, reqs[0].Topic0)
	require.Equal(t, []SyncerID{"any"}, reqs[0].Syncers)

	// An arbitrary topic at the "any" frontier is still required via "any".
	req, ok := set.RequiresTransaction(txReqAddr, 150, []common.Hash{txReqTopicOther})
	require.True(t, ok)
	require.Equal(t, []SyncerID{"any"}, req.Syncers)
	require.Empty(t, req.Topic0)

	// Topicless log at the "any" frontier is required.
	req, ok = set.RequiresTransaction(txReqAddr, 150, nil)
	require.True(t, ok)
	require.Equal(t, []SyncerID{"any"}, req.Syncers)
}

// TestContractTransactionRequirements_EffectiveFrontier pins that the effective
// lower bound is max(SyncerConfig.FromBlock, TransactionRequirement.FromBlock).
func TestContractTransactionRequirements_EffectiveFrontier(t *testing.T) {
	t.Run("syncer FromBlock is later", func(t *testing.T) {
		set := NewSetSyncerConfig()
		set.Add(aggkittypes.SyncerConfig{
			SyncerID:          "s1",
			ContractAddresses: []common.Address{txReqAddr},
			FromBlock:         500,
			TransactionRequirements: aggkittypes.TransactionRequirement{
				Enabled:   true,
				FromBlock: 100,
				Topic0:    []common.Hash{txReqTopicA},
			},
		})
		_, ok := set.RequiresTransaction(txReqAddr, 400, []common.Hash{txReqTopicA})
		require.False(t, ok)
		_, ok = set.RequiresTransaction(txReqAddr, 499, []common.Hash{txReqTopicA})
		require.False(t, ok)
		_, ok = set.RequiresTransaction(txReqAddr, 500, []common.Hash{txReqTopicA})
		require.True(t, ok)
	})

	t.Run("requirement FromBlock is later", func(t *testing.T) {
		set := NewSetSyncerConfig()
		set.Add(aggkittypes.SyncerConfig{
			SyncerID:          "s1",
			ContractAddresses: []common.Address{txReqAddr},
			FromBlock:         50,
			TransactionRequirements: aggkittypes.TransactionRequirement{
				Enabled:   true,
				FromBlock: 100,
				Topic0:    []common.Hash{txReqTopicA},
			},
		})
		_, ok := set.RequiresTransaction(txReqAddr, 99, []common.Hash{txReqTopicA})
		require.False(t, ok)
		_, ok = set.RequiresTransaction(txReqAddr, 100, []common.Hash{txReqTopicA})
		require.True(t, ok)
	})
}

// TestContractTransactionRequirements_NumericToBlockBound pins that a numeric
// ToBlock bounds the match while a tag finality is left to the worker.
func TestContractTransactionRequirements_NumericToBlockBound(t *testing.T) {
	set := NewSetSyncerConfig()
	set.Add(aggkittypes.SyncerConfig{
		SyncerID:          "s1",
		ContractAddresses: []common.Address{txReqAddr},
		ToBlock:           *aggkittypes.NewBlockNumber(150),
		TransactionRequirements: aggkittypes.TransactionRequirement{
			Enabled:   true,
			FromBlock: 100,
			Topic0:    []common.Hash{txReqTopicA},
		},
	})

	_, ok := set.RequiresTransaction(txReqAddr, 150, []common.Hash{txReqTopicA})
	require.True(t, ok)
	_, ok = set.RequiresTransaction(txReqAddr, 151, []common.Hash{txReqTopicA})
	require.False(t, ok, "numeric ToBlock must bound the match")

	// A tag ToBlock cannot be resolved here and must not bound the match.
	tagSet := NewSetSyncerConfig()
	tagSet.Add(aggkittypes.SyncerConfig{
		SyncerID:          "s1",
		ContractAddresses: []common.Address{txReqAddr},
		ToBlock:           aggkittypes.LatestBlock,
		TransactionRequirements: aggkittypes.TransactionRequirement{
			Enabled:   true,
			FromBlock: 100,
			Topic0:    []common.Hash{txReqTopicA},
		},
	})
	req, ok := tagSet.RequiresTransaction(txReqAddr, 1_000_000, []common.Hash{txReqTopicA})
	require.True(t, ok)
	require.Equal(t, aggkittypes.LatestBlock, req.ToBlock)
}

// TestContractTransactionRequirements_EmptyTopic0MatchesTopicless pins that an
// empty selector matches topicless logs while a restricted one does not.
func TestContractTransactionRequirements_EmptyTopic0MatchesTopicless(t *testing.T) {
	anySet := NewSetSyncerConfig()
	anySet.Add(aggkittypes.SyncerConfig{
		SyncerID:          "s1",
		ContractAddresses: []common.Address{txReqAddr},
		TransactionRequirements: aggkittypes.TransactionRequirement{
			Enabled:   true,
			FromBlock: 0,
		},
	})
	_, ok := anySet.RequiresTransaction(txReqAddr, 5, nil)
	require.True(t, ok)
	_, ok = anySet.RequiresTransaction(txReqAddr, 5, []common.Hash{txReqTopicOther})
	require.True(t, ok)

	restrictedSet := NewSetSyncerConfig()
	restrictedSet.Add(aggkittypes.SyncerConfig{
		SyncerID:          "s1",
		ContractAddresses: []common.Address{txReqAddr},
		TransactionRequirements: aggkittypes.TransactionRequirement{
			Enabled:   true,
			FromBlock: 0,
			Topic0:    []common.Hash{txReqTopicA},
		},
	})
	_, ok = restrictedSet.RequiresTransaction(txReqAddr, 5, nil)
	require.False(t, ok)
}

func TestContractTransactionRequirements_RequiresTransaction(t *testing.T) {
	set := NewSetSyncerConfig()
	set.Add(aggkittypes.SyncerConfig{
		SyncerID:          "s1",
		ContractAddresses: []common.Address{txReqAddr},
		TransactionRequirements: aggkittypes.TransactionRequirement{
			Enabled:   true,
			FromBlock: 100,
			Topic0:    []common.Hash{txReqTopicA},
		},
	})

	_, ok := set.RequiresTransaction(txReqAddr, 100, []common.Hash{txReqTopicA})
	require.True(t, ok)

	// Below FromBlock.
	_, ok = set.RequiresTransaction(txReqAddr, 99, []common.Hash{txReqTopicA})
	require.False(t, ok)

	// Unrelated address.
	_, ok = set.RequiresTransaction(txReqOtherAddr, 100, []common.Hash{txReqTopicA})
	require.False(t, ok)

	// Topic0 not in the OR list.
	_, ok = set.RequiresTransaction(txReqAddr, 100, []common.Hash{txReqTopicOther})
	require.False(t, ok)

	// Topicless log when a Topic0 filter is set does not match.
	_, ok = set.RequiresTransaction(txReqAddr, 100, nil)
	require.False(t, ok)
}

func TestContractTransactionRequirements_SafeOnNilAndUninitialized(t *testing.T) {
	var set *SetSyncerConfig
	require.Empty(t, set.ContractTransactionRequirements())
	require.Empty(t, set.SyncerConfigs())
	_, ok := set.RequiresTransaction(txReqAddr, 1, nil)
	require.False(t, ok)
}

// TestContractTransactionRequirements_SyncerConfigsSnapshot pins that
// SyncerConfigs returns a deterministic, deep-copied immutable snapshot.
func TestContractTransactionRequirements_SyncerConfigsSnapshot(t *testing.T) {
	set := NewSetSyncerConfig()
	set.Add(aggkittypes.SyncerConfig{
		SyncerID:          "s2",
		ContractAddresses: []common.Address{txReqAddr},
		TransactionRequirements: aggkittypes.TransactionRequirement{
			Enabled: true,
			Topic0:  []common.Hash{txReqTopicA},
		},
	})
	set.Add(aggkittypes.SyncerConfig{
		SyncerID:          "s1",
		ContractAddresses: []common.Address{txReqOtherAddr},
	})

	snapshot := set.SyncerConfigs()
	require.Len(t, snapshot, 2)
	// Sorted by SyncerID.
	require.Equal(t, "s1", snapshot[0].SyncerID)
	require.Equal(t, "s2", snapshot[1].SyncerID)

	// Mutating the snapshot must not affect the internal filters.
	snapshot[1].ContractAddresses[0] = txReqOtherAddr
	snapshot[1].TransactionRequirements.Topic0[0] = txReqTopicB

	fresh := set.SyncerConfigs()
	require.Equal(t, txReqAddr, fresh[1].ContractAddresses[0])
	require.Equal(t, []common.Hash{txReqTopicA}, fresh[1].TransactionRequirements.Topic0)
}

// TestContractTransactionRequirements_RequirementsAreReadCopies pins that the
// Topic0 slice in a derived requirement is a copy, so mutating it cannot change
// the configuration.
func TestContractTransactionRequirements_RequirementsAreReadCopies(t *testing.T) {
	set := NewSetSyncerConfig()
	set.Add(aggkittypes.SyncerConfig{
		SyncerID:          "s1",
		ContractAddresses: []common.Address{txReqAddr},
		TransactionRequirements: aggkittypes.TransactionRequirement{
			Enabled:   true,
			FromBlock: 10,
			Topic0:    []common.Hash{txReqTopicA},
		},
	})

	reqs := set.ContractTransactionRequirements()
	require.Len(t, reqs, 1)
	reqs[0].Topic0[0] = txReqTopicB

	fresh := set.ContractTransactionRequirements()
	require.Equal(t, []common.Hash{txReqTopicA}, fresh[0].Topic0)
}
