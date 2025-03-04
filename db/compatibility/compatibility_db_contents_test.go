package compatibility

import (
	"context"
	"fmt"
	"testing"

	"github.com/agglayer/aggkit/db/compatibility/mocks"
	"github.com/stretchr/testify/require"
)

var errUnittest = fmt.Errorf("unittest")

type testBindData struct {
	a int
}

func (t testBindData) IsCompatible(storage testBindData) error {
	if t.a != storage.a {
		return fmt.Errorf("a mismatch: %d != %d", t.a, storage.a)
	}
	return nil
}
func (t testBindData) String() string {
	return fmt.Sprintf("a: %d", t.a)
}

func TestCheckCompatibilityData(t *testing.T) {
	storageMock := mocks.NewCompatibilityDataStorager[testBindData](t)
	getterFunc := func(ctx context.Context) (testBindData, error) {
		return testBindData{a: 1}, nil
	}
	sut := NewCompatibilityCheck[testBindData](true, "unittest", getterFunc, storageMock)
	ctx := context.Background()

	t.Run("Fails read from DB", func(t *testing.T) {
		storageMock.EXPECT().GetCompatibilityData(ctx, nil).Return(false, testBindData{}, errUnittest)
		err := sut.Check(ctx, nil)
		require.Error(t, err)
	})
	t.Run("First run store data", func(t *testing.T) {
		// No data stored (false,....)
		storageMock.EXPECT().GetCompatibilityData(ctx, nil).Return(false, testBindData{}, nil)
		// writes new data
		storageMock.EXPECT().SetCompatibilityData(ctx, nil, testBindData{a: 1}).Return(nil)
		err := sut.Check(ctx, nil)
		require.NoError(t, err)
	})
	t.Run("First run, store data fails", func(t *testing.T) {
		// No data stored (false,....)
		storageMock.EXPECT().GetCompatibilityData(ctx, nil).Return(false, testBindData{}, nil)
		// Must write new data but fails
		storageMock.EXPECT().SetCompatibilityData(ctx, nil, testBindData{a: 1}).Return(errUnittest)
		err := sut.Check(ctx, nil)
		require.Error(t, err)
	})
	t.Run("data on store compatible with runtime", func(t *testing.T) {
		// No data stored (false,....)
		storageMock.EXPECT().GetCompatibilityData(ctx, nil).Return(true, testBindData{a: 1}, nil)
		err := sut.Check(ctx, nil)
		require.NoError(t, err)
	})
	t.Run("data on store incompatible with runtime, RequireStorageContentCompatibility=true", func(t *testing.T) {
		// No data stored (false,....)
		storageMock.EXPECT().GetCompatibilityData(ctx, nil).Return(true, testBindData{a: 2}, nil)
		err := sut.Check(ctx, nil)
		require.Error(t, err)
	})
	t.Run("data on store incompatible with runtime, RequireStorageContentCompatibility=false", func(t *testing.T) {
		// No data stored (false,....)
		sut.RequireStorageContentCompatibility = false
		storageMock.EXPECT().GetCompatibilityData(ctx, nil).Return(true, testBindData{a: 2}, nil)
		err := sut.Check(ctx, nil)
		require.NoError(t, err)
	})

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
