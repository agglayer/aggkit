package e2e

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/agglayer/aggkit/bridgeservice/client"
	"github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/test/e2e/envs"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

// BridgeL1ToL2 runs the L1 -> L2 bridge flow using the given environment and transactors.
// Performs the full deposit and claim flows. Returns error for any non-successful operation.
func BridgeL1ToL2(ctx context.Context, env *envs.Env, l1Opts, l2Opts *bind.TransactOpts) error {
	log.Info("Starting L1->L2 bridge flow (helper)")
	callOpts := &bind.CallOpts{Context: ctx}
	l2NetworkID, err := env.L2.Contracts.L2Bridge.NetworkID(callOpts)
	if err != nil {
		return fmt.Errorf("failed to get L2 network ID: %w", err)
	}
	bridgeAmount := big.NewInt(1e14) // 0.0001 ETH
	destinationAddress := l1Opts.From
	forceUpdateGlobalExitRoot := true
	initialL2Balance, err := env.Clients.L2.BalanceAt(ctx, destinationAddress, nil)
	if err != nil {
		return fmt.Errorf("failed to get initial L2 balance: %w", err)
	}
	l1Opts.Value = bridgeAmount
	defer func() { l1Opts.Value = nil }()
	tx, err := env.L1.Contracts.Bridge.BridgeAsset(
		l1Opts, l2NetworkID, destinationAddress, bridgeAmount,
		common.Address{}, forceUpdateGlobalExitRoot, nil,
	)
	if err != nil {
		return fmt.Errorf("failed to send bridge transaction: %w", err)
	}
	log.Debugf("L1->L2 bridge tx submitted, waiting for mining: tx=%s", tx.Hash().Hex())
	receipt, err := bind.WaitMined(ctx, env.Clients.L1, tx)
	if err != nil {
		return fmt.Errorf("failed to wait for bridge tx: %w", err)
	}
	log.Debugf("L1->L2 bridge tx mined: tx=%s block=%d", tx.Hash().Hex(), receipt.BlockNumber.Uint64())
	if receipt.Status != ethtypes.ReceiptStatusSuccessful {
		return errors.New("bridge transaction failed")
	}
	log.Debugf("waiting for bridge to appear in bridge service: tx=%s", tx.Hash().Hex())
	var bridge *types.BridgeResponse
	for i := 0; i < 30; i++ {
		pageSize := uint32(100)
		params := client.GetBridgesParams{NetworkID: 0, PageSize: &pageSize}
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
	if bridge == nil {
		return errors.New("bridge not found in bridge service")
	}
	log.Debugf("bridge found in bridge service: deposit_count=%d", bridge.DepositCount)
	depositCount := bridge.DepositCount
	log.Debugf("waiting for bridge to be included in L1 Info Tree: deposit_count=%d", depositCount)
	var l1InfoTreeIndex uint32
	for i := 0; i < 60; i++ {
		idx, err := env.Clients.BridgeService.GetL1InfoTreeIndex(ctx, 0, int(depositCount))
		if err == nil {
			l1InfoTreeIndex = idx
			break
		}
		time.Sleep(5 * time.Second)
	}
	if l1InfoTreeIndex == 0 {
		return errors.New("bridge was not included in L1 Info Tree")
	}
	log.Debugf("bridge included in L1 Info Tree: deposit_count=%d l1InfoTreeIndex=%d", depositCount, l1InfoTreeIndex)
	log.Debugf("waiting for L1InfoTreeLeaf to be injected on L2: l2NetworkID=%d l1InfoTreeIndex=%d", l2NetworkID, l1InfoTreeIndex)
	var injectedLeaf *types.L1InfoTreeLeafResponse
	for i := 0; i < 120; i++ {
		leaf, err := env.Clients.BridgeService.GetInjectedL1InfoLeaf(ctx, int(l2NetworkID), int(l1InfoTreeIndex))
		if err == nil && leaf != nil {
			injectedLeaf = leaf
			break
		}
		time.Sleep(5 * time.Second)
	}
	if injectedLeaf == nil {
		return errors.New("L1InfoTreeLeaf was not injected")
	}
	log.Debugf("L1InfoTreeLeaf injected: l2NetworkID=%d l1InfoTreeIndex=%d", l2NetworkID, l1InfoTreeIndex)
	log.Debugf("fetching claim proof: networkID=0 l1InfoTreeIndex=%d depositCount=%d", l1InfoTreeIndex, depositCount)
	claimProof, err := env.Clients.BridgeService.GetClaimProof(ctx, 0, l1InfoTreeIndex, depositCount)
	if err != nil || claimProof == nil {
		return fmt.Errorf("failed to get claim proof: %w", err)
	}
	log.Debugf("claim proof fetched")
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
	metadata := common.FromHex(bridge.Metadata)
	log.Debugf("sending claim transaction on L2")
	claimTx, err := env.L2.Contracts.L2Bridge.ClaimAsset(
		l2Opts, smtProofLocalExitRoot, smtProofRollupExitRoot,
		bridge.GlobalIndex, mainnetExitRoot, rollupExitRoot,
		bridge.OriginNetwork, originTokenAddress, bridge.DestinationNetwork,
		destinationAddress, bridgeAmount, metadata,
	)
	if err != nil {
		return fmt.Errorf("failed to send claim transaction: %w", err)
	}
	log.Debugf("L2 claim tx submitted, waiting for mining: tx=%s", claimTx.Hash().Hex())
	claimReceipt, err := bind.WaitMined(ctx, env.Clients.L2, claimTx)
	if err != nil {
		return fmt.Errorf("failed to wait for claim tx: %w", err)
	}
	log.Debugf("L2 claim tx mined: tx=%s block=%d", claimTx.Hash().Hex(), claimReceipt.BlockNumber.Uint64())
	if claimReceipt.Status != ethtypes.ReceiptStatusSuccessful {
		return errors.New("claim transaction failed")
	}
	finalL2Balance, err := env.Clients.L2.BalanceAt(ctx, destinationAddress, nil)
	if err != nil {
		return fmt.Errorf("failed to get final L2 balance: %w", err)
	}
	balanceIncrease := new(big.Int).Sub(finalL2Balance, initialL2Balance)
	minExpectedIncrease := new(big.Int).Div(new(big.Int).Mul(bridgeAmount, big.NewInt(90)), big.NewInt(100))
	if balanceIncrease.Cmp(minExpectedIncrease) < 0 {
		return fmt.Errorf("balance did not increase as expected: got %s, want at least %s", balanceIncrease.String(), minExpectedIncrease.String())
	}
	log.Info("L1->L2 flow completed successfully (helper)")
	return nil
}

// bridgeResult holds the outcome of an L1->L2 bridge operation for use in E2E tests.
type bridgeResult struct {
	Bridge          *types.BridgeResponse
	DepositCount    uint32
	L1InfoTreeIndex uint32
	ClaimTxHash     common.Hash
	GlobalIndex     *big.Int
	DestinationAddr common.Address
	BridgeAmount    *big.Int
}

// BridgeL1ToL2WithResult performs a full L1->L2 bridge and claim using the given transactors.
// Uses l2Opts.From as the destination address. Returns detailed bridge and claim information.
func BridgeL1ToL2WithResult(ctx context.Context, env *envs.Env, l1Opts, l2Opts *bind.TransactOpts, bridgeAmount *big.Int) (*bridgeResult, error) {
	callOpts := &bind.CallOpts{Context: ctx}
	l2NetworkID, err := env.L2.Contracts.L2Bridge.NetworkID(callOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to get L2 network ID: %w", err)
	}
	destinationAddress := l2Opts.From
	forceUpdateGlobalExitRoot := true
	l1Opts.Value = bridgeAmount
	defer func() { l1Opts.Value = nil }()
	tx, err := env.L1.Contracts.Bridge.BridgeAsset(
		l1Opts, l2NetworkID, destinationAddress, bridgeAmount,
		common.Address{}, forceUpdateGlobalExitRoot, nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to send bridge transaction: %w", err)
	}
	log.Debugf("L1->L2 bridge tx submitted, waiting for mining: tx=%s", tx.Hash().Hex())
	receipt, err := bind.WaitMined(ctx, env.Clients.L1, tx)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for bridge tx: %w", err)
	}
	log.Debugf("L1->L2 bridge tx mined: tx=%s block=%d", tx.Hash().Hex(), receipt.BlockNumber.Uint64())
	if receipt.Status != ethtypes.ReceiptStatusSuccessful {
		return nil, errors.New("bridge transaction failed")
	}
	log.Debugf("waiting for bridge to appear in bridge service: tx=%s", tx.Hash().Hex())
	var bridge *types.BridgeResponse
	for i := 0; i < 30; i++ {
		pageSize := uint32(100)
		params := client.GetBridgesParams{NetworkID: 0, PageSize: &pageSize}
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
	if bridge == nil {
		return nil, errors.New("bridge not found in bridge service")
	}
	log.Debugf("bridge found in bridge service: deposit_count=%d", bridge.DepositCount)
	depositCount := bridge.DepositCount
	log.Debugf("waiting for bridge to be included in L1 Info Tree: deposit_count=%d", depositCount)
	var l1InfoTreeIndex uint32
	for i := 0; i < 60; i++ {
		idx, err := env.Clients.BridgeService.GetL1InfoTreeIndex(ctx, 0, int(depositCount))
		if err == nil {
			l1InfoTreeIndex = idx
			break
		}
		time.Sleep(5 * time.Second)
	}
	if l1InfoTreeIndex == 0 {
		return nil, errors.New("bridge was not included in L1 Info Tree")
	}
	log.Debugf("bridge included in L1 Info Tree: deposit_count=%d l1InfoTreeIndex=%d", depositCount, l1InfoTreeIndex)
	log.Debugf("waiting for L1InfoTreeLeaf to be injected on L2: l2NetworkID=%d l1InfoTreeIndex=%d", l2NetworkID, l1InfoTreeIndex)
	for i := 0; i < 120; i++ {
		_, err := env.Clients.BridgeService.GetInjectedL1InfoLeaf(ctx, int(l2NetworkID), int(l1InfoTreeIndex))
		if err == nil {
			break
		}
		time.Sleep(5 * time.Second)
	}
	log.Debugf("L1InfoTreeLeaf injected: l2NetworkID=%d l1InfoTreeIndex=%d", l2NetworkID, l1InfoTreeIndex)
	log.Debugf("fetching claim proof: networkID=0 l1InfoTreeIndex=%d depositCount=%d", l1InfoTreeIndex, depositCount)
	claimProof, err := env.Clients.BridgeService.GetClaimProof(ctx, 0, l1InfoTreeIndex, depositCount)
	if err != nil || claimProof == nil {
		return nil, fmt.Errorf("failed to get claim proof: %w", err)
	}
	log.Debugf("claim proof fetched")
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
	metadata := common.FromHex(bridge.Metadata)
	log.Debugf("sending claim transaction on L2")
	claimTx, err := env.L2.Contracts.L2Bridge.ClaimAsset(
		l2Opts, smtProofLocalExitRoot, smtProofRollupExitRoot,
		bridge.GlobalIndex, mainnetExitRoot, rollupExitRoot,
		bridge.OriginNetwork, originTokenAddress, bridge.DestinationNetwork,
		destinationAddress, bridgeAmount, metadata,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to send claim transaction: %w", err)
	}
	log.Debugf("L2 claim tx submitted, waiting for mining: tx=%s", claimTx.Hash().Hex())
	claimReceipt, err := bind.WaitMined(ctx, env.Clients.L2, claimTx)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for claim tx: %w", err)
	}
	log.Debugf("L2 claim tx mined: tx=%s block=%d", claimTx.Hash().Hex(), claimReceipt.BlockNumber.Uint64())
	if claimReceipt.Status != ethtypes.ReceiptStatusSuccessful {
		return nil, errors.New("claim transaction failed")
	}
	return &bridgeResult{
		Bridge:          bridge,
		DepositCount:    depositCount,
		L1InfoTreeIndex: l1InfoTreeIndex,
		ClaimTxHash:     claimTx.Hash(),
		GlobalIndex:     bridge.GlobalIndex,
		DestinationAddr: destinationAddress,
		BridgeAmount:    bridgeAmount,
	}, nil
}

