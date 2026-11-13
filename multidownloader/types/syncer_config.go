package types

import (
	"fmt"
	"sort"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

type SyncerID = string

// ContractConfig represents the configuration for a specific contract to be synced,
// the same as SyncerConfig but for individual contracts
type ContractConfig struct {
	Address             common.Address
	FromBlock           uint64
	ToBlock             aggkittypes.BlockNumberFinality
	RequiredBlockHeader bool
	Syncers             []SyncerID
}

func NewContractConfigFromSyncerConfig(address common.Address,
	syncerConfig aggkittypes.SyncerConfig) *ContractConfig {
	return &ContractConfig{
		Address:   address,
		FromBlock: syncerConfig.FromBlock,
		ToBlock:   syncerConfig.ToBlock,
		Syncers:   []SyncerID{syncerConfig.SyncerID},
	}
}

func (c *ContractConfig) Update(syncerConfig aggkittypes.SyncerConfig) {
	if syncerConfig.FromBlock < c.FromBlock {
		c.FromBlock = syncerConfig.FromBlock
	}
	lessFinal, err := syncerConfig.ToBlock.LessFinalThan(c.ToBlock)
	if err != nil {
		// In case of error, we do not update ToBlock
		log.Warnf("ContractConfig.Update: cannot compare ToBlock finality: %v", err)
		return
	}
	if lessFinal {
		c.ToBlock = syncerConfig.ToBlock
	}
	if !elementMatch(c.Syncers, syncerConfig.SyncerID) {
		c.Syncers = append(c.Syncers, syncerConfig.SyncerID)
		sort.Strings(c.Syncers)
	}
}

type SetSyncerConfig struct {
	filters map[SyncerID]aggkittypes.SyncerConfig
}

func NewSetSyncerConfig() SetSyncerConfig {
	return SetSyncerConfig{
		filters: make(map[SyncerID]aggkittypes.SyncerConfig),
	}
}
func (f *SetSyncerConfig) Brief() string {
	if f == nil || f.filters == nil {
		return "SetSyncerConfig{<nil>}"
	}
	result := "SetSyncerConfig{ "
	// Sort syncer IDs to ensure deterministic output
	syncerIDs := make([]string, 0, len(f.filters))
	for syncerID := range f.filters {
		syncerIDs = append(syncerIDs, syncerID)
	}
	sort.Strings(syncerIDs)
	for _, syncerID := range syncerIDs {
		filter := f.filters[syncerID]
		result += fmt.Sprintf("(%s -> [%d - %s]) ", syncerID, filter.FromBlock, filter.ToBlock.String())
	}
	result += "}"
	return result
}
func (f *SetSyncerConfig) Add(filter aggkittypes.SyncerConfig) {
	if f.filters == nil {
		f.filters = make(map[SyncerID]aggkittypes.SyncerConfig)
	}
	f.filters[filter.SyncerID] = filter
}

// Addresses returns the unique list of contract addresses from all filters within the specified block range
// TODO: check blockRange
func (f *SetSyncerConfig) Addresses(blockRange aggkitcommon.BlockRange) []common.Address {
	if f == nil || f.filters == nil {
		return []common.Address{}
	}
	// Trivial implementation
	addresses := []common.Address{}
	dups := map[common.Address]struct{}{}

	for _, filter := range f.filters {
		if filter.FromBlock >= blockRange.FromBlock {
			for _, addr := range filter.ContractAddresses {
				if _, exists := dups[addr]; !exists {
					addresses = append(addresses, addr)
					dups[addr] = struct{}{}
				}
			}
		}
	}
	return addresses
}

// ContractTransactionRequirement is a derived transaction requirement for a
// single (syncer, contract address) registration. It preserves the exact
// per-syncer FromBlock frontier, Topic0 selector and ToBlock target, so the
// distinct per-syncer requirements are never lossily merged: a syncer that only
// requires topicA from block 100 never forces another syncer's topicB log at
// block 100. An empty Topic0 means "any log, including topicless logs".
type ContractTransactionRequirement struct {
	Address   common.Address
	FromBlock uint64
	Topic0    []common.Hash
	// ToBlock is the target finality of the originating syncer registration.
	// Tag finalities (Latest/Safe/Finalized) cannot be resolved here; the worker
	// resolves the numeric upper bound against the chain.
	ToBlock aggkittypes.BlockNumberFinality
	Syncers []SyncerID
}

// String returns a human readable representation of the requirement.
func (r *ContractTransactionRequirement) String() string {
	if r == nil {
		return "<nil>"
	}
	return fmt.Sprintf(
		"ContractTransactionRequirement{addr=%s, fromBlock=%d, toBlock=%s, topic0=%v, syncers=%v}",
		r.Address.Hex(), r.FromBlock, r.ToBlock.String(), r.Topic0, r.Syncers)
}

// ContractTransactionRequirements derives the transaction requirements from
// every syncer config whose TransactionRequirements are enabled. One
// requirement is emitted per (syncer, contract address) registration and the
// results are sorted deterministically; no per-address merging is performed so
// the address/from/topic/ToBlock association is never lost. Disabled
// requirements (zero value) are ignored and produce no entry.
func (f *SetSyncerConfig) ContractTransactionRequirements() []ContractTransactionRequirement {
	if f == nil || f.filters == nil {
		return []ContractTransactionRequirement{}
	}
	result := make([]ContractTransactionRequirement, 0, len(f.filters))
	for _, filter := range f.filters {
		if !filter.TransactionRequirements.Enabled {
			continue
		}
		for _, addr := range filter.ContractAddresses {
			result = append(result, ContractTransactionRequirement{
				Address:   addr,
				FromBlock: filter.TransactionRequirements.FromBlock,
				Topic0:    copyHashes(filter.TransactionRequirements.Topic0),
				ToBlock:   filter.ToBlock,
				Syncers:   []SyncerID{filter.SyncerID},
			})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Address != result[j].Address {
			return result[i].Address.Hex() < result[j].Address.Hex()
		}
		if result[i].ToBlock.String() != result[j].ToBlock.String() {
			return result[i].ToBlock.String() < result[j].ToBlock.String()
		}
		if result[i].FromBlock != result[j].FromBlock {
			return result[i].FromBlock < result[j].FromBlock
		}
		return firstSyncer(result[i].Syncers) < firstSyncer(result[j].Syncers)
	})
	return result
}

// SyncerConfigs returns an immutable snapshot of the registered syncer configs,
// sorted by SyncerID for deterministic ordering. The returned slice and every
// nested slice are copied so callers cannot mutate the internal filters.
func (f *SetSyncerConfig) SyncerConfigs() []aggkittypes.SyncerConfig {
	if f == nil || f.filters == nil {
		return []aggkittypes.SyncerConfig{}
	}
	result := make([]aggkittypes.SyncerConfig, 0, len(f.filters))
	for _, cfg := range f.filters {
		result = append(result, copySyncerConfig(cfg))
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].SyncerID < result[j].SyncerID
	})
	return result
}

