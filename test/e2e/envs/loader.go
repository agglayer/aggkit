package envs

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayergerl2"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayermanager"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/aggoraclecommittee"
	"github.com/agglayer/aggkit/bridgeservice/client"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/test/contracts/mintableerc20"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// ENVName represents a valid E2E test environment name
type ENVName string

const (
	// EnvOpPP is an alias for op-pp-2chains (consolidated to single OP-PP env)
	EnvOpPP ENVName = "op-pp-2chains"

	// EnvOpFEP is a testing env that runs an OP network in Full Execution Proof (FEP) mode.
	EnvOpFEP ENVName = "op-fep"

	// EnvOpFEPCommittee is a testing env that runs an OP network in FEP mode with a DA committee.
	EnvOpFEPCommittee ENVName = "op-fep-committee"

	// EnvOpPP2Chains is a testing env with two OP-PP L2 networks (and two aggkit services).
	EnvOpPP2Chains ENVName = "op-pp-2chains"

	// EnvCDKErigon3Chains is a testing env with three cdk-erigon L2 networks.
	EnvCDKErigon3Chains ENVName = "cdk-erigon-3chains"

	// Constants for string parsing and timeouts
	decimalBase          = 10
	serviceReadyTimeout  = 4 * time.Minute
	serviceCheckTimeout  = 5 * time.Second
	serviceCheckInterval = 2 * time.Second
)

// SequencerType identifies the L2 sequencer/stack used by an environment.
type SequencerType string

const (
	// SequencerOpStack denotes an OP-stack sequencer (op-geth based).
	SequencerOpStack SequencerType = "op-stack"

	// SequencerCDKErigon denotes a cdk-erigon sequencer.
	SequencerCDKErigon SequencerType = "cdk-erigon"
)

// EnvCapabilities describes the shape and behavior of a test environment so the
// loader can drive conditional logic (gas model, sequencer type, multi-network,
// multi-aggkit) without hardcoding op-pp assumptions.
type EnvCapabilities struct {
	// NativeGas is true when L2 uses native ETH gas. When true the loader
	// auto-deploys a MintableERC20 on each L2 for L2-native bridging tests.
	// Custom-gas environments set this to false and skip the auto-deploy.
	NativeGas bool

	// Sequencer is the sequencer/stack type for the environment's L2 networks.
	Sequencer SequencerType

	// MultiNetwork is true when the environment is expected to expose more than
	// one L2 network. It is informational; the loader always loads every network
	// present in summary.json.
	MultiNetwork bool

	// MultiAggkit is true when the environment runs more than one aggkit service.
	MultiAggkit bool

	// SettlementSupported is true when the environment can complete an L1<->L2
	// bridge + L2->L1 settlement flow post-boot. It gates the post-test bridge
	// health-check in TestMain. The FEP envs (op-fep, op-fep-committee) have a
	// known, documented L2->L1 FEP-settlement limitation (snapshots emit
	// settled:false; see ENVS_INTEGRATION_PLAN P3/P4), so they are CI'd as
	// boot/load/checks smoke ONLY and set this to false to exclude the
	// settlement assertion. All other envs set it to true so their full
	// post-test bridge/settlement health-check still runs unchanged.
	SettlementSupported bool
}

// envCapabilities is the per-env capability table. Entries for the new envs are
// declared so the constants compile and the conditional code branches are
// reachable; their snapshots/runtime paths are completed in later plan steps.
var envCapabilities = map[ENVName]EnvCapabilities{
	EnvOpPP: {
		NativeGas:           true,
		Sequencer:           SequencerOpStack,
		MultiNetwork:        false,
		MultiAggkit:         false,
		SettlementSupported: true,
	},
	EnvOpFEP: {
		NativeGas:    true,
		Sequencer:    SequencerOpStack,
		MultiNetwork: false,
		MultiAggkit:  false,
		// FEP settlement (L2->L1) is a documented out-of-scope limitation for the
		// snapshotted FEP envs (settled:false). CI'd as boot/load/checks smoke only.
		SettlementSupported: false,
	},
	EnvOpFEPCommittee: {
		NativeGas:    true,
		Sequencer:    SequencerOpStack,
		MultiNetwork: false,
		MultiAggkit:  false,
		// Same FEP settlement limitation as op-fep; committee quorum is still
		// validated by checks.go, but the L2->L1 settlement assertion is excluded.
		SettlementSupported: false,
	},
	EnvOpPP2Chains: {
		NativeGas:           true,
		Sequencer:           SequencerOpStack,
		MultiNetwork:        true,
		MultiAggkit:         true,
		SettlementSupported: true,
	},
	EnvCDKErigon3Chains: {
		// NativeGas is the env-level "native deploys permitted" flag, not a claim
		// that every chain is native. This env is mixed-gas: networks 001/002 are
		// custom-gas and 003 is native ETH. The per-network MintableERC20 decision
		// is (NativeGas allowed) AND (network has no gas_token), so 001/002 skip
		// the deploy + surface their gas token while 003 deploys MintableERC20.
		NativeGas:           true,
		Sequencer:           SequencerCDKErigon,
		MultiNetwork:        true,
		MultiAggkit:         true,
		SettlementSupported: true,
	},
}

// KnownEnvs returns the list of valid env names, ordered as declared. It is used
// to validate the E2E_ENV selection and to surface valid values in error messages.
func KnownEnvs() []ENVName {
	return []ENVName{EnvOpPP, EnvOpFEP, EnvOpFEPCommittee, EnvOpPP2Chains, EnvCDKErigon3Chains}
}

// ParseENVName resolves a string to a known ENVName. An empty string resolves to
// EnvOpPP so the default behavior (op-pp) is preserved when E2E_ENV is unset. An
// unrecognized value returns an error listing the valid values.
func ParseENVName(s string) (ENVName, error) {
	if s == "" {
		return EnvOpPP, nil
	}
	for _, name := range KnownEnvs() {
		if string(name) == s {
			return name, nil
		}
	}
	return "", fmt.Errorf("unknown env %q; valid values: %v", s, KnownEnvs())
}

// capabilitiesFor returns the capabilities for the named env. Unknown envs
// default to the op-pp-equivalent shape (native gas, op-stack, single network)
// so previously-supported behavior is preserved.
func capabilitiesFor(envName ENVName) EnvCapabilities {
	if caps, ok := envCapabilities[envName]; ok {
		return caps
	}
	return EnvCapabilities{
		NativeGas:           true,
		Sequencer:           SequencerOpStack,
		MultiNetwork:        false,
		MultiAggkit:         false,
		SettlementSupported: true,
	}
}

