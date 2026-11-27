#!/usr/bin/env bash
# Rebuilds the creation bytecode (*.bin) of the Circle USDC (USDC.e, usdc-lxly), Safe and recovery-package
# contracts used by the e2e and checks it against the committed .bin files. See README.md.
#
# The Solidity sources are not vendored in this repository (licensing): build.sh fetches them from their
# upstream repositories at pinned commits into a temp dir and compiles them there. The two recovery-package
# contracts (ZkMinterBurnerPayout, UsdcRecoveryBurn) have no public repository to pin: they come from the
# Circle USDC recovery package, which must be given with --package (their sha256 are pinned below);
# without it they are skipped.
#
# Usage: ./build.sh [--package <dir>] [--write]
#   --package <dir>  a directory holding the package's ZkMinterBurnerPayout.sol and UsdcRecoveryBurn.sol
#                    (e.g. its contracts/src/), checked against the pinned sha256.
#   --write          overwrite the committed .bin files with the build output instead of only comparing.
#
# Needs git, forge (Foundry; the committed .bin were produced with forge 1.5.1 — another version may change
# the metadata hash of the third-party bins), jq and sha256sum, plus network access to GitHub and to the solc
# binaries forge downloads. Nothing from the upstream repositories is executed: their Solidity sources are
# copied into Foundry projects created here, and only `forge build` runs on them. Exits non-zero when a
# rebuilt .bin differs from the committed one (without --write).
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${HERE}/../../helper.sh"

# --- Pinned upstreams ---------------------------------------------------------------------------
STABLECOIN_REPO="https://github.com/circlefin/stablecoin-evm"
STABLECOIN_COMMIT="b5aba6ce7b5a867fda355a54a8449818b313bf1a" # tag v2.1.0
OZ_REPO="https://github.com/OpenZeppelin/openzeppelin-contracts"
OZ_FIATTOKEN_COMMIT="de99bccbfd4ecd19d7369d01b070aa72c64423c9" # tag v3.1.0 (stablecoin-evm v2.1.0 yarn.lock)
USDC_LXLY_REPO="https://github.com/BuildOnPolygon/usdc-lxly"
USDC_LXLY_COMMIT="d7cea0d4c901af48f2e80a9b631489c215018374" # default branch (main)
OZ_LXLY_COMMIT="e50c24f5839db17f46991478384bfda14acfb830"   # v4.9.2, usdc-lxly lib/openzeppelin-contracts
OZU_REPO="https://github.com/OpenZeppelin/openzeppelin-contracts-upgradeable"
OZU_LXLY_COMMIT="bc95521e34dcd49792065e264a7ad2b5a86f0091" # v4.9.2, usdc-lxly lib/openzeppelin-contracts-upgradeable
ZKEVM_REPO="https://github.com/0xPolygonHermez/zkevm-contracts"
ZKEVM_LXLY_COMMIT="b737362078215bfcb3973f9e8a4183f711c8738a" # usdc-lxly lib/zkevm-contracts
SAFE_REPO="https://github.com/safe-global/safe-smart-account"
SAFE_COMMIT="186a21a74b327f17fc41217a927dea7064f74604" # tag v1.3.0
# Circle USDC recovery package (v2, not publicly available; no repository to pin): contracts/src/ sources.
PACKAGE_PAYOUT_SHA256="ed2e8d373cf414adaf75bc5bba00befc88182f91de61f96d82d3c37fe8aaf40b" # ZkMinterBurnerPayout.sol
PACKAGE_BURN_SHA256="2918cd7f869a345fd5389905cc5a0d4f08e64e9be432c0752052e640687fd17a"   # UsdcRecoveryBurn.sol

PACKAGE_DIR=""
WRITE=0
while [[ $# -gt 0 ]]; do
    case "$1" in
        --package) [[ $# -ge 2 ]] || { log_error "--package needs a directory"; exit 1; }; PACKAGE_DIR="$2"; shift 2 ;;
        --write) WRITE=1; shift ;;
        -h | --help) sed -n '2,24p' "${BASH_SOURCE[0]}"; exit 0 ;;
        *) log_error "unknown argument: $1"; exit 1 ;;
    esac
done

for bin in git forge jq sha256sum; do
    command -v "${bin}" >/dev/null 2>&1 || {
        log_error "'${bin}' not found in \$PATH."
        exit 1
    }
done

WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}"' EXIT

# fetch <dir> <repo url> <commit>: shallow checkout of one commit (no submodules, no hooks run).
fetch() {
    local dir="$1" url="$2" commit="$3"
    mkdir -p "${dir}"
    git -C "${dir}" init -q
    local try
    for try in 1 2 3; do
        git -C "${dir}" fetch -q --depth 1 "${url}" "${commit}" && break
        [ "${try}" = 3 ] && {
            log_error "cannot fetch ${url} ${commit}"
            exit 1
        }
        log_warn "fetch of ${url} failed, retrying"
        sleep 5
    done
    git -C "${dir}" -c advice.detachedHead=false checkout -q FETCH_HEAD
    [ "$(git -C "${dir}" rev-parse HEAD)" = "${commit}" ] || {
        log_error "${url}: got $(git -C "${dir}" rev-parse HEAD), expected ${commit}"
        exit 1
    }
}

