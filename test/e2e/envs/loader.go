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
	"strings"
	"sync"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayergerl2"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayermanager"
	"github.com/agglayer/aggkit/bridgeservice/client"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// ENVName represents a valid E2E test environment name
type ENVName string

const (
	// EnvOpPP is a testing env that has a single OP-PP network deployed
	EnvOpPP ENVName = "op-pp"

	// Constants for string parsing and timeouts
	decimalBase          = 10
	serviceReadyTimeout  = 4 * time.Minute
	serviceCheckTimeout  = 5 * time.Second
	serviceCheckInterval = 2 * time.Second
)

// Env represents a loaded E2E test environment
type Env struct {
	L1               L1Config
	L2               L2Config
	Clients          ClientsConfig
	Keys             KeysConfig
	EnvDir           string
	envName          ENVName
	startedCompose   bool   // Track if we started docker compose (so we know if we should stop it)
	bridgeServiceURL string // Used by StartAggkit to wait for bridge readiness
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
}

// L2Contracts contains initialized L2 contract bindings
type L2Contracts struct {
	L2Bridge        *agglayerbridgel2.Agglayerbridgel2
	L2BridgeAddress common.Address
	GlobalExitRoot  *agglayergerl2.Agglayergerl2
}

// ClientsConfig contains RPC clients
type ClientsConfig struct {
	L1            *ethclient.Client
	L2            *ethclient.Client
	BridgeService *client.Client
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
			Accounts []struct {
				Address    string  `json:"address"`
				PrivateKey *string `json:"private_key"`
			} `json:"accounts"`
		} `json:"l1"`
		L2Networks map[string]struct {
			ChainID   string `json:"chain_id"`
			Contracts struct {
				L2Bridge       string `json:"l2_bridge"`
				GlobalExitRoot string `json:"global_exit_root"`
			} `json:"contracts"`
			Services struct {
				OpGeth struct {
					HTTPRpc struct {
						External string `json:"external"`
					} `json:"http_rpc"`
				} `json:"op-geth"`
				Aggkit struct {
					BridgeService struct {
						External string `json:"external"`
					} `json:"rest_api"`
				} `json:"aggkit"`
			} `json:"services"`
			Accounts []struct {
				Address    string  `json:"address"`
				PrivateKey *string `json:"private_key"`
			} `json:"accounts"`
		} `json:"l2_networks"`
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

	// Start docker compose environment (with locking and checking if already running)
	startedCompose, err := ensureDockerComposeRunning(ctx, envDir)
	if err != nil {
		return nil, fmt.Errorf("start docker compose: %w", err)
	}

	// Wait for services to be ready
	if err := waitForServices(ctx, &summary); err != nil {
		// Only stop services if we started them
		if startedCompose {
			_ = stopDockerCompose(context.Background(), envDir)
		}
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

	// Get L2 network (assuming first network is "001")
	l2Network, ok := summary.Networks.L2Networks["001"]
	if !ok {
		return nil, fmt.Errorf("L2 network 001 not found in summary.json")
	}

	// Parse L2 chain ID
	l2ChainID := new(big.Int)
	if _, ok := l2ChainID.SetString(l2Network.ChainID, decimalBase); !ok {
		return nil, fmt.Errorf("parse L2 chain ID: %s", l2Network.ChainID)
	}

	// Create L2 client
	l2Client, err := ethclient.DialContext(ctx, l2Network.Services.OpGeth.HTTPRpc.External)
	if err != nil {
		return nil, fmt.Errorf("dial L2 client: %w", err)
	}

	// Create bridge service client
	bridgeServiceClient := client.New(client.Config{
		BaseURL: l2Network.Services.Aggkit.BridgeService.External,
	})

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

	// Initialize L2 contracts
	l2BridgeAddr := common.HexToAddress(l2Network.Contracts.L2Bridge)
	l2Bridge, err := agglayerbridgel2.NewAgglayerbridgel2(l2BridgeAddr, l2Client)
	if err != nil {
		return nil, fmt.Errorf("initialize L2 bridge contract: %w", err)
	}

	l2NetworkID, err := l2Bridge.NetworkID(&bind.CallOpts{Context: ctx})
	if err != nil {
		return nil, fmt.Errorf("fetch L2 network ID from bridge contract: %w", err)
	}

	globalExitRootAddr := common.HexToAddress(l2Network.Contracts.GlobalExitRoot)
	globalExitRoot, err := agglayergerl2.NewAgglayergerl2(globalExitRootAddr, l2Client)
	if err != nil {
		return nil, fmt.Errorf("initialize global exit root contract: %w", err)
	}

	// Collect all L1 keys with private_key for the pool (deduplicate by address)
	seenL1Addr := make(map[common.Address]bool)
	var l1Keys []*ecdsa.PrivateKey
	for _, account := range summary.Networks.L1.Accounts {
		if account.PrivateKey != nil && *account.PrivateKey != "" {
			pk, err := parsePrivateKey(*account.PrivateKey)
			if err != nil {
				return nil, fmt.Errorf("parse L1 private key: %w", err)
			}
			addr := crypto.PubkeyToAddress(pk.PublicKey)
			if seenL1Addr[addr] {
				continue
			}
			seenL1Addr[addr] = true
			l1Keys = append(l1Keys, pk)
		}
	}
	if len(l1Keys) == 0 {
		return nil, fmt.Errorf("no L1 account with private key found")
	}
	l1KeyPool := newKeyPool(l1Keys, l1ChainID)
	l1Transactor, err := bind.NewKeyedTransactorWithChainID(l1Keys[0], l1ChainID)
	if err != nil {
		return nil, fmt.Errorf("create L1 transactor: %w", err)
	}

	// Collect all L2 keys with private_key for the pool (deduplicate by address)
	seenL2Addr := make(map[common.Address]bool)
	var l2Keys []*ecdsa.PrivateKey
	for _, account := range l2Network.Accounts {
		if account.PrivateKey != nil && *account.PrivateKey != "" {
			pk, err := parsePrivateKey(*account.PrivateKey)
			if err != nil {
				return nil, fmt.Errorf("parse L2 private key: %w", err)
			}
			addr := crypto.PubkeyToAddress(pk.PublicKey)
			if seenL2Addr[addr] {
				continue
			}
			seenL2Addr[addr] = true
			l2Keys = append(l2Keys, pk)
		}
	}
	if len(l2Keys) == 0 {
		return nil, fmt.Errorf("no L2 account with private key found")
	}
	l2KeyPool := newKeyPool(l2Keys, l2ChainID)
	l2Transactor, err := bind.NewKeyedTransactorWithChainID(l2Keys[0], l2ChainID)
	if err != nil {
		return nil, fmt.Errorf("create L2 transactor: %w", err)
	}

	// Load aggoracle and sovereign admin from keystores (fallback to hardcoded test keys)
	const keystorePassword = "pSnv6Dh5s9ahuzGzH9RoCDrKAMddaX3m"
	aggoracleKey, err := loadAggOracleKey(envDir, keystorePassword)
	if err != nil {
		return nil, fmt.Errorf("load aggoracle key: %w", err)
	}
	sovereignAdminKey, err := loadSovereignAdminKey(envDir, keystorePassword)
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
		L2: L2Config{
			ChainID:   l2ChainID,
			NetworkID: l2NetworkID,
			Contracts: L2Contracts{
				L2Bridge:        l2Bridge,
				L2BridgeAddress: l2BridgeAddr,
				GlobalExitRoot:  globalExitRoot,
			},
			Transactor: l2Transactor,
		},
		Clients: ClientsConfig{
			L1:            l1Client,
			L2:            l2Client,
			BridgeService: bridgeServiceClient,
		},
		Keys: KeysConfig{
			L1Keys:         l1KeyPool,
			L2Keys:         l2KeyPool,
			AggOracle:      aggoracleKey,
			SovereignAdmin: sovereignAdminKey,
		},
		EnvDir:           envDir,
		envName:          envName,
		startedCompose:   startedCompose,
		bridgeServiceURL: l2Network.Services.Aggkit.BridgeService.External,
	}, nil
}