// Env represents a loaded E2E test environment
type Env struct {
	L1 L1Config
	// L2 is the backward-compatible single-network accessor. For single-network
	// envs (op-pp) it points at the only network; for multi-network envs it points
	// at the primary network (the lowest network-id key in summary.json, e.g. "001").
	L2 L2Config
	// L2s holds every L2 network present in summary.json, ordered by ascending
	// network-id key. For single-network envs it contains exactly one entry equal
	// to L2. Use L2ByNetworkID to look up a specific network.
	L2s              []L2Config
	Clients          ClientsConfig
	Keys             KeysConfig
	EnvDir           string
	AggsenderRPCURL  string // External URL of the aggsender JSON-RPC endpoint (primary network)
	Capabilities     EnvCapabilities
	envName          ENVName
	bridgeServiceURL string // Used by StartAggkit to wait for bridge readiness (primary network)
	aggkitDataDir    string // Host path of the primary aggkit container's /tmp bind-mount
}

// PrimaryL2 returns the primary L2 network configuration (the same value as the
// backward-compatible Env.L2 field).
func (e *Env) PrimaryL2() L2Config {
	return e.L2
}

// L2ByNetworkID returns the L2 network whose summary.json key matches the given
// network id (e.g. 1 for "001"), and whether it was found.
func (e *Env) L2ByNetworkID(id uint32) (L2Config, bool) {
	for _, l2 := range e.L2s {
		if l2.SummaryKey == networkSummaryKey(id) {
			return l2, true
		}
	}
	return L2Config{}, false
}

// KeysConfig exposes key pools and special keys for tests
type KeysConfig struct {
	L1Keys         *KeyPool
	L2Keys         *KeyPool
	AggOracle      *ecdsa.PrivateKey
	SovereignAdmin *ecdsa.PrivateKey
}

// KeyPool is a mutex-guarded pool of pre-funded private keys for parallel tests
type KeyPool struct {
	mu        sync.Mutex
	available []*ecdsa.PrivateKey
	inUse     map[common.Address]*ecdsa.PrivateKey
	chainID   *big.Int
}

// L1Config contains L1 network configuration
type L1Config struct {
	ChainID    *big.Int
	Contracts  L1Contracts
	Transactor *bind.TransactOpts
}

// L1Contracts contains initialized L1 contract bindings
type L1Contracts struct {
	RollupManager *agglayermanager.Agglayermanager
	Bridge        *agglayerbridge.Agglayerbridge
}

// L2Config contains L2 network configuration
type L2Config struct {
	ChainID    *big.Int
	NetworkID  uint32
	Contracts  L2Contracts
	Transactor *bind.TransactOpts
	// SummaryKey is the key under networks.l2_networks in summary.json (e.g. "001").
	SummaryKey string
	// AggkitServiceName is the docker-compose service name for this network's
	// aggkit instance (e.g. "aggkit-001").
	AggkitServiceName string
	// OpGethRPCURL is the external URL of this network's L2 EL JSON-RPC endpoint.
	// Despite the op-geth-specific name (kept for backward compatibility), it is
	// populated with whichever sequencer EL the network uses: op-geth for
	// op-stack chains, cdk-erigon for cdk-erigon chains (see l2RPCURLForNetwork).
	// This is the RPC to dial for standard eth_* calls (chain id, blocks,
	// balances). Distinct from AggsenderRPCURL, which is the aggkit node RPC.
	// NOTE (P12): consider renaming to a sequencer-agnostic field (e.g. L2RPCURL)
	// across the wider API; left as-is here to keep this change bounded.
	OpGethRPCURL string
	// AggsenderRPCURL is the external URL of this network's aggsender JSON-RPC endpoint.
	AggsenderRPCURL string
	// BridgeServiceURL is the external URL of this network's bridge REST API.
	BridgeServiceURL string
	// Keys exposes this network's L2 key pool.
	Keys *KeyPool
	// AggkitDataDir is the host path of this network's aggkit container /tmp bind-mount.
	AggkitDataDir string
}

// L2Contracts contains initialized L2 contract bindings
type L2Contracts struct {
	L2Bridge             *agglayerbridgel2.Agglayerbridgel2
	L2BridgeAddress      common.Address
	GlobalExitRoot       *agglayergerl2.Agglayergerl2
	MintableERC20        *mintableerc20.Mintableerc20
	MintableERC20Address common.Address

	// AggOracleCommittee is the bound on-chain AggOracleCommittee contract,
	// populated only for committee-enabled envs (e.g. op-fep-committee) where
	// summary.json carries the committee proxy address under
	// contracts.aggoracle_committee. nil otherwise. Exposes read-only access to
	// the committee quorum and membership (Quorum / GetAllAggOracleMembers /
	// GetAggOracleMembersCount) so callers can verify the M-of-N committee.
	AggOracleCommittee        *aggoraclecommittee.Aggoraclecommittee
	AggOracleCommitteeAddress common.Address

	// GasTokenAddress is the custom gas-token contract address for custom-gas
	// chains (e.g. cdk-erigon networks 001/002), sourced from
	// summary.json networks.l2_networks.<key>.contracts.gas_token. It is the
	// zero address for native-ETH chains. Surfacing the address lets tests
	// detect a custom-gas chain; no ABI binding is created here.
	GasTokenAddress common.Address
}

// ClientsConfig contains RPC clients
type ClientsConfig struct {
	L1            *ethclient.Client
	L2            *ethclient.Client
	BridgeService *client.Client
}

// summaryAccount mirrors a single accounts[] entry in summary.json (L1 or L2).
type summaryAccount struct {
	Address    string  `json:"address"`
	PrivateKey *string `json:"private_key"`
}