// copySyncerConfig returns a deep copy of cfg so callers cannot mutate the
// nested ContractAddresses/Topic0 slices held by SetSyncerConfig.
func copySyncerConfig(cfg aggkittypes.SyncerConfig) aggkittypes.SyncerConfig {
	out := cfg
	if cfg.ContractAddresses != nil {
		out.ContractAddresses = append([]common.Address(nil), cfg.ContractAddresses...)
	}
	if cfg.TransactionRequirements.Topic0 != nil {
		out.TransactionRequirements.Topic0 = append([]common.Hash(nil), cfg.TransactionRequirements.Topic0...)
	}
	return out
}

// copyHashes returns a copy of h (nil-safe) so callers cannot mutate the
// filters held by SetSyncerConfig.
func copyHashes(h []common.Hash) []common.Hash {
	if h == nil {
		return nil
	}
	return append([]common.Hash(nil), h...)
}

// firstSyncer returns the first syncer ID or "" for an empty list.
func firstSyncer(syncers []SyncerID) SyncerID {
	if len(syncers) == 0 {
		return ""
	}
	return syncers[0]
}

// RequiresTransaction reports whether the canonical log at the given contract
// address and block number, whose topics are provided, requires transaction
// data. The second return value is the matched per-registration requirement
// (preserving that syncer's FromBlock/Topic0/ToBlock).
//
// Matching rules: the requirement must be enabled, the address must be declared
// by the syncer, blockNumber must be >= max(syncer FromBlock, requirement
// FromBlock) and, when the requirement has a non-empty Topic0 list, the log
// must carry a topic0 that is part of that list (entries are OR-ed). An empty
// Topic0 list matches every log, including topicless ones. When the syncer's
// ToBlock is numeric it also bounds the match; tag finalities
// (Latest/Safe/Finalized) cannot be resolved here, so the worker resolves the
// upper bound against the chain. It is safe with nil/empty filters and never
// panics.
func (f *SetSyncerConfig) RequiresTransaction(addr common.Address, blockNumber uint64,
	topics []common.Hash) (ContractTransactionRequirement, bool) {
	if f == nil || f.filters == nil {
		return ContractTransactionRequirement{}, false
	}
	// Deterministic order so the returned requirement is stable when several
	// syncers match the same log.
	ids := make([]string, 0, len(f.filters))
	for id := range f.filters {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		filter := f.filters[id]
		tr := filter.TransactionRequirements
		if !tr.Enabled {
			continue
		}
		if !addressMatch(filter.ContractAddresses, addr) {
			continue
		}
		// The effective frontier is the later of the syncer log FromBlock and
		// the requirement FromBlock: a log before either frontier is not
		// required.
		lower := tr.FromBlock
		if filter.FromBlock > lower {
			lower = filter.FromBlock
		}
		if blockNumber < lower {
			continue
		}
		if filter.ToBlock.Block.IsConstant() && blockNumber > filter.ToBlock.Specific {
			continue
		}
		if !Topic0Match(tr.Topic0, topics) {
			continue
		}
		return ContractTransactionRequirement{
			Address:   addr,
			FromBlock: tr.FromBlock,
			Topic0:    copyHashes(tr.Topic0),
			ToBlock:   filter.ToBlock,
			Syncers:   []SyncerID{filter.SyncerID},
		}, true
	}
	return ContractTransactionRequirement{}, false
}

