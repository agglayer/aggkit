package e2e

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgeservice/client"
	"github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/test/e2e/envs"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

// TestBridgeFlows tests both L1->L2 and L2->L1 bridge flows in parallel
func TestBridgeFlows(t *testing.T) {
	// // Skip in short mode as this is an E2E test
	// if testing.Short() {
	// 	t.Skip("Skipping E2E test in short mode")
	// } else {
	// 	t.Skip("Skipping E2E test in short mode")
	// }

	require.NotNil(t, testEnv, "shared env must be set by TestMain")
	env := testEnv

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	// Use env transactors (shared env has one compose; KeyPool checkouts can be used when Keys is populated)
	l1Opts := env.L1.Transactor
	l2Opts := env.L2.Transactor

	log.Info("Environment loaded successfully")

	// Wait for L2 to be fully operational and producing blocks
	log.Info("Waiting for L2 to start producing blocks...")
	var l2BlockNum uint64
	var err error
	for i := 0; i < 60; i++ { // Wait up to 2 minutes
		l2BlockNum, err = env.Clients.L2.BlockNumber(ctx)
		if err == nil && l2BlockNum > 0 {
			log.Infof("L2 is operational at block %d", l2BlockNum)
			break
		}
		time.Sleep(2 * time.Second)
	}
	require.NoError(t, err, "L2 should be operational")
	require.Greater(t, l2BlockNum, uint64(0), "L2 should have blocks")

	// Run L1 -> L2 flow first
	testBridgeL1ToL2(t, ctx, env, l1Opts, l2Opts)

	// Then run L2 -> L1 flow
	testBridgeL2ToL1(t, ctx, env, l1Opts, l2Opts, common.Address{})

	log.Info("Both bridge flows completed successfully!")
}