// summaryL2Network mirrors a single entry under networks.l2_networks in summary.json.
type summaryL2Network struct {
	ChainID   string `json:"chain_id"`
	Contracts struct {
		L2Bridge       string `json:"l2_bridge"`
		GlobalExitRoot string `json:"global_exit_root"`
		// AggOracleCommittee is the AggOracleCommittee proxy address, present
		// only for committee-enabled envs (op-fep-committee). Empty otherwise.
		AggOracleCommittee string `json:"aggoracle_committee"`
		// GasToken is the custom gas-token contract address, present only for
		// custom-gas chains (e.g. cdk-erigon custom-gas networks). Empty/absent
		// for native-ETH chains. When non-empty the network is treated as
		// custom-gas: the MintableERC20 auto-deploy is skipped and the address
		// is surfaced on L2Contracts.GasTokenAddress.
		GasToken string `json:"gas_token"`
	} `json:"contracts"`
	Services struct {
		OpGeth struct {
			HTTPRpc struct {
				External string `json:"external"`
			} `json:"http_rpc"`
		} `json:"op-geth"`
		// CDKErigon is the L2 EL RPC for cdk-erigon sequencer chains. cdk-erigon
		// envs emit their L2 RPC under this key instead of "op-geth"; the loader
		// selects whichever is present (see l2RPCURLForNetwork).
		CDKErigon struct {
			HTTPRpc struct {
				External string `json:"external"`
			} `json:"http_rpc"`
		} `json:"cdk-erigon"`
		Aggkit struct {
			RPC struct {
				External string `json:"external"`
			} `json:"rpc"`
			BridgeService struct {
				External string `json:"external"`
			} `json:"rest_api"`
		} `json:"aggkit"`
	} `json:"services"`
	Accounts []summaryAccount `json:"accounts"`
}

// summaryJSON represents the structure of summary.json
type summaryJSON struct {
	Networks struct {
		L1 struct {
			ChainID   string `json:"chain_id"`
			Contracts struct {
				RollupManager string `json:"rollup_manager"`
				Bridge        string `json:"bridge"`
			} `json:"contracts"`
			Services struct {
				Geth struct {
					HTTPRpc struct {
						External string `json:"external"`
					} `json:"http_rpc"`
				} `json:"geth"`
			} `json:"services"`
			Accounts []summaryAccount `json:"accounts"`
		} `json:"l1"`
		L2Networks map[string]summaryL2Network `json:"l2_networks"`
	} `json:"networks"`
}

// FindEnvsDir finds the envs directory dynamically
// Works when running from repo root (Makefile) or from test package directory (IDE)
func FindEnvsDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}

	// Walk up the directory tree to find repo root (go.mod)
	dir := cwd
	for {
		goModPath := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(goModPath); err == nil {
			// Found repo root, construct path to envs directory
			envsDir := filepath.Join(dir, "test", "e2e", "envs")
			if _, err := os.Stat(envsDir); err == nil {
				return envsDir, nil
			}
			return "", fmt.Errorf("envs directory not found at %s", envsDir)
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root without finding go.mod
			break
		}
		dir = parent
	}

	return "", fmt.Errorf("could not find repo root (go.mod) from working directory: %s", cwd)
}

