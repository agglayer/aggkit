package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetAutoClaimMigrations(t *testing.T) {
	migrations := GetAutoClaimMigrations()
	require.Len(t, migrations, 1)
	require.Equal(t, "autoclaim0001", migrations[0].ID)
	require.NotEmpty(t, migrations[0].SQL)
}

func TestGetFullMigrations(t *testing.T) {
	full := GetFullMigrations()
	autoclaim := GetAutoClaimMigrations()
	require.NotEmpty(t, full)
	require.True(t, len(full) > len(autoclaim), "full migrations should include base + autoclaim")
	// Last entries should be autoclaim migrations
	last := full[len(full)-len(autoclaim):]
	require.Equal(t, autoclaim, last)
}
