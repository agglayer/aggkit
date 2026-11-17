package api

import (
	"os"
	"regexp"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// latestRevision returns the highest revision number matched by the first capture group of re in
// the file
func latestRevision(t *testing.T, file string, re *regexp.Regexp) int {
	t.Helper()

	content, err := os.ReadFile(file)
	require.NoError(t, err)

	entries := re.FindAllStringSubmatch(string(content), -1)
	require.NotEmpty(t, entries, "%s has no revision matching %s", file, re)

	latest := 0
	for _, entry := range entries {
		revision, err := strconv.Atoi(entry[1])
		require.NoError(t, err)
		latest = max(latest, revision)
	}
	return latest
}

// TestCurrentAPIRevisionHasHistoryEntry keeps CurrentAPIRevision in sync with the revision
// history of revision.go: the latest entry must be the current revision, so a bump cannot land
// without explaining the change
func TestCurrentAPIRevisionHasHistoryEntry(t *testing.T) {
	require.Equal(t, CurrentAPIRevision,
		latestRevision(t, "revision.go", regexp.MustCompile(`(?m)^//   - (\d+):`)),
		"CurrentAPIRevision changed without a matching entry in the revision history of revision.go")
}

// TestAPIDocSampleMatchesCurrentAPIRevision keeps the "api_revision" sample of the Health example
// in docs/bridgetracker/API.md equal to CurrentAPIRevision, so the documented example cannot go
// stale again
func TestAPIDocSampleMatchesCurrentAPIRevision(t *testing.T) {
	require.Equal(t, CurrentAPIRevision,
		latestRevision(t, "../../docs/bridgetracker/API.md", regexp.MustCompile(`(?m)^\s*"api_revision": (\d+),`)),
		"the api_revision sample of docs/bridgetracker/API.md is not the current revision")
}
