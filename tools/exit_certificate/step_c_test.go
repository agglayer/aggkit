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

	scLocked, ok := new(big.Int).SetString(result.SCLockedValues[0].SCLockedBalance, 10)
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
	require.Equal(t, "0", result.SCLockedValues[0].SCLockedBalance)
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
		scLockedMap[v.WrappedTokenAddress] = v.SCLockedBalance
	}

	require.Equal(t, "700000", scLockedMap[token1])
	require.Equal(t, "1500000", scLockedMap[token2])
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
	require.Equal(t, "700000", result.SCLockedValues[0].SCLockedBalance)
}
