package aggkit

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetVersion(t *testing.T) {
	data := GetVersion()
	require.NotEmpty(t, data.Version)
	require.NotEmpty(t, data.GitRev)
	require.NotEmpty(t, data.GitBranch)
	require.NotEmpty(t, data.BuildDate)
	require.NotEmpty(t, data.GoVersion)
	require.NotEmpty(t, data.OS)
	require.NotEmpty(t, data.Arch)
}

func TestString(t *testing.T) {
	data := FullVersion{
		Version:   "v0.1.0",
		GitRev:    "undefined",
		GitBranch: "undefined",
		BuildDate: "Fri, 17 Jun 1988 01:58:00 +0200",
		GoVersion: "go1.16.3",
		OS:        "linux",
		Arch:      "amd64",
	}
	fmt.Printf("%s", data.String())
	require.Equal(t, `Version:      v0.1.0
Git revision: undefined
Git branch:   undefined
Go version:   go1.16.3
Built:        Fri, 17 Jun 1988 01:58:00 +0200
OS/Arch:      linux/amd64
`, data.String())
}

func TestJSONMarshal(t *testing.T) {
	data := FullVersion{
		Version:   "v0.1.0",
		GitRev:    "4ebab70",
		GitBranch: "test",
		BuildDate: "Fri, 17 Jun 1988 01:58:00 +0200",
		GoVersion: "go1.16.3",
		OS:        "linux",
		Arch:      "amd64",
	}

	b, err := json.Marshal(data)
	require.NoError(t, err)
	require.Equal(t, `{"Version":"v0.1.0","GitRev":"4ebab70","GitBranch":"test","BuildDate":"Fri, 17 Jun 1988 01:58:00 +0200","GoVersion":"go1.16.3","OS":"linux","Arch":"amd64"}`, string(b))
}
