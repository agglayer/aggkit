package signer

type SignerConfig struct {
	Method string                 `jsonschema:"enum=local, enum=web3signer" mapstructure:"Method"`
	Config map[string]interface{} `jsonschema:"omitempty" mapstructure:",remain"`
}
