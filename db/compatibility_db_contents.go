package db

import (
	"encoding/json"
	"errors"
	"fmt"
)

var ErrIncompatibleData = errors.New("incompatible data")

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

func CheckCompatibilityData(tx Querier, ownerName string, data interface{}) error {
	key := "compatibility_content"
	dataStr, err := json.Marshal(data)
	if err != nil {
		return err
	}
	exists, err := ExistsKey(tx, ownerName, key)
	if err != nil {
		return err
	}
	if !exists {
		// Store data
		return InsertValue(tx, ownerName, key, string(dataStr))
	}
	// Check content
	value, err := GetValue(tx, ownerName, key)
	if err != nil {
		return err
	}
	if value == string(dataStr) {
		// Data is the same
		return nil
	}
	// Data is different
	return fmt.Errorf("data on DB is [%s] != runtime [%s]. Err: %w", dataStr, value, ErrIncompatibleData)
}
