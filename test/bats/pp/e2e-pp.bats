setup() {
    load '../helpers/common-setup'
    
    _common_setup
}

@test "Verify certificate settlement" {
    echo "Waiting 10 minutes to get some settled certificate...." >&3

    readonly bridge_addr=$L2_BRIDGE_ADDRESS
    readonly l2_rpc_network_id=$(cast call --rpc-url $l2_rpc_url $bridge_addr 'networkID() (uint32)')

    run $PROJECT_ROOT/../scripts/agglayer_certificates_monitor.sh 1 600 $l2_rpc_network_id
    assert_success
}
