package exit_certificate

import (
	"context"
	"encoding/hex"
	"encoding/json"
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

	// impersonatedSender is Anvil's first default funded account.
	impersonatedSender = "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"
	// largeETHBalance is MaxUint256 in hex, enough for any bridgeAsset call regardless of exit amounts.
	largeETHBalance = "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"

	// OZ ERC-20 storage layout used by the bridge's wrapped tokens.
	erc20BalanceSlot   = 0
	erc20AllowanceSlot = 1
)

var (
	// bridgeABI is the parsed ABI for the AgglayerBridgeL2 contract, used to
	// encode/decode bridgeAsset, getRoot, and getTokenWrappedAddress calls.
	bridgeABI abi.ABI

	// EIP-1967 proxy sentinel slots — never touch these.
	eip1967AdminSlot = "0xb53127684a568b3173ae13b9f8a6016e243e63b6e8ee1178d6a717850b5d6103"
	eip1967ImplSlot  = "0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc"

	// lbtSlotThreshold distinguishes computed mapping slots (> 2^200) from fixed slots (< 1000).
	lbtSlotThreshold = new(big.Int).Lsh(big.NewInt(1), 200)

	// maxUint256Hex is the value written to LBT slots so bridgeAsset never underflows.
	maxUint256Hex = "0x" + hex.EncodeToString(
		common.LeftPadBytes(
			new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1)).Bytes(),
			32,
		),
	)
)

func init() {
	parsed, err := agglayerbridgel2.Agglayerbridgel2MetaData.GetAbi()
	if err != nil {
		panic(fmt.Sprintf("parse agglayerbridgel2 ABI: %v", err))
	}
	bridgeABI = *parsed
}

// jsLBTTracer is a JavaScript tracer that collects SLOAD slot values.
const jsLBTTracer = `{
	sloads:[],
	step:function(log){
		if(log.op.toString()==='SLOAD'){
			var s=log.stack.peek(0).toString(16);
			while(s.length<64)s='0'+s;
			this.sloads.push('0x'+s);
		}
	},
	fault:function(){},
	result:function(){return this.sloads;}
}`

// resolvedToken holds the L2 token address for a bridge exit.
type resolvedToken struct {
	addr     common.Address
	isNative bool // true for ETH — the tx carries the amount as msg.value
}

