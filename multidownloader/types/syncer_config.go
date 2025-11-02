package types

import (
	"math/big"

	aggkitcommon "github.com/agglayer/aggkit/common"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
)

type SyncerID = string

type SyncerConfig struct {
	// SyncerID is the unique identifier for the syncer
	SyncerID SyncerID
	// ContractAddr is list of contract addresses to sync
	ContractsAddr []common.Address
	// Starting block
	FromBlock uint64
	// Taget for final block
	ToBlock             aggkittypes.BlockNumberFinality
	RequiredBlockHeader bool
}

func NewSyncerConfig(data aggkittypes.SyncerConfig) SyncerConfig {
	return SyncerConfig{
		SyncerID:            data.SyncerID,
		ContractsAddr:       data.ContractsAddr,
		FromBlock:           data.FromBlock,
		ToBlock:             data.ToBlock,
		RequiredBlockHeader: data.RequiredBlockHeader,
	}
}

// ContractConfig represents the configuration for a specific contract to be synced
// the same as SyncerConfig but for individual contracts
type ContractConfig struct {
	Address             common.Address
	FromBlock           uint64
	ToBlock             aggkittypes.BlockNumberFinality
	RequiredBlockHeader bool
	Syncers             []SyncerID
}

type SetSyncerConfig struct {
	filters map[SyncerID]SyncerConfig
}

func NewSetSyncerConfig() SetSyncerConfig {
	return SetSyncerConfig{
		filters: make(map[SyncerID]SyncerConfig),
	}
}

func (f *SetSyncerConfig) Add(filter SyncerConfig) {
	if f.filters == nil {
		f.filters = make(map[SyncerID]SyncerConfig)
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
			for _, addr := range filter.ContractsAddr {
				if _, exists := dups[addr]; !exists {
					addresses = append(addresses, addr)
					dups[addr] = struct{}{}
				}
			}
		}
	}
	return addresses
}

func (f *SetSyncerConfig) Finalities() []aggkittypes.BlockNumberFinality {
	if f == nil || f.filters == nil {
		return []aggkittypes.BlockNumberFinality{}
	}
	finalities := []aggkittypes.BlockNumberFinality{}
	dups := map[aggkittypes.BlockNumberFinality]struct{}{}

	for _, filter := range f.filters {
		if _, exists := dups[filter.ToBlock]; !exists {
			finalities = append(finalities, filter.ToBlock)
			dups[filter.ToBlock] = struct{}{}
		}
	}
	return finalities
}

func elementMatch(slice []SyncerID, element SyncerID) bool {
	for _, e := range slice {
		if e == element {
			return true
		}
	}
	return false
}

func (f *SetSyncerConfig) ContractConfigs() []ContractConfig {
	if f == nil || f.filters == nil {
		return []ContractConfig{}
	}
	contractMap := make(map[common.Address]*ContractConfig)
	for syncerID, filter := range f.filters {
		for _, addr := range filter.ContractsAddr {
			cc, exists := contractMap[addr]
			if !exists {
				cc = &ContractConfig{
					Address:             addr,
					FromBlock:           filter.FromBlock,
					ToBlock:             filter.ToBlock,
					Syncers:             []SyncerID{},
					RequiredBlockHeader: filter.RequiredBlockHeader,
				}
				contractMap[addr] = cc
			} else {
				// Update FromBlock and ToBlock if needed
				if filter.FromBlock < cc.FromBlock {
					cc.FromBlock = filter.FromBlock
				}
				if filter.ToBlock.LessFinalThan(cc.ToBlock) {
					cc.ToBlock = filter.ToBlock
				}
				cc.RequiredBlockHeader = cc.RequiredBlockHeader || filter.RequiredBlockHeader
			}
			if !elementMatch(cc.Syncers, syncerID) {
				cc.Syncers = append(cc.Syncers, syncerID)
			}
		}
	}
	// Convert map to slice
	contractConfigs := make([]ContractConfig, 0, len(contractMap))
	for _, cc := range contractMap {
		contractConfigs = append(contractConfigs, *cc)
	}
	return contractConfigs
}

// Combine all filters into one, must check if the blockRange overlaps
func (f *SetSyncerConfig) Combine(blockRange aggkitcommon.BlockRange) ethereum.FilterQuery {
	// Trivial implementation
	return ethereum.FilterQuery{
		Addresses: f.Addresses(blockRange),
		FromBlock: new(big.Int).SetUint64(blockRange.FromBlock),
		ToBlock:   new(big.Int).SetUint64(blockRange.ToBlock),
	}
}

// SyncSegments group the SetSyncerConfig into segments per contract address and blockRange
func (f *SetSyncerConfig) SyncSegments() (*SetSyncSegment, error) {
	segments := NewSetSyncSegment()
	// Trivial implementation, it have to be improved to group by
	// contract address and block range
	for _, filter := range f.filters {
		// TODO: instead of calling RPC use block_notifier_values
		for _, addr := range filter.ContractsAddr {
			segment := SyncSegment{
				ContractAddr: addr,
				// Initially set ToBlock as 0, it will be updated later
				BlockRange:          aggkitcommon.NewBlockRange(filter.FromBlock, 0),
				TargetToBlock:       filter.ToBlock,
				RequiredBlockHeader: filter.RequiredBlockHeader,
			}
			segments.Add(segment)
		}
	}
	return &segments, nil
}
