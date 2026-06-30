package exit_certificate

// Output filenames written/read by the pipeline steps, all relative to options.outputDir.
// Centralized here so each name has a single source of truth (no duplicated string literals).
const (
	fileFinalCertificate  = "exit-certificate-final.json"
	fileSignedCertificate = "exit-certificate-signed.json"

	fileStep0TargetBlock = "step-0-l2_target_block.json"
	fileStep0LBT         = "step-0-lbt.json"

	fileStepAAddresses    = "step-a-addresses.json"
	fileStepAFailedTraces = "step-a-failed-traces.json"
	fileStepA1Addresses   = "step-a1-addresses.json"
	fileStepA1FailedTrace = "step-a1-failed-traces.json"
	fileStepA2Addresses   = "step-a2-addresses.json"
	fileStepAAltAddresses = "step-aalt-addresses.json"

	fileStepBAccumulated       = "step-b-accumulated.json"
	fileStepBContractAddresses = "step-b-contract-addresses.json"
	fileStepBEOABalances       = "step-b-eoa-balances.json"
	fileStepB2DetectedERC20s   = "step-b2-detected-erc20s.json"
	fileStepB2DiscardedERC20s  = "step-b2-discarded-erc20s.json"
	fileStepB3ERC20Holders     = "step-b3-erc20-holders.json"

	fileStepCSCLockedValues = "step-c-sc-locked-values.json"
	fileStepCHolderBridges  = "step-c-holder-bridges.json"

	fileStepCheckResult = "step-check-result.json"

	fileStepDCertificate = "step-d-exit-certificate.json"

	fileStepECertificate      = "step-e-exit-certificate.json"
	fileStepEUnclaimedBridges = "step-e-unclaimed-bridges.json"
	fileStepEUnclaimedMsgs    = "step-e-unclaimed-messages.json"

	fileStepFCappedCertificate = "step-f-capped-certificate.json"
	fileStepFChecks            = "step-f-checks.json"
	//nolint:gosec // G101 false positive: this is an output filename, not a credential.
	fileStepFTokenBalances = "step-f-token-balances.json"

	fileStepG1ShadowForkBlock     = "step-g1-shadow-fork-block.json"
	fileStepG1LiteDB              = "step-g1-l2bridgesyncerlite.sqlite"
	fileStepGNewLocalExitRoot     = "step-g-new-local-exit-root.json"
	fileStepGReorderedCertificate = "step-g-reordered-certificate.json"
	fileStepGFailedExit           = "step-g-failed-exit.json"
	fileStepGLiteDB               = "step-g-l2bridgesyncerlite.sqlite"

	fileStepHPreviousLocalExitRoot = "step-h-previous-local-exit-root.json"

	fileStepSubmitResult = "step-submit-result.json"
	fileStepWaitResult   = "step-wait-result.json"
)
