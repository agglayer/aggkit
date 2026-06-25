// Command agglayer_status prints the status and height of the latest agglayer
// certificate for an L2 network, using the same agglayer gRPC client as the
// exit_certificate tool. With -wait it polls until the latest certificate settles.
//
// Connection info is taken from the environment (AGGLAYER_GRPC_URL) so it composes
// with tools/exit_certificate/scripts/export_kurtosis_env.sh. It is normally invoked
// through the agglayer_certificate_status.sh wrapper.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/agglayer/aggkit/agglayer"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	aggkitgrpc "github.com/agglayer/aggkit/grpc"
	"github.com/agglayer/aggkit/log"
)

const (
	// defaultPollInterval is how often -wait polls the agglayer for settlement.
	defaultPollInterval = 5 * time.Second
	// defaultWaitTimeout is the maximum time -wait blocks before giving up.
	defaultWaitTimeout = 10 * time.Minute
)

// clientFactory builds an agglayer client. It matches agglayer.NewAgglayerClient and is
// injectable so run can be exercised in tests without a live endpoint.
type clientFactory func(agglayer.ClientConfig, aggkitcommon.Logger) (agglayer.AgglayerClientInterface, error)

func main() {
	if err := run(os.Args[1:], agglayer.NewAgglayerClient); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, newClient clientFactory) error {
	fs := flag.NewFlagSet("agglayer_status", flag.ContinueOnError)

	defGRPC := os.Getenv("AGGLAYER_GRPC_URL")
	defNetwork := uint(1)
	if v := os.Getenv("NETWORK_INDEX"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 32); err == nil {
			defNetwork = uint(n)
		}
	}

	grpcURL := fs.String("grpc", defGRPC, "agglayer gRPC endpoint (default: $AGGLAYER_GRPC_URL)")
	networkID := fs.Uint("network", defNetwork, "L2 network id (default: $NETWORK_INDEX or 1)")
	useTLS := fs.Bool("tls", false, "use TLS for the gRPC connection")
	wait := fs.Bool("wait", false, "poll until the latest certificate is Settled")
	interval := fs.Duration("interval", defaultPollInterval, "poll interval when -wait is set")
	timeout := fs.Duration("timeout", defaultWaitTimeout, "max time to wait with -wait (0 = no limit)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *grpcURL == "" {
		return errors.New("agglayer gRPC URL is required (set AGGLAYER_GRPC_URL or pass -grpc)")
	}

	logger := log.WithFields("module", "agglayer_status")

	grpcCfg := aggkitgrpc.DefaultConfig()
	grpcCfg.URL = *grpcURL
	grpcCfg.UseTLS = *useTLS

	client, err := newClient(agglayer.ClientConfig{GRPC: grpcCfg}, logger)
	if err != nil {
		return fmt.Errorf("create agglayer client: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	netID := uint32(*networkID)
	fmt.Printf("Agglayer gRPC: %s\n", *grpcURL)
	fmt.Printf("Network id:    %d\n", netID)

	if !*wait {
		return printStatus(ctx, client, netID)
	}

	return waitForSettled(ctx, client, netID, *interval, *timeout)
}

// printStatus shows the latest certificate (pending if any, otherwise settled).
func printStatus(ctx context.Context, client agglayer.AgglayerClientInterface, netID uint32) error {
	header, label, err := latestHeader(ctx, client, netID)
	if err != nil {
		return err
	}
	if header == nil {
		fmt.Printf("No certificate found for network %d yet.\n", netID)
	} else {
		printHeader(header, label)
	}
	return nil
}

// latestHeader returns the latest pending certificate header if one exists, else the
// latest settled one. The returned label describes which was found.
func latestHeader(
	ctx context.Context, client agglayer.AgglayerClientInterface, netID uint32,
) (*agglayertypes.CertificateHeader, string, error) {
	pending, err := client.GetLatestPendingCertificateHeader(ctx, netID)
	if err != nil {
		return nil, "", fmt.Errorf("get latest pending certificate header: %w", err)
	}
	if pending != nil {
		return pending, "Latest certificate (pending):", nil
	}
	settled, err := client.GetLatestSettledCertificateHeader(ctx, netID)
	if err != nil {
		return nil, "", fmt.Errorf("get latest settled certificate header: %w", err)
	}
	if settled != nil {
		return settled, "Latest certificate (settled):", nil
	}
	return nil, "", nil
}

func printHeader(h *agglayertypes.CertificateHeader, label string) {
	fmt.Println(label)
	fmt.Printf("  Status:               %s\n", h.Status.String())
	fmt.Printf("  Height:               %d\n", h.Height)
	fmt.Printf("  Certificate ID:       %s\n", h.CertificateID.Hex())
	fmt.Printf("  Epoch / Index:        %s / %s\n", u64ptr(h.EpochNumber), u64ptr(h.CertificateIndex))
	fmt.Printf("  New local exit root:  %s\n", h.NewLocalExitRoot.Hex())
	if h.PreviousLocalExitRoot != nil {
		fmt.Printf("  Prev local exit root: %s\n", h.PreviousLocalExitRoot.Hex())
	}
	if h.SettlementTxHash != nil {
		fmt.Printf("  Settlement tx hash:   %s\n", h.SettlementTxHash.Hex())
	}
	if h.Error != nil {
		fmt.Printf("  Error:                %s\n", h.Error.Error())
	}
}

// waitForSettled polls GetNetworkInfo until the latest certificate is settled (no open
// pending certificate). Returns an error if a pending certificate is in error state or
// the timeout elapses.
func waitForSettled(
	ctx context.Context, client agglayer.AgglayerClientInterface,
	netID uint32, interval, timeout time.Duration,
) error {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	fmt.Printf("Waiting until the latest certificate is settled (interval=%s, timeout=%s)...\n",
		interval, durStr(timeout))

	start := time.Now()
	for {
		info, err := client.GetNetworkInfo(ctx, netID)
		if err != nil {
			return fmt.Errorf("get network info: %w", err)
		}

		elapsed := time.Since(start).Round(time.Second)
		if info.LatestPendingStatus != nil {
			status := *info.LatestPendingStatus
			height := u64ptr(info.LatestPendingHeight)
			switch {
			case status.IsInError():
				return fmt.Errorf("latest certificate (height %s) is in error state", height)
			case status.IsSettled():
				fmt.Printf("Certificate settled (height %s).\n", height)
				return printStatus(ctx, client, netID)
			default:
				fmt.Printf("  height=%s status=%s — still waiting... (%s elapsed)\n", height, status.String(), elapsed)
			}
		} else {
			// No open pending certificate: whatever was submitted has settled.
			fmt.Printf("No pending certificate — latest is settled. (%s elapsed)\n", elapsed)
			return printStatus(ctx, client, netID)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for settlement after %s", durStr(timeout))
		case <-time.After(interval):
		}
	}
}

func u64ptr(p *uint64) string {
	if p == nil {
		return "—"
	}
	return strconv.FormatUint(*p, 10)
}

func durStr(d time.Duration) string {
	if d == 0 {
		return "none"
	}
	return d.String()
}
