package exit_certificate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig_FileNotFound(t *testing.T) {
	t.Parallel()
	_, err := LoadConfig("/nonexistent/path/parameters.json")
	require.Error(t, err)
	require.Contains(t, err.Error(), "read config file")
}

func TestLoadConfig_InvalidJSON(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "bad.json")
	require.NoError(t, os.WriteFile(path, []byte("{not valid json}"), 0o600))

	_, err := LoadConfig(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "parse config JSON")
}

func TestLoadConfig_MissingL2RPCURL(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "missing.json")
	data := `{"l2BridgeAddress": "0x2a3DD3EB832aF982ec71669E178424b10Dca2EDe", "targetBlock": "100"}`
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))

	_, err := LoadConfig(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "l2RpcUrl")
}

func TestLoadConfig_MissingL2BridgeAddress(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "missing.json")
	data := `{"l2RpcUrl": "http://localhost:8545", "targetBlock": "100"}`
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))

	_, err := LoadConfig(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "l2BridgeAddress")
}

func TestLoadConfig_MinimalValid(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "minimal.json")
	data := `{
		"l2RpcUrl": "http://localhost:8545",
		"l2BridgeAddress": "0x2a3DD3EB832aF982ec71669E178424b10Dca2EDe",
		"targetBlock": "100"
	}`
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.Equal(t, "http://localhost:8545", cfg.L2RPCURL)
	require.Equal(t, common.HexToAddress("0x2a3DD3EB832aF982ec71669E178424b10Dca2EDe"), cfg.L2BridgeAddress)
	require.Equal(t, "100", cfg.TargetBlock)
	require.Equal(t, uint32(1), cfg.L2NetworkID)
	require.Equal(t, cfg.L2BridgeAddress, cfg.L1BridgeAddress)
}

func TestLoadConfig_FullConfig(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "full.json")
	data := `{
		"l2RpcUrl": "http://l2:8545",
		"l1RpcUrl": "http://l1:8545",
		"l2BridgeAddress": "0x2a3DD3EB832aF982ec71669E178424b10Dca2EDe",
		"l1BridgeAddress": "0x1111111111111111111111111111111111111111",
		"l2NetworkId": 5,
		"targetBlock": "latest",
		"exitAddress": "0x0000000000000000000000000000000000000001",
		"destinationNetwork": 0,
		"options": {
			"blockRange": 10000,
			"concurrencyLimit": 200,
			"rpcBatchSize": 200,
			"rpcDelayMs": 10,
			"l1StartBlock": 1000
		}
	}`
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.Equal(t, "http://l2:8545", cfg.L2RPCURL)
	require.Equal(t, "http://l1:8545", cfg.L1RPCURL)
	require.Equal(t, uint32(5), cfg.L2NetworkID)
	require.Equal(t, "latest", cfg.TargetBlock)
	require.Equal(t, common.HexToAddress("0x0000000000000000000000000000000000000001"), cfg.ExitAddress)
	require.Equal(t, common.HexToAddress("0x1111111111111111111111111111111111111111"), cfg.L1BridgeAddress)
	require.Equal(t, 10000, cfg.Options.BlockRange)
	require.Equal(t, 200, cfg.Options.ConcurrencyLimit)
	require.Equal(t, 200, cfg.Options.RPCBatchSize)
	require.Equal(t, 10, cfg.Options.RPCDelayMs)
	require.Equal(t, uint64(1000), cfg.Options.L1StartBlock)
}

func TestLoadConfig_DefaultOptions(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "defaults.json")
	data := `{
		"l2RpcUrl": "http://localhost:8545",
		"l2BridgeAddress": "0x2a3DD3EB832aF982ec71669E178424b10Dca2EDe",
		"targetBlock": "100"
	}`
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.Equal(t, 5000, cfg.Options.BlockRange)
	require.Equal(t, 20, cfg.Options.ConcurrencyLimit)
	require.Equal(t, 200, cfg.Options.RPCBatchSize)
	require.Equal(t, 0, cfg.Options.RPCDelayMs)
	require.Equal(t, uint64(0), cfg.Options.L1StartBlock)
}

func TestLoadConfig_RelativeLBTFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "parameters.json")
	data := `{
		"l2RpcUrl": "http://localhost:8545",
		"l2BridgeAddress": "0x2a3DD3EB832aF982ec71669E178424b10Dca2EDe",
		"targetBlock": "100",
		"lbtFile": "../some/lbt.json"
	}`
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "../some/lbt.json"), cfg.LBTFile)
}

func TestLoadConfig_RelativeOutputDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "parameters.json")
	data := `{
		"l2RpcUrl": "http://localhost:8545",
		"l2BridgeAddress": "0x2a3DD3EB832aF982ec71669E178424b10Dca2EDe",
		"targetBlock": "100",
		"options": {
			"outputDir": "./output"
		}
	}`
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "output"), cfg.Options.OutputDir)
}

func TestLoadLBTWrappedTokens_EmptyPath(t *testing.T) {
	t.Parallel()
	tokens, err := LoadLBTWrappedTokens("")
	require.NoError(t, err)
	require.Nil(t, tokens)
}

func TestLoadLBTWrappedTokens_FileNotFound(t *testing.T) {
	t.Parallel()
	_, err := LoadLBTWrappedTokens("/nonexistent/file.json")
	require.Error(t, err)
}

func TestLoadLBTWrappedTokens_ValidFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "lbt.json")

	entries := []LBTEntry{
		{
			WrappedTokenAddress: common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"),
			OriginNetwork:       0,
			OriginTokenAddress:  common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"),
			Balance:             "1000000",
		},
		{
			WrappedTokenAddress: common.Address{},
			OriginNetwork:       0,
			OriginTokenAddress:  common.Address{},
			Balance:             "500000",
		},
		{
			WrappedTokenAddress: common.HexToAddress("0xCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC"),
			OriginNetwork:       1,
			OriginTokenAddress:  common.HexToAddress("0xDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD"),
			Balance:             "2000000",
		},
	}

	data, err := json.Marshal(entries)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))

	tokens, err := LoadLBTWrappedTokens(path)
	require.NoError(t, err)
	require.Len(t, tokens, 2)
	require.Equal(t, common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"), tokens[0].WrappedTokenAddress)
	require.Equal(t, common.HexToAddress("0xCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC"), tokens[1].WrappedTokenAddress)
}

func TestLoadLBTEntries_ValidFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "lbt.json")

	entries := []LBTEntry{
		{
			WrappedTokenAddress: common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"),
			OriginNetwork:       0,
			OriginTokenAddress:  common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"),
			Balance:             "1000000",
		},
	}

	data, err := json.Marshal(entries)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))

	result, err := LoadLBTEntries(path)
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, "1000000", result[0].Balance)
}