// testBridgeL1ToL2 tests the L1 -> L2 bridge flow with native ETH
func testBridgeL1ToL2(t *testing.T, ctx context.Context, env *envs.Env, l1Opts, l2Opts *bind.TransactOpts) {
	t.Helper()
	log.Info("Starting L1->L2 bridge flow")

	// Get the L2 network ID from the contract
	callOpts := &bind.CallOpts{Context: ctx}
	l2NetworkID, err := env.L2.Contracts.L2Bridge.NetworkID(callOpts)
	require.NoError(t, err, "failed to get L2 network ID")

	bridgeAmount := big.NewInt(100000000000000) // 0.0001 ETH
	destinationAddress := l1Opts.From           // Use the funded L1 account (receives on L2)
	forceUpdateGlobalExitRoot := true

	// Get initial balance on L2
	initialL2Balance, err := env.Clients.L2.BalanceAt(ctx, destinationAddress, nil)
	require.NoError(t, err, "failed to get initial L2 balance")
	log.Infof("L1->L2: Initial L2 balance: %s", initialL2Balance.String())

	// Use checked-out L1 transactor (already funded)
	l1Opts.Value = bridgeAmount
	defer func() { l1Opts.Value = nil }()

	// Bridge native ETH from L1 to L2
	tx, err := env.L1.Contracts.Bridge.BridgeAsset(
		l1Opts,
		l2NetworkID,
		destinationAddress,
		bridgeAmount,
		common.Address{}, // address(0) for native ETH
		forceUpdateGlobalExitRoot,
		nil,
	)
	require.NoError(t, err, "failed to send bridge transaction")
	log.Infof("L1->L2: Bridge transaction sent: %s", tx.Hash().Hex())

	// Wait for transaction to be mined
	receipt, err := bind.WaitMined(ctx, env.Clients.L1, tx)
	require.NoError(t, err, "failed to wait for bridge transaction")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, receipt.Status, "bridge transaction failed")
	log.Infof("L1->L2: Bridge transaction mined in block %d", receipt.BlockNumber.Uint64())

	var bridge *types.BridgeResponse
	maxBridgeRetries := 30
	for i := 0; i < maxBridgeRetries; i++ {
		pageSize := uint32(100)
		params := client.GetBridgesParams{
			NetworkID: 0,
			PageSize:  &pageSize,
		}
		bridgesResult, err := env.Clients.BridgeService.GetBridges(ctx, params)
		if err == nil && bridgesResult != nil {
			for _, b := range bridgesResult.Bridges {
				if string(b.TxHash) == tx.Hash().Hex() {
					bridge = b
					break
				}
			}
		}
		if bridge != nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	require.NotNil(t, bridge, "bridge not found in bridge service")
	log.Infof("L1->L2: Bridge found: deposit_count=%d", bridge.DepositCount)

	depositCount := bridge.DepositCount

	// Wait for L1InfoTree inclusion
	var l1InfoTreeIndex uint32
	maxL1InfoTreeRetries := 60
	for i := 0; i < maxL1InfoTreeRetries; i++ {
		idx, err := env.Clients.BridgeService.GetL1InfoTreeIndex(ctx, 0, int(depositCount))
		if err == nil {
			l1InfoTreeIndex = idx
			log.Infof("L1->L2: L1InfoTree index: %d", l1InfoTreeIndex)
			break
		}
		if i%6 == 0 {
			log.Infof("L1->L2: Waiting for L1 Info Tree inclusion... (%d/%d)", i+1, maxL1InfoTreeRetries)
		}
		time.Sleep(5 * time.Second)
	}
	require.NotZero(t, l1InfoTreeIndex, "bridge was not included in L1 Info Tree")

	// Wait for L1InfoTreeLeaf injection on L2
	var injectedLeaf *types.L1InfoTreeLeafResponse
	maxRetries := 120
	for i := 0; i < maxRetries; i++ {
		leaf, err := env.Clients.BridgeService.GetInjectedL1InfoLeaf(ctx, int(l2NetworkID), int(l1InfoTreeIndex))
		if err == nil && leaf != nil {
			injectedLeaf = leaf
			log.Infof("L1->L2: L1InfoTreeLeaf injected")
			break
		}
		if i%6 == 0 {
			log.Infof("L1->L2: Waiting for GER injection on L2... (%d/%d)", i+1, maxRetries)
		}
		time.Sleep(5 * time.Second)
	}
	require.NotNil(t, injectedLeaf, "L1InfoTreeLeaf was not injected")

	// Get claim proof
	claimProof, err := env.Clients.BridgeService.GetClaimProof(ctx, 0, l1InfoTreeIndex, depositCount)
	require.NoError(t, err, "failed to get claim proof")
	require.NotNil(t, claimProof, "claim proof is nil")
	log.Info("L1->L2: Claim proof obtained")

	// Prepare claim
	var smtProofLocalExitRoot [32][32]byte
	for i, proofHex := range claimProof.ProofLocalExitRoot {
		if i >= 32 {
			break
		}
		smtProofLocalExitRoot[i] = common.HexToHash(string(proofHex))
	}

	var smtProofRollupExitRoot [32][32]byte
	for i, proofHex := range claimProof.ProofRollupExitRoot {
		if i >= 32 {
			break
		}
		smtProofRollupExitRoot[i] = common.HexToHash(string(proofHex))
	}

	mainnetExitRoot := common.HexToHash(string(claimProof.L1InfoTreeLeaf.MainnetExitRoot))
	rollupExitRoot := common.HexToHash(string(claimProof.L1InfoTreeLeaf.RollupExitRoot))

	originTokenAddress := common.HexToAddress(string(bridge.OriginAddress))
	metadata := common.Hex2Bytes(bridge.Metadata)

	// Claim on L2 using checked-out L2 transactor
	claimTx, err := env.L2.Contracts.L2Bridge.ClaimAsset(
		l2Opts,
		smtProofLocalExitRoot,
		smtProofRollupExitRoot,
		bridge.GlobalIndex,
		mainnetExitRoot,
		rollupExitRoot,
		bridge.OriginNetwork,
		originTokenAddress,
		bridge.DestinationNetwork,
		destinationAddress,
		bridgeAmount,
		metadata,
	)
	require.NoError(t, err, "failed to send claim transaction")
	log.Infof("L1->L2: Claim transaction sent: %s", claimTx.Hash().Hex())

	claimReceipt, err := bind.WaitMined(ctx, env.Clients.L2, claimTx)
	require.NoError(t, err, "failed to wait for claim transaction")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, claimReceipt.Status, "claim transaction failed")
	log.Infof("L1->L2: Claim successful in block %d", claimReceipt.BlockNumber.Uint64())

	// Verify balance
	finalL2Balance, err := env.Clients.L2.BalanceAt(ctx, destinationAddress, nil)
	require.NoError(t, err, "failed to get final L2 balance")
	balanceIncrease := new(big.Int).Sub(finalL2Balance, initialL2Balance)
	log.Infof("L1->L2: Balance increased by %s", balanceIncrease.String())

	minExpectedIncrease := new(big.Int).Div(new(big.Int).Mul(bridgeAmount, big.NewInt(90)), big.NewInt(100))
	require.True(t, balanceIncrease.Cmp(minExpectedIncrease) >= 0,
		"balance did not increase as expected")

	log.Info("L1->L2 flow completed successfully!")
}

