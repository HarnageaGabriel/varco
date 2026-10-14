package cli

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func serverReturning(body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
}

// Source: local httptest servers stand in for old and new endpoints.
// Source: exit codes in docs/exit-codes.md; flag errors from the standard flag package.
func TestRunDiff(t *testing.T) {
	a, b, c := serverReturning("same"), serverReturning("same"), serverReturning("other")
	defer a.Close()
	defer b.Close()
	defer c.Close()
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := l.Addr().String()
	_ = l.Close()
	addr := func(s *httptest.Server) string { return s.Listener.Addr().String() }
	common := []string{"--scheme", "http", "--host", "app.example", "--rate", "1000", "--timeout", "5s"}
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{"identical", []string{"--old", addr(a), "--new", addr(b)}, 0, "identical", ""},
		{"different", []string{"--old", addr(a), "--new", addr(c)}, 1, "VAR-CMP-0006", ""},
		{"different json", []string{"--old", addr(a), "--new", addr(c), "--format", "json"}, 1, `"schema_version": 1`, ""},
		{"unreachable", []string{"--old", addr(a), "--new", dead}, 3, "", "VAR-NET-0001"},
		{"hostname", []string{"--old", "example.com", "--new", addr(b)}, 2, "", "VAR-CFG-0001"},
		{"unsafe method", []string{"--old", addr(a), "--new", addr(b), "--method", "POST"}, 2, "", "VAR-RUN-0001"},
		{"unsafe method allowed", []string{"--old", addr(a), "--new", addr(b), "--method", "POST", "--allow-unsafe-methods"}, 0, "identical", ""},
		{"credential header", []string{"--old", addr(a), "--new", addr(b), "--header", "Authorization=x"}, 2, "", "VAR-CFG-0001"},
		{"bad header", []string{"--old", addr(a), "--new", addr(b), "--header", "nope"}, 2, "", "NAME=VALUE"},
		{"bad format", []string{"--old", addr(a), "--new", addr(b), "--format", "xml"}, 2, "", "--format"},
		{"bad scheme", []string{"--old", addr(a), "--new", addr(b), "--scheme", "ftp"}, 2, "", "--scheme"},
		{"bad rate", []string{"--old", addr(a), "--new", addr(b), "--rate", "0"}, 2, "", "--rate"},
		{"rate too high", []string{"--old", addr(a), "--new", addr(b), "--rate", "1e10"}, 2, "", "--rate"},
		{"rate too low", []string{"--old", addr(a), "--new", addr(b), "--rate", "1e-12"}, 2, "", "--rate"},
		{"rate NaN", []string{"--old", addr(a), "--new", addr(b), "--rate", "NaN"}, 2, "", "--rate"},
		{"header CRLF value", []string{"--old", addr(a), "--new", addr(b), "--header", "X-A=a\r\nX-B: b"}, 2, "", "invalid value"},
		{"header bad name", []string{"--old", addr(a), "--new", addr(b), "--header", "X A=1"}, 2, "", "invalid request header name"},
		{"header Host", []string{"--old", addr(a), "--new", addr(b), "--header", "Host=evil"}, 2, "", "use --host"},
		{"header Content-Length", []string{"--old", addr(a), "--new", addr(b), "--header", "content-length=5"}, 2, "", "cannot be overridden"},
		{"header Transfer-Encoding", []string{"--old", addr(a), "--new", addr(b), "--header", "Transfer-Encoding=chunked"}, 2, "", "cannot be overridden"},
		{"header Trailer", []string{"--old", addr(a), "--new", addr(b), "--header", "Trailer=X"}, 2, "", "cannot be overridden"},
		{"credential X-Api-Key", []string{"--old", addr(a), "--new", addr(b), "--header", "x-api-key=k"}, 2, "", "not allowed"},
		{"host with slash", []string{"--old", addr(a), "--new", addr(b), "--host", "a/b"}, 2, "", "--host"},
		{"host with space", []string{"--old", addr(a), "--new", addr(b), "--host", "a b"}, 2, "", "--host"},
		{"SNI with port", []string{"--old", addr(a), "--new", addr(b), "--sni", "app.example:443"}, 2, "", "--sni"},
		{"path CRLF", []string{"--old", addr(a), "--new", addr(b), "/x\r\nX: 1"}, 2, "", "control byte"},
		{"path with query", []string{"--old", addr(a), "--new", addr(b), "/q?x=1"}, 0, "identical", ""},
		{"bad timeout", []string{"--old", addr(a), "--new", addr(b), "--timeout", "0s"}, 2, "", "--timeout"},
		{"bad path", []string{"--old", addr(a), "--new", addr(b), "x"}, 2, "", "must start with /"},
		{"missing flags", nil, 2, "", "required"},
		{"unknown flag", []string{"--nope"}, 2, "", "flag provided but not defined: -nope"},
		{"bad new", []string{"--old", addr(a), "--new", "nonsense"}, 2, "", "invalid --new"},
		{"ignore header", []string{"--old", addr(a), "--new", addr(b), "--ignore-header", "x-frame-options"}, 0, "identical", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"diff"}, common...)
			args = append(args, test.args...)
			if test.name == "missing flags" {
				args = []string{"diff"}
			}
			var stdout, stderr bytes.Buffer
			if got := Run(args, stdin(), &stdout, &stderr); got != test.wantCode {
				t.Fatalf("Run() = %d, want %d; stderr=%q", got, test.wantCode, stderr.String())
			}
			if !strings.Contains(stdout.String(), test.wantStdout) {
				t.Errorf("stdout = %q, want %q", stdout.String(), test.wantStdout)
			}
			if !strings.Contains(stderr.String(), test.wantStderr) {
				t.Errorf("stderr = %q, want %q", stderr.String(), test.wantStderr)
			}
		})
	}
}

