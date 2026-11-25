package runarchive_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agglayer/aggkit/tools/exit_certificate/runarchive"
	"github.com/stretchr/testify/require"
)

func TestRunParts(t *testing.T) {
	base := runarchive.Location{Bucket: "let-archive", Prefix: "runs"}
	run := base.Run(1, 42)
	gotBase, networkID, height, ok := run.RunParts()
	require.True(t, ok)
	require.Equal(t, base, gotBase)
	require.Equal(t, uint32(1), networkID)
	require.Equal(t, uint64(42), height)

	// A deeper base and the largest values round-trip too.
	deep := runarchive.Location{Bucket: "b", Prefix: "a/b/c"}
	gotBase, networkID, height, ok = deep.Run(4294967295, 9999999999).RunParts()
	require.True(t, ok)
	require.Equal(t, deep, gotBase)
	require.Equal(t, uint32(4294967295), networkID)
	require.Equal(t, uint64(9999999999), height)

	for _, prefix := range []string{
		"runs",                       // the base
		"runs/1",                     // base + network
		"1/0000000042",               // no base prefix before the network
		"runs/1/42",                  // height not padded to 10 digits
		"runs/1/00000000042",         // 11 digits
		"runs/x/0000000042",          // network is not a number
		"runs/01/0000000042",         // network with a leading zero (Run never writes it)
		"runs/4294967296/0000000042", // network out of range
		"runs/1/000000004a",          // height is not a number
		"runs/2026-11-24-test",       // a free-form prefix
	} {
		_, _, _, ok := runarchive.Location{Bucket: "b", Prefix: prefix}.RunParts()
		require.False(t, ok, prefix)
	}
}

func TestVersionRange(t *testing.T) {
	r := runarchive.VersionRange{Min: 1, Max: 2}
	require.False(t, r.Supports(0))
	require.True(t, r.Supports(1))
	require.True(t, r.Supports(2))
	require.False(t, r.Supports(3))
	require.Equal(t, "1 to 2", r.String())
	require.Equal(t, "1", runarchive.VersionRange{Min: 1, Max: 1}.String())
}

func TestUploadRecordsDataVersion(t *testing.T) {
	objs := &objects{}
	dir := runDir(t)
	files, err := runarchive.CollectFiles(dir, nil)
	require.NoError(t, err)
	res, err := runarchive.Upload(context.Background(), newStore(t, objs), testLoc, dir, files, 3, nil, t.TempDir())
	require.NoError(t, err)
	require.Equal(t, 3, res.Manifest.DataVersion)

	raw, ok := objs.Get(testLoc.ObjectName(runarchive.ManifestFile))
	require.True(t, ok)
	var stored map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &stored))
	require.JSONEq(t, "3", string(stored["dataVersion"]))
	require.JSONEq(t, "1", string(stored["version"]), "the manifest version is a separate field")

	m, err := runarchive.ReadManifest(context.Background(), newStore(t, objs), testLoc)
	require.NoError(t, err)
	require.Equal(t, 3, m.DataVersion)
	require.NoError(t, runarchive.CheckDataVersion(m, runarchive.VersionRange{Min: 1, Max: 3}))
	err = runarchive.CheckDataVersion(m, runarchive.VersionRange{Min: 1, Max: 2})
	require.ErrorIs(t, err, runarchive.ErrUnsupportedVersion)
	require.ErrorContains(t, err, "data version 3")

	_, err = runarchive.Upload(context.Background(), newStore(t, &objects{}), testLoc, dir, files, 0, nil, t.TempDir())
	require.ErrorContains(t, err, "invalid data version")
}

