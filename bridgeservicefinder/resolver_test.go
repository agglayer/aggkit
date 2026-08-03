package bridgeservicefinder

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/agglayer/aggkit/bridgeservicefinder/mocks"
	"github.com/stretchr/testify/require"
)

// TestResolver_ConfigWins verifies that a configured URL short-circuits bridge resolution. With a
// nil reader (network 0 / L1) the JSON-RPC URL stays empty.
func TestResolver_ConfigWins(t *testing.T) {
	res := newResolver(map[uint32]string{7: "https://config.example.com:5577"}, nil, DefaultBridgeServicePort)

	urls, source, err := res.resolve(context.Background(), 7, nil)
	require.NoError(t, err)
	require.Equal(t, "https://config.example.com:5577", urls.BridgeURL)
	require.Empty(t, urls.JSONRPCURL)
	require.Equal(t, SourceConfig, source)
}

// TestResolver_ConfigWithReaderFillsJSONRPC verifies that a config-sourced bridge URL is still
// enriched with the network's JSON-RPC endpoint (the raw trustedSequencerURL) when a reader is
// available, and that a failing read degrades to an empty JSON-RPC URL rather than an error.
func TestResolver_ConfigWithReaderFillsJSONRPC(t *testing.T) {
	res := newResolver(map[uint32]string{1: "https://config.example.com:5577"}, nil, DefaultBridgeServicePort)

	t.Run("sequencer url available", func(t *testing.T) {
		reader := mocks.NewRollupContractReader(t)
		reader.EXPECT().TrustedSequencerURL(context.Background()).
			Return("https://seq.example.com:8545", nil)

		urls, source, err := res.resolve(context.Background(), 1, reader)
		require.NoError(t, err)
		require.Equal(t, "https://config.example.com:5577", urls.BridgeURL)
		require.Equal(t, "https://seq.example.com:8545", urls.JSONRPCURL)
		require.Equal(t, SourceConfig, source)
	})

	t.Run("sequencer url unavailable is best-effort", func(t *testing.T) {
		reader := mocks.NewRollupContractReader(t)
		reader.EXPECT().TrustedSequencerURL(context.Background()).
			Return("", errors.New("transport failure"))

		urls, source, err := res.resolve(context.Background(), 1, reader)
		require.NoError(t, err)
		require.Equal(t, "https://config.example.com:5577", urls.BridgeURL)
		require.Empty(t, urls.JSONRPCURL)
		require.Equal(t, SourceConfig, source)
	})
}

// TestResolver_ConfigRPCOverrideWins verifies Config.RPCURLs is served verbatim as the JSON-RPC
// endpoint, regardless of which source produces the bridge URL, and that with both overrides
// present the reader is never touched.
func TestResolver_ConfigRPCOverrideWins(t *testing.T) {
	const rpcOverride = "https://rpc-override.example.com:8545"

	t.Run("both bridge and rpc from config: reader untouched", func(t *testing.T) {
		res := newResolver(
			map[uint32]string{1: "https://config.example.com:5577"},
			map[uint32]string{1: rpcOverride},
			DefaultBridgeServicePort)
		reader := mocks.NewRollupContractReader(t) // no expectations: any call fails the test

		urls, source, err := res.resolve(context.Background(), 1, reader)
		require.NoError(t, err)
		require.Equal(t, "https://config.example.com:5577", urls.BridgeURL)
		require.Equal(t, rpcOverride, urls.JSONRPCURL)
		require.Equal(t, SourceConfig, source)
	})

	t.Run("rpc from config, bridge from metadata", func(t *testing.T) {
		res := newResolver(nil, map[uint32]string{1: rpcOverride}, DefaultBridgeServicePort)
		reader := mocks.NewRollupContractReader(t)

		reader.EXPECT().TrustedSequencerURL(context.Background()).
			Return("https://seq.example.com:8545", nil)
		reader.EXPECT().AggchainMetadata(context.Background(), MetadataBridgeServiceURLKey).
			Return("https://metadata.example.com:5577", nil)

		urls, source, err := res.resolve(context.Background(), 1, reader)
		require.NoError(t, err)
		require.Equal(t, "https://metadata.example.com:5577", urls.BridgeURL)
		require.Equal(t, rpcOverride, urls.JSONRPCURL,
			"config rpc override must beat the on-chain sequencer url")
		require.Equal(t, SourceMetadata, source)
	})

	t.Run("rpc from config, bridge from sequencer", func(t *testing.T) {
		res := newResolver(nil, map[uint32]string{1: rpcOverride}, DefaultBridgeServicePort)
		reader := mocks.NewRollupContractReader(t)

		reader.EXPECT().TrustedSequencerURL(context.Background()).
			Return("https://seq.example.com:8545", nil)
		reader.EXPECT().AggchainMetadata(context.Background(), MetadataBridgeServiceURLKey).
			Return("", ErrSourceNotAvailable)

		urls, source, err := res.resolve(context.Background(), 1, reader)
		require.NoError(t, err)
		require.Contains(t, urls.BridgeURL, fmt.Sprintf(":%d", DefaultBridgeServicePort))
		require.Equal(t, rpcOverride, urls.JSONRPCURL)
		require.Equal(t, SourceSequencerURL, source)
	})
}

