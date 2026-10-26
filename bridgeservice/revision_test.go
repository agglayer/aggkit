package bridgeservice

import (
	"os"
	"regexp"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// latestRevision returns the highest revision number matched by the first capture group of re in the file
func latestRevision(t *testing.T, file string, re *regexp.Regexp) int {
	t.Helper()

	content, err := os.ReadFile(file)
	require.NoError(t, err)

	entries := re.FindAllStringSubmatch(string(content), -1)
	require.NotEmpty(t, entries, "%s has no revision history entry", file)

	latest := 0
	for _, entry := range entries {
		revision, err := strconv.Atoi(entry[1])
		require.NoError(t, err)
		latest = max(latest, revision)
	}
	return latest
}

// TestCurrentAPIRevisionHasHistoryEntry keeps CurrentAPIRevision in sync with the revision history of
// revision.go and with the detailed changes in docs/API_REVISIONS.md: the latest entry of each must be
// the current revision.
func TestCurrentAPIRevisionHasHistoryEntry(t *testing.T) {
	const bumpHint = "CurrentAPIRevision changed without a matching entry in %s"

	require.Equal(t, CurrentAPIRevision,
		latestRevision(t, "revision.go", regexp.MustCompile(`(?m)^//   - (\d+):`)),
		bumpHint, "the revision history of revision.go")
	require.Equal(t, CurrentAPIRevision,
		latestRevision(t, "docs/API_REVISIONS.md", regexp.MustCompile(`(?m)^### Revision (\d+)\s*$`)),
		bumpHint, "docs/API_REVISIONS.md")
}
