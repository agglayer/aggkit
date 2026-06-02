package exit_certificate

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestRunStepC_Basic(t *testing.T) {
	t.Parallel()

	tokenAddr := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	originAddr := common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")

	lbtEntries := []LBTEntry{
		{
			WrappedTokenAddress: tokenAddr,
			OriginNetwork:       0,
			OriginTokenAddress:  originAddr,
			Balance:             "1000000",
		},
	}

	stepB := &StepBResult{
		Accumulated: []AccumulatedBalance{
			{
				WrappedTokenAddress: tokenAddr,
				OriginNetwork:       0,
				OriginTokenAddress:  originAddr,
				TotalBalance:        "600000",
			},
		},
	}

	result, err := RunStepC(lbtEntries, stepB)
	require.NoError(t, err)
	require.Len(t, result.SCLockedValues, 1)

	scLocked, ok := new(big.Int).SetString(result.SCLockedValues[0].PendingSCLockedBalance, 10)
	require.True(t, ok)
	require.Equal(t, big.NewInt(400000), scLocked)
}

func TestRunStepC_EOAExceedsLBT(t *testing.T) {
	t.Parallel()

	tokenAddr := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	originAddr := common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")

	lbtEntries := []LBTEntry{
		{
			WrappedTokenAddress: tokenAddr,
			OriginNetwork:       0,
			OriginTokenAddress:  originAddr,
			Balance:             "500000",
		},
	}

	stepB := &StepBResult{
		Accumulated: []AccumulatedBalance{
			{
				WrappedTokenAddress: tokenAddr,
				OriginNetwork:       0,
				OriginTokenAddress:  originAddr,
				TotalBalance:        "800000",
			},
		},
	}

	result, err := RunStepC(lbtEntries, stepB)
	require.NoError(t, err)
	require.Len(t, result.SCLockedValues, 1)

	// SC-locked should be clamped to 0 when EOA exceeds LBT
	require.Equal(t, "0", result.SCLockedValues[0].PendingSCLockedBalance)
}

func TestRunStepC_EmptyLBT(t *testing.T) {
	t.Parallel()

	result, err := RunStepC([]LBTEntry{}, &StepBResult{Accumulated: nil})
	require.NoError(t, err)
	require.Empty(t, result.SCLockedValues)
}

func TestRunStepC_MultipleTokens(t *testing.T) {
	t.Parallel()

	token1 := common.HexToAddress("0x1111111111111111111111111111111111111111")
	token2 := common.HexToAddress("0x2222222222222222222222222222222222222222")
	origin1 := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	origin2 := common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")

	lbtEntries := []LBTEntry{
		{WrappedTokenAddress: token1, OriginNetwork: 0, OriginTokenAddress: origin1, Balance: "1000000"},
		{WrappedTokenAddress: token2, OriginNetwork: 1, OriginTokenAddress: origin2, Balance: "2000000"},
	}

	stepB := &StepBResult{
		Accumulated: []AccumulatedBalance{
			{WrappedTokenAddress: token1, OriginNetwork: 0, OriginTokenAddress: origin1, TotalBalance: "300000"},
			{WrappedTokenAddress: token2, OriginNetwork: 1, OriginTokenAddress: origin2, TotalBalance: "500000"},
		},
	}

	result, err := RunStepC(lbtEntries, stepB)
	require.NoError(t, err)
	require.Len(t, result.SCLockedValues, 2)

	scLockedMap := make(map[common.Address]string)
	for _, v := range result.SCLockedValues {
		scLockedMap[v.WrappedTokenAddress] = v.PendingSCLockedBalance
	}

	require.Equal(t, "700000", scLockedMap[token1])
	require.Equal(t, "1500000", scLockedMap[token2])
}

// --- ERC20HolderBreakdown tests ---

// fixture used by the breakdown tests:
//
//	LBT[token] = 2000, EOA_accumulated[token] = 1000
//	→ raw SC_locked = 1000 (the vault's holdings are inside this)
//	vault holds 900 of token
func breakdownFixture() (tokenAddr, originAddr, vaultAddr, alice, bob common.Address, lbtEntries []LBTEntry, accumulated []AccumulatedBalance) {
	tokenAddr = common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	originAddr = common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")
	vaultAddr = common.HexToAddress("0xCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC")
	alice = common.HexToAddress("0x1111111111111111111111111111111111111111")
	bob = common.HexToAddress("0x2222222222222222222222222222222222222222")

	lbtEntries = []LBTEntry{
		{WrappedTokenAddress: tokenAddr, OriginNetwork: 0, OriginTokenAddress: originAddr, Balance: "2000"},
	}
	accumulated = []AccumulatedBalance{
		{WrappedTokenAddress: tokenAddr, OriginNetwork: 0, OriginTokenAddress: originAddr, TotalBalance: "1000"},
	}
	return
}

