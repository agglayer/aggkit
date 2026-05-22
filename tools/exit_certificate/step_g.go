package exit_certificate

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os/exec"
	"strings"
	"time"

	agglayerbridgel2 "github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	anvilReadyTimeout   = 30 * time.Second
	anvilPollInterval   = 300 * time.Millisecond
	receiptPollTimeout  = 30 * time.Second
	receiptPollInterval = 200 * time.Millisecond

	// largeETHBalance is MaxUint256 in hex, enough for any bridgeAsset call regardless of exit amounts.
	largeETHBalance = "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"

	abiFuncSelectorSize = 4 // bytes in an ABI function selector
)

var (
	// bridgeABI is the parsed ABI for the AgglayerBridgeL2 contract, used to
	// encode/decode bridgeAsset, getRoot, and getTokenWrappedAddress calls.
	bridgeABI abi.ABI

	bridgeEventTopicHash common.Hash
)

func init() {
	parsed, err := agglayerbridgel2.Agglayerbridgel2MetaData.GetAbi()
	if err != nil {
		panic(fmt.Sprintf("parse agglayerbridgel2 ABI: %v", err))
	}
	bridgeABI = *parsed
	bridgeEventTopicHash = crypto.Keccak256Hash([]byte(
		"BridgeEvent(uint8,uint32,address,uint32,address,uint256,bytes,uint32)",
	))
}

// tokenOriginKey identifies an L1/L2 token by its origin chain and address.
type tokenOriginKey struct {
	network uint32
	addr    common.Address
}

// rpcLog is the JSON representation of a log entry in an eth_getTransactionReceipt response.
type rpcLog struct {
	Address string   `json:"address"`
	Topics  []string `json:"topics"`
	Data    string   `json:"data"`
}

type bridgeEventLog struct {
	LeafType           uint8
	OriginNetwork      uint32
	OriginAddress      common.Address
	DestinationNetwork uint32
	DestinationAddress common.Address
	Amount             *big.Int
	Metadata           []byte
	DepositCount       uint32
}

