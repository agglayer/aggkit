package bridgelooptester

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	bridgeserviceclient "github.com/agglayer/aggkit/bridgeservice/client"
	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	trackerapi "github.com/agglayer/aggkit/bridgetracker/api"
	trackertypes "github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
)

// ErrDeadlineExceeded is the sentinel every *DeadlineExceededError wraps (via Is), so a caller
// that only cares about "did a gate time out" can use errors.Is(err, ErrDeadlineExceeded) without
// switching on the concrete gate name. See DeadlineExceededError for the diagnosable form.
var ErrDeadlineExceeded = errors.New("bridge_loop_tester: deadline exceeded waiting for a gate")

// ErrL1BridgeServiceUnavailable is returned by (*ProxyClient).BridgeAddresses(ctx, 0) when the
// aggkit-proxy's bridgeservicefinder has no URL cached for network_id=0 at all (a routing-level
// 404, not a "not indexed yet" one). Per DESIGN.md Gap G4, network_id=0's routing depends on an
// env-specific BridgeServiceFinder.BridgeURLs[0] setting rather than a protocol guarantee: a
// proxy deployment that never sets it will 404 every network_id=0 call forever, so this is
// reported as a distinct, fail-fast sentinel instead of the ordinary "retry later" ErrNotFound -
// callers must not wait it out the way they would wait out an L1InfoTreeIndex/InjectedLeaf/
// ClaimProof 404. It still wraps bridgeserviceclient.ErrNotFound, so a caller that does not care
// about the distinction can still match on that.
var ErrL1BridgeServiceUnavailable = errors.New(
	"bridge_loop_tester: aggkit-proxy has no L1 (network_id=0) bridge service configured")

// DeadlineExceededError reports that a Wait* helper's configured deadline elapsed before its gate
// condition became true. It names the gate and carries the concrete inputs that were being waited
// on, so a stalled soak run is diagnosable from one log line, plus the last retry-class error
// observed (bridgeserviceclient.ErrNotFound or bridgeserviceclient.ErrServiceUnavailable), if any.
//
// errors.Is(err, ErrDeadlineExceeded) reports true for any *DeadlineExceededError, regardless of
// gate; errors.Is(err, bridgeserviceclient.ErrNotFound) / errors.Is(err,
// bridgeserviceclient.ErrServiceUnavailable) additionally reports true when that was the last
// observed condition before the deadline (via Unwrap).
type DeadlineExceededError struct {
	// Gate names the wire call that never became ready, e.g. "l1-info-tree-index",
	// "injected-l1-info-leaf", "claim-proof" or "claimed" (DESIGN.md §2/§4).
	Gate string
	// Detail carries the concrete inputs of the call that never became ready (network ids,
	// deposit count, leaf index, global index), verbatim in the query-parameter names used on the
	// wire, so the failing call can be replayed by hand.
	Detail string
	// Deadline is the configured deadline that elapsed.
	Deadline time.Duration
	// LastErr is the last retry-class error observed before the deadline elapsed (always either
	// bridgeserviceclient.ErrNotFound or bridgeserviceclient.ErrServiceUnavailable), or nil if the
	// deadline elapsed before a single attempt completed.
	LastErr error
}

// Error renders the gate, its concrete inputs, the configured deadline and the last observed
// retry-class error, so a stalled soak run is diagnosable from this one line alone.
func (e *DeadlineExceededError) Error() string {
	if e.LastErr != nil {
		return fmt.Sprintf("bridge_loop_tester: gate %q (%s) not satisfied within %s: last error: %v",
			e.Gate, e.Detail, e.Deadline, e.LastErr)
	}
	return fmt.Sprintf("bridge_loop_tester: gate %q (%s) not satisfied within %s", e.Gate, e.Detail, e.Deadline)
}

