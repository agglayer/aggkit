package compatibility

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/agglayer/aggkit/db"
)

/*
This file contains the compatibility storage helper functions:
- You can implement a  CompatibilityDataStorager[T] from a storage:

If you have a sql.DB object:
- First you must create a keyValueStorager implementation using the db.NewKeyValueStorage function
- Then you can create a KeyValueToCompatibilityStorage object using the NewKeyValueToCompatibilityStorage function:

database := db.NewSQLDatabase(dbPath)
....
compatibility.NewKeyValueToCompatibilityStorage[db.RuntimeData](
	db.NewKeyValueStorage(database),
	aggkitcommon.AGGSENDER),

If you have a implementation of key/value storage (KeyValueStorager):
compatibility.NewKeyValueToCompatibilityStorage[db.RuntimeData](
	storage,
	aggkitcommon.AGGSENDER),

*/

const (
	// compatibilityContentKey is the key used to store the compatibility data in storage key/value table
	compatibilityContentKey = "compatibility_content"
)

type KeyValueStorager interface {
	InsertValue(tx db.Querier, owner, key, value string) error
	GetValue(tx db.Querier, owner, key string) (string, error)
}

type KeyValueToCompatibilityStorage[T any] struct {
	KVStorage KeyValueStorager
	OwnerName string
}

func NewKeyValueToCompatibilityStorage[T any](kvStorage KeyValueStorager,
	ownerName string) *KeyValueToCompatibilityStorage[T] {
	return &KeyValueToCompatibilityStorage[T]{
		KVStorage: kvStorage,
		OwnerName: ownerName}
}

func (s *KeyValueToCompatibilityStorage[T]) GetCompatibilityData(ctx context.Context,
	tx db.Querier) (bool, T, error) {
	var runtimeDataUnmarshaled T
	var err error
	runtimeDataRaw, err := s.KVStorage.GetValue(tx, s.OwnerName, compatibilityContentKey)
	if err != nil && errors.Is(err, db.ErrNotFound) {
		return false, runtimeDataUnmarshaled, nil
	}
	if err != nil {
		return false, runtimeDataUnmarshaled, err
	}
	err = json.Unmarshal([]byte(runtimeDataRaw), &runtimeDataUnmarshaled)
	if err != nil {
		return false, runtimeDataUnmarshaled,
			fmt.Errorf("compatibilityCheck: fails to unmarshal runtime data from storage. Err: %w", err)
	}

	return true, runtimeDataUnmarshaled, nil
}

func (s *KeyValueToCompatibilityStorage[T]) SetCompatibilityData(ctx context.Context, tx db.Querier, data T) error {
	dataStr, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("compatibilityCheck: fails to marshal runtime data. Err: %w", err)
	}
	return s.KVStorage.InsertValue(tx, s.OwnerName, compatibilityContentKey, string(dataStr))
}
