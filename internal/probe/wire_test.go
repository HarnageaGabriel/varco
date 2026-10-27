package probe

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/result"
)

func base(t *testing.T, server *httptest.Server, scheme string) Request {
	t.Helper()
	return Request{Endpoint: endpointOf(t, server.Listener.Addr().String()), Scheme: scheme, Host: "h.example", SNI: "h.example", Method: "GET", Path: "/", Timeout: 5 * time.Second}
}

func rawListener(t *testing.T, handler func(net.Conn)) (Request, func()) {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		reader := bufio.NewReader(conn)
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil || line == "\r\n" {
				break
			}
		}
		handler(conn)
	}()
	request := Request{Endpoint: endpointOf(t, listener.Addr().String()), Scheme: "http", Host: "h.example", Method: "GET", Path: "/", Timeout: 2 * time.Second}
	cleanup := func() {
		_ = listener.Close()
		<-done
	}
	return request, cleanup
}

// Source: RFC 9110 sections 15.2 and 15.3; informational responses precede
// the final response and do not become the probe result.
func TestDoSkipsInterimResponses(t *testing.T) {
	for _, interim := range []string{"100 Continue", "103 Early Hints"} {
		t.Run(interim, func(t *testing.T) {
			request, cleanup := rawListener(t, func(conn net.Conn) {
				_, _ = io.WriteString(conn, "HTTP/1.1 "+interim+"\r\nX-Interim: yes\r\n\r\nHTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
			})
			defer cleanup()
			got, err := Do(context.Background(), request)
			if err != nil || got.StatusCode != 200 || got.BodySize != 2 {
				t.Fatalf("result=%+v err=%v", got, err)
			}
		})
	}
}

func TestDoLimitsInterimResponses(t *testing.T) {
	request, cleanup := rawListener(t, func(conn net.Conn) {
		_, _ = io.WriteString(conn, strings.Repeat("HTTP/1.1 100 Continue\r\n\r\n", maxInterimResponses+1))
	})
	defer cleanup()
	if _, err := Do(context.Background(), request); err == nil || !strings.Contains(err.Error(), "too many interim") {
		t.Fatalf("err=%v", err)
	}
}

func TestDoBodylessStatus(t *testing.T) {
	for _, status := range []string{"204 No Content", "304 Not Modified"} {
		t.Run(status, func(t *testing.T) {
			request, cleanup := rawListener(t, func(conn net.Conn) {
				_, _ = io.WriteString(conn, "HTTP/1.1 "+status+"\r\nConnection: keep-alive\r\n\r\n")
			})
			defer cleanup()
			got, err := Do(context.Background(), request)
			if err != nil || got.BodySize != 0 {
				t.Fatalf("result=%+v err=%v", got, err)
			}
		})
	}
}

func TestDoCapturesTrailers(t *testing.T) {
	request, cleanup := rawListener(t, func(conn net.Conn) {
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nTrailer: X-Checksum\r\n\r\n2\r\nok\r\n0\r\nX-Checksum: abc\r\n\r\n")
	})
	defer cleanup()
	got, err := Do(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	for _, header := range got.Headers {
		if header.Name == "Trailer:X-Checksum" && len(header.Values) == 1 && header.Values[0] == "abc" {
			return
		}
	}
	t.Fatalf("headers=%+v", got.Headers)
}

func TestDoRejectsOversizeHeader(t *testing.T) {
	request, cleanup := rawListener(t, func(conn net.Conn) {
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nX-Large: "+strings.Repeat("a", maxHeaderBytes)+"\r\n\r\n")
	})
	defer cleanup()
	if _, err := Do(context.Background(), request); err == nil || !strings.Contains(err.Error(), "header exceeds") {
		t.Fatalf("err=%v", err)
	}
}

func TestDoResponseParseErrorDoesNotExposeHeaderValue(t *testing.T) {
	const secret = "header-super-secret"
	request, cleanup := rawListener(t, func(conn net.Conn) {
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nX-Test: "+secret+"\x00\r\n\r\n")
	})
	defer cleanup()
	if _, err := Do(context.Background(), request); err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "invalid HTTP response") {
		t.Fatalf("err=%v", err)
	}
}

func TestDoTrailerParseErrorDoesNotExposeHeaderValue(t *testing.T) {
	const secret = "trailer-super-secret"
	request, cleanup := rawListener(t, func(conn net.Conn) {
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nTrailer: X-Test\r\n\r\n0\r\nX-Test: "+secret+"\x00\r\n\r\n")
	})
	defer cleanup()
	if _, err := Do(context.Background(), request); err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "invalid HTTP response body") {
		t.Fatalf("err=%v", err)
	}
}

