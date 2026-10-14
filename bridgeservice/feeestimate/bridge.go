package feeestimate

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/jellydator/ttlcache/v3"
)

var (
	// bridgeEventTopic is the BridgeEvent the bridge emits on every bridge, the one log a
	// sampled bridge transaction must hold exactly once. The signature mirrors
	// bridgesync/downloader.go so the estimator and the syncer agree on what counts as a
	// bridge.
	bridgeEventTopic = crypto.Keccak256Hash([]byte(
		"BridgeEvent(uint8,uint32,address,uint32,address,uint256,bytes,uint32)"))
	// bridgeEventTopics are the topics a bridge-emitted log must carry to count as one
	// bridge.
	bridgeEventTopics = []common.Hash{bridgeEventTopic}
)

// bridgeLeafTypeAsset is the leaf type of an asset bridge; message leaves are not sampled
// (they run arbitrary receiver code).
const bridgeLeafTypeAsset = uint8(bridgesynctypes.LeafTypeAsset)

// tokenInfoWords is the number of ABI-encoded words wrappedTokenToTokenInfo returns: the
// origin network and the origin token address.
const tokenInfoWords = 2

// bridgeStats is the gas-unit statistics snapshot of the bridge window on the estimator's
// network: the buckets the bridge estimate reads. It is computed from the latest bridges the
// bridges reader has indexed and their transactions, and reused for Config.StatsTTL.
type bridgeStats struct {
	// computedAt is when the snapshot computation started; the freshest data in it is at
	// least this old.
	computedAt time.Time
	// byMechanism holds the gasUsed of asset bridges by bridging mechanism.
	byMechanism map[Mechanism][]uint64
}

// tokenInfo is the (origin network, origin token) pair the bridge maps a wrapped token
// back to: the value shape of the wrappedTokenToTokenInfo view.
type tokenInfo struct {
	originNetwork uint32
	originToken   common.Address
}

// EstimateBridgeFee estimates the cost of bridging token out of the estimator's own network
// (the bridge origin), grouped by bridging mechanism with no per-token breakdown. It
// samples the latest indexed bridges, resolves which mechanism a bridge of token would use,
// and prices that mechanism's bucket with a live gas price read. It fails closed: when
// the mechanism's bucket has no samples or a required RPC call fails, it returns an error
// wrapping ErrFeeEstimateUnavailable instead of a default value.
func (e *Estimator) EstimateBridgeFee(
	ctx context.Context, token common.Address,
) (*BridgeFeeEstimateResponse, error) {
	if e.bridges == nil {
		return nil, fmt.Errorf("%w: bridge fee estimation is not configured for network %d",
			ErrFeeEstimateUnavailable, e.cfg.NetworkID)
	}

	stats, err := e.bridgeStats.get(ctx, e.collectBridgeStats)
	if err != nil {
		return nil, fmt.Errorf("%w: computing the bridge statistics: %w", ErrFeeEstimateUnavailable, err)
	}
	gas, err := e.gasPrice.get(ctx, e.readGasPrice)
	if err != nil {
		return nil, fmt.Errorf("%w: computing the gas price: %w", ErrFeeEstimateUnavailable, err)
	}
	mechanism, err := e.resolveMechanism(ctx, token)
	if err != nil {
		return nil, err
	}

	samples := stats.byMechanism[mechanism]
	if len(samples) < e.cfg.MinSamples {
		return nil, fmt.Errorf("%w: no %s samples in the latest %d bridges of network %d (need %d)",
			ErrFeeEstimateUnavailable, mechanism, e.cfg.Window, e.cfg.NetworkID, e.cfg.MinSamples)
	}
	gasUnits := statisticOf(samples, e.cfg.Percentile)

	// The oldest input the estimate reflects: statistics snapshot or gas price read.
	computedAt := stats.computedAt
	if gas.readAt.Before(computedAt) {
		computedAt = gas.readAt
	}

	return &BridgeFeeEstimateResponse{
		NetworkID:    e.cfg.NetworkID,
		TokenAddress: types.Address(token.Hex()),
		Mechanism:    mechanism,
		GasPrice:     gas.info,
		Estimate: FeeEstimate{
			GasUnits:    gasUnits,
			Cost:        costOf(gasUnits, gas.price()),
			SampleCount: len(samples),
			Source:      sourceMechanism,
		},
		ComputedAt: uint64(computedAt.Unix()),
	}, nil
}

