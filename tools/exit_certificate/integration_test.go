package exit_certificate

import (
	"math/big"
	"os"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// TestLoadParametersJSON loads the actual parameters.json used in production
// and validates that the config is parsed correctly.
func TestLoadParametersJSON(t *testing.T) {
	t.Parallel()

	configPath := "../../../exit-certificate-tool/parameters.json"
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Skip("parameters.json not found at expected path — skipping integration test")
	}

	cfg, err := LoadConfig(configPath)
	require.NoError(t, err)

	require.NotEmpty(t, cfg.L2RPCURL)
	require.NotEmpty(t, cfg.L1RPCURL)
	require.NotEqual(t, common.Address{}, cfg.L2BridgeAddress)
	require.NotEqual(t, common.Address{}, cfg.L1BridgeAddress)
	require.NotEmpty(t, cfg.TargetBlock)
	require.Greater(t, cfg.Options.BlockRange, 0)
	require.Greater(t, cfg.Options.ConcurrencyLimit, 0)
	require.Greater(t, cfg.Options.RPCBatchSize, 0)
}

// TestStepD_WithProductionLikeData tests Step D with data structures matching
// the format that a real run would produce.
func TestStepD_WithProductionLikeData(t *testing.T) {
	t.Parallel()

	ethBalance, _ := new(big.Int).SetString("5000000000000000000", 10)
	tokenBalance, _ := new(big.Int).SetString("1000000000", 10)

	cfg := &Config{
		L2NetworkID:        1,
		ExitAddress:        common.HexToAddress("0x0000000000000000000000000000000000000001"),
		DestinationNetwork: 0,
	}

	stepB := &StepBResult{
		EOABalances: []EOABalance{
			{
				Address:    common.HexToAddress("0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045"),
				ETHBalance: ethBalance.String(),
				Tokens: []EOATokenBalance{
					{
						WrappedTokenAddress: common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"),
						OriginNetwork:       0,
						OriginTokenAddress:  common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"),
						Balance:             tokenBalance.String(),
					},
				},
			},
			{
				Address:    common.HexToAddress("0x1234567890123456789012345678901234567890"),
				ETHBalance: "100000000000000000",
			},
		},
	}

	stepC := &StepCResult{
		SCLockedValues: []SCLockedValue{
			{
				WrappedTokenAddress:    common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"),
				OriginNetwork:          0,
				OriginTokenAddress:     common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"),
				LBTBalance:             "5000000000",
				EOAAccumulated:         "1000000000",
				PendingSCLockedBalance: "4000000000",
			},
		},
	}

	result, err := RunStepD(cfg, stepB, stepC)
	require.NoError(t, err)
	require.NotNil(t, result.Certificate)

	// 2 EOAs (1 ETH + 1 token each for first, 1 ETH for second) + 1 SC-locked = 4
	require.Len(t, result.Certificate.BridgeExits, 4)
	require.Equal(t, uint32(1), result.Certificate.NetworkID)
	require.Equal(t, uint64(0), result.Certificate.Height)
	require.Equal(t, common.Hash{}, result.Certificate.PrevLocalExitRoot)
	require.Equal(t, common.Hash{}, result.Certificate.NewLocalExitRoot)

	// Verify EOA exits
	exit0 := result.Certificate.BridgeExits[0]
	require.Equal(t, common.HexToAddress("0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045"), exit0.DestinationAddress)
	require.Equal(t, ethBalance, exit0.Amount)
	require.Equal(t, common.Address{}, exit0.TokenInfo.OriginTokenAddress)

	exit1 := result.Certificate.BridgeExits[1]
	require.Equal(t, common.HexToAddress("0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045"), exit1.DestinationAddress)
	require.Equal(t, tokenBalance, exit1.Amount)
	require.Equal(t, common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"), exit1.TokenInfo.OriginTokenAddress)

	// Verify SC-locked exit goes to exit address
	exit3 := result.Certificate.BridgeExits[3]
	require.Equal(t, common.HexToAddress("0x0000000000000000000000000000000000000001"), exit3.DestinationAddress)
	scAmount, _ := new(big.Int).SetString("4000000000", 10)
	require.Equal(t, scAmount, exit3.Amount)
}

// TestStepE_WithProductionLikeData tests Step E filtering with a simulated claimed set.
func TestStepE_WithProductionLikeData(t *testing.T) {
	t.Parallel()

	cert := createTestCertificate(t, 1, 2)

	// Simulate 3 L1 deposits targeting L2, with deposit counts 0, 1, 2
	deposits := []L1Deposit{
		{DepositCount: 0, Amount: big.NewInt(1000)},
		{DepositCount: 1, Amount: big.NewInt(2000)},
		{DepositCount: 2, Amount: big.NewInt(5000), DestinationAddress: common.HexToAddress("0x1234")},
	}

	// Simulate isClaimed results: deposits 0 and 1 are claimed
	claimedSet := map[uint32]struct{}{0: {}, 1: {}}
	unclaimed := filterUnclaimedDeposits(deposits, claimedSet)
	require.Len(t, unclaimed, 1)
	require.Equal(t, uint32(2), unclaimed[0].DepositCount)

	require.Len(t, cert.BridgeExits, 2)
}

func createTestCertificate(t *testing.T, networkID uint32, numExits int) *agglayertypes.Certificate {
	t.Helper()

	exits := make([]*agglayertypes.BridgeExit, numExits)
	for i := range numExits {
		exits[i] = MakeBridgeExit(
			0, common.Address{}, 0,
			common.HexToAddress("0x1111"),
			big.NewInt(int64(1000*(i+1))),
		)
	}

	return &agglayertypes.Certificate{
		NetworkID:   networkID,
		BridgeExits: exits,
	}
}
