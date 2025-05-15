package types

type OptimisticModeQuerier interface {
	// IsOptimisticModeOn returns true if the optimistic mode is on
	IsOptimisticModeOn() (bool, error)
}
