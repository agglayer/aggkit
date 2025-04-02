package types

import "fmt"

type CompatibilityComparer[T any] interface {
	// IsCompatible returns an error if the data in storage is not compatible
	fmt.Stringer
	IsCompatible(storage T) error
}
