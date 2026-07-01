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
// trustedSequencerURL+port) for a single network. It is stateless with respect to the cache; it
// only knows the config overrides and how to talk to a rollup contract.
type resolver struct {
	// configURLs is the SourceConfig static override map (Config.URLs). Highest priority.
	configURLs map[uint32]string
	// bridgeServicePort is the port substituted into a trusted-sequencer URL for source #3.
	bridgeServicePort int
}

// newResolver builds a resolver from the config override map and the bridge service port.
func newResolver(configURLs map[uint32]string, bridgeServicePort int) *resolver {
	return &resolver{
		configURLs:        configURLs,
		bridgeServicePort: bridgeServicePort,
	}
}

// resolve returns the bridge service URL and the Source that produced it for the given networkID,
// consulting the sources in strict descending priority:
//
//  1. SourceConfig       - Config.URLs[networkID], if present.
//  2. SourceMetadata     - reader.AggchainMetadata(MetadataBridgeServiceURLKey), if non-empty.
//  3. SourceSequencerURL - reader.TrustedSequencerURL() with its port replaced by bridgeServicePort.
//
// A source is skipped (fall-through) when it returns ErrSourceNotAvailable (method absent / call
// reverted) or an empty value. If none of the sources yields a URL, ErrNoSourceAvailable is
// returned. reader may be nil (e.g. network 0 / L1), in which case only SourceConfig is consulted.
func (r *resolver) resolve(
	ctx context.Context, networkID uint32, reader RollupContractReader,
) (string, Source, error) {
	if u, ok := r.configURLs[networkID]; ok && u != "" {
		return u, SourceConfig, nil
	}

	if reader == nil {
		return "", SourceConfig, ErrNoSourceAvailable
	}

	if u, err := reader.AggchainMetadata(ctx, MetadataBridgeServiceURLKey); err == nil && u != "" {
		return u, SourceMetadata, nil
	} else if err != nil && !errors.Is(err, ErrSourceNotAvailable) {
		return "", SourceMetadata, fmt.Errorf("failed to read aggchain metadata for network %d: %w", networkID, err)
	}

	if seqURL, err := reader.TrustedSequencerURL(ctx); err == nil && seqURL != "" {
		u, err := withPort(seqURL, r.bridgeServicePort)
		if err != nil {
			return "", SourceSequencerURL,
				fmt.Errorf("failed to substitute port in trusted sequencer url for network %d: %w", networkID, err)
		}

		return u, SourceSequencerURL, nil
	} else if err != nil && !errors.Is(err, ErrSourceNotAvailable) {
		return "", SourceSequencerURL,
			fmt.Errorf("failed to read trusted sequencer url for network %d: %w", networkID, err)
	}

	return "", SourceSequencerURL, ErrNoSourceAvailable
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
