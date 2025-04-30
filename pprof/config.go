package pprof

// Config represents the configuration settings for the profiling server.
// It includes options to specify the host, port, and whether profiling is enabled.
type Config struct {
	// ProfilingHost is the address to bind the profiling server
	ProfilingHost string `mapstructure:"ProfilingHost"`
	// ProfilingPort is the port to bind the profiling server
	ProfilingPort int `mapstructure:"ProfilingPort"`
	// ProfilingEnabled is the flag to enable/disable the profiling server
	ProfilingEnabled bool `mapstructure:"ProfilingEnabled"`
}
