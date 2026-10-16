package feeestimate

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/agglayer/aggkit/bridgeservice/types"
)

// gasPriceSnapshot is one live gas price reading together with when it was taken.
type gasPriceSnapshot struct {
	info   GasPriceInfo
	readAt time.Time
}

// price is the effective gas price the snapshot carries, as a big.Int for cost arithmetic.
func (s gasPriceSnapshot) price() *big.Int {
	return s.info.GasPrice.ToBigInt()
}

// readGasPrice takes the live gas price reading: on an EIP-1559 chain
// (the latest header carries a base fee) it is the base fee plus eth_maxPriorityFeePerGas,
// with all three reported; on a chain without a base fee it is eth_gasPrice and the base fee
// and priority fee are null.
func (e *Estimator) readGasPrice(ctx context.Context) (gasPriceSnapshot, error) {
	header, err := e.chain.HeaderByNumber(ctx, nil)
	if err != nil {
		return gasPriceSnapshot{}, fmt.Errorf("reading latest header: %w", err)
	}
	if header == nil || header.Number == nil {
		return gasPriceSnapshot{}, fmt.Errorf("latest header carries no block number")
	}

	snapshot := gasPriceSnapshot{readAt: e.now()}
	if header.BaseFee != nil {
		tip, err := e.chain.SuggestGasTipCap(ctx)
		if err != nil {
			return gasPriceSnapshot{}, fmt.Errorf("suggesting priority fee: %w", err)
		}
		if tip == nil {
			return gasPriceSnapshot{}, fmt.Errorf("suggested priority fee is nil")
		}
		total := new(big.Int).Add(header.BaseFee, tip)
		snapshot.info = GasPriceInfo{
			BaseFee:     bigIntStringPtr(header.BaseFee),
			PriorityFee: bigIntStringPtr(tip),
			GasPrice:    types.BigIntString(total.String()),
			BlockNumber: header.Number.Uint64(),
		}
		return snapshot, nil
	}

	price, err := e.chain.SuggestGasPrice(ctx)
	if err != nil {
		return gasPriceSnapshot{}, fmt.Errorf("suggesting gas price: %w", err)
	}
	if price == nil {
		return gasPriceSnapshot{}, fmt.Errorf("suggested gas price is nil")
	}
	snapshot.info = GasPriceInfo{
		GasPrice:    types.BigIntString(price.String()),
		BlockNumber: header.Number.Uint64(),
	}
	return snapshot, nil
}

// bigIntStringPtr converts v to the pointer decimal-string shape GasPriceInfo.BaseFee and
// GasPriceInfo.PriorityFee take.
func bigIntStringPtr(v *big.Int) *types.BigIntString {
	s := types.BigIntString(v.String())
	return &s
}