func TestDoInvalidPathErrorIsRedacted(t *testing.T) {
	const secret = "token=super-secret"
	request := Request{Scheme: "http", Host: "h", Method: "GET", Path: "/bad path?" + secret}
	if _, err := Do(context.Background(), request); err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "length") {
		t.Fatalf("err=%v", err)
	}
}

func TestDoContextCancelMidBody(t *testing.T) {
	wrote := make(chan struct{})
	request, cleanup := rawListener(t, func(conn net.Conn) {
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 10\r\n\r\nx")
		close(wrote)
		_, _ = io.Copy(io.Discard, conn)
	})
	defer cleanup()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-wrote; cancel() }()
	if _, err := Do(ctx, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

// Source: RFC 9112 section 3.2.1 (origin-form is sent as-is); the review
// blocker was net/url re-escaping %2F, ? and friends.
func TestDoRequestTargetVerbatim(t *testing.T) {
	var mu sync.Mutex
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = r.RequestURI
		mu.Unlock()
	}))
	defer server.Close()
	for _, path := range []string{"/a%2Fb", "/a/../b", "//x", "/q?x=1", "////uuid", "/a%20b", "/p#frag", "/a?b=%2F&c", "/é"} {
		t.Run(path, func(t *testing.T) {
			request := base(t, server, "http")
			request.Path = path
			if _, err := Do(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if got != path {
				t.Fatalf("RequestURI = %q, want %q", got, path)
			}
		})
	}
}

// Source: RFC 9112 section 2.2 (CR/LF framing) and request-smuggling advisories.
func TestDoRejectsUnsafeInput(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer server.Close()
	header := func(name, value string) func(*Request) {
		return func(r *Request) { r.Headers = []result.Header{{Name: name, Values: []string{value}}} }
	}
	tests := []struct {
		name   string
		mutate func(*Request)
	}{
		{"path CRLF", func(r *Request) { r.Path = "/x\r\nX-Injected: 1" }},
		{"path LF", func(r *Request) { r.Path = "/x\nHost: evil" }},
		{"path NUL", func(r *Request) { r.Path = "/x\x00" }},
		{"path space", func(r *Request) { r.Path = "/x HTTP/1.1\r\n" }},
		{"path DEL", func(r *Request) { r.Path = "/x\x7f" }},
		{"path relative", func(r *Request) { r.Path = "x" }},
		{"host CRLF", func(r *Request) { r.Host = "h\r\nX: 1" }},
		{"host slash", func(r *Request) { r.Host = "h/x" }},
		{"host at", func(r *Request) { r.Host = "u@h" }},
		{"host empty", func(r *Request) { r.Host = "" }},
		{"Host override CRLF", header("Host", "h\r\nX: 1")},
		{"Host override slash", header("Host", "h/x")},
		{"Host override at", header("Host", "u@h")},
		{"Host override empty", header("Host", "")},
		{"Host override two values", func(r *Request) { r.Headers = []result.Header{{Name: "Host", Values: []string{"a", "b"}}} }},
		{"Host override duplicate", func(r *Request) {
			r.Headers = []result.Header{{Name: "Host", Values: []string{"a"}}, {Name: "Host", Values: []string{"b"}}}
		}},
		{"header value CRLF", header("X-A", "a\r\nX-B: b")},
		{"header name space", header("X A", "a")},
		{"header Content-Length", header("Content-Length", "5")},
		{"header Transfer-Encoding", header("Transfer-Encoding", "chunked")},
		{"header Trailer", header("Trailer", "X")},
		{"credential X-Api-Key", header("x-api-key", "k")},
		{"credential Private-Token", header("Private-Token", "k")},
		{"method token", func(r *Request) { r.Method = "GE T" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := base(t, server, "http")
			test.mutate(&request)
			_, err := Do(context.Background(), request)
			var de *diag.Error
			if !errors.As(err, &de) || de.Diagnostic.Code != diag.CodeInvalidFlag {
				t.Fatalf("err = %v", err)
			}
		})
	}
	if hits.Load() != 0 {
		t.Fatalf("server was hit %d times by rejected requests", hits.Load())
	}
}

