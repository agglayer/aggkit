package compatibility

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/agglayer/aggkit/db"
	dbmocks "github.com/agglayer/aggkit/db/mocks"
	"github.com/stretchr/testify/require"
)

const (
	testOwnerName = "test_owner"
)

type testData struct {
	Value  string `json:"value"`
	Number int    `json:"number"`
}

func TestNewKeyValueToCompatibilityStorage(t *testing.T) {
	kvStorageMock := dbmocks.NewKeyValueStorager(t)

	storage := NewKeyValueToCompatibilityStorage[testData](kvStorageMock, testOwnerName)

	require.NotNil(t, storage)
	require.Equal(t, kvStorageMock, storage.KVStorage)
	require.Equal(t, testOwnerName, storage.OwnerName)
}

func TestKeyValueToCompatibilityStorage_GetCompatibilityData(t *testing.T) {
	ctx := context.Background()

	t.Run("No data stored returns false with no error", func(t *testing.T) {
		kvStorageMock := dbmocks.NewKeyValueStorager(t)
		storage := NewKeyValueToCompatibilityStorage[testData](kvStorageMock, testOwnerName)

		kvStorageMock.EXPECT().GetValue(nil, testOwnerName, compatibilityContentKey).
			Return("", db.ErrNotFound).Once()

		exists, data, err := storage.GetCompatibilityData(ctx, nil)

		require.NoError(t, err)
		require.False(t, exists)
		require.Equal(t, testData{}, data)
	})

	t.Run("Data exists and is valid returns true with unmarshaled data", func(t *testing.T) {
		kvStorageMock := dbmocks.NewKeyValueStorager(t)
		storage := NewKeyValueToCompatibilityStorage[testData](kvStorageMock, testOwnerName)

		expectedData := testData{Value: "test", Number: 42}
		jsonData, err := json.Marshal(expectedData)
		require.NoError(t, err)

		kvStorageMock.EXPECT().GetValue(nil, testOwnerName, compatibilityContentKey).
			Return(string(jsonData), nil).Once()

		exists, data, err := storage.GetCompatibilityData(ctx, nil)

		require.NoError(t, err)
		require.True(t, exists)
		require.Equal(t, expectedData, data)
	})

	t.Run("Invalid JSON data returns error", func(t *testing.T) {
		kvStorageMock := dbmocks.NewKeyValueStorager(t)
		storage := NewKeyValueToCompatibilityStorage[testData](kvStorageMock, testOwnerName)

		kvStorageMock.EXPECT().GetValue(nil, testOwnerName, compatibilityContentKey).
			Return("invalid json", nil).Once()

		exists, data, err := storage.GetCompatibilityData(ctx, nil)

		require.Error(t, err)
		require.Contains(t, err.Error(), "fails to unmarshal runtime data")
		require.False(t, exists)
		require.Equal(t, testData{}, data)
	})

	t.Run("Storage error other than ErrNotFound returns error", func(t *testing.T) {
		kvStorageMock := dbmocks.NewKeyValueStorager(t)
		storage := NewKeyValueToCompatibilityStorage[testData](kvStorageMock, testOwnerName)

		expectedErr := errors.New("storage error")
		kvStorageMock.EXPECT().GetValue(nil, testOwnerName, compatibilityContentKey).
			Return("", expectedErr).Once()

		exists, data, err := storage.GetCompatibilityData(ctx, nil)

		require.Error(t, err)
		require.Equal(t, expectedErr, err)
		require.False(t, exists)
		require.Equal(t, testData{}, data)
	})
}