// Unwrap returns the last retry-class error observed before the deadline elapsed (or nil), so
// errors.Is(err, bridgeserviceclient.ErrNotFound) / errors.Is(err,
// bridgeserviceclient.ErrServiceUnavailable) still reach it.
func (e *DeadlineExceededError) Unwrap() error { return e.LastErr }

// Is reports whether target is ErrDeadlineExceeded, so errors.Is(err, ErrDeadlineExceeded) matches
// any *DeadlineExceededError regardless of which gate it names.
func (e *DeadlineExceededError) Is(target error) bool { return errors.Is(target, ErrDeadlineExceeded) }

// isRetryGate reports whether err is one of the two sentinels a Wait* poll retries on:
// bridgeserviceclient.ErrNotFound ("not ready yet") or bridgeserviceclient.ErrServiceUnavailable
// ("transiently unable to answer, e.g. a syncer resolving a reorg"). Any other non-nil error is a
// hard failure the caller must not retry (a genuine 4xx/5xx, a transport error, or ctx
// cancellation) - see the package-level error taxonomy in the ProxyClient doc comment.
func isRetryGate(err error) bool {
	return errors.Is(err, bridgeserviceclient.ErrNotFound) || errors.Is(err, bridgeserviceclient.ErrServiceUnavailable)
}

// pollGate repeatedly calls fetch, sleeping pollInterval between attempts, until it returns a nil
// error (success), a non-retry-class error (hard failure - returned immediately, unmodified, no
// further retries), or deadline elapses (returns a *DeadlineExceededError naming gate/detail). ctx
// cancellation is honoured promptly: it aborts an in-flight fetch immediately (via the context
// fetch itself receives) and is checked again the instant a poll wakes up, so a caller does not
// wait out a full poll interval after cancelling.
func pollGate[T any](
	ctx context.Context, pollInterval, deadline time.Duration, gate, detail string,
	fetch func(ctx context.Context) (T, error),
) (T, error) {
	var zero T
	if pollInterval <= 0 {
		return zero, fmt.Errorf("bridge_loop_tester: poll gate %q: pollInterval must be > 0, got %s",
			gate, pollInterval)
	}
	if deadline <= 0 {
		return zero, fmt.Errorf("bridge_loop_tester: poll gate %q: deadline must be > 0, got %s", gate, deadline)
	}

	deadlineCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	var lastErr error
	for {
		value, err := fetch(deadlineCtx)
		if err == nil {
			return value, nil
		}

		// A request that was in flight exactly when deadlineCtx (or ctx itself) expired surfaces
		// as a plain wrapped context.DeadlineExceeded/context.Canceled, not as
		// ErrNotFound/ErrServiceUnavailable - classify that race explicitly before falling
		// through to the retry/hard-failure branches below, so it is never mistaken for a hard
		// failure.
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			if parentErr := ctx.Err(); parentErr != nil {
				return zero, fmt.Errorf("bridge_loop_tester: poll gate %q (%s) cancelled: %w", gate, detail, parentErr)
			}
			return zero, &DeadlineExceededError{Gate: gate, Detail: detail, Deadline: deadline, LastErr: lastErr}
		}

		if !isRetryGate(err) {
			return zero, err
		}
		lastErr = err

		select {
		case <-deadlineCtx.Done():
			if parentErr := ctx.Err(); parentErr != nil {
				return zero, fmt.Errorf("bridge_loop_tester: poll gate %q (%s) cancelled: %w", gate, detail, parentErr)
			}
			return zero, &DeadlineExceededError{Gate: gate, Detail: detail, Deadline: deadline, LastErr: lastErr}
		case <-ticker.C:
		}
	}
}