// resolveMechanism resolves which bridging mechanism a bridge of token uses on the
// estimator's network: the zero address is the network's native gas token bridged via
// msg.value; a token the bridge maps back to an origin (wrappedTokenToTokenInfo non-zero)
// is a wrapped token burned on this network; anything else is an origin-native ERC20 locked
// in the bridge.
func (e *Estimator) resolveMechanism(ctx context.Context, token common.Address) (Mechanism, error) {
	if token == (common.Address{}) {
		return Native, nil
	}
	originNetwork, originToken, err := e.wrappedTokenToTokenInfo(ctx, token)
	if err != nil {
		return "", err
	}
	if originNetwork != 0 || originToken != (common.Address{}) {
		return WrappedBurn, nil
	}
	return ERC20Lock, nil
}

// wrappedTokenToTokenInfo eth_calls the bridge's wrappedTokenToTokenInfo(wrappedAddress)
// and returns the (origin network, origin token) pair the token maps back to, or the zero
// pair when the token is not a wrapped token the bridge knows. Answers — the zero pair
// included, since "not a wrapped token" is a fact — are cached per token for
// Config.TokenFactsTTL, mirroring the claim side's wrapper lookup: the mapping is stable
// on-chain state, so an unauthenticated endpoint must not let one eth_call per request fan
// out at request rate. The calldata is packed with
// the same hand-declared ABI as the wrapper lookup so the package needs no generated
// binding; the selector it produces is the well-known 0x318aee3d.
func (e *Estimator) wrappedTokenToTokenInfo(
	ctx context.Context, token common.Address,
) (uint32, common.Address, error) {
	if item := e.tokenInfos.Get(token); item != nil {
		info := item.Value()
		return info.originNetwork, info.originToken, nil
	}
	calldata, err := e.viewABI.Pack("wrappedTokenToTokenInfo", token)
	if err != nil {
		// The ABI is a fixed constant, so a packing failure is a programming error rather
		// than an RPC failure; it does not map to 503.
		return 0, common.Address{}, fmt.Errorf("packing wrappedTokenToTokenInfo: %w", err)
	}
	output, err := e.chain.CallContract(ctx, ethereum.CallMsg{To: &e.cfg.BridgeAddr, Data: calldata}, nil)
	if err != nil {
		return 0, common.Address{}, fmt.Errorf(
			"%w: calling wrappedTokenToTokenInfo on the bridge: %w", ErrFeeEstimateUnavailable, err)
	}
	if len(output) != tokenInfoWords*abiWordLen {
		return 0, common.Address{}, fmt.Errorf(
			"%w: wrappedTokenToTokenInfo returned %d bytes, want %d",
			ErrFeeEstimateUnavailable, len(output), tokenInfoWords*abiWordLen)
	}
	info := tokenInfo{
		originNetwork: uint32(new(big.Int).SetBytes(output[:abiWordLen]).Uint64()),
		originToken:   common.BytesToAddress(output[abiWordLen:]),
	}
	// Only successful calls are cached; a failed one is retried by the next request.
	e.tokenInfos.Set(token, info, ttlcache.DefaultTTL)
	return info.originNetwork, info.originToken, nil
}

// carriesValue reports whether the observed transaction moved native value with it: the
// marker of a native gas-token bridge, the first mechanism rule. It also covers
// custom-gas-token L2s, where the native token's BridgeEvent carries the gas-token origin
// network rather than the network the bridge runs on, so msg.value decides the mechanism
// before the origin network does.
func carriesValue(obs *txObservation) bool {
	value := obs.tx.Value()
	return value != nil && value.Sign() > 0
}