// LoadEnv loads an E2E test environment by name
func LoadEnv(ctx context.Context, envName ENVName) (*Env, error) {
	// Find the envs directory dynamically
	envsDir, err := FindEnvsDir()
	if err != nil {
		return nil, fmt.Errorf("find envs directory: %w", err)
	}

	// Construct path to environment directory
	envDir := filepath.Join(envsDir, string(envName))
	summaryPath := filepath.Join(envDir, "summary.json")

	// Read and parse summary.json
	data, err := os.ReadFile(summaryPath)
	if err != nil {
		return nil, fmt.Errorf("read summary.json: %w", err)
	}

	var summary summaryJSON
	if err := json.Unmarshal(data, &summary); err != nil {
		return nil, fmt.Errorf("parse summary.json: %w", err)
	}
	isDockerRunning := os.Getenv("E2E_DOCKER_IS_RUNNING") == "1"
	if !isDockerRunning {
		// Ensure containers are down and per-network aggkit data dirs are clean,
		// then start fresh.
		if err := ensureDockerComposeRunning(ctx, envDir, sortedL2Keys(summary.Networks.L2Networks)); err != nil {
			return nil, fmt.Errorf("start docker compose: %w", err)
		}
	}
	// Wait for services to be ready
	if err := waitForServices(ctx, &summary); err != nil {
		_ = stopDockerCompose(context.Background(), envDir)
		return nil, fmt.Errorf("wait for services: %w", err)
	}

	// Parse L1 chain ID
	l1ChainID := new(big.Int)
	if _, ok := l1ChainID.SetString(summary.Networks.L1.ChainID, decimalBase); !ok {
		return nil, fmt.Errorf("parse L1 chain ID: %s", summary.Networks.L1.ChainID)
	}

	// Create L1 client
	l1Client, err := ethclient.DialContext(ctx, summary.Networks.L1.Services.Geth.HTTPRpc.External)
	if err != nil {
		return nil, fmt.Errorf("dial L1 client: %w", err)
	}

	caps := capabilitiesFor(envName)

	// Initialize L1 contracts
	rollupManagerAddr := common.HexToAddress(summary.Networks.L1.Contracts.RollupManager)
	rollupManager, err := agglayermanager.NewAgglayermanager(rollupManagerAddr, l1Client)
	if err != nil {
		return nil, fmt.Errorf("initialize rollup manager contract: %w", err)
	}

	bridgeAddr := common.HexToAddress(summary.Networks.L1.Contracts.Bridge)
	bridgeContract, err := agglayerbridge.NewAgglayerbridge(bridgeAddr, l1Client)
	if err != nil {
		return nil, fmt.Errorf("initialize bridge contract: %w", err)
	}

	// Build an L2Config for every network in summary.json, ordered by ascending key,
	// so single-network (op-pp) and multi-network envs are handled uniformly.
	keys := sortedL2Keys(summary.Networks.L2Networks)
	if len(keys) == 0 {
		return nil, fmt.Errorf("no L2 networks found in summary.json")
	}

	clients := ClientsConfig{L1: l1Client}
	l2s := make([]L2Config, 0, len(keys))
	for _, key := range keys {
		l2Network := summary.Networks.L2Networks[key]

		// Parse L2 chain ID
		l2ChainID := new(big.Int)
		if _, ok := l2ChainID.SetString(l2Network.ChainID, decimalBase); !ok {
			return nil, fmt.Errorf("parse L2 chain ID for network %s: %s", key, l2Network.ChainID)
		}

		// Create L2 client. The RPC URL is selected per sequencer type (op-geth
		// for op-stack, cdk-erigon for cdk-erigon chains) so multi-sequencer envs
		// dial the correct EL.
		l2RPCURL := l2RPCURLForNetwork(l2Network)
		if l2RPCURL == "" {
			return nil, fmt.Errorf("no L2 EL RPC URL (op-geth/cdk-erigon) for network %s", key)
		}
		l2Client, err := ethclient.DialContext(ctx, l2RPCURL)
		if err != nil {
			return nil, fmt.Errorf("dial L2 client for network %s: %w", key, err)
		}

		// Initialize L2 contracts
		l2BridgeAddr := common.HexToAddress(l2Network.Contracts.L2Bridge)
		l2Bridge, err := agglayerbridgel2.NewAgglayerbridgel2(l2BridgeAddr, l2Client)
		if err != nil {
			return nil, fmt.Errorf("initialize L2 bridge contract for network %s: %w", key, err)
		}

		l2NetworkID, err := l2Bridge.NetworkID(&bind.CallOpts{Context: ctx})
		if err != nil {
			return nil, fmt.Errorf("fetch L2 network ID from bridge contract for network %s: %w", key, err)
		}

		globalExitRootAddr := common.HexToAddress(l2Network.Contracts.GlobalExitRoot)
		globalExitRoot, err := agglayergerl2.NewAgglayergerl2(globalExitRootAddr, l2Client)
		if err != nil {
			return nil, fmt.Errorf("initialize global exit root contract for network %s: %w", key, err)
		}

		l2Contracts := L2Contracts{
			L2Bridge:        l2Bridge,
			L2BridgeAddress: l2BridgeAddr,
			GlobalExitRoot:  globalExitRoot,
		}

		// Surface the custom gas-token address for custom-gas chains. When
		// summary.json carries a non-empty contracts.gas_token the network uses a
		// custom gas token (e.g. cdk-erigon networks 001/002); the address is
		// exposed so tests can detect/skip native-ETH-only paths. Native chains
		// (no gas_token, or the zero address) leave this as the zero address.
		gasTokenStr := strings.TrimSpace(l2Network.Contracts.GasToken)
		networkHasGasToken := gasTokenStr != "" && common.HexToAddress(gasTokenStr) != (common.Address{})
		if networkHasGasToken {
			l2Contracts.GasTokenAddress = common.HexToAddress(gasTokenStr)
			log.Infof("[LoadEnv] custom gas token %s surfaced for network %s",
				l2Contracts.GasTokenAddress.Hex(), key)
		}

		// Bind the AggOracleCommittee contract for committee-enabled envs so
		// callers can read the on-chain quorum and membership (M-of-N). The
		// address is present in summary.json only when the env was snapshotted
		// with use_agg_oracle_committee (e.g. op-fep-committee); otherwise this
		// is skipped and AggOracleCommittee stays nil.
		if addr := strings.TrimSpace(l2Network.Contracts.AggOracleCommittee); addr != "" {
			committeeAddr := common.HexToAddress(addr)
			committee, err := aggoraclecommittee.NewAggoraclecommittee(committeeAddr, l2Client)
			if err != nil {
				return nil, fmt.Errorf("initialize AggOracleCommittee contract for network %s: %w", key, err)
			}
			l2Contracts.AggOracleCommittee = committee
			l2Contracts.AggOracleCommitteeAddress = committeeAddr
			log.Infof("[LoadEnv] AggOracleCommittee bound at %s for network %s", committeeAddr.Hex(), key)
		}

		// Conditionally deploy a MintableERC20 for native-gas networks. This is
		// used by tests that bridge L2-native tokens, which bypass the Local
		// Balance Tree underflow check in AgglayerBridgeL2. The decision is
		// per-network: a network is native iff the env allows native gas
		// (caps.NativeGas) AND the network itself has no custom gas token. This
		// lets mixed-gas multi-chain envs behave correctly — e.g. cdk-erigon-3chains
		// where 001/002 are custom-gas (skip deploy, surface gas_token) and 003 is
		// native (deploy). op-* envs are unchanged: they have no gas_token, so the
		// per-network condition reduces to the previous env-level caps.NativeGas.
		deployMintable := caps.NativeGas && !networkHasGasToken
		if deployMintable {
			erc20Addr, erc20Contract, err := deployMintableERC20(ctx, l2Client, l2ChainID, l2Network.Accounts)
			if err != nil {
				return nil, fmt.Errorf("deploy MintableERC20 for network %s: %w", key, err)
			}
			l2Contracts.MintableERC20 = erc20Contract
			l2Contracts.MintableERC20Address = erc20Addr
			log.Infof("[LoadEnv] MintableERC20 deployed at %s for network %s", erc20Addr.Hex(), key)
		} else {
			log.Infof("[LoadEnv] skipping MintableERC20 deploy for network %s "+
				"(sequencer=%s, env_native_gas=%v, network_has_gas_token=%v)",
				key, caps.Sequencer, caps.NativeGas, networkHasGasToken)
		}

		// Collect this network's L2 keys (deduplicate by address)
		l2Keys, err := collectPrivateKeys(l2Network.Accounts)
		if err != nil {
			return nil, fmt.Errorf("collect L2 keys for network %s: %w", key, err)
		}
		if len(l2Keys) == 0 {
			return nil, fmt.Errorf("no L2 account with private key found for network %s", key)
		}
		l2KeyPool := newKeyPool(l2Keys, l2ChainID)
		l2Transactor, err := bind.NewKeyedTransactorWithChainID(l2Keys[0], l2ChainID)
		if err != nil {
			return nil, fmt.Errorf("create L2 transactor for network %s: %w", key, err)
		}

		l2s = append(l2s, L2Config{
			ChainID:           l2ChainID,
			NetworkID:         l2NetworkID,
			Contracts:         l2Contracts,
			Transactor:        l2Transactor,
			SummaryKey:        key,
			AggkitServiceName: aggkitServiceNameForKey(key),
			OpGethRPCURL:      l2RPCURL,
			AggsenderRPCURL:   l2Network.Services.Aggkit.RPC.External,
			BridgeServiceURL:  l2Network.Services.Aggkit.BridgeService.External,
			Keys:              l2KeyPool,
			AggkitDataDir:     aggkitDataDirForKey(envDir, key),
		})

		// The primary (first/lowest-key) network owns the shared single-client field
		// and bridge-service client for backward compatibility.
		if clients.L2 == nil {
			clients.L2 = l2Client
			clients.BridgeService = client.New(client.Config{
				BaseURL: l2Network.Services.Aggkit.BridgeService.External,
			})
		}
	}

	// Collect all L1 keys with private_key for the pool (deduplicate by address)
	l1Keys, err := collectPrivateKeys(summary.Networks.L1.Accounts)
	if err != nil {
		return nil, fmt.Errorf("collect L1 keys: %w", err)
	}
	if len(l1Keys) == 0 {
		return nil, fmt.Errorf("no L1 account with private key found")
	}
	l1KeyPool := newKeyPool(l1Keys, l1ChainID)
	l1Transactor, err := bind.NewKeyedTransactorWithChainID(l1Keys[0], l1ChainID)
	if err != nil {
		return nil, fmt.Errorf("create L1 transactor: %w", err)
	}

	// The primary network is the first (lowest-key) network and backs Env.L2 for
	// backward compatibility with single-network callers.
	primary := l2s[0]

	// Load aggoracle and sovereign admin from the primary network's keystores
	// (fallback to hardcoded test keys).
	const keystorePassword = "pSnv6Dh5s9ahuzGzH9RoCDrKAMddaX3m"
	aggoracleKey, err := loadAggOracleKey(envDir, primary.SummaryKey, keystorePassword)
	if err != nil {
		return nil, fmt.Errorf("load aggoracle key: %w", err)
	}
	sovereignAdminKey, err := loadSovereignAdminKey(envDir, primary.SummaryKey, keystorePassword)
	if err != nil {
		return nil, fmt.Errorf("load sovereign admin key: %w", err)
	}

	return &Env{
		L1: L1Config{
			ChainID: l1ChainID,
			Contracts: L1Contracts{
				RollupManager: rollupManager,
				Bridge:        bridgeContract,
			},
			Transactor: l1Transactor,
		},
		L2:      primary,
		L2s:     l2s,
		Clients: clients,
		Keys: KeysConfig{
			L1Keys:         l1KeyPool,
			L2Keys:         primary.Keys,
			AggOracle:      aggoracleKey,
			SovereignAdmin: sovereignAdminKey,
		},
		EnvDir:           envDir,
		AggsenderRPCURL:  primary.AggsenderRPCURL,
		Capabilities:     caps,
		envName:          envName,
		bridgeServiceURL: primary.BridgeServiceURL,
		aggkitDataDir:    primary.AggkitDataDir,
	}, nil
}

