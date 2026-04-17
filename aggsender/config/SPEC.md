# SPEC: aggsender/config

## Summary

Defines the configuration surface consumed by the AggSender subsystem. Configuration is the public contract between operators (via a config file or env overrides) and the AggSender runtime: it selects the operational mode (PessimisticProof vs AggchainProof vs Auto), the certificate trigger strategy (EpochBased / ASAP / NewBridge / Auto), storage location, Agglayer and AggkitProver client endpoints, the signer for certificates, and the L1/L2 contract addresses that the AggSender must talk to.

Three exported types live here: the top-level `Config`, plus two trigger-mode sub-configs (`TriggerASAPConfig`, `TriggerEpochBasedConfig`). The top-level `Config` and `TriggerASAPConfig` each expose a `Validate` method; the aggregate `Validate` on `Config` is the authoritative gate the rest of the AggSender relies on to refuse start-up with a bad configuration.

## Requirements

- **1.** Every field of the externally-visible configuration surface MUST be bindable from a configuration source keyed by the `mapstructure` tag of that field (see External interface for the authoritative key list).
- **2.** Validation of the AggSender configuration MUST delegate to the embedded Agglayer gRPC client config's own validation, and MUST fail if that validation fails.
- **3.** When the configured mode is `AggchainProof`, validation MUST require the AggkitProver client config to be set and valid; validation MUST fail if it is absent or invalid.
- **4.** When the configured mode is not `AggchainProof`, validation MUST NOT require the AggkitProver client config to be present or valid.
- **5.** Validation of the AggSender configuration MUST delegate to the retry-policy validation for the "build and send certificate" retry policy, and MUST fail if that validation fails.
- **6.** Validation of the AggSender configuration MUST delegate to the storage-retain-certificates-policy validation, and MUST fail if that validation fails.
- **7.** Validation of the AggSender configuration MUST delegate to the L1-info-tree block-finality validation, and MUST fail if that validation fails.
- **8.** Validation of the AggSender configuration MUST delegate to the certificate-send-trigger-mode validation, and MUST fail if that validation fails.
- **9.** Each failed sub-validation MUST be surfaced as an error whose message identifies which sub-component was invalid, and MUST wrap the underlying error so the original cause is preserved.
- **10.** Validation of the ASAP trigger sub-configuration MUST reject a negative `DelayBetweenCertificates`.
- **11.** Validation of the ASAP trigger sub-configuration MUST reject a zero or negative `MinimumNewCertificateInterval`.
- **12.** The default ASAP trigger configuration MUST set `DelayBetweenCertificates` to 1 second, `MinimumNewCertificateInterval` to 1 hour, and `OnNewL2Bridge` to false.
- **13.** A human-readable rendering of the AggSender configuration MUST be obtainable that includes at minimum the storage path, certificates directory, Agglayer client summary, signer method, dry-run flag, RPC-enabled flag, AggkitProver client summary, mode, check-status interval, retry-on-in-error flag, sovereign rollup address, no-FEP-block-gap flag, build-and-send retry policy summary, storage retention policy summary, L1-info-tree block finality, certificate-send trigger mode, and EpochBased trigger sub-config.
- **14.** The human-readable rendering MUST NOT include the raw private key material of the signer; it MAY include only the signer method identifier.

## External interface

The configuration keys below are the contract. Consumers (config files, deployment manifests) depend on these exact names. Changing a key is a breaking change for operators.

Top-level keys (`mapstructure`):

