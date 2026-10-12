// Package probe sends constrained HTTP probes directly to numeric endpoints.
package probe

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/endpoint"
	"github.com/HarnageaGabriel/varco/internal/result"
)

// maxBodyBytes bounds how many body bytes are hashed. Every byte up to the
// bound is streamed into the digest (nothing is buffered); a longer body is
// reported as truncated. It is a variable so tests can lower it.
var maxBodyBytes int64 = 1 << 30

// Request configures one endpoint probe.
type Request struct {
	Endpoint          endpoint.Endpoint
	Scheme            string
	Host              string
	SNI               string
	Method            string
	Path              string
	Headers           []result.Header
	Timeout           time.Duration
	AllowUnsafeMethod bool
}

// Do sends one HTTP/1.1 request without DNS, proxies, compression or redirect
// following. The request target is written to the wire byte-for-byte as given.
// A new connection is opened per probe and closed afterwards (intentional: a
// probe must not share connection state with another endpoint or path).
func Do(ctx context.Context, request Request) (result.Result, error) {
	method := strings.ToUpper(request.Method)
	if !validToken(method) {
		return result.Result{}, diagnosticError(diag.CodeInvalidFlag, fmt.Sprintf("invalid method %q", request.Method), nil)
	}
	if !safeMethod(method) && !request.AllowUnsafeMethod {
		return result.Result{}, diagnosticError(diag.CodeUnsupportedMethod, fmt.Sprintf("method %s requires --allow-unsafe-methods", method), nil)
	}
	if request.Scheme != "http" && request.Scheme != "https" {
		return result.Result{}, fmt.Errorf("unsupported scheme %q", request.Scheme)
	}
	if !ValidPath(request.Path) {
		return result.Result{}, diagnosticError(diag.CodeInvalidFlag, fmt.Sprintf("invalid request path %q", request.Path), nil)
	}
	if !ValidHost(request.Host) {
		return result.Result{}, diagnosticError(diag.CodeInvalidFlag, fmt.Sprintf("invalid host %q", request.Host), nil)
	}
	for _, header := range request.Headers {
		if CredentialHeader(header.Name) {
			return result.Result{}, diagnosticError(diag.CodeInvalidFlag, "credential request headers are not allowed", nil)
		}
		if !ValidHeaderName(header.Name) || ForbiddenHeader(header.Name) {
			return result.Result{}, diagnosticError(diag.CodeInvalidFlag, fmt.Sprintf("request header %q is not allowed", header.Name), nil)
		}
		for _, value := range header.Values {
			if !ValidHeaderValue(value) {
				return result.Result{}, diagnosticError(diag.CodeInvalidFlag, fmt.Sprintf("invalid value for request header %s", header.Name), nil)
			}
		}
	}

	if request.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, request.Timeout)
		defer cancel()
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", request.Endpoint.String())
	if err != nil {
		return result.Result{}, fmt.Errorf("probe %s: %w", request.Endpoint.String(), err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	rw := conn
	var state *tls.ConnectionState
	if request.Scheme == "https" {
		tlsConfig := &tls.Config{ //nolint:gosec // probes capture certificates and accept old protocols; they do not assert trust.
			InsecureSkipVerify: true,
			MinVersion:         tls.VersionTLS10,
			ServerName:         request.SNI,
			NextProtos:         []string{"http/1.1"},
		}
		tlsConn := tls.Client(conn, tlsConfig)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return result.Result{}, fmt.Errorf("probe %s: tls handshake: %w", request.Endpoint.String(), err)
		}
		connectionState := tlsConn.ConnectionState()
		state = &connectionState
		rw = tlsConn
	}
	if _, err := io.WriteString(rw, buildRequest(method, request)); err != nil {
		return result.Result{}, fmt.Errorf("probe %s: write request: %w", request.Endpoint.String(), err)
	}
	response, err := http.ReadResponse(bufio.NewReader(rw), &http.Request{Method: method})
	if err != nil {
		return result.Result{}, fmt.Errorf("probe %s: read response: %w", request.Endpoint.String(), err)
	}
	defer response.Body.Close()
	return capture(response, state)
}

func buildRequest(method string, request Request) string {
	var b strings.Builder
	b.WriteString(method + " " + request.Path + " HTTP/1.1\r\nHost: " + request.Host + "\r\n")
	hasUserAgent := false
	for _, header := range request.Headers {
		if strings.EqualFold(header.Name, "User-Agent") {
			hasUserAgent = true
		}
		for _, value := range header.Values {
			b.WriteString(header.Name + ": " + value + "\r\n")
		}
	}
	if !hasUserAgent {
		b.WriteString("User-Agent: varco\r\n")
	}
	b.WriteString("Connection: close\r\n\r\n")
	return b.String()
}