func TestRunStepC_BreakdownCreatesHolderBridges(t *testing.T) {
	t.Parallel()

	tokenAddr, originAddr, vaultAddr, alice, bob, lbtEntries, accumulated := breakdownFixture()
	// vault holds 900, alice=400 + bob=500 = 900 → full coverage, no remainder
	breakdowns := []ERC20HolderBreakdown{{
		Address: vaultAddr,
		Holders: []ERC20Holder{
			{Address: alice, Balance: "400"},
			{Address: bob, Balance: "500"},
		},
		Detected: &DetectedERC20{
			WrappedTokenBalances: []WrappedTokenBalance{{
				Token:   WrappedToken{WrappedTokenAddress: tokenAddr, OriginNetwork: 0, OriginTokenAddress: originAddr},
				Balance: "900",
			}},
		},
	}}

	result, err := RunStepC(lbtEntries, &StepBResult{Accumulated: accumulated, ERC20HolderBreakdowns: breakdowns})
	require.NoError(t, err)

	// Two individual holder bridges (1:1 with holder balances)
	require.Len(t, result.HolderBridges, 2)
	holderMap := make(map[common.Address]string)
	for _, hb := range result.HolderBridges {
		require.Equal(t, vaultAddr, hb.VaultAddress)
		require.Equal(t, tokenAddr, hb.WrappedTokenAddress)
		holderMap[hb.HolderAddress] = hb.Amount
	}
	require.Equal(t, "400", holderMap[alice])
	require.Equal(t, "500", holderMap[bob])

	// SC_locked = (2000 - 1000) - 900 = 100 (other contracts not in breakdown)
	require.Len(t, result.SCLockedValues, 1)
	require.Equal(t, "100", result.SCLockedValues[0].PendingSCLockedBalance)
}

func TestRunStepC_UnattributedRemainderGoesToSCLocked(t *testing.T) {
	t.Parallel()

	// alice=300 + bob=400 = 700 < 900 → remainder=200 unattributed
	// Those 200 stay in SC_locked and flow to exitAddress — no error
	tokenAddr, originAddr, vaultAddr, alice, bob, lbtEntries, accumulated := breakdownFixture()
	breakdowns := []ERC20HolderBreakdown{{
		Address: vaultAddr,
		Holders: []ERC20Holder{
			{Address: alice, Balance: "300"},
			{Address: bob, Balance: "400"},
		},
		Detected: &DetectedERC20{
			WrappedTokenBalances: []WrappedTokenBalance{{
				Token:   WrappedToken{WrappedTokenAddress: tokenAddr, OriginNetwork: 0, OriginTokenAddress: originAddr},
				Balance: "900",
			}},
		},
	}}

	result, err := RunStepC(lbtEntries, &StepBResult{Accumulated: accumulated, ERC20HolderBreakdowns: breakdowns})
	require.NoError(t, err)

	// Only known-holder bridges are created (700 total distributed)
	require.Len(t, result.HolderBridges, 2)

	// SC_locked = (2000 - 1000) - 700 = 300
	// (200 of the vault's 900 are unattributed and remain as SC_locked)
	require.Len(t, result.SCLockedValues, 1)
	require.Equal(t, "300", result.SCLockedValues[0].PendingSCLockedBalance)
}

func TestRunStepC_HolderBalancesExceedVaultHoldings_Error(t *testing.T) {
	t.Parallel()

	// alice=300 + bob=400 = 700 > 500 (vault holds) → corrupt data → error
	tokenAddr, originAddr, vaultAddr, alice, bob, lbtEntries, accumulated := breakdownFixture()
	breakdowns := []ERC20HolderBreakdown{{
		Address: vaultAddr,
		Holders: []ERC20Holder{
			{Address: alice, Balance: "300"},
			{Address: bob, Balance: "400"},
		},
		Detected: &DetectedERC20{
			WrappedTokenBalances: []WrappedTokenBalance{{
				Token:   WrappedToken{WrappedTokenAddress: tokenAddr, OriginNetwork: 0, OriginTokenAddress: originAddr},
				Balance: "500",
			}},
		},
	}}

	_, err := RunStepC(lbtEntries, &StepBResult{Accumulated: accumulated, ERC20HolderBreakdowns: breakdowns})
	require.Error(t, err)
	require.Contains(t, err.Error(), vaultAddr.Hex())
	require.Contains(t, err.Error(), "exceeds vault holdings")
}

func TestRunStepC_BreakdownWithoutDetected_Skipped(t *testing.T) {
	t.Parallel()

	tokenAddr, originAddr, _, alice, _, lbtEntries, accumulated := breakdownFixture()
	vaultAddr := common.HexToAddress("0xCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC")

	// Breakdown has no Detected → skipped entirely
	breakdowns := []ERC20HolderBreakdown{{
		Address:  vaultAddr,
		Holders:  []ERC20Holder{{Address: alice, Balance: "400"}},
		Detected: nil,
	}}

	result, err := RunStepC(lbtEntries, &StepBResult{Accumulated: accumulated, ERC20HolderBreakdowns: breakdowns})
	require.NoError(t, err)
	require.Empty(t, result.HolderBridges)
	// SC_locked unchanged: 2000 - 1000 = 1000
	require.Len(t, result.SCLockedValues, 1)
	require.Equal(t, "1000", result.SCLockedValues[0].PendingSCLockedBalance)

	_ = tokenAddr
	_ = originAddr
}

func TestRunStepC_TokenNotInLBT(t *testing.T) {
	t.Parallel()

	token1 := common.HexToAddress("0x1111111111111111111111111111111111111111")
	origin1 := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	extraToken := common.HexToAddress("0x9999999999999999999999999999999999999999")

	lbtEntries := []LBTEntry{
		{WrappedTokenAddress: token1, OriginNetwork: 0, OriginTokenAddress: origin1, Balance: "1000000"},
	}

	stepB := &StepBResult{
		Accumulated: []AccumulatedBalance{
			{WrappedTokenAddress: token1, OriginNetwork: 0, OriginTokenAddress: origin1, TotalBalance: "300000"},
			{WrappedTokenAddress: extraToken, OriginNetwork: 2, OriginTokenAddress: common.Address{}, TotalBalance: "100000"},
		},
	}

	result, err := RunStepC(lbtEntries, stepB)
	require.NoError(t, err)
	// Only token1 is in LBT, so only 1 SC-locked entry
	require.Len(t, result.SCLockedValues, 1)
	require.Equal(t, "700000", result.SCLockedValues[0].PendingSCLockedBalance)
}