// Source: net/http Transport.DisableCompression docs; the prober must not
// decode so that body hashes and Content-Encoding reflect the wire.
func TestDoDoesNotDecompress(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write([]byte("hello hello hello"))
	_ = zw.Close()
	compressed := buf.Bytes()
	var acceptEncoding atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		acceptEncoding.Store(r.Header.Get("Accept-Encoding"))
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(compressed)
	}))
	defer server.Close()
	got, err := Do(context.Background(), base(t, server, "http"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(compressed)
	if got.BodySHA256 != hex.EncodeToString(sum[:]) || got.BodySize != int64(len(compressed)) {
		t.Fatalf("body %s size %d", got.BodySHA256, got.BodySize)
	}
	if v, _ := acceptEncoding.Load().(string); v != "" {
		t.Fatalf("Accept-Encoding sent: %q", v)
	}
	found := false
	for _, h := range got.Headers {
		if h.Name == "Content-Encoding" && strings.Join(h.Values, ",") == "gzip" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Content-Encoding missing: %+v", got.Headers)
	}
}

// Source: RFC 9110 section 9.3.2 (HEAD has no content).
func TestDoHead(t *testing.T) {
	var method atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method.Store(r.Method)
		w.Header().Set("Content-Length", "100")
	}))
	defer server.Close()
	request := base(t, server, "http")
	request.Method = "head"
	got, err := Do(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if method.Load() != "HEAD" || got.StatusCode != 200 || got.BodySize != 0 {
		t.Fatalf("method=%v result=%+v", method.Load(), got)
	}
}

// Source: net/http ProxyFromEnvironment docs; probes must never use a proxy.
func TestDoIgnoresProxyEnvironment(t *testing.T) {
	var proxyHits, serverHits atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { proxyHits.Add(1) }))
	defer proxy.Close()
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { serverHits.Add(1) }))
	defer server.Close()
	for _, name := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY"} {
		t.Setenv(name, proxy.URL)
	}
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	if _, err := Do(context.Background(), base(t, server, "http")); err != nil {
		t.Fatal(err)
	}
	if proxyHits.Load() != 0 || serverHits.Load() != 1 {
		t.Fatalf("proxy=%d server=%d", proxyHits.Load(), serverHits.Load())
	}
}

// Source: crypto/tls Config.MinVersion docs (client default is TLS 1.2).
func TestDoNegotiatesLegacyTLS(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("x")) }))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS11} //nolint:gosec // legacy server under test.
	server.StartTLS()
	defer server.Close()
	got, err := Do(context.Background(), base(t, server, "https"))
	if err != nil {
		t.Fatal(err)
	}
	if got.TLSVersion != "TLS 1.1" {
		t.Fatalf("TLSVersion = %q", got.TLSVersion)
	}
}

func TestDoNegotiatesTLS13(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("x")) }))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13}
	server.StartTLS()
	defer server.Close()
	got, err := Do(context.Background(), base(t, server, "https"))
	if err != nil || got.TLSVersion != "TLS 1.3" {
		t.Fatalf("TLSVersion=%q err=%v", got.TLSVersion, err)
	}
}

func TestDoTLSWithoutDNSName(t *testing.T) {
	var names []string
	var mu sync.Mutex
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.TLS = &tls.Config{GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		mu.Lock()
		names = append(names, hello.ServerName)
		mu.Unlock()
		return nil, nil
	}}
	server.StartTLS()
	defer server.Close()
	for _, sni := range []string{"", "127.0.0.1"} {
		request := base(t, server, "https")
		request.SNI = sni
		if _, err := Do(context.Background(), request); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(names) != 2 || names[0] != "" || names[1] != "" {
		t.Fatalf("server names=%q", names)
	}
}

// Source: RFC 9110 section 15.4 (redirects) over TLS: not followed, Location kept.
func TestDoRedirectNotFollowedHTTPS(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "https://elsewhere.invalid/x", http.StatusMovedPermanently)
	}))
	defer server.Close()
	got, err := Do(context.Background(), base(t, server, "https"))
	if err != nil {
		t.Fatal(err)
	}
	location := ""
	for _, h := range got.Headers {
		if h.Name == "Location" {
			location = h.Values[0]
		}
	}
	if got.StatusCode != 301 || hits.Load() != 1 || location != "https://elsewhere.invalid/x" || got.Certificate == nil {
		t.Fatalf("status=%d hits=%d location=%q", got.StatusCode, hits.Load(), location)
	}
}

