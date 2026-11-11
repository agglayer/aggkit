package version

import (
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGet(t *testing.T) {
	info := Get()
	require.Equal(t, Version, info.Version)
	require.Equal(t, GitRev, info.GitRev)
	require.Equal(t, GitBranch, info.GitBranch)
	require.Equal(t, BuildDate, info.BuildDate)
	require.Equal(t, runtime.Version(), info.GoVersion)
	require.Equal(t, runtime.GOOS, info.OS)
	require.Equal(t, runtime.GOARCH, info.Arch)
}

// The release workflows check the image with `docker run <image> version | awk '/^Version:/{print $2}'`
func TestInfo_String_HasTheLineTheWorkflowsCheck(t *testing.T) {
	info := Info{
		Version: "v1.2.3", GitRev: "abc1234", GitBranch: "main", BuildDate: "2026-11-11T10:00:00Z",
		GoVersion: "go1.27.3", OS: "linux", Arch: "amd64",
	}
	out := info.String()

	var versionField string
	for _, line := range strings.Split(out, "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && fields[0] == "Version:" {
			versionField = fields[1]
		}
	}
	require.Equal(t, "v1.2.3", versionField)

	for _, expected := range []string{
		"Git revision: abc1234", "Git branch:   main", "Go version:   go1.27.3",
		"Built:        2026-11-11T10:00:00Z", "OS/Arch:      linux/amd64",
	} {
		require.Contains(t, out, expected)
	}
}
