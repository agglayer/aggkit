package agglayer

import (
	aggkitgrpc "github.com/agglayer/aggkit/grpc"
)

type ClientConfig struct {
	GRPC               *aggkitgrpc.ClientConfig
	Cached             bool
	ConfigurationCache *ConfigurationCache
}

// Validate checks if the client configuration is valid.
func (c *ClientConfig) Validate() error {
	if err := c.GRPC.Validate(); err != nil {
		return err
	}
	if c.Cached {
		return c.ConfigurationCache.Validate()
	}
	return nil
}