// Proxy is the proxy-facing observation layer the hop state machine (S6) depends on: every
// /bridge/v1 + /tracker/v1 read the state machine needs to drive itself, expressed as an
// interface so a test can substitute a mock instead of an httptest server (see mocks.Proxy).
// *ProxyClient is the concrete implementation, built with NewProxyClient.
//
// # Error taxonomy
//
// Every method below classifies every error it can return into exactly one of:
//
//   - "not ready yet, retry": bridgeserviceclient.ErrNotFound. The Wait* methods retry on this
//     internally and never return it directly (a caller only ever sees it wrapped inside a
//     *DeadlineExceededError's LastErr, if the deadline elapses while this was the last observed
//     condition); BridgeAddresses returns it directly (or, for network 0, wrapped in
//     ErrL1BridgeServiceUnavailable - see Gap G4 below) since it has no retry loop of its own.
//   - "transient/service unavailable, retry": bridgeserviceclient.ErrServiceUnavailable (HTTP
//     503, e.g. a syncer resolving a reorg). Retried exactly like ErrNotFound by every Wait*
//     method.
//   - "hard failure, abort": any other non-nil error (a genuine 4xx/5xx status, a transport
//     failure, a JSON decode failure). Returned immediately, unmodified, with no retry - a Wait*
//     method's poll loop stops the instant it sees one of these.
//   - "deadline exceeded, report which gate": *DeadlineExceededError, returned by every Wait*
//     method when its deadline elapses while only ever seeing "not ready"/"transient" responses.
//     Names the gate and the concrete inputs (network ids, deposit count, leaf index, global
//     index) that never became satisfied - see DeadlineExceededError.
//   - ctx cancellation: reported as a plain wrapped ctx.Err(), distinct from
//     *DeadlineExceededError, since it means the caller asked to stop, not that the configured
//     wait budget was exhausted.
//
// # Gap G4: L1 (network_id=0) fail-fast
//
// A persistent 404 from GET /bridge/v1/l1-info-tree-index?network_id=0&... inside
// WaitL1InfoTreeIndex is, on its own, indistinguishable from "L1's bridge/L1-info syncer just
// hasn't indexed this deposit yet" (an ordinary "not ready, retry" condition) and "this proxy
// deployment has no L1 bridge service configured at all and never will" (DESIGN.md Gap G4) -
// waiting the latter out looks identical to waiting out the former until the deadline elapses.
// BridgeAddresses(ctx, 0) is the fail-fast preflight check for this: call it once before driving
// any hop that touches network 0, and treat errors.Is(err, ErrL1BridgeServiceUnavailable) as an
// immediate, permanent refusal to run that hop rather than something to retry.
type Proxy interface {
	// WaitL1InfoTreeIndex polls GET /bridge/v1/l1-info-tree-index?network_id=<source>&
	// deposit_count=<depositCount> (DESIGN.md §2, call 1) until source's own bridge/L1-info
	// syncer has indexed depositCount, and returns the resulting L1 info tree index I.
	//
	// Pass the returned I, unmodified, as WaitInjectedLeaf's leafIndex argument - WaitInjectedLeaf
	// itself is the one that may need to advance it to a later, actually-injected index (see its
	// doc comment and DESIGN.md §2's table note).
	WaitL1InfoTreeIndex(
		ctx context.Context, source uint32, depositCount uint64, pollInterval, deadline time.Duration,
	) (uint32, error)

	// WaitInjectedLeaf polls GET /bridge/v1/injected-l1-info-leaf?network_id=<destination>&
	// leaf_index=<leafIndex> (DESIGN.md §2, call 2) until destination has injected a global exit
	// root covering leafIndex, then returns the leaf's *actual* L1 info tree index
	// (response.l1_info_tree_index) - which may exceed leafIndex, since a concurrent bridge
	// elsewhere can cause a later-covering leaf to be injected first.
	//
	// Callers MUST use the returned index (I'), not the leafIndex they polled with (I), as
	// WaitClaimProof's leafIndex argument: using I instead of I' desyncs the claim proof from
	// what is really injected on destination (DESIGN.md §2's table note).
	//
	// destination == 0 (L1) is a no-op: it performs no HTTP call and returns leafIndex unchanged.
	// Settlement onto L1 already is the L1 GER update, so L1 never needs (and has no endpoint
	// for) an "injected leaf" wait (DESIGN.md §2) - when the caller's destination is L1, I' = I
	// from WaitL1InfoTreeIndex, always.
	WaitInjectedLeaf(
		ctx context.Context, destination uint32, leafIndex uint32, pollInterval, deadline time.Duration,
	) (uint32, error)

	// WaitClaimProof polls GET /bridge/v1/claim-proof?network_id=<source>&leaf_index=<leafIndex>&
	// deposit_count=<depositCount> (DESIGN.md §2, call 3) until source's bridgesync/
	// l1infotreesync has caught up to leafIndex/depositCount, then returns the Merkle proofs and
	// L1 info tree leaf needed to submit the claim.
	//
	// leafIndex MUST be the actual index WaitInjectedLeaf returned (I'), not the one originally
	// polled from WaitL1InfoTreeIndex (I) - see WaitInjectedLeaf's doc comment.
	WaitClaimProof(
		ctx context.Context, source, leafIndex, depositCount uint32, pollInterval, deadline time.Duration,
	) (*bridgeservicetypes.ClaimProof, error)

	// WaitClaimed polls GET /bridge/v1/claims?network_id=<destination>&global_index=<globalIndex>
	// until a claim record for globalIndex appears on destination, for cross-checking the
	// autoclaim path (DESIGN.md §4, state S4, Claim == "auto"): the hop engine's authoritative
	// claimed/not-claimed signal is still an on-chain isClaimed read (Bridge.IsClaimed, DESIGN.md
	// §8) - WaitClaimed is a proxy-only corroboration, useful e.g. once isClaimed reports true,
	// to confirm the claim syncer itself has also caught up, or wherever only the proxy (not a
	// JSON-RPC client) is available.
	//
	// Unlike the other Wait* methods, GET /bridge/v1/claims never 404s: it always answers 200,
	// with an empty list when nothing matches yet (it does not use the endpoint's
	// doRequestAllowNotFound path). "Not ready" is therefore an empty result, which WaitClaimed
	// treats exactly like bridgeserviceclient.ErrNotFound for retry purposes internally; that
	// translation never escapes to the caller - a hard failure from the underlying call (e.g. a
	// 500) is still returned as-is.
	WaitClaimed(
		ctx context.Context, destination uint32, globalIndex *big.Int, pollInterval, deadline time.Duration,
	) (*bridgeservicetypes.ClaimResponse, error)

	// TrackBridge calls GET /tracker/v1/network/<network>/tx/<txHash> once, for diagnosis: it is
	// the tracker's own independent cross-check of hop progress (DESIGN.md §3), never the source
	// of truth for hop-state transitions, which stay driven purely by /bridge/v1/* responses and
	// on-chain reads (DESIGN.md §4).
	//
	// This is a side-effecting GET: the first call for a given (network, txHash) registers it
	// with the tracker, occupying a slot in its bounded MaxTrackedBridges registry until
	// RetentionPeriod/IdleTimeout expire it (DESIGN.md Gap G2). Call it once per hop, right after
	// the bridge tx is mined - never in a tight poll loop; re-calling it later (e.g. once per
	// diagnostic check on a stalled hop) is fine, just do not poll it as the retry mechanism
	// itself.
	//
	// A 503 (the tracker's supervised registry is at capacity, domain.ErrRegistryFull) is
	// reported as bridgeserviceclient.ErrServiceUnavailable, mirroring the /bridge/v1/* taxonomy;
	// a 400 (malformed network id or tx hash) or any other non-200 is a hard error.
	TrackBridge(ctx context.Context, network uint32, txHash common.Hash) (*trackerapi.TrackingData, error)

	// Health calls GET /tracker/v1/health to confirm the aggkit-proxy's shared HTTP server (both
	// its proxy and tracker components) is reachable at all, independent of any one network's
	// bridge service - see BridgeAddresses for the per-network reachability/preflight check. A
	// cheap first step before a run starts driving any hop.
	Health(ctx context.Context) (*trackertypes.HealthResponse, error)

	// BridgeAddresses calls GET /bridge/v1/config?network_id=<networkID> to preflight-check that
	// networkID's bridge service is reachable through the proxy, and returns its public
	// configuration (contract addresses per network - DESIGN.md §1: for networkID == 0, this is
	// served by whichever instance the proxy's finder has configured as the L1 bridge service,
	// e.g. BridgeServiceFinder.BridgeURLs[0] in aggkit-proxy.toml).
	//
	// A 404 here means the proxy's bridgeservicefinder has no URL cached for networkID at all - a
	// routing-level failure, distinct from any /bridge/v1/* endpoint's "not indexed yet" 404
	// (DESIGN.md Gap G4). For networkID == 0 specifically, this commonly means the proxy
	// deployment simply has no L1 bridge service configured (BridgeURLs[0] unset) and never will
	// on its own - waiting it out like an ordinary ErrNotFound would hang forever, so this method
	// reports it as ErrL1BridgeServiceUnavailable instead (still wrapping
	// bridgeserviceclient.ErrNotFound, so errors.Is(err, bridgeserviceclient.ErrNotFound) still
	// holds for a caller that does not care about the distinction). For networkID != 0, a 404 is
	// reported as plain bridgeserviceclient.ErrNotFound: an L2's bridge service can legitimately
	// not be enumerated by the finder yet very early after startup, so it is left to the caller
	// to decide whether/how long to retry.
	//
	// Call this once per network during preflight, not as a hop-loop poll: unlike the Wait*
	// methods, there is no bounded "eventually true" contract to retry against for network 0 -
	// see Gap G4 above.
	BridgeAddresses(ctx context.Context, networkID uint32) (*bridgeservicetypes.PublicConfigResponse, error)
}

