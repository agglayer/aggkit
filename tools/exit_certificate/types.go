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

// Step0Result holds the output of Step 0 (LBT generation).
type Step0Result struct {
	TargetBlock uint64     `json:"targetBlock"`
	Entries     []LBTEntry `json:"entries"`
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
	// ERC20HoldersCovered is the portion of SC-locked value distributed as individual
	// bridge exits to holders of ERC-20 vault contracts (from Step B3 breakdowns).
	// Empty when no breakdown applies to this token.
	ERC20HoldersCovered string `json:"erc20HoldersCovered,omitempty"`
	// TotalSCLockedBalance is the gross value locked in smart contracts: LBT - EOA.
	// It includes both the portion covered by ERC-20 holder bridges and the remainder.
	TotalSCLockedBalance string `json:"totalSCLockedBalance"`
	// PendingSCLockedBalance is the net SC-locked value that requires a bridge exit to
	// exitAddress: TotalSCLockedBalance − ERC20HoldersCovered.
	PendingSCLockedBalance string `json:"pendingSCLockedBalance"`
}

// FailedTrace pairs a transaction hash with the RPC error that caused its trace to fail.
type FailedTrace struct {
	Hash  common.Hash `json:"hash"`
	Error string      `json:"error"`
}

// StepAResult holds the combined output of Step A (A1 + A2).
type StepAResult struct {
	Addresses     []common.Address `json:"addresses"`
	FailedTraces  []FailedTrace    `json:"failedTraces"`
	WrappedTokens []WrappedToken   `json:"-"`
}

// StepA2Result holds addresses recovered from tx receipts of failed traces (Step A2).
type StepA2Result struct {
	Addresses []common.Address `json:"addresses"`
}

// StepB1Result holds the output produced exclusively by Step B1
// (address classification and balance fetching). It does not include
// the ERC-20 detection data added by Step B2.
type StepB1Result struct {
	EOABalances       []EOABalance         `json:"eoaBalances"`
	Accumulated       []AccumulatedBalance `json:"accumulated"`
	ContractAddresses []common.Address     `json:"contractAddresses"`
}

// StepBResult holds the combined output of Step B (B1 + B2 + B3).
type StepBResult struct {
	EOABalances           []EOABalance           `json:"eoaBalances"`
	Accumulated           []AccumulatedBalance   `json:"accumulated"`
	ContractAddresses     []common.Address       `json:"contractAddresses"`
	DetectedERC20s        []DetectedERC20        `json:"detectedErc20s,omitempty"`
	DiscardedERC20s       []DiscardedERC20       `json:"discardedErc20s,omitempty"`
	ERC20HolderBreakdowns []ERC20HolderBreakdown `json:"erc20HolderBreakdowns,omitempty"`
}

// ERC20HolderBreakdown holds the full holder decomposition for a single ERC-20 contract
// produced by Step B3.
type ERC20HolderBreakdown struct {
	Address common.Address `json:"address"`
	Holders []ERC20Holder  `json:"holders"`
	// Detected is the collateral info from Step B2: which tracked wrapped tokens this
	// contract holds, plus its name/symbol/totalSupply. Nil when the contract was not
	// present in the B2 detected list (e.g. it holds no tracked wrapped tokens).
	Detected *DetectedERC20 `json:"detected,omitempty"`
}

// StepB3Result holds the output of Step B3 (extra ERC-20 holder decomposition).
type StepB3Result struct {
	Breakdowns []ERC20HolderBreakdown `json:"breakdowns"`
}

// StepB2Result holds the output of Step B2.
type StepB2Result struct {
	// DetectedERC20s are contracts that hold at least one tracked wrapped token.
	DetectedERC20s []DetectedERC20 `json:"detectedErc20s"`
	// DiscardedERC20s are contracts that responded to totalSupply() but hold none
	// of the tracked wrapped tokens and are therefore irrelevant to the certificate.
	DiscardedERC20s []DiscardedERC20 `json:"discardedErc20s,omitempty"`
}

// DetectedERC20 holds an ERC-20 contract that holds at least one tracked wrapped token.
type DetectedERC20 struct {
	Address              common.Address        `json:"address"`
	Name                 string                `json:"name,omitempty"`
	Symbol               string                `json:"symbol,omitempty"`
	TotalSupply          string                `json:"totalSupply"`
	WrappedTokenBalances []WrappedTokenBalance `json:"wrappedTokenBalances"`
}

