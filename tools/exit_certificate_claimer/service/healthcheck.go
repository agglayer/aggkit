package claimer

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/urfave/cli/v2"
)

// HealthCheckDefaultPort is the port the healthcheck subcommand probes by default: the port the
// server itself binds when none is configured.
const HealthCheckDefaultPort = defaultPort

// HealthCheckDefaultTimeout bounds the healthcheck subcommand probe by default.
const HealthCheckDefaultTimeout = 5 * time.Second

// HealthCheckURL builds the URL of the health endpoint served at host:port.
func HealthCheckURL(host string, port int) string {
	return fmt.Sprintf("http://%s%s/health", net.JoinHostPort(host, strconv.Itoa(port)), apiBasePath)
}

// HealthCheck probes url and returns nil only if it answers 200 OK within timeout.
func HealthCheck(ctx context.Context, url string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return fmt.Errorf("building health request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("health request to %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health request to %s: unexpected status %s", url, resp.Status)
	}
	return nil
}

// RunHealthCheck is the urfave/cli action for the healthcheck subcommand. It probes the health
// endpoint of a running claimer and exits 0 (healthy) or non-zero (unhealthy), so it can back a
// container HEALTHCHECK in the shell-less production image.
func RunHealthCheck(c *cli.Context) error {
	url := HealthCheckURL(c.String("address"), c.Int("port"))
	if err := HealthCheck(c.Context, url, c.Duration("timeout")); err != nil {
		return err
	}
	fmt.Printf("healthy: %s\n", url)
	return nil
}
