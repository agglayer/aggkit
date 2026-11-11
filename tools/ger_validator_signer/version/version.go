// Package version has the build information of the GER validator signer. The values are set at build time with
// -ldflags "-X github.com/agglayer/aggkit/tools/ger_validator_signer/version.Version=..."
package version

import "fmt"

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
}

// Get returns the build information
func Get() Info {
	return Info{Version: Version, GitRev: GitRev, GitBranch: GitBranch, BuildDate: BuildDate}
}

// String returns the build information in one line
func (i Info) String() string {
	return fmt.Sprintf("%s (git revision %s, branch %s, built %s)", i.Version, i.GitRev, i.GitBranch, i.BuildDate)
}