# submodule_commit <repo dir> <path>: the commit a submodule is pinned to in the superproject.
submodule_commit() { git -C "$1" ls-tree HEAD "$2" | awk '{print $3}'; }

# --- Fetch -------------------------------------------------------------------------------------
log_info "Fetching the pinned upstream commits into ${WORK}"
fetch "${WORK}/stablecoin-evm" "${STABLECOIN_REPO}" "${STABLECOIN_COMMIT}"
fetch "${WORK}/oz-fiattoken" "${OZ_REPO}" "${OZ_FIATTOKEN_COMMIT}"
fetch "${WORK}/usdc-lxly" "${USDC_LXLY_REPO}" "${USDC_LXLY_COMMIT}"
for sub in "openzeppelin-contracts ${OZ_LXLY_COMMIT}" \
    "openzeppelin-contracts-upgradeable ${OZU_LXLY_COMMIT}" \
    "zkevm-contracts ${ZKEVM_LXLY_COMMIT}"; do
    read -r name commit <<<"${sub}"
    [ "$(submodule_commit "${WORK}/usdc-lxly" "lib/${name}")" = "${commit}" ] || {
        log_error "usdc-lxly lib/${name} is not pinned to ${commit}"
        exit 1
    }
done
fetch "${WORK}/oz-lxly" "${OZ_REPO}" "${OZ_LXLY_COMMIT}"
fetch "${WORK}/ozu-lxly" "${OZU_REPO}" "${OZU_LXLY_COMMIT}"
fetch "${WORK}/zkevm-contracts" "${ZKEVM_REPO}" "${ZKEVM_LXLY_COMMIT}"
fetch "${WORK}/safe" "${SAFE_REPO}" "${SAFE_COMMIT}"

# --- Foundry projects (created here; only Solidity sources are copied in) -----------------------
# FiatToken: stablecoin-evm v2.1.0 + OpenZeppelin 3.1.0. Settings of the zkEVM USDC.e deployment
# (Sourcify, FiatTokenProxy 0x37eA…19e5): solc 0.6.12, optimizer 200 runs, istanbul.
FT="${WORK}/proj-fiattoken"
mkdir -p "${FT}/src" "${FT}/lib/openzeppelin-contracts"
for d in upgradeability util v1 v1.1 v2; do
    cp -r "${WORK}/stablecoin-evm/contracts/${d}" "${FT}/src/"
done
rm -rf "${FT}/src/v2/upgrader"
cp -r "${WORK}/oz-fiattoken/contracts" "${FT}/lib/openzeppelin-contracts/"
cat >"${FT}/foundry.toml" <<'EOF'
[profile.default]
src = "src"
out = "out"
libs = ["lib"]
solc_version = "0.6.12"
evm_version = "istanbul"
optimizer = true
optimizer_runs = 200
remappings = ["@openzeppelin/contracts/=lib/openzeppelin-contracts/contracts/"]
EOF

# usdc-lxly: its own foundry.toml (solc 0.8.17) with the settings of the mainnet deployment
# (Sourcify: optimizer 200 runs, london), and its remappings.txt.
LX="${WORK}/proj-lxly"
mkdir -p "${LX}/lib/openzeppelin-contracts" "${LX}/lib/openzeppelin-contracts-upgradeable" "${LX}/lib/zkevm-contracts"
cp -r "${WORK}/usdc-lxly/src" "${LX}/src"
rm -rf "${LX}/src/mocks"
cp "${WORK}/usdc-lxly/remappings.txt" "${LX}/"
cp -r "${WORK}/oz-lxly/contracts" "${LX}/lib/openzeppelin-contracts/"
cp -r "${WORK}/ozu-lxly/contracts" "${LX}/lib/openzeppelin-contracts-upgradeable/"
cp -r "${WORK}/zkevm-contracts/contracts" "${LX}/lib/zkevm-contracts/"
cat >"${LX}/foundry.toml" <<'EOF'
[profile.default]
src = "src"
out = "out"
libs = ["lib"]
solc_version = "0.8.17"
optimizer = true
optimizer_runs = 200
evm_version = "london"
EOF

# Safe v1.3.0: solc 0.7.6, optimizer disabled (CHANGELOG.md of v1.3.0). ViewStorageAccessible.sol
# (pragma <0.6.0, an unused interface) is left out.
SF="${WORK}/proj-safe"
mkdir -p "${SF}"
cp -r "${WORK}/safe/contracts" "${SF}/contracts"
rm -rf "${SF}/contracts/test" "${SF}/contracts/examples" "${SF}/contracts/interfaces/ViewStorageAccessible.sol"
cat >"${SF}/foundry.toml" <<'EOF'
[profile.default]
src = "contracts"
out = "out"
libs = []
solc_version = "0.7.6"
evm_version = "istanbul"
optimizer = false
EOF

