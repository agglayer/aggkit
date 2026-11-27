package exit_certificate

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/agglayer/mocks"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/tools/exit_certificate/runarchive"
	"github.com/agglayer/aggkit/tools/exit_certificate/runarchive/archivetest"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// archivePreviousRun archives a settled run of network 7 at the given height under testArchiveURI and
// returns the objects, the run location and the run's new local exit root.
func archivePreviousRun(t *testing.T, objs *archivetest.Objects, height uint64) (runarchive.Location, common.Hash) {
	t.Helper()
	// A different number of leaves per height, so every run has its own local exit root.
	dir, root := writePreviousRun(t, fileSignedCertificate, int(height), nil)
	var cert agglayertypes.Certificate
	require.NoError(t, loadJSON(dir, fileSignedCertificate, &cert))
	cert.Height = height
	require.NoError(t, saveJSON(dir, fileSignedCertificate, &cert))
	cfg := &Config{L2NetworkID: deltaTestNetworkID, Options: Options{
		OutputDir: dir, ArchiveURI: testArchiveURI, ArchiveCacheDir: t.TempDir(),
	}}
	require.NoError(t, runUpload(context.Background(), cfg, archivetest.Opener(t, objs)))
	base, err := runarchive.ParseURI(testArchiveURI)
	require.NoError(t, err)
	return base.Run(deltaTestNetworkID, height), root
}

// settledAt returns an agglayer opener whose network info says the last settled certificate has that
// height and local exit root.
func settledAt(t *testing.T, height uint64, ler common.Hash) agglayerClientOpener {
	t.Helper()
	return func(*Config) (agglayer.AgglayerClientInterface, error) {
		client := mocks.NewAgglayerClientMock(t)
		client.EXPECT().GetNetworkInfo(mock.Anything, deltaTestNetworkID).Return(agglayertypes.NetworkInfo{
			SettledLER: &ler, SettledHeight: &height,
		}, nil)
		return client, nil
	}
}

// noAgglayer fails the test if the agglayer is asked: an explicit run needs no lookup.
func noAgglayer(t *testing.T) agglayerClientOpener {
	t.Helper()
	return func(*Config) (agglayer.AgglayerClientInterface, error) {
		t.Error("the agglayer must not be queried for an explicit run")
		return nil, errors.New("unexpected")
	}
}

func deltaConfig(t *testing.T, previousRun string) *Config {
	t.Helper()
	return &Config{L2NetworkID: deltaTestNetworkID, Options: Options{
		DeltaMode: true, PreviousRun: previousRun, OutputDir: t.TempDir(), ArchiveCacheDir: t.TempDir(),
	}}
}

// editManifest rewrites the archived manifest of loc.
func editManifest(t *testing.T, objs *archivetest.Objects, loc runarchive.Location, edit func(string) string) {
	t.Helper()
	name := loc.ObjectName(runarchive.ManifestFile)
	raw, ok := objs.Get(name)
	require.True(t, ok)
	edited := edit(string(raw))
	require.NotEqual(t, string(raw), edited, "the edit must change the manifest")
	objs.Data[name] = []byte(edited)
}

