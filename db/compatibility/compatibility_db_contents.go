package compatibility

import (
	"context"
	"errors"
	"fmt"

	"github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db"
	comptypes "github.com/agglayer/aggkit/db/compatibility/types"
	"github.com/agglayer/aggkit/log"
)

/*
This file contains the compatibility check logic:
The usage is:
- Add a field `CompatibilityChecker` in your class
- Create a checker with the NewCompatibilityCheck function
- Invoke the Check method in your class
*/

// CompatibilityChecker is the interface that defines the methods to check the compatibility
// the object CompatibilityCheck[T] implements this interface
type CompatibilityChecker interface {
	Check(ctx context.Context, tx db.Querier) error
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

// RuntimeDataGetterFunc is a function that returns the runtime data
type RuntimeDataGetterFunc[T comptypes.CompatibilityComparer[T]] func(ctx context.Context) (T, error)

// CompatibilityCheck is the object that checks the compatibility between the runtime data and the data stored in the storage
// it's the implementation of the CompatibilityChecker interface
type CompatibilityCheck[T comptypes.CompatibilityComparer[T]] struct {
	RequireStorageContentCompatibility bool
	RuntimeDataGetter                  RuntimeDataGetterFunc[T]
	Storage                            CompatibilityDataStorager[T]
	Logger                             common.Logger
}

// NewCompatibilityCheck creates a new CompatibilityCheck object
func NewCompatibilityCheck[T comptypes.CompatibilityComparer[T]](
	requireStorageContentCompatibility bool,
	runtimeDataGetter RuntimeDataGetterFunc[T],
	storage CompatibilityDataStorager[T]) *CompatibilityCheck[T] {
	return &CompatibilityCheck[T]{
		RequireStorageContentCompatibility: requireStorageContentCompatibility,
		RuntimeDataGetter:                  runtimeDataGetter,
		Storage:                            storage,
	}
}

// Check checks the compatibility between the runtime data and the data stored in the storage
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
		s.Logger.Infof("compatibilityCheck: no data stored, storing runtime data: [%s]", runtimeData.String())
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
			return nil
		}
	}
	s.Logger.Infof("compatibilityCheck: data in DB[%s] is compatible with [%s]",
		storageData.String(), runtimeData.String())

	return nil
}

func (s *CompatibilityCheck[T]) initialize() error {
	if s.Logger == nil {
		s.Logger = log.WithFields("module", "compatibilityCheck")
	}
	if s.RuntimeDataGetter == nil {
		return errors.New("compatibilityCheck: runtime data getter is nil, please set it")
	}
	if s.Storage == nil {
		return errors.New("compatibilityCheck: storage is nil, please set it")
	}
	return nil
}