// BridgeL1NoClaim performs a real L1->L2 bridge and waits for it to be fully indexed but does not claim.
// label is used in log messages to identify the caller context (e.g. "B1", "B2-1").
func BridgeL1NoClaim(ctx context.Context, env *envs.Env, l1Opts, l2Opts *bind.TransactOpts, bridgeAmount *big.Int, label string) (*bridgeResult, error) {
	callOpts := &bind.CallOpts{Context: ctx}
	l2NetworkID, err := env.L2.Contracts.L2Bridge.NetworkID(callOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to get L2 network ID: %w", err)
	}
	destinationAddress := l2Opts.From
	forceUpdateGlobalExitRoot := true
	l1Opts.Value = bridgeAmount
	defer func() { l1Opts.Value = nil }()
	tx, err := env.L1.Contracts.Bridge.BridgeAsset(
		l1Opts, l2NetworkID, destinationAddress, bridgeAmount,
		common.Address{}, forceUpdateGlobalExitRoot, nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to send bridge tx: %w", err)
	}
	log.Infof("[%s] bridge tx sent, tx=%s", label, tx.Hash().Hex())
	log.Debugf("[%s] waiting for bridge tx to be mined: tx=%s", label, tx.Hash().Hex())
	mineCtx, mineCancel := context.WithTimeout(ctx, 30*time.Second)
	defer mineCancel()
	receipt, err := bind.WaitMined(mineCtx, env.Clients.L1, tx)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for bridge tx: %w", err)
	}
	if receipt.Status != ethtypes.ReceiptStatusSuccessful {
		return nil, errors.New("bridge transaction failed")
	}
	log.Infof("[%s] bridge tx mined at block %d", label, receipt.BlockNumber.Uint64())
	bridgeEvent, err := env.L1.Contracts.Bridge.ParseBridgeEvent(*receipt.Logs[0])
	if err != nil {
		return nil, fmt.Errorf("failed to parse bridge event: %w", err)
	}
	depositCount := uint64(bridgeEvent.DepositCount)
	log.Debugf("[%s] waiting for bridge to appear in bridge service: tx=%s deposit_count=%d", label, tx.Hash().Hex(), depositCount)
	var bridge *types.BridgeResponse
	for i := 0; i < 30; i++ {
		pageSize := uint32(100)
		params := client.GetBridgesParams{NetworkID: 0, PageSize: &pageSize, DepositCount: &depositCount}
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
		if (i+1)%5 == 0 {
			log.Infof("[%s] bridge not in bridge service yet, attempt %d/30", label, i+1)
		}
		time.Sleep(2 * time.Second)
	}
	if bridge == nil {
		return nil, errors.New("bridge not found in bridge service")
	}
	log.Infof("[%s] bridge found, deposit_count=%d", label, bridge.DepositCount)
	log.Debugf("[%s] waiting for bridge to be included in L1 Info Tree: deposit_count=%d", label, depositCount)
	var l1InfoTreeIndex uint32
	for i := 0; i < 60; i++ {
		idx, err := env.Clients.BridgeService.GetL1InfoTreeIndex(ctx, 0, int(depositCount))
		if err == nil {
			l1InfoTreeIndex = idx
			break
		}
		if (i+1)%6 == 0 {
			log.Infof("[%s] L1InfoTreeIndex not ready, attempt %d/60", label, i+1)
		}
		time.Sleep(5 * time.Second)
	}
	if l1InfoTreeIndex == 0 {
		return nil, errors.New("bridge not included in L1 Info Tree")
	}
	log.Infof("[%s] L1InfoTreeIndex ready: %d", label, l1InfoTreeIndex)
	log.Debugf("[%s] waiting for L1InfoTreeLeaf to be injected on L2: l2NetworkID=%d l1InfoTreeIndex=%d", label, l2NetworkID, l1InfoTreeIndex)
	for i := 0; i < 120; i++ {
		_, err := env.Clients.BridgeService.GetInjectedL1InfoLeaf(ctx, int(l2NetworkID), int(l1InfoTreeIndex))
		if err == nil {
			break
		}
		if (i+1)%12 == 0 {
			log.Infof("[%s] GetInjectedL1InfoLeaf not ready, attempt %d/120", label, i+1)
		}
		time.Sleep(5 * time.Second)
	}
	log.Infof("[%s] bridge fully indexed (no claim)", label)
	log.Debugf("[%s] L1InfoTreeLeaf injected: l2NetworkID=%d l1InfoTreeIndex=%d", label, l2NetworkID, l1InfoTreeIndex)
	return &bridgeResult{
		Bridge:          bridge,
		DepositCount:    uint32(depositCount),
		L1InfoTreeIndex: l1InfoTreeIndex,
		ClaimTxHash:     common.Hash{},
		GlobalIndex:     bridge.GlobalIndex,
		DestinationAddr: destinationAddress,
		BridgeAmount:    bridgeAmount,
	}, nil
}