// ProxyClient is the Proxy implementation. It wraps bridgeservice/client.Client (pointed at the
// aggkit-proxy's ProxyURL) for /bridge/v1/*, plus a thin, unexported /tracker/v1 client, and never
// reads a database or an aggkit component's internal storage: every observation goes through the
// proxy REST API (DESIGN.md §1).
type ProxyClient struct {
	bridge     *bridgeserviceclient.Client
	tracker    *trackerClient
	httpClient *http.Client
	// baseURL is Global.ProxyURL trimmed of a trailing slash, reused for the one /bridge/v1
	// endpoint bridgeserviceclient.Client does not itself wrap (/bridge/v1/config, see
	// BridgeAddresses).
	baseURL string
}

var _ Proxy = (*ProxyClient)(nil)

// ProxyOption tunes a ProxyClient built by NewProxyClient.
type ProxyOption func(*ProxyClient)

// WithHTTPTimeout bounds every individual HTTP round trip the ProxyClient makes (not a Wait*
// method's overall poll deadline - that is the deadline parameter each Wait* method already
// takes). Values that are not strictly positive are ignored. Defaults to
// bridgeserviceclient.DefaultTimeout.
func WithHTTPTimeout(timeout time.Duration) ProxyOption {
	return func(p *ProxyClient) {
		if timeout > 0 {
			p.httpClient.Timeout = timeout
		}
	}
}

