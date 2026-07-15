package exit_certificate

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestRunStepD_EOABalancesOnly(t *testing.T) {
	t.Parallel()

	addr1 := common.HexToAddress("0x1111111111111111111111111111111111111111")
	addr2 := common.HexToAddress("0x2222222222222222222222222222222222222222")

	cfg := &Config{
		L2NetworkID:        1,
		ExitAddress:        common.HexToAddress("0x0000000000000000000000000000000000000001"),
		DestinationNetwork: 0,
	}

	stepB := &StepBResult{
		EOABalances: []EOABalance{
			{
				Address:    addr1,
				ETHBalance: "1000000000000000000",
				Tokens: []EOATokenBalance{
					{
						WrappedTokenAddress: common.HexToAddress("0xAAAA"),
						OriginNetwork:       0,
						OriginTokenAddress:  common.HexToAddress("0xBBBB"),
						Balance:             "5000000",
					},
				},
			},
			{
				Address:    addr2,
				ETHBalance: "2000000000000000000",
			},
		},
	}

	stepC := &StepCResult{SCLockedValues: nil}

	result, err := RunStepD(cfg, stepB, stepC)
	require.NoError(t, err)
	require.NotNil(t, result.Certificate)
	require.Equal(t, uint32(1), result.Certificate.NetworkID)

	// addr1: ETH + 1 token = 2 exits, addr2: ETH = 1 exit → total 3
	require.Len(t, result.Certificate.BridgeExits, 3)

	// Verify first exit is addr1 ETH
	exit0 := result.Certificate.BridgeExits[0]
	require.Equal(t, addr1, exit0.DestinationAddress)
	require.Equal(t, uint32(0), exit0.DestinationNetwork)
	expectedETH, _ := new(big.Int).SetString("1000000000000000000", 10)
	require.Equal(t, expectedETH, exit0.Amount)

	// Verify second exit is addr1 token
	exit1 := result.Certificate.BridgeExits[1]
	require.Equal(t, addr1, exit1.DestinationAddress)
	require.Equal(t, big.NewInt(5000000), exit1.Amount)

	// Verify third exit is addr2 ETH
	exit2 := result.Certificate.BridgeExits[2]
	require.Equal(t, addr2, exit2.DestinationAddress)
}

func TestRunStepD_WithSCLockedValues(t *testing.T) {
	t.Parallel()

	exitAddr := common.HexToAddress("0x0000000000000000000000000000000000000001")
	tokenOriginAddr := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")

	cfg := &Config{
		L2NetworkID:        2,
		ExitAddress:        exitAddr,
		DestinationNetwork: 0,
	}

	stepB := &StepBResult{EOABalances: nil}
	stepC := &StepCResult{
		SCLockedValues: []SCLockedValue{
			{
				WrappedTokenAddress:    common.HexToAddress("0xBBBB"),
				OriginNetwork:          0,
				OriginTokenAddress:     tokenOriginAddr,
				LBTBalance:             "1000000",
				EOAAccumulated:         "300000",
				PendingSCLockedBalance: "700000",
			},
			{
				WrappedTokenAddress:    common.HexToAddress("0xCCCC"),
				OriginNetwork:          1,
				OriginTokenAddress:     common.HexToAddress("0xDDDD"),
				LBTBalance:             "500000",
				EOAAccumulated:         "500000",
				PendingSCLockedBalance: "0",
			},
		},
	}

	result, err := RunStepD(cfg, stepB, stepC)
	require.NoError(t, err)

	// Only 1 SC-locked exit (the second has balance 0)
	require.Len(t, result.Certificate.BridgeExits, 1)

	exit := result.Certificate.BridgeExits[0]
	require.Equal(t, exitAddr, exit.DestinationAddress)
	require.Equal(t, big.NewInt(700000), exit.Amount)
	require.Equal(t, tokenOriginAddr, exit.TokenInfo.OriginTokenAddress)
}

