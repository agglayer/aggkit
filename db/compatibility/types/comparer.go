package types

import "fmt"

type CompatibilityComparer[T any] interface {
	fmt.Stringer
	// IsCompatible returns an error if the data in storage is not compatible with the runtime data
	// and return a new T to be stored into DB (if !=nil)
	IsCompatible(storage T) (*T, error)
}
