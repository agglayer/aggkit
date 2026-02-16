package types

import "fmt"

type CompatibilityComparer[T any] interface {
	// IsCompatible returns an error if the data in storage is not compatible
	fmt.Stringer
	// IsCompatible returns an error if the data in storage is not compatible with the runtime data
	// and return a new T to be stored into DB (if !=nil)
	IsCompatible(storage T) (*T, error)
}