// classifyBridgeObservation applies the bridge-side eligibility rules to one
// observation-row pair: the transaction must have succeeded and gone directly to the
// bridge, and its receipt must hold exactly one bridge-emitted BridgeEvent. It reports the
// gas the bridge actually used and the bridging mechanism it performed: msg.value
// makes a native bridge; without it, a token native to this network is locked and anything
// else is a wrapped token burned.
func classifyBridgeObservation(
	obs *txObservation, bridgeAddr common.Address, networkID uint32, row *bridgesync.Bridge,
) (gasUsed uint64, mechanism Mechanism, ok bool) {
	if !isSuccessfulBridgeTx(obs, bridgeAddr) {
		return 0, "", false
	}
	if countBridgeLogs(obs, bridgeAddr, bridgeEventTopics) != 1 {
		return 0, "", false
	}
	if carriesValue(obs) {
		return obs.receipt.GasUsed, Native, true
	}
	if row.OriginNetwork == networkID {
		return obs.receipt.GasUsed, ERC20Lock, true
	}
	return obs.receipt.GasUsed, WrappedBurn, true
}

// collectBridgeStats pages through the latest Config.Window bridges the reader has indexed
// (newest first, no filters), keeps the asset bridges that are real user transactions,
// fetches each transaction with its receipt, applies the eligibility rules and buckets the
// survivors by mechanism. It is the compute function behind the bridge
// statistics TTL cache.
func (e *Estimator) collectBridgeStats(ctx context.Context) (bridgeStats, error) {
	stats := bridgeStats{
		computedAt:  e.now(),
		byMechanism: make(map[Mechanism][]uint64),
	}
	bridges, err := e.pageBridges(ctx, e.cfg.Window)
	if err != nil {
		return bridgeStats{}, err
	}

	// One entry per transaction: a transaction is only eligible when it performed exactly
	// one bridge, and its row carries the origin network the mechanism is derived from.
	rows := make(map[common.Hash]*bridgesync.Bridge, len(bridges))
	order := make([]common.Hash, 0, len(bridges))
	for _, bridge := range bridges {
		if bridge == nil || bridge.LeafType != bridgeLeafTypeAsset || bridge.Source != "" {
			continue // message leaves and synthetic LET rows are excluded: not user bridges
		}
		if _, dup := rows[bridge.TxHash]; dup {
			continue
		}
		rows[bridge.TxHash] = bridge
		order = append(order, bridge.TxHash)
	}

	observations, err := e.txs.fetch(ctx, order)
	if err != nil {
		return bridgeStats{}, err
	}

	for _, hash := range order {
		observation, ok := observations[hash]
		if !ok {
			continue // unknown or pending transaction: not a sample
		}
		gasUsed, mechanism, ok := classifyBridgeObservation(
			observation, e.cfg.BridgeAddr, e.cfg.NetworkID, rows[hash])
		if !ok {
			continue
		}
		stats.byMechanism[mechanism] = append(stats.byMechanism[mechanism], gasUsed)
	}
	return stats, nil
}

// pageBridges returns the newest limit bridge rows the reader has indexed, with no filters.
// Pages are requested newest first at one constant page size (the bridge service's
// MaxPageSize, or the window when smaller), because the reader keys a page's offset to the
// page size of the call; the last page may therefore deliver more rows than the window
// needs, and the result is trimmed down to the window.
func (e *Estimator) pageBridges(ctx context.Context, limit int) ([]*bridgesync.Bridge, error) {
	pageSize := min(limit, defaultWindow)
	bridges := make([]*bridgesync.Bridge, 0, limit)
	for page := uint32(1); ; page++ {
		pageRows, count, err := e.bridges.GetBridgesPaged(ctx, page, uint32(pageSize), nil, nil, "")
		if err != nil {
			return nil, fmt.Errorf("paging bridges (page %d, size %d): %w", page, pageSize, err)
		}
		if len(pageRows) == 0 {
			break
		}
		bridges = append(bridges, pageRows...)
		if len(bridges) >= count || len(bridges) >= limit || len(pageRows) < pageSize {
			break // saw every bridge the reader holds, filled the window, or hit the last page
		}
	}
	if len(bridges) > limit {
		bridges = bridges[:limit]
	}
	return bridges, nil
}
