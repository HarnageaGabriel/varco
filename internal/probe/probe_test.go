package probe

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/endpoint"
	"github.com/HarnageaGabriel/varco/internal/result"
)

func endpointOf(t *testing.T, address string) endpoint.Endpoint {
	t.Helper()
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		t.Fatal(err)
	}
	return endpoint.Endpoint{Addr: ap.Addr(), Port: ap.Port()}
}

// Source: net/http/httptest docs (NewUnstartedServer + StartTLS) for SNI capture.
func TestDoTLSSNIAndHost(t *testing.T) {
	var mu sync.Mutex
	var sni, host string
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		host = r.Host
		mu.Unlock()
		_, _ = w.Write([]byte("hello"))
	}))
	server.TLS = &tls.Config{GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
		mu.Lock()
		sni = h.ServerName
		mu.Unlock()
		return nil, nil
	}}
	server.StartTLS()
	defer server.Close()
	got, err := Do(context.Background(), Request{Endpoint: endpointOf(t, server.Listener.Addr().String()), Scheme: "https", Host: "api.example.com", SNI: "sni.example.com", Method: "GET", Path: "/x", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if sni != "sni.example.com" || host != "api.example.com" {
		t.Fatalf("sni=%q host=%q", sni, host)
	}
	if got.StatusCode != 200 || got.Certificate == nil || got.TLSVersion == "" || got.BodySize != 5 || got.Truncated || got.ALPN == "h2" {
		t.Fatalf("unexpected result %+v", got)
	}
	if len(got.Certificate.Fingerprint) != 64 {
		t.Fatalf("fingerprint %q", got.Certificate.Fingerprint)
	}
}

func TestDoRedirectNotFollowed(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Redirect(w, r, "http://elsewhere.invalid/", http.StatusFound)
	}))
	defer server.Close()
	got, err := Do(context.Background(), Request{Endpoint: endpointOf(t, server.Listener.Addr().String()), Scheme: "http", Host: "h.example", Method: "GET", Path: "/", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if got.StatusCode != 302 || hits != 1 || got.Certificate != nil {
		t.Fatalf("status=%d hits=%d", got.StatusCode, hits)
	}
}

func TestDoRefusals(t *testing.T) {
	base := Request{Endpoint: endpoint.Endpoint{Addr: netip.MustParseAddr("127.0.0.1"), Port: 1}, Scheme: "http", Host: "h", Method: "GET", Path: "/", Timeout: time.Second}
	tests := []struct {
		name   string
		mutate func(*Request)
		code   diag.Code
	}{
		{"unsafe method", func(r *Request) { r.Method = "POST" }, diag.CodeUnsupportedMethod},
		{"credential header", func(r *Request) { r.Headers = []result.Header{{Name: "authorization", Values: []string{"x"}}} }, diag.CodeInvalidFlag},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := base
			test.mutate(&r)
			_, err := Do(context.Background(), r)
			var de *diag.Error
			if !errors.As(err, &de) || de.Diagnostic.Code != test.code {
				t.Fatalf("err = %v", err)
			}
		})
	}
	r := base
	r.Scheme = "ftp"
	if _, err := Do(context.Background(), r); err == nil {
		t.Fatal("expected scheme error")
	}
}

func TestDoUnsafeMethodAllowed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("X-A", "1")
		w.Header().Add("X-A", "2")
		_, _ = w.Write([]byte(r.Method + r.Header.Get("X-Custom")))
	}))
	defer server.Close()
	got, err := Do(context.Background(), Request{Endpoint: endpointOf(t, server.Listener.Addr().String()), Scheme: "http", Host: "h", Method: "post", Path: "/", Headers: []result.Header{{Name: "X-Custom", Values: []string{"v"}}}, Timeout: 5 * time.Second, AllowUnsafeMethod: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.BodySize != int64(len("POSTv")) {
		t.Fatalf("size %d", got.BodySize)
	}
	found := false
	for _, h := range got.Headers {
		if h.Name == "X-A" && strings.Join(h.Values, ",") == "1,2" {
			found = true
		}
	}
	if !found {
		t.Fatalf("headers %+v", got.Headers)
	}
}

func TestDoBodyTruncation(t *testing.T) {
	chunk := make([]byte, 1<<20)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		for i := 0; i < 9; i++ {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	got, err := Do(context.Background(), Request{Endpoint: endpointOf(t, server.Listener.Addr().String()), Scheme: "http", Host: "h", Method: "GET", Path: "/", Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Truncated || got.BodySize != maxBodyBytes {
		t.Fatalf("truncated=%v size=%d", got.Truncated, got.BodySize)
	}
}

func TestDoConnectFailure(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	if _, err = Do(context.Background(), Request{Endpoint: endpointOf(t, addr), Scheme: "http", Host: "h", Method: "GET", Path: "/", Timeout: time.Second}); err == nil {
		t.Fatal("expected error")
	}
}
