package feeestimate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/jellydator/ttlcache/v3"
)

// Estimate source values (FeeEstimate.Source), part of the wire contract.
const (
	// sourceToken marks an estimate quoted from the origin token's own sample bucket.
	sourceToken = "token"
	// sourceClass marks an estimate quoted from the token-class fallback bucket.
	sourceClass = "class"
	// sourceMechanism marks an estimate quoted from a bridging-mechanism bucket.
	sourceMechanism = "mechanism"
)

// abiWordLen is the byte length of one ABI-encoded return slot, the shape an address return
// value takes.
const abiWordLen = 32

// bridgeViewABIJSON declares only the bridge view methods the estimator eth_calls: the
// claim side's wrapper lookup, the bridge side's wrapped-token lookup, and the gas-token
// identity pair the claim-side classification consults. Hand-declared
// instead of the full generated binding so the package stays light; the selectors they pack
// are the well-known 0x22e95f2c (getTokenWrappedAddress), 0x318aee3d
// (wrappedTokenToTokenInfo), 0x3c351e10 (gasTokenAddress) and 0x3cbc795b
// (gasTokenNetwork).
const bridgeViewABIJSON = `[
  {
    "type": "function",
    "name": "getTokenWrappedAddress",
    "stateMutability": "view",
    "inputs": [
      {"name": "originNetwork", "type": "uint32", "internalType": "uint32"},
      {"name": "originTokenAddress", "type": "address", "internalType": "address"}
    ],
    "outputs": [
      {"name": "", "type": "address", "internalType": "address"}
    ]
  },
  {
    "type": "function",
    "name": "wrappedTokenToTokenInfo",
    "stateMutability": "view",
    "inputs": [
      {"name": "wrappedAddress", "type": "address", "internalType": "address"}
    ],
    "outputs": [
      {"name": "originNetwork", "type": "uint32", "internalType": "uint32"},
      {"name": "originTokenAddress", "type": "address", "internalType": "address"}
    ]
  },
  {
    "type": "function",
    "name": "gasTokenAddress",
    "stateMutability": "view",
    "inputs": [],
    "outputs": [
      {"name": "", "type": "address", "internalType": "address"}
    ]
  },
  {
    "type": "function",
    "name": "gasTokenNetwork",
    "stateMutability": "view",
    "inputs": [],
    "outputs": [
      {"name": "", "type": "uint32", "internalType": "uint32"}
    ]
  }
]`

// Estimator implements FeeEstimator for one network: it derives gas units from the
// transactions the syncers have already indexed on that network, classifies them (claims by
// first versus repeat, bridges by mechanism) and prices them with a live gas price read. One
// instance serves exactly one network; the bridge service holds one per network it serves.
//
// It fails closed: every path that cannot produce a real estimate returns an error
// wrapping ErrFeeEstimateUnavailable rather than a default value.
type Estimator struct {
	cfg     Config
	chain   ChainReader
	claims  ClaimsReader
	bridges BridgesReader
	viewABI abi.ABI

	// gasPrice caches the live gas price snapshot for Config.GasPriceTTL.
	gasPrice *ttlResult[gasPriceSnapshot]
	// claimStats caches the claim gas-unit statistics snapshot for Config.StatsTTL.
	claimStats *ttlResult[claimStats]
	// bridgeStats caches the bridge gas-unit statistics snapshot for Config.StatsTTL.
	bridgeStats *ttlResult[bridgeStats]
	// txs fetches and caches transactions and receipts by hash.
	txs *txStore
	// wrappedAddresses caches getTokenWrappedAddress answers by origin token, and
	// tokenInfos caches wrappedTokenToTokenInfo answers by wrapped address, both for
	// Config.TokenFactsTTL: wrapper existence and token mappings are stable on-chain
	// state, so reusing them keeps an unauthenticated caller from fanning the same
	// eth_calls out at request rate.
	wrappedAddresses *ttlcache.Cache[tokenKey, common.Address]
	tokenInfos       *ttlcache.Cache[common.Address, tokenInfo]
	// gasToken is the origin identity of the network's own native gas token, read from
	// the bridge contract once at construction (readGasTokenPair): on a custom-gas-token
	// L2 the gas token arrives as that (origin network, origin token) pair instead of
	// the plain 0x0 marker. The zero pair means the network's gas token is plain native
	// currency (L1 always, and any L2 whose bridge answers zero or fails the read).
	gasToken tokenKey

	// now is the clock the caches and snapshots consult; tests replace it via setClock.
	now func() time.Time
}

// Estimator implements the interface the bridge service consumes.
var _ FeeEstimator = (*Estimator)(nil)

// New builds the estimator for one network. chain must not be nil; claims and bridges may
// be nil, in which case the matching estimate method fails closed with "not configured"
// (ErrFeeEstimateUnavailable) instead of returning anything. For an L2 (NetworkID != 0) the
// bridge's gas-token identity is eth_called once here; the read is fail-soft (see
// readGasTokenPair) and never fails construction.
func New(cfg Config, chain ChainReader, claims ClaimsReader, bridges BridgesReader) (*Estimator, error) {
	if chain == nil {
		return nil, errors.New("feeestimate: chain reader must not be nil")
	}
	viewABI, err := abi.JSON(strings.NewReader(bridgeViewABIJSON))
	if err != nil {
		return nil, fmt.Errorf("feeestimate: parsing the bridge view ABI: %w", err)
	}

	cfg = cfg.withDefaults()
	now := time.Now
	e := &Estimator{
		cfg:              cfg,
		chain:            chain,
		claims:           claims,
		bridges:          bridges,
		viewABI:          viewABI,
		gasPrice:         newTTLResult[gasPriceSnapshot](cfg.GasPriceTTL, cfg.ComputeTimeout, now),
		claimStats:       newTTLResult[claimStats](cfg.StatsTTL, cfg.ComputeTimeout, now),
		bridgeStats:      newTTLResult[bridgeStats](cfg.StatsTTL, cfg.ComputeTimeout, now),
		txs:              newTxStore(chain, cfg.TxCacheSize, cfg.RPCConcurrency),
		wrappedAddresses: newViewCache[tokenKey, common.Address](cfg.TokenFactsTTL),
		tokenInfos:       newViewCache[common.Address, tokenInfo](cfg.TokenFactsTTL),
		now:              now,
	}

	// Network 0 is L1 by convention, whose gas token is plain native ETH: no read. An L2's
	// gas-token identity never changes, so once is enough; a read that fails (an older
	// bridge without the view methods, or a briefly unreachable chain at startup) is
	// tolerated as "no custom gas token" rather than failing startup, trading a
	// misclassification until the next restart for a fail-soft constructor.
	if cfg.NetworkID != 0 {
		readCtx, cancel := context.WithTimeout(context.Background(), cfg.ComputeTimeout)
		pair, err := e.readGasTokenPair(readCtx)
		cancel()
		if err != nil {
			log.Debugf("feeestimate: reading the gas token identity of network %d from bridge %s failed; "+
				"treating the network as having no custom gas token: %v",
				cfg.NetworkID, cfg.BridgeAddr.Hex(), err)
		} else {
			e.gasToken = pair
		}
	}

	return e, nil
}

// setClock replaces the clock the estimator and its caches consult. It exists for tests;
// production code always uses time.Now.
func (e *Estimator) setClock(now func() time.Time) {
	e.now = now
	e.gasPrice.now = now
	e.claimStats.now = now
	e.bridgeStats.now = now
}
