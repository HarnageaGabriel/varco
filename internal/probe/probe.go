// Package probe sends constrained HTTP probes directly to numeric endpoints.
package probe

import (
	"bufio"
	"bytes"
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

const (
	defaultMaxBodyBytes = int64(256 << 20)
	defaultTimeout      = 10 * time.Second
	maxHeaderBytes      = 64 << 10
	maxInterimResponses = 8
)

var (
	errInvalidResponse     = errors.New("invalid HTTP response")
	errInvalidResponseBody = errors.New("invalid HTTP response body")
	errTooManyInterim      = errors.New("too many interim responses")
)

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
	MaxBodyBytes      int64
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
		return result.Result{}, diagnosticError(diag.CodeInvalidFlag, fmt.Sprintf("invalid request path (length %d)", len(request.Path)), nil)
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

	if request.Timeout <= 0 {
		request.Timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, request.Timeout)
	defer cancel()
	maxBodyBytes := request.MaxBodyBytes
	if maxBodyBytes == 0 {
		maxBodyBytes = defaultMaxBodyBytes
	}
	if maxBodyBytes < 0 {
		return result.Result{}, diagnosticError(diag.CodeInvalidFlag, "maximum body bytes must not be negative", nil)
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", request.Endpoint.String())
	if err != nil {
		return result.Result{}, ioFailure(ctx, request.Endpoint.String(), "", err)
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() {
		if errors.Is(ctx.Err(), context.Canceled) {
			_ = conn.Close()
		}
	})
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	rw := conn
	var state *tls.ConnectionState
	if request.Scheme == "https" {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec // probes capture certificates and accept old protocols; they do not assert trust.
			MinVersion:         tls.VersionTLS10,
			ServerName:         request.SNI,
			NextProtos:         []string{"http/1.1"},
		}
		tlsConn := tls.Client(conn, tlsConfig)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return result.Result{}, ioFailure(ctx, request.Endpoint.String(), "tls handshake", err)
		}
		connectionState := tlsConn.ConnectionState()
		state = &connectionState
		rw = tlsConn
	}
	if _, err := io.WriteString(rw, buildRequest(method, request)); err != nil {
		return result.Result{}, ioFailure(ctx, request.Endpoint.String(), "write request", err)
	}
	response, err := readResponse(rw, method) //nolint:bodyclose // body is consumed by capture; the connection is closed by defer conn.Close.
	if err != nil {
		if errors.Is(err, errTooManyInterim) {
			return result.Result{}, fmt.Errorf("probe %s: too many interim responses", request.Endpoint.String())
		}
		return result.Result{}, ioFailure(ctx, request.Endpoint.String(), "read response", err)
	}
	captured, err := capture(response, state, maxBodyBytes)
	if err != nil {
		return result.Result{}, ioFailure(ctx, request.Endpoint.String(), "", err)
	}
	return captured, nil
}

func readResponse(source io.Reader, method string) (*http.Response, error) {
	reader := bufio.NewReader(source)
	for interim := 0; ; {
		header, err := readHeaderBlock(reader)
		if err != nil {
			return nil, err
		}
		parsed, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(header)), &http.Request{Method: method}) //nolint:bodyclose // body is never drained; the caller owns the response.
		if err != nil {
			return nil, errInvalidResponse
		}
		if parsed.StatusCode < 100 || parsed.StatusCode > 599 {
			return nil, errInvalidResponse
		}
		if parsed.StatusCode >= 200 || parsed.StatusCode == http.StatusSwitchingProtocols {
			response, err := http.ReadResponse(bufio.NewReader(io.MultiReader(bytes.NewReader(header), reader)), &http.Request{Method: method}) //nolint:bodyclose // body is consumed by capture.
			if err != nil {
				return nil, errInvalidResponse
			}
			return response, nil
		}
		interim++
		if interim > maxInterimResponses {
			return nil, errTooManyInterim
		}
	}
}

func readHeaderBlock(reader *bufio.Reader) ([]byte, error) {
	var block bytes.Buffer
	for {
		fragment, err := reader.ReadSlice('\n')
		if block.Len()+len(fragment) > maxHeaderBytes {
			return nil, fmt.Errorf("response header exceeds %d bytes", maxHeaderBytes)
		}
		_, _ = block.Write(fragment)
		if err != nil && !errors.Is(err, bufio.ErrBufferFull) {
			return nil, err
		}
		if err == nil && (bytes.Equal(fragment, []byte("\n")) || bytes.Equal(fragment, []byte("\r\n"))) {
			return block.Bytes(), nil
		}
	}
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

func capture(response *http.Response, state *tls.ConnectionState, maxBodyBytes int64) (result.Result, error) {
	hasher := sha256.New()
	size, err := io.Copy(hasher, io.LimitReader(response.Body, maxBodyBytes))
	if err != nil {
		return result.Result{}, errInvalidResponseBody
	}
	truncated := false
	if size == maxBodyBytes {
		var extra [1]byte
		n, readErr := response.Body.Read(extra[:])
		if n > 0 {
			truncated = true
		} else if readErr != nil && !errors.Is(readErr, io.EOF) {
			return result.Result{}, errInvalidResponseBody
		}
	}
	headers := canonicalHeaders(response.Header)
	for _, trailer := range canonicalHeaders(response.Trailer) {
		trailer.Name = "Trailer:" + trailer.Name
		headers = append(headers, trailer)
	}
	sort.Slice(headers, func(i, j int) bool { return headers[i].Name < headers[j].Name })
	captured := result.Result{StatusCode: response.StatusCode, Headers: headers, BodySHA256: hex.EncodeToString(hasher.Sum(nil)), BodySize: size, Truncated: truncated}
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

// ValidSNI accepts an IP address or DNS hostname without a port.
func ValidSNI(name string) bool {
	if net.ParseIP(name) != nil {
		return true
	}
	if name == "" || len(name) > 253 || strings.ContainsAny(name, ":[]") {
		return false
	}
	name = strings.TrimSuffix(name, ".")
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' {
				return false
			}
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

// ioFailure reports an I/O error from a probe, preferring the probe context's
// own error when it is already done. Cancellation and deadlines must surface as
// such rather than as a closed connection or a bare socket error.
func ioFailure(ctx context.Context, endpointName, stage string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		err = ctxErr
	}
	if stage == "" {
		return fmt.Errorf("probe %s: %w", endpointName, err)
	}
	return fmt.Errorf("probe %s: %s: %w", endpointName, stage, err)
}

func diagnosticError(code diag.Code, message string, cause error) error {
	diagnostic, err := diag.New(code, message)
	if err != nil {
		return err
	}
	return &diag.Error{Diagnostic: diagnostic, Cause: cause}
}