// NewProxyClient builds a ProxyClient against cfg.ProxyURL, the aggkit-proxy REST API exposing
// /bridge/v1 and /tracker/v1 on one shared HTTP server (DESIGN.md §1).
func NewProxyClient(cfg Global, opts ...ProxyOption) (*ProxyClient, error) {
	if cfg.ProxyURL == "" {
		return nil, fmt.Errorf("new proxy client: Global.ProxyURL is required")
	}

	httpClient := &http.Client{Timeout: bridgeserviceclient.DefaultTimeout}
	baseURL := strings.TrimSuffix(cfg.ProxyURL, "/")

	p := &ProxyClient{
		bridge:     bridgeserviceclient.New(bridgeserviceclient.Config{BaseURL: cfg.ProxyURL}),
		httpClient: httpClient,
		baseURL:    baseURL,
	}
	for _, opt := range opts {
		opt(p)
	}
	p.tracker = newTrackerClient(p.httpClient, p.baseURL)

	return p, nil
}

// WaitL1InfoTreeIndex implements Proxy.
func (p *ProxyClient) WaitL1InfoTreeIndex(
	ctx context.Context, source uint32, depositCount uint64, pollInterval, deadline time.Duration,
) (uint32, error) {
	detail := fmt.Sprintf("network_id=%d deposit_count=%d", source, depositCount)

	return pollGate(ctx, pollInterval, deadline, "l1-info-tree-index", detail,
		func(ctx context.Context) (uint32, error) {
			return p.bridge.GetL1InfoTreeIndex(ctx, int(source), int(depositCount))
		})
}