// DiscardedERC20 is an ERC-20 contract that holds none of the tracked wrapped tokens.
type DiscardedERC20 struct {
	Address     common.Address `json:"address"`
	Name        string         `json:"name,omitempty"`
	Symbol      string         `json:"symbol,omitempty"`
	TotalSupply string         `json:"totalSupply"`
}

// WrappedTokenBalance is the balance of a tracked wrapped token held by an ERC-20 contract.
type WrappedTokenBalance struct {
	Token   WrappedToken `json:"token"`
	Balance string       `json:"balance"`
}

// ERC20Holder is an (address, balance) pair produced by Step B2.
type ERC20Holder struct {
	Address common.Address `json:"address"`
	Balance string         `json:"balance"`
}

// HolderBridge is an individual bridge exit for a holder of an ERC-20 vault/staking
// contract, representing their proportional share of the tracked wrapped tokens locked
// inside that contract. Produced by Step C from the Step B3 breakdown data.
type HolderBridge struct {
	VaultAddress        common.Address `json:"vaultAddress"`
	WrappedTokenAddress common.Address `json:"wrappedTokenAddress"`
	OriginNetwork       uint32         `json:"originNetwork"`
	OriginTokenAddress  common.Address `json:"originTokenAddress"`
	HolderAddress       common.Address `json:"holderAddress"`
	Amount              string         `json:"amount"`
}

// StepCResult holds the output of Step C.
type StepCResult struct {
	SCLockedValues []SCLockedValue `json:"scLockedValues"`
	// HolderBridges are individual bridge exits for holders of ERC-20 vault contracts
	// whose breakdowns were provided by Step B3. These replace what would otherwise be a
	// single SC-locked exit to exitAddress for the portion of value they cover.
	HolderBridges []HolderBridge `json:"holderBridges,omitempty"`
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
	// UnclaimedBridges are unclaimed L1→L2 deposits with leaf_type=asset that were added
	// to the certificate as bridge exits and imported bridge exits.
	UnclaimedBridges []L1Deposit `json:"unclaimedBridges"`
	// UnclaimedMessages are unclaimed L1→L2 deposits with leaf_type=message. These are
	// logged as warnings but NOT added to the certificate (messages are not transferable assets).
	UnclaimedMessages []L1Deposit                `json:"unclaimedMessages,omitempty"`
	FinalCertificate  *agglayertypes.Certificate `json:"finalCertificate"`
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
	// RemainingBalance is the cap budget for this token: min(LBT, agglayer).
	// Not persisted to JSON; used internally by capCertificateExits.
	RemainingBalance *big.Int `json:"-"`
}

// StepFResult holds the output of Step F (agglayer token balance check).
type StepFResult struct {
	AllMatch      bool                `json:"allMatch,omitempty"`
	TokenBalances json.RawMessage     `json:"tokenBalances,omitempty"`
	Checks        []TokenBalanceCheck `json:"checks,omitempty"`
	// CappedCertificate is set when mismatches were found and ignoreBalanceMismatch=true.
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

// StepG1Result holds the output of Step G1: the L2 block at which Step G2 spins up its Anvil
// shadow-fork. Step G1 lite-syncs the L2 bridge history from genesis up to that block into the lite
// DB Step G2 reuses.
type StepG1Result struct {
	// ShadowForkBlock is the L2 block Step G2 forks at — the resolved targetBlock up to which Step G1
	// lite-synced the bridge history.
	ShadowForkBlock uint64 `json:"shadowForkBlock"`
}

// StepGResult holds the output of Step G (NewLocalExitRoot calculation).
type StepGResult struct {
	// InitialLocalExitRoot is the LER read from the bridge contract at targetBlock,
	// before any bridge exits from the certificate are replayed.
	InitialLocalExitRoot common.Hash `json:"initialLocalExitRoot"`
	NewLocalExitRoot     common.Hash `json:"newLocalExitRoot"`
	BridgeExitCount      uint64      `json:"bridgeExitCount"`
	// BridgeExitMetadata holds the Metadata field from the BridgeEvent emitted for each
	// replayed bridge exit, in the same order as Certificate.BridgeExits. Step I applies
	// these values to each BridgeExit.Metadata before finalising the certificate.
	BridgeExitMetadata [][]byte `json:"bridgeExitMetadata,omitempty"`
}

// StepHResult holds the output of Step H (PreviousLocalExitRoot and next height from agglayer).
type StepHResult struct {
	PreviousLocalExitRoot common.Hash `json:"previousLocalExitRoot"`
	// Height is the certificate height to use for the exit certificate (settled_height + 1,
	// or 0 if no certificate has been settled yet).
	Height uint64 `json:"height"`
}
