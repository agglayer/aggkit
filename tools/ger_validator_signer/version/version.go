// Package version has the build information of the GER validator signer. The values are set at build time with
// -ldflags "-X github.com/agglayer/aggkit/tools/ger_validator_signer/version.Version=..."
package version

import (
	"fmt"
	"runtime"
)

// Set at build time
var (
	// Version is the version of the release
	Version = "v0.0.0-dev"
	// GitRev is the commit it was built from
	GitRev = "unknown"
	// GitBranch is the branch it was built from
	GitBranch = "unknown"
	// BuildDate is when it was built
	BuildDate = "unknown"
)

// Info is the build information
type Info struct {
	Version   string `json:"version"`
	GitRev    string `json:"git_rev"`
	GitBranch string `json:"git_branch"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
}

// Get returns the build information
func Get() Info {
	return Info{
		Version:   Version,
		GitRev:    GitRev,
		GitBranch: GitBranch,
		BuildDate: BuildDate,
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}
}

// String returns the build information in the same format that aggkit prints: the line that starts with
// "Version:" is what the release workflows check in the image
func (i Info) String() string {
	return fmt.Sprintf("Version:      %s\n"+
		"Git revision: %s\n"+
		"Git branch:   %s\n"+
		"Go version:   %s\n"+
		"Built:        %s\n"+
		"OS/Arch:      %s/%s\n",
		i.Version, i.GitRev, i.GitBranch, i.GoVersion, i.BuildDate, i.OS, i.Arch)
}