// WaitInjectedLeaf implements Proxy.
func (p *ProxyClient) WaitInjectedLeaf(
	ctx context.Context, destination uint32, leafIndex uint32, pollInterval, deadline time.Duration,
) (uint32, error) {
	if destination == mainnetNetworkID {
		return leafIndex, nil
	}

	detail := fmt.Sprintf("network_id=%d leaf_index=%d", destination, leafIndex)
	resp, err := pollGate(ctx, pollInterval, deadline, "injected-l1-info-leaf", detail,
		func(ctx context.Context) (*bridgeservicetypes.L1InfoTreeLeafResponse, error) {
			return p.bridge.GetInjectedL1InfoLeaf(ctx, int(destination), int(leafIndex))
		})
	if err != nil {
		return 0, err
	}

	return resp.L1InfoTreeIndex, nil
}

// WaitClaimProof implements Proxy.
func (p *ProxyClient) WaitClaimProof(
	ctx context.Context, source, leafIndex, depositCount uint32, pollInterval, deadline time.Duration,
) (*bridgeservicetypes.ClaimProof, error) {
	detail := fmt.Sprintf("network_id=%d leaf_index=%d deposit_count=%d", source, leafIndex, depositCount)

	return pollGate(ctx, pollInterval, deadline, "claim-proof", detail,
		func(ctx context.Context) (*bridgeservicetypes.ClaimProof, error) {
			return p.bridge.GetClaimProof(ctx, source, leafIndex, depositCount)
		})
}

// WaitClaimed implements Proxy.
func (p *ProxyClient) WaitClaimed(
	ctx context.Context, destination uint32, globalIndex *big.Int, pollInterval, deadline time.Duration,
) (*bridgeservicetypes.ClaimResponse, error) {
	if globalIndex == nil {
		return nil, fmt.Errorf("bridge_loop_tester: wait claimed on network %d: globalIndex is required", destination)
	}

	detail := fmt.Sprintf("network_id=%d global_index=%s", destination, globalIndex)

	return pollGate(ctx, pollInterval, deadline, "claimed", detail,
		func(ctx context.Context) (*bridgeservicetypes.ClaimResponse, error) {
			result, err := p.bridge.GetClaims(ctx, bridgeserviceclient.GetClaimsParams{
				NetworkID:   destination,
				GlobalIndex: globalIndex,
			})
			if err != nil {
				return nil, err
			}
			if len(result.Claims) == 0 {
				return nil, bridgeserviceclient.ErrNotFound
			}

			return result.Claims[0], nil
		})
}

// TrackBridge implements Proxy.
func (p *ProxyClient) TrackBridge(
	ctx context.Context, network uint32, txHash common.Hash,
) (*trackerapi.TrackingData, error) {
	data, err := p.tracker.TrackBridge(ctx, network, txHash)
	if err != nil {
		return nil, fmt.Errorf("bridge_loop_tester: track bridge network=%d tx=%s: %w", network, txHash, err)
	}

	return data, nil
}