# Recovery package contracts: the unmodified sources given with --package (sha256 checked), with the
# package's contracts/foundry.toml.
PK="${WORK}/proj-package"
PROJECTS=("${FT}" "${LX}" "${SF}")
if [[ -n "${PACKAGE_DIR}" ]]; then
    mkdir -p "${PK}/src"
    for pair in "ZkMinterBurnerPayout.sol ${PACKAGE_PAYOUT_SHA256}" "UsdcRecoveryBurn.sol ${PACKAGE_BURN_SHA256}"; do
        read -r file sum <<<"${pair}"
        src="$(find "${PACKAGE_DIR}" -type f -name "${file}" | head -n 1)"
        [[ -n "${src}" ]] || { log_error "--package ${PACKAGE_DIR}: ${file} not found"; exit 1; }
        [[ "$(sha256sum <"${src}" | cut -d' ' -f1)" == "${sum}" ]] || {
            log_error "${src}: sha256 differs from the pinned ${sum} (not the package's source)"
            exit 1
        }
        cp "${src}" "${PK}/src/${file}"
    done
    PROJECTS+=("${PK}")
else
    log_warn "No --package: ZkMinterBurnerPayout.bin and UsdcRecoveryBurn.bin are not rebuilt (no public source to fetch)"
fi
mkdir -p "${PK}"
cat >"${PK}/foundry.toml" <<'EOF'
[profile.default]
src = "src"
out = "out"
solc_version = "0.8.28"
evm_version = "shanghai"
optimizer = true
optimizer_runs = 200
bytecode_hash = "none"
cbor_metadata = false
EOF

for p in "${PROJECTS[@]}"; do
    log_info "forge build ${p##*/}"
    run_quiet forge build --root "${p}"
done

# --- Outputs -----------------------------------------------------------------------------------
MISMATCH=0
# check_bin <project> <source path in project> <contract>: compares the creation bytecode (plain hex, no 0x,
# no trailing newline) with the committed <contract>.bin, or writes it there with --write.
check_bin() {
    local proj="$1" src="$2" name="$3" art code
    art="${proj}/out/$(basename "${src}")/${name}.json"
    code="$(jq -r '.bytecode.object' "${art}")"
    code="${code#0x}"
    if [[ ! "${code}" =~ ^[0-9a-f]+$ ]]; then
        log_error "${name}: bytecode is not plain hex (unlinked library?)"
        exit 1
    fi
    if [[ "${WRITE}" -eq 1 ]]; then
        printf '%s' "${code}" >"${HERE}/${name}.bin"
        log_info "wrote ${name}.bin"
    elif [[ "$(printf '%s' "${code}" | sha256sum)" == "$(sha256sum <"${HERE}/${name}.bin")" ]]; then
        log_info "✅ ${name}.bin matches the build"
    else
        log_error "❌ ${name}.bin differs from the build (another forge/solc version changes the metadata hash)"
        MISMATCH=$((MISMATCH + 1))
    fi
}

# name|project|source in project (upstream path: see README.md)
TARGETS=(
    "FiatTokenV2_1|${FT}|src/v2/FiatTokenV2_1.sol"
    "FiatTokenProxy|${FT}|src/v1/FiatTokenProxy.sol"
    "L1Escrow|${LX}|src/L1Escrow.sol"
    "L1EscrowProxy|${LX}|src/L1EscrowProxy.sol"
    "ZkMinterBurner|${LX}|src/ZkMinterBurner.sol"
    "ZkMinterBurnerProxy|${LX}|src/ZkMinterBurnerProxy.sol"
    "NativeConverter|${LX}|src/NativeConverter.sol"
    "NativeConverterProxy|${LX}|src/NativeConverterProxy.sol"
    "GnosisSafeL2|${SF}|contracts/GnosisSafeL2.sol"
    "GnosisSafeProxyFactory|${SF}|contracts/proxies/GnosisSafeProxyFactory.sol"
    "MultiSendCallOnly|${SF}|contracts/libraries/MultiSendCallOnly.sol"
)
if [[ -n "${PACKAGE_DIR}" ]]; then
    TARGETS+=("ZkMinterBurnerPayout|${PK}|src/ZkMinterBurnerPayout.sol" "UsdcRecoveryBurn|${PK}|src/UsdcRecoveryBurn.sol")
fi
for t in "${TARGETS[@]}"; do
    IFS='|' read -r name proj src <<<"${t}"
    check_bin "${proj}" "${src}" "${name}"
done

log_info "sha256 of the bins:"
(cd "${HERE}" && sha256sum ./*.bin)
if [[ "${MISMATCH}" -ne 0 ]]; then
    log_error "${MISMATCH} .bin file(s) differ from the build (rerun with --write to update them)"
    exit 1
fi
