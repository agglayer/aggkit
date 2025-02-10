package prometheus

// Config represents the configuration of the metrics
type Config struct {
	// Enabled is the flag to enable/disable the metrics server
	Enabled bool `mapstructure:"Enabled"`
	// Host is the address to bind the metrics server
	Host string `mapstructure:"Host"`
	// Port is the port to bind the metrics server
	Port int `mapstructure:"Port"`
}
