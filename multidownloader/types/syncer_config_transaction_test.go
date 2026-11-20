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

// TestCopySyncerConfig_DeepClone pins the single remaining deep-copy helper:
// mutating any nested slice of the copy must never affect the source and vice
// versa.
func TestCopySyncerConfig_DeepClone(t *testing.T) {
	cfg := aggkittypes.SyncerConfig{
		SyncerID:          "s1",
		ContractAddresses: []common.Address{txReqAddr},
		FromBlock:         10,
		ToBlock:           aggkittypes.FinalizedBlock,
		TransactionRequirements: aggkittypes.TransactionRequirement{
			Enabled:   true,
			FromBlock: 20,
			Topic0:    []common.Hash{txReqTopicA},
		},
	}

	cp := CopySyncerConfig(cfg)
	require.Equal(t, cfg, cp)

	// Mutating the copy's nested slices must not touch the source.
	cp.ContractAddresses[0] = txReqOtherAddr
	cp.TransactionRequirements.Topic0[0] = txReqTopicB
	require.Equal(t, []common.Address{txReqAddr}, cfg.ContractAddresses)
	require.Equal(t, []common.Hash{txReqTopicA}, cfg.TransactionRequirements.Topic0)

	// Mutating the source's nested slices must not touch a fresh copy.
	cp2 := CopySyncerConfig(cfg)
	cfg.ContractAddresses[0] = txReqOtherAddr
	cfg.TransactionRequirements.Topic0[0] = txReqTopicB
	require.Equal(t, []common.Address{txReqAddr}, cp2.ContractAddresses)
	require.Equal(t, []common.Hash{txReqTopicA}, cp2.TransactionRequirements.Topic0)
}

// TestCopySyncerConfig_NilSlicesPreserved pins that the copy is nil-safe: nil
// slices stay nil (no allocation) instead of being turned into empty slices.
func TestCopySyncerConfig_NilSlicesPreserved(t *testing.T) {
	cp := CopySyncerConfig(aggkittypes.SyncerConfig{SyncerID: "s1"})
	require.Nil(t, cp.ContractAddresses)
	require.Nil(t, cp.TransactionRequirements.Topic0)
}

// TestTopic0Match pins the OR semantics of the topic0 selector: an empty
// required list matches any log (including topicless), otherwise the log's
// topic0 must be contained in the required list.
func TestTopic0Match(t *testing.T) {
	require.True(t, Topic0Match(nil, nil), "empty selector matches topicless logs")
	require.True(t, Topic0Match([]common.Hash{}, []common.Hash{txReqTopicA}))
	require.True(t, Topic0Match([]common.Hash{txReqTopicA}, []common.Hash{txReqTopicA}))
	require.True(t, Topic0Match([]common.Hash{txReqTopicA, txReqTopicB}, []common.Hash{txReqTopicB}),
		"required entries are OR-ed")
	require.False(t, Topic0Match([]common.Hash{txReqTopicA}, nil),
		"a restricted selector must not match a topicless log")
	require.False(t, Topic0Match([]common.Hash{txReqTopicA}, []common.Hash{txReqTopicOther}))
}

// TestSyncerConfigs_DeepSnapshot pins that SyncerConfigs returns a
// deterministic, deep-copied immutable snapshot: mutating it can never change
// the internal filters.
func TestSyncerConfigs_DeepSnapshot(t *testing.T) {
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
