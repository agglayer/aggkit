#!/usr/bin/env bash
# reproduce_sc_locked.sh — reproduce the SC-locked ERC-20 exit error in Step G
#
# Issue: ensureERC20Balance() returns an error for SC-locked ERC-20 exits instead of
# patching the Anvil storage slot. This script sets up the scenario and runs the tool.
#
# Steps:
#   1. Deploy a test ERC-20 on L1 (TestToken with 1000 TTK)
#   2. Bridge some TTK from L1 to L2, wait for claim (wrapped wTTK minted to recipient)
#   3. Deploy a dummy contract on L2 (SC holder — any address with code)
#   4. Transfer half the wTTK from the EOA to the SC holder
#   5. Generate the exit-certificate config and run the full pipeline
#   6. Step G will fail with: "ERC-20 balance insufficient token: ... account: exitAddress"
#
# Requirements: kurtosis, cast, forge (Foundry), go, anvil
set -euo pipefail

GREEN='\033[0;32m'
RED='\033[0;31m'
ORANGE='\033[0;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info()    { echo -e "${GREEN}[INFO]${NC} $*" >&2; }
log_warn()    { echo -e "${ORANGE}[WARN]${NC} $*" >&2; }
log_error()   { echo -e "${RED}[ERROR]${NC} $*" >&2; }
log_section() { echo -e "\n${BLUE}══ $* ══${NC}" >&2; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOOL_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
PROJECT_ROOT="$(cd "$TOOL_DIR/../../.." && pwd)"

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------

KURTOSIS_ENCLAVE="${KURTOSIS_ENCLAVE:-aggkit}"
L1_SERVICE="${L1_SERVICE:-el-1-geth-lighthouse}"
L2_SERVICE="${L2_SERVICE:-op-el-1-op-geth-op-node-001}"
AGGLAYER_SERVICE="${AGGLAYER_SERVICE:-agglayer}"
NETWORK_INDEX="${NETWORK_INDEX:-1}"

# Kurtosis L1 faucet key (1_000_000_000 ETH on L1 in local enclaves)
PRIVATE_KEY="${PRIVATE_KEY:-0x04b9f63ecf84210c5366c66d68fa1f5da1fa4f634fad6dfc86178e4d79ff9e59}"

# exitAddress: receives SC-locked value in the certificate — must NOT hold wTTK
EXIT_ADDRESS="${EXIT_ADDRESS:-0x000000000000000000000000000000000000dEaD}"

TOKEN_TOTAL_SUPPLY="1000000000000000000000"  # 1000 TTK (18 decimals)
BRIDGE_AMOUNT="600000000000000000000"        # 600 TTK bridged to L2
SC_LOCK_AMOUNT="400000000000000000000"       # 400 TTK transferred to SC (SC-locked)

OUTPUT_DIR="${OUTPUT_DIR:-/tmp/sc-locked-reproduce}"

CLAIM_TIMEOUT="${CLAIM_TIMEOUT:-300}"  # seconds to wait for L2 auto-claim

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

check_deps() {
    local missing=()
    command -v kurtosis &>/dev/null || missing+=("kurtosis")
    command -v cast     &>/dev/null || missing+=("cast")
    command -v forge    &>/dev/null || missing+=("forge")
    command -v anvil    &>/dev/null || missing+=("anvil")
    command -v go       &>/dev/null || missing+=("go")
    if [[ ${#missing[@]} -gt 0 ]]; then
        log_error "Missing required tools: ${missing[*]}"
        log_error "Install Foundry: https://getfoundry.sh"
        exit 1
    fi
}

port_to_localhost_url() {
    local raw_url="$1"
    local port
    port=$(echo "$raw_url" | sed -E 's|^[a-zA-Z]+://||' | cut -f2 -d':')
    echo "http://localhost:${port}"
}

get_rpc_url() {
    local service="$1" port_name="$2"
    local raw
    raw=$(kurtosis port print "$KURTOSIS_ENCLAVE" "$service" "$port_name" 2>/dev/null) \
        || { log_error "Cannot get port '$port_name' from '$service'"; exit 1; }
    port_to_localhost_url "$raw"
}

wait_for_claim() {
    local l2_rpc="$1" l2_bridge="$2" deposit_count="$3"
    local timeout_secs="${CLAIM_TIMEOUT}" poll_secs=5 elapsed=0

    local leaf_idx_hex src_net_hex
    leaf_idx_hex=$(printf '%064x' "$deposit_count")
    src_net_hex=$(printf '%064x' 0)
    local calldata="0xcc461632${leaf_idx_hex}${src_net_hex}"

    log_info "Waiting for auto-claim on L2 (depositCount=$deposit_count)..."
    while [[ $elapsed -lt $timeout_secs ]]; do
        local result val
        result=$(cast call --rpc-url "$l2_rpc" "$l2_bridge" "$calldata" 2>/dev/null || echo "0x")
        val=$(cast to-dec "${result:-0x0}" 2>/dev/null || echo "0")
        if [[ "$val" != "0" ]]; then
            log_info "Deposit claimed on L2 after ${elapsed}s"
            return 0
        fi
        sleep "$poll_secs"
        elapsed=$((elapsed + poll_secs))
        log_info "  Waiting for claim... ${elapsed}s / ${timeout_secs}s"
    done
    log_error "Timed out waiting for claim after ${timeout_secs}s"
    exit 1
}

extract_deposit_count() {
    local tx_hash="$1" l1_rpc="$2"
    local bridge_event_topic="0x501781209a1f8899323b96b4ef08b168df93e0a90c673d1e4cce39366cb62f9b"
    local receipt data
    receipt=$(cast receipt --rpc-url "$l1_rpc" "$tx_hash" --json 2>/dev/null || echo "{}")
    data=$(echo "$receipt" | python3 -c "
import sys, json
receipt = json.load(sys.stdin)
topic = '$bridge_event_topic'
for log in receipt.get('logs', []):
    if log.get('topics', [None])[0] == topic:
        print(log.get('data', ''))
        break
" 2>/dev/null || true)
    [[ -z "$data" ]] && { log_warn "BridgeEvent log not found"; echo ""; return; }
    # depositCount is the 8th 32-byte word (7*64=448 hex chars offset)
    local hex_data="${data#0x}"
    local deposit_count_hex="${hex_data:448:64}"
    python3 -c "print(int('$deposit_count_hex', 16))" 2>/dev/null || echo ""
}

# ---------------------------------------------------------------------------
# Step 1: Deploy test ERC-20 on L1
# ---------------------------------------------------------------------------

deploy_test_erc20() {
    local l1_rpc="$1"
    log_section "Step 1: Deploy test ERC-20 on L1"

    # Write minimal ERC-20 Solidity source
    local sol_dir="$OUTPUT_DIR/contracts"
    mkdir -p "$sol_dir"
    cat > "$sol_dir/TestToken.sol" <<'EOF'
// SPDX-License-Identifier: MIT
pragma solidity ^0.8.0;
contract TestToken {
    string  public name     = "TestToken";
    string  public symbol   = "TTK";
    uint8   public decimals = 18;
    uint256 public totalSupply;
    mapping(address => uint256) public balanceOf;
    mapping(address => mapping(address => uint256)) public allowance;
    event Transfer(address indexed from, address indexed to,              uint256 value);
    event Approval(address indexed owner, address indexed spender, uint256 value);
    constructor(uint256 _supply) {
        totalSupply          = _supply;
        balanceOf[msg.sender] = _supply;
        emit Transfer(address(0), msg.sender, _supply);
    }
    function transfer(address to, uint256 amount) external returns (bool) {
        balanceOf[msg.sender] -= amount;
        balanceOf[to]          += amount;
        emit Transfer(msg.sender, to, amount);
        return true;
    }
    function approve(address spender, uint256 amount) external returns (bool) {
        allowance[msg.sender][spender] = amount;
        emit Approval(msg.sender, spender, amount);
        return true;
    }
    function transferFrom(address from, address to, uint256 amount) external returns (bool) {
        allowance[from][msg.sender] -= amount;
        balanceOf[from]              -= amount;
        balanceOf[to]                += amount;
        emit Transfer(from, to, amount);
        return true;
    }
}
EOF

    log_info "Deploying TestToken on L1 (supply: $TOKEN_TOTAL_SUPPLY)..."
    local out
    out=$(forge create \
        --rpc-url "$l1_rpc" \
        --private-key "$PRIVATE_KEY" \
        --broadcast \
        "$sol_dir/TestToken.sol:TestToken" \
        --constructor-args "$TOKEN_TOTAL_SUPPLY" \
        2>&1)
    echo "$out" >&2

    local l1_token_addr
    l1_token_addr=$(echo "$out" | grep "Deployed to:" | awk '{print $3}')
    if [[ -z "$l1_token_addr" ]]; then
        log_error "Failed to extract deployed address from forge output"
        exit 1
    fi
    log_info "TestToken deployed at: $l1_token_addr"
    echo "$l1_token_addr"
}

# ---------------------------------------------------------------------------
# Step 2: Bridge TTK from L1 to L2
# ---------------------------------------------------------------------------

bridge_erc20_to_l2() {
    local l1_rpc="$1" l2_rpc="$2" l1_bridge="$3" l1_token="$4" recipient="$5"
    log_section "Step 2: Bridge $BRIDGE_AMOUNT TTK from L1 to L2"

    log_info "Approving L1 bridge to spend $BRIDGE_AMOUNT TTK..."
    cast send \
        --rpc-url "$l1_rpc" \
        --private-key "$PRIVATE_KEY" \
        "$l1_token" \
        "approve(address,uint256)" \
        "$l1_bridge" \
        "$BRIDGE_AMOUNT" >/dev/null
    log_info "Approval done."

    log_info "Calling bridgeAsset on L1 bridge..."
    local tx_json
    tx_json=$(cast send \
        --rpc-url "$l1_rpc" \
        --private-key "$PRIVATE_KEY" \
        --json \
        "$l1_bridge" \
        "bridgeAsset(uint32,address,uint256,address,bool,bytes)" \
        "$NETWORK_INDEX" \
        "$recipient" \
        "$BRIDGE_AMOUNT" \
        "$l1_token" \
        true \
        "0x")
    local tx_hash
    tx_hash=$(echo "$tx_json" | python3 -c "import sys,json; print(json.load(sys.stdin)['transactionHash'])")
    log_info "Bridge tx: $tx_hash"

    local deposit_count
    deposit_count=$(extract_deposit_count "$tx_hash" "$l1_rpc")
    if [[ -z "$deposit_count" ]]; then
        log_error "Could not extract depositCount from bridge tx — cannot wait for claim"
        exit 1
    fi
    log_info "depositCount: $deposit_count"

    wait_for_claim "$l2_rpc" "$l1_bridge" "$deposit_count"
    echo "$deposit_count"
}

# ---------------------------------------------------------------------------
# Step 3: Find wrapped token address on L2
# ---------------------------------------------------------------------------

find_wrapped_token_on_l2() {
    local l2_rpc="$1" l2_bridge="$2" l1_network_id="$3" l1_token="$4"
    log_section "Step 3: Find wrapped token address on L2"

    # getTokenWrappedAddress(uint32 originNetwork, address originTokenAddress) returns (address)
    local calldata
    calldata=$(cast calldata "getTokenWrappedAddress(uint32,address)" "$l1_network_id" "$l1_token")
    local result
    result=$(cast call --rpc-url "$l2_rpc" "$l2_bridge" "$calldata")
    local wrapped
    wrapped=$(cast parse-bytes32-address "$result" 2>/dev/null || echo "")
    if [[ -z "$wrapped" ]] || [[ "$wrapped" == "0x0000000000000000000000000000000000000000" ]]; then
        # fallback: decode as address
        wrapped=$(cast abi-decode "f()(address)" "$result" 2>/dev/null | head -1 || echo "")
    fi
    if [[ -z "$wrapped" ]] || [[ "$wrapped" == "0x0000000000000000000000000000000000000000" ]]; then
        log_error "Wrapped token not found on L2 for L1 token $l1_token (network $l1_network_id)"
        exit 1
    fi
    log_info "Wrapped token on L2: $wrapped"
    echo "$wrapped"
}

# ---------------------------------------------------------------------------
# Step 4: Deploy dummy SC holder on L2 and transfer tokens
# ---------------------------------------------------------------------------

create_sc_locked_tokens() {
    local l2_rpc="$1" l2_bridge="$2" wrapped_token="$3" sender="$4"
    log_section "Step 4: Create SC-locked tokens on L2"

    # Fund sender on L2 first (bridge ETH for gas)
    log_info "Checking L2 ETH balance for gas..."
    local l2_bal
    l2_bal=$(cast balance --rpc-url "$l2_rpc" "$sender")
    if python3 -c "import sys; sys.exit(0 if int('$l2_bal') < 10**15 else 1)" 2>/dev/null; then
        log_info "L2 balance low ($l2_bal), bridging ETH for gas..."
        local gas_amount="100000000000000000"  # 0.1 ETH
        cast send \
            --rpc-url "$(get_rpc_url "$L1_SERVICE" rpc)" \
            --private-key "$PRIVATE_KEY" \
            --value "$gas_amount" \
            "$l2_bridge" \
            "bridgeAsset(uint32,address,uint256,address,bool,bytes)" \
            "$NETWORK_INDEX" \
            "$sender" \
            "$gas_amount" \
            "0x0000000000000000000000000000000000000000" \
            true \
            "0x" >/dev/null
        log_info "ETH bridge submitted. Waiting for L2 balance..."
        local elapsed=0
        while [[ $elapsed -lt 120 ]]; do
            l2_bal=$(cast balance --rpc-url "$l2_rpc" "$sender")
            python3 -c "import sys; sys.exit(1 if int('$l2_bal') < 10**15 else 0)" 2>/dev/null && break
            sleep 5; elapsed=$((elapsed + 5))
        done
        log_info "L2 ETH balance: $(cast from-wei "$l2_bal" ether) ETH"
    else
        log_info "L2 ETH balance: $(cast from-wei "$l2_bal" ether) ETH (sufficient)"
    fi

    # Deploy a Solidity holder contract (must have runtime bytecode so eth_getCode != 0x)
    log_info "Deploying SC holder on L2 (Solidity contract with runtime code)..."
    local sol_dir="$OUTPUT_DIR/contracts"
    mkdir -p "$sol_dir"
    cat > "$sol_dir/TokenHolder.sol" <<'EOF'
// SPDX-License-Identifier: MIT
pragma solidity ^0.8.0;
// A non-EOA address that holds ERC-20 tokens.
// Empty Solidity contracts still have non-empty runtime bytecode (compiler metadata hash).
contract TokenHolder {}
EOF
    local holder_out
    holder_out=$(forge create \
        --rpc-url "$l2_rpc" \
        --private-key "$PRIVATE_KEY" \
        --broadcast \
        "$sol_dir/TokenHolder.sol:TokenHolder" 2>&1)
    echo "$holder_out" >&2
    local holder_addr
    holder_addr=$(echo "$holder_out" | grep "Deployed to:" | awk '{print $3}')
    if [[ -z "$holder_addr" ]]; then
        log_error "Failed to deploy holder contract"
        exit 1
    fi
    # Verify it has code (essential — otherwise Step B classifies it as EOA)
    local holder_code
    holder_code=$(cast code --rpc-url "$l2_rpc" "$holder_addr" 2>/dev/null || echo "0x")
    if [[ "$holder_code" == "0x" ]]; then
        log_error "Holder contract at $holder_addr has no code — Step B will treat it as EOA"
        exit 1
    fi
    log_info "SC holder deployed at: $holder_addr (code length: ${#holder_code} bytes hex)"

    # Check wTTK balance on L2 (use raw hex → decimal to avoid cast annotation like "[2e20]")
    balanceof_hex() { cast call --rpc-url "$l2_rpc" "$wrapped_token" "balanceOf(address)" "$1" 2>/dev/null; }
    local sender_bal_hex
    sender_bal_hex=$(balanceof_hex "$sender")
    local sender_wttk
    sender_wttk=$(cast to-dec "$sender_bal_hex")
    log_info "wTTK balance of sender: $sender_wttk"

    # Determine actual transfer amount: min(SC_LOCK_AMOUNT, sender_balance)
    local transfer_amount
    transfer_amount=$(python3 -c "print(min(int('$SC_LOCK_AMOUNT'), int('$sender_wttk')))")
    log_info "Transferring $transfer_amount wTTK to SC holder $holder_addr..."
    cast send \
        --rpc-url "$l2_rpc" \
        --private-key "$PRIVATE_KEY" \
        "$wrapped_token" \
        "transfer(address,uint256)" \
        "$holder_addr" \
        "$transfer_amount" >/dev/null
    log_info "Transfer done."

    local eoa_bal sc_bal
    eoa_bal=$(cast to-dec "$(balanceof_hex "$sender")")
    sc_bal=$(cast to-dec "$(balanceof_hex "$holder_addr")")
    log_info "wTTK balance — EOA: $eoa_bal  |  SC holder: $sc_bal"
    log_info "SC-locked amount: $sc_bal (will trigger ensureERC20Balance error in Step G)"

    echo "$holder_addr"
}

# ---------------------------------------------------------------------------
# Step 5: Build the exit-certificate tool
# ---------------------------------------------------------------------------

build_tool() {
    log_section "Step 5: Build exit-certificate tool"
    pushd "$TOOL_DIR" >/dev/null
    go build -o "$OUTPUT_DIR/exit-certificate" ./cmd
    popd >/dev/null
    log_info "Binary: $OUTPUT_DIR/exit-certificate"
}

# ---------------------------------------------------------------------------
# Step 6: Generate config and run pipeline
# ---------------------------------------------------------------------------

run_pipeline() {
    local l1_rpc="$1" l2_rpc="$2" l2_bridge="$3" l1_network_id="$4" target_block="$5" l1_token="$6"
    log_section "Step 6: Run exit-certificate pipeline"

    local agglayer_grpc sovereign_rollup l1_ger_addr
    agglayer_grpc=$(get_rpc_url_grpc "$AGGLAYER_SERVICE")

    local config_tmp
    config_tmp=$(mktemp -d)
    trap "rm -rf '$config_tmp'" RETURN
    kurtosis files download "$KURTOSIS_ENCLAVE" "aggkit-bridge-config-001" "$config_tmp" &>/dev/null || true
    sovereign_rollup=$(grep -E '^\s*SovereignRollupAddr\s*=' "$config_tmp/config.toml" 2>/dev/null \
        | head -1 | tr -d '[:space:]' | cut -f2 -d'=' | tr -d '"' || echo "")
    l1_ger_addr=$(grep -E '^\s*polygonZkEVMGlobalExitRootAddress\s*=' "$config_tmp/config.toml" 2>/dev/null \
        | head -1 | tr -d '[:space:]' | cut -f2 -d'=' | tr -d '"' || echo "")

    local config_file="$OUTPUT_DIR/parameters.json"
    cat > "$config_file" <<EOF
{
    "l2RpcUrl":       "$l2_rpc",
    "l1RpcUrl":       "$l1_rpc",
    "l2BridgeAddress":"$l2_bridge",
    "l1BridgeAddress":"$l2_bridge",
    "l2NetworkId":    $NETWORK_INDEX,
    "targetBlock":    "$target_block",
    "exitAddress":    "$EXIT_ADDRESS",
    "destinationNetwork": 0,
    "sovereignRollupAddr":    "${sovereign_rollup:-0x0000000000000000000000000000000000000000}",
    "l1GlobalExitRootAddress":"${l1_ger_addr:-0x0000000000000000000000000000000000000000}",
    "options": {
        "blockRange":           5000,
        "concurrencyLimit":     20,
        "rpcBatchSize":         200,
        "rpcDelayMs":           0,
        "outputDir":            "$OUTPUT_DIR/output",
        "l1StartBlock":         0,
        "agglayerClient":       { "GRPC": { "URL": "$agglayer_grpc" } },
        "ignoreGenesisBalance": true
    }
}
EOF
    log_info "Config written to: $config_file"

    # Phase 1: run steps 0→E to build intermediate files (LBT, addresses, balances, certificate)
    # Stop before Step G since genesis ETH in other addresses causes LocalBalanceTreeUnderflow.
    log_info "Phase 1: running steps 0 → E to build intermediate files..."
    rm -rf "$OUTPUT_DIR/output"
    set +e
    "$OUTPUT_DIR/exit-certificate" --config "$config_file" --step 0    --verbose 2>&1 | tee -a "$OUTPUT_DIR/tool-output.log"
    "$OUTPUT_DIR/exit-certificate" --config "$config_file" --step a    --verbose 2>&1 | tee -a "$OUTPUT_DIR/tool-output.log"
    "$OUTPUT_DIR/exit-certificate" --config "$config_file" --step b    --verbose 2>&1 | tee -a "$OUTPUT_DIR/tool-output.log"
    "$OUTPUT_DIR/exit-certificate" --config "$config_file" --step c    --verbose 2>&1 | tee -a "$OUTPUT_DIR/tool-output.log"
    "$OUTPUT_DIR/exit-certificate" --config "$config_file" --step d    --verbose 2>&1 | tee -a "$OUTPUT_DIR/tool-output.log"
    "$OUTPUT_DIR/exit-certificate" --config "$config_file" --step e    --verbose 2>&1 | tee -a "$OUTPUT_DIR/tool-output.log"
    set -e

    # Show SC-locked values found
    if [[ -f "$OUTPUT_DIR/output/step-c-sc-locked-values.json" ]]; then
        log_info "SC-locked values (step C output):"
        python3 -c "
import json
data = json.load(open('$OUTPUT_DIR/output/step-c-sc-locked-values.json'))
for e in data:
    bal = e.get('scLockedBalance', e.get('sc_locked_balance', '0'))
    addr = e.get('wrappedTokenAddress', e.get('wrapped_token_address', '?'))
    print(f'  token={addr}  sc_locked={bal}')
" 2>/dev/null || true
    fi

    # Phase 2: patch step-e-exit-certificate.json to contain ONLY the SC-locked ERC-20 exit,
    # then run step G. This isolates the bug (ensureERC20Balance) from unrelated ETH underflow
    # errors caused by genesis balances.
    log_info "Phase 2: patching certificate to keep only SC-locked ERC-20 exits..."
    local cert_file="$OUTPUT_DIR/output/step-e-exit-certificate.json"
    python3 - "$cert_file" "$l1_token" "$EXIT_ADDRESS" <<'PYEOF'
import json, sys, re

cert_path = sys.argv[1]
l1_token   = sys.argv[2].lower()
exit_addr  = sys.argv[3].lower()

with open(cert_path) as f:
    cert = json.load(f)

orig = cert.get('bridge_exits', [])
# Keep only exits where: token matches our L1 TestToken AND destination is exitAddress (SC-locked)
sc_exits = [
    e for e in orig
    if (e.get('token_info', {}).get('origin_token_address', '').lower() == l1_token
        and e.get('dest_address', '').lower() == exit_addr)
]
print(f"  Original exits: {len(orig)}, SC-locked TestToken exits: {len(sc_exits)}", file=sys.stderr)
if not sc_exits:
    print("  WARNING: no SC-locked exits found for TestToken — check if step C found SC-locked value > 0", file=sys.stderr)
    sys.exit(1)

cert['bridge_exits'] = sc_exits
with open(cert_path, 'w') as f:
    json.dump(cert, f, indent=2)
print(f"  Patched certificate has {len(sc_exits)} SC-locked exit(s).", file=sys.stderr)
PYEOF

    # Phase 3: run step G against the patched certificate — expect the ensureERC20Balance error
    log_info "Phase 3: running step G — expect ensureERC20Balance error..."
    log_info ""
    set +e
    "$OUTPUT_DIR/exit-certificate" --config "$config_file" --step g --verbose 2>&1 | tee "$OUTPUT_DIR/step-g-output.log"
    local exit_code=$?
    set -e

    echo ""
    if [[ $exit_code -ne 0 ]]; then
        log_warn "Step G failed (exit $exit_code) — this is the bug described in F-01"
        log_info ""
        grep -E "ERC-20 balance insufficient|ensure ERC-20 balance|patching via storage" \
            "$OUTPUT_DIR/step-g-output.log" | head -10 || true
        log_info ""
        log_info "Root cause (step_g2.go:ensureERC20Balance):"
        log_info "  The function sees exitAddress has 0 wTTK balance and returns an error."
        log_info "  It should instead call hardhat_setStorageAt to patch the ERC-20 storage slot."
    else
        log_info "Step G completed successfully (bug may have been fixed)"
    fi
}

get_rpc_url_grpc() {
    local service="$1"
    local raw port
    raw=$(kurtosis port print "$KURTOSIS_ENCLAVE" "$service" aglr-grpc 2>/dev/null) \
        || { log_error "Cannot get gRPC port from '$service'"; exit 1; }
    port=$(echo "$raw" | sed -E 's|^[a-zA-Z]+://||' | cut -f2 -d':')
    echo "http://localhost:${port}"
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

check_deps

log_info "Enclave:      $KURTOSIS_ENCLAVE"
log_info "Network:      $NETWORK_INDEX"
log_info "EXIT_ADDRESS: $EXIT_ADDRESS  (receives SC-locked value, must have no wTTK)"
log_info "Output dir:   $OUTPUT_DIR"
mkdir -p "$OUTPUT_DIR"

L1_RPC=$(get_rpc_url "$L1_SERVICE" rpc)
L2_RPC=$(get_rpc_url "$L2_SERVICE" rpc)
log_info "L1 RPC: $L1_RPC"
log_info "L2 RPC: $L2_RPC"

# Determine L1 network ID (needed to look up wrapped token on L2)
L1_NETWORK_ID=$(cast chain-id --rpc-url "$L1_RPC")
log_info "L1 chainId (used as originNetwork): $L1_NETWORK_ID"

SENDER=$(cast wallet address --private-key "$PRIVATE_KEY")
log_info "Sender: $SENDER"

# Get bridge address
BRIDGE_TMP=$(mktemp -d)
kurtosis files download "$KURTOSIS_ENCLAVE" "aggkit-bridge-config-001" "$BRIDGE_TMP" &>/dev/null
L2_BRIDGE=$(grep 'BridgeAddr' "$BRIDGE_TMP/config.toml" | head -1 | tr -d '[:space:]' \
    | cut -f2 -d'=' | tr -d '"')
rm -rf "$BRIDGE_TMP"
log_info "L2 Bridge: $L2_BRIDGE"

# Step 1: Deploy test ERC-20 on L1
L1_TOKEN=$(deploy_test_erc20 "$L1_RPC")
log_info "L1 TestToken: $L1_TOKEN"

# Step 2: Bridge TTK to L2 (sends to SENDER address on L2)
bridge_erc20_to_l2 "$L1_RPC" "$L2_RPC" "$L2_BRIDGE" "$L1_TOKEN" "$SENDER"

# Step 3: Find wrapped token on L2
# Note: the bridge uses the L2 networkId as originNetwork for wrapped tokens, not L1 chainId.
# For cross-chain wrapped tokens, originNetwork is the network that originally issued the token.
# In AgglayerBridge, for an L1-originated token bridged to L2, the wrapped token is looked up
# by (originNetwork=0, originTokenAddress=L1_TOKEN) where 0 is the L1 network in the bridge topology.
# But the bridge topology uses networkId(). Let's try network 0 first (typical L1 network in bridge).
WRAPPED_TOKEN=""
for origin_net in 0 1 "$L1_NETWORK_ID"; do
    candidate=$(cast call --rpc-url "$L2_RPC" "$L2_BRIDGE" \
        "getTokenWrappedAddress(uint32,address)(address)" \
        "$origin_net" "$L1_TOKEN" 2>/dev/null || echo "0x0000000000000000000000000000000000000000")
    if [[ "$candidate" != "0x0000000000000000000000000000000000000000" ]]; then
        log_info "Found wrapped token at originNetwork=$origin_net: $candidate"
        WRAPPED_TOKEN="$candidate"
        break
    fi
done
if [[ -z "$WRAPPED_TOKEN" ]]; then
    log_error "Could not find wrapped token on L2 for L1 token $L1_TOKEN"
    log_error "It may still be pending. Try increasing CLAIM_TIMEOUT."
    exit 1
fi

# Step 4: Create SC-locked tokens
create_sc_locked_tokens "$L2_RPC" "$L2_BRIDGE" "$WRAPPED_TOKEN" "$SENDER"

# Capture current L2 block as target
TARGET_BLOCK=$(cast block-number --rpc-url "$L2_RPC")
log_info "Target block (current L2 tip): $TARGET_BLOCK"

# Step 5: Build the tool
build_tool

# Step 6: Run and observe the error
run_pipeline "$L1_RPC" "$L2_RPC" "$L2_BRIDGE" "$L1_NETWORK_ID" "$TARGET_BLOCK" "$L1_TOKEN"
