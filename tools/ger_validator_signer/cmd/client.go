package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/agglayer/aggkit/tools/ger_validator_signer/api"
	"github.com/agglayer/aggkit/tools/ger_validator_signer/client"
	"github.com/agglayer/aggkit/tools/ger_validator_signer/config"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

const (
	// urlEnvVar is where the URL of the validator is taken from if --url is not set
	urlEnvVar  = "GER_VALIDATOR_URL"
	defaultURL = "http://localhost:8080"

	maxRequests = 100000
	maxParallel = 1000

	outcomeSigned          = "signed"
	outcomeNoAnswer        = "no answer"
	outcomeInvalidResponse = "invalid response"
	outcomeBadSignature    = "bad signature"
)

func clientUsage(output io.Writer) {
	_, _ = fmt.Fprintf(output, `Client of the REST API of a GER validator, to call one by hand.

Usage:
  ger_validator_signer client info   [flags]               version, signer, finality and L1 of the validator
  ger_validator_signer client health [flags]               is the validator running
  ger_validator_signer client sign   --ger <0x...> [flags] ask the validator to sign a GER and verify the signature

Flags of every command:
  --url <url>        URL of the validator (default $%s, or %s)
  --timeout <time>   timeout of each request (default %s)
  --ca-cert <file>   PEM file with the CA of the certificate of the validator, if it is not a public one
  --json             print the result as JSON

Flags of sign:
  --ger <0x...>             the GER, 32 bytes in hex (required)
  --expect-signer <0x...>   the address that must have signed: the one registered in the committee
  --count <n>               send the request n times (default 1), and print how many got each answer
  --parallel <n>            how many requests at the same time (default 1)

The signature is always verified as the contract does: 65 bytes, v 27 or 28, s in the lower half of the curve, over
keccak256(ger), and it must recover the signer.
Exit code: 0 ok, 1 the validator did not sign or did not answer, 2 usage.
`, urlEnvVar, defaultURL, client.DefaultTimeout)
}

// clientFlags are the flags that every command of the client has
type clientFlags struct {
	url        string
	timeout    time.Duration
	caCert     string
	jsonOutput bool
}

func newClientFlagSet(name string, stderr io.Writer) (*flag.FlagSet, *clientFlags) {
	flags := flag.NewFlagSet("client "+name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	shared := &clientFlags{}
	defaultValue := os.Getenv(urlEnvVar)
	if defaultValue == "" {
		defaultValue = defaultURL
	}
	flags.StringVar(&shared.url, "url", defaultValue, "URL of the validator")
	flags.DurationVar(&shared.timeout, "timeout", client.DefaultTimeout, "timeout of each request")
	flags.StringVar(&shared.caCert, "ca-cert", "", "PEM file with the CA of the certificate of the validator")
	flags.BoolVar(&shared.jsonOutput, "json", false, "print the result as JSON")
	return flags, shared
}

func (f *clientFlags) newClient() (*client.Client, error) {
	return client.New(client.Config{BaseURL: f.url, Timeout: f.timeout, CACertFile: f.caCert})
}

// clientCommand runs the commands of the client of the validator, and returns the exit code of the process
func clientCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		clientUsage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "info":
		return clientInfo(args[1:], stdout, stderr)
	case "health":
		return clientHealth(args[1:], stdout, stderr)
	case "sign":
		return clientSign(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		clientUsage(stdout)
		return exitOK
	default:
		_, _ = fmt.Fprintf(stderr, "unknown client command %q\n\n", args[0])
		clientUsage(stderr)
		return exitUsage
	}
}

// parseClientFlags parses the flags and returns the client. The second value is false if the command must stop, and
// has printed why (the exit code is the third)
func parseClientFlags(
	flags *flag.FlagSet, shared *clientFlags, args []string, stderr io.Writer,
) (*client.Client, int, bool) {
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, exitOK, false
		}
		return nil, exitUsage, false
	}
	if flags.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "unexpected argument %q (the flags go with their value: --ger 0x...)\n", flags.Arg(0))
		return nil, exitUsage, false
	}
	validatorClient, err := shared.newClient()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "error:", err)
		return nil, exitUsage, false
	}
	return validatorClient, exitOK, true
}

