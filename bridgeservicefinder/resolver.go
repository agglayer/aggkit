package bridgeservicefinder

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
)

// resolver applies the strict three-source priority algorithm (config -> aggchainMetadata ->
// trustedSequencerURL+port) for a single network's bridge URL, plus the two-source (config ->
// trustedSequencerURL) rule for its JSON-RPC endpoint. It is stateless with respect to the cache;
// it only knows the config overrides and how to talk to a rollup contract.
type resolver struct {
	// configBridgeURLs is the SourceConfig static override map (Config.BridgeURLs). Highest priority.
	configBridgeURLs map[uint32]string
	// configRPCURLs is the static JSON-RPC override map (Config.RPCURLs). A network present here gets
	// its JSON-RPC endpoint verbatim from config and never from (or refreshed by) the on-chain
	// trustedSequencerURL.
	configRPCURLs map[uint32]string
	// bridgeServicePort is the port substituted into a trusted-sequencer URL for source #3.
	bridgeServicePort int
}

// newResolver builds a resolver from the config override maps and the bridge service port.
func newResolver(configBridgeURLs, configRPCURLs map[uint32]string, bridgeServicePort int) *resolver {
	return &resolver{
		configBridgeURLs:  configBridgeURLs,
		configRPCURLs:     configRPCURLs,
		bridgeServicePort: bridgeServicePort,
	}
}

// configRPCURL returns the config JSON-RPC override for networkID ("" if none).
func (r *resolver) configRPCURL(networkID uint32) string {
	return r.configRPCURLs[networkID]
}

// resolve returns the network's URLs (bridge service + JSON-RPC) and the Source that produced the
// bridge URL for the given networkID, consulting the bridge sources in strict descending priority:
//
//  1. SourceConfig       - Config.BridgeURLs[networkID], if present.
//  2. SourceMetadata     - reader.AggchainMetadata(MetadataBridgeServiceURLKey), if non-empty.
//  3. SourceSequencerURL - reader.TrustedSequencerURL() with its port replaced by bridgeServicePort.
//
// The JSON-RPC URL follows the same config-first rule: Config.RPCURLs[networkID] wins verbatim if
// present; otherwise it is the raw trustedSequencerURL (no port substitution), regardless of which
// source produced the bridge URL, and empty when that read is unavailable.
//
// A bridge source is skipped (fall-through) when it returns ErrSourceNotAvailable (method absent /
// call reverted) or an empty value. If none of the sources yields a URL, ErrNoSourceAvailable is
// returned. reader may be nil (e.g. network 0 / L1), in which case only the config sources are
// consulted.
func (r *resolver) resolve(
	ctx context.Context, networkID uint32, reader RollupContractReader,
) (NetworkURLs, Source, error) {
	rpcOverride := r.configRPCURL(networkID)

	if u, ok := r.configBridgeURLs[networkID]; ok && u != "" {
		jsonRPCURL := rpcOverride
		if jsonRPCURL == "" {
			jsonRPCURL = r.bestEffortJSONRPCURL(ctx, reader)
		}

		return NetworkURLs{BridgeURL: u, JSONRPCURL: jsonRPCURL}, SourceConfig, nil
	}

	if reader == nil {
		return NetworkURLs{}, SourceConfig, ErrNoSourceAvailable
	}

	// The trusted sequencer URL is read up front because it serves double duty: verbatim it is the
	// network's JSON-RPC endpoint (unless overridden by config), and with the bridge port substituted
	// it is bridge source #3.
	seqURL, err := reader.TrustedSequencerURL(ctx)
	if err != nil {
		if !errors.Is(err, ErrSourceNotAvailable) {
			return NetworkURLs{}, SourceSequencerURL,
				fmt.Errorf("failed to read trusted sequencer url for network %d: %w", networkID, err)
		}

		seqURL = ""
	}

	jsonRPCURL := rpcOverride
	if jsonRPCURL == "" {
		jsonRPCURL = seqURL
	}

	if u, err := reader.AggchainMetadata(ctx, MetadataBridgeServiceURLKey); err == nil && u != "" {
		return NetworkURLs{BridgeURL: u, JSONRPCURL: jsonRPCURL}, SourceMetadata, nil
	} else if err != nil && !errors.Is(err, ErrSourceNotAvailable) {
		return NetworkURLs{}, SourceMetadata,
			fmt.Errorf("failed to read aggchain metadata for network %d: %w", networkID, err)
	}

	if seqURL != "" {
		u, err := withPort(seqURL, r.bridgeServicePort)
		if err != nil {
			return NetworkURLs{}, SourceSequencerURL,
				fmt.Errorf("failed to substitute port in trusted sequencer url for network %d: %w", networkID, err)
		}

		return NetworkURLs{BridgeURL: u, JSONRPCURL: jsonRPCURL}, SourceSequencerURL, nil
	}

	return NetworkURLs{}, SourceSequencerURL, ErrNoSourceAvailable
}

// bestEffortJSONRPCURL reads trustedSequencerURL as the network's JSON-RPC endpoint for a network
// whose bridge URL is already settled by config (and whose JSON-RPC is not itself overridden).
// Errors are swallowed: the config override must keep working even when the on-chain read is
// unavailable, at the cost of an empty JSON-RPC URL.
func (r *resolver) bestEffortJSONRPCURL(ctx context.Context, reader RollupContractReader) string {
	if reader == nil {
		return ""
	}

	u, err := reader.TrustedSequencerURL(ctx)
	if err != nil {
		return ""
	}

	return u
}

// withPort returns rawURL with its port replaced by port, preserving scheme, host, path and the
// rest of the URL. If rawURL has no scheme it is assumed to be host[:port] and defaults to http.
func withPort(rawURL string, port int) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse url %q: %w", rawURL, err)
	}

	// A URL without a scheme (e.g. "seq.example.com:8123") is parsed with an empty Host and the
	// whole value in Path/Opaque. Re-parse it with an explicit scheme so Host is populated.
	if u.Host == "" {
		u, err = url.Parse("http://" + rawURL)
		if err != nil {
			return "", fmt.Errorf("parse schemeless url %q: %w", rawURL, err)
		}
	}

	u.Host = net.JoinHostPort(u.Hostname(), strconv.Itoa(port))

	return u.String(), nil
}
