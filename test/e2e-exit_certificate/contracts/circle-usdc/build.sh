#!/usr/bin/env bash
# Regenerates the creation bytecode (*.bin) and the flattened sources of the Circle USDC (USDC.e,
# usdc-lxly) contracts used by the e2e, from pinned upstream commits. See README.md.
#
# Usage: ./build.sh
#
# Needs git, forge (Foundry; the sha256 listed in README.md were produced with forge 1.5.1), jq and
# sha256sum, plus network access to GitHub and to the solc binaries forge downloads. Nothing from the
# upstream repositories is executed: their Solidity sources are copied into Foundry projects created
# here, and only `forge build` / `forge flatten` run on them. Idempotent: running it again rewrites
# the same files.
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

# Recovery package contracts: the unmodified sources of this directory, with the package's
# contracts/foundry.toml.
PK="${WORK}/proj-package"
mkdir -p "${PK}/src"
cp "${HERE}/ZkMinterBurnerPayout.sol" "${HERE}/UsdcRecoveryBurn.sol" "${PK}/src/"
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

for p in "${FT}" "${LX}" "${SF}" "${PK}"; do
    log_info "forge build ${p##*/}"
    run_quiet forge build --root "${p}"
done

# --- Outputs -----------------------------------------------------------------------------------
# write_bin <project> <source path in project> <contract>: <contract>.bin = creation bytecode, plain
# hex, no 0x, no trailing newline.
write_bin() {
    local proj="$1" src="$2" name="$3" art code
    art="${proj}/out/$(basename "${src}")/${name}.json"
    code="$(jq -r '.bytecode.object' "${art}")"
    code="${code#0x}"
    if [[ ! "${code}" =~ ^[0-9a-f]+$ ]]; then
        log_error "${name}: bytecode is not plain hex (unlinked library?)"
        exit 1
    fi
    printf '%s' "${code}" >"${HERE}/${name}.bin"
}

# write_flat <project> <source path in project> <contract> <repo> <commit> <ref> <upstream path>
#            <licence> <compiler>: <contract>.sol = header + `forge flatten` output (code unedited).
write_flat() {
    local proj="$1" src="$2" name="$3" repo="$4" commit="$5" ref="$6" upath="$7" lic="$8" cc="$9"
    {
        echo "// Flattened by build.sh with \`forge flatten\`; the code below is not edited."
        echo "//   Upstream:  ${repo}"
        echo "//   Commit:    ${commit} (${ref})"
        echo "//   Path:      ${upath}"
        echo "//   Licence:   ${lic}"
        echo "//   Compiler:  ${cc}"
        echo "// ${name}.bin is compiled from the upstream source tree (see build.sh), not from this file."
        echo
        forge flatten --root "${proj}" "${proj}/${src}"
    } >"${HERE}/${name}.sol"
}

FT_LIC="MIT (stablecoin-evm v2.1.0 LICENSE, CENTRE SECZ; OpenZeppelin Contracts 3.1.0)"
FT_CC="solc 0.6.12, optimizer enabled, 200 runs, evm istanbul"
LX_LIC="MIT (usdc-lxly SPDX headers; the repository has no LICENSE file; OpenZeppelin 4.9.2)"
LX_LIC_ZK="${LX_LIC}; AGPL-3.0 for the zkevm-contracts interfaces IBridgeMessageReceiver and IPolygonZkEVMBridge (${ZKEVM_LXLY_COMMIT})"
LX_CC="solc 0.8.17, optimizer enabled, 200 runs, evm london"
SF_LIC="LGPL-3.0-only"
SF_CC="solc 0.7.6, optimizer disabled, evm istanbul"

# name|project|source in project|upstream repo|commit|ref|upstream path|licence|compiler
TARGETS=(
    "FiatTokenV2_1|${FT}|src/v2/FiatTokenV2_1.sol|${STABLECOIN_REPO}|${STABLECOIN_COMMIT}|tag v2.1.0|contracts/v2/FiatTokenV2_1.sol|${FT_LIC}|${FT_CC}"
    "FiatTokenProxy|${FT}|src/v1/FiatTokenProxy.sol|${STABLECOIN_REPO}|${STABLECOIN_COMMIT}|tag v2.1.0|contracts/v1/FiatTokenProxy.sol|${FT_LIC}|${FT_CC}"
    "L1Escrow|${LX}|src/L1Escrow.sol|${USDC_LXLY_REPO}|${USDC_LXLY_COMMIT}|main|src/L1Escrow.sol|${LX_LIC_ZK}|${LX_CC}"
    "L1EscrowProxy|${LX}|src/L1EscrowProxy.sol|${USDC_LXLY_REPO}|${USDC_LXLY_COMMIT}|main|src/L1EscrowProxy.sol|${LX_LIC}|${LX_CC}"
    "ZkMinterBurner|${LX}|src/ZkMinterBurner.sol|${USDC_LXLY_REPO}|${USDC_LXLY_COMMIT}|main|src/ZkMinterBurner.sol|${LX_LIC_ZK}|${LX_CC}"
    "ZkMinterBurnerProxy|${LX}|src/ZkMinterBurnerProxy.sol|${USDC_LXLY_REPO}|${USDC_LXLY_COMMIT}|main|src/ZkMinterBurnerProxy.sol|${LX_LIC}|${LX_CC}"
    "NativeConverter|${LX}|src/NativeConverter.sol|${USDC_LXLY_REPO}|${USDC_LXLY_COMMIT}|main|src/NativeConverter.sol|${LX_LIC_ZK}|${LX_CC}"
    "NativeConverterProxy|${LX}|src/NativeConverterProxy.sol|${USDC_LXLY_REPO}|${USDC_LXLY_COMMIT}|main|src/NativeConverterProxy.sol|${LX_LIC}|${LX_CC}"
    "GnosisSafeL2|${SF}|contracts/GnosisSafeL2.sol|${SAFE_REPO}|${SAFE_COMMIT}|tag v1.3.0|contracts/GnosisSafeL2.sol|${SF_LIC}|${SF_CC}"
    "GnosisSafeProxyFactory|${SF}|contracts/proxies/GnosisSafeProxyFactory.sol|${SAFE_REPO}|${SAFE_COMMIT}|tag v1.3.0|contracts/proxies/GnosisSafeProxyFactory.sol|${SF_LIC}|${SF_CC}"
    "MultiSendCallOnly|${SF}|contracts/libraries/MultiSendCallOnly.sol|${SAFE_REPO}|${SAFE_COMMIT}|tag v1.3.0|contracts/libraries/MultiSendCallOnly.sol|${SF_LIC}|${SF_CC}"
)
for t in "${TARGETS[@]}"; do
    IFS='|' read -r name proj src repo commit ref upath lic cc <<<"${t}"
    write_bin "${proj}" "${src}" "${name}"
    write_flat "${proj}" "${src}" "${name}" "${repo}" "${commit}" "${ref}" "${upath}" "${lic}" "${cc}"
done
# The package contracts keep their own (unmodified) sources: only the bins are written.
write_bin "${PK}" "src/ZkMinterBurnerPayout.sol" "ZkMinterBurnerPayout"
write_bin "${PK}" "src/UsdcRecoveryBurn.sol" "UsdcRecoveryBurn"

log_info "sha256 of the bins:"
(cd "${HERE}" && sha256sum ./*.bin)