// loadAggOracleKey loads the aggoracle key from the keystore under config/<networkKey>
// for the given network, falling back to a known test key.
func loadAggOracleKey(envDir, networkKey, password string) (*ecdsa.PrivateKey, error) {
	path := filepath.Join(envDir, "config", networkKey, "aggoracle.keystore")
	key, err := loadKeystoreKey(path, password)
	if err == nil {
		return key, nil
	}
	// Fallback to hardcoded test key from runbook
	return parsePrivateKey("0x6d1d3ef5765cf34176d42276edd7a479ed5dc8dbf35182dfdb12e8aafe0a4919")
}

// loadSovereignAdminKey loads the sovereign admin key from the keystore under
// config/<networkKey> for the given network, falling back to a known test key.
func loadSovereignAdminKey(envDir, networkKey, password string) (*ecdsa.PrivateKey, error) {
	path := filepath.Join(envDir, "config", networkKey, "sovereignadmin.keystore")
	key, err := loadKeystoreKey(path, password)
	if err == nil {
		return key, nil
	}
	// Fallback to hardcoded test key from runbook
	return parsePrivateKey("0xa574853f4757bfdcbb59b03635324463750b27e16df897f3d00dc6bef2997ae0")
}

// collectPrivateKeys parses the private keys from a list of summary.json accounts,
// deduplicating by address and skipping entries without a private key.
func collectPrivateKeys(accounts []summaryAccount) ([]*ecdsa.PrivateKey, error) {
	seen := make(map[common.Address]bool)
	var keys []*ecdsa.PrivateKey
	for _, account := range accounts {
		if account.PrivateKey == nil || *account.PrivateKey == "" {
			continue
		}
		pk, err := parsePrivateKey(*account.PrivateKey)
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		addr := crypto.PubkeyToAddress(pk.PublicKey)
		if seen[addr] {
			continue
		}
		seen[addr] = true
		keys = append(keys, pk)
	}
	return keys, nil
}

// deployMintableERC20 deploys a MintableERC20 token on an L2 network using the
// first account that carries a private key, waiting for the deployment to be mined.
func deployMintableERC20(
	ctx context.Context,
	l2Client *ethclient.Client,
	l2ChainID *big.Int,
	accounts []summaryAccount,
) (common.Address, *mintableerc20.Mintableerc20, error) {
	var deployerKey *ecdsa.PrivateKey
	for _, account := range accounts {
		if account.PrivateKey != nil && *account.PrivateKey != "" {
			pk, err := parsePrivateKey(*account.PrivateKey)
			if err != nil {
				return common.Address{}, nil, fmt.Errorf("parse deployer key for MintableERC20: %w", err)
			}
			deployerKey = pk
			break
		}
	}
	if deployerKey == nil {
		return common.Address{}, nil, fmt.Errorf("no L2 account with private key found for MintableERC20 deployment")
	}
	deployerAuth, err := bind.NewKeyedTransactorWithChainID(deployerKey, l2ChainID)
	if err != nil {
		return common.Address{}, nil, fmt.Errorf("create deployer transactor for MintableERC20: %w", err)
	}
	erc20Addr, erc20Tx, erc20Contract, err := mintableerc20.DeployMintableerc20(deployerAuth, l2Client, "TestToken", "TEST")
	if err != nil {
		return common.Address{}, nil, fmt.Errorf("deploy MintableERC20: %w", err)
	}
	if _, err := bind.WaitMined(ctx, l2Client, erc20Tx); err != nil {
		return common.Address{}, nil, fmt.Errorf("wait for MintableERC20 deployment: %w", err)
	}
	return erc20Addr, erc20Contract, nil
}

