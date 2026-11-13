package claimsync

import (
	"testing"

	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	cfgtypes "github.com/agglayer/aggkit/config/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// invalidDownloader is a programmatic Downloader value that no constructor accepts. It can only be
// produced by library callers (the CLI/config unmarshalling rejects it first).
const invalidDownloader cfgtypes.Downloader = "invalid"

// TestClaimConstructors_RejectInvalidDownloader verifies that every public constructor rejects an
// unsupported Downloader selector before touching the database, the network or any dependency, so a
// programmatic caller cannot silently fall back to the legacy downloader.
func TestClaimConstructors_RejectInvalidDownloader(t *testing.T) {
	ctx := t.Context()
	bridge := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")

	t.Run("NewStandaloneClaimSync aborts before touching nil deps", func(t *testing.T) {
		cfg := mdStandaloneConfig(t, bridge, invalidDownloader)
		cs, err := NewStandaloneClaimSync(ctx, cfg, nil, nil, claimsynctypes.L1ClaimSyncer, 0)
		require.Nil(t, cs)
		require.ErrorContains(t, err, "invalid Downloader")
		require.ErrorContains(t, err, `"invalid"`)
		require.NoFileExists(t, cfg.DBPath)
	})

	t.Run("NewClaimSync aborts before touching nil deps", func(t *testing.T) {
		cfg := mdStandaloneConfig(t, bridge, invalidDownloader)
		cs, err := NewClaimSync(ctx, cfg, nil, nil, 0, claimsynctypes.L1ClaimSyncer, nil)
		require.Nil(t, cs)
		require.ErrorContains(t, err, "invalid Downloader")
		require.ErrorContains(t, err, `"invalid"`)
		require.NoFileExists(t, cfg.DBPath)
	})

	t.Run("NewStandaloneClaimSyncWithDownloader reports exact validation not requireMD", func(t *testing.T) {
		cfg := mdStandaloneConfig(t, bridge, invalidDownloader)
		cs, err := NewStandaloneClaimSyncWithDownloader(
			ctx, cfg, nil, nil, claimsynctypes.L1ClaimSyncer, 0, &fakeMultidownloader{})
		require.Nil(t, cs)
		require.ErrorContains(t, err, "invalid Downloader")
		require.NotContains(t, err.Error(), "requires Downloader=multidownloader")
		require.NoFileExists(t, cfg.DBPath)
	})
}

// TestClaimConstructors_ZeroDownloaderIsLegacy documents that the zero value keeps behaving as the
// legacy downloader: it passes the early validation and is only rejected by the selector check, never
// by the invalid-value guard. NewStandaloneClaimSyncWithDownloader checks the selector before touching
// any dependency, so it can be exercised with nil deps.
func TestClaimConstructors_ZeroDownloaderIsLegacy(t *testing.T) {
	ctx := t.Context()
	bridge := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	cfg := mdStandaloneConfig(t, bridge, "")

	cs, err := NewStandaloneClaimSyncWithDownloader(
		ctx, cfg, nil, nil, claimsynctypes.L1ClaimSyncer, 0, &fakeMultidownloader{})
	require.Nil(t, cs)
	require.ErrorContains(t, err, "requires Downloader=multidownloader")
	require.NotContains(t, err.Error(), "invalid Downloader")
	require.NoFileExists(t, cfg.DBPath)
}