// loadAggOracleKey loads aggoracle key from keystore or falls back to known test key
func loadAggOracleKey(envDir, password string) (*ecdsa.PrivateKey, error) {
	path := filepath.Join(envDir, "config", "001", "aggoracle.keystore")
	key, err := loadKeystoreKey(path, password)
	if err == nil {
		return key, nil
	}
	// Fallback to hardcoded test key from runbook
	return parsePrivateKey("0x6d1d3ef5765cf34176d42276edd7a479ed5dc8dbf35182dfdb12e8aafe0a4919")
}

// loadSovereignAdminKey loads sovereign admin key from keystore or falls back to known test key
func loadSovereignAdminKey(envDir, password string) (*ecdsa.PrivateKey, error) {
	path := filepath.Join(envDir, "config", "001", "sovereignadmin.keystore")
	key, err := loadKeystoreKey(path, password)
	if err == nil {
		return key, nil
	}
	// Fallback to hardcoded test key from runbook
	return parsePrivateKey("0xa574853f4757bfdcbb59b03635324463750b27e16df897f3d00dc6bef2997ae0")
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
// Only stops if this Env instance started the docker compose environment
func (e *Env) Stop(ctx context.Context) error {
	if !e.startedCompose {
		// We didn't start docker compose, so don't stop it
		return nil
	}
	return stopDockerCompose(ctx, e.EnvDir)
}

const aggkitServiceName = "aggkit-001"

// StopAggkit stops only the aggkit service so the test can use the aggoracle key without conflicting with the running aggkit.
func (e *Env) StopAggkit(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "docker", "compose", "stop", aggkitServiceName)
	cmd.Dir = e.EnvDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose stop %s: %w\nOutput:\n%s", aggkitServiceName, err, string(output))
	}
	if len(output) > 0 {
		log.Debugf("docker compose stop %s output:\n%s\n", aggkitServiceName, string(output))
	}
	return nil
}