// sortedL2Keys returns the keys of the l2_networks map sorted ascending so the
// primary network selection (Env.L2) is deterministic across runs.
func sortedL2Keys[V any](networks map[string]V) []string {
	keys := make([]string, 0, len(networks))
	for k := range networks {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// parsePrivateKey parses a private key from hex string (with or without 0x prefix)
func parsePrivateKey(privateKeyHex string) (*ecdsa.PrivateKey, error) {
	// Remove 0x prefix if present
	if len(privateKeyHex) >= 2 && privateKeyHex[:2] == "0x" {
		privateKeyHex = privateKeyHex[2:]
	}

	privateKey, err := crypto.HexToECDSA(privateKeyHex)
	if err != nil {
		return nil, fmt.Errorf("convert hex to ECDSA: %w", err)
	}

	return privateKey, nil
}

// loadKeystoreKey decrypts a keystore file and returns the private key
func loadKeystoreKey(keystorePath, password string) (*ecdsa.PrivateKey, error) {
	contents, err := os.ReadFile(filepath.Clean(keystorePath))
	if err != nil {
		return nil, err
	}
	key, err := keystore.DecryptKey(contents, password)
	if err != nil {
		return nil, err
	}
	return key.PrivateKey, nil
}

// Checkout removes a key from the pool and returns transact opts and the key; caller must Return the key when done
func (p *KeyPool) Checkout() (*bind.TransactOpts, *ecdsa.PrivateKey, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.available) == 0 {
		return nil, nil, fmt.Errorf("no keys available in pool")
	}
	key := p.available[len(p.available)-1]
	p.available = p.available[:len(p.available)-1]
	addr := crypto.PubkeyToAddress(key.PublicKey)
	if p.inUse == nil {
		p.inUse = make(map[common.Address]*ecdsa.PrivateKey)
	}
	p.inUse[addr] = key
	opts, err := bind.NewKeyedTransactorWithChainID(key, p.chainID)
	if err != nil {
		p.available = append(p.available, key)
		delete(p.inUse, addr)
		return nil, nil, err
	}
	return opts, key, nil
}

// Return returns a key to the pool so it can be checked out again
func (p *KeyPool) Return(key *ecdsa.PrivateKey) {
	if key == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	addr := crypto.PubkeyToAddress(key.PublicKey)
	if p.inUse != nil {
		delete(p.inUse, addr)
	}
	p.available = append(p.available, key)
}

// newKeyPool builds a KeyPool from a list of private keys and chain ID
func newKeyPool(keys []*ecdsa.PrivateKey, chainID *big.Int) *KeyPool {
	available := make([]*ecdsa.PrivateKey, len(keys))
	copy(available, keys)
	return &KeyPool{
		available: available,
		inUse:     make(map[common.Address]*ecdsa.PrivateKey),
		chainID:   chainID,
	}
}

// Stop stops the E2E test environment by running docker compose down
func (e *Env) Stop(ctx context.Context) error {
	return stopDockerCompose(ctx, e.EnvDir)
}

// networkSummaryKey returns the zero-padded summary.json key for a network id
// (e.g. 1 -> "001"), matching the convention used by the env snapshots.
func networkSummaryKey(id uint32) string {
	return fmt.Sprintf("%03d", id)
}

// aggkitServiceNameForKey returns the docker-compose aggkit service name for the
// given summary network key (e.g. "001" -> "aggkit-001").
func aggkitServiceNameForKey(networkKey string) string {
	return "aggkit-" + networkKey
}

// l2RPCURLForNetwork returns the external L2 EL JSON-RPC URL for a network,
// selecting the sequencer-appropriate service key: op-stack chains expose their
// EL under services."op-geth", cdk-erigon chains under services."cdk-erigon".
// op-geth is preferred when present so op-* envs are byte-compatible; cdk-erigon
// is used otherwise. The returned URL is the RPC to dial for standard eth_* calls.
func l2RPCURLForNetwork(l2Network summaryL2Network) string {
	if url := l2Network.Services.OpGeth.HTTPRpc.External; url != "" {
		return url
	}
	return l2Network.Services.CDKErigon.HTTPRpc.External
}

// aggkitDataDirForKey returns the host directory bind-mounted into the aggkit
// container for the given network key as /tmp (e.g. <envDir>/aggkit-001-data).
func aggkitDataDirForKey(envDir, networkKey string) string {
	return filepath.Join(envDir, aggkitServiceNameForKey(networkKey)+"-data")
}

// AggkitServiceName returns the docker-compose service name of the primary
// network's aggkit instance (e.g. "aggkit-001" for op-pp).
func (e *Env) AggkitServiceName() string {
	return e.L2.AggkitServiceName
}

// aggsenderValidatorServiceName is the compose service name of the OPTIONAL, on-demand committee
// validator used only by TestCommitteeUpdates (P10). It lives behind the "committee" compose
// profile (see docker-compose.yml), so the default `up -d` in ensureDockerComposeRunning never
// starts it and waitForServices never waits on it.
const aggsenderValidatorServiceName = "aggsender-validator-004"

// committeeProfile is the compose profile guarding aggsenderValidatorServiceName. The validator
// service only starts when this profile is explicitly activated by StartAggsenderValidator.
const committeeProfile = "committee"

func dockerComposeUserEnv(ctx context.Context) (string, string) {
	cmd := exec.CommandContext(ctx, "docker", "info", "--format", "{{json .SecurityOptions}}")
	out, err := cmd.Output()
	if err == nil && strings.Contains(string(out), "rootless") {
		return "0", "0"
	}

	return fmt.Sprintf("%d", os.Getuid()), fmt.Sprintf("%d", os.Getgid())
}

// newDockerComposeCmd creates a docker compose command with the correct working directory.
// It injects UID and GID into the command environment so docker-compose.yml can choose a
// host-compatible user for bind-mounted writes. Under rootless Docker we use container
// root, which maps back to the host user; otherwise we use the current host UID/GID.
func newDockerComposeCmd(ctx context.Context, envDir string, args ...string) *exec.Cmd {
	uid, gid := dockerComposeUserEnv(ctx)
	cmd := exec.CommandContext(ctx, "docker", append([]string{"compose"}, args...)...)
	cmd.Dir = envDir
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("UID=%s", uid),
		fmt.Sprintf("GID=%s", gid),
	)
	return cmd
}

// StopAggkit stops the primary network's aggkit service so the test can use the
// aggoracle key without conflicting with the running aggkit. For multi-aggkit envs
// use StopAggkitService to target a specific network's aggkit.
func (e *Env) StopAggkit(ctx context.Context) error {
	return e.StopAggkitService(ctx, e.L2.AggkitServiceName)
}

// StopAggkitService stops the named aggkit docker-compose service.
func (e *Env) StopAggkitService(ctx context.Context, serviceName string) error {
	cmd := newDockerComposeCmd(ctx, e.EnvDir, "stop", serviceName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose stop %s: %w\nOutput:\n%s", serviceName, err, string(output))
	}
	if len(output) > 0 {
		log.Debugf("docker compose stop %s output:\n%s\n", serviceName, string(output))
	}
	return nil
}