// A manifest written before dataVersion existed is read as data version 1.
func TestManifestWithoutDataVersion(t *testing.T) {
	objs := archived(t)
	name := testLoc.ObjectName(runarchive.ManifestFile)
	raw, _ := objs.Get(name)
	require.Contains(t, string(raw), `"dataVersion": 1`)
	legacy := strings.Replace(string(raw), `  "dataVersion": 1,`+"\n", "", 1)
	require.NotContains(t, legacy, "dataVersion")
	objs.Data[name] = []byte(legacy)

	m, err := runarchive.ReadManifest(context.Background(), newStore(t, objs), testLoc)
	require.NoError(t, err)
	require.Equal(t, runarchive.DataVersionLegacy, m.DataVersion)
	require.Equal(t, 1, m.DataVersion)

	dest := t.TempDir()
	res, err := runarchive.Download(context.Background(), newStore(t, objs), testLoc, dest, nil)
	require.NoError(t, err)
	require.Equal(t, 1, res.Manifest.DataVersion)
	require.NoError(t, runarchive.CheckDataVersion(res.Manifest, runarchive.VersionRange{Min: 1, Max: 1}))
}

func TestReadManifest(t *testing.T) {
	ctx := context.Background()

	// Nothing at the prefix, or an upload that did not complete (files but no manifest): incomplete.
	_, err := runarchive.ReadManifest(ctx, newStore(t, &objects{}), testLoc)
	require.ErrorIs(t, err, runarchive.ErrIncompleteRun)
	partial := archived(t)
	delete(partial.Data, testLoc.ObjectName(runarchive.ManifestFile))
	require.NotEmpty(t, partial.Data)
	_, err = runarchive.ReadManifest(ctx, newStore(t, partial), testLoc)
	require.ErrorIs(t, err, runarchive.ErrIncompleteRun)

	// An unknown manifest version is told apart from a corrupted manifest, whatever else it holds.
	for name, tc := range map[string]struct {
		manifest    string
		unsupported bool
	}{
		"newer version":                 {`{"version":2,"compression":"zstd","files":[]}`, true},
		"newer version, unknown fields": {`{"version":2,"layout":"new","files":{}}`, true},
		"corrupted":                     {`{"version":1,"compre`, false},
		"no version":                    {`{"compression":"zstd","files":[]}`, false},
		"unknown field in this version": {`{"version":1,"compression":"zstd","files":[],"extra":1}`, false},
		"negative data version":         {`{"version":1,"dataVersion":-1,"compression":"zstd","files":[]}`, false},
		"unknown compression":           {`{"version":1,"compression":"gzip","files":[]}`, false},
	} {
		t.Run(name, func(t *testing.T) {
			objs := &objects{Data: map[string][]byte{testLoc.ObjectName(runarchive.ManifestFile): []byte(tc.manifest)}}
			_, err := runarchive.ReadManifest(ctx, newStore(t, objs), testLoc)
			require.Error(t, err)
			require.Equal(t, tc.unsupported, errors.Is(err, runarchive.ErrUnsupportedVersion), err)
			require.NotErrorIs(t, err, runarchive.ErrIncompleteRun)
		})
	}
}

// A run its reader rejects (unsupported data version) leaves nothing in the local directory.
func TestDownloadRejectedBySelectLeavesNoDir(t *testing.T) {
	objs := archived(t)
	dest := filepath.Join(t.TempDir(), "cache")
	_, err := runarchive.Download(context.Background(), newStore(t, objs), testLoc, dest,
		func(m *runarchive.Manifest) ([]string, error) {
			return nil, runarchive.CheckDataVersion(m, runarchive.VersionRange{Min: 2, Max: 2})
		})
	require.ErrorIs(t, err, runarchive.ErrUnsupportedVersion)
	_, statErr := os.Stat(dest)
	require.ErrorIs(t, statErr, os.ErrNotExist)

	// A manifest of an unknown version is refused by Download as unsupported too.
	objs.Data[testLoc.ObjectName(runarchive.ManifestFile)] = []byte(`{"version":9}`)
	_, err = runarchive.Download(context.Background(), newStore(t, objs), testLoc, dest, nil)
	require.ErrorIs(t, err, runarchive.ErrUnsupportedVersion)
}
