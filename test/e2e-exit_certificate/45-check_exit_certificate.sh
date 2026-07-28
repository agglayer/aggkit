#!/usr/bin/env bash
# Verifies the generated exit certificate contains the ERC-20 scenario prepared
# by 20-prepare_network.sh (AET-02): the bridged ERC-20 must produce exactly two
# bridge exits — one for the active holder and one for the passive recipient
# that only ever received a Transfer (never sent any L2 transaction).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/helper.sh"

AGGKIT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
EXIT_CERT_CONFIG="${EXIT_CERT_CONFIG:-${AGGKIT_DIR}/tmp/exit_certificate-kurtosis.json}"
ERC20_STATE_FILE="${ERC20_STATE_FILE:-${AGGKIT_DIR}/tmp/exit_certificate-e2e-erc20.env}"

if [[ ! -f "$EXIT_CERT_CONFIG" ]]; then
    log_error "exit_certificate config not found: $EXIT_CERT_CONFIG"
    log_error "Run 40-generate_exit_certificate.sh first."
    exit 1
fi
if [[ ! -f "$ERC20_STATE_FILE" ]]; then
    log_error "ERC-20 scenario state file not found: $ERC20_STATE_FILE"
    log_error "Run 20-prepare_network.sh first."
    exit 1
fi

# set -a exports everything the state file defines, so the embedded python
# check below can read the scenario via os.environ.
set -a
# shellcheck source=/dev/null
source "$ERC20_STATE_FILE"
set +a

# Resolve the certificate path: options.outputDir is relative to the config file dir.
FINAL_CERTIFICATE=$(python3 - "$EXIT_CERT_CONFIG" <<'PYEOF'
import json, os, sys
config_path = sys.argv[1]
with open(config_path) as f:
    config = json.load(f)
output_dir = config.get("options", {}).get("outputDir", "./output")
if not os.path.isabs(output_dir):
    output_dir = os.path.join(os.path.dirname(os.path.abspath(config_path)), output_dir)
print(os.path.join(output_dir, "exit-certificate-final.json"))
PYEOF
)

if [[ ! -f "$FINAL_CERTIFICATE" ]]; then
    log_error "Final certificate not found: $FINAL_CERTIFICATE"
    log_error "Run 40-generate_exit_certificate.sh first."
    exit 1
fi

log_info "🔎 Checking ERC-20 bridge exits in: $FINAL_CERTIFICATE"
log_info "   ERC-20 (L1 origin): $ERC20_L1_ADDRESS"
log_info "   holder:             $ERC20_HOLDER_ADDRESS ($((ERC20_BRIDGE_AMOUNT - ERC20_TRANSFER_AMOUNT)) expected)"
log_info "   passive recipient:  $ERC20_PASSIVE_ADDRESS ($ERC20_TRANSFER_AMOUNT expected)"

python3 - "$FINAL_CERTIFICATE" <<'PYEOF'
import json, os, sys

GREEN, RED, NC = "\033[0;32m", "\033[0;31m", "\033[0m"
failures = 0

def check(ok, message):
    global failures
    mark = f"{GREEN}✅{NC}" if ok else f"{RED}❌{NC}"
    print(f"{mark} {message}", file=sys.stderr)
    if not ok:
        failures += 1

erc20 = os.environ["ERC20_L1_ADDRESS"].lower()
holder = os.environ["ERC20_HOLDER_ADDRESS"].lower()
passive = os.environ["ERC20_PASSIVE_ADDRESS"].lower()
bridged = int(os.environ["ERC20_BRIDGE_AMOUNT"])
transferred = int(os.environ["ERC20_TRANSFER_AMOUNT"])

with open(sys.argv[1]) as f:
    certificate = json.load(f)

token_exits = [
    bexit for bexit in certificate["bridge_exits"]
    if (bexit.get("token_info") or {}).get("origin_token_address", "").lower() == erc20
]

check(len(token_exits) == 2,
      f"the ERC-20 has exactly 2 bridge exits (found {len(token_exits)})")

by_dest = {bexit["dest_address"].lower(): bexit for bexit in token_exits}

holder_exit = by_dest.get(holder)
check(holder_exit is not None, f"holder {holder} has a bridge exit")
if holder_exit:
    check(int(holder_exit["amount"]) == bridged - transferred,
          f"holder amount is {bridged - transferred} (got {holder_exit['amount']})")

passive_exit = by_dest.get(passive)
check(passive_exit is not None,
      f"passive ERC-20 recipient {passive} has a bridge exit (AET-02)")
if passive_exit:
    check(int(passive_exit["amount"]) == transferred,
          f"passive recipient amount is {transferred} (got {passive_exit['amount']})")

for bexit in token_exits:
    check(bexit["token_info"]["origin_network"] == 0,
          f"exit to {bexit['dest_address']}: token origin_network is 0")
    check(bexit["dest_network"] == 0,
          f"exit to {bexit['dest_address']}: dest_network is 0")

if failures:
    print(f"{RED}[ERROR]{NC} {failures} certificate check(s) failed", file=sys.stderr)
    sys.exit(1)
PYEOF

log_info "✅ Exit certificate contains both ERC-20 holders (active + passive)"