// RunStepG computes Certificate.NewLocalExitRoot by replaying all bridge exits
// against an Anvil shadow-fork of the L2 chain at cfg.ResolvedTargetBlock.
// lbtEntries is the output of Step 0; when non-nil it is used as a lookup table for
// wrapped token addresses so that getTokenWrappedAddress RPC calls are avoided.
func RunStepG(
	ctx context.Context, cfg *Config, certificate *agglayertypes.Certificate, lbtEntries []LBTEntry,
) (*StepGResult, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP G - Calculate NewLocalExitRoot")
	log.Info("═══════════════════════════════════════════")

	if certificate == nil {
		return nil, fmt.Errorf("certificate is nil")
	}

	if len(certificate.BridgeExits) == 0 {
		log.Info("No bridge exits — using EmptyLER")
		initialLER, err := readLocalExitRoot(ctx, cfg.L2RPCURL, cfg.L2BridgeAddress, toBlockTag(cfg.ResolvedTargetBlock))
		if err != nil {
			log.Warnf("Could not read initial LocalExitRoot: %v", err)
		}
		log.Infof("InitialLocalExitRoot: %s", initialLER.Hex())
		return &StepGResult{
			InitialLocalExitRoot: initialLER,
			NewLocalExitRoot:     bridgesynctypes.EmptyLER,
			BridgeExitCount:      0,
		}, nil
	}

	if err := checkAnvilAvailable(); err != nil {
		return nil, err
	}

	anvilURL, cleanup, err := startAnvil(ctx, cfg.L2RPCURL, cfg.ResolvedTargetBlock)
	if err != nil {
		return nil, fmt.Errorf("start anvil: %w", err)
	}
	defer cleanup()

	gasTokenNetwork, gasTokenAddress, err := fetchGasTokenInfo(ctx, cfg.L2RPCURL, cfg.L2BridgeAddress)
	if err != nil {
		log.Warnf("Failed to fetch gas token info (assuming standard ETH): %v", err)
		gasTokenNetwork = 0
		gasTokenAddress = common.Address{}
	}

	initialLER, err := readLocalExitRoot(ctx, anvilURL, cfg.L2BridgeAddress, "latest")
	if err != nil {
		return nil, fmt.Errorf("read initial local exit root: %w", err)
	}
	log.Infof("InitialLocalExitRoot: %s", initialLER.Hex())

	lbtMap := buildLBTTokenMap(lbtEntries)
	l2Tokens, err := resolveTokenAddresses(
		ctx, anvilURL, cfg.L2BridgeAddress, certificate.BridgeExits,
		cfg.L2NetworkID, gasTokenNetwork, gasTokenAddress, lbtMap,
	)
	if err != nil {
		return nil, fmt.Errorf("resolve token addresses: %w", err)
	}
	for k, v := range l2Tokens {
		log.Debugf("token map: origin(network=%d addr=%s) -> L2 wrapped %s", k.network, k.addr.Hex(), v.Hex())
	}

	metadatas := make([][]byte, 0, len(certificate.BridgeExits))
	for i, bridge := range certificate.BridgeExits {
		isNative := isNativeBridgeExit(bridge.TokenInfo, gasTokenNetwork, gasTokenAddress)
		log.Infof("[%d/%d] bridgeAsset bridge exit [%d/%s] -> %s:  amount=%s isNative=%t", i+1, len(certificate.BridgeExits),
			bridge.TokenInfo.OriginNetwork, bridge.TokenInfo.OriginTokenAddress.Hex(),
			bridge.DestinationAddress.Hex(),
			bridge.Amount.String(), isNative)

		var l2TokenAddr common.Address
		if !isNative {
			l2TokenAddr, err = findTokenAddress(bridge, l2Tokens)
			if err != nil {
				return nil, fmt.Errorf("find token address: %w", err)
			}

			// Do an allowance of ERC20 before doing the bridge
			if err := approveERC20(
				ctx, anvilURL, cfg.L2BridgeAddress, bridge.DestinationAddress, bridge, l2TokenAddr,
			); err != nil {
				return nil, fmt.Errorf("approve ERC20: %w", err)
			}
		}

		event, err := bridgeAsset(ctx, anvilURL, cfg.L2BridgeAddress, bridge, isNative, l2TokenAddr)
		if err != nil {
			return nil, fmt.Errorf("bridge asset: %w", err)
		}
		log.Debugf("BridgeEvent depositCount=%d originNetwork=%d originAddress=%s amount=%s metadata=%x",
			event.DepositCount, event.OriginNetwork, event.OriginAddress.Hex(), event.Amount, event.Metadata)
		metadatas = append(metadatas, event.Metadata)
	}

	ler, err := readLocalExitRoot(ctx, anvilURL, cfg.L2BridgeAddress, "latest")
	if err != nil {
		return nil, fmt.Errorf("read local exit root: %w", err)
	}

	result := &StepGResult{
		InitialLocalExitRoot: initialLER,
		NewLocalExitRoot:     ler,
		BridgeExitCount:      uint64(len(certificate.BridgeExits)),
		BridgeExitMetadata:   metadatas,
	}
	log.Infof("Bridge exits processed: %d", result.BridgeExitCount)
	log.Infof("NewLocalExitRoot: %s", result.NewLocalExitRoot.Hex())
	log.Info("STEP G complete")
	return result, nil
}

func isNativeBridgeExit(
	ti *agglayertypes.TokenInfo, gasTokenNetwork uint32, gasTokenAddress common.Address,
) bool {
	return ti == nil ||
		ti.OriginTokenAddress == (common.Address{}) ||
		(ti.OriginNetwork == gasTokenNetwork && ti.OriginTokenAddress == gasTokenAddress)
}

// findTokenAddress looks up the L2 ERC-20 address for a bridge exit in the token map
// returned by resolveTokenAddresses.
func findTokenAddress(
	bridgeExit *agglayertypes.BridgeExit, tokenMap map[tokenOriginKey]common.Address,
) (common.Address, error) {
	if bridgeExit.TokenInfo == nil {
		return common.Address{}, fmt.Errorf("bridge exit has nil TokenInfo")
	}
	ti := bridgeExit.TokenInfo
	addr, ok := tokenMap[tokenOriginKey{ti.OriginNetwork, ti.OriginTokenAddress}]
	if !ok {
		return common.Address{}, fmt.Errorf("token (network=%d addr=%s) not found in token map",
			ti.OriginNetwork, ti.OriginTokenAddress.Hex())
	}
	return addr, nil
}