// TestResolver_MetadataWinsOverSequencer verifies metadata is preferred over the sequencer URL when
// both are available for the bridge URL, while the JSON-RPC URL still carries the raw sequencer URL.
func TestResolver_MetadataWinsOverSequencer(t *testing.T) {
	res := newResolver(nil, nil, DefaultBridgeServicePort)
	reader := mocks.NewRollupContractReader(t)

	reader.EXPECT().TrustedSequencerURL(context.Background()).
		Return("https://seq.example.com:8545", nil)
	reader.EXPECT().AggchainMetadata(context.Background(), MetadataBridgeServiceURLKey).
		Return("https://metadata.example.com:5577", nil)

	urls, source, err := res.resolve(context.Background(), 1, reader)
	require.NoError(t, err)
	require.Equal(t, "https://metadata.example.com:5577", urls.BridgeURL)
	require.Equal(t, "https://seq.example.com:8545", urls.JSONRPCURL)
	require.Equal(t, SourceMetadata, source)
}

// TestResolver_MetadataFallsThroughToSequencer verifies ErrSourceNotAvailable (revert / method
// absent) from AggchainMetadata falls through to the sequencer source, and that the configured
// bridgeServicePort (here a non-default value, to prove it is honoured rather than hardcoded) is
// substituted into the sequencer URL - while the JSON-RPC URL keeps the original port.
func TestResolver_MetadataFallsThroughToSequencer(t *testing.T) {
	const customPort = 6001
	res := newResolver(nil, nil, customPort)
	reader := mocks.NewRollupContractReader(t)

	reader.EXPECT().AggchainMetadata(context.Background(), MetadataBridgeServiceURLKey).
		Return("", ErrSourceNotAvailable)
	reader.EXPECT().TrustedSequencerURL(context.Background()).
		Return("https://seq.example.com:8545", nil)

	urls, source, err := res.resolve(context.Background(), 1, reader)
	require.NoError(t, err)
	require.Contains(t, urls.BridgeURL, "seq.example.com")
	require.Contains(t, urls.BridgeURL, fmt.Sprintf(":%d", customPort))
	require.Equal(t, "https://seq.example.com:8545", urls.JSONRPCURL)
	require.Equal(t, SourceSequencerURL, source)
}

// TestResolver_EmptyMetadataFallsThroughToSequencer verifies an empty (unset key) metadata value
// with a nil error also falls through, per the documented "unset key" behaviour.
func TestResolver_EmptyMetadataFallsThroughToSequencer(t *testing.T) {
	res := newResolver(nil, nil, DefaultBridgeServicePort)
	reader := mocks.NewRollupContractReader(t)

	reader.EXPECT().AggchainMetadata(context.Background(), MetadataBridgeServiceURLKey).
		Return("", nil)
	reader.EXPECT().TrustedSequencerURL(context.Background()).
		Return("https://seq.example.com:8545", nil)

	urls, source, err := res.resolve(context.Background(), 1, reader)
	require.NoError(t, err)
	require.Contains(t, urls.BridgeURL, "seq.example.com")
	require.Equal(t, "https://seq.example.com:8545", urls.JSONRPCURL)
	require.Equal(t, SourceSequencerURL, source)
}

