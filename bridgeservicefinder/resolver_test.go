package bridgeservicefinder

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/agglayer/aggkit/bridgeservicefinder/mocks"
	"github.com/stretchr/testify/require"
)

// TestResolver_ConfigWins verifies that a configured URL short-circuits resolution without ever
// touching the reader.
func TestResolver_ConfigWins(t *testing.T) {
	res := newResolver(map[uint32]string{7: "https://config.example.com:5577"}, DefaultBridgeServicePort)

	url, source, err := res.resolve(context.Background(), 7, nil)
	require.NoError(t, err)
	require.Equal(t, "https://config.example.com:5577", url)
	require.Equal(t, SourceConfig, source)
}

// TestResolver_MetadataWinsOverSequencer verifies metadata is preferred over the sequencer URL when
// both are available, and the reader's TrustedSequencerURL is never even called.
func TestResolver_MetadataWinsOverSequencer(t *testing.T) {
	res := newResolver(nil, DefaultBridgeServicePort)
	reader := mocks.NewRollupContractReader(t)

	reader.EXPECT().AggchainMetadata(context.Background(), MetadataBridgeServiceURLKey).
		Return("https://metadata.example.com:5577", nil)

	url, source, err := res.resolve(context.Background(), 1, reader)
	require.NoError(t, err)
	require.Equal(t, "https://metadata.example.com:5577", url)
	require.Equal(t, SourceMetadata, source)
}

// TestResolver_MetadataFallsThroughToSequencer verifies ErrSourceNotAvailable (revert / method
// absent) from AggchainMetadata falls through to the sequencer source, and that the configured
// bridgeServicePort (here a non-default value, to prove it is honoured rather than hardcoded) is
// substituted into the sequencer URL.
func TestResolver_MetadataFallsThroughToSequencer(t *testing.T) {
	const customPort = 6001
	res := newResolver(nil, customPort)
	reader := mocks.NewRollupContractReader(t)

	reader.EXPECT().AggchainMetadata(context.Background(), MetadataBridgeServiceURLKey).
		Return("", ErrSourceNotAvailable)
	reader.EXPECT().TrustedSequencerURL(context.Background()).
		Return("https://seq.example.com:8545", nil)

	url, source, err := res.resolve(context.Background(), 1, reader)
	require.NoError(t, err)
	require.Contains(t, url, "seq.example.com")
	require.Contains(t, url, fmt.Sprintf(":%d", customPort))
	require.Equal(t, SourceSequencerURL, source)
}

// TestResolver_EmptyMetadataFallsThroughToSequencer verifies an empty (unset key) metadata value
// with a nil error also falls through, per the documented "unset key" behaviour.
func TestResolver_EmptyMetadataFallsThroughToSequencer(t *testing.T) {
	res := newResolver(nil, DefaultBridgeServicePort)
	reader := mocks.NewRollupContractReader(t)

	reader.EXPECT().AggchainMetadata(context.Background(), MetadataBridgeServiceURLKey).
		Return("", nil)
	reader.EXPECT().TrustedSequencerURL(context.Background()).
		Return("https://seq.example.com:8545", nil)

	url, source, err := res.resolve(context.Background(), 1, reader)
	require.NoError(t, err)
	require.Contains(t, url, "seq.example.com")
	require.Equal(t, SourceSequencerURL, source)
}

// TestResolver_SequencerFallsThroughToNoSource verifies that when both sources are unavailable, the
// resolver returns ErrNoSourceAvailable.
func TestResolver_SequencerFallsThroughToNoSource(t *testing.T) {
	res := newResolver(nil, DefaultBridgeServicePort)
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
	res := newResolver(nil, DefaultBridgeServicePort)

	_, source, err := res.resolve(context.Background(), 0, nil)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrNoSourceAvailable)
	require.Equal(t, SourceConfig, source)
}

// TestResolver_MetadataHardErrorAborts verifies a genuine (non-fall-through) error from
// AggchainMetadata aborts resolution rather than falling through to the sequencer source.
func TestResolver_MetadataHardErrorAborts(t *testing.T) {
	res := newResolver(nil, DefaultBridgeServicePort)
	reader := mocks.NewRollupContractReader(t)

	wantErr := errors.New("transport failure")
	reader.EXPECT().AggchainMetadata(context.Background(), MetadataBridgeServiceURLKey).
		Return("", wantErr)

	_, _, err := res.resolve(context.Background(), 1, reader)
	require.Error(t, err)
	require.ErrorIs(t, err, wantErr)
}

// TestResolver_SequencerHardErrorAborts verifies a genuine (non-fall-through) error from
// TrustedSequencerURL aborts resolution with the wrapped error rather than ErrNoSourceAvailable.
func TestResolver_SequencerHardErrorAborts(t *testing.T) {
	res := newResolver(nil, DefaultBridgeServicePort)
	reader := mocks.NewRollupContractReader(t)

	wantErr := errors.New("transport failure")
	reader.EXPECT().AggchainMetadata(context.Background(), MetadataBridgeServiceURLKey).
		Return("", ErrSourceNotAvailable)
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