func TestResolvePreviousRunFromArchiveBase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("reads the run of the last settled height", func(t *testing.T) {
		t.Parallel()
		objs := &archivetest.Objects{}
		archivePreviousRun(t, objs, 3)
		latest, latestRoot := archivePreviousRun(t, objs, 4)
		cfg := deltaConfig(t, testArchiveURI)

		require.NoError(t, resolvePreviousRun(ctx, cfg, archivetest.Opener(t, objs), settledAt(t, 4, latestRoot)))
		require.Equal(t, runarchive.CacheDir(cfg.Options.ArchiveCacheDir, latest), cfg.Options.PreviousRun)
		// The chosen run (not the base) is what ends up in delta-manifest.json (previousRunArchive).
		require.Equal(t, testArchiveURI+"/7/0000000004", cfg.Options.PreviousRunArchive)
		require.NotEmpty(t, cfg.Options.PreviousRunArchiveManifestSHA256)

		prev, err := loadPreviousRun(cfg.Options.PreviousRun)
		require.NoError(t, err)
		require.Equal(t, uint64(4), prev.Certificate.Height)
		require.Equal(t, latestRoot, prev.Certificate.NewLocalExitRoot)
		// The existing settled_ler / height check still holds on what was loaded.
		require.NoError(t, verifyChainsOnPreviousRun(prev, latestRoot, 5))
	})

	t.Run("a trailing slash on the base is the base too", func(t *testing.T) {
		t.Parallel()
		objs := &archivetest.Objects{}
		_, root := archivePreviousRun(t, objs, 3)
		cfg := deltaConfig(t, testArchiveURI+"/")
		require.NoError(t, resolvePreviousRun(ctx, cfg, archivetest.Opener(t, objs), settledAt(t, 3, root)))
		require.Equal(t, testRunURI, cfg.Options.PreviousRunArchive)
	})

	t.Run("the last settled run is not archived", func(t *testing.T) {
		t.Parallel()
		objs := &archivetest.Objects{}
		archivePreviousRun(t, objs, 3) // an older run is there, but chaining on it is never valid
		cfg := deltaConfig(t, testArchiveURI)
		err := resolvePreviousRun(ctx, cfg, archivetest.Opener(t, objs), settledAt(t, 4, common.HexToHash("0x4")))
		require.ErrorIs(t, err, runarchive.ErrIncompleteRun)
		require.ErrorContains(t, err, "has height 4, but its run is not archived")
		require.Equal(t, testArchiveURI, cfg.Options.PreviousRun, "left untouched on failure")
	})

	t.Run("an incomplete upload at the settled height is not a run", func(t *testing.T) {
		t.Parallel()
		objs := &archivetest.Objects{}
		loc, root := archivePreviousRun(t, objs, 3)
		delete(objs.Data, loc.ObjectName(runarchive.ManifestFile))
		cfg := deltaConfig(t, testArchiveURI)
		err := resolvePreviousRun(ctx, cfg, archivetest.Opener(t, objs), settledAt(t, 3, root))
		require.ErrorIs(t, err, runarchive.ErrIncompleteRun)
	})

	t.Run("the archived run is not the settled certificate", func(t *testing.T) {
		t.Parallel()
		objs := &archivetest.Objects{}
		archivePreviousRun(t, objs, 3)
		cfg := deltaConfig(t, testArchiveURI)
		err := resolvePreviousRun(ctx, cfg, archivetest.Opener(t, objs), settledAt(t, 3, common.HexToHash("0xbad")))
		require.ErrorContains(t, err, "it is not the run of the settled certificate")
		_, statErr := os.Stat(runarchive.CacheDir(cfg.Options.ArchiveCacheDir, mustParseURI(t, testRunURI)))
		require.ErrorIs(t, statErr, os.ErrNotExist, "nothing is downloaded")
	})

	t.Run("no settled certificate", func(t *testing.T) {
		t.Parallel()
		open := func(*Config) (agglayer.AgglayerClientInterface, error) {
			client := mocks.NewAgglayerClientMock(t)
			client.EXPECT().GetNetworkInfo(mock.Anything, deltaTestNetworkID).Return(agglayertypes.NetworkInfo{}, nil)
			return client, nil
		}
		err := resolvePreviousRun(ctx, deltaConfig(t, testArchiveURI), archivetest.Opener(t, &archivetest.Objects{}), open)
		require.ErrorContains(t, err, "has no settled certificate for network 7")
	})

	t.Run("a pending certificate", func(t *testing.T) {
		t.Parallel()
		open := func(*Config) (agglayer.AgglayerClientInterface, error) {
			client := mocks.NewAgglayerClientMock(t)
			pending := agglayertypes.Pending
			client.EXPECT().GetNetworkInfo(mock.Anything, deltaTestNetworkID).Return(agglayertypes.NetworkInfo{
				SettledHeight: ptrUint64(3), LatestPendingStatus: &pending, LatestPendingHeight: ptrUint64(4),
			}, nil)
			return client, nil
		}
		err := resolvePreviousRun(ctx, deltaConfig(t, testArchiveURI), archivetest.Opener(t, &archivetest.Objects{}), open)
		require.ErrorContains(t, err, "pending certificate")
	})

	t.Run("no agglayer configured", func(t *testing.T) {
		t.Parallel()
		err := resolvePreviousRun(ctx, deltaConfig(t, testArchiveURI), archivetest.Opener(t, &archivetest.Objects{}),
			newAgglayerClient)
		require.ErrorContains(t, err, "is an archive base")
		require.ErrorContains(t, err, "agglayerClient.grpc.url is required")
	})
}

