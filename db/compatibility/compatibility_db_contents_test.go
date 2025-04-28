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
	sut := NewCompatibilityCheck(true, getterFunc, storageMock)
	ctx := context.Background()

	t.Run("Fails read from DB", func(t *testing.T) {
		storageMock.EXPECT().GetCompatibilityData(ctx, nil).Return(false, testBindData{}, errUnittest).Once()
		err := sut.Check(ctx, nil)
		require.Error(t, err)
	})
	t.Run("First run store data", func(t *testing.T) {
		// No data stored (false,....)
		storageMock.EXPECT().GetCompatibilityData(ctx, nil).Return(false, testBindData{}, nil).Once()
		// writes new data
		storageMock.EXPECT().SetCompatibilityData(ctx, nil, testBindData{a: 1}).Return(nil).Once()
		err := sut.Check(ctx, nil)
		require.NoError(t, err)
	})
	t.Run("First run, store data fails", func(t *testing.T) {
		// No data stored (false,....)
		storageMock.EXPECT().GetCompatibilityData(ctx, nil).Return(false, testBindData{}, nil).Once()
		// Must write new data but fails
		storageMock.EXPECT().SetCompatibilityData(ctx, nil, testBindData{a: 1}).Return(errUnittest).Once()
		err := sut.Check(ctx, nil)
		require.Error(t, err)
	})
	t.Run("data on store compatible with runtime", func(t *testing.T) {
		// No data stored (false,....)
		storageMock.EXPECT().GetCompatibilityData(ctx, nil).Return(true, testBindData{a: 1}, nil).Once()
		err := sut.Check(ctx, nil)
		require.NoError(t, err)
	})
	t.Run("data on store incompatible with runtime, RequireStorageContentCompatibility=true", func(t *testing.T) {
		// No data stored (false,....)
		storageMock.EXPECT().GetCompatibilityData(ctx, nil).Return(true, testBindData{a: 2}, nil).Once()
		err := sut.Check(ctx, nil)
		require.Error(t, err)
	})
	t.Run("data on store incompatible with runtime, RequireStorageContentCompatibility=false", func(t *testing.T) {
		// No data stored (false,....)
		sut.RequireStorageContentCompatibility = false
		storageMock.EXPECT().GetCompatibilityData(ctx, nil).Return(true, testBindData{a: 2}, nil).Once()
		err := sut.Check(ctx, nil)
		require.NoError(t, err)
	})
}

func TestInitialize(t *testing.T) {
	sut := CompatibilityCheck[testBindData]{}
	require.ErrorContains(t, sut.initialize(), "data getter")
	sut.RuntimeDataGetter = func(ctx context.Context) (testBindData, error) { return testBindData{}, nil }
	require.ErrorContains(t, sut.initialize(), "storage")
	sut.Storage = mocks.NewCompatibilityDataStorager[testBindData](t)
	require.NoError(t, sut.initialize())
	require.NotNil(t, sut.Logger)
}