// approveERC20 sets the token balance and bridge allowance for sender on the ERC-20 token
// via Anvil storage manipulation (OZ slot 0 / slot 1), so that the subsequent bridgeAsset
// call does not revert with insufficient balance or allowance.
func approveERC20(ctx context.Context, rpcURL string, bridgeAddr, sender common.Address,
	bridgeExit *agglayertypes.BridgeExit,
	l2TokenAddr common.Address) error {
	tokenAddr := l2TokenAddr
	if tokenAddr == (common.Address{}) {
		return fmt.Errorf("invalid L2 token address")
	}

	log.Debugf("Approving ERC-20 L2 token: %s for L1 token (network=%d addr=%s) with amount %s",
		tokenAddr.Hex(), bridgeExit.TokenInfo.OriginNetwork,
		bridgeExit.TokenInfo.OriginTokenAddress.Hex(), bridgeExit.Amount.String())

	amount := bridgeExit.Amount
	if amount == nil {
		amount = new(big.Int)
	}

	if err := ensureERC20Balance(ctx, rpcURL, tokenAddr, sender, amount); err != nil {
		return fmt.Errorf("ensure ERC-20 balance: %w", err)
	}

	callData := encodeERC20ApproveCallRaw(bridgeAddr, amount)
	if err := setupImpersonation(ctx, rpcURL, sender); err != nil {
		return fmt.Errorf("setup impersonation for %s to approve ERC-20 token: %w", sender.Hex(), err)
	}

	txHash, err := sendAnvilTransaction(ctx, rpcURL, sender, tokenAddr, nil, callData)
	if err != nil {
		log.Errorf("Failed to approve ERC-20 token: %v", err)
		return fmt.Errorf("failed approve ERC-20 token: %w", err)
	}

	if _, err := waitForReceipt(ctx, rpcURL, txHash); err != nil {
		return fmt.Errorf("wait for approve ERC-20 token (%s) receipt: %w", tokenAddr.Hex(), err)
	}
	log.Debugf("✅ ERC-20 approval for bridgeAddr for L2Token: %s successful", tokenAddr.Hex())

	return nil
}

func bridgeAsset(ctx context.Context, rpcURL string,
	bridgeAddr common.Address,
	bridgeExit *agglayertypes.BridgeExit,
	isNative bool,
	l2TokenAddr common.Address) (*bridgeEventLog, error) {
	sender := bridgeExit.DestinationAddress

	var value *big.Int

	if isNative && bridgeExit.Amount != nil {
		value = bridgeExit.Amount
	}

	if err := setupImpersonation(ctx, rpcURL, sender); err != nil {
		return nil, fmt.Errorf("setup impersonation for %s: %w", sender.Hex(), err)
	}

	callData := encodeBridgeAssetCallRaw(
		bridgeExit.DestinationNetwork,
		bridgeExit.DestinationAddress,
		bridgeExit.Amount,
		l2TokenAddr,
	)

	txHash, err := sendAnvilTransaction(ctx, rpcURL, sender, bridgeAddr, value, callData)
	if err != nil {
		log.Errorf("Failed to bridge asset: %v", err)
		return nil, fmt.Errorf("failed bridge asset: %w", err)
	}
	logs, err := waitForReceipt(ctx, rpcURL, txHash)
	if err != nil {
		log.Errorf("Failed to get receipt for bridge asset tx: %v", err)
		return nil, fmt.Errorf("failed to get receipt for bridge asset tx: %w", err)
	}
	event, err := parseBridgeEventFromLogs(logs)
	if err != nil {
		return nil, fmt.Errorf("parse BridgeEvent from receipt: %w", err)
	}
	return event, nil
}

func checkAnvilAvailable() error {
	if _, err := exec.LookPath("anvil"); err != nil {
		return fmt.Errorf("anvil not found in $PATH — install the Foundry toolchain from https://getfoundry.sh")
	}
	return nil
}

func findFreePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected listener address type %T", ln.Addr())
	}
	return tcpAddr.Port, nil
}

func startAnvil(ctx context.Context, l2RPCURL string, targetBlock uint64) (string, func(), error) {
	port, err := findFreePort()
	if err != nil {
		return "", nil, fmt.Errorf("find free port: %w", err)
	}

	cmd := exec.CommandContext(ctx, "anvil",
		"--fork-url", l2RPCURL,
		"--fork-block-number", fmt.Sprintf("%d", targetBlock),
		"--port", fmt.Sprintf("%d", port),
		"--silent",
	)
	if err := cmd.Start(); err != nil {
		return "", nil, fmt.Errorf("start anvil process: %w", err)
	}

	cleanup := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}

	anvilURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	if err := waitForAnvil(ctx, anvilURL); err != nil {
		cleanup()
		return "", nil, err
	}
	log.Infof("Anvil fork ready at %s (block %d)", anvilURL, targetBlock)
	return anvilURL, cleanup, nil
}

func waitForAnvil(ctx context.Context, anvilURL string) error {
	deadline := time.Now().Add(anvilReadyTimeout)
	for time.Now().Before(deadline) {
		if _, err := singleRPC(ctx, anvilURL, "eth_blockNumber", nil, 1); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(anvilPollInterval):
		}
	}
	return fmt.Errorf("anvil not ready after %s", anvilReadyTimeout)
}

func setupImpersonation(ctx context.Context, anvilURL string, sender common.Address) error {
	if _, err := singleRPC(ctx, anvilURL, "anvil_impersonateAccount",
		[]any{sender.Hex()}, defaultRetries); err != nil {
		return fmt.Errorf("impersonate account: %w", err)
	}
	if _, err := singleRPC(ctx, anvilURL, "anvil_setBalance",
		[]any{sender.Hex(), largeETHBalance}, defaultRetries); err != nil {
		return fmt.Errorf("set balance: %w", err)
	}
	return nil
}

// buildLBTTokenMap builds a lookup map from (originNetwork, originToken) to wrapped address
// using the LBT entries produced by Step 0. Returns an empty map when entries is nil.
func buildLBTTokenMap(entries []LBTEntry) map[tokenOriginKey]common.Address {
	m := make(map[tokenOriginKey]common.Address, len(entries))
	for _, e := range entries {
		if e.WrappedTokenAddress != (common.Address{}) {
			m[tokenOriginKey{e.OriginNetwork, e.OriginTokenAddress}] = e.WrappedTokenAddress
		}
	}
	return m
}

// resolveTokenAddresses returns a map from origin token identity to its L2 ERC-20 address.
// Native tokens (ETH and custom gas token) are omitted — callers use isNativeBridgeExit to
// distinguish them. L2-native tokens map to their own address; external-origin tokens are
// resolved first from lbtMap (Step 0 output) and fall back to getTokenWrappedAddress on the
// bridge contract when not present.
func resolveTokenAddresses(
	ctx context.Context, anvilURL string, bridgeAddr common.Address,
	exits []*agglayertypes.BridgeExit, l2NetworkID uint32,
	gasTokenNetwork uint32, gasTokenAddress common.Address,
	lbtMap map[tokenOriginKey]common.Address,
) (map[tokenOriginKey]common.Address, error) {
	result := make(map[tokenOriginKey]common.Address)

	for _, be := range exits {
		ti := be.TokenInfo
		key := tokenOriginKey{ti.OriginNetwork, ti.OriginTokenAddress}
		if _, ok := result[key]; ok {
			continue // already resolved
		}
		// Skip native tokens — no ERC-20 address to look up.
		if isNativeBridgeExit(ti, gasTokenNetwork, gasTokenAddress) {
			continue
		}
		// L2-native token — its L2 address is the origin address itself.
		if ti.OriginNetwork == l2NetworkID {
			result[key] = ti.OriginTokenAddress
			continue
		}
		// External-origin wrapped token — prefer the LBT map (already accounts for
		// SetSovereignTokenAddress overrides), fall back to the bridge contract.
		if wrapped, ok := lbtMap[key]; ok {
			log.Debugf("token resolved from LBT: origin(network=%d addr=%s) -> %s",
				ti.OriginNetwork, ti.OriginTokenAddress.Hex(), wrapped.Hex())
			result[key] = wrapped
			continue
		}
		wrapped, err := callGetTokenWrappedAddress(ctx, anvilURL, bridgeAddr, ti.OriginNetwork, ti.OriginTokenAddress)
		if err != nil {
			return nil, fmt.Errorf("getTokenWrappedAddress(net=%d addr=%s): %w",
				ti.OriginNetwork, ti.OriginTokenAddress.Hex(), err)
		}
		if wrapped == (common.Address{}) {
			return nil, fmt.Errorf("no wrapped token on L2 for origin network=%d addr=%s",
				ti.OriginNetwork, ti.OriginTokenAddress.Hex())
		}
		log.Debugf("token resolved from contract: origin(network=%d addr=%s) -> %s",
			ti.OriginNetwork, ti.OriginTokenAddress.Hex(), wrapped.Hex())
		result[key] = wrapped
	}
	return result, nil
}