func TestRunDiffJSONValid(t *testing.T) {
	a, c := serverReturning("a"), serverReturning("b")
	defer a.Close()
	defer c.Close()
	var stdout, stderr bytes.Buffer
	code := Run([]string{"diff", "--scheme", "http", "--host", "h", "--rate", "1000", "--format", "json", "--old", a.Listener.Addr().String(), "--new", c.Listener.Addr().String(), "/", "/x"}, stdin(), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("code %d %s", code, stderr.String())
	}
	var doc struct {
		SchemaVersion int  `json:"schema_version"`
		Redacted      bool `json:"redacted"`
		Identical     bool `json:"identical"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil || doc.SchemaVersion != 1 || !doc.Redacted || doc.Identical {
		t.Fatalf("doc %+v err %v", doc, err)
	}
}

// Source: docs/usage.md "Ignored by default"; chunked vs Content-Length framing
// and per-server Set-Cookie must not produce differences end to end.
func TestRunDiffIgnoresTransportNoise(t *testing.T) {
	body := strings.Repeat("payload ", 100)
	withLength := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "800")
		w.Header().Add("Set-Cookie", "session=one")
		_, _ = w.Write([]byte(body))
	}))
	chunked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("Set-Cookie", "session=two")
		_, _ = w.Write([]byte(body[:100]))
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte(body[100:]))
	}))
	defer withLength.Close()
	defer chunked.Close()
	var stdout, stderr bytes.Buffer
	code := Run([]string{"diff", "--scheme", "http", "--host", "h", "--rate", "1000", "--old", withLength.Listener.Addr().String(), "--new", chunked.Listener.Addr().String()}, stdin(), &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "identical") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

// Source: review finding 5; JSON output order must not follow argument order.
func TestRunDiffPathOrderDeterministic(t *testing.T) {
	serve := func(suffix string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(r.RequestURI + suffix))
		}))
	}
	a, b := serve("x"), serve("y")
	defer a.Close()
	defer b.Close()
	run := func(paths ...string) string {
		args := append([]string{"diff", "--scheme", "http", "--host", "h", "--rate", "1000", "--format", "json", "--old", a.Listener.Addr().String(), "--new", b.Listener.Addr().String()}, paths...)
		var stdout, stderr bytes.Buffer
		if code := Run(args, stdin(), &stdout, &stderr); code != 1 {
			t.Fatalf("code=%d stderr=%q", code, stderr.String())
		}
		return stdout.String()
	}
	forward, reversed := run("/a", "/b", "/a"), run("/b", "/a")
	if forward != reversed || strings.Index(forward, `"/a"`) > strings.Index(forward, `"/b"`) {
		t.Fatalf("forward: %s | reversed: %s", forward, reversed)
	}
}

// Source: review minor finding; one failing path must not abort the others (exit 3 at the end).
func TestRunDiffContinuesAfterProbeFailure(t *testing.T) {
	var okHits atomic.Int32
	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.RequestURI == "/ok" {
			okHits.Add(1)
		}
		_, _ = w.Write([]byte("same"))
	}))
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.RequestURI == "/bad?token=super-secret" {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		_, _ = w.Write([]byte("same"))
	}))
	defer old.Close()
	defer broken.Close()
	var stdout, stderr bytes.Buffer
	code := Run([]string{"diff", "--scheme", "http", "--host", "h", "--rate", "1000", "--old", old.Listener.Addr().String(), "--new", broken.Listener.Addr().String(), "/bad?token=super-secret", "/ok"}, stdin(), &stdout, &stderr)
	if code != 3 || !strings.Contains(stderr.String(), "VAR-NET-0001") || strings.Contains(stderr.String(), "super-secret") || !strings.Contains(stderr.String(), "path of length") || okHits.Load() != 1 {
		t.Fatalf("code=%d okHits=%d stderr=%q", code, okHits.Load(), stderr.String())
	}
}

// Source: RFC 6749 section 4.1.2: an OAuth authorization code can be carried
// in a Location query value and must not reach output without explicit opt-in.
func TestRunDiffRedactsLocationUnlessOptedIn(t *testing.T) {
	server := func(location string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", location)
			w.WriteHeader(http.StatusFound)
		}))
	}
	oldServer := server("/cb?code=old-super-secret")
	newServer := server("/cb?code=new-super-secret")
	defer oldServer.Close()
	defer newServer.Close()
	base := []string{"diff", "--scheme", "http", "--host", "h", "--rate", "1000", "--old", oldServer.Listener.Addr().String(), "--new", newServer.Listener.Addr().String()}

	for _, format := range []string{"table", "json"} {
		t.Run(format+" default", func(t *testing.T) {
			args := append(append([]string(nil), base...), "--format", format)
			var stdout, stderr bytes.Buffer
			if code := Run(args, stdin(), &stdout, &stderr); code != 1 {
				t.Fatalf("code=%d stderr=%q", code, stderr.String())
			}
			combined := stdout.String() + stderr.String()
			if strings.Contains(combined, "old-super-secret") || strings.Contains(combined, "new-super-secret") || !strings.Contains(stdout.String(), "REDACTED") || stderr.Len() != 0 {
				t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
		})
	}

	t.Run("show sensitive", func(t *testing.T) {
		args := append(append([]string(nil), base...), "--show-sensitive")
		var stdout, stderr bytes.Buffer
		if code := Run(args, stdin(), &stdout, &stderr); code != 1 {
			t.Fatalf("code=%d stderr=%q", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "old-super-secret") || !strings.Contains(stdout.String(), "new-super-secret") || strings.Count(stderr.String(), "\n") != 1 || !strings.Contains(stderr.String(), "warning:") {
			t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
	})
}

// Source: net/http/httptest NewTLSServer; end-to-end HTTPS path including SNI/cert capture.
func TestRunDiffHTTPS(t *testing.T) {
	var sni atomic.Value
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("tls")) }))
	server.TLS = &tls.Config{GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) { sni.Store(h.ServerName); return nil, nil }}
	server.StartTLS()
	defer server.Close()
	addr := server.Listener.Addr().String()
	var stdout, stderr bytes.Buffer
	code := Run([]string{"diff", "--host", "app.example", "--sni", "edge.example", "--rate", "1000", "--old", addr, "--new", addr, "/"}, stdin(), &stdout, &stderr)
	if code != 0 || sni.Load() != "edge.example" {
		t.Fatalf("code=%d sni=%v stderr=%q", code, sni.Load(), stderr.String())
	}
}

func TestRunDiffDefaultSNIStripsHostPort(t *testing.T) {
	var sni atomic.Value
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.TLS = &tls.Config{GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		sni.Store(hello.ServerName)
		return nil, nil
	}}
	server.StartTLS()
	defer server.Close()
	addr := server.Listener.Addr().String()
	var stdout, stderr bytes.Buffer
	code := Run([]string{"diff", "--host", "app.example:8443", "--rate", "1000", "--old", addr, "--new", addr}, stdin(), &stdout, &stderr)
	if code != 0 || sni.Load() != "app.example" {
		t.Fatalf("code=%d sni=%v stderr=%q", code, sni.Load(), stderr.String())
	}
}

func TestRunDiffInvalidPathIsRedacted(t *testing.T) {
	const secret = "token=super-secret"
	var stdout, stderr bytes.Buffer
	code := Run([]string{"diff", "--old", "127.0.0.1", "--new", "127.0.0.2", "--host", "h", "/bad path?" + secret}, stdin(), &stdout, &stderr)
	if code != 2 || strings.Contains(stderr.String(), secret) || !strings.Contains(stderr.String(), "length") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

// Source: flag package docs: ErrHelp means the user asked for help, which is success.
func TestRunHelpFlagsExitZero(t *testing.T) {
	for _, args := range [][]string{{"diff", "-h"}, {"diff", "--help"}, {"version", "-h"}} {
		var stdout, stderr bytes.Buffer
		if code := Run(args, stdin(), &stdout, &stderr); code != 0 || !strings.Contains(stderr.String(), "Usage") {
			t.Fatalf("%v: code=%d stderr=%q", args, code, stderr.String())
		}
	}
}