// Health implements Proxy.
func (p *ProxyClient) Health(ctx context.Context) (*trackertypes.HealthResponse, error) {
	resp, err := p.tracker.Health(ctx)
	if err != nil {
		return nil, fmt.Errorf("bridge_loop_tester: health check: %w", err)
	}

	return resp, nil
}

// BridgeAddresses implements Proxy.
func (p *ProxyClient) BridgeAddresses(
	ctx context.Context, networkID uint32,
) (*bridgeservicetypes.PublicConfigResponse, error) {
	query := url.Values{}
	query.Set("network_id", strconv.FormatUint(uint64(networkID), 10))

	var resp bridgeservicetypes.PublicConfigResponse
	err := doProxyGet(ctx, p.httpClient, p.baseURL, "/bridge/v1/config?"+query.Encode(), &resp)

	switch {
	case err == nil:
		return &resp, nil
	case errors.Is(err, bridgeserviceclient.ErrNotFound) && networkID == mainnetNetworkID:
		return nil, fmt.Errorf("%w: %w", ErrL1BridgeServiceUnavailable, err)
	default:
		return nil, fmt.Errorf("bridge_loop_tester: bridge addresses for network %d: %w", networkID, err)
	}
}

// trackerClient is a thin client for the aggkit bridge tracker's /tracker/v1 REST API (DESIGN.md
// §3): just enough surface for ProxyClient's TrackBridge/Health methods - see the tracker's own
// richer API (activity, bridge-address, websocket) which this tool does not need.
type trackerClient struct {
	httpClient *http.Client
	baseURL    string
}

// newTrackerClient builds a trackerClient sharing httpClient and baseURL with the ProxyClient that
// owns it - the tracker and the bridge-service proxy are two components on one shared HTTP server
// (DESIGN.md §1), so there is nothing network-specific to configure separately.
func newTrackerClient(httpClient *http.Client, baseURL string) *trackerClient {
	return &trackerClient{httpClient: httpClient, baseURL: baseURL}
}

// Health calls GET /tracker/v1/health.
func (t *trackerClient) Health(ctx context.Context) (*trackertypes.HealthResponse, error) {
	var resp trackertypes.HealthResponse
	if err := doProxyGet(ctx, t.httpClient, t.baseURL, "/tracker/v1/health", &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

// TrackBridge calls GET /tracker/v1/network/<network>/tx/<txHash>. See Proxy.TrackBridge for the
// side-effecting-GET and error-taxonomy notes.
func (t *trackerClient) TrackBridge(
	ctx context.Context, network uint32, txHash common.Hash,
) (*trackerapi.TrackingData, error) {
	path := fmt.Sprintf("/tracker/v1/network/%d/tx/%s", network, txHash.Hex())

	var data trackerapi.TrackingData
	if err := doProxyGet(ctx, t.httpClient, t.baseURL, path, &data); err != nil {
		return nil, err
	}

	return &data, nil
}

// doProxyGet performs an HTTP GET against baseURL+path and decodes a 200 response into result.
// HTTP 404 is reported as bridgeserviceclient.ErrNotFound and HTTP 503 as
// bridgeserviceclient.ErrServiceUnavailable, mirroring bridgeservice/client's own
// doRequestAllowNotFound semantics, for the two endpoints bridgeserviceclient.Client does not
// itself wrap (GET /bridge/v1/config and every GET /tracker/v1/*).
func doProxyGet(ctx context.Context, httpClient *http.Client, baseURL, path string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return bridgeserviceclient.ErrNotFound
	case http.StatusServiceUnavailable:
		return bridgeserviceclient.ErrServiceUnavailable
	default:
		return fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	if result != nil {
		if err := json.Unmarshal(body, result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}

	return nil
}