func callGetTokenWrappedAddress(
	ctx context.Context, anvilURL string, bridgeAddr common.Address,
	originNetwork uint32, originTokenAddr common.Address,
) (common.Address, error) {
	callData, err := bridgeABI.Pack("getTokenWrappedAddress", originNetwork, originTokenAddr)
	if err != nil {
		return common.Address{}, fmt.Errorf("pack getTokenWrappedAddress: %w", err)
	}
	raw, err := singleRPC(ctx, anvilURL, "eth_call", []any{
		map[string]any{"to": bridgeAddr.Hex(), "data": "0x" + hex.EncodeToString(callData)},
		"latest",
	}, defaultRetries)
	if err != nil {
		return common.Address{}, err
	}
	var hexStr string
	if err := json.Unmarshal(raw, &hexStr); err != nil {
		return common.Address{}, fmt.Errorf("parse eth_call result: %w", err)
	}
	b, err := hex.DecodeString(strings.TrimPrefix(hexStr, "0x"))
	if err != nil {
		return common.Address{}, fmt.Errorf("decode hex result: %w", err)
	}
	results, err := bridgeABI.Unpack("getTokenWrappedAddress", b)
	if err != nil {
		return common.Address{}, fmt.Errorf("unpack getTokenWrappedAddress: %w", err)
	}
	addr, ok := results[0].(common.Address)
	if !ok {
		return common.Address{}, fmt.Errorf("unexpected return type for getTokenWrappedAddress")
	}
	return addr, nil
}

// erc20NamespacedStorageLocation is the ERC-20 storage namespace for OZ v5 upgradeable tokens.
var erc20NamespacedStorageLocation = common.HexToHash(
	"0x52c63247e1f47db19d5ce0460030c497f067ca4cebf71ba98eeadabe20bace00",
)