func TestKeyValueToCompatibilityStorage_SetCompatibilityData(t *testing.T) {
	ctx := context.Background()

	t.Run("First time storing data uses InsertValue", func(t *testing.T) {
		kvStorageMock := dbmocks.NewKeyValueStorager(t)
		storage := NewKeyValueToCompatibilityStorage[testData](kvStorageMock, testOwnerName)

		dataToStore := testData{Value: "new", Number: 123}
		jsonData, err := json.Marshal(dataToStore)
		require.NoError(t, err)

		// First GetValue returns ErrNotFound
		kvStorageMock.EXPECT().GetValue(nil, testOwnerName, compatibilityContentKey).
			Return("", db.ErrNotFound).Once()
		// Then InsertValue is called
		kvStorageMock.EXPECT().InsertValue(nil, testOwnerName, compatibilityContentKey, string(jsonData)).
			Return(nil).Once()

		err = storage.SetCompatibilityData(ctx, nil, dataToStore)

		require.NoError(t, err)
	})

	t.Run("Updating existing data uses UpdateValue", func(t *testing.T) {
		kvStorageMock := dbmocks.NewKeyValueStorager(t)
		storage := NewKeyValueToCompatibilityStorage[testData](kvStorageMock, testOwnerName)

		dataToStore := testData{Value: "updated", Number: 456}
		jsonData, err := json.Marshal(dataToStore)
		require.NoError(t, err)

		// GetValue returns existing data (not ErrNotFound)
		existingData := testData{Value: "old", Number: 999}
		existingJSON, err := json.Marshal(existingData)
		require.NoError(t, err)

		kvStorageMock.EXPECT().GetValue(nil, testOwnerName, compatibilityContentKey).
			Return(string(existingJSON), nil).Once()
		// Then UpdateValue is called
		kvStorageMock.EXPECT().UpdateValue(nil, testOwnerName, compatibilityContentKey, string(jsonData)).
			Return(nil).Once()

		err = storage.SetCompatibilityData(ctx, nil, dataToStore)

		require.NoError(t, err)
	})

	t.Run("InsertValue error is returned", func(t *testing.T) {
		kvStorageMock := dbmocks.NewKeyValueStorager(t)
		storage := NewKeyValueToCompatibilityStorage[testData](kvStorageMock, testOwnerName)

		dataToStore := testData{Value: "test", Number: 789}
		expectedErr := errors.New("insert error")

		kvStorageMock.EXPECT().GetValue(nil, testOwnerName, compatibilityContentKey).
			Return("", db.ErrNotFound).Once()
		kvStorageMock.EXPECT().InsertValue(nil, testOwnerName, compatibilityContentKey, string([]byte(`{"value":"test","number":789}`))).
			Return(expectedErr).Once()

		err := storage.SetCompatibilityData(ctx, nil, dataToStore)

		require.Error(t, err)
		require.Equal(t, expectedErr, err)
	})

	t.Run("UpdateValue error is returned", func(t *testing.T) {
		kvStorageMock := dbmocks.NewKeyValueStorager(t)
		storage := NewKeyValueToCompatibilityStorage[testData](kvStorageMock, testOwnerName)

		dataToStore := testData{Value: "test", Number: 321}
		expectedErr := errors.New("update error")

		kvStorageMock.EXPECT().GetValue(nil, testOwnerName, compatibilityContentKey).
			Return(`{"value":"old","number":1}`, nil).Once()
		kvStorageMock.EXPECT().UpdateValue(nil, testOwnerName, compatibilityContentKey, string([]byte(`{"value":"test","number":321}`))).
			Return(expectedErr).Once()

		err := storage.SetCompatibilityData(ctx, nil, dataToStore)

		require.Error(t, err)
		require.Equal(t, expectedErr, err)
	})
}

func TestKeyValueToCompatibilityStorage_SetCompatibilityData_MarshalError(t *testing.T) {
	// Test with a type that cannot be marshaled (e.g., contains a channel)
	type unmarshalableData struct {
		Channel chan int
	}

	ctx := context.Background()
	kvStorageMock := dbmocks.NewKeyValueStorager(t)
	storage := NewKeyValueToCompatibilityStorage[unmarshalableData](kvStorageMock, testOwnerName)

	dataToStore := unmarshalableData{Channel: make(chan int)}

	err := storage.SetCompatibilityData(ctx, nil, dataToStore)

	require.Error(t, err)
	require.Contains(t, err.Error(), "fails to marshal runtime data")
}