- `StoragePath` — filesystem path for the AggSender sqlite database.
- `StorageRetainCertificatesPolicy` — retention policy identifier (see `aggsender/db` contract for legal values).
- `CertificatesDir` — directory where certificate JSON files are stored.
- `AgglayerClient` — Agglayer gRPC client sub-config.
- `AggsenderPrivateKey` — signer sub-config used to sign certificates.
- `URLRPCL2` — URL of the L2 RPC node.
- `MaxRetriesStoreCertificate` — integer; `0` means infinite.
- `DelayBetweenRetries` — duration used for store-certificate retries and the initial check.
- `MaxCertSize` — unsigned integer maximum emitted certificate size; `0` means infinite.
- `DryRun` — boolean; when true the AggSender MUST NOT submit certificates to Agglayer.
- `EnableRPC` — boolean; enables the AggSender RPC surface.
- `AggkitProverClient` — gRPC client sub-config for the AggkitProver; required only in `AggchainProof` mode (see #3).
- `Mode` — one of `PessimisticProof`, `AggchainProof`, `Auto`.
- `CheckStatusCertificateInterval` — duration between Agglayer certificate-status polls.
- `RetryCertAfterInError` — boolean; when true an `InError` certificate is resent immediately.
- `GlobalExitRootL2` — L2 GlobalExitRootManager contract address (used in AggchainProof mode).
- `GlobalExitRootL1Addr` — L1 GlobalExitRootManager contract address (used in AggchainProof mode).
- `SovereignRollupAddr` — L1 sovereign rollup contract address.
- `RequireStorageContentCompatibility` — boolean; requires DB content to be compatible with the running binary.
- `RequireNoFEPBlockGap` — boolean; forbids a gap between the last-certificate last block and the first FEP block.
- `OptimisticModeConfig` — optimistic-mode sub-config (required by FEP mode).
- `RequireOneBridgeInPPCertificate` — boolean; forces at least one bridge exit per Pessimistic Proof certificate.
- `RollupManagerAddr` — L1 RollupManager contract address.
- `RollupCreationBlockL1` — uint64 L1 block where the rollup was created.
- `MaxL2BlockNumber` — last L2 block included in any certificate; `0` disables the cap.
- `StopOnFinishedSendingAllCertificates` — boolean; stops the AggSender after it sends all certificates up to `MaxL2BlockNumber`.
- `ValidatorClient` — gRPC client sub-config for the Validator.
- `RetriesToBuildAndSendCertificate` — generic retry-policy sub-config.
- `RequireCommitteeMembershipCheck` — boolean; verifies the signer belongs to the committee.
- `CommitteeOverride` — override sub-config for the committee URL (test/dev).
- `AgglayerBridgeL2Addr` — L2 sovereign bridge contract address.
- `UnsetClaimsMaxLogBlockRange` — uint64 max block range for `eth_getLogs` when fetching unset claims; `0` disables.
- `BlockFinalityForL1InfoTree` — one of `LatestBlock`, `SafeBlock`, `PendingBlock`, `FinalizedBlock`, `EarliestBlock`.
- `TriggerCertMode` — one of `EpochBased`, `NewBridge`, `ASAP`, `Auto`.
- `TriggerEpochBased` — sub-config; active when `TriggerCertMode==EpochBased`.
- `TriggerASAP` — sub-config; active when `TriggerCertMode==ASAP`.

`TriggerEpochBased` keys:

- `EpochNotificationPercentage` — unsigned integer; `0` means begin of epoch, `50` means middle.

`TriggerASAP` keys (see #10–#12 for validation):

- `DelayBetweenCertificates` — duration; MUST NOT be negative.
- `MinimumNewCertificateInterval` — duration; MUST be strictly positive.
- `OnNewL2Bridge` — boolean; when true a new certificate is triggered on a detected L2 bridge exit.

## Error modes

- **15.** All validation failures MUST be returned as Go errors; no validation failure MAY be surfaced via panic, log-only, or silent default substitution.

## Out of scope

- Loading configuration from disk / environment. This directory only defines the shape and validation; binding is performed by the top-level `config` package.
- Defaulting of top-level `Config` fields. Only `TriggerASAPConfig` has a constructor for defaults in this directory; defaults for the top-level `Config` are managed outside.
- Cross-field semantic validation beyond what is delegated to sub-configs (e.g., no enforcement that `AgglayerBridgeL2Addr` is set when `TriggerASAP.OnNewL2Bridge` is true).
