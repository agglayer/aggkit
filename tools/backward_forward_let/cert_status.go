package backward_forward_let

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/agglayer/aggkit/agglayer"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/log"
	"github.com/urfave/cli/v2"
)

const DefaultCertStatusTimeout = 30 * time.Minute

// RunCertStatus prints AggLayer certificate settlement and pending status.
func RunCertStatus(c *cli.Context) error {
	cfg, err := LoadConfig(c)
	if err != nil {
		return err
	}
	client, err := agglayer.NewAgglayerClient(cfg.AgglayerClient, log.GetDefaultLogger())
	if err != nil {
		return fmt.Errorf("create agglayer client: %w", err)
	}

	timeout := c.Duration("timeout")
	if timeout <= 0 {
		timeout = DefaultCertStatusTimeout
	}

	if c.Bool("wait-no-pending") || c.Bool("wait-settled") {
		deadline := time.Now().Add(timeout)
		for {
			info, _, err := getNetworkInfoAllowNotFound(c.Context, client, cfg.BackwardForwardLET.L2NetworkID)
			if err != nil {
				return err
			}
			if c.Bool("wait-no-pending") && !hasOpenPendingAtOrAbove(info, 0) {
				printCertStatus(info, cfg.BackwardForwardLET.L2NetworkID, c.Uint64("height"))
				fmt.Println("Wait complete: no open pending certificate.")
				return nil
			}
			if c.Bool("wait-settled") {
				height := c.Uint64("height")
				if info.SettledHeight != nil && *info.SettledHeight >= height {
					printCertStatus(info, cfg.BackwardForwardLET.L2NetworkID, height)
					fmt.Printf("Wait complete: height %d is settled.\n", height)
					return nil
				}
			}
			if time.Now().After(deadline) {
				printCertStatus(info, cfg.BackwardForwardLET.L2NetworkID, c.Uint64("height"))
				return fmt.Errorf("timed out after %s waiting for requested certificate status", timeout)
			}
			select {
			case <-c.Context.Done():
				return c.Context.Err()
			case <-time.After(15 * time.Second):
			}
		}
	}

	info, _, err := getNetworkInfoAllowNotFound(context.Background(), client, cfg.BackwardForwardLET.L2NetworkID)
	if err != nil {
		return err
	}
	printCertStatus(info, cfg.BackwardForwardLET.L2NetworkID, c.Uint64("height"))
	return nil
}

func printCertStatus(info agglayertypes.NetworkInfo, networkID uint32, requestedHeight uint64) {
	printCertStatusTo(os.Stdout, info, networkID, requestedHeight)
}

func printCertStatusTo(w io.Writer, info agglayertypes.NetworkInfo, networkID uint32, requestedHeight uint64) {
	fmt.Fprintf(w, "Network ID: %d\n", networkID)
	if info.SettledHeight == nil {
		fmt.Fprintln(w, "Latest settled height: none")
	} else {
		fmt.Fprintf(w, "Latest settled height: %d\n", *info.SettledHeight)
	}
	if info.SettledCertificateID != nil {
		fmt.Fprintf(w, "Latest settled certificate ID: %s\n", info.SettledCertificateID.Hex())
	}
	if info.SettledLER != nil {
		fmt.Fprintf(w, "Latest settled LER: %s\n", info.SettledLER.Hex())
	}
	if info.SettledLETLeafCount != nil {
		fmt.Fprintf(w, "Latest settled deposit count: %d\n", *info.SettledLETLeafCount)
	}

	if info.LatestPendingHeight == nil {
		fmt.Fprintln(w, "Latest pending certificate: none")
	} else {
		fmt.Fprintf(w, "Latest pending height: %d\n", *info.LatestPendingHeight)
		status := "unknown"
		if info.LatestPendingStatus != nil {
			status = info.LatestPendingStatus.String()
		}
		fmt.Fprintf(w, "Latest pending status: %s\n", status)
		if info.LatestPendingError != "" {
			fmt.Fprintf(w, "Latest pending error: %s\n", info.LatestPendingError)
		}
	}

	if requestedHeight > 0 || (info.SettledHeight != nil && *info.SettledHeight == 0) {
		fmt.Fprintf(w, "Requested height: %d\n", requestedHeight)
		switch {
		case info.SettledHeight != nil && *info.SettledHeight >= requestedHeight:
			fmt.Fprintf(w, "Requested height status: Settled\n")
		case info.LatestPendingHeight != nil && *info.LatestPendingHeight == requestedHeight && info.LatestPendingStatus != nil:
			fmt.Fprintf(w, "Requested height status: %s\n", info.LatestPendingStatus.String())
		default:
			fmt.Fprintln(w, "Requested height status: not settled")
		}
	}
}