func capture(response *http.Response, state *tls.ConnectionState) (result.Result, error) {
	hasher := sha256.New()
	size, err := io.Copy(hasher, io.LimitReader(response.Body, maxBodyBytes))
	if err != nil {
		return result.Result{}, fmt.Errorf("read response body: %w", err)
	}
	truncated := false
	if size == maxBodyBytes {
		var extra [1]byte
		n, readErr := response.Body.Read(extra[:])
		if n > 0 {
			truncated = true
		} else if readErr != nil && !errors.Is(readErr, io.EOF) {
			return result.Result{}, fmt.Errorf("read response body: %w", readErr)
		}
	}
	captured := result.Result{StatusCode: response.StatusCode, Headers: canonicalHeaders(response.Header), BodySHA256: hex.EncodeToString(hasher.Sum(nil)), BodySize: size, Truncated: truncated}
	if state != nil {
		captured.TLSVersion = tls.VersionName(state.Version)
		captured.CipherSuite = tls.CipherSuiteName(state.CipherSuite)
		captured.ALPN = state.NegotiatedProtocol
		if len(state.PeerCertificates) != 0 {
			leaf := state.PeerCertificates[0]
			fingerprint := sha256.Sum256(leaf.Raw)
			dnsNames := append([]string(nil), leaf.DNSNames...)
			sort.Strings(dnsNames)
			captured.Certificate = &result.Certificate{Subject: leaf.Subject.String(), Issuer: leaf.Issuer.String(), Serial: leaf.SerialNumber.Text(16), NotBefore: leaf.NotBefore, NotAfter: leaf.NotAfter, Fingerprint: hex.EncodeToString(fingerprint[:]), DNSNames: dnsNames}
		}
	}
	return captured, nil
}

func canonicalHeaders(headers http.Header) []result.Header {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, http.CanonicalHeaderKey(name))
	}
	sort.Strings(names)
	resultHeaders := make([]result.Header, 0, len(names))
	for _, name := range names {
		resultHeaders = append(resultHeaders, result.Header{Name: name, Values: append([]string(nil), headers.Values(name)...)})
	}
	return resultHeaders
}

func safeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

// CredentialHeader reports whether name is a request header that may carry
// credentials. The list is best effort: custom credential headers outside it
// are not detected and remain the caller's responsibility.
func CredentialHeader(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Authorization", "Cookie", "Proxy-Authorization", "X-Api-Key", "X-Auth-Token", "Private-Token", "Api-Key":
		return true
	}
	return false
}

// ForbiddenHeader reports headers the prober owns and callers may not set.
func ForbiddenHeader(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Host", "Content-Length", "Transfer-Encoding", "Trailer", "Connection":
		return true
	}
	return false
}

// ValidHeaderName reports whether name is an RFC 9110 token.
func ValidHeaderName(name string) bool { return validToken(name) }

// ValidHeaderValue rejects control bytes (including CR, LF and NUL) except tab.
func ValidHeaderValue(value string) bool {
	for i := 0; i < len(value); i++ {
		if c := value[i]; (c < 0x20 && c != '\t') || c == 0x7f {
			return false
		}
	}
	return true
}

// ValidPath accepts an origin-form request target starting with "/" and
// containing no space, control or DEL bytes. It is otherwise sent verbatim.
func ValidPath(path string) bool {
	if !strings.HasPrefix(path, "/") {
		return false
	}
	for i := 0; i < len(path); i++ {
		if c := path[i]; c <= 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

// ValidHost rejects empty hosts and bytes that could alter the request framing
// or be read as URL structure: whitespace, controls, / ? # @ and backslash.
func ValidHost(host string) bool {
	if host == "" {
		return false
	}
	for i := 0; i < len(host); i++ {
		if c := host[i]; c <= 0x20 || c == 0x7f || strings.IndexByte(`/?#@\`, c) >= 0 {
			return false
		}
	}
	return true
}

func validToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0 {
			continue
		}
		return false
	}
	return true
}

func diagnosticError(code diag.Code, message string, cause error) error {
	diagnostic, err := diag.New(code, message)
	if err != nil {
		return err
	}
	return &diag.Error{Diagnostic: diagnostic, Cause: cause}
}