// ensureERC20Balance checks the ERC-20 balance of account on tokenAddr.
// If insufficient it patches _balances[account] via hardhat_setStorageAt.
// Tries two storage layouts in order, verifying balanceOf after each patch:
//  1. OZ v4 non-upgradeable: _balances at mapping slot 0
//  2. OZ v5 upgradeable: _balances inside the namespaced ERC20Storage struct
func ensureERC20Balance(
	ctx context.Context, rpcURL string, tokenAddr, account common.Address, required *big.Int,
) error {
	balanceOf := func() (*big.Int, error) {
		callData := make([]byte, abiFuncSelectorSize+abiWordBytes)
		copy(callData, crypto.Keccak256([]byte("balanceOf(address)"))[:abiFuncSelectorSize])
		copy(callData[abiFuncSelectorSize:], common.LeftPadBytes(account.Bytes(), abiWordBytes))
		raw, err := singleRPC(ctx, rpcURL, "eth_call", []any{
			map[string]any{"to": tokenAddr.Hex(), "data": "0x" + hex.EncodeToString(callData)},
			"latest",
		}, defaultRetries)
		if err != nil {
			return nil, fmt.Errorf("balanceOf(%s): %w", account.Hex(), err)
		}
		var hexBal string
		if err := json.Unmarshal(raw, &hexBal); err != nil {
			return nil, fmt.Errorf("parse balanceOf result: %w", err)
		}
		bal, ok := new(big.Int).SetString(strings.TrimPrefix(hexBal, "0x"), hexBase)
		if !ok {
			return nil, fmt.Errorf("invalid balanceOf hex: %s", hexBal)
		}
		return bal, nil
	}

	bal, err := balanceOf()
	if err != nil {
		return err
	}
	if bal.Cmp(required) >= 0 {
		log.Debugf("ERC-20 %s balance of %s is sufficient (%s >= %s)", tokenAddr.Hex(), account.Hex(), bal, required)
		return nil
	}

	log.Infof("ERC-20 %s balance of %s insufficient (%s < %s) — patching via storage slot",
		tokenAddr.Hex(), account.Hex(), bal, required)

	valueHex := "0x" + hex.EncodeToString(common.LeftPadBytes(required.Bytes(), abiWordBytes))

	// erc20BalanceSlot returns keccak256(abi.encode(account, mapSlot)),
	// which is the Solidity storage slot for _balances[account] when _balances
	// is a mapping located at mapSlot.
	erc20BalanceSlot := func(mapSlot common.Hash) string {
		preimage := append(
			common.LeftPadBytes(account.Bytes(), abiWordBytes),
			mapSlot.Bytes()...,
		)
		return "0x" + hex.EncodeToString(crypto.Keccak256(preimage))
	}

	// Try OZ v4 (slot 0) first, then OZ v5 upgradeable (namespaced storage).
	candidates := []string{
		erc20BalanceSlot(common.Hash{}),                  // OZ v4: _balances at slot 0
		erc20BalanceSlot(erc20NamespacedStorageLocation), // OZ v5 upgradeable
	}

	for _, slotHex := range candidates {
		if _, err := singleRPC(ctx, rpcURL, "hardhat_setStorageAt",
			[]any{tokenAddr.Hex(), slotHex, valueHex}, defaultRetries); err != nil {
			return fmt.Errorf("set ERC-20 balance storage slot: %w", err)
		}
		newBal, err := balanceOf()
		if err != nil {
			return err
		}
		if newBal.Cmp(required) >= 0 {
			log.Infof("✅ ERC-20 %s balance of %s patched to %s (slot %s)",
				tokenAddr.Hex(), account.Hex(), required, slotHex)
			return nil
		}
		log.Debugf("slot %s did not update balanceOf — trying next layout", slotHex)
	}

	return fmt.Errorf("could not patch ERC-20 balance for token %s account %s: "+
		"no storage layout matched (tried OZ v4 slot-0 and OZ v5 upgradeable)",
		tokenAddr.Hex(), account.Hex())
}

// encodeERC20ApproveCallRaw ABI-encodes an ERC-20 approve(spender, amount) call.
// Selector: keccak256("approve(address,uint256)")[:4] = 0x095ea7b3
func encodeERC20ApproveCallRaw(spender common.Address, amount *big.Int) []byte {
	if amount == nil {
		amount = new(big.Int)
	}
	selector := crypto.Keccak256([]byte("approve(address,uint256)"))[:4]
	encodedSpender := common.LeftPadBytes(spender.Bytes(), abiWordBytes)
	encodedAmount := common.LeftPadBytes(amount.Bytes(), abiWordBytes)
	return append(selector, append(encodedSpender, encodedAmount...)...)
}

func encodeBridgeAssetCallRaw(
	destNetwork uint32, destAddr common.Address, amount *big.Int, tokenAddr common.Address,
) []byte {
	if amount == nil {
		amount = new(big.Int)
	}
	data, err := bridgeABI.Pack("bridgeAsset", destNetwork, destAddr, amount, tokenAddr, true, []byte{})
	if err != nil {
		// Static types match the ABI; Pack only fails on type mismatches, which cannot happen here.
		panic(fmt.Sprintf("pack bridgeAsset: %v", err))
	}
	return data
}

