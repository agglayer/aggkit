package compatibility

import "testing"

func TestCheckCompatibilityData(t *testing.T) {
}

/*
func TestCheckCompatibilityData(t *testing.T) {
	logger := log.WithFields("test", "sqlite")
	path := path.Join(t.TempDir(), "base.sqlite")
	db, err := NewSQLiteDB(path)
	require.NoError(t, err)
	err = RunMigrationsDB(logger, db, []types.Migration{})
	require.NoError(t, err)
	owner := "unittest"
	data := struct {
		A string
	}{
		A: "value1",
	}
	// Set initial value
	err = CheckCompatibilityData(db, owner, data)
	require.NoError(t, err)
	// Check same value
	err = CheckCompatibilityData(db, owner, data)
	require.NoError(t, err)
	data2 := struct {
		A string
	}{
		A: "value2",
	}
	// Data change, so error compatibility
	err = CheckCompatibilityData(db, owner, data2)
	require.Error(t, err)
}
*/
