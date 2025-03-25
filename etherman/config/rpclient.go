package config

import (
	"fmt"
)

type RPCMode string

var (
	RPCModeBasic RPCMode = "basic"
	RPCModeOp    RPCMode = "op"
)

type RPCClientConfig struct {
	URL         string         `mapstructure:"URL"`
	Mode        RPCMode        `jsonschema:"enum=basic, enum=op" mapstructure:"Mode"`
	ExtraParams map[string]any `jsonschema:"omitempty" mapstructure:",remain"`
}

func (c RPCClientConfig) GetString(key string) (string, error) {
	valueAny, ok := c.ExtraParams[key]
	if !ok {
		return "", fmt.Errorf("field %s not found in extra params of rpcclient config", key)
	}
	stringValue, ok := valueAny.(string)
	if !ok {
		return "", fmt.Errorf("field %s is not a string", key)
	}
	return stringValue, nil
}
