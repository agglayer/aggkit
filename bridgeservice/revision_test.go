package bridgeservice

import (
	"os"
	"regexp"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCurrentAPIRevisionHasHistoryEntry keeps CurrentAPIRevision in sync with the revision history
// documented in revision.go: the latest history entry must be the current revision.
func TestCurrentAPIRevisionHasHistoryEntry(t *testing.T) {
	content, err := os.ReadFile("revision.go")
	require.NoError(t, err)

	entries := regexp.MustCompile(`(?m)^//   - (\d+):`).FindAllStringSubmatch(string(content), -1)
	require.NotEmpty(t, entries, "revision.go has no revision history entry")

	latest := 0
	for _, entry := range entries {
		revision, err := strconv.Atoi(entry[1])
		require.NoError(t, err)
		latest = max(latest, revision)
	}
	require.Equal(t, CurrentAPIRevision, latest,
		"CurrentAPIRevision changed without a matching entry in the revision history of revision.go")
}
