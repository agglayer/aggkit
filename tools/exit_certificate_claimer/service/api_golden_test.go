package claimer

import (
	"bytes"
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// updateGolden rewrites the golden API responses under testdata/api instead of comparing them:
//
//	go test ./tools/exit_certificate_claimer/service/ -run TestAPIGolden -update
var updateGolden = flag.Bool("update", false, "rewrite the golden API responses in testdata/api")

const goldenAPIDir = "testdata/api"

// goldenVersion is the build version the golden /health responses carry: the real one depends on the
// build and the machine.
var goldenVersion = VersionInfo{
	Version:   "v0.0.0-golden",
	GitRev:    "0000000",
	GitBranch: "golden",
	BuildDate: "1970-01-01T00:00:00Z",
	GoVersion: "go0.0.0",
	OS:        "linux",
	Arch:      "amd64",
}

// goldenRequest is one entry of testdata/api/requests.json: a request and the file with the exact
// body the claimer answers it with.
type goldenRequest struct {
	Name   string `json:"name"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Status int    `json:"status"`
	File   string `json:"file"`
}

// TestAPIGolden pins the HTTP contract of API v1 and v2: every response body is compared, byte for
// byte once indented, with a golden file. The files are also the fixtures of the contract tests of
// API clients (the claims UI), so a diff here is a change of the public API: a breaking one needs a
// new major version (see SPEC.md, "API versioning").
//
// The data is the circleFixture: a holder with a Transfer and a Message exit in the certificate, plus a
// pending Message and a pending Transfer, over the Circle USDC message route.
func TestAPIGolden(t *testing.T) {
	t.Parallel()
	f := newCircleFixture(t)
	cfg := &Config{Address: "127.0.0.1", Port: 7080, ReadTimeoutSeconds: 1, WriteTimeoutSeconds: 1}
	srv := NewServer(cfg, f.claimer, f.claimer.logger)
	srv.version = goldenVersion

	// A second claimer over the same data whose certificate is not settled on L1 yet (409).
	notSettledL1 := *f.l1
	notSettledL1.localRoot = common.HexToHash("0x27ae5ba08d7291c96c8cbddcc148bf48a6d68c7974b94356f53754ef6171d757")
	notSettled := *f.claimer
	notSettled.l1 = &notSettledL1
	notSettledSrv := NewServer(cfg, &notSettled, f.claimer.logger)

	holder := f.holder.Hex()
	q1 := "?dest_address=" + holder
	q2 := "?address=" + holder
	dc := func(depositCount uint32) string {
		return "&deposit_count=" + strconv.FormatUint(uint64(depositCount), 10)
	}

	cases := []struct {
		name   string
		srv    *Server
		path   string
		status int
		file   string
	}{
		// API v1: unchanged, Transfer exits only.
		{"v1 health", srv, "/claimer/v1/health", http.StatusOK, "v1/health.json"},
		{"v1 bridges of a holder: Transfer exits only", srv, "/claimer/v1/bridges" + q1, http.StatusOK,
			"v1/bridges.json"},
		{"v1 bridges of the message destination contract: none", srv,
			"/claimer/v1/bridges?dest_address=" + circleL1Escrow.Hex(), http.StatusOK, "v1/bridges-empty.json"},
		{"v1 claim-params of a holder: every Transfer exit", srv, "/claimer/v1/claim-params" + q1, http.StatusOK,
			"v1/claim-params.json"},
		{"v1 claim-params of one deposit", srv, "/claimer/v1/claim-params" + q1 + dc(circleDCTransfer),
			http.StatusOK, "v1/claim-params-deposit.json"},
		{"v1 missing dest_address", srv, "/claimer/v1/bridges", http.StatusBadRequest,
			"v1/error-400-missing-dest-address.json"},
		{"v1 invalid dest_address", srv, "/claimer/v1/bridges?dest_address=0x1234", http.StatusBadRequest,
			"v1/error-400-invalid-dest-address.json"},
		{"v1 invalid deposit_count", srv, "/claimer/v1/claim-params" + q1 + "&deposit_count=abc",
			http.StatusBadRequest, "v1/error-400-invalid-deposit-count.json"},
		{"v1 certificate not settled", notSettledSrv, "/claimer/v1/claim-params" + q1, http.StatusConflict,
			"v1/error-409-not-settled.json"},

		// API v2: Transfer and Message exits.
		{"v2 health", srv, "/claimer/v2/health", http.StatusOK, "v2/health.json"},
		{"v2 bridges of a holder: Transfer, decoded Message, pending Message, pending Transfer", srv,
			"/claimer/v2/bridges" + q2, http.StatusOK, "v2/bridges.json"},
		{"v2 bridges of a message of a route that is not configured", srv,
			"/claimer/v2/bridges?address=" + f.unknownDest.Hex(), http.StatusOK, "v2/bridges-unknown-route.json"},
		{"v2 bridges of an address without exits", srv,
			"/claimer/v2/bridges?address=0x00000000000000000000000000000000000000EE", http.StatusOK,
			"v2/bridges-empty.json"},
		{"v2 claim-params of a Transfer exit (claimAsset)", srv,
			"/claimer/v2/claim-params" + q2 + dc(circleDCTransfer), http.StatusOK,
			"v2/claim-params-claim-asset.json"},
		{"v2 claim-params of a Message exit (claimMessage)", srv,
			"/claimer/v2/claim-params" + q2 + dc(circleDCMessage), http.StatusOK,
			"v2/claim-params-claim-message.json"},
		{"v2 claim-params of a pending Message (claimMessage)", srv,
			"/claimer/v2/claim-params" + q2 + dc(circleDCPendingMessage), http.StatusOK,
			"v2/claim-params-pending-message.json"},
		{"v2 claim-params of a holder: every exit", srv, "/claimer/v2/claim-params" + q2, http.StatusOK,
			"v2/claim-params-all.json"},
		{"v2 claim-params of a deposit of another address: none", srv,
			"/claimer/v2/claim-params" + q2 + dc(circleDCDuplicateMessage), http.StatusOK,
			"v2/claim-params-empty.json"},
		{"v2 missing address", srv, "/claimer/v2/bridges", http.StatusBadRequest,
			"v2/error-400-missing-address.json"},
		{"v2 invalid address", srv, "/claimer/v2/bridges?address=0x1234", http.StatusBadRequest,
			"v2/error-400-invalid-address.json"},
		{"v2 invalid deposit_count", srv, "/claimer/v2/claim-params" + q2 + "&deposit_count=abc",
			http.StatusBadRequest, "v2/error-400-invalid-deposit-count.json"},
		{"v2 certificate not settled", notSettledSrv, "/claimer/v2/claim-params" + q2, http.StatusConflict,
			"v2/error-409-not-settled.json"},
	}

	index := make([]goldenRequest, 0, len(cases))
	for _, c := range cases {
		rec := doRequest(t, c.srv, c.path)
		require.Equal(t, c.status, rec.Code, c.name)
		require.Contains(t, rec.Header().Get("Content-Type"), "application/json", c.name)

		var indented bytes.Buffer
		require.NoError(t, json.Indent(&indented, rec.Body.Bytes(), "", "  "), c.name)
		indented.WriteByte('\n')
		compareGolden(t, c.file, indented.Bytes())

		index = append(index, goldenRequest{
			Name: c.name, Method: http.MethodGet, Path: c.path, Status: c.status, File: c.file,
		})
	}

	// requests.json maps each golden file to the request that produces it.
	rawIndex, err := json.MarshalIndent(index, "", "  ")
	require.NoError(t, err)
	compareGolden(t, "requests.json", append(rawIndex, '\n'))
}

// compareGolden compares got with the golden file (relative to testdata/api), or rewrites the file
// with -update.
func compareGolden(t *testing.T, file string, got []byte) {
	t.Helper()
	path := filepath.Join(goldenAPIDir, filepath.FromSlash(file))
	if *updateGolden {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, got, 0o600))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "golden file missing: run the test with -update")
	require.Equal(t, string(want), string(got),
		"%s differs from the golden response: the API changed. If it is intended and not breaking for the "+
			"API version, run the test with -update", file)
}

// The v1 body of an exit must keep exactly the fields a v1 client knows: nothing of v2 leaks into it.
func TestAPIV1FieldsUnchanged(t *testing.T) {
	t.Parallel()
	f := newCircleFixture(t)
	cfg := &Config{Address: "127.0.0.1", Port: 7080, ReadTimeoutSeconds: 1, WriteTimeoutSeconds: 1}
	srv := NewServer(cfg, f.claimer, f.claimer.logger)

	keys := func(path string, list string) []string {
		rec := doRequest(t, srv, path)
		require.Equal(t, http.StatusOK, rec.Code)
		var body map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.NotContains(t, body, "api_version")
		var entries []map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(body[list], &entries))
		require.NotEmpty(t, entries)
		out := make([]string, 0, len(entries[0]))
		for k := range entries[0] {
			out = append(out, k)
		}
		return out
	}

	require.ElementsMatch(t, []string{
		"leaf_type", "origin_network", "origin_token_address", "destination_network", "destination_address",
		"amount", "metadata", "deposit_count",
	}, keys("/claimer/v1/bridges?dest_address="+f.holder.Hex(), "bridges"))
	require.ElementsMatch(t, []string{
		"smt_proof_local_exit_root", "smt_proof_rollup_exit_root", "global_index", "mainnet_exit_root",
		"rollup_exit_root", "origin_network", "origin_token_address", "destination_network",
		"destination_address", "amount", "metadata", "leaf_type", "deposit_count", "l1_info_tree_index",
	}, keys("/claimer/v1/claim-params?dest_address="+f.holder.Hex(), "claims"))

	// /health: api_versions is the only addition.
	rec := doRequest(t, srv, "/claimer/v1/health")
	var health map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &health))
	require.Len(t, health, 4)
	require.JSONEq(t, "[1,2]", string(health["api_versions"]))
	require.NotContains(t, health, "api_version")
}
