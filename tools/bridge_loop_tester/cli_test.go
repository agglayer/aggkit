package bridgelooptester_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	bridgelooptester "github.com/agglayer/aggkit/tools/bridge_loop_tester"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
)

// ringTOML is a minimal, valid 2-network config with one closed ring. enabled controls the loop's
// Enabled flag, which is the whole point of TestValidateRejectsAConfigWithNoEnabledLoop.
func ringTOML(statePath string, enabled bool) string {
	enabledLine := ""
	if enabled {
		enabledLine = "Enabled = true"
	}

	return `
[Global]
ProxyURL = "http://127.0.0.1:15601"
StatePath = "` + statePath + `"

[[Networks]]
NetworkID = 0
Name = "L1"
RPCURL = "http://127.0.0.1:13545"
BridgeAddr = "0x0000000000000000000000000000000000000001"

[Networks.Signer]
Method = "local"
Path = "/keystore"
Password = "changeme"

[[Networks]]
NetworkID = 1
Name = "L2A"
RPCURL = "http://127.0.0.1:14545"
BridgeAddr = "0x0000000000000000000000000000000000000002"

[Networks.Signer]
Method = "local"
Path = "/keystore"
Password = "changeme"

[[Loops]]
Name = "ring"
Asset = "eth"
Amount = "1000"
` + enabledLine + `

[[Loops.Hops]]
Source = 0
Destination = 1
Claim = "auto"

[[Loops.Hops]]
Source = 1
Destination = 0
Claim = "manual"
`
}

// writeConfig writes a config file into a temporary directory and returns its path.
func writeConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	return path
}

// runCLI invokes one command of an app wired exactly like cmd/main.go's, and returns its stdout.
//
// Tests that call it must not use t.Parallel: cli.NewApp appends the package-level cli.HelpFlag,
// and urfave/cli's flag Apply writes to that shared global, so two apps running at once race
// inside the library (not inside this package).
func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer
	app := cli.NewApp()
	app.Name = "bridge-loop-tester"
	app.Writer = &out
	app.ErrWriter = &out
	cfg := func() cli.Flag {
		return &cli.StringSliceFlag{Name: "cfg", Aliases: []string{"c"}}
	}
	json := func() cli.Flag { return &cli.BoolFlag{Name: "json"} }
	app.Commands = []*cli.Command{
		{
			Name:   "run",
			Action: bridgelooptester.CmdRun,
			Flags: []cli.Flag{
				cfg(),
				&cli.BoolFlag{Name: "dry-run"},
				&cli.Uint64Flag{Name: "iterations"},
				&cli.BoolFlag{Name: "resume-halted"},
			},
		},
		{Name: "validate", Action: bridgelooptester.CmdValidate, Flags: []cli.Flag{cfg(), json()}},
		{
			Name:   "deploy-token",
			Action: bridgelooptester.CmdDeployToken,
			Flags: []cli.Flag{
				cfg(), json(),
				&cli.UintFlag{Name: "network"},
				&cli.StringFlag{Name: "name"},
				&cli.StringFlag{Name: "symbol"},
				&cli.StringFlag{Name: "mint"},
				&cli.StringFlag{Name: "loop"},
			},
		},
		{
			Name:   "claim",
			Action: bridgelooptester.CmdClaim,
			Flags: []cli.Flag{
				cfg(), json(),
				&cli.UintFlag{Name: "source"},
				&cli.UintFlag{Name: "destination"},
				&cli.UintFlag{Name: "deposit-count"},
				&cli.Uint64Flag{Name: "gas-limit"},
			},
		},
		{Name: "status", Action: bridgelooptester.CmdStatus, Flags: []cli.Flag{cfg(), json()}},
	}

	err := app.Run(append([]string{"bridge-loop-tester"}, args...))

	return out.String(), err
}

