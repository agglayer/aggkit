#!/usr/bin/env bash
# Regenerates the Go bindings of the AggOracleCommittee v2 contract.
#
# Usage: ./generate.sh <path-to-agglayer-contracts-clone>
#
# The clone must be checked out at PINNED_COMMIT (branch feature/agg-oracle-committee of
# https://github.com/agglayer/agglayer-contracts). The contract is compiled with hardhat, the ABI and
# the bytecode are extracted from the artifact and abigen generates the Go package.
#
# Requires: git, jq, node/npm (v24, see the agglayer-contracts repo) and go.
set -euo pipefail

# Commit of agglayer-contracts the bindings are generated from. Bump it (and re-run this script)
# when the contract changes.
PINNED_COMMIT="a6b96a86f8ec6345b2fa2f618f3f8c418c6c6d92"
ABIGEN_VERSION="v1.17.7"
PACKAGE="aggoraclecommitteev2"
ARTIFACT="artifacts/contracts/sovereignChains/AggOracleCommittee.sol/AggOracleCommittee.json"

log_info() { echo "[INFO] $*"; }
log_error() { echo "[ERROR] $*" >&2; }

usage() {
    echo "Usage: $0 <path-to-agglayer-contracts-clone>" >&2
    exit 1
}

[[ $# -eq 1 ]] || usage
CONTRACTS_DIR="$(cd "$1" && pwd)"
OUT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

head_commit="$(git -C "${CONTRACTS_DIR}" rev-parse HEAD)"
if [[ "${head_commit}" != "${PINNED_COMMIT}" ]]; then
    log_error "agglayer-contracts is at ${head_commit}, expected ${PINNED_COMMIT}"
    log_error "run: git -C ${CONTRACTS_DIR} checkout ${PINNED_COMMIT}"
    exit 1
fi
if [[ -n "$(git -C "${CONTRACTS_DIR}" status --porcelain --untracked-files=no)" ]]; then
    log_error "agglayer-contracts has local changes, refusing to generate bindings from a dirty tree"
    exit 1
fi

log_info "Compiling contracts at ${PINNED_COMMIT}"
(cd "${CONTRACTS_DIR}" && npm ci && npx hardhat compile)

artifact_path="${CONTRACTS_DIR}/${ARTIFACT}"
[[ -f "${artifact_path}" ]] || { log_error "artifact not found: ${artifact_path}"; exit 1; }

jq '.abi' "${artifact_path}" >"${OUT_DIR}/${PACKAGE}.abi"
jq -r '.bytecode' "${artifact_path}" | sed 's/^0x//' >"${OUT_DIR}/${PACKAGE}.bin"

log_info "Generating Go bindings with abigen ${ABIGEN_VERSION}"
go run "github.com/ethereum/go-ethereum/cmd/abigen@${ABIGEN_VERSION}" \
    --abi "${OUT_DIR}/${PACKAGE}.abi" \
    --bin "${OUT_DIR}/${PACKAGE}.bin" \
    --pkg "${PACKAGE}" \
    --type AggOracleCommitteeV2 \
    --out "${OUT_DIR}/${PACKAGE}.go"

log_info "Done: ${OUT_DIR}/${PACKAGE}.go (agglayer-contracts ${PINNED_COMMIT})"
