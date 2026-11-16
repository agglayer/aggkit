package claimsync

import (
	"testing"

	aggsync "github.com/agglayer/aggkit/sync"
	"github.com/stretchr/testify/require"
)

// The multidownloader-backed constructor installs the enricher on the real downloader, so the
// concrete EVMDownloader must expose the optional hook asserted by installClaimLogsEnricher.
var _ claimLogsEnricherInstaller = (*aggsync.EVMDownloader)(nil)

// fakeLogsEnricherInstaller mimics the optional downloader enrichment hook. The supported flag is
// configurable so both the happy path and the fail-closed path can be exercised, and installs
// records how many times the hook was requested.
type fakeLogsEnricherInstaller struct {
	supported bool
	installs  int
	installed aggsync.LogsEnricherFunc
}

func (f *fakeLogsEnricherInstaller) InstallLogsEnricher(
	enricher aggsync.LogsEnricherFunc,
) (aggsync.LogsEnricherFunc, bool) {
	f.installs++
	if !f.supported {
		return nil, false
	}
	prev := f.installed
	f.installed = enricher
	return prev, true
}

// TestInstallClaimLogsEnricher covers the fail-closed contract of the claim enricher install: the
// constructor must abort when the downloader does not report the hook as installed, instead of
// starting a syncer whose handlers can never resolve a prefetched trace.
func TestInstallClaimLogsEnricher(t *testing.T) {
	t.Parallel()

	resolver := &TraceResolver{}

	t.Run("supported first install reports installed", func(t *testing.T) {
		t.Parallel()
		installer := &fakeLogsEnricherInstaller{supported: true}

		require.NoError(t, installClaimLogsEnricher(installer, resolver))
		require.Equal(t, 1, installer.installs)
		require.NotNil(t, installer.installed,
			"the first install must set the enricher even though the previous enricher is nil")
	})

	t.Run("unsupported implementation fails closed", func(t *testing.T) {
		t.Parallel()
		installer := &fakeLogsEnricherInstaller{supported: false}

		err := installClaimLogsEnricher(installer, resolver)
		require.ErrorIs(t, err, ErrLogsEnricherUnsupported)
		require.ErrorContains(t, err, "does not support the logs enricher")
		require.Equal(t, 1, installer.installs, "the hook must be requested exactly once")
		require.Nil(t, installer.installed, "nothing may be installed when the hook is unsupported")
	})

	t.Run("target without the hook fails closed", func(t *testing.T) {
		t.Parallel()

		err := installClaimLogsEnricher(struct{}{}, resolver)
		require.ErrorIs(t, err, ErrLogsEnricherUnsupported)
		require.ErrorContains(t, err, "does not support the logs enricher")
	})

	t.Run("nil resolver needs no install", func(t *testing.T) {
		t.Parallel()
		installer := &fakeLogsEnricherInstaller{supported: true}

		require.NoError(t, installClaimLogsEnricher(installer, nil))
		require.Zero(t, installer.installs, "the legacy/L2 path must not install the enricher")
	})
}