func clientHealth(args []string, stdout, stderr io.Writer) int {
	flags, shared := newClientFlagSet("health", stderr)
	validatorClient, code, ok := parseClientFlags(flags, shared, args, stderr)
	if !ok {
		return code
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	health, err := validatorClient.Health(ctx)
	if err != nil {
		return reportFailure(shared.jsonOutput, stdout, stderr, err)
	}
	if shared.jsonOutput {
		return printJSON(stdout, stderr, health)
	}
	_, _ = fmt.Fprintln(stdout, health.Status)
	return exitOK
}

func clientInfo(args []string, stdout, stderr io.Writer) int {
	flags, shared := newClientFlagSet("info", stderr)
	validatorClient, code, ok := parseClientFlags(flags, shared, args, stderr)
	if !ok {
		return code
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	info, err := validatorClient.Info(ctx)
	if err != nil {
		return reportFailure(shared.jsonOutput, stdout, stderr, err)
	}
	if shared.jsonOutput {
		return printJSON(stdout, stderr, info)
	}
	_, _ = fmt.Fprintf(stdout, `Version:      %s (git %s, branch %s, built %s)
Signer:       %s
AgglayerGER:  %s
L1 chain id:  %d
Finality:     %s
`, info.Version, info.GitRev, info.GitBranch, info.BuildDate, info.Signer.Hex(), info.AgglayerGER.Hex(),
		info.L1ChainID, config.BlockFinality{Tag: info.Finality.Tag, NumBlocks: info.Finality.NumBlocks})
	return exitOK
}

// signFlags are the flags of the command sign
type signFlags struct {
	ger            string
	expectedSigner string
	count          int
	parallel       int
}

func clientSign(args []string, stdout, stderr io.Writer) int {
	flags, shared := newClientFlagSet("sign", stderr)
	sign := &signFlags{}
	flags.StringVar(&sign.ger, "ger", "", "the GER to sign, 32 bytes in hex")
	flags.StringVar(&sign.expectedSigner, "expect-signer", "", "the address that must have signed")
	flags.IntVar(&sign.count, "count", 1, "how many requests to send")
	flags.IntVar(&sign.parallel, "parallel", 1, "how many requests at the same time")
	validatorClient, code, ok := parseClientFlags(flags, shared, args, stderr)
	if !ok {
		return code
	}
	request, err := sign.validate()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "error:", err)
		return exitUsage
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if sign.count == 1 {
		result := signOnce(ctx, validatorClient, request)
		return reportSign(shared.jsonOutput, stdout, stderr, request, result)
	}
	return reportBurst(shared.jsonOutput, stdout, stderr, runBurst(ctx, validatorClient, request, sign))
}

// signRequest is what to ask a validator to sign, and what to expect in the answer
type signRequest struct {
	ger      common.Hash
	expected *common.Address
}

func (f *signFlags) validate() (signRequest, error) {
	var request signRequest
	if f.ger == "" {
		return request, errors.New("the flag --ger is required")
	}
	gerBytes, err := hexutil.Decode(f.ger)
	if err != nil || len(gerBytes) != common.HashLength {
		return request, errors.New("--ger must be 32 bytes in hex, with the 0x prefix")
	}
	request.ger = common.BytesToHash(gerBytes)
	if f.expectedSigner != "" {
		if !common.IsHexAddress(f.expectedSigner) {
			return request, errors.New("--expect-signer must be an address")
		}
		address := common.HexToAddress(f.expectedSigner)
		request.expected = &address
	}
	if f.count < 1 || f.count > maxRequests {
		return request, fmt.Errorf("--count must be between 1 and %d", maxRequests)
	}
	if f.parallel < 1 || f.parallel > maxParallel {
		return request, fmt.Errorf("--parallel must be between 1 and %d", maxParallel)
	}
	return request, nil
}

// signResult is what happened with one request to sign
type signResult struct {
	response *api.SignResponse
	// err is why it was not signed: an *client.Error (the validator said no), a request that failed, a response that
	// is not the API, or a signature that is not right
	err     error
	elapsed time.Duration
}

// outcomeOf groups the results: signed (no error), the code of the validator that said no, or what failed
func outcomeOf(err error) string {
	var apiErr *client.Error
	switch {
	case err == nil:
		return outcomeSigned
	case errors.As(err, &apiErr) && apiErr.Code != "":
		return apiErr.Code
	case errors.As(err, &apiErr):
		return fmt.Sprintf("HTTP %d", apiErr.Status)
	case errors.Is(err, client.ErrBadSignature):
		return outcomeBadSignature
	case errors.Is(err, client.ErrInvalidResponse):
		return outcomeInvalidResponse
	default:
		return outcomeNoAnswer
	}
}

func signOnce(ctx context.Context, validatorClient *client.Client, request signRequest) signResult {
	start := time.Now()
	response, err := validatorClient.SignGER(ctx, request.ger)
	if err == nil {
		// The signature is checked even if the validator is the one that says who signed
		err = client.Verify(request.ger, response, request.expected)
	}
	return signResult{response: response, err: err, elapsed: time.Since(start)}
}

// signJSON is the result of one request to sign, as JSON
type signJSON struct {
	GER       common.Hash     `json:"ger"`
	Signature hexutil.Bytes   `json:"signature,omitempty"`
	Signer    *common.Address `json:"signer,omitempty"`
	Verified  bool            `json:"verified"`
	Error     *errorJSON      `json:"error,omitempty"`
	ElapsedMS float64         `json:"elapsed_ms"`
}

// errorJSON is why a request failed, as JSON
type errorJSON struct {
	// Outcome is the code of the validator, or what failed
	Outcome           string `json:"outcome"`
	Status            int    `json:"status,omitempty"`
	Message           string `json:"message"`
	Retryable         bool   `json:"retryable"`
	RetryAfterSeconds int    `json:"retry_after_seconds,omitempty"`
}

func newErrorJSON(outcome string, err error) *errorJSON {
	result := &errorJSON{Outcome: outcome, Message: err.Error()}
	var apiErr *client.Error
	if errors.As(err, &apiErr) {
		result.Status = apiErr.Status
		result.Message = apiErr.Message
		result.Retryable = apiErr.Retryable()
		result.RetryAfterSeconds = int(apiErr.RetryAfter.Seconds())
	}
	return result
}

func milliseconds(duration time.Duration) float64 {
	return float64(duration.Microseconds()) / 1000 //nolint:mnd
}

func reportSign(jsonOutput bool, stdout, stderr io.Writer, request signRequest, result signResult) int {
	if jsonOutput {
		out := signJSON{GER: request.ger, ElapsedMS: milliseconds(result.elapsed)}
		if result.response != nil {
			out.Signature = result.response.Signature
			out.Signer = &result.response.Signer
		}
		if result.err == nil {
			out.Verified = true
		} else {
			out.Error = newErrorJSON(outcomeOf(result.err), result.err)
		}
		code := printJSON(stdout, stderr, out)
		if result.err != nil && code == exitOK {
			return exitError
		}
		return code
	}

	if result.err != nil {
		_, _ = fmt.Fprintf(stderr, "the GER %s was not signed (%s): %s\n", request.ger,
			result.elapsed.Round(time.Microsecond), result.err)
		var apiErr *client.Error
		if errors.As(result.err, &apiErr) && apiErr.Retryable() {
			retryAfter := ""
			if apiErr.RetryAfter > 0 {
				retryAfter = fmt.Sprintf(", the validator asks to wait %s", apiErr.RetryAfter)
			}
			_, _ = fmt.Fprintf(stderr, "it can be retried later%s\n", retryAfter)
		}
		return exitError
	}
	_, _ = fmt.Fprintf(stdout, `GER:        %s
Signer:     %s
Signature:  %s
Verified:   yes: v %d, s in the lower half, it recovers the signer
Time:       %s
`, result.response.GER, result.response.Signer, result.response.Signature,
		result.response.Signature[len(result.response.Signature)-1], result.elapsed.Round(time.Microsecond))
	return exitOK
}

// burstResult is what happened with the requests of a burst
type burstResult struct {
	count    int
	parallel int
	elapsed  time.Duration
	// outcomes counts the results of each kind
	outcomes map[string]int
	// interrupted is true if the user stopped it before the end
	interrupted bool
	min, max    time.Duration
	total       time.Duration
	done        int
}

// runBurst sends the requests of the flags, with the parallelism of the flags, and counts the answers
func runBurst(ctx context.Context, validatorClient *client.Client, request signRequest, flags *signFlags) burstResult {
	burst := burstResult{count: flags.count, parallel: flags.parallel, outcomes: map[string]int{}}
	jobs := make(chan struct{})
	var mutex sync.Mutex
	var workers sync.WaitGroup

	start := time.Now()
	for range flags.parallel {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range jobs {
				result := signOnce(ctx, validatorClient, request)
				mutex.Lock()
				burst.outcomes[outcomeOf(result.err)]++
				burst.done++
				burst.total += result.elapsed
				if burst.done == 1 || result.elapsed < burst.min {
					burst.min = result.elapsed
				}
				burst.max = max(burst.max, result.elapsed)
				mutex.Unlock()
			}
		}()
	}
feed:
	for range flags.count {
		select {
		case jobs <- struct{}{}:
		case <-ctx.Done():
			burst.interrupted = true
			break feed
		}
	}
	close(jobs)
	workers.Wait()
	burst.elapsed = time.Since(start)
	return burst
}

