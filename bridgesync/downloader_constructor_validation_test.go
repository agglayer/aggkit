package bridgesync

import (
	"testing"

	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	cfgtypes "github.com/agglayer/aggkit/config/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// invalidDownloader is a programmatic Downloader value that no constructor accepts. It can only be
// produced by library callers (the CLI/config unmarshalling rejects it first).
const invalidDownloader cfgtypes.Downloader = "invalid"

// TestBridgeConstructors_RejectInvalidDownloader verifies that every public constructor rejects an
// unsupported Downloader selector before touching the database, the network or any dependency, so a
// programmatic caller cannot silently fall back to the legacy downloader.
func TestBridgeConstructors_RejectInvalidDownloader(t *testing.T) {
	ctx := t.Context()
	bridge := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")

	t.Run("NewL1 aborts before touching nil deps", func(t *testing.T) {
		cfg := mdTestConfig(t, bridge, invalidDownloader)
		bs, err := NewL1(ctx, cfg, nil, nil, 1)
		require.Nil(t, bs)
		require.ErrorContains(t, err, "invalid Downloader")
		require.ErrorContains(t, err, `"invalid"`)
		require.NoFileExists(t, cfg.DBPath)
	})

	t.Run("NewL2 aborts before touching nil deps", func(t *testing.T) {
		cfg := mdTestConfig(t, bridge, invalidDownloader)
		bs, err := NewL2(ctx, cfg, nil, nil, 1, false, bridgesynctypes.EmptyLER)
		require.Nil(t, bs)
		require.ErrorContains(t, err, "invalid Downloader")
		require.ErrorContains(t, err, `"invalid"`)
		require.NoFileExists(t, cfg.DBPath)
	})

	t.Run("NewL1WithDownloader reports exact validation not requireMD", func(t *testing.T) {
		cfg := mdTestConfig(t, bridge, invalidDownloader)
		bs, err := NewL1WithDownloader(ctx, cfg, nil, nil, 1, &fakeMultidownloader{}, &fakeTransactionProvider{})
		require.Nil(t, bs)
		require.ErrorContains(t, err, "invalid Downloader")
		require.NotContains(t, err.Error(), "requires Downloader=multidownloader")
		require.NoFileExists(t, cfg.DBPath)
	})
}

// TestBridgeConstructors_ZeroDownloaderIsLegacy documents that the zero value keeps behaving as the
// legacy downloader: it passes the early validation and is only rejected by the selector check, never
// by the invalid-value guard. NewL1WithDownloader checks the selector before touching any dependency,
// so it can be exercised with nil deps.
func TestBridgeConstructors_ZeroDownloaderIsLegacy(t *testing.T) {
	ctx := t.Context()
	bridge := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	cfg := mdTestConfig(t, bridge, "")

	bs, err := NewL1WithDownloader(ctx, cfg, nil, nil, 1, &fakeMultidownloader{}, &fakeTransactionProvider{})
	require.Nil(t, bs)
	require.ErrorContains(t, err, "requires Downloader=multidownloader")
	require.NotContains(t, err.Error(), "invalid Downloader")
	require.NoFileExists(t, cfg.DBPath)
}