// RunStepG computes Certificate.NewLocalExitRoot by replaying all bridge exits
// against an Anvil shadow-fork of the L2 chain at cfg.ResolvedTargetBlock.
func RunStepG(ctx context.Context, cfg *Config, certificate *agglayertypes.Certificate) (*StepGResult, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP G - Calculate NewLocalExitRoot")
	log.Info("═══════════════════════════════════════════")

	if certificate == nil {
		return nil, fmt.Errorf("certificate is nil")
	}

	if len(certificate.BridgeExits) == 0 {
		log.Info("No bridge exits — using EmptyLER")
		return &StepGResult{NewLocalExitRoot: bridgesynctypes.EmptyLER, BridgeExitCount: 0}, nil
	}

	if err := checkAnvilAvailable(); err != nil {
		return nil, err
	}

	anvilURL, cleanup, err := startAnvil(ctx, cfg.L2RPCURL, cfg.ResolvedTargetBlock)
	if err != nil {
		return nil, fmt.Errorf("start anvil: %w", err)
	}
	defer cleanup()

	sender := common.HexToAddress(impersonatedSender)
	if err := setupImpersonation(ctx, anvilURL, sender); err != nil {
		return nil, fmt.Errorf("setup impersonation: %w", err)
	}

	blockTag := toBlockTag(cfg.ResolvedTargetBlock)
	gasTokenNetwork, gasTokenAddress, err := fetchGasTokenInfo(ctx, cfg.L2RPCURL, cfg.L2BridgeAddress, blockTag)
	if err != nil {
		log.Warnf("Failed to fetch gas token info (assuming standard ETH): %v", err)
		gasTokenNetwork = 0
		gasTokenAddress = common.Address{}
	}

	tokens, err := resolveTokenAddresses(ctx, anvilURL, cfg.L2BridgeAddress, certificate.BridgeExits, cfg.L2NetworkID, gasTokenNetwork, gasTokenAddress)
	if err != nil {
		return nil, fmt.Errorf("resolve token addresses: %w", err)
	}

	if err := setupLBTSlots(ctx, cfg.L2RPCURL, cfg.ResolvedTargetBlock, anvilURL,
		cfg.L2BridgeAddress, sender, certificate.BridgeExits, tokens); err != nil {
		return nil, fmt.Errorf("setup LBT slots: %w", err)
	}

	if err := setupERC20Balances(ctx, anvilURL, cfg.L2BridgeAddress, sender, certificate.BridgeExits, tokens); err != nil {
		return nil, fmt.Errorf("setup ERC-20 balances: %w", err)
	}

	for i, be := range certificate.BridgeExits {
		if err := replayBridgeExit(ctx, anvilURL, cfg.L2BridgeAddress, sender, be, tokens[i]); err != nil {
			return nil, fmt.Errorf("replay bridge exit %d: %w", i, err)
		}
	}

	ler, err := readLocalExitRoot(ctx, anvilURL, cfg.L2BridgeAddress)
	if err != nil {
		return nil, fmt.Errorf("read local exit root: %w", err)
	}

	result := &StepGResult{
		NewLocalExitRoot: ler,
		BridgeExitCount:  uint64(len(certificate.BridgeExits)),
	}
	log.Infof("Bridge exits processed: %d", result.BridgeExitCount)
	log.Infof("NewLocalExitRoot: %s", result.NewLocalExitRoot.Hex())
	log.Info("STEP G complete")
	return result, nil
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
	return ln.Addr().(*net.TCPAddr).Port, nil
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

// resolveTokenAddresses returns the L2 token address for each bridge exit.
// Results are in the same order as exits. Wrapped-token lookups are cached.
// gasTokenNetwork and gasTokenAddress identify the chain's custom gas token (both
// zero for standard ETH chains); exits that match are treated as native.
func resolveTokenAddresses(
	ctx context.Context, anvilURL string, bridgeAddr common.Address,
	exits []*agglayertypes.BridgeExit, l2NetworkID uint32,
	gasTokenNetwork uint32, gasTokenAddress common.Address,
) ([]resolvedToken, error) {
	type cacheKey struct {
		network uint32
		addr    common.Address
	}
	cache := make(map[cacheKey]common.Address)
	result := make([]resolvedToken, len(exits))

	for i, be := range exits {
		ti := be.TokenInfo
		// Native ETH
		if ti.OriginNetwork == 0 && ti.OriginTokenAddress == (common.Address{}) {
			result[i] = resolvedToken{isNative: true}
			continue
		}
		// Custom gas token — bridgeAsset expects token=address(0) for native
		if gasTokenAddress != (common.Address{}) &&
			ti.OriginNetwork == gasTokenNetwork && ti.OriginTokenAddress == gasTokenAddress {
			result[i] = resolvedToken{isNative: true}
			continue
		}
		// L2-native token — use origin address directly
		if ti.OriginNetwork == l2NetworkID {
			result[i] = resolvedToken{addr: ti.OriginTokenAddress}
			continue
		}
		// External-origin wrapped token — query bridge for its L2 address
		key := cacheKey{ti.OriginNetwork, ti.OriginTokenAddress}
		if wrapped, ok := cache[key]; ok {
			result[i] = resolvedToken{addr: wrapped}
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
		cache[key] = wrapped
		result[i] = resolvedToken{addr: wrapped}
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

// setupERC20Balances sets token balances and bridge allowances for all ERC-20
// exits via hardhat_setStorageAt on the OZ storage layout (slot 0 / slot 1).
func setupERC20Balances(
	ctx context.Context, anvilURL string, bridgeAddr, sender common.Address,
	exits []*agglayertypes.BridgeExit, tokens []resolvedToken,
) error {
	totals := make(map[common.Address]*big.Int)
	for i, be := range exits {
		rt := tokens[i]
		if rt.isNative {
			continue
		}
		if _, ok := totals[rt.addr]; !ok {
			totals[rt.addr] = new(big.Int)
		}
		if be.Amount != nil {
			totals[rt.addr].Add(totals[rt.addr], be.Amount)
		}
	}
	for tokenAddr, total := range totals {
		if err := setStorageSlot(ctx, anvilURL, tokenAddr, erc20BalanceStorageKey(sender), total); err != nil {
			return fmt.Errorf("set balance for token %s: %w", tokenAddr.Hex(), err)
		}
		if err := setStorageSlot(ctx, anvilURL, tokenAddr, erc20AllowanceStorageKey(sender, bridgeAddr), total); err != nil {
			return fmt.Errorf("set allowance for token %s: %w", tokenAddr.Hex(), err)
		}
	}
	return nil
}

// erc20BalanceStorageKey returns the OZ slot-0 balance mapping key for account.
// slot = keccak256(abi.encode(account, uint256(0)))
func erc20BalanceStorageKey(account common.Address) string {
	slot := crypto.Keccak256Hash(
		common.LeftPadBytes(account.Bytes(), 32),
		common.LeftPadBytes([]byte{}, 32), // slot 0 = 32 zero bytes
	)
	return "0x" + hex.EncodeToString(slot.Bytes())
}

// erc20AllowanceStorageKey returns the OZ slot-1 allowance mapping key for owner→spender.
// innerSlot = keccak256(abi.encode(owner, uint256(1)))
// slot      = keccak256(abi.encode(spender, innerSlot))
func erc20AllowanceStorageKey(owner, spender common.Address) string {
	inner := crypto.Keccak256Hash(
		common.LeftPadBytes(owner.Bytes(), 32),
		common.LeftPadBytes(big.NewInt(erc20AllowanceSlot).Bytes(), 32),
	)
	slot := crypto.Keccak256Hash(
		common.LeftPadBytes(spender.Bytes(), 32),
		inner.Bytes(),
	)
	return "0x" + hex.EncodeToString(slot.Bytes())
}

func setStorageSlot(ctx context.Context, anvilURL string, contractAddr common.Address, slot string, value *big.Int) error {
	valueHex := "0x" + hex.EncodeToString(common.LeftPadBytes(value.Bytes(), 32))
	_, err := singleRPC(ctx, anvilURL, "hardhat_setStorageAt",
		[]any{contractAddr.Hex(), slot, valueHex}, defaultRetries)
	return err
}

// probeLBTSlots traces a minimal bridgeAsset call (amount=1) on the real L2 RPC
// and returns the storage slots that look like LBT mapping entries: keccak256-style
// slots > 2^200, excluding known EIP-1967 proxy sentinels.
func probeLBTSlots(
	ctx context.Context,
	l2RPCURL string, targetBlock uint64,
	bridgeAddr, sender common.Address,
	be *agglayertypes.BridgeExit, rt resolvedToken,
) ([]string, error) {
	callData := encodeBridgeAssetCallRaw(
		be.DestinationNetwork, be.DestinationAddress,
		big.NewInt(1), rt.addr,
	)
	tx := map[string]any{
		"from": sender.Hex(),
		"to":   bridgeAddr.Hex(),
		"data": "0x" + hex.EncodeToString(callData),
	}
	if rt.isNative {
		tx["value"] = "0x1"
	}

	blockHex := fmt.Sprintf("0x%x", targetBlock)
	result, err := singleRPC(ctx, l2RPCURL, "debug_traceCall", []any{
		tx, blockHex, map[string]any{"tracer": jsLBTTracer},
	}, defaultRetries)
	if err != nil {
		return nil, err
	}

	var slots []string
	if err := json.Unmarshal(result, &slots); err != nil {
		return nil, fmt.Errorf("parse SLOAD slots: %w", err)
	}

	seen := make(map[string]bool)
	var candidates []string
	for _, slot := range slots {
		if seen[slot] {
			continue
		}
		seen[slot] = true
		if slot == eip1967AdminSlot || slot == eip1967ImplSlot {
			continue
		}
		slotBig, ok := new(big.Int).SetString(strings.TrimPrefix(slot, "0x"), 16)
		if !ok || slotBig.Cmp(lbtSlotThreshold) <= 0 {
			continue
		}
		candidates = append(candidates, slot)
	}
	return candidates, nil
}

// setupLBTSlots discovers the on-chain LBT storage slots for each unique token in
// the exit list (via debug_traceCall on the real L2 RPC) and sets them to MaxUint256
// on the Anvil fork so that bridgeAsset calls never revert with LocalBalanceTreeUnderflow.
func setupLBTSlots(
	ctx context.Context,
	l2RPCURL string, targetBlock uint64,
	anvilURL string, bridgeAddr, sender common.Address,
	exits []*agglayertypes.BridgeExit, tokens []resolvedToken,
) error {
	type tokenKey struct {
		network uint32
		addr    common.Address
		native  bool
	}
	seen := make(map[tokenKey]bool)

	for i, be := range exits {
		rt := tokens[i]
		key := tokenKey{be.TokenInfo.OriginNetwork, rt.addr, rt.isNative}
		if seen[key] {
			continue
		}
		seen[key] = true

		slots, err := probeLBTSlots(ctx, l2RPCURL, targetBlock, bridgeAddr, sender, be, rt)
		if err != nil {
			log.Warnf("probe LBT slots for bridge exit %d: %v (continuing)", i, err)
			continue
		}
		for _, slot := range slots {
			log.Infof("Unlocking LBT slot %s (origin network=%d addr=%s)",
				slot, be.TokenInfo.OriginNetwork, rt.addr.Hex())
			if _, err := singleRPC(ctx, anvilURL, "hardhat_setStorageAt",
				[]any{bridgeAddr.Hex(), slot, maxUint256Hex}, defaultRetries); err != nil {
				return fmt.Errorf("set LBT slot %s: %w", slot, err)
			}
		}
	}
	return nil
}

func replayBridgeExit(
	ctx context.Context, anvilURL string, bridgeAddr, sender common.Address,
	be *agglayertypes.BridgeExit, rt resolvedToken,
) error {
	callData := encodeBridgeAssetCall(be, rt.addr)
	var value *big.Int
	if rt.isNative && be.Amount != nil {
		value = be.Amount
	}
	txHash, err := sendAnvilTransaction(ctx, anvilURL, sender, bridgeAddr, value, callData)
	if err != nil {
		return err
	}
	return waitForReceipt(ctx, anvilURL, txHash)
}

func encodeBridgeAssetCallRaw(destNetwork uint32, destAddr common.Address, amount *big.Int, tokenAddr common.Address) []byte {
	if amount == nil {
		amount = new(big.Int)
	}
	data, err := bridgeABI.Pack("bridgeAsset", destNetwork, destAddr, amount, tokenAddr, false, []byte{})
	if err != nil {
		// Static types match the ABI; Pack only fails on type mismatches, which cannot happen here.
		panic(fmt.Sprintf("pack bridgeAsset: %v", err))
	}
	return data
}

func encodeBridgeAssetCall(be *agglayertypes.BridgeExit, tokenAddr common.Address) []byte {
	amount := be.Amount
	if amount == nil {
		amount = new(big.Int)
	}
	return encodeBridgeAssetCallRaw(be.DestinationNetwork, be.DestinationAddress, amount, tokenAddr)
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
		tx["value"] = "0x" + value.Text(16)
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

func waitForReceipt(ctx context.Context, anvilURL string, txHash common.Hash) error {
	deadline := time.Now().Add(receiptPollTimeout)
	for time.Now().Before(deadline) {
		result, err := singleRPC(ctx, anvilURL, "eth_getTransactionReceipt",
			[]any{txHash.Hex()}, defaultRetries)
		if err != nil {
			return err
		}
		if len(result) == 0 || string(result) == "null" {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(receiptPollInterval):
				continue
			}
		}
		var receipt struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(result, &receipt); err != nil {
			return fmt.Errorf("parse receipt: %w", err)
		}
		if receipt.Status == "0x0" {
			return fmt.Errorf("transaction %s reverted", txHash.Hex())
		}
		return nil
	}
	return fmt.Errorf("timeout waiting for receipt of %s", txHash.Hex())
}

// readLocalExitRoot calls getRoot() on the bridge contract to get the current LER.
func readLocalExitRoot(ctx context.Context, anvilURL string, bridgeAddr common.Address) (common.Hash, error) {
	callData, err := bridgeABI.Pack("getRoot")
	if err != nil {
		return common.Hash{}, fmt.Errorf("pack getRoot: %w", err)
	}
	raw, err := singleRPC(ctx, anvilURL, "eth_call", []any{
		map[string]any{
			"to":   bridgeAddr.Hex(),
			"data": "0x" + hex.EncodeToString(callData),
		},
		"latest",
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