// BridgeL2ToL1 runs the L2 -> L1 bridge flow using the given environment and transactors.
// Returns error for any failure.
func BridgeL2ToL1(ctx context.Context, env *envs.Env, l1Opts, l2Opts *bind.TransactOpts, token common.Address) error {
	log.Info("Starting L2->L1 bridge flow (helper)")
	callOpts := &bind.CallOpts{Context: ctx}
	l2NetworkID, err := env.L2.Contracts.L2Bridge.NetworkID(callOpts)
	if err != nil {
		return fmt.Errorf("failed to get L2 network ID: %w", err)
	}
	bridgeAmount := big.NewInt(1e14)
	destinationAddress := l2Opts.From
	forceUpdateGlobalExitRoot := true
	// removed unused variable 'initialL1Balance' (was: initialL1Balance, err := env.Clients.L1.BalanceAt(...))
	zeroAddr := common.Address{}
	if token == zeroAddr {
		l2Opts.Value = bridgeAmount
	}
	defer func() { l2Opts.Value = nil }()
	bridgeTx, err := env.L2.Contracts.L2Bridge.BridgeAsset(
		l2Opts, 0, destinationAddress, bridgeAmount, token, forceUpdateGlobalExitRoot, nil,
	)
	if err != nil {
		return fmt.Errorf("failed to send L2->L1 bridge tx: %w", err)
	}
	log.Debugf("L2->L1 bridge tx submitted, waiting for mining: tx=%s", bridgeTx.Hash().Hex())
	receipt, err := bind.WaitMined(ctx, env.Clients.L2, bridgeTx)
	if err != nil {
		return fmt.Errorf("failed to wait for L2->L1 bridge tx: %w", err)
	}
	log.Debugf("L2->L1 bridge tx mined: tx=%s block=%d", bridgeTx.Hash().Hex(), receipt.BlockNumber.Uint64())
	if receipt.Status != ethtypes.ReceiptStatusSuccessful {
		return errors.New("L2->L1 bridge transaction failed")
	}
	log.Debugf("waiting for L2->L1 bridge to appear in bridge service: tx=%s", bridgeTx.Hash().Hex())
	var bridge *types.BridgeResponse
	for i := 0; i < 30; i++ {
		pageSize := uint32(100)
		params := client.GetBridgesParams{NetworkID: l2NetworkID, PageSize: &pageSize}
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
	if bridge == nil {
		return errors.New("bridge not found in bridge service")
	}
	log.Debugf("L2->L1 bridge found in bridge service: deposit_count=%d", bridge.DepositCount)
	depositCount := bridge.DepositCount
	log.Debugf("waiting for L2->L1 bridge to be included in L1 Info Tree: deposit_count=%d", depositCount)
	// An L2->L1 bridge becomes claimable only once a PP certificate covering its exit settles and the
	// resulting rollup exit root propagates into a new GER / L1 Info Tree leaf — a multi-epoch async
	// process. Poll until the bridge service resolves the L1 Info Tree index or the caller's context
	// deadline is reached (the caller owns the timeout budget), rather than a fixed iteration count.
	// GetL1InfoTreeIndex returns an error while the index is not yet available; once it succeeds the
	// index is valid (including a legitimate index 0 for the first leaf), so success is keyed off err.
	var l1InfoTreeIndex uint32
	for {
		idx, err := env.Clients.BridgeService.GetL1InfoTreeIndex(ctx, int(l2NetworkID), int(depositCount))
		if err == nil {
			l1InfoTreeIndex = idx
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("bridge not included in L1 Info Tree (L2->L1) deposit_count=%d: %w",
				depositCount, ctx.Err())
		case <-time.After(5 * time.Second):
		}
	}
	log.Debugf("L2->L1 bridge included in L1 Info Tree: deposit_count=%d l1InfoTreeIndex=%d", depositCount, l1InfoTreeIndex)
	log.Debugf("fetching L2->L1 claim proof: networkID=%d l1InfoTreeIndex=%d depositCount=%d", l2NetworkID, l1InfoTreeIndex, depositCount)
	claimProof, err := env.Clients.BridgeService.GetClaimProof(ctx, l2NetworkID, l1InfoTreeIndex, depositCount)
	if err != nil || claimProof == nil {
		return fmt.Errorf("failed to get claim proof: %w", err)
	}
	log.Debugf("L2->L1 claim proof fetched")
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
	metadata := common.FromHex(bridge.Metadata)
	log.Debugf("sending L2->L1 claim transaction on L1")
	claimTx, err := env.L1.Contracts.Bridge.ClaimAsset(
		l1Opts, smtProofLocalExitRoot, smtProofRollupExitRoot, bridge.GlobalIndex,
		mainnetExitRoot, rollupExitRoot, bridge.OriginNetwork, originTokenAddress,
		bridge.DestinationNetwork, destinationAddress, bridgeAmount, metadata,
	)
	if err != nil {
		return fmt.Errorf("failed to send L2->L1 claim transaction: %w", err)
	}
	log.Debugf("L1 claim tx submitted, waiting for mining: tx=%s", claimTx.Hash().Hex())
	claimReceipt, err := bind.WaitMined(ctx, env.Clients.L1, claimTx)
	if err != nil {
		return fmt.Errorf("failed to wait for L2->L1 claim tx: %w", err)
	}
	log.Debugf("L1 claim tx mined: tx=%s block=%d", claimTx.Hash().Hex(), claimReceipt.BlockNumber.Uint64())
	if claimReceipt.Status != ethtypes.ReceiptStatusSuccessful {
		return errors.New("L2->L1 claim tx failed")
	}
	log.Info("L2->L1 flow completed successfully (helper)")
	return nil
}