// Topic0Match returns true when requiredTopics is empty (match any log,
// including topicless) or when the log carries a topic0 contained in
// requiredTopics. Entries in requiredTopics are OR-ed.
func Topic0Match(requiredTopics []common.Hash, logTopics []common.Hash) bool {
	if len(requiredTopics) == 0 {
		return true
	}
	if len(logTopics) == 0 {
		return false
	}
	for _, required := range requiredTopics {
		if required == logTopics[0] {
			return true
		}
	}
	return false
}

// addressMatch returns true when addr is contained in addrs.
func addressMatch(addrs []common.Address, addr common.Address) bool {
	for _, a := range addrs {
		if a == addr {
			return true
		}
	}
	return false
}

func elementMatch(slice []SyncerID, element SyncerID) bool {
	for _, e := range slice {
		if e == element {
			return true
		}
	}
	return false
}

// ContractConfigs combines the SyncerConfig into ContractConfig per contract address
func (f *SetSyncerConfig) ContractConfigs() []ContractConfig {
	if f == nil || f.filters == nil {
		return []ContractConfig{}
	}
	contractMap := make(map[common.Address]*ContractConfig)
	for _, filter := range f.filters {
		for _, addr := range filter.ContractAddresses {
			cc, exists := contractMap[addr]
			if !exists {
				contractMap[addr] = NewContractConfigFromSyncerConfig(addr, filter)
			} else {
				// Update FromBlock and ToBlock if needed
				cc.Update(filter)
			}
		}
	}
	return convertContractMapToSlice(contractMap)
}

// SyncSegments groups the SetSyncerConfig into segments per contract address and blockRange
func (f *SetSyncerConfig) SyncSegments(
	blockNumbers map[aggkittypes.BlockNumberFinality]uint64) (*SetSyncSegment, error) {
	segments := NewSetSyncSegment()
	// Trivial implementation; it needs to be improved to group by
	// contract address and block range
	for _, filter := range f.filters {
		// TODO: instead of calling RPC use block_notifier_values
		for _, addr := range filter.ContractAddresses {
			toBlock, ok := blockNumbers[filter.ToBlock]
			if !ok {
				return nil, fmt.Errorf("SyncSegments: block number for finality %s not found", filter.ToBlock.String())
			}
			segment := SyncSegment{
				ContractAddr:  addr,
				BlockRange:    aggkitcommon.NewBlockRange(filter.FromBlock, toBlock),
				TargetToBlock: filter.ToBlock,
			}
			segments.Add(segment)
		}
	}
	return &segments, nil
}

// GetTargetToBlockTags returns the list of TargetToBlock tags in the
// SetSyncSegment witout duplicates
func (f *SetSyncerConfig) GetTargetToBlockTags() []aggkittypes.BlockNumberFinality {
	if f == nil {
		return nil
	}
	result := make([]aggkittypes.BlockNumberFinality, 0, len(f.filters))
	for _, segment := range f.filters {
		// if it's already in list don't add it again
		exists := false
		for _, existing := range result {
			if existing == segment.ToBlock {
				exists = true
				break
			}
		}
		if !exists {
			result = append(result, segment.ToBlock)
		}
	}
	return result
}

// convertContractMapToSlice converts map to slice
func convertContractMapToSlice(contractMap map[common.Address]*ContractConfig) []ContractConfig {
	contractConfigs := make([]ContractConfig, 0, len(contractMap))
	for _, cc := range contractMap {
		contractConfigs = append(contractConfigs, *cc)
	}
	// Sort by address to ensure deterministic output
	sort.Slice(contractConfigs, func(i, j int) bool {
		return contractConfigs[i].Address.Hex() < contractConfigs[j].Address.Hex()
	})
	return contractConfigs
}
