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
	"github.com/agglayer/aggkit/test/contracts/mintableerc20"
	"github.com/agglayer/aggkit/test/e2e/envs"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
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
	// Wait for the GER to be confirmed on-chain via the L2 GER contract. The bridge service
	// may report injection before the op-reth RPC node exposes the new block at 'latest',
	// which would cause ClaimAsset simulation to revert.
	gerHash := crypto.Keccak256Hash(mainnetExitRoot[:], rollupExitRoot[:])
	log.Debugf("waiting for GER to be confirmed on L2: gerHash=%s", gerHash.Hex())
	for i := 0; i < 30; i++ {
		ts, gerErr := env.L2.Contracts.GlobalExitRoot.GlobalExitRootMap(callOpts, gerHash)
		if gerErr == nil && ts.Sign() > 0 {
			log.Debugf("GER confirmed on L2: gerHash=%s", gerHash.Hex())
			break
		}
		time.Sleep(time.Second)
	}
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
	// Wait for the GER to be confirmed on-chain (same race guard as in BridgeL1ToL2).
	gerHash := crypto.Keccak256Hash(mainnetExitRoot[:], rollupExitRoot[:])
	log.Debugf("waiting for GER to be confirmed on L2: gerHash=%s", gerHash.Hex())
	for i := 0; i < 30; i++ {
		ts, gerErr := env.L2.Contracts.GlobalExitRoot.GlobalExitRootMap(callOpts, gerHash)
		if gerErr == nil && ts.Sign() > 0 {
			log.Debugf("GER confirmed on L2: gerHash=%s", gerHash.Hex())
			break
		}
		time.Sleep(time.Second)
	}
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
	var l1InfoTreeIndex uint32
	for i := 0; i < 120; i++ {
		idx, err := env.Clients.BridgeService.GetL1InfoTreeIndex(ctx, int(l2NetworkID), int(depositCount))
		if err == nil {
			l1InfoTreeIndex = idx
			break
		}
		time.Sleep(5 * time.Second)
	}
	if l1InfoTreeIndex == 0 {
		return errors.New("bridge not included in L1 Info Tree (L2->L1)")
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

// BridgeL2ToL2 runs the L2A -> L2B bridge flow using the given environment and transactors.
// originOpts authorizes the bridge tx on L2A; destOpts authorizes the claim on L2B and its
// From address is used as the destination address. token is the L2A-native asset to bridge
// (the caller is responsible for funding/approving it on L2A). Returns error for any failure.
//
// This helper requires a multi-chain env: env.L2B must be non-nil (EnvOpPP2Chains).
func BridgeL2ToL2(
	ctx context.Context, env *envs.Env, originOpts, destOpts *bind.TransactOpts, token common.Address,
) error {
	log.Info("Starting L2->L2 bridge flow (helper)")
	if env.L2B == nil {
		return errors.New("L2->L2 bridge requires a multi-chain env (env.L2B is nil)")
	}
	callOpts := &bind.CallOpts{Context: ctx}
	originNetworkID, err := env.L2.Contracts.L2Bridge.NetworkID(callOpts)
	if err != nil {
		return fmt.Errorf("failed to get origin (L2A) network ID: %w", err)
	}
	destNetworkID, err := env.L2B.Contracts.L2Bridge.NetworkID(callOpts)
	if err != nil {
		return fmt.Errorf("failed to get destination (L2B) network ID: %w", err)
	}
	bridgeAmount := big.NewInt(1e14)
	destinationAddress := destOpts.From
	forceUpdateGlobalExitRoot := true

	zeroAddr := common.Address{}
	if token == zeroAddr {
		originOpts.Value = bridgeAmount
	}
	defer func() { originOpts.Value = nil }()

	// 1. Send bridge tx on L2A, destination network = L2B's network ID.
	bridgeTx, err := env.L2.Contracts.L2Bridge.BridgeAsset(
		originOpts, destNetworkID, destinationAddress, bridgeAmount, token, forceUpdateGlobalExitRoot, nil,
	)
	if err != nil {
		return fmt.Errorf("failed to send L2->L2 bridge tx: %w", err)
	}
	log.Debugf("L2->L2 bridge tx submitted, waiting for mining: tx=%s", bridgeTx.Hash().Hex())
	receipt, err := bind.WaitMined(ctx, env.Clients.L2, bridgeTx)
	if err != nil {
		return fmt.Errorf("failed to wait for L2->L2 bridge tx: %w", err)
	}
	log.Debugf("L2->L2 bridge tx mined: tx=%s block=%d", bridgeTx.Hash().Hex(), receipt.BlockNumber.Uint64())
	if receipt.Status != ethtypes.ReceiptStatusSuccessful {
		return errors.New("L2->L2 bridge transaction failed")
	}

	// 2. Poll the origin (L2A) bridge service until the deposit is indexed.
	log.Debugf("waiting for L2->L2 bridge to appear in origin bridge service: tx=%s", bridgeTx.Hash().Hex())
	var bridge *types.BridgeResponse
	for i := 0; i < 60; i++ {
		pageSize := uint32(100)
		params := client.GetBridgesParams{NetworkID: originNetworkID, PageSize: &pageSize}
		bridgesResult, err := env.L2.BridgeService.GetBridges(ctx, params)
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
		time.Sleep(5 * time.Second)
	}
	if bridge == nil {
		return errors.New("bridge not found in origin bridge service")
	}
	log.Debugf("L2->L2 bridge found in origin bridge service: deposit_count=%d", bridge.DepositCount)
	depositCount := bridge.DepositCount

	// 3. Poll until the origin bridge is included in the L1 Info Tree (exit root settled to L1).
	log.Debugf("waiting for L2->L2 bridge to be included in L1 Info Tree: deposit_count=%d", depositCount)
	var l1InfoTreeIndex uint32
	for i := 0; i < 120; i++ {
		idx, err := env.L2.BridgeService.GetL1InfoTreeIndex(ctx, int(originNetworkID), int(depositCount))
		if err == nil {
			l1InfoTreeIndex = idx
			break
		}
		time.Sleep(5 * time.Second)
	}
	if l1InfoTreeIndex == 0 {
		return errors.New("bridge not included in L1 Info Tree (L2->L2)")
	}
	log.Debugf("L2->L2 bridge in L1 Info Tree: deposit_count=%d l1InfoTreeIndex=%d", depositCount, l1InfoTreeIndex)

	// 4. GER-propagation wait (the L2->L2-specific step): poll the DESTINATION (L2B) bridge
	// service until the L1 info leaf has been injected on L2B. This proves agglayer settled
	// the origin exit root and aggoracle injected the corresponding GER leaf onto L2B.
	log.Debugf("waiting for L1InfoTreeLeaf injection on L2B: dest=%d leafIndex=%d", destNetworkID, l1InfoTreeIndex)
	var injectedLeaf *types.L1InfoTreeLeafResponse
	for i := 0; i < 120; i++ {
		leaf, err := env.L2B.BridgeService.GetInjectedL1InfoLeaf(ctx, int(destNetworkID), int(l1InfoTreeIndex))
		if err == nil && leaf != nil {
			injectedLeaf = leaf
			break
		}
		time.Sleep(5 * time.Second)
	}
	if injectedLeaf == nil {
		return errors.New("L1InfoTreeLeaf was not injected on L2B")
	}
	log.Debugf("L1InfoTreeLeaf injected on L2B: destNetworkID=%d l1InfoTreeIndex=%d", destNetworkID, l1InfoTreeIndex)

	// 5. Record initial destination balance (before claim) for balance assertion.
	var initialDestBalance *big.Int
	if token != (common.Address{}) {
		wrappedTokenAddr, err := env.L2B.Contracts.L2Bridge.ComputeTokenProxyAddress(callOpts, originNetworkID, token)
		if err != nil {
			return fmt.Errorf("failed to compute wrapped token address on L2B: %w", err)
		}
		wrappedToken, err := mintableerc20.NewMintableerc20(wrappedTokenAddr, env.L2B.Client)
		if err != nil {
			return fmt.Errorf("failed to bind wrapped token on L2B: %w", err)
		}
		bal, err := wrappedToken.BalanceOf(callOpts, destinationAddress)
		if err != nil {
			// Token contract not yet deployed at this address; initial balance is zero.
			initialDestBalance = big.NewInt(0)
		} else {
			initialDestBalance = bal
		}
	}

	// 6. Fetch the claim proof from the ORIGIN (L2A) bridge service.
	// The origin bridge service knows about L2A's deposits; networkID must be
	// originNetworkID so the service uses L2A's bridge syncer for the local exit proof.
	log.Debugf("fetching L2->L2 claim proof: origin=%d leaf=%d deposit=%d", originNetworkID, l1InfoTreeIndex, depositCount)
	claimProof, err := env.L2.BridgeService.GetClaimProof(ctx, originNetworkID, l1InfoTreeIndex, depositCount)
	if err != nil || claimProof == nil {
		return fmt.Errorf("failed to get L2->L2 claim proof from L2A: %w", err)
	}
	log.Debugf("L2->L2 claim proof fetched from L2A")
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

	// 7. Execute the claim on L2B.
	log.Debugf("sending L2->L2 claim transaction on L2B")
	claimTx, err := env.L2B.Contracts.L2Bridge.ClaimAsset(
		destOpts, smtProofLocalExitRoot, smtProofRollupExitRoot, bridge.GlobalIndex,
		mainnetExitRoot, rollupExitRoot, bridge.OriginNetwork, originTokenAddress,
		bridge.DestinationNetwork, destinationAddress, bridgeAmount, metadata,
	)
	if err != nil {
		return fmt.Errorf("failed to send L2->L2 claim transaction: %w", err)
	}
	log.Debugf("L2B claim tx submitted, waiting for mining: tx=%s", claimTx.Hash().Hex())
	claimReceipt, err := bind.WaitMined(ctx, env.L2B.Client, claimTx)
	if err != nil {
		return fmt.Errorf("failed to wait for L2->L2 claim tx: %w", err)
	}
	log.Debugf("L2B claim tx mined: tx=%s block=%d", claimTx.Hash().Hex(), claimReceipt.BlockNumber.Uint64())
	if claimReceipt.Status != ethtypes.ReceiptStatusSuccessful {
		return errors.New("L2->L2 claim tx failed")
	}

	// 8. Assert destination balance increased by bridgeAmount.
	if initialDestBalance != nil {
		wrappedTokenAddr, err := env.L2B.Contracts.L2Bridge.ComputeTokenProxyAddress(callOpts, originNetworkID, token)
		if err != nil {
			return fmt.Errorf("failed to compute wrapped token address on L2B for balance check: %w", err)
		}
		wrappedToken, err := mintableerc20.NewMintableerc20(wrappedTokenAddr, env.L2B.Client)
		if err != nil {
			return fmt.Errorf("failed to bind wrapped token on L2B for balance check: %w", err)
		}
		finalDestBalance, err := wrappedToken.BalanceOf(callOpts, destinationAddress)
		if err != nil {
			return fmt.Errorf("failed to get destination balance after claim: %w", err)
		}
		expected := new(big.Int).Add(initialDestBalance, bridgeAmount)
		if finalDestBalance.Cmp(expected) != 0 {
			return fmt.Errorf("destination balance mismatch: got %s, expected %s (initial %s + bridged %s)",
				finalDestBalance, expected, initialDestBalance, bridgeAmount)
		}
		log.Debugf("L2->L2 destination balance verified: %s (initial %s + bridged %s)", finalDestBalance, initialDestBalance, bridgeAmount)
	}

	log.Info("L2->L2 flow completed successfully (helper)")
	return nil
}
