package exit_certificate

import (
	"encoding/json"
	"math/big"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/ethereum/go-ethereum/common"
)

// StepWaitResult holds the outcome of the WAIT step.
type StepWaitResult struct {
	CertificateHash  common.Hash                     `json:"certificateHash"`
	FinalStatus      agglayertypes.CertificateStatus `json:"finalStatus"`
	SettlementTxHash *common.Hash                    `json:"settlementTxHash,omitempty"`
	ElapsedSeconds   float64                         `json:"elapsedSeconds"`
	// PendingCertWaited is set when a pre-existing pending certificate was found on the
	// network and waited for before polling our submitted certificate.
	PendingCertWaited *common.Hash `json:"pendingCertWaited,omitempty"`
}

// WrappedToken describes a wrapped token deployed on L2 by the bridge contract.
type WrappedToken struct {
	WrappedTokenAddress common.Address `json:"wrappedTokenAddress"`
	OriginNetwork       uint32         `json:"originNetwork"`
	OriginTokenAddress  common.Address `json:"originTokenAddress"`
}

// LegacyToken records a wrapped token address that was replaced by a SetSovereignTokenAddress
// override, along with its totalSupply at the target block.
type LegacyToken struct {
	Address common.Address `json:"address"`
	Balance string         `json:"balance"`
}

// LBTEntry is a single entry from the Local Balance Tree file exported by the getLBT tool.
type LBTEntry struct {
	WrappedTokenAddress common.Address `json:"wrappedTokenAddress"`
	OriginNetwork       uint32         `json:"originNetwork"`
	OriginTokenAddress  common.Address `json:"originTokenAddress"`
	Balance             string         `json:"balance"`
	// LegacyTokens holds previous wrapped addresses (replaced via SetSovereignTokenAddress)
	// and their totalSupply at the target block. Populated only when an override was applied.
	LegacyTokens []LegacyToken `json:"legacyTokens,omitempty"`
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
	FailedTraces  []common.Hash    `json:"failedTraces"`
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

// CertificateEntry is one bridge exit entry for a given token, used in mismatch reports.
type CertificateEntry struct {
	DestinationNetwork uint32 `json:"destinationNetwork"`
	DestinationAddress string `json:"destinationAddress"`
	Amount             string `json:"amount"`
}

// TokenBalanceCheck holds the three-way comparison between Step 0 (LBT), the certificate bridge exits,
// and the agglayer state for one token. LBTAmount is empty when LBT data was not available.
type TokenBalanceCheck struct {
	OriginNetwork      uint32             `json:"originNetwork"`
	OriginTokenAddress string             `json:"originTokenAddress"`
	LBTAmount          string             `json:"lbtAmount,omitempty"`
	CertificateAmount  string             `json:"certificateAmount"`
	AgglayerAmount     string             `json:"agglayerAmount"`
	Match              bool               `json:"match"`
	CertificateEntries []CertificateEntry `json:"certificateEntries,omitempty"`
}

// StepFResult holds the output of Step F (agglayer token balance check).
type StepFResult struct {
	Skipped           bool                       `json:"skipped,omitempty"`
	AllMatch          bool                       `json:"allMatch,omitempty"`
	TokenBalances     json.RawMessage            `json:"tokenBalances,omitempty"`
	Checks            []TokenBalanceCheck        `json:"checks,omitempty"`
	// CappedCertificate is set when mismatches were found and continueIfBalanceMismatch=true.
	// Bridge exits are proportionally scaled down to min(agglayer, lbt) per token.
	CappedCertificate *agglayertypes.Certificate `json:"cappedCertificate,omitempty"`
}

// StepCheckResult holds the output of Step CHECK (prerequisite verification).
type StepCheckResult struct {
	AnvilInstalled  bool     `json:"anvilInstalled"`
	BridgeNetworkID uint32   `json:"bridgeNetworkID"`
	NetworkType     string   `json:"networkType"`
	Threshold       uint64   `json:"threshold"`
	SignerCount     int      `json:"signerCount"`
	Signers         []string `json:"signers,omitempty"`
	GasTokenAddress string   `json:"gasTokenAddress,omitempty"`
	GasTokenNetwork uint32   `json:"gasTokenNetwork,omitempty"`
	WETHToken       string   `json:"wethToken,omitempty"`
}

// StepGResult holds the output of Step G (NewLocalExitRoot calculation).
type StepGResult struct {
	NewLocalExitRoot common.Hash `json:"newLocalExitRoot"`
	BridgeExitCount  uint64      `json:"bridgeExitCount"`
}

// StepHResult holds the output of Step H (PreviousLocalExitRoot and next height from agglayer).
type StepHResult struct {
	PreviousLocalExitRoot common.Hash `json:"previousLocalExitRoot"`
	// Height is the certificate height to use for the exit certificate (settled_height + 1,
	// or 0 if no certificate has been settled yet).
	Height uint64 `json:"height"`
}