func TestRunStepD_SkipSCLockedValue(t *testing.T) {
	t.Parallel()

	eoaAddr := common.HexToAddress("0x1111111111111111111111111111111111111111")
	holderAddr := common.HexToAddress("0x2222222222222222222222222222222222222222")

	cfg := &Config{
		L2NetworkID:        2,
		DestinationNetwork: 0,
		Options:            Options{SkipSCLockedValue: true},
	}

	stepB := &StepBResult{
		EOABalances: []EOABalance{{Address: eoaAddr, ETHBalance: "1000"}},
	}
	stepC := &StepCResult{
		SCLockedValues: []SCLockedValue{
			{
				WrappedTokenAddress:    common.HexToAddress("0xBBBB"),
				OriginNetwork:          0,
				OriginTokenAddress:     common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"),
				LBTBalance:             "1000000",
				EOAAccumulated:         "300000",
				PendingSCLockedBalance: "700000",
			},
		},
		HolderBridges: []HolderBridge{
			{
				HolderAddress:      holderAddr,
				VaultAddress:       common.HexToAddress("0xCCCC"),
				OriginNetwork:      0,
				OriginTokenAddress: common.HexToAddress("0xDDDD"),
				Amount:             "42",
			},
		},
	}

	result, err := RunStepD(cfg, stepB, stepC)
	require.NoError(t, err)

	// The SC-locked exit is omitted; the EOA and holder-bridge exits are kept.
	require.Len(t, result.Certificate.BridgeExits, 2)
	require.Equal(t, eoaAddr, result.Certificate.BridgeExits[0].DestinationAddress)
	require.Equal(t, holderAddr, result.Certificate.BridgeExits[1].DestinationAddress)
	for _, exit := range result.Certificate.BridgeExits {
		require.NotEqual(t, cfg.ExitAddress, exit.DestinationAddress)
	}
}

func TestRunStepD_EmptyInputs(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		L2NetworkID:        1,
		DestinationNetwork: 0,
	}

	result, err := RunStepD(cfg, &StepBResult{}, &StepCResult{})
	require.NoError(t, err)
	require.NotNil(t, result.Certificate)
	require.Empty(t, result.Certificate.BridgeExits)
}

func TestMakeBridgeExit(t *testing.T) {
	t.Parallel()

	origin := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	dest := common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")
	amount := big.NewInt(12345)

	exit := MakeBridgeExit(1, origin, 0, dest, amount)

	require.Equal(t, uint8(0), exit.LeafType.Uint8())
	require.NotNil(t, exit.TokenInfo)
	require.Equal(t, uint32(1), exit.TokenInfo.OriginNetwork)
	require.Equal(t, origin, exit.TokenInfo.OriginTokenAddress)
	require.Equal(t, uint32(0), exit.DestinationNetwork)
	require.Equal(t, dest, exit.DestinationAddress)
	require.Equal(t, amount, exit.Amount)
}

func TestRunStepD_CombinedEOAAndSCLocked(t *testing.T) {
	t.Parallel()

	addr1 := common.HexToAddress("0x1111111111111111111111111111111111111111")
	exitAddr := common.HexToAddress("0x0000000000000000000000000000000000000001")

	cfg := &Config{
		L2NetworkID:        1,
		ExitAddress:        exitAddr,
		DestinationNetwork: 0,
	}

	stepB := &StepBResult{
		EOABalances: []EOABalance{
			{Address: addr1, ETHBalance: "1000000"},
		},
	}

	stepC := &StepCResult{
		SCLockedValues: []SCLockedValue{
			{
				WrappedTokenAddress:    common.HexToAddress("0xAAAA"),
				OriginNetwork:          0,
				OriginTokenAddress:     common.HexToAddress("0xBBBB"),
				PendingSCLockedBalance: "500000",
			},
		},
	}

	result, err := RunStepD(cfg, stepB, stepC)
	require.NoError(t, err)

	// 1 EOA exit + 1 SC-locked exit = 2
	require.Len(t, result.Certificate.BridgeExits, 2)

	// First exit is EOA's ETH
	require.Equal(t, addr1, result.Certificate.BridgeExits[0].DestinationAddress)
	// Second exit is SC-locked to exitAddr
	require.Equal(t, exitAddr, result.Certificate.BridgeExits[1].DestinationAddress)
}
