package types

import "github.com/agglayer/aggkit"

// HealthStatusOK is the value of HealthResponse.Status: the endpoint always
// returns 200, so the status is always "ok"
const HealthStatusOK = "ok"

// HealthResponse is the body of GET /tracker/v1/health
type HealthResponse struct {
	// Status is always "ok"
	Status string `json:"status"`
	// InstanceID is a UUID generated at startup; it changes on every execution, so two
	// responses with different InstanceID come from different instances (or the same
	// instance after a restart)
	InstanceID string `json:"instance_id"`
	// ConfigSHA1 is the sha1sum (hex) of the configuration the instance was started with;
	// it allows checking that all instances behind a proxy run the same configuration
	ConfigSHA1 string `json:"config_sha1"`
	// Version is the build/version information of the running instance
	Version VersionInfo `json:"version"`
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
