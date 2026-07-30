package bridgetracker

import (
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config/types"
)

// DefaultRetentionPeriod is the default Config.RetentionPeriod (see
// DefaultEngineRetentionPeriod for the semantics; both must stay in sync with the [Tracker]
// section of the proxy's default config)
var DefaultRetentionPeriod = types.Duration{Duration: DefaultEngineRetentionPeriod}

// Config holds the configuration of the bridge tracker service. Only the mapstructure-tagged
// fields come from the configuration file; the rest are wired programmatically by the binary
// (see proxy/cmd)
type Config struct {
	// RetentionPeriod is how long a terminal bridge (Finished, or failed to ever resolve)
	// stays queryable before the tracker forgets it. Clients polling or subscribed observe
	// the terminal TrackingStatus during this window; once forgotten, a new request for the
	// same tx re-registers it and tracking restarts from scratch — the retry path for a tx
	// the tracker gave up on
	RetentionPeriod types.Duration `mapstructure:"RetentionPeriod"`

	Logger aggkitcommon.Logger `mapstructure:"-"`

	// ConfigSHA1 is the sha1sum (hex) of the configuration the binary was started with,
	// exposed by the health endpoint to check that all instances behind a proxy run the
	// same configuration
	ConfigSHA1 string `mapstructure:"-"`

	// Registry is the supervised-bridges subsystem to use. Leave nil to get the in-memory
	// adapter (single instance); inject a shared-store implementation so several tracker
	// instances behind a proxy answer for any registered tx
	Registry SupervisedRegistry `mapstructure:"-"`
}
