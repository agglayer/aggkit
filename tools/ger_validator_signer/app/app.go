// Package app puts the GER validator signer together: the signer, the L1 client, the service and the REST API.
package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strconv"

	"github.com/agglayer/aggkit/tools/ger_validator_signer/api"
	"github.com/agglayer/aggkit/tools/ger_validator_signer/cache"
	"github.com/agglayer/aggkit/tools/ger_validator_signer/config"
	"github.com/agglayer/aggkit/tools/ger_validator_signer/finality"
	"github.com/agglayer/aggkit/tools/ger_validator_signer/l1"
	"github.com/agglayer/aggkit/tools/ger_validator_signer/service"
	"github.com/agglayer/aggkit/tools/ger_validator_signer/signer"
	"github.com/agglayer/aggkit/tools/ger_validator_signer/version"
	"github.com/ethereum/go-ethereum/common"
)

// startupChecksTimeoutFactor is how many L1 requests the startup checks can take, at most, each one limited by
// L1RequestTimeout
const startupChecksTimeoutFactor = 3

// logFileMode is the mode of the log files that are created
const logFileMode = 0o640

// L1StartupReader is what the startup checks need from L1
type L1StartupReader interface {
	// ChainID returns the chain id of the endpoint
	ChainID(ctx context.Context) (uint64, error)
	// CodeAt returns the code of a contract
	CodeAt(ctx context.Context, addr common.Address) ([]byte, error)
	// GlobalExitRootMap reads AgglayerGER.globalExitRootMap(ger)
	GlobalExitRootMap(ctx context.Context, agglayerGER common.Address, ger common.Hash) (common.Hash, error)
}

// CheckL1 makes sure the validator is pointed to the right place before it signs anything: the endpoint is the
// configured chain, and the configured address is a contract that answers globalExitRootMap.
func CheckL1(ctx context.Context, reader L1StartupReader, cfg config.Config) error {
	chainID, err := reader.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("failed to get the chain id of L1: %w", err)
	}
	if chainID != cfg.L1ChainID {
		return fmt.Errorf("the L1 endpoint is the chain %d, but L1ChainID is %d", chainID, cfg.L1ChainID)
	}

	code, err := reader.CodeAt(ctx, cfg.AgglayerGERAddr)
	if err != nil {
		return fmt.Errorf("failed to get the code of AgglayerGERAddr %s: %w", cfg.AgglayerGERAddr, err)
	}
	if len(code) == 0 {
		return fmt.Errorf("there is no contract at AgglayerGERAddr %s on the chain %d", cfg.AgglayerGERAddr, chainID)
	}

	// The zero GER is never inserted: the contract answers zero, if it is AgglayerGER
	if _, err := reader.GlobalExitRootMap(ctx, cfg.AgglayerGERAddr, common.Hash{}); err != nil {
		return fmt.Errorf("AgglayerGERAddr %s doesn't answer globalExitRootMap (is it the AgglayerGER contract?): %w",
			cfg.AgglayerGERAddr, err)
	}
	return nil
}

// App is the GER validator signer, ready to serve
type App struct {
	logger *slog.Logger
	cfg    config.Config
	server *api.Server
}

// New creates the signer, checks L1 and creates the service and the REST API. It fails if anything is not as
// configured, so a validator that can't work doesn't start.
func New(ctx context.Context, cfg config.Config, logger *slog.Logger) (*App, error) {
	validatorSigner, err := signer.New(ctx, cfg.Signer, cfg.L1ChainID, logger)
	if err != nil {
		return nil, err
	}

	l1Client := l1.NewClient(cfg.L1RPCURL, cfg.L1RequestTimeout.Duration)
	checkCtx, cancel := context.WithTimeout(ctx, startupChecksTimeoutFactor*cfg.L1RequestTimeout.Duration)
	defer cancel()
	if err := CheckL1(checkCtx, l1Client, cfg); err != nil {
		return nil, err
	}

	svc := service.New(logger, service.Config{
		AgglayerGER:    cfg.AgglayerGERAddr,
		Finality:       finality.Rule{Tag: cfg.BlockFinality.Tag, NumBlocks: cfg.BlockFinality.NumBlocks},
		NegativeTTL:    cfg.Cache.NegativeTTL.Duration,
		RequestTimeout: cfg.PublicREST.RequestTimeout.Duration,
	}, l1Client, validatorSigner, cache.New(cfg.Cache.MaxEntries))

	logger.Info("GER validator signer ready",
		"version", version.Get().Version,
		"gitRev", version.Get().GitRev,
		"signer", validatorSigner.Address(),
		"l1ChainID", cfg.L1ChainID,
		"agglayerGER", cfg.AgglayerGERAddr,
		"blockFinality", cfg.BlockFinality.String(),
		"tls", cfg.PublicREST.TLSCertFile != "",
	)
	if cfg.PublicREST.InsecureHTTP {
		logger.Warn("serving HTTP without TLS: the signatures travel in the clear " +
			"unless the TLS is terminated in front of the validator")
	}

	return &App{logger: logger, cfg: cfg, server: api.New(logger, cfg, svc)}, nil
}

// Serve serves the REST API on the listener until the context is done
func (a *App) Serve(ctx context.Context, listener net.Listener) error {
	a.logger.Info("REST API listening", "address", listener.Addr().String())
	return a.server.Serve(ctx, listener)
}

// Run creates the validator and serves it on the configured address until the context is done
func Run(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	validator, err := New(ctx, cfg, logger)
	if err != nil {
		return err
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp",
		net.JoinHostPort(cfg.PublicREST.Host, strconv.Itoa(int(cfg.PublicREST.Port))))
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}
	return validator.Serve(ctx, listener)
}

// NewLogger creates the logger of the [Log] section, as aggkit: JSON in production and text in development, at the
// configured level, written to every output. The output "stderr" is the stderr writer, "stdout" the standard output
// and any other one the path of a file, where the logs are appended.
func NewLogger(cfg config.LogConfig, stderr io.Writer) (*slog.Logger, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.Level)); err != nil {
		return nil, fmt.Errorf("invalid log level %q", cfg.Level)
	}
	writers := make([]io.Writer, 0, len(cfg.Outputs))
	for _, output := range cfg.Outputs {
		switch output {
		case "stderr":
			writers = append(writers, stderr)
		case "stdout":
			writers = append(writers, os.Stdout)
		default:
			file, err := os.OpenFile(output, os.O_CREATE|os.O_WRONLY|os.O_APPEND, logFileMode)
			if err != nil {
				return nil, fmt.Errorf("failed to open the log output %s: %w", output, err)
			}
			writers = append(writers, file)
		}
	}
	output := io.MultiWriter(writers...)
	options := &slog.HandlerOptions{Level: level}
	if cfg.Environment == config.LogEnvironmentProduction {
		return slog.New(slog.NewJSONHandler(output, options)), nil
	}
	return slog.New(slog.NewTextHandler(output, options)), nil
}