func sendAnvilTransaction(
	ctx context.Context, anvilURL string,
	from, to common.Address, value *big.Int, data []byte,
) (common.Hash, error) {
	tx := map[string]any{
		"from": from.Hex(),
		"to":   to.Hex(),
		"data": "0x" + hex.EncodeToString(data),
	}
	if value != nil && value.Sign() > 0 {
		tx["value"] = "0x" + value.Text(hexBase)
	}
	result, err := singleRPC(ctx, anvilURL, "eth_sendTransaction", []any{tx}, defaultRetries)
	if err != nil {
		return common.Hash{}, err
	}
	var txHashHex string
	if err := json.Unmarshal(result, &txHashHex); err != nil {
		return common.Hash{}, fmt.Errorf("parse tx hash: %w", err)
	}
	return common.HexToHash(txHashHex), nil
}

func waitForReceipt(ctx context.Context, anvilURL string, txHash common.Hash) ([]rpcLog, error) {
	deadline := time.Now().Add(receiptPollTimeout)
	for time.Now().Before(deadline) {
		result, err := singleRPC(ctx, anvilURL, "eth_getTransactionReceipt",
			[]any{txHash.Hex()}, defaultRetries)
		if err != nil {
			return nil, err
		}
		if len(result) == 0 || string(result) == "null" {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(receiptPollInterval):
				continue
			}
		}
		var receipt struct {
			Status      string   `json:"status"`
			BlockNumber string   `json:"blockNumber"`
			Logs        []rpcLog `json:"logs"`
		}
		if err := json.Unmarshal(result, &receipt); err != nil {
			return nil, fmt.Errorf("parse receipt: %w", err)
		}
		if receipt.Status == "0x0" {
			reason := fetchRevertReason(ctx, anvilURL, txHash, receipt.BlockNumber)
			return nil, fmt.Errorf("transaction %s reverted: %s", txHash.Hex(), reason)
		}
		return receipt.Logs, nil
	}
	return nil, fmt.Errorf("timeout waiting for receipt of %s", txHash.Hex())
}

func parseBridgeEventFromLogs(logs []rpcLog) (*bridgeEventLog, error) {
	wantTopic := bridgeEventTopicHash.Hex()
	for _, l := range logs {
		if len(l.Topics) == 0 || !strings.EqualFold(l.Topics[0], wantTopic) {
			continue
		}
		data, err := hex.DecodeString(strings.TrimPrefix(l.Data, "0x"))
		if err != nil {
			return nil, fmt.Errorf("decode BridgeEvent data: %w", err)
		}
		values, err := bridgeABI.Events["BridgeEvent"].Inputs.UnpackValues(data)
		if err != nil {
			return nil, fmt.Errorf("unpack BridgeEvent: %w", err)
		}
		if len(values) != bridgeEventFields {
			return nil, fmt.Errorf("expected %d BridgeEvent fields, got %d", bridgeEventFields, len(values))
		}
		leafType, ok0 := values[0].(uint8)
		originNetwork, ok1 := values[1].(uint32)
		originAddress, ok2 := values[2].(common.Address)
		destNetwork, ok3 := values[3].(uint32)
		destAddress, ok4 := values[4].(common.Address)
		amount, ok5 := values[5].(*big.Int)
		metadata, ok6 := values[6].([]byte)
		depositCount, ok7 := values[7].(uint32)
		if !ok0 || !ok1 || !ok2 || !ok3 || !ok4 || !ok5 || !ok6 || !ok7 {
			return nil, fmt.Errorf("unexpected field types in BridgeEvent values")
		}
		return &bridgeEventLog{
			LeafType:           leafType,
			OriginNetwork:      originNetwork,
			OriginAddress:      originAddress,
			DestinationNetwork: destNetwork,
			DestinationAddress: destAddress,
			Amount:             amount,
			Metadata:           metadata,
			DepositCount:       depositCount,
		}, nil
	}
	return nil, fmt.Errorf("BridgeEvent not found in receipt logs")
}

