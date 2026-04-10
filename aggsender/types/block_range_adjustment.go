package types

// BlockRangeAdjustmentOptions contains the flow-specific constraints applied by
// the unified block-range adjuster.
type BlockRangeAdjustmentOptions struct {
	MaxL2BlockNumber              uint64
	AllowResizeRetryCert          bool
	RequireOneBridgeInCertificate bool
	ValidateRootToProve           bool
	DisableSizeLimit              bool
}