// TestValidateRejectsAConfigWithNoEnabledLoop is the guard against the worst failure mode a soak
// test has: Enabled has no implicit true-default, so an omitted or misspelled Enabled would
// otherwise produce a run that does nothing at all and exits successfully.
func TestValidateRejectsAConfigWithNoEnabledLoop(t *testing.T) {
	path := writeConfig(t, ringTOML("", false))

	out, err := runCLI(t, "validate", "--cfg", path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no loop is enabled")
	require.Contains(t, err.Error(), "set Enabled = true explicitly")
	require.Contains(t, err.Error(), "ring (Enabled=false)")
	require.NotContains(t, out, "OK:")
}

func TestValidateRejectsAConfigWithNoLoopsAtAll(t *testing.T) {
	t.Parallel()

	cfg := &bridgelooptester.Config{
		Global:   bridgelooptester.Global{ProxyURL: "http://127.0.0.1:1"},
		Networks: nil,
		Loops:    nil,
	}
	err := cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no loop is enabled")
	require.Contains(t, err.Error(), "none configured")
}

func TestCommandsRequireAConfigFile(t *testing.T) {
	for _, command := range []string{"run", "validate", "deploy-token", "claim", "status"} {
		t.Run(command, func(t *testing.T) {
			_, err := runCLI(t, command)
			require.Error(t, err)
			require.Contains(t, err.Error(), "no config file(s) given")
		})
	}
}

func TestClaimRequiresItsIdentifyingFlags(t *testing.T) {
	path := writeConfig(t, ringTOML("", true))

	_, err := runCLI(t, "claim", "--cfg", path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--source is required")

	_, err = runCLI(t, "claim", "--cfg", path, "--source", "0")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--destination is required")

	_, err = runCLI(t, "claim", "--cfg", path, "--source", "0", "--destination", "1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--deposit-count is required")
}

func TestDeployTokenRequiresANetwork(t *testing.T) {
	path := writeConfig(t, ringTOML("", true))

	_, err := runCLI(t, "deploy-token", "--cfg", path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--network is required")
}

func TestDeployTokenRejectsANonDecimalMint(t *testing.T) {
	path := writeConfig(t, ringTOML("", true))

	_, err := runCLI(t, "deploy-token", "--cfg", path, "--network", "1", "--mint", "0x10")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a base-10 integer")
}

func TestStatusCommandPrintsWhereTheValueSits(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	path := writeConfig(t, ringTOML(statePath, true))

	// With nothing persisted yet, status says so rather than failing.
	out, err := runCLI(t, "status", "--cfg", path)
	require.NoError(t, err)
	require.Contains(t, out, "No state has been persisted yet")
	require.Contains(t, out, `Loop "ring"`)
	require.Contains(t, out, "the ring is closed")

	// Seed a stranded ring and check status names where the value is.
	store, err := bridgelooptester.NewFileStateStore(statePath)
	require.NoError(t, err)
	state := bridgelooptester.NewState()
	record := state.Loop("ring")
	record.HopIndex = 1
	record.CyclesCompleted = 2
	record.CyclesAttempted = 3
	record.LastError = "the claim proof never became available"
	require.NoError(t, store.Save(context.Background(), state))

	out, err = runCLI(t, "status", "--cfg", path)
	require.NoError(t, err)
	require.Contains(t, out, "STRANDED at rest on network 1 (L2A)")
	require.Contains(t, out, "2 completed / 3 attempted")
	require.Contains(t, out, "the claim proof never became available")

	// The JSON form carries the same verdict, machine-readably.
	out, err = runCLI(t, "status", "--cfg", path, "--json")
	require.NoError(t, err)
	require.Contains(t, out, `"stranded": true`)
	require.Contains(t, out, `"hop_index": 1`)
}

func TestStatusCommandRejectsAnInvalidConfig(t *testing.T) {
	path := writeConfig(t, ringTOML("", false))

	_, err := runCLI(t, "status", "--cfg", path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no loop is enabled")
}

func TestRunCommandFailsFastOnAnUnreachableRPC(t *testing.T) {
	// The config points at ports nothing listens on, so building the client pool fails - which is
	// the correct behaviour: a run must not start against endpoints it cannot reach.
	path := writeConfig(t, ringTOML("", true))

	_, err := runCLI(t, "run", "--cfg", path, "--iterations", "1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "run command:")
}
