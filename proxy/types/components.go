package types

// Component identifies a proxy component.
type Component string

const (
	// ComponentFinder is the bridge service finder component.
	ComponentFinder Component = "FINDER"
	// ComponentTracker is the bridge tracker component.
	ComponentTracker Component = "TRACKER"
	// ComponentL1RPC is the L1 RPC component.
	ComponentL1RPC Component = "L1RPC"
	// ComponentLog is the logging component.
	ComponentLog Component = "LOG"
	// ComponentREST is the REST API component.
	ComponentREST Component = "REST"
)

// String returns the string representation of the Component.
func (c Component) String() string {
	return string(c)
}

// IsValid returns true if the Component is one of the known values.
func (c Component) IsValid() bool {
	switch c {
	case ComponentFinder, ComponentTracker, ComponentL1RPC, ComponentLog, ComponentREST:
		return true
	}
	return false
}