// Source: review finding 3; differences past the old 8 MiB cap must change the hash.
func TestDoHashesBeyondCap(t *testing.T) {
	serve := func(last byte) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			chunk := make([]byte, 1<<20)
			for i := 0; i < 9; i++ {
				_, _ = w.Write(chunk)
			}
			_, _ = w.Write([]byte{last})
		}))
	}
	a, b := serve('a'), serve('b')
	defer a.Close()
	defer b.Close()
	ra, err := Do(context.Background(), base(t, a, "http"))
	if err != nil {
		t.Fatal(err)
	}
	rb, err := Do(context.Background(), base(t, b, "http"))
	if err != nil {
		t.Fatal(err)
	}
	want := int64(9<<20 + 1)
	if ra.BodySize != want || rb.BodySize != want || ra.Truncated || ra.BodySHA256 == rb.BodySHA256 {
		t.Fatalf("sizes %d %d truncated=%v equalhash=%v", ra.BodySize, rb.BodySize, ra.Truncated, ra.BodySHA256 == rb.BodySHA256)
	}
}

func TestDoTimeout(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer server.Close()
	defer close(release)
	request := base(t, server, "http")
	request.Timeout = 100 * time.Millisecond
	if _, err := Do(context.Background(), request); !errors.Is(err, os.ErrDeadlineExceeded) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
}

func TestValidators(t *testing.T) {
	if !ValidHeaderValue("a\tb") || ValidHeaderValue("a\x00") || ValidHeaderValue("a\nb") {
		t.Fatal("header value validation")
	}
	if !ValidHeaderName("X-Custom_1") || ValidHeaderName("") || ValidHeaderName("a:b") {
		t.Fatal("header name validation")
	}
	if !ValidHost("api.example.com:8443") || ValidHost("a b") || ValidHost("a\\b") || ValidHost("a?b") || ValidHost("a#b") {
		t.Fatal("host validation")
	}
	if !ValidSNI("api.example.com") || !ValidSNI("127.0.0.1") || !ValidSNI("::1") || ValidSNI("") || ValidSNI("api.example.com:443") || ValidSNI("bad_name") {
		t.Fatal("SNI validation")
	}
}

// Source: internal/probe/probe.go ValidWireSNI; the names a probe may send as
// its TLS server name: letters, digits, hyphen, underscore and dot labels, an
// IP literal or nothing at all. Everything that could change the structure of
// the request is refused.
func TestValidWireSNI(t *testing.T) {
	tests := []struct {
		name   string
		sni    string
		accept bool
	}{
		{name: "empty", sni: "", accept: true},
		{name: "plain host", sni: "api.example.com", accept: true},
		{name: "underscore label", sni: "my_app.example.com", accept: true},
		{name: "trailing dot", sni: "api.example.com.", accept: true},
		{name: "ip literal", sni: "127.0.0.1", accept: true},
		{name: "ipv6 literal", sni: "::1", accept: true},
		{name: "253 bytes", sni: strings.Repeat("a", 253), accept: true},
		{name: "space", sni: "api example.com"},
		{name: "nul byte", sni: "api\x00.example.com"},
		{name: "colon", sni: "api.example.com:443"},
		{name: "slash", sni: "api.example.com/path"},
		{name: "at sign", sni: "api@example.com"},
		{name: "254 bytes", sni: strings.Repeat("a", 254)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ValidWireSNI(test.sni); got != test.accept {
				t.Fatalf("ValidWireSNI(%q) = %v, want %v", test.sni, got, test.accept)
			}
		})
	}
}

// Source: internal/probe/probe.go ValidMethod; a request method must be an
// RFC 9110 token to go on the wire.
func TestValidMethod(t *testing.T) {
	tests := []struct {
		name   string
		method string
		accept bool
	}{
		{name: "get", method: "GET", accept: true},
		{name: "lowercase", method: "get", accept: true},
		{name: "token punctuation", method: "M-SEARCH", accept: true},
		{name: "empty", method: ""},
		{name: "space", method: "GET POST"},
		{name: "crlf", method: "GET\r\nX-Injected: 1"},
		{name: "colon", method: "GET:"},
		{name: "control byte", method: "GET\x00"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ValidMethod(test.method); got != test.accept {
				t.Fatalf("ValidMethod(%q) = %v, want %v", test.method, got, test.accept)
			}
		})
	}
}
