// Package oapi holds the server types and strict gin bindings generated from
// the OpenAPI document in bridgeservice/apispec/generated/openapi.yaml.
//
// The document is not hand-written: it is emitted by the Zod registry in
// bridgeservice/apispec. Regenerate this package after any change there.
package oapi

//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config oapi-codegen.yaml ../apispec/generated/openapi.yaml
