package common

import (
	"errors"
	"fmt"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRedactSensitive(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
		// forbidden lists substrings that must not appear in the output when non-empty.
		forbidden []string
	}{
		{
			name:      "V1 dial tcp with IPv4 URL",
			input:     `Post "http://1.2.3.4:8545": dial tcp 1.2.3.4:8545: connect: no route to host`,
			expected:  `Post <redacted-url>: dial tcp <redacted-host>: connect: no route to host`,
			forbidden: []string{"://", "1.2.3.4", "1.2.3.4:8545"},
		},
		{
			name:      "V2 dns lookup with dns name url",
			input:     `Get "https://user:pw@rpc.example.com/v3/KEY": dial tcp: lookup rpc.example.com: no such host`,
			expected:  `Get <redacted-url>: dial tcp: lookup <redacted-host>: no such host`,
			forbidden: []string{"://", "rpc.example.com"},
		},
		{
			name:      "V3a lookup host on ipv4 dns server",
			input:     `lookup host on 10.96.0.10:53: no such host`,
			expected:  `lookup <redacted-host> on <redacted-host>: no such host`,
			forbidden: []string{"10.96.0.10", "10.96.0.10:53"},
		},
		{
			name:      "V3b dial tcp lookup host on ipv4 dns server with url",
			input:     `Get "http://host:8545": dial tcp: lookup host on 10.96.0.10:53: no such host`,
			expected:  `Get <redacted-url>: dial tcp: lookup <redacted-host> on <redacted-host>: no such host`,
			forbidden: []string{"://", "10.96.0.10", "10.96.0.10:53"},
		},
		{
			name:      "V4 dial tcp with bracketed ipv6",
			input:     `dial tcp [2001:db8::1]:8545: i/o timeout`,
			expected:  `dial tcp <redacted-host>: i/o timeout`,
			forbidden: []string{"[2001:db8::1]", "2001:db8::1"},
		},
		{
			name:      "V5a grpc unavailable dial error",
			input:     `rpc error: code = Unavailable desc = connection error: desc = "transport: Error while dialing: dial tcp 10.0.0.1:4443: connect: connection refused"`,
			expected:  `rpc error: code = Unavailable desc = connection error: desc = "transport: Error while dialing: dial tcp <redacted-host>: connect: connection refused"`,
			forbidden: []string{"10.0.0.1", "10.0.0.1:4443"},
		},
		{
			name:      "V5b grpc unavailable dial error with certificate wrap",
			input:     `fetching certificate header 0xabc...: failed to get certificate header: Code: Unavailable, Message: connection error: desc = "transport: Error while dialing: dial tcp 10.0.0.1:4443: connect: connection refused", Details: none`,
			expected:  `fetching certificate header 0xabc...: failed to get certificate header: Code: Unavailable, Message: connection error: desc = "transport: Error while dialing: dial tcp <redacted-host>: connect: connection refused", Details: none`,
			forbidden: []string{"10.0.0.1", "10.0.0.1:4443"},
		},
		{
			name:      "V6 dialing JSON-RPC client with credentialed url",
			input:     `dialing JSON-RPC client of network 2 at http://user:pw@rpc.example.com/v3/KEY: 502 Bad Gateway: <html>Bad Gateway</html>`,
			expected:  `dialing JSON-RPC client of network 2 at <redacted-url>: 502 Bad Gateway: <html>Bad Gateway</html>`,
			forbidden: []string{"://", "rpc.example.com"},
		},
		{
			name:     "V7 unexpected status code no sensitive content",
			input:    `claim status: fetching claims of global index 123 on network 2: unexpected status code 502: <html>Bad Gateway</html>`,
			expected: `claim status: fetching claims of global index 123 on network 2: unexpected status code 502: <html>Bad Gateway</html>`,
		},
		{
			name:      "V8 do request with ipv4 url",
			input:     `claim status: fetching claims of global index 123 on network 2: do request: Get "http://10.0.0.5:5577/bridge/v1/claims?global_index=123&network_id=2": context deadline exceeded`,
			expected:  `claim status: fetching claims of global index 123 on network 2: do request: Get <redacted-url>: context deadline exceeded`,
			forbidden: []string{"://", "10.0.0.5"},
		},
		{
			name:     "V9a step index out of range no sensitive content",
			input:    `step index 3 out of range: allSteps has 8 entries`,
			expected: `step index 3 out of range: allSteps has 8 entries`,
		},
		{
			name:     "V9b network tx does not exist no sensitive content",
			input:    `network=2/tx=0xabc123...def does not exist on the network`,
			expected: `network=2/tx=0xabc123...def does not exist on the network`,
		},
		{
			name:      "V10 do request with ipv4 url then dial tcp ipv4",
			input:     `claim status: fetching claims of global index 123 on network 2: do request: Get "http://10.0.0.5:5577/bridge/v1/claims?network_id=2&global_index=123": dial tcp 10.0.0.5:5577: connect: connection refused`,
			expected:  `claim status: fetching claims of global index 123 on network 2: do request: Get <redacted-url>: dial tcp <redacted-host>: connect: connection refused`,
			forbidden: []string{"://", "10.0.0.5", "10.0.0.5:5577"},
		},
		{
			name:     "F1 network tx hex zero prefix no sensitive content",
			input:    `network=0/tx=0x1f7ad7caa53e35b4f0d138dc5cbf91ac108a2674 does not exist on the network`,
			expected: `network=0/tx=0x1f7ad7caa53e35b4f0d138dc5cbf91ac108a2674 does not exist on the network`,
		},
		{
			name:     "F2 tracking data is nil no sensitive content",
			input:    `tracking data is nil`,
			expected: `tracking data is nil`,
		},
		{
			name:     "F3 invalid network_id parameter no sensitive content",
			input:    `invalid network_id parameter: abc`,
			expected: `invalid network_id parameter: abc`,
		},
		{
			name:     "F4 supervised registry full with durations no sensitive content",
			input:    `supervised registry is full: 100000 entries, timeout 5m0s after 1m30s`,
			expected: `supervised registry is full: 100000 entries, timeout 5m0s after 1m30s`,
		},
		{
			name:     "F5 bridge service url not found no sensitive content",
			input:    `bridge service url not found for network: network 3`,
			expected: `bridge service url not found for network: network 3`,
		},
		{
			name:      "F6 dialing JSON-RPC client with dns name url and lookup on ipv4",
			input:     `dialing JSON-RPC client of network 2 at http://unreachable-2.invalid:8545: Post "http://unreachable-2.invalid:8545": dial tcp: lookup unreachable-2.invalid on 127.0.0.11:53: no such host`,
			expected:  `dialing JSON-RPC client of network 2 at <redacted-url>: Post <redacted-url>: dial tcp: lookup <redacted-host> on <redacted-host>: no such host`,
			forbidden: []string{"://", "unreachable-2.invalid", "127.0.0.11"},
		},
		{
			name:      "F7 do request with container name url and dial tcp container name",
			input:     `do request: Get "http://aggkit-002:5577/bridge/v1/claims?network_id=2": dial tcp aggkit-002:5577: connect: connection refused`,
			expected:  `do request: Get <redacted-url>: dial tcp <redacted-host>: connect: connection refused`,
			forbidden: []string{"://", "aggkit-002"},
		},
		{
			name:     "F8 empty string",
			input:    ``,
			expected: ``,
		},
		{
			name:      "F9 dialing JSON-RPC client with dns name url no trailing content",
			input:     `dialing JSON-RPC client of network 2 at http://rpc.example.com:8545`,
			expected:  `dialing JSON-RPC client of network 2 at <redacted-url>`,
			forbidden: []string{"://", "rpc.example.com"},
		},
		{
			name:     "F10 resolving bridge service URL not found no sensitive content",
			input:    `resolving bridge service URL for network 7: bridge service url not found for network: network 7`,
			expected: `resolving bridge service URL for network 7: bridge service url not found for network: network 7`,
		},
		{
			name:      "F11 bracketed ipv6 url and dial tcp bracketed ipv6",
			input:     `Get "http://[2001:db8::1]:5577/bridge/v1/claims": dial tcp [2001:db8::1]:5577: connect: connection refused`,
			expected:  `Get <redacted-url>: dial tcp <redacted-host>: connect: connection refused`,
			forbidden: []string{"://", "2001:db8::1"},
		},
		{
			name:      "F12 lookup with double space before dns name",
			input:     `lookup  spaced.example.com: no such host`,
			expected:  `lookup  <redacted-host>: no such host`,
			forbidden: []string{"spaced.example.com"},
		},
		{
			name:      "M2-1 dialing JSON-RPC client with scheme-less dotless host and port",
			input:     `dialing JSON-RPC client of network 2 at aggkit-002:8545: no known transport for URL scheme ""`,
			expected:  `dialing JSON-RPC client of network 2 at <redacted-host>: no known transport for URL scheme ""`,
			forbidden: []string{"aggkit-002:8545", "aggkit-002"},
		},
		{
			name:      "M2-2 dialing JSON-RPC client with scheme-less dotless host and port variant",
			input:     `dialing JSON-RPC client of network 2 at rpc-internal:8545: no known transport for URL scheme ""`,
			expected:  `dialing JSON-RPC client of network 2 at <redacted-host>: no known transport for URL scheme ""`,
			forbidden: []string{"rpc-internal:8545", "rpc-internal"},
		},
		{
			name:      "M2-3 quoted scheme-less host:port/path echoed by url.Error",
			input:     `do request: Get "aggkit-002:5577/bridge/v1/claims?network_id=2": context deadline exceeded`,
			expected:  `do request: Get <redacted-url>: context deadline exceeded`,
			forbidden: []string{"aggkit-002:5577"},
		},
		{
			name:      "M2-4 upstream dotless host:port inside an html error body",
			input:     `unexpected status code 502: <html><body>upstream aggkit-002:5577 refused</body></html>`,
			expected:  `unexpected status code 502: <html><body>upstream <redacted-host> refused</body></html>`,
			forbidden: []string{"aggkit-002:5577"},
		},
		{
			name:      "M2-5 grpc name resolver error with dotless target host:port",
			input:     `Code: Unavailable, Message: name resolver error for target agglayer-internal:4443, Details: none`,
			expected:  `Code: Unavailable, Message: name resolver error for target <redacted-host>, Details: none`,
			forbidden: []string{"agglayer-internal:4443"},
		},
		{
			name:      "M2-6 connecting to dotless host:port",
			input:     `connecting to agglayer-internal:4443 failed`,
			expected:  `connecting to <redacted-host> failed`,
			forbidden: []string{"agglayer-internal:4443"},
		},
		{
			name:      "M3-1 x509 certificate valid for mismatched dotted hostnames",
			input:     `Get "https://rpc.internal.example.com:8545": tls: failed to verify certificate: x509: certificate is valid for rpc.internal.example.com, not proxy.example.com`,
			expected:  `Get <redacted-url>: tls: failed to verify certificate: x509: certificate is valid for <redacted-host>, not <redacted-host>`,
			forbidden: []string{"://", "rpc.internal.example.com", "proxy.example.com"},
		},
		{
			name:      "M3-2 no such host suffix with bare dotted hostname",
			input:     `no such host: unreachable-2.invalid`,
			expected:  `no such host: <redacted-host>`,
			forbidden: []string{"unreachable-2.invalid"},
		},
		{
			name:      "M3-3 x509 certificate valid for mismatched non-dotted hostnames",
			input:     `x509: certificate is valid for internal-rpc, not aggkit`,
			expected:  `x509: certificate is valid for <redacted-host>, not <redacted-host>`,
			forbidden: []string{"internal-rpc"},
		},
		{
			name:     "G1 bare network colon prefix no sensitive content",
			input:    `network 2:`,
			expected: `network 2:`,
		},
		{
			name:     "G2 hex hash colon prefix no sensitive content",
			input:    `0x1234abcd..: something`,
			expected: `0x1234abcd..: something`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := RedactSensitive(tt.input)
			require.Equal(t, tt.expected, actual)

			// Idempotency: redacting again must be a no-op.
			require.Equal(t, actual, RedactSensitive(actual))

			for _, f := range tt.forbidden {
				require.NotContains(t, actual, f)
			}
		})
	}
}

