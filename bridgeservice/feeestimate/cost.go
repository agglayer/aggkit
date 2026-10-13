package feeestimate

import (
	"math/big"

	"github.com/agglayer/aggkit/bridgeservice/types"
)

// costOf returns what a transaction of gasUnits costs at gasPrice, in wei of the network's
// native gas token: cost = gas_units × gas_price, as the plan's Summary prescribes. No
// margin is added (D3): callers buffer for gas-price movement between the quote and their
// own transaction themselves.
func costOf(gasUnits uint64, gasPrice *big.Int) types.BigIntString {
	cost := new(big.Int).Mul(new(big.Int).SetUint64(gasUnits), gasPrice)
	return types.BigIntString(cost.String())
}
