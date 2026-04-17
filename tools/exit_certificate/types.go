package exit_certificate

import (
	"math/big"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/ethereum/go-ethereum/common"
)

// WrappedToken describes a wrapped token deployed on L2 by the bridge contract.
type WrappedToken struct {
	WrappedTokenAddress common.Address `json:"wrappedTokenAddress"`
	OriginNetwork       uint32         `json:"originNetwork"`
	OriginTokenAddress  common.Address `json:"originTokenAddress"`
}

// LBTEntry is a single entry from the Local Balance Tree file exported by the getLBT tool.
type LBTEntry struct {
	WrappedTokenAddress common.Address `json:"wrappedTokenAddress"`
	OriginNetwork       uint32         `json:"originNetwork"`
	OriginTokenAddress  common.Address `json:"originTokenAddress"`
	Balance             string         `json:"balance"`
}

// EOATokenBalance records a single token balance for an EOA.
type EOATokenBalance struct {
	WrappedTokenAddress common.Address `json:"wrappedTokenAddress"`
	OriginNetwork       uint32         `json:"originNetwork"`
	OriginTokenAddress  common.Address `json:"originTokenAddress"`
	Balance             string         `json:"balance"`
}

// EOABalance holds all non-zero balances for a single EOA address.
type EOABalance struct {
	Address    common.Address    `json:"address"`
	ETHBalance string            `json:"ethBalance"`
	Tokens     []EOATokenBalance `json:"tokens"`
}

// AccumulatedBalance holds the total balance across all EOAs for a single token.
type AccumulatedBalance struct {
	WrappedTokenAddress common.Address `json:"wrappedTokenAddress"`
	OriginNetwork       uint32         `json:"originNetwork"`
	OriginTokenAddress  common.Address `json:"originTokenAddress"`
	TotalBalance        string         `json:"totalBalance"`
}

// SCLockedValue holds the computed smart-contract-locked value for a single token.
type SCLockedValue struct {
	WrappedTokenAddress common.Address `json:"wrappedTokenAddress"`
	OriginNetwork       uint32         `json:"originNetwork"`
	OriginTokenAddress  common.Address `json:"originTokenAddress"`
	LBTBalance          string         `json:"lbtBalance"`
	EOAAccumulated      string         `json:"eoaAccumulated"`
	SCLockedBalance     string         `json:"scLockedBalance"`
}

// StepAResult holds the output of Step A.
type StepAResult struct {
	Addresses     []common.Address `json:"addresses"`
	WrappedTokens []WrappedToken   `json:"-"`
}

// StepBResult holds the output of Step B.
type StepBResult struct {
	EOABalances       []EOABalance         `json:"eoaBalances"`
	Accumulated       []AccumulatedBalance `json:"accumulated"`
	ContractAddresses []common.Address     `json:"contractAddresses"`
}

// StepCResult holds the output of Step C.
type StepCResult struct {
	SCLockedValues []SCLockedValue `json:"scLockedValues"`
}

// StepDResult holds the output of Step D.
type StepDResult struct {
	Certificate *agglayertypes.Certificate `json:"certificate"`
}

// L1Deposit represents an L1 bridge deposit targeting the L2 chain.
type L1Deposit struct {
	LeafType           uint8          `json:"leafType"`
	OriginNetwork      uint32         `json:"originNetwork"`
	OriginAddress      common.Address `json:"originAddress"`
	DestinationNetwork uint32         `json:"destinationNetwork"`
	DestinationAddress common.Address `json:"destinationAddress"`
	Amount             *big.Int       `json:"amount"`
	Metadata           []byte         `json:"metadata"`
	DepositCount       uint32         `json:"depositCount"`
	BlockNumber        uint64         `json:"blockNumber"`
	TxHash             common.Hash    `json:"txHash"`
}

// StepEResult holds the output of Step E.
type StepEResult struct {
	UnclaimedBridges []L1Deposit                `json:"unclaimedBridges"`
	FinalCertificate *agglayertypes.Certificate `json:"finalCertificate"`
}
