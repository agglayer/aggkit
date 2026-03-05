package backward_forward_let

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"

	agglayermocks "github.com/agglayer/aggkit/agglayer/mocks"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	aggkitgrpc "github.com/agglayer/aggkit/grpc"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
	"google.golang.org/grpc/codes"
)

// buildTestCLIContext creates a *cli.Context with --cfg pointing to configPath.
func buildTestCLIContext(t *testing.T, configPath string) *cli.Context {
	t.Helper()
	app := cli.NewApp()
	app.Flags = []cli.Flag{
		&cli.StringSliceFlag{Name: "cfg", Aliases: []string{"c"}},
	}
	set := flag.NewFlagSet("", flag.ContinueOnError)
	for _, f := range app.Flags {
		require.NoError(t, f.Apply(set))
	}
	require.NoError(t, set.Parse([]string{"--cfg", configPath}))
	return cli.NewContext(app, set, nil)
}

// TestLoadConfig_FileNotFound verifies that a missing config file produces an error.
func TestLoadConfig_FileNotFound(t *testing.T) {
	t.Parallel()

	ctx := buildTestCLIContext(t, "/nonexistent/path/config.toml")
	_, err := LoadConfig(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "read config")
}

// TestLoadConfig_InvalidTOML verifies that a file with invalid TOML causes the render step to fail.
func TestLoadConfig_InvalidTOML(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "bad.toml")
	require.NoError(t, os.WriteFile(path, []byte("this is [not valid toml }"), 0o600))

	ctx := buildTestCLIContext(t, path)
	_, err := LoadConfig(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "render config")
}

// TestLoadConfig_Success verifies that an empty config file is loaded successfully,
// relying on default values for all fields.
func TestLoadConfig_Success(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "empty.toml")
	require.NoError(t, os.WriteFile(path, []byte(""), 0o600))

	ctx := buildTestCLIContext(t, path)
	cfg, err := LoadConfig(ctx)
	require.NoError(t, err)
	require.NotNil(t, cfg)
}

// TestLoadConfig_NoCfgFlag verifies that LoadConfig with no --cfg args succeeds,
// using only the built-in default values.
func TestLoadConfig_NoCfgFlag(t *testing.T) {
	t.Parallel()

	app := cli.NewApp()
	app.Flags = []cli.Flag{
		&cli.StringSliceFlag{Name: "cfg", Aliases: []string{"c"}},
	}
	set := flag.NewFlagSet("", flag.ContinueOnError)
	for _, f := range app.Flags {
		require.NoError(t, f.Apply(set))
	}
	// Parse with no --cfg argument.
	require.NoError(t, set.Parse([]string{}))
	ctx := cli.NewContext(app, set, nil)

	cfg, err := LoadConfig(ctx)
	require.NoError(t, err)
	require.NotNil(t, cfg)
}

// TestSetupEnv_EmptyBridgeServiceURL verifies that SetupEnv returns an error
// when BridgeServiceURL is not set.
func TestSetupEnv_EmptyBridgeServiceURL(t *testing.T) {
	t.Parallel()

	cfg := &Config{}
	_, err := SetupEnv(context.Background(), cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "BridgeServiceURL is required")
}

// TestDiagnose_GetNetworkInfo_NotFound verifies that a NotFound gRPC error from
// GetNetworkInfo causes Diagnose to return NoDivergence (not an error).
func TestDiagnose_GetNetworkInfo_NotFound(t *testing.T) {
	t.Parallel()

	mockAgglayer := agglayermocks.NewAgglayerClientMock(t)
	notFoundErr := aggkitgrpc.GRPCError{Code: codes.NotFound, Message: "not found"}
	mockAgglayer.EXPECT().GetNetworkInfo(mock.Anything, uint32(1)).Return(agglayertypes.NetworkInfo{}, notFoundErr)

	env := &Env{
		AgglayerClient: mockAgglayer,
		L2NetworkID:    1,
	}

	result, err := Diagnose(context.Background(), env)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, NoDivergence, result.Case)
}

// TestDiagnose_GetNetworkInfo_OtherError verifies that a non-NotFound error from
// GetNetworkInfo causes Diagnose to return an error.
func TestDiagnose_GetNetworkInfo_OtherError(t *testing.T) {
	t.Parallel()

	mockAgglayer := agglayermocks.NewAgglayerClientMock(t)
	otherErr := aggkitgrpc.GRPCError{Code: codes.Internal, Message: "internal error"}
	mockAgglayer.EXPECT().GetNetworkInfo(mock.Anything, uint32(2)).Return(agglayertypes.NetworkInfo{}, otherErr)

	env := &Env{
		AgglayerClient: mockAgglayer,
		L2NetworkID:    2,
	}

	result, err := Diagnose(context.Background(), env)
	require.Error(t, err)
	require.Nil(t, result)
}

// TestDiagnose_SettledHeightNil verifies that when GetNetworkInfo succeeds but
// SettledHeight is nil, Diagnose returns NoDivergence (no error).
func TestDiagnose_SettledHeightNil(t *testing.T) {
	t.Parallel()

	mockAgglayer := agglayermocks.NewAgglayerClientMock(t)
	// Return NetworkInfo with SettledHeight == nil (no settled certificates).
	mockAgglayer.EXPECT().GetNetworkInfo(mock.Anything, uint32(3)).Return(agglayertypes.NetworkInfo{
		SettledHeight: nil,
	}, nil)

	env := &Env{
		AgglayerClient: mockAgglayer,
		L2NetworkID:    3,
	}

	result, err := Diagnose(context.Background(), env)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, NoDivergence, result.Case)
}
