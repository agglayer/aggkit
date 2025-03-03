package compatibility

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
)

const (
	compatibilityContentKey = "compatibility_content"
)

var ErrIncompatibleData = errors.New("incompatible data")

type CompatibilityChecker interface {
	Check(ctx context.Context, tx db.Querier) error
}

type CompatibilityComparer[T any] interface {
	// IsCompatible returns an error if the data in storage is not compatible
	fmt.Stringer
	IsCompatible(storage T) error
}

// CompatibilityDataStorager is the interface that defines the methods to interact with the storage
type CompatibilityDataStorager[T any] interface {
	// GetCompatibilityData returns the compatibility data from the storage:
	// true -> if data is stored / false -> if data is not stored yet
	// T -> the data stored
	// error -> if there is an error
	GetCompatibilityData(ctx context.Context, tx db.Querier) (bool, T, error)
	// SetCompatibilityData stores the compatibility data in the storage
	// error -> if there is an error
	SetCompatibilityData(ctx context.Context, tx db.Querier, data T) error
}

type CompatibilityDataGetter[T CompatibilityComparer[T]] func(ctx context.Context) (T, error)

type CompatibilityCheck[T CompatibilityComparer[T]] struct {
	RequireStorageContentCompatibility bool
	OwnerName                          string
	RuntimeDataGetter                  CompatibilityDataGetter[T]
	Storage                            CompatibilityDataStorager[T]
	Logger                             common.Logger
}

func NewCompatibilityCheck[T CompatibilityComparer[T]](
	requireStorageContentCompatibility bool,
	ownerName string,
	runtimeDataGetter CompatibilityDataGetter[T],
	storage CompatibilityDataStorager[T]) *CompatibilityCheck[T] {
	return &CompatibilityCheck[T]{
		RequireStorageContentCompatibility: requireStorageContentCompatibility,
		OwnerName:                          ownerName,
		RuntimeDataGetter:                  runtimeDataGetter,
		Storage:                            storage,
	}
}

func (s *CompatibilityCheck[T]) Check(ctx context.Context, tx db.Querier) error {
	err := s.initialize()
	if err != nil {
		return fmt.Errorf("compatibilityCheck: fails to initialize. Err: %w", err)
	}
	runtimeData, err := s.RuntimeDataGetter(ctx)
	if err != nil {
		return err
	}
	// Read runtime data from storage
	exists, storageData, err := s.Storage.GetCompatibilityData(ctx, tx)
	// If there are no data in DB, we set the runtimeData
	if !exists && err == nil {
		// Store data
		return s.Storage.SetCompatibilityData(ctx, tx, runtimeData)
	}
	if err != nil {
		return fmt.Errorf("compatibilityCheck: error reading value from storage. Err: %w", err)
	}
	// Compare data
	err = runtimeData.IsCompatible(storageData)
	if err != nil {
		if s.RequireStorageContentCompatibility {
			return fmt.Errorf("compatibilityCheck: data on DB is [%s] != runtime [%s]. Err: %w",
				storageData.String(), runtimeData.String(), err)
		} else {
			s.Logger.Warnf("compatibilityCheck: data on DB is [%s] != runtime [%s]. Err: %w",
				storageData.String(), runtimeData.String(), err)
		}
	}
	return nil
}

func (s *CompatibilityCheck[T]) initialize() error {
	if s.Logger == nil {
		s.Logger = log.WithFields("module", "compatibilityCheck")
	}
	if s.OwnerName == "" {
		return errors.New("compatibilityCheck: owner name is empty, please set it")
	}
	if s.RuntimeDataGetter == nil {
		return errors.New("compatibilityCheck: runtime data getter is nil, please set it")
	}
	if s.Storage == nil {
		return errors.New("compatibilityCheck: storage is nil, please set it")
	}
	return nil
}

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

/*
There are 3 cases:
- No data on DB -> store it
- There are previous data on DB:
	- If the data is the same -> do nothing
	- If the data is different -> error
	      - [FUTURE] Check if the data is compatible with the previous data
	      	- If it is compatible -> store it
	      	- If it is not compatible -> return an error
*/