func TestKeyValueToCompatibilityStorage_WithTransaction(t *testing.T) {
	ctx := context.Background()
	mockTx := dbmocks.NewQuerier(t)

	t.Run("GetCompatibilityData with transaction", func(t *testing.T) {
		kvStorageMock := dbmocks.NewKeyValueStorager(t)
		storage := NewKeyValueToCompatibilityStorage[testData](kvStorageMock, testOwnerName)

		expectedData := testData{Value: "txtest", Number: 100}
		jsonData, err := json.Marshal(expectedData)
		require.NoError(t, err)

		kvStorageMock.EXPECT().GetValue(mockTx, testOwnerName, compatibilityContentKey).
			Return(string(jsonData), nil).Once()

		exists, data, err := storage.GetCompatibilityData(ctx, mockTx)

		require.NoError(t, err)
		require.True(t, exists)
		require.Equal(t, expectedData, data)
	})

	t.Run("SetCompatibilityData with transaction", func(t *testing.T) {
		kvStorageMock := dbmocks.NewKeyValueStorager(t)
		storage := NewKeyValueToCompatibilityStorage[testData](kvStorageMock, testOwnerName)

		dataToStore := testData{Value: "txtransaction", Number: 200}
		jsonData, err := json.Marshal(dataToStore)
		require.NoError(t, err)

		kvStorageMock.EXPECT().GetValue(mockTx, testOwnerName, compatibilityContentKey).
			Return("", db.ErrNotFound).Once()
		kvStorageMock.EXPECT().InsertValue(mockTx, testOwnerName, compatibilityContentKey, string(jsonData)).
			Return(nil).Once()

		err = storage.SetCompatibilityData(ctx, mockTx, dataToStore)

		require.NoError(t, err)
	})
}

func TestKeyValueToCompatibilityStorage_WithDifferentTypes(t *testing.T) {
	ctx := context.Background()

	t.Run("Works with string type", func(t *testing.T) {
		kvStorageMock := dbmocks.NewKeyValueStorager(t)
		storage := NewKeyValueToCompatibilityStorage[string](kvStorageMock, testOwnerName)

		expectedData := "test string"
		jsonData, err := json.Marshal(expectedData)
		require.NoError(t, err)

		kvStorageMock.EXPECT().GetValue(nil, testOwnerName, compatibilityContentKey).
			Return(string(jsonData), nil).Once()

		exists, data, err := storage.GetCompatibilityData(ctx, nil)

		require.NoError(t, err)
		require.True(t, exists)
		require.Equal(t, expectedData, data)
	})

	t.Run("Works with int type", func(t *testing.T) {
		kvStorageMock := dbmocks.NewKeyValueStorager(t)
		storage := NewKeyValueToCompatibilityStorage[int](kvStorageMock, testOwnerName)

		expectedData := 42
		jsonData, err := json.Marshal(expectedData)
		require.NoError(t, err)

		kvStorageMock.EXPECT().GetValue(nil, testOwnerName, compatibilityContentKey).
			Return(string(jsonData), nil).Once()

		exists, data, err := storage.GetCompatibilityData(ctx, nil)

		require.NoError(t, err)
		require.True(t, exists)
		require.Equal(t, expectedData, data)
	})

	t.Run("Works with nested struct type", func(t *testing.T) {
		type nestedStruct struct {
			Inner testData
			Name  string
		}

		kvStorageMock := dbmocks.NewKeyValueStorager(t)
		storage := NewKeyValueToCompatibilityStorage[nestedStruct](kvStorageMock, testOwnerName)

		expectedData := nestedStruct{
			Inner: testData{Value: "inner", Number: 99},
			Name:  "nested",
		}
		jsonData, err := json.Marshal(expectedData)
		require.NoError(t, err)

		kvStorageMock.EXPECT().GetValue(nil, testOwnerName, compatibilityContentKey).
			Return(string(jsonData), nil).Once()

		exists, data, err := storage.GetCompatibilityData(ctx, nil)

		require.NoError(t, err)
		require.True(t, exists)
		require.Equal(t, expectedData, data)
	})
}
