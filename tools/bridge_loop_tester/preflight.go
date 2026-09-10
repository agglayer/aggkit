package bridgelooptester

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"

	bridgeserviceclient "github.com/agglayer/aggkit/bridgeservice/client"
	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	trackertypes "github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
)

// NetworkPreflight is what the live preflight learned about one configured network. Every field is
// read-only: the preflight submits no transaction, on any mode.
type NetworkPreflight struct {
	// NetworkID and Name are the configured identity.
	NetworkID uint32 `json:"network_id"`
	Name      string `json:"name"`
	// ChainID is what eth_chainId reported (the configured ChainID, when set, was already
	// checked against it while the client was built).
	ChainID uint64 `json:"chain_id"`
	// From is the signing account resolved from the network's Signer config.
	From common.Address `json:"from"`
	// NativeBalance is From's native balance, and MinNativeReserve the configured floor it must
	// stay above.
	NativeBalance    *big.Int `json:"native_balance"`
	MinNativeReserve *big.Int `json:"min_native_reserve"`
	// GasTokenAddress is the bridge's gasTokenAddress(): the zero address means the network's gas
	// token is ether, which is the only case an "eth" loop supports (DESIGN.md §6, Gap G3).
	GasTokenAddress common.Address `json:"gas_token_address"`
	// WETHToken is the bridge's WETHToken(): non-zero on a network whose gas token is not ether,
	// where a native bridge would have to go through WETH - a path this tool deliberately does
	// not implement.
	WETHToken common.Address `json:"weth_token"`
	// BridgeNetworkID is the networkID() the bridge contract itself reports, cross-checked
	// against the configured NetworkID.
	BridgeNetworkID uint32 `json:"bridge_network_id"`
	// BridgeAddrConfigured is the configured bridge address; BridgeAddrReported is what the
	// proxy's GET /bridge/v1/config publishes for this network, when it answered.
	BridgeAddrConfigured common.Address `json:"bridge_addr_configured"`
	BridgeAddrReported   common.Address `json:"bridge_addr_reported,omitempty"`
	// ProxyReachable reports whether GET /bridge/v1/config?network_id=<id> answered at all.
	ProxyReachable bool `json:"proxy_reachable"`
	// GasTokenIsEther is the derived verdict an "eth" loop depends on.
	GasTokenIsEther bool `json:"gas_token_is_ether"`
	// Notes carries per-network observations that are not themselves failures.
	Notes []string `json:"notes,omitempty"`
}

