# SPEC: aggsender/validator/proto/v1

## Summary

Defines the v1 protocol buffer schema for the `AggsenderValidator` gRPC service: the external contract any aggsender-validator server implementation and any aggsender client must conform to. The service lets an aggsender ask a validator to (a) report its health/version, (b) attest a proposed certificate by returning a signature over it, and (c) attest a Global Exit Root (GER) by returning a signature over it. Signatures are the observable output; the cryptographic scheme is fixed at 65-byte fixed-width blobs so the wire format is stable across implementations.

Messages compose types owned by other agglayer repos (`agglayer.node.types.v1.Certificate`, `agglayer.node.types.v1.CertificateId`, `agglayer.interop.types.v1.FixedBytes65`); this directory does not redefine them and does not own their semantics.

## Requirements

- **1.** The package MUST define a proto3 service named `AggsenderValidator` in the proto package `aggkit.aggsender.validator.v1` exposing exactly three unary RPCs: `HealthCheck`, `ValidateCertificate`, `ValidateGER`.
- **2.** `HealthCheck` MUST accept `google.protobuf.Empty` and return a response carrying the validator's version string, a status string, and a reason string.
- **3.** `ValidateCertificate` MUST accept a request carrying the previous certificate identifier, the certificate to be validated, and the last L2 block number included in that certificate, and return a response carrying a single 65-byte signature produced by the validator over the proposed certificate.
- **4.** `ValidateGER` MUST accept a request carrying the GER bytes to be validated and return a response carrying a single 65-byte signature produced by the validator over the proposed GER.
- **5.** Signature fields on validation responses MUST be typed as `agglayer.interop.types.v1.FixedBytes65`; the wire type MUST NOT be substituted with a variable-length `bytes` field.
- **6.** Certificate and certificate-identifier fields MUST be typed as the authoritative `agglayer.node.types.v1.Certificate` and `agglayer.node.types.v1.CertificateId` messages; this directory MUST NOT redefine those types locally.
- **7.** The generated Go package path MUST be `github.com/agglayer/aggkit/aggsender/validator/proto/v1` (per the proto `go_package` option) so Go consumers import the bindings from a stable location.
- **8.** Field tag numbers assigned in this schema MUST NOT be renumbered or reused for a different field; additions MUST use a previously unused tag number, preserving wire compatibility with existing clients and servers.
- **9.** Existing field names MUST NOT be renamed on the wire; proto3 JSON mappings and language bindings key off these names.

## External interface

The proto file `validator.proto` is the authoritative contract. Consumers (aggsender clients, validator servers, mocks, tests) MUST generate or consume bindings from that file.

Service surface (from `validator.proto`):

- `rpc HealthCheck(google.protobuf.Empty) returns (HealthCheckResponse)` — `HealthCheckResponse { string version = 1; string status = 2; string reason = 3; }`.
- `rpc ValidateCertificate(ValidateCertificateRequest) returns (ValidateCertificateResponse)` — request carries `agglayer.node.types.v1.CertificateId previous_certificate_id = 1`, `agglayer.node.types.v1.Certificate certificate = 2`, `uint64 last_l2_block_in_cert = 3`; response carries `agglayer.interop.types.v1.FixedBytes65 signature = 1`.
- `rpc ValidateGER(ValidateGERRequest) returns (ValidateGERResponse)` — request carries `bytes ger = 1`; response carries `agglayer.interop.types.v1.FixedBytes65 signature = 1`.

Proto package: `aggkit.aggsender.validator.v1`. Fully-qualified gRPC method names follow `/aggkit.aggsender.validator.v1.AggsenderValidator/<Method>` and are part of the wire contract.

## Out of scope

- Transport, authentication, TLS, deadlines, and retry policy. This directory defines only the message/service schema; connection setup and call semantics are the caller's responsibility.
- The cryptographic algorithm used to produce the signature, what the signature covers, and how the validator decides whether to sign. Those live with the validator server implementation, not with this schema.
- Generated code upkeep. The `*.pb.go` files next to the proto are mechanically produced from `validator.proto`; they are not an independent contract.
