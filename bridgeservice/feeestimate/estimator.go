package feeestimate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// Estimate source values (FeeEstimate.Source), part of the wire contract.
const (
	// sourceToken marks an estimate quoted from the origin token's own sample bucket.
	sourceToken = "token"
	// sourceClass marks an estimate quoted from the token-class fallback bucket.
	sourceClass = "class"
)

// abiWordLen is the byte length of one ABI-encoded return slot, the shape an address return
// value takes.
const abiWordLen = 32

// bridgeViewABIJSON declares only the bridge view method the claim estimator eth_calls:
// the wrapper lookup. Hand-declared instead of the full generated binding so the package
// stays light; the selector it packs is the well-known 0x22e95f2c.
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
  }
]`

// Estimator implements FeeEstimator for one network: it derives gas units from the
// transactions the syncers have already indexed on that network, classifies them (claims by
// first versus repeat, bridges by mechanism) and prices them with a live gas price read. One
// instance serves exactly one network; the bridge service holds one per network it serves.
//
// It fails closed (D1): every path that cannot produce a real estimate returns an error
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
	// txs fetches and caches transactions and receipts by hash.
	txs *txStore

	// now is the clock the caches and snapshots consult; tests replace it via setClock.
	now func() time.Time
}

// Estimator implements the interface the bridge service consumes.
var _ FeeEstimator = (*Estimator)(nil)

// New builds the estimator for one network. chain must not be nil; claims and bridges may
// be nil, in which case the matching estimate method fails closed with "not configured"
// (ErrFeeEstimateUnavailable) instead of returning anything.
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
	return &Estimator{
		cfg:        cfg,
		chain:      chain,
		claims:     claims,
		bridges:    bridges,
		viewABI:    viewABI,
		gasPrice:   newTTLResult[gasPriceSnapshot](cfg.GasPriceTTL, now),
		claimStats: newTTLResult[claimStats](cfg.StatsTTL, now),
		txs:        newTxStore(chain, cfg.TxCacheSize, cfg.RPCConcurrency),
		now:        now,
	}, nil
}

// setClock replaces the clock the estimator and its caches consult. It exists for tests;
// production code always uses time.Now.
func (e *Estimator) setClock(now func() time.Time) {
	e.now = now
	e.gasPrice.now = now
	e.claimStats.now = now
}

// EstimateBridgeFee is the bridge-side (per mechanism) estimate, built in a later step on
// top of this package's sampler, statistics and gas price code. Until then it fails closed
// rather than return anything: a nil bridges reader means the estimate is not configured,
// a configured one meets a not-yet-implemented estimator.
func (e *Estimator) EstimateBridgeFee(
	_ context.Context, _ common.Address,
) (*BridgeFeeEstimateResponse, error) {
	if e.bridges == nil {
		return nil, fmt.Errorf("%w: bridge fee estimation is not configured for network %d",
			ErrFeeEstimateUnavailable, e.cfg.NetworkID)
	}
	return nil, fmt.Errorf("%w: bridge fee estimation is not implemented yet",
		ErrFeeEstimateUnavailable)
}