// StartAggkit starts the primary network's aggkit service and waits for its bridge
// service to be ready. For multi-aggkit envs use StartAggkitService to target a
// specific network's aggkit.
func (e *Env) StartAggkit(ctx context.Context) error {
	return e.StartAggkitService(ctx, e.L2.AggkitServiceName, e.bridgeServiceURL)
}

// StartAggkitService starts the named aggkit docker-compose service and waits for
// the given bridge service URL to become ready.
func (e *Env) StartAggkitService(ctx context.Context, serviceName, bridgeServiceURL string) error {
	cmd := newDockerComposeCmd(ctx, e.EnvDir, "start", serviceName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose start %s: %w\nOutput:\n%s", serviceName, err, string(output))
	}
	if len(output) > 0 {
		log.Debugf("docker compose start %s output:\n%s\n", serviceName, string(output))
	}
	if err := waitForBridgeService(ctx, bridgeServiceURL); err != nil {
		return fmt.Errorf("wait for bridge service after start: %w", err)
	}
	return nil
}

// StartAggsenderValidator starts the OPTIONAL, on-demand committee validator container
// (aggsender-validator-004) by activating the "committee" compose profile. It is used only by
// TestCommitteeUpdates: the container joins the op-pp_default network at hostname
// aggkit-001-aggsender-validator-004 and serves the validator gRPC on :5578, which is the on-chain
// committee member Url the test adds. It does NOT touch any other service and is never part of the
// default `up -d` or waitForServices, so the shared env is unaffected for the other tests.
func (e *Env) StartAggsenderValidator(ctx context.Context) error {
	cmd := newDockerComposeCmd(ctx, e.EnvDir,
		"--profile", committeeProfile, "up", "-d", aggsenderValidatorServiceName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose up %s: %w\nOutput:\n%s", aggsenderValidatorServiceName, err, string(output))
	}
	if len(output) > 0 {
		log.Debugf("docker compose up %s output:\n%s\n", aggsenderValidatorServiceName, string(output))
	}
	return nil
}

// StopAggsenderValidator stops AND removes the on-demand committee validator container so the
// shared env is left exactly as before (mirrors the legacy bats teardown_file, which does
// `docker stop` + `docker rm` of aggkit-001-aggsender-validator-004). It is safe to call even if
// the container was never started or already removed; `rm -sf` is idempotent.
func (e *Env) StopAggsenderValidator(ctx context.Context) error {
	cmd := newDockerComposeCmd(ctx, e.EnvDir,
		"--profile", committeeProfile, "rm", "-sf", aggsenderValidatorServiceName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose rm %s: %w\nOutput:\n%s", aggsenderValidatorServiceName, err, string(output))
	}
	if len(output) > 0 {
		log.Debugf("docker compose rm %s output:\n%s\n", aggsenderValidatorServiceName, string(output))
	}
	return nil
}

// GetAggkitConfigPath returns the path to the primary network's aggkit config file
// on the host (config/<primary-network-key>/aggkit-config.toml).
func (e *Env) GetAggkitConfigPath() string {
	return filepath.Join(e.EnvDir, "config", e.L2.SummaryKey, "aggkit-config.toml")
}

// GetAggsenderDBPath returns the host path to the aggsender SQLite database file.
// This is accessible because the aggkit container's /tmp is bind-mounted to the host.
func (e *Env) GetAggsenderDBPath() string {
	return filepath.Join(e.aggkitDataDir, "aggsender.sqlite")
}

// GetAggkitDataDir returns the host path of the aggkit container's /tmp bind-mount directory.
// Files written by the container to /tmp (e.g. aggsender.sqlite, certificates/) are accessible
// at this path on the host.
func (e *Env) GetAggkitDataDir() string {
	return e.aggkitDataDir
}

