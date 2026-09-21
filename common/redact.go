package common

import (
	"regexp"
	"strings"
)

const (
	// RedactedURLPlaceholder replaces any scheme-bearing URL in a client-visible message.
	RedactedURLPlaceholder = "<redacted-url>"
	// RedactedHostPlaceholder replaces any bare host:port, IP address or DNS name in a
	// client-visible message.
	RedactedHostPlaceholder = "<redacted-host>"
)

var (
	// 1. Any scheme-bearing URL, with the surrounding double quotes Go's *url.Error adds.
	reURL = regexp.MustCompile(`"?[A-Za-z][A-Za-z0-9+.\-]*://[^\s"]*"?`)
	// 2. Bracketed IPv6 literal with optional port. At least one ':' inside the brackets is
	//    required so plain bracketed words ("[abc]") are not mistaken for an address.
	reIPv6Host = regexp.MustCompile(`\[[0-9A-Fa-f:.]*:[0-9A-Fa-f:.]*\](?::\d{1,5})?`)
	// 3. IPv4 literal with optional port.
	reIPv4Host = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}(?::\d{1,5})?\b`)
	// 4. net.OpError address: "<op> <network> <host>:<port>" with a non-dotted host
	//    (e.g. a container name), which rules 2/3/5 do not cover.
	reNetAddr = regexp.MustCompile(
		`\b((?:dial|read|write|listen|accept) (?:tcp|udp|ip)[46]?) [^\s:,;"]+:\d{1,5}\b`)
	// 5. Dotted DNS name with a port. A port is REQUIRED so bare dotted tokens (file names,
	//    version strings, prose) are left alone.
	reHostPort = regexp.MustCompile(
		`\b(?:[A-Za-z0-9](?:[A-Za-z0-9\-]*[A-Za-z0-9])?\.)+[A-Za-z][A-Za-z0-9\-]*:\d{1,5}\b`)
	// 6. net.DNSError name: "lookup <name>". The placeholder's own characters are part of the
	//    token class so a second pass rewrites it to itself (idempotency).
	reLookup = regexp.MustCompile(`\b(lookup\s+)[A-Za-z0-9<][A-Za-z0-9.\-<>]*`)
	// 7. A quoted, scheme-less "URL" (host[:port][/path]) as produced when an operator-configured
	//    RPCURLs/BridgeURLs value has no scheme and Go's url.Parse / rpc.Dial echoes it verbatim
	//    inside the double quotes *url.Error normally reserves for a real URL (S17 M2). The host
	//    must start with a letter so a quoted numeric/timestamp-like token is never mistaken for
	//    one.
	reQuotedNoSchemeURL = regexp.MustCompile(`"[A-Za-z][A-Za-z0-9\-]*:\d{1,5}[^\s"]*"`)
	// 8. A dotless host:port immediately after one of a small set of known wrap-text anchors
	//    ("dialing ... at <host>:<port>", "for target <host>:<port>", "upstream <host>:<port>
	//    refused", ...). Anchored deliberately (rather than matching any bare "word:digits" pair)
	//    so ordinary prose ("network 2:", timestamps, durations) is never touched (S17 M2); the
	//    host must start with a letter, which also keeps a following timestamp
	//    ("resolved at 2026-...T10:11:12Z") untouched.
	reAnchoredHostPort = regexp.MustCompile(
		`\b(at|to|target|upstream|host|address)\s+([A-Za-z][A-Za-z0-9\-]*:\d{1,5})\b`)
	// 9. The exact "certificate is valid for <name>, not <name>" shape a TLS/x509 SAN mismatch
	//    produces, redacting both the certificate's SAN and the hostname the client dialed (S17
	//    M3). Anchored to this literal phrase rather than the generic words "for"/"not" so
	//    unrelated uses of those words (e.g. "for network 7") are never touched.
	reCertValidFor = regexp.MustCompile(
		`\b(certificate is valid for )([A-Za-z0-9][A-Za-z0-9.\-]*)(, not )([A-Za-z0-9][A-Za-z0-9.\-]*)`)
	// 10. A bare hostname (dotted or not) after the literal "no such host: " prefix (S17 M3) -
	//     the reverse ordering of the usual net.DNSError "lookup <name>: no such host" text that
	//     rule 6 already covers.
	reNoSuchHostSuffix = regexp.MustCompile(
		`\b(no such host: )((?:[A-Za-z0-9](?:[A-Za-z0-9\-]*[A-Za-z0-9])?\.)*[A-Za-z][A-Za-z0-9\-]*)\b`)
)

// urlTrailingPunct are the characters stripped off the tail of an unquoted URL match before the
// placeholder is emitted, so "…/v3/KEY: 502 Bad Gateway" keeps its separator colon.
const urlTrailingPunct = `:;,.!?)]}'`

// RedactSensitive returns s with every URL, host:port, IP address and DNS name replaced by
// RedactedURLPlaceholder / RedactedHostPlaceholder. It is pure and idempotent
// (RedactSensitive(RedactSensitive(s)) == RedactSensitive(s)) and returns s unchanged when it
// carries no such token. It is meant for strings that leave the process towards an API client;
// log output is deliberately NOT redacted.
func RedactSensitive(s string) string {
	if s == "" {
		return s
	}
	s = reURL.ReplaceAllStringFunc(s, func(m string) string {
		if strings.HasSuffix(m, `"`) {
			return RedactedURLPlaceholder
		}
		trimmed := strings.TrimRight(m, urlTrailingPunct)
		return RedactedURLPlaceholder + m[len(trimmed):]
	})
	s = reIPv6Host.ReplaceAllString(s, RedactedHostPlaceholder)
	s = reIPv4Host.ReplaceAllString(s, RedactedHostPlaceholder)
	s = reNetAddr.ReplaceAllString(s, "${1} "+RedactedHostPlaceholder)
	s = reHostPort.ReplaceAllString(s, RedactedHostPlaceholder)
	s = reLookup.ReplaceAllString(s, "${1}"+RedactedHostPlaceholder)
	s = reQuotedNoSchemeURL.ReplaceAllString(s, RedactedURLPlaceholder)
	s = reAnchoredHostPort.ReplaceAllString(s, "${1} "+RedactedHostPlaceholder)
	s = reCertValidFor.ReplaceAllString(s, "${1}"+RedactedHostPlaceholder+"${3}"+RedactedHostPlaceholder)
	s = reNoSuchHostSuffix.ReplaceAllString(s, "${1}"+RedactedHostPlaceholder)
	return s
}

// RedactError returns RedactSensitive(err.Error()), or "" when err is nil.
func RedactError(err error) string {
	if err == nil {
		return ""
	}
	return RedactSensitive(err.Error())
}

// RedactSensitiveSlice returns a NEW slice holding RedactSensitive of every element of in, or
// nil when in is nil. It never mutates in (callers such as ErrorStep.MarshalJSON hold a slice
// header copy whose backing array is shared with the stored value).
func RedactSensitiveSlice(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = RedactSensitive(v)
	}
	return out
}