// TestRedactSensitive_NoSensitiveContentIsByteIdentical asserts the "no sensitive content"
// vector (V7) is returned byte-identical to its input.
func TestRedactSensitive_NoSensitiveContentIsByteIdentical(t *testing.T) {
	input := `claim status: fetching claims of global index 123 on network 2: unexpected status code 502: <html>Bad Gateway</html>`
	require.Equal(t, input, RedactSensitive(input))
}

// TestRedactSensitive_NoRemainingSensitiveTokens is a defense-in-depth pass over every vector
// whose input carries sensitive content: the output must contain none of "://", an IPv4
// literal, a "[..]:port" bracketed literal, or the original hostname/URL host (forbiddenHosts;
// see S17 L12 - the original hostname check is asserted explicitly, not just implied by the
// other three).
func TestRedactSensitive_NoRemainingSensitiveTokens(t *testing.T) {
	cases := []struct {
		name           string
		input          string
		forbiddenHosts []string
	}{
		{"V1", `Post "http://1.2.3.4:8545": dial tcp 1.2.3.4:8545: connect: no route to host`, nil},
		{
			"V2", `Get "https://user:pw@rpc.example.com/v3/KEY": dial tcp: lookup rpc.example.com: no such host`,
			[]string{"rpc.example.com"},
		},
		{"V3a", `lookup host on 10.96.0.10:53: no such host`, nil},
		{"V3b", `Get "http://host:8545": dial tcp: lookup host on 10.96.0.10:53: no such host`, nil},
		{"V4", `dial tcp [2001:db8::1]:8545: i/o timeout`, nil},
		{
			"V6", `dialing JSON-RPC client of network 2 at http://user:pw@rpc.example.com/v3/KEY: 502 Bad Gateway: <html>Bad Gateway</html>`,
			[]string{"rpc.example.com"},
		},
		{
			"V8", `claim status: fetching claims of global index 123 on network 2: do request: Get "http://10.0.0.5:5577/bridge/v1/claims?global_index=123&network_id=2": context deadline exceeded`,
			nil,
		},
		{
			"V10", `claim status: fetching claims of global index 123 on network 2: do request: Get "http://10.0.0.5:5577/bridge/v1/claims?network_id=2&global_index=123": dial tcp 10.0.0.5:5577: connect: connection refused`,
			nil,
		},
		{
			"F6", `dialing JSON-RPC client of network 2 at http://unreachable-2.invalid:8545: Post "http://unreachable-2.invalid:8545": dial tcp: lookup unreachable-2.invalid on 127.0.0.11:53: no such host`,
			[]string{"unreachable-2.invalid"},
		},
		{
			"F7", `do request: Get "http://aggkit-002:5577/bridge/v1/claims?network_id=2": dial tcp aggkit-002:5577: connect: connection refused`,
			[]string{"aggkit-002"},
		},
		{"F9", `dialing JSON-RPC client of network 2 at http://rpc.example.com:8545`, []string{"rpc.example.com"}},
		{"F11", `Get "http://[2001:db8::1]:5577/bridge/v1/claims": dial tcp [2001:db8::1]:5577: connect: connection refused`, nil},
		{
			"M2-1", `dialing JSON-RPC client of network 2 at aggkit-002:8545: no known transport for URL scheme ""`,
			[]string{"aggkit-002"},
		},
		{
			"M2-3", `do request: Get "aggkit-002:5577/bridge/v1/claims?network_id=2": context deadline exceeded`,
			[]string{"aggkit-002:5577"},
		},
		{
			"M2-4", `unexpected status code 502: <html><body>upstream aggkit-002:5577 refused</body></html>`,
			[]string{"aggkit-002"},
		},
		{
			"M2-5", `Code: Unavailable, Message: name resolver error for target agglayer-internal:4443, Details: none`,
			[]string{"agglayer-internal"},
		},
		{"M2-6", `connecting to agglayer-internal:4443 failed`, []string{"agglayer-internal"}},
		{
			"M3-1", `Get "https://rpc.internal.example.com:8545": tls: failed to verify certificate: x509: certificate is valid for rpc.internal.example.com, not proxy.example.com`,
			[]string{"rpc.internal.example.com", "proxy.example.com"},
		},
		{"M3-2", `no such host: unreachable-2.invalid`, []string{"unreachable-2.invalid"}},
		{"M3-3", `x509: certificate is valid for internal-rpc, not aggkit`, []string{"internal-rpc"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := RedactSensitive(c.input)
			require.NotContains(t, out, "://")
			require.False(t, ipv4Regexp.MatchString(out), "output still contains an IPv4 literal: %s", out)
			require.False(t, bracketedPortRegexp.MatchString(out), "output still contains a [..]:port literal: %s", out)
			for _, h := range c.forbiddenHosts {
				require.NotContains(t, out, h, "output still contains the original hostname: %s", h)
			}
		})
	}
}