// StartAggkit starts the aggkit service and waits for the bridge service to be ready.
func (e *Env) StartAggkit(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "docker", "compose", "start", aggkitServiceName)
	cmd.Dir = e.EnvDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose start %s: %w\nOutput:\n%s", aggkitServiceName, err, string(output))
	}
	if len(output) > 0 {
		log.Debugf("docker compose start %s output:\n%s\n", aggkitServiceName, string(output))
	}
	if err := waitForBridgeService(ctx, e.bridgeServiceURL); err != nil {
		return fmt.Errorf("wait for bridge service after start: %w", err)
	}
	return nil
}

// ensureDockerComposeRunning ensures docker compose is running for the given environment
// Returns true if we started docker compose, false if it was already running
func ensureDockerComposeRunning(ctx context.Context, envDir string) (bool, error) {
	projectName := filepath.Base(envDir)
	alreadyRunning, err := isDockerComposeRunning(ctx, envDir)
	if err == nil && alreadyRunning {
		log.Debugf("docker compose is running for %s\n", projectName)
		return false, nil
	}

	// Not running, so start it
	networkName := projectName + "_default"

	// Step 1: Remove all containers from this project
	cleanupCmd := exec.CommandContext(ctx, "docker", "compose", "down", "-v", "--remove-orphans")
	cleanupCmd.Dir = envDir
	if cleanupOutput, err := cleanupCmd.CombinedOutput(); err != nil {
		log.Debugf("docker compose cleanup (ignored): %v\nOutput:\n%s\n", err, string(cleanupOutput))
	} else if len(cleanupOutput) > 0 {
		log.Debugf("docker compose cleanup output:\n%s\n", string(cleanupOutput))
	}

	// Step 2: Find and remove all networks with the project name (by ID to handle duplicates)
	listNetworksCmd := exec.CommandContext(ctx, "docker", "network", "ls", "--filter", "name="+networkName, "--format", "{{.ID}}")
	networkIDs, err := listNetworksCmd.Output()
	if err == nil && len(networkIDs) > 0 {
		// Split by newline and remove each network by ID
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

	log.Debugf("running docker compose up -d for %s\n", projectName)
	cmd := exec.CommandContext(ctx, "docker", "compose", "up", "-d")
	cmd.Dir = envDir

	// Capture output to include in error messages if the command fails
	output, err := cmd.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("docker compose up: %w\nOutput:\n%s", err, string(output))
	}

	// Log the output on success for debugging
	if len(output) > 0 {
		log.Debugf("docker compose up output:\n%s\n", string(output))
	}

	return true, nil
}

// stopDockerCompose stops the docker compose environment
func stopDockerCompose(ctx context.Context, envDir string) error {
	cmd := exec.CommandContext(ctx, "docker", "compose", "down", "-v", "--remove-orphans")
	cmd.Dir = envDir

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

// isDockerComposeRunning checks if docker compose is already running for this environment
func isDockerComposeRunning(ctx context.Context, envDir string) (bool, error) {
	cmd := exec.CommandContext(ctx, "docker", "compose", "ps", "-q")
	cmd.Dir = envDir

	output, err := cmd.Output()
	if err != nil {
		// If docker compose ps fails, assume it's not running
		return false, nil
	}

	// If there's any output, containers are running
	return len(output) > 0, nil
}

// waitForServices waits for all services to be ready
func waitForServices(ctx context.Context, summary *summaryJSON) error {
	// Wait for L1 geth to be ready
	if err := waitForEthereumService(ctx, summary.Networks.L1.Services.Geth.HTTPRpc.External); err != nil {
		return fmt.Errorf("wait for L1 geth: %w", err)
	}

	// Wait for L2 op-geth to be ready (assuming first network is "001")
	for _, l2Network := range summary.Networks.L2Networks {
		if err := waitForEthereumService(ctx, l2Network.Services.OpGeth.HTTPRpc.External); err != nil {
			return fmt.Errorf("wait for L2 op-geth: %w", err)
		}

		// Wait for bridge service to be ready
		if err := waitForBridgeService(ctx, l2Network.Services.Aggkit.BridgeService.External); err != nil {
			return fmt.Errorf("wait for bridge service: %w", err)
		}
		break // Only check first L2 network
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
