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
	for syncerID, filter := range f.filters {
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
func (f *SetSyncerConfig) SyncSegments() (*SetSyncSegment, error) {
	segments := NewSetSyncSegment()
	// Trivial implementation; it needs to be improved to group by
	// contract address and block range
	for _, filter := range f.filters {
		// TODO: instead of calling RPC use block_notifier_values
		for _, addr := range filter.ContractAddresses {
			segment := SyncSegment{
				ContractAddr: addr,
				// Initially set ToBlock as 0; it will be updated later
				BlockRange:    aggkitcommon.NewBlockRange(filter.FromBlock, 0),
				TargetToBlock: filter.ToBlock,
			}
			segments.Add(segment)
		}
	}
	return &segments, nil
}

// convertContractMapToSlice converts map to slice
func convertContractMapToSlice(contractMap map[common.Address]*ContractConfig) []ContractConfig {
	contractConfigs := make([]ContractConfig, 0, len(contractMap))
	for _, cc := range contractMap {
		contractConfigs = append(contractConfigs, *cc)
	}
	return contractConfigs
}