// burstJSON is the result of a burst, as JSON
type burstJSON struct {
	Requests    int            `json:"requests"`
	Done        int            `json:"done"`
	Parallel    int            `json:"parallel"`
	Interrupted bool           `json:"interrupted,omitempty"`
	TotalMS     float64        `json:"total_ms"`
	MinMS       float64        `json:"min_ms"`
	AvgMS       float64        `json:"avg_ms"`
	MaxMS       float64        `json:"max_ms"`
	Outcomes    map[string]int `json:"outcomes"`
}

func reportBurst(jsonOutput bool, stdout, stderr io.Writer, burst burstResult) int {
	var average time.Duration
	if burst.done > 0 {
		average = burst.total / time.Duration(burst.done)
	}
	allSigned := !burst.interrupted && burst.outcomes[outcomeSigned] == burst.count
	code := exitOK
	if !allSigned {
		code = exitError
	}

	if jsonOutput {
		printed := printJSON(stdout, stderr, burstJSON{
			Requests: burst.count, Done: burst.done, Parallel: burst.parallel, Interrupted: burst.interrupted,
			TotalMS: milliseconds(burst.elapsed), MinMS: milliseconds(burst.min), AvgMS: milliseconds(average),
			MaxMS: milliseconds(burst.max), Outcomes: burst.outcomes,
		})
		if printed != exitOK {
			return printed
		}
		return code
	}

	_, _ = fmt.Fprintf(stdout, "%d of %d requests, %d at a time, in %s", burst.done, burst.count, burst.parallel,
		burst.elapsed.Round(time.Millisecond))
	if burst.interrupted {
		_, _ = fmt.Fprint(stdout, " (interrupted)")
	}
	_, _ = fmt.Fprintln(stdout)
	outcomes := make([]string, 0, len(burst.outcomes))
	for outcome := range burst.outcomes {
		outcomes = append(outcomes, outcome)
	}
	// The most frequent first, the signed ones before the rest in a tie
	sort.Slice(outcomes, func(i, j int) bool {
		if burst.outcomes[outcomes[i]] != burst.outcomes[outcomes[j]] {
			return burst.outcomes[outcomes[i]] > burst.outcomes[outcomes[j]]
		}
		return outcomes[i] < outcomes[j]
	})
	for _, outcome := range outcomes {
		_, _ = fmt.Fprintf(stdout, "  %-24s %d\n", outcome, burst.outcomes[outcome])
	}
	_, _ = fmt.Fprintf(stdout, "latency: min %s, avg %s, max %s\n", burst.min.Round(time.Microsecond),
		average.Round(time.Microsecond), burst.max.Round(time.Microsecond))
	return code
}

// reportFailure prints why info or health failed
func reportFailure(jsonOutput bool, stdout, stderr io.Writer, err error) int {
	if jsonOutput {
		if code := printJSON(stdout, stderr, struct {
			Error *errorJSON `json:"error"`
		}{newErrorJSON(outcomeOf(err), err)}); code != exitOK {
			return code
		}
		return exitError
	}
	_, _ = fmt.Fprintln(stderr, "error:", err)
	return exitError
}

func printJSON(stdout, stderr io.Writer, value any) int {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		_, _ = fmt.Fprintln(stderr, "error:", strings.TrimSpace(err.Error()))
		return exitError
	}
	return exitOK
}