// testBridgeL2ToL1 tests the L2 -> L1 bridge flow with native ETH
func testBridgeL2ToL1(t *testing.T, ctx context.Context, env *envs.Env, l1Opts, l2Opts *bind.TransactOpts, token common.Address) {
	t.Helper()
	log.Info("Starting L2->L1 bridge flow (native ETH)")

	// Get the L2 network ID
	callOpts := &bind.CallOpts{Context: ctx}
	l2NetworkID, err := env.L2.Contracts.L2Bridge.NetworkID(callOpts)
	require.NoError(t, err, "failed to get L2 network ID")

	bridgeAmount := big.NewInt(100000000000000) // 0.0001 ETH
	destinationAddress := l2Opts.From           // Use the funded L2 account (receives on L1)
	forceUpdateGlobalExitRoot := true

	// Get initial balance on L1
	initialL1Balance, err := env.Clients.L1.BalanceAt(ctx, destinationAddress, nil)
	require.NoError(t, err, "failed to get initial L1 balance")
	log.Infof("L2->L1: Initial L1 balance: %s", initialL1Balance.String())

	// Bridge native ETH from L2 to L1
	zeroAddr := common.Address{}
	if token == zeroAddr {
		l2Opts.Value = bridgeAmount
	}
	defer func() { l2Opts.Value = nil }()

	bridgeTx, err := env.L2.Contracts.L2Bridge.BridgeAsset(
		l2Opts,
		0, // L1 network ID
		destinationAddress,
		bridgeAmount,
		token,
		forceUpdateGlobalExitRoot,
		nil,
	)
	require.NoError(t, err, "failed to send bridge transaction")
	log.Infof("L2->L1: Bridge transaction sent: %s", bridgeTx.Hash().Hex())

	// Wait for transaction to be mined
	receipt, err := bind.WaitMined(ctx, env.Clients.L2, bridgeTx)
	require.NoError(t, err, "failed to wait for bridge transaction")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, receipt.Status, "bridge transaction failed")
	log.Infof("L2->L1: Bridge transaction mined in block %d", receipt.BlockNumber.Uint64())

	// Query bridge service for the bridge event
	var bridge *types.BridgeResponse
	maxBridgeRetries := 30
	for i := 0; i < maxBridgeRetries; i++ {
		pageSize := uint32(100)
		params := client.GetBridgesParams{
			NetworkID: l2NetworkID,
			PageSize:  &pageSize,
		}
		bridgesResult, err := env.Clients.BridgeService.GetBridges(ctx, params)
		if err == nil && bridgesResult != nil {
			for _, b := range bridgesResult.Bridges {
				if string(b.TxHash) == bridgeTx.Hash().Hex() {
					bridge = b
					break
				}
			}
		}
		if bridge != nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	require.NotNil(t, bridge, "bridge not found in bridge service")
	log.Infof("L2->L1: Bridge found: deposit_count=%d", bridge.DepositCount)

	depositCount := bridge.DepositCount

	// Wait for bridge to be included in L1 Info Tree (requires certificate submission)
	var l1InfoTreeIndex uint32
	maxL1InfoTreeRetries := 120
	for i := 0; i < maxL1InfoTreeRetries; i++ {
		idx, err := env.Clients.BridgeService.GetL1InfoTreeIndex(ctx, int(l2NetworkID), int(depositCount))
		if err == nil {
			l1InfoTreeIndex = idx
			log.Infof("L2->L1: L1InfoTree index: %d", l1InfoTreeIndex)
			break
		}
		if i%6 == 0 {
			log.Infof("L2->L1: Waiting for L1 Info Tree inclusion (needs certificate)... (%d/%d)", i+1, maxL1InfoTreeRetries)
		}
		time.Sleep(5 * time.Second)
	}
	require.NotZero(t, l1InfoTreeIndex, "bridge was not included in L1 Info Tree")

	// Get claim proof
	log.Info("L2->L1: Getting claim proof")
	claimProof, err := env.Clients.BridgeService.GetClaimProof(ctx, l2NetworkID, l1InfoTreeIndex, depositCount)
	require.NoError(t, err, "failed to get claim proof")
	require.NotNil(t, claimProof, "claim proof is nil")
	log.Info("L2->L1: Claim proof obtained")

	// Prepare claim
	var smtProofLocalExitRoot [32][32]byte
	for i, proofHex := range claimProof.ProofLocalExitRoot {
		if i >= 32 {
			break
		}
		smtProofLocalExitRoot[i] = common.HexToHash(string(proofHex))
	}

	var smtProofRollupExitRoot [32][32]byte
	for i, proofHex := range claimProof.ProofRollupExitRoot {
		if i >= 32 {
			break
		}
		smtProofRollupExitRoot[i] = common.HexToHash(string(proofHex))
	}

	mainnetExitRoot := common.HexToHash(string(claimProof.L1InfoTreeLeaf.MainnetExitRoot))
	rollupExitRoot := common.HexToHash(string(claimProof.L1InfoTreeLeaf.RollupExitRoot))

	originTokenAddress := common.HexToAddress(string(bridge.OriginAddress))
	metadata := common.Hex2Bytes(bridge.Metadata)

	// Claim on L1 using checked-out L1 transactor
	log.Info("L2->L1: Claiming on L1")
	claimTx, err := env.L1.Contracts.Bridge.ClaimAsset(
		l1Opts,
		smtProofLocalExitRoot,
		smtProofRollupExitRoot,
		bridge.GlobalIndex,
		mainnetExitRoot,
		rollupExitRoot,
		bridge.OriginNetwork,
		originTokenAddress,
		bridge.DestinationNetwork,
		destinationAddress,
		bridgeAmount,
		metadata,
	)
	require.NoError(t, err, "failed to send claim transaction")
	log.Infof("L2->L1: Claim transaction sent: %s", claimTx.Hash().Hex())

	claimReceipt, err := bind.WaitMined(ctx, env.Clients.L1, claimTx)
	require.NoError(t, err, "failed to wait for claim transaction")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, claimReceipt.Status, "claim transaction failed")
	log.Infof("L2->L1: Claim successful in block %d", claimReceipt.BlockNumber.Uint64())

	log.Info("L2->L1 flow completed successfully!")
}