func TestRedactError(t *testing.T) {
	require.Equal(t, "", RedactError(nil))

	v1raw := `Post "http://1.2.3.4:8545": dial tcp 1.2.3.4:8545: connect: no route to host`
	v1redacted := `Post <redacted-url>: dial tcp <redacted-host>: connect: no route to host`

	wrapped := fmt.Errorf("wrap: %w", errors.New(v1raw))
	require.Equal(t, "wrap: "+v1redacted, RedactError(wrapped))
}

func TestRedactSensitiveSlice(t *testing.T) {
	require.Nil(t, RedactSensitiveSlice(nil))

	in := []string{
		`Post "http://1.2.3.4:8545": dial tcp 1.2.3.4:8545: connect: no route to host`,
		`tracking data is nil`,
	}
	inCopy := append([]string(nil), in...)

	out := RedactSensitiveSlice(in)

	require.Equal(t, []string{
		`Post <redacted-url>: dial tcp <redacted-host>: connect: no route to host`,
		`tracking data is nil`,
	}, out)

	// The input slice must not be mutated.
	require.Equal(t, inCopy, in)
}

// ipv4Regexp / bracketedPortRegexp are local, test-only helpers used to assert that no IPv4
// literal or bracketed "[..]:port" literal remains in a redacted string. They intentionally
// mirror (but do not reuse) the production regexes so the test does not tautologically pass.
var (
	ipv4Regexp          = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)
	bracketedPortRegexp = regexp.MustCompile(`\[[^\]]*\](?::\d{1,5})?`)
)