// DockerComposeLogs runs "docker compose logs" with the given extra args for this environment,
// injecting AGGKIT_DATA_DIR so compose can resolve the bind-mount variable.
func (e *Env) DockerComposeLogs(ctx context.Context, args ...string) ([]byte, error) {
	cmd := newDockerComposeCmd(ctx, e.EnvDir, append([]string{"logs"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker compose logs: %w\nOutput:\n%s", err, string(out))
	}
	return out, nil
}

// cleanAggkitDataDir removes the aggkit data directory and recreates it with /tmp-like
// permissions so bind-mounts remain writable even under rootless Docker, where the
// mount may appear as root:root inside the container.
func cleanAggkitDataDir(_ context.Context, dataDir string) error {
	if err := os.RemoveAll(dataDir); err != nil {
		return fmt.Errorf("remove dir: %w", err)
	}
	if err := os.MkdirAll(dataDir, 0o1777); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}
	if err := os.Chmod(dataDir, 0o1777); err != nil {
		return fmt.Errorf("chmod dir: %w", err)
	}
	return nil
}

// StopAggkitAndEditConfig stops aggkit and calls editFn with the config file path.
// The caller is responsible for restarting aggkit after editing the config.
func (e *Env) StopAggkitAndEditConfig(ctx context.Context, editFn func(configPath string) error) error {
	if err := e.StopAggkit(ctx); err != nil {
		return fmt.Errorf("stop aggkit: %w", err)
	}
	configPath := e.GetAggkitConfigPath()
	if err := editFn(configPath); err != nil {
		return fmt.Errorf("edit aggkit config %s: %w", configPath, err)
	}
	return nil
}

// RestartAggkitWithConfig stops aggkit, calls editFn to modify the config, then starts aggkit.
// Waits for the bridge service to be ready before returning.
func (e *Env) RestartAggkitWithConfig(ctx context.Context, editFn func(configPath string) error) error {
	if err := e.StopAggkitAndEditConfig(ctx, editFn); err != nil {
		return err
	}
	return e.StartAggkit(ctx)
}

// ensureDockerComposeRunning brings down any running containers, cleans the aggkit data
// directory, then starts docker compose fresh. This guarantees a predictable initial state
// on every test run.
func ensureDockerComposeRunning(ctx context.Context, envDir string, networkKeys []string) error {
	projectName := filepath.Base(envDir)
	networkName := projectName + "_default"

	// Step 1: Bring down any running containers from this project (idempotent)
	cleanupCmd := newDockerComposeCmd(ctx, envDir, "down", "-v", "--remove-orphans")
	if cleanupOutput, err := cleanupCmd.CombinedOutput(); err != nil {
		log.Debugf("docker compose down (ignored): %v\nOutput:\n%s\n", err, string(cleanupOutput))
	} else if len(cleanupOutput) > 0 {
		log.Debugf("docker compose down output:\n%s\n", string(cleanupOutput))
	}

	// Step 2: Find and remove all networks with the project name (by ID to handle duplicates)
	listNetworksCmd := exec.CommandContext(ctx, "docker", "network", "ls", "--filter", "name="+networkName, "--format", "{{.ID}}")
	networkIDs, err := listNetworksCmd.Output()
	if err == nil && len(networkIDs) > 0 {
		for _, networkID := range strings.Split(strings.TrimSpace(string(networkIDs)), "\n") {
			if networkID != "" {
				removeNetworkCmd := exec.CommandContext(ctx, "docker", "network", "rm", networkID)
				if networkOutput, err := removeNetworkCmd.CombinedOutput(); err != nil {
					log.Debugf("docker network cleanup for %s (ignored): %v\nOutput:\n%s\n", networkID, err, string(networkOutput))
				} else {
					log.Debugf("removed network %s\n", networkID)
				}
			}
		}
	}

	// Step 3: Remove any leftover containers with the same names (force remove)
	listContainersCmd := exec.CommandContext(ctx, "docker", "ps", "-a", "--filter", "name="+projectName, "--format", "{{.ID}}")
	containerIDs, err := listContainersCmd.Output()
	if err == nil && len(containerIDs) > 0 {
		for _, containerID := range strings.Split(strings.TrimSpace(string(containerIDs)), "\n") {
			if containerID != "" {
				removeContainerCmd := exec.CommandContext(ctx, "docker", "rm", "-f", containerID)
				if containerOutput, err := removeContainerCmd.CombinedOutput(); err != nil {
					log.Debugf("docker container cleanup for %s (ignored): %v\nOutput:\n%s\n", containerID, err, string(containerOutput))
				} else {
					log.Debugf("removed container %s\n", containerID)
				}
			}
		}
	}

	// Step 4: Clean each network's aggkit data directory for a fresh state. Falls
	// back to the legacy single "001" directory when no network keys are provided.
	keys := networkKeys
	if len(keys) == 0 {
		keys = []string{"001"}
	}
	for _, networkKey := range keys {
		dataDir := aggkitDataDirForKey(envDir, networkKey)
		if err := cleanAggkitDataDir(ctx, dataDir); err != nil {
			return fmt.Errorf("clean aggkit data dir %s: %w", dataDir, err)
		}
		log.Debugf("prepared aggkit data dir: %s\n", dataDir)
	}

	// Step 5: Start fresh.
	// Use --wait so the command blocks until every service is healthy/running instead of
	// returning as soon as containers are created. On a cold host (images loaded for the first
	// time) the health-gated dependencies (e.g. agglayer, op-geth) can be slow to become healthy;
	// a plain "up -d" can abort early on those depends_on conditions. The generous --wait-timeout
	// accommodates the heavier multi-container envs (FEP prover, committee, multi-chain).
	log.Debugf("running docker compose up -d --wait for %s\n", projectName)
	cmd := newDockerComposeCmd(ctx, envDir, "up", "-d", "--wait", "--wait-timeout", "600")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose up: %w\nOutput:\n%s", err, string(output))
	}
	if len(output) > 0 {
		log.Debugf("docker compose up output:\n%s\n", string(output))
	}
	return nil
}

// stopDockerCompose stops the docker compose environment
func stopDockerCompose(ctx context.Context, envDir string) error {
	cmd := newDockerComposeCmd(ctx, envDir, "down", "-v", "--remove-orphans")

	// Capture output to include in error messages if the command fails
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose down: %w\nOutput:\n%s", err, string(output))
	}

	// Log the output on success for debugging
	if len(output) > 0 {
		log.Debugf("docker compose down output:\n%s\n", string(output))
	}

	return nil
}

// waitForServices waits for all services to be ready
func waitForServices(ctx context.Context, summary *summaryJSON) error {
	// Wait for L1 geth to be ready
	if err := waitForEthereumService(ctx, summary.Networks.L1.Services.Geth.HTTPRpc.External); err != nil {
		return fmt.Errorf("wait for L1 geth: %w", err)
	}

	// Wait for every L2 network's EL (op-geth or cdk-erigon) and bridge service to
	// be ready, in a deterministic order.
	for _, key := range sortedL2Keys(summary.Networks.L2Networks) {
		l2Network := summary.Networks.L2Networks[key]
		if err := waitForEthereumService(ctx, l2RPCURLForNetwork(l2Network)); err != nil {
			return fmt.Errorf("wait for L2 EL (network %s): %w", key, err)
		}

		// Wait for bridge service to be ready
		if err := waitForBridgeService(ctx, l2Network.Services.Aggkit.BridgeService.External); err != nil {
			return fmt.Errorf("wait for bridge service (network %s): %w", key, err)
		}
	}

	return nil
}

// waitForEthereumService waits for an Ethereum RPC service to be ready
func waitForEthereumService(ctx context.Context, url string) error {
	timeout := serviceReadyTimeout
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		client, err := ethclient.DialContext(ctx, url)
		if err == nil {
			// Try to get chain ID to verify service is responsive
			checkCtx, cancel := context.WithTimeout(ctx, serviceCheckTimeout)
			_, err := client.ChainID(checkCtx)
			cancel()
			client.Close()

			if err == nil {
				return nil
			}
		}

		time.Sleep(serviceCheckInterval)
	}

	return fmt.Errorf("ethereum service at %s did not become ready within %v", url, timeout)
}

// waitForBridgeService waits for the bridge service to be ready
func waitForBridgeService(ctx context.Context, url string) error {
	timeout := serviceReadyTimeout
	deadline := time.Now().Add(timeout)

	bridgeClient := client.New(client.Config{
		BaseURL: url,
		Timeout: serviceCheckTimeout,
	})

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Try to call HealthCheck to verify service is responsive
		checkCtx, cancel := context.WithTimeout(ctx, serviceCheckTimeout)
		_, err := bridgeClient.HealthCheck(checkCtx)
		cancel()

		if err == nil {
			return nil
		}

		time.Sleep(serviceCheckInterval)
	}

	return fmt.Errorf("bridge service at %s did not become ready within %v", url, timeout)
}
