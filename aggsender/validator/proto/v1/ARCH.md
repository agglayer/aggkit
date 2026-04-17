# ARCH: aggsender/validator/proto/v1

## Overview

Three files: `validator.proto` is the source of truth; `validator.pb.go` and `validator_grpc.pb.go` are mechanically generated Go bindings (`protoc-gen-go` and `protoc-gen-go-grpc`, both carry `DO NOT EDIT` headers) that let Go code in this repo consume the schema. The proto file upholds every SPEC claim; the generated files exist solely to expose that schema to Go callers (#1–#7).

External type dependencies (`agglayer.node.types.v1.Certificate`, `agglayer.node.types.v1.CertificateId`, `agglayer.interop.types.v1.FixedBytes65`, `google.protobuf.Empty`) are resolved at generation time against the agglayer buf registry; the generated Go code imports them from `buf.build/gen/go/...` rather than from local packages.

## Patterns

- **1.** Schema edits MUST land in `validator.proto`; the `*.pb.go` files MUST be regenerated from it rather than hand-edited. Hand-edits are erased on the next regeneration and break the invariant that the proto is the source of truth.
- **2.** New fields SHOULD be appended with fresh tag numbers; removed fields SHOULD be reserved (`reserved N;` / `reserved "name";`) rather than deleted outright, to enforce SPEC #8 and #9 at the schema level.
- **3.** New shared types (certificates, signatures, hashes) SHOULD be imported from the upstream agglayer proto packages rather than redefined here, so this service stays aligned with SPEC #6 and any upstream schema evolution.

## Notable decisions

- **4.** Signatures are typed `FixedBytes65` instead of plain `bytes`. The fixed-width wrapper makes the 65-byte (r‖s‖v) ECDSA layout explicit on the wire and gives consumers a single place to evolve signature representation across all agglayer services.
- **5.** `HealthCheck` takes `google.protobuf.Empty` rather than a dedicated request message. An empty request type was rejected to keep the health RPC callable with zero-byte payloads from any gRPC client without bespoke message construction; version/status negotiation is returned in the response only.
- **6.** `ValidateCertificateRequest` carries `last_l2_block_in_cert` as a sibling field instead of folding it into `Certificate`. The certificate type is owned upstream in `agglayer.node.types.v1`; attaching aggsender-specific metadata here keeps this service's request self-contained without forcing a change on the shared certificate schema.