// PreflightReport is the whole result of the live, read-only preflight: what every network
// reported, what the proxy reported, and the problems found, split into refusals (Errors) and
// observations worth reading (Warnings).
type PreflightReport struct {
	// Strict records whether the preflight ran in strict mode (the `validate` command), where
	// every problem is an error, or in run mode, where only the checks that can invalidate the
	// test itself are.
	Strict bool `json:"strict"`
	// ProxyHealthy and ProxyHealth record GET /tracker/v1/health.
	ProxyHealthy bool                         `json:"proxy_healthy"`
	ProxyHealth  *trackertypes.HealthResponse `json:"proxy_health,omitempty"`
	// L1BridgeServiceAvailable records GET /bridge/v1/config?network_id=0: false means the proxy
	// has no L1 bridge service configured at all, so no hop through network 0 can ever work
	// (DESIGN.md Gap G4). NetworkZeroParticipates says whether any enabled loop cares.
	L1BridgeServiceAvailable bool `json:"l1_bridge_service_available"`
	NetworkZeroParticipates  bool `json:"network_zero_participates"`
	// Networks holds one entry per configured network, in configuration order.
	Networks []NetworkPreflight `json:"networks"`
	// Errors are the reasons the run must not start; Warnings are observations that do not by
	// themselves invalidate it.
	Errors   []string `json:"errors,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// OK reports whether the preflight found no refusal.
func (p *PreflightReport) OK() bool { return len(p.Errors) == 0 }

// Err returns one error joining every refusal, or nil when there is none.
func (p *PreflightReport) Err() error {
	if len(p.Errors) == 0 {
		return nil
	}

	joined := make([]error, 0, len(p.Errors))
	for _, problem := range p.Errors {
		joined = append(joined, errors.New(problem))
	}

	return errors.Join(joined...)
}

// Network returns the preflight entry for a network ID, or nil.
func (p *PreflightReport) Network(networkID uint32) *NetworkPreflight {
	for i := range p.Networks {
		if p.Networks[i].NetworkID == networkID {
			return &p.Networks[i]
		}
	}

	return nil
}

// Preflight performs every live, read-only check the tool can make before moving any value, and
// returns what it found. It never submits a transaction, in either mode.
//
// # Strict vs. run mode
//
// strict is what the `validate` command uses: every problem found is a refusal, including ones
// that are merely suspicious (a signer below its MinNativeReserve, a bridge address the proxy
// disagrees with, an L2 whose bridge service the proxy cannot route to yet). That is the right
// bar for a command whose only job is to answer "will this config work".
//
// Run mode (strict == false) is the subset (*Orchestrator).Run enforces before it starts driving
// hops. Two checks stay refusals there, because passing them is what makes the *test* meaningful
// rather than merely convenient:
//
//   - a loop configured as "eth" on a network whose gasTokenAddress() is not the zero address
//     (DESIGN.md Gap G3). Bridging the native asset there needs the WETH path, which this tool
//     does not implement, so the hop would fail confusingly mid-run instead of clearly at
//     startup.
//   - a route through network 0 when the proxy reports no L1 bridge service
//     (ErrL1BridgeServiceUnavailable, DESIGN.md Gap G4). This is a routing-level, permanent
//     condition, not a "not indexed yet" one: waiting it out looks exactly like a stuck hop and
//     burns the whole HopTimeout on every single cycle.
//
// The rest (balances, bridge-address agreement, proxy health) become warnings in run mode: a soak
// run started deliberately against a partly-warm environment should say so and carry on, not
// refuse.
func (o *Orchestrator) Preflight(ctx context.Context, strict bool) (*PreflightReport, error) {
	report := &PreflightReport{Strict: strict}

	if err := o.cfg.Validate(); err != nil {
		return nil, fmt.Errorf("preflight: configuration is invalid:\n%w", err)
	}
	for _, warning := range o.cfg.Warnings() {
		report.Warnings = append(report.Warnings, "config: "+warning)
	}

	report.NetworkZeroParticipates = o.networkZeroParticipates()

	o.preflightProxy(ctx, report)
	o.preflightNetworks(ctx, report)
	o.preflightLoops(report)

	return report, nil
}

// networkZeroParticipates reports whether any enabled loop has a hop touching network 0 (L1).
func (o *Orchestrator) networkZeroParticipates() bool {
	for _, loop := range o.cfg.EnabledLoops() {
		for _, hop := range loop.Hops {
			if hop.Source == mainnetNetworkID || hop.Destination == mainnetNetworkID {
				return true
			}
		}
	}

	return false
}

// preflightProxy checks the proxy's shared HTTP server is up and that it can route network 0 at
// all - the Gap G4 fail-fast check, made once, before any hop starts polling.
func (o *Orchestrator) preflightProxy(ctx context.Context, report *PreflightReport) {
	health, err := o.proxy.Health(ctx)
	if err != nil {
		report.problem(true, fmt.Sprintf("proxy: GET /tracker/v1/health on %s failed: %v",
			o.cfg.Global.ProxyURL, err))
	} else {
		report.ProxyHealthy = true
		report.ProxyHealth = health
	}

	// Gap G4: this is a preflight, never a polling gate. ErrL1BridgeServiceUnavailable means the
	// proxy's finder has no URL for network 0 at all, which no amount of waiting fixes.
	_, err = o.proxy.BridgeAddresses(ctx, mainnetNetworkID)
	switch {
	case err == nil:
		report.L1BridgeServiceAvailable = true
	case errors.Is(err, ErrL1BridgeServiceUnavailable):
		message := fmt.Sprintf("proxy: %s has no L1 (network_id=0) bridge service configured, so no hop "+
			"through network 0 can ever be observed (DESIGN.md Gap G4): %v", o.cfg.Global.ProxyURL, err)
		if report.NetworkZeroParticipates {
			report.problem(true, message+" - an enabled loop routes through network 0, so this is a "+
				"permanent refusal, not something to retry")
		} else {
			report.problem(false, message+" - no enabled loop routes through network 0, so this is only "+
				"a warning")
		}
	default:
		report.problem(false, fmt.Sprintf("proxy: GET /bridge/v1/config?network_id=0 on %s failed: %v",
			o.cfg.Global.ProxyURL, err))
	}
}

// preflightNetworks reads, per configured network: the bridge's own network ID, its gas token and
// WETH token (Gap G3), the signer's native balance against MinNativeReserve, and the bridge
// address the proxy publishes for it.
func (o *Orchestrator) preflightNetworks(ctx context.Context, report *PreflightReport) {
	for _, cfgNetwork := range o.cfg.Networks {
		pooled, ok := o.pool.network(cfgNetwork.NetworkID)
		if !ok {
			report.problem(true, fmt.Sprintf("network %d (%s): no client was built for it",
				cfgNetwork.NetworkID, cfgNetwork.Name))
			continue
		}

		entry := NetworkPreflight{
			NetworkID:            cfgNetwork.NetworkID,
			Name:                 cfgNetwork.Name,
			ChainID:              pooled.Client.ChainID().Uint64(),
			From:                 pooled.Client.From(),
			MinNativeReserve:     cfgNetwork.MinNativeReserve.BigInt(),
			BridgeAddrConfigured: cfgNetwork.BridgeAddr,
		}

		o.preflightBridgeIdentity(ctx, pooled, &entry, report)
		o.preflightGasToken(ctx, pooled, &entry, report)
		o.preflightBalance(ctx, pooled, &entry, report)
		o.preflightProxyBridgeAddress(ctx, cfgNetwork, &entry, report)

		report.Networks = append(report.Networks, entry)
	}
}

// preflightBridgeIdentity confirms the bridge contract at the configured address reports the
// configured network ID - the cheapest way to catch a bridge address pasted from the wrong chain.
func (o *Orchestrator) preflightBridgeIdentity(
	ctx context.Context, pooled *pooledNetwork, entry *NetworkPreflight, report *PreflightReport,
) {
	bridgeNetworkID, err := pooled.Bridge.NetworkID(ctx)
	if err != nil {
		report.problem(true, fmt.Sprintf("network %d (%s): read networkID() from the bridge at %s: %v",
			entry.NetworkID, entry.Name, entry.BridgeAddrConfigured, err))
		return
	}

	entry.BridgeNetworkID = bridgeNetworkID
	if bridgeNetworkID != entry.NetworkID {
		report.problem(true, fmt.Sprintf("network %d (%s): the bridge at %s reports networkID() = %d, "+
			"so BridgeAddr points at a different network's bridge",
			entry.NetworkID, entry.Name, entry.BridgeAddrConfigured, bridgeNetworkID))
	}
}

// preflightGasToken performs the DESIGN.md Gap G3 check: read gasTokenAddress() and WETHToken()
// live rather than trusting the static conclusion that the gas token is ether.
func (o *Orchestrator) preflightGasToken(
	ctx context.Context, pooled *pooledNetwork, entry *NetworkPreflight, report *PreflightReport,
) {
	gasToken, err := pooled.Bridge.GasTokenAddress(ctx)
	if err != nil {
		report.problem(true, fmt.Sprintf("network %d (%s): read gasTokenAddress() from the bridge at %s: %v",
			entry.NetworkID, entry.Name, entry.BridgeAddrConfigured, err))
		return
	}
	entry.GasTokenAddress = gasToken
	entry.GasTokenIsEther = gasToken == (common.Address{})

	wethToken, err := pooled.Bridge.WETHToken(ctx)
	if err != nil {
		// A bridge whose gas token is ether has no WETH token and some deployments revert rather
		// than returning the zero address, so this is never a refusal on its own.
		entry.Notes = append(entry.Notes, fmt.Sprintf("WETHToken() could not be read: %v", err))
	} else {
		entry.WETHToken = wethToken
	}

	if !entry.GasTokenIsEther {
		entry.Notes = append(entry.Notes, fmt.Sprintf(
			"gas token is %s (not ether), so the native asset would have to be bridged through WETH %s - "+
				"a path this tool does not implement", gasToken, entry.WETHToken))
	}
}

// preflightBalance checks the signer can actually pay for gas on this network.
func (o *Orchestrator) preflightBalance(
	ctx context.Context, pooled *pooledNetwork, entry *NetworkPreflight, report *PreflightReport,
) {
	balance, err := pooled.Client.NativeBalance(ctx, pooled.Client.From())
	if err != nil {
		report.problem(true, fmt.Sprintf("network %d (%s): read the native balance of %s: %v",
			entry.NetworkID, entry.Name, entry.From, err))
		return
	}
	entry.NativeBalance = balance

	if balance.Cmp(entry.MinNativeReserve) < 0 {
		report.problem(report.Strict, fmt.Sprintf("network %d (%s): signer %s holds %s wei, below its "+
			"configured MinNativeReserve of %s wei, so it cannot pay for gas without breaching the reserve",
			entry.NetworkID, entry.Name, entry.From, balance, entry.MinNativeReserve))
	}
}

// preflightProxyBridgeAddress compares the bridge address the proxy publishes for a network with
// the configured one: a disagreement means the tool and the observation layer are watching
// different contracts, which would make every readiness gate stall for reasons nothing explains.
func (o *Orchestrator) preflightProxyBridgeAddress(
	ctx context.Context, cfgNetwork Network, entry *NetworkPreflight, report *PreflightReport,
) {
	config, err := o.proxy.BridgeAddresses(ctx, cfgNetwork.NetworkID)
	if err != nil {
		severity := report.Strict
		if errors.Is(err, bridgeserviceclient.ErrNotFound) && cfgNetwork.NetworkID != mainnetNetworkID {
			// An L2's bridge service can legitimately not be enumerated by the proxy's finder yet
			// shortly after startup (see Proxy.BridgeAddresses).
			entry.Notes = append(entry.Notes, "the proxy could not route GET /bridge/v1/config for this "+
				"network yet (it may still be discovering it)")
		}
		report.problem(severity, fmt.Sprintf("network %d (%s): GET /bridge/v1/config?network_id=%d: %v",
			cfgNetwork.NetworkID, cfgNetwork.Name, cfgNetwork.NetworkID, err))
		return
	}

	entry.ProxyReachable = true
	entry.BridgeAddrReported = reportedBridgeAddress(cfgNetwork.NetworkID, config)
	if entry.BridgeAddrReported == (common.Address{}) {
		entry.Notes = append(entry.Notes, "the proxy published no bridge address for this network")
		return
	}
	if entry.BridgeAddrReported != cfgNetwork.BridgeAddr {
		report.problem(report.Strict, fmt.Sprintf("network %d (%s): configured BridgeAddr %s disagrees with "+
			"the %s the proxy publishes for it, so the tool and the observation layer would be watching "+
			"different contracts",
			cfgNetwork.NetworkID, cfgNetwork.Name, cfgNetwork.BridgeAddr, entry.BridgeAddrReported))
	}
}

// reportedBridgeAddress picks the bridge address a /bridge/v1/config response publishes for a
// network: the L1 section for network 0, the L2 section otherwise.
func reportedBridgeAddress(
	networkID uint32, config *bridgeservicetypes.PublicConfigResponse,
) common.Address {
	if config == nil {
		return common.Address{}
	}
	if networkID == mainnetNetworkID {
		return common.HexToAddress(string(config.Contracts.L1.BridgeAddr))
	}

	return common.HexToAddress(string(config.Contracts.L2.BridgeAddr))
}

// preflightLoops applies the per-loop refusals that depend on what the networks reported: today,
// the Gap G3 rule that an "eth" loop may only touch networks whose gas token is ether.
func (o *Orchestrator) preflightLoops(report *PreflightReport) {
	for _, loop := range o.cfg.EnabledLoops() {
		if loop.Asset != AssetETH {
			continue
		}

		var offenders []string
		for _, networkID := range loopNetworks(loop) {
			entry := report.Network(networkID)
			if entry == nil || entry.GasTokenIsEther {
				continue
			}
			offenders = append(offenders, fmt.Sprintf("%d (%s, gas token %s)",
				entry.NetworkID, entry.Name, entry.GasTokenAddress))
		}
		if len(offenders) > 0 {
			report.problem(true, fmt.Sprintf("loop %q is configured as Asset = %q but its route touches "+
				"network(s) %s whose gasTokenAddress() is not the zero address; bridging the native asset "+
				"there requires the WETH path, which this tool does not implement (DESIGN.md §6/Gap G3): %s",
				loop.Name, AssetETH, strings.Join(offenders, ", "), ErrNativeAssetUnsupported))
		}
	}
}

// loopNetworks returns every network ID a loop's route touches, in first-seen order.
func loopNetworks(loop Loop) []uint32 {
	seen := map[uint32]bool{}
	var networks []uint32
	for _, hop := range loop.Hops {
		for _, networkID := range []uint32{hop.Source, hop.Destination} {
			if !seen[networkID] {
				seen[networkID] = true
				networks = append(networks, networkID)
			}
		}
	}

	return networks
}

// problem records a finding as a refusal when fatal is true and as a warning otherwise.
func (p *PreflightReport) problem(fatal bool, message string) {
	if fatal {
		p.Errors = append(p.Errors, message)
		return
	}
	p.Warnings = append(p.Warnings, message)
}
