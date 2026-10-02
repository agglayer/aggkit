package types

import (
	"time"

	"github.com/agglayer/aggkit"
)

// HealthStatusOK is the value of HealthResponse.Status: the endpoint always
// returns 200, so the status is always "ok"
const HealthStatusOK = "ok"

// HealthResponse is the body of GET /tracker/v1/health
type HealthResponse struct {
	// Status is always "ok"
	Status string `json:"status"`
	// APIRevision is the tracker's wire API contract version (see CurrentAPIRevision) —
	// bumped whenever the wire contract changes, so a client can detect which shape the
	// responding instance speaks
	APIRevision int `json:"api_revision"`
	// InstanceID is a UUID generated at startup; it changes on every execution, so two
	// responses with different InstanceID come from different instances (or the same
	// instance after a restart)
	InstanceID string `json:"instance_id"`
	// StartDate is when this instance started (RFC3339, UTC), fixed for as long as InstanceID
	// is. It is the reference point PendingNetworks is relative to — every entry there was, by
	// definition, discovered after it. Uptime is not served as its own field: a client derives
	// it as now minus StartDate, and an absolute instant keeps the response byte-identical
	// between calls
	StartDate time.Time `json:"start_date"`
	// ConfigSHA1 is the sha1sum (hex) of the configuration the instance was started with;
	// it allows checking that all instances behind a proxy run the same configuration
	ConfigSHA1 string `json:"config_sha1"`
	// Version is the build/version information of the running instance
	Version VersionInfo `json:"version"`
	// PendingNetworks lists the networks discovered after startup that were not activated
	// because AutoRegisterNewNetworks is disabled, sorted by network id. Omitted when empty
	PendingNetworks []PendingNetwork `json:"pending_networks,omitempty"`
}

// PendingNetwork is a network the bridge service finder saw appear after startup but did not
// activate because [BridgeServiceFinder] AutoRegisterNewNetworks is false. It is served read-only
// by the health endpoint; activating it requires restarting the service (or adding the network to
// BridgeURLs/RPCURLs).
type PendingNetwork struct {
	// NetworkID is the network (rollup) id that was not activated
	NetworkID uint32 `json:"network_id"`
	// RollupAddress is the hex address of the rollup contract that triggered the event
	RollupAddress string `json:"rollup_address"`
	// BlockNumber is the block of the first event that would have activated the network
	// (0 when the triggering event's block is not available)
	BlockNumber uint64 `json:"block_number"`
	// FirstSeen is when that first event was processed (RFC3339, UTC)
	FirstSeen time.Time `json:"first_seen"`
	// Reason describes which activation path was blocked
	Reason string `json:"reason"`
}

// VersionInfo is the build/version information of the running instance,
// populated from aggkit.GetVersion()
type VersionInfo struct {
	// Version is the semantic version (e.g. "v0.1.0")
	Version string `json:"version"`
	// GitRev is the git revision the binary was built from
	GitRev string `json:"git_rev"`
	// GitBranch is the git branch the binary was built from
	GitBranch string `json:"git_branch"`
	// BuildDate is the build timestamp
	BuildDate string `json:"build_date"`
	// GoVersion is the Go runtime version (e.g. "go1.24.0")
	GoVersion string `json:"go_version"`
	// OS is the target operating system (e.g. "linux")
	OS string `json:"os"`
	// Arch is the target architecture (e.g. "amd64")
	Arch string `json:"arch"`
}

// NewVersionInfo builds a VersionInfo from the build-time version data
func NewVersionInfo() VersionInfo {
	v := aggkit.GetVersion()
	return VersionInfo{
		Version:   v.Version,
		GitRev:    v.GitRev,
		GitBranch: v.GitBranch,
		BuildDate: v.BuildDate,
		GoVersion: v.GoVersion,
		OS:        v.OS,
		Arch:      v.Arch,
	}
}
