#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/helper.sh"

EXIT_CETIFICATE_SCRIPT_DIR="${SCRIPT_DIR}/../../tools/exit_certificate/scripts"

#
# First we need a settled certificate, for that we need
# a bridge L2 -> L1 to produce this certificate. 
# for that we need first a L1 -> L2 to don't run underbalance tree error

log_info "🛠️ Set kurtosis env variables"
source <("${EXIT_CETIFICATE_SCRIPT_DIR}/export_kurtosis_env.sh" 1)

log_info "🔗 Run L1 -> L2 bridge to have funds in L2 (autoclaimed by zkevm-bridge-service)"
run_quiet "${EXIT_CETIFICATE_SCRIPT_DIR}/bridge_l1_to_l2.sh" --amount 98476876062940103 --wait

log_info "🔗 Run L2 -> L1 bridge to produce a certificate"
run_quiet "${EXIT_CETIFICATE_SCRIPT_DIR}/bridge_l2_to_l1.sh" --amount 1

log_info "⏳ Wait for the certificate to be settled"
run_quiet "${EXIT_CETIFICATE_SCRIPT_DIR}/agglayer_certificate_status.sh" --wait

log_info "🛑 Stop aggsender to avoid new certificates to be produced"
kurtosis service stop aggkit aggkit-001

log_info "✅ Done, the network is ready for exit-certificate tests"