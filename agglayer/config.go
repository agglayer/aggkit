package agglayer

import (
	"errors"

	aggkitgrpc "github.com/agglayer/aggkit/grpc"
)

var (
	ErrConfigurationCacheRequired = errors.New("configuration cache is required when Cached is true")
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
	if c.Cached && c.ConfigurationCache == nil {
		return ErrConfigurationCacheRequired
	}
	if c.Cached {
		return c.ConfigurationCache.Validate()
	}
	return nil
}