func mustParseURI(t *testing.T, uri string) runarchive.Location {
	t.Helper()
	loc, err := runarchive.ParseURI(uri)
	require.NoError(t, err)
	return loc
}

func TestResolvePreviousRunVersions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	newerData := func(m string) string { return strings.Replace(m, `"dataVersion": 1`, `"dataVersion": 2`, 1) }
	newerManifest := func(m string) string { return strings.Replace(m, `"version": 1`, `"version": 2`, 1) }
	legacy := func(m string) string { return strings.Replace(m, `  "dataVersion": 1,`+"\n", "", 1) }

	for name, edit := range map[string]func(string) string{"data version": newerData, "manifest version": newerManifest} {
		t.Run("unsupported "+name+", archive base", func(t *testing.T) {
			t.Parallel()
			objs := &archivetest.Objects{}
			archivePreviousRun(t, objs, 2) // an older, supported run does not help
			loc, root := archivePreviousRun(t, objs, 3)
			editManifest(t, objs, loc, edit)
			cfg := deltaConfig(t, testArchiveURI)
			err := resolvePreviousRun(ctx, cfg, archivetest.Opener(t, objs), settledAt(t, 3, root))
			require.ErrorIs(t, err, runarchive.ErrUnsupportedVersion)
			require.ErrorContains(t, err, "archived by a newer exit_certificate")
			_, statErr := os.Stat(runarchive.CacheDir(cfg.Options.ArchiveCacheDir, loc))
			require.ErrorIs(t, statErr, os.ErrNotExist, "nothing is downloaded")
		})
		t.Run("unsupported "+name+", explicit run", func(t *testing.T) {
			t.Parallel()
			objs := &archivetest.Objects{}
			loc, _ := archivePreviousRun(t, objs, 3)
			editManifest(t, objs, loc, edit)
			err := resolvePreviousRun(ctx, deltaConfig(t, testRunURI), archivetest.Opener(t, objs), noAgglayer(t))
			require.ErrorIs(t, err, runarchive.ErrUnsupportedVersion)
		})
	}

	t.Run("a manifest without dataVersion is data version 1", func(t *testing.T) {
		t.Parallel()
		objs := &archivetest.Objects{}
		loc, root := archivePreviousRun(t, objs, 3)
		editManifest(t, objs, loc, legacy)
		cfg := deltaConfig(t, testArchiveURI)
		require.NoError(t, resolvePreviousRun(ctx, cfg, archivetest.Opener(t, objs), settledAt(t, 3, root)))
		require.Equal(t, testRunURI, cfg.Options.PreviousRunArchive)
	})

	t.Run("the upload records the tool's data version", func(t *testing.T) {
		t.Parallel()
		objs := &archivetest.Objects{}
		loc, _ := archivePreviousRun(t, objs, 3)
		m, err := runarchive.ReadManifest(ctx, archivetest.NewStore(t, objs), loc)
		require.NoError(t, err)
		require.Equal(t, ArchiveDataVersion, m.DataVersion)
		require.True(t, supportedArchiveDataVersions.Supports(ArchiveDataVersion))
	})
}