// TestResolver_SequencerFallsThroughToNoSource verifies that when both sources are unavailable, the
// resolver returns ErrNoSourceAvailable.
func TestResolver_SequencerFallsThroughToNoSource(t *testing.T) {
	res := newResolver(nil, nil, DefaultBridgeServicePort)
	reader := mocks.NewRollupContractReader(t)

	reader.EXPECT().AggchainMetadata(context.Background(), MetadataBridgeServiceURLKey).
		Return("", ErrSourceNotAvailable)
	reader.EXPECT().TrustedSequencerURL(context.Background()).
		Return("", ErrSourceNotAvailable)

	_, _, err := res.resolve(context.Background(), 1, reader)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrNoSourceAvailable)
}

// TestResolver_NilReaderWithoutConfigErrors verifies the documented nil-reader short-circuit
// (relevant to network 0 / L1, which the finder never calls resolve() for directly, but is still
// part of the resolver's documented contract).
func TestResolver_NilReaderWithoutConfigErrors(t *testing.T) {
	res := newResolver(nil, nil, DefaultBridgeServicePort)

	_, source, err := res.resolve(context.Background(), 0, nil)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrNoSourceAvailable)
	require.Equal(t, SourceConfig, source)
}

// TestResolver_MetadataHardErrorAborts verifies a genuine (non-fall-through) error from
// AggchainMetadata aborts resolution rather than falling through to the sequencer source.
func TestResolver_MetadataHardErrorAborts(t *testing.T) {
	res := newResolver(nil, nil, DefaultBridgeServicePort)
	reader := mocks.NewRollupContractReader(t)

	wantErr := errors.New("transport failure")
	reader.EXPECT().TrustedSequencerURL(context.Background()).
		Return("https://seq.example.com:8545", nil)
	reader.EXPECT().AggchainMetadata(context.Background(), MetadataBridgeServiceURLKey).
		Return("", wantErr)

	_, _, err := res.resolve(context.Background(), 1, reader)
	require.Error(t, err)
	require.ErrorIs(t, err, wantErr)
}

// TestResolver_SequencerHardErrorAborts verifies a genuine (non-fall-through) error from
// TrustedSequencerURL aborts resolution with the wrapped error rather than ErrNoSourceAvailable.
// The sequencer URL is read up front (it doubles as the JSON-RPC endpoint), so the metadata source
// is never consulted.
func TestResolver_SequencerHardErrorAborts(t *testing.T) {
	res := newResolver(nil, nil, DefaultBridgeServicePort)
	reader := mocks.NewRollupContractReader(t)

	wantErr := errors.New("transport failure")
	reader.EXPECT().TrustedSequencerURL(context.Background()).
		Return("", wantErr)

	_, _, err := res.resolve(context.Background(), 1, reader)
	require.Error(t, err)
	require.ErrorIs(t, err, wantErr)
}

// TestWithPort covers withPort's scheme preservation, schemeless defaulting and port replacement.
func TestWithPort(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		port    int
		want    string
		wantErr bool
	}{
		{
			name: "https with existing port replaced",
			raw:  "https://sequencer.example.com:8545/path",
			port: 5577,
			want: "https://sequencer.example.com:5577/path",
		},
		{
			name: "http with no port gets one added",
			raw:  "http://sequencer.example.com",
			port: 5577,
			want: "http://sequencer.example.com:5577",
		},
		{
			name: "schemeless defaults to http",
			raw:  "sequencer.example.com:8545",
			port: 5577,
			want: "http://sequencer.example.com:5577",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := withPort(tt.raw, tt.port)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