// knownErrors maps 4-byte selector (hex, no 0x) to signature and argument decoder.
var knownErrors = map[string]struct {
	sig    string
	decode func(args []byte) string
}{
	// LocalBalanceTreeUnderflow(uint32,address,uint256,uint256)
	"14603c01": {
		sig: "LocalBalanceTreeUnderflow(uint32,address,uint256,uint256)",
		decode: func(args []byte) string {
			if len(args) < fourABIWords {
				return ""
			}
			network := uint32(new(big.Int).SetBytes(args[0:32]).Uint64())
			addr := common.BytesToAddress(args[32:64])
			balance := new(big.Int).SetBytes(args[64:96])
			available := new(big.Int).SetBytes(args[96:128])
			return fmt.Sprintf("network=%d addr=%s balance=%s available=%s",
				network, addr.Hex(), balance, available)
		},
	},
}

// decodeRevertData tries to match the 4-byte selector of hexData against knownErrors
// and returns a human-readable string. Falls back to the raw hex if unknown.
func decodeRevertData(hexData string) string {
	data, err := hex.DecodeString(strings.TrimPrefix(hexData, "0x"))
	if err != nil || len(data) < 4 {
		return hexData
	}
	selector := hex.EncodeToString(data[:4])
	entry, ok := knownErrors[selector]
	if !ok {
		return fmt.Sprintf("unknown selector 0x%s data=%s", selector, hexData)
	}
	decoded := entry.decode(data[4:])
	if decoded == "" {
		return fmt.Sprintf("%s [0x%s] (raw: %s)", entry.sig, selector, hexData)
	}
	return fmt.Sprintf("%s [0x%s]: %s", entry.sig, selector, decoded)
}

// fetchRevertReason replays the failed transaction via eth_call at the block it was
// mined in order to extract the revert reason from the JSON-RPC error message.
func fetchRevertReason(ctx context.Context, anvilURL string, txHash common.Hash, blockNumber string) string {
	raw, err := singleRPC(ctx, anvilURL, "eth_getTransactionByHash", []any{txHash.Hex()}, 1)
	if err != nil {
		return fmt.Sprintf("(could not fetch tx: %v)", err)
	}
	var tx struct {
		From  string `json:"from"`
		To    string `json:"to"`
		Input string `json:"input"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(raw, &tx); err != nil {
		return fmt.Sprintf("(could not parse tx: %v)", err)
	}
	callParams := map[string]any{
		"from": tx.From,
		"to":   tx.To,
		"data": tx.Input,
	}
	if tx.Value != "" && tx.Value != "0x0" && tx.Value != "0x" {
		callParams["value"] = tx.Value
	}
	block := blockNumber
	if block == "" {
		block = "latest"
	}
	_, callErr := singleRPC(ctx, anvilURL, "eth_call", []any{callParams, block}, 1)
	if callErr == nil {
		return "no revert reason available"
	}
	var rpcErr *RPCExecutionError
	if errors.As(callErr, &rpcErr) && rpcErr.Data != "" {
		return decodeRevertData(rpcErr.Data)
	}
	return callErr.Error()
}

// readLocalExitRoot calls getRoot() on the bridge contract to get the LER at blockTag.
func readLocalExitRoot(
	ctx context.Context, rpcURL string, bridgeAddr common.Address, blockTag string,
) (common.Hash, error) {
	callData, err := bridgeABI.Pack("getRoot")
	if err != nil {
		return common.Hash{}, fmt.Errorf("pack getRoot: %w", err)
	}
	raw, err := singleRPC(ctx, rpcURL, "eth_call", []any{
		map[string]any{
			"to":   bridgeAddr.Hex(),
			"data": "0x" + hex.EncodeToString(callData),
		},
		blockTag,
	}, defaultRetries)
	if err != nil {
		return common.Hash{}, err
	}
	var hexStr string
	if err := json.Unmarshal(raw, &hexStr); err != nil {
		return common.Hash{}, fmt.Errorf("parse getRoot result: %w", err)
	}
	b, err := hex.DecodeString(strings.TrimPrefix(hexStr, "0x"))
	if err != nil {
		return common.Hash{}, fmt.Errorf("decode getRoot hex: %w", err)
	}
	results, err := bridgeABI.Unpack("getRoot", b)
	if err != nil {
		return common.Hash{}, fmt.Errorf("unpack getRoot: %w", err)
	}
	hash, ok := results[0].([32]byte)
	if !ok {
		return common.Hash{}, fmt.Errorf("unexpected return type for getRoot")
	}
	return common.Hash(hash), nil
}
