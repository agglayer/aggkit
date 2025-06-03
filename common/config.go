package common

import (
	"fmt"

	"github.com/agglayer/aggkit/config/types"
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
)

// Config holds the configuration for the Aggkit.
type Config struct {
	// NetworkID is the networkID of the Aggkit being run
	NetworkID uint32 `mapstructure:"NetworkID"`
	// L2URL is the URL of the L2 node
	L2RPC ethermanconfig.RPCClientConfig `mapstructure:"L2RPC"`
}

// RESTConfig contains the configuration settings for the REST service in the Aggkit application.
type RESTConfig struct {
	// Host specifies the hostname or IP address on which the REST service will listen.
	Host string `mapstructure:"Host"`

	// Port defines the port number on which the REST service will be accessible.
	Port int `mapstructure:"Port"`

	// ReadTimeout is the HTTP server read timeout
	// check net/http.server.ReadTimeout and net/http.server.ReadHeaderTimeout
	ReadTimeout types.Duration `mapstructure:"ReadTimeout"`

	// WriteTimeout is the HTTP server write timeout
	// check net/http.server.WriteTimeout
	WriteTimeout types.Duration `mapstructure:"WriteTimeout"`

	// MaxRequestsPerIPAndSecond defines how many requests a single IP can
	// send within a single second
	MaxRequestsPerIPAndSecond float64 `mapstructure:"MaxRequestsPerIPAndSecond"`
}

// Address constructs and returns the address as a string in the format "host:port".
func (c *RESTConfig) Address() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}
