package api

import _ "embed" // the swagger specification is part of the binary

// The specification is generated from the annotations of this package with
// `swag init -g server.go -d api --output api/docs --outputTypes json,yaml --parseDependency=false`
// (make generate-swagger-docs-ger_validator_signer). It is served at /swagger/doc.json and /swagger/doc.yaml.
var (
	//go:embed docs/swagger.json
	specJSON []byte
	//go:embed docs/swagger.yaml
	specYAML []byte
)
