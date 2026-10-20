package e2e

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const hostileServerDeadline = 5 * time.Second

// startRawServer listens on 127.0.0.1:0 and hands exactly one accepted
// connection to handler. The prober opens one connection per probe
// (internal/probe/probe.go), so a hostile server never serves more than one.
// The listener, the connection and the context end with the test.
func startRawServer(t *testing.T, handler func(context.Context, net.Conn)) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), hostileServerDeadline)
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	var (
		mu     sync.Mutex
		active net.Conn
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		mu.Lock()
		active = conn
		mu.Unlock()
		defer func() {
			_ = conn.Close()
			mu.Lock()
			active = nil
			mu.Unlock()
		}()
		if deadline, ok := ctx.Deadline(); ok {
			_ = conn.SetDeadline(deadline)
		}
		handler(ctx, conn)
	}()
	t.Cleanup(func() {
		cancel()
		_ = listener.Close()
		mu.Lock()
		if active != nil {
			_ = active.Close()
		}
		mu.Unlock()
		<-done
	})
	return listener.Addr().String()
}

// writeAll writes the whole buffer and stops at the first write error: a
// hostile peer may drop the connection at any moment.
func writeAll(conn net.Conn, data []byte) {
	for len(data) != 0 {
		n, err := conn.Write(data)
		if err != nil {
			return
		}
		data = data[n:]
	}
}

func startRawHTTPServer(t *testing.T, handler func(context.Context, net.Conn)) string {
	t.Helper()
	return startRawServer(t, func(ctx context.Context, conn net.Conn) {
		if readRequestHead(conn) != nil {
			return
		}
		handler(ctx, conn)
	})
}

func runHostileCLI(t *testing.T, plan, oldAddr, newAddr string) (runResult, runReportDoc, time.Duration) {
	t.Helper()
	started := time.Now() //nolint:forbidigo // time.Since needs a start instant for the required wall-time bounds.
	result := runCLI(t, []string{
		"run",
		"--plan", testDataPath(plan),
		"--old", oldAddr,
		"--new", newAddr,
		"--timeout", "1s",
		"--rate", "10000",
		"--concurrency", "1",
		"--quiet",
		"--format", "json",
	})
	elapsed := time.Since(started)
	if result.stderr != "" {
		t.Fatalf("stderr = %q, want empty", result.stderr)
	}
	var report runReportDoc
	if err := json.Unmarshal([]byte(result.stdout), &report); err != nil {
		t.Fatalf("JSON report is not valid: %v\n%s", err, result.stdout)
	}
	if report.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d, want 1", report.SchemaVersion)
	}
	if len(report.Cases) != 1 || report.Cases[0].CaseID != "hostile0000000a" {
		t.Fatalf("cases = %+v, want hostile0000000a", report.Cases)
	}
	return result, report, elapsed
}

func assertHostileOutcome(t *testing.T, result runResult, report runReportDoc, wantExit int, wantVerdict, wantOutcome string) {
	t.Helper()
	if result.exit != wantExit {
		t.Fatalf("Run() = %d, want %d; stdout=%q stderr=%q", result.exit, wantExit, result.stdout, result.stderr)
	}
	if report.Verdict != wantVerdict {
		t.Fatalf("verdict = %q, want %q", report.Verdict, wantVerdict)
	}
	if report.Cases[0].Outcome != wantOutcome {
		t.Fatalf("outcome = %q, want %q", report.Cases[0].Outcome, wantOutcome)
	}
}

func assertProbeFailure(t *testing.T, result runResult, report runReportDoc, stableSubstring string) {
	t.Helper()
	assertHostileOutcome(t, result, report, 3, "INCONCLUSIVE", "INCONCLUSIVE")
	caseReport := report.Cases[0]
	if !strings.Contains(caseReport.Error, "VAR-NET-0001") || (stableSubstring != "" && !strings.Contains(caseReport.Error, stableSubstring)) {
		t.Fatalf("case error = %q, want VAR-NET-0001 and %q", caseReport.Error, stableSubstring)
	}
	if len(caseReport.Differences) != 0 {
		t.Fatalf("differences = %+v, want none", caseReport.Differences)
	}
}

func assertElapsed(t *testing.T, elapsed, minimum, maximum time.Duration) {
	t.Helper()
	if elapsed < minimum || elapsed >= maximum {
		t.Fatalf("elapsed = %s, want >= %s and < %s", elapsed, minimum, maximum)
	}
}

// Source: internal/probe/probe.go Do wraps the complete request in --timeout;
// docs/adr/0007-verdict-model.md makes a timeout INCONCLUSIVE with exit code 3.
func TestHostileSlowLorisHeaders(t *testing.T) {
	old := startServer(t, ok200Handler)
	newAddr := startRawHTTPServer(t, func(ctx context.Context, conn net.Conn) {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		response := []byte("HTTP/1.1 200 OK\r\n")
		for _, value := range response {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := conn.Write([]byte{value}); err != nil {
					return
				}
			}
		}
		<-ctx.Done()
	})
	result, report, elapsed := runHostileCLI(t, "plan-hostile-http.json", old.Listener.Addr().String(), newAddr)
	assertProbeFailure(t, result, report, "read response")
	assertElapsed(t, elapsed, 900*time.Millisecond, 5*time.Second)
}

// Source: internal/probe/probe.go Do applies --timeout while reading a response;
// docs/exit-codes.md maps an unobservable case to exit code 3.
func TestHostileSilentServer(t *testing.T) {
	old := startServer(t, ok200Handler)
	newAddr := startRawHTTPServer(t, func(ctx context.Context, _ net.Conn) { <-ctx.Done() })
	result, report, elapsed := runHostileCLI(t, "plan-hostile-http.json", old.Listener.Addr().String(), newAddr)
	assertProbeFailure(t, result, report, "read response")
	assertElapsed(t, elapsed, 900*time.Millisecond, 5*time.Second)
}

// Source: internal/probe/probe.go maxInterimResponses is eight; ADR 0007 maps
// a probe read failure to INCONCLUSIVE and docs/exit-codes.md maps it to 3.
func TestHostileInterimResponses(t *testing.T) {
	const final = "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok"
	t.Run("eight tolerated", func(t *testing.T) {
		response := []byte(strings.Repeat("HTTP/1.1 100 Continue\r\n\r\n", 8) + final)
		serve := func(_ context.Context, conn net.Conn) { writeAll(conn, response) }
		oldAddr := startRawHTTPServer(t, serve)
		newAddr := startRawHTTPServer(t, serve)
		result, report, elapsed := runHostileCLI(t, "plan-hostile-http.json", oldAddr, newAddr)
		assertHostileOutcome(t, result, report, 0, "EQUIVALENT", "EQUIVALENT")
		if report.Cases[0].Error != "" || len(report.Cases[0].Differences) != 0 {
			t.Fatalf("case = %+v, want no error or differences", report.Cases[0])
		}
		assertElapsed(t, elapsed, 0, 5*time.Second)
	})
	t.Run("ninth rejected", func(t *testing.T) {
		response := []byte(strings.Repeat("HTTP/1.1 100 Continue\r\n\r\n", 9))
		old := startServer(t, ok200Handler)
		newAddr := startRawHTTPServer(t, func(_ context.Context, conn net.Conn) { writeAll(conn, response) })
		result, report, elapsed := runHostileCLI(t, "plan-hostile-http.json", old.Listener.Addr().String(), newAddr)
		assertProbeFailure(t, result, report, "too many interim responses")
		assertElapsed(t, elapsed, 0, 5*time.Second)
	})
}

// Source: internal/probe/probe.go maxHeaderBytes is 64 KiB and readHeaderBlock
// rejects a larger response head before parsing it.
func TestHostileHeaderFlood(t *testing.T) {
	response := []byte("HTTP/1.1 200 OK\r\nX-Flood: " + strings.Repeat("a", 128<<10) + "\r\n\r\n")
	old := startServer(t, ok200Handler)
	newAddr := startRawHTTPServer(t, func(_ context.Context, conn net.Conn) { writeAll(conn, response) })
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	result, report, elapsed := runHostileCLI(t, "plan-hostile-http.json", old.Listener.Addr().String(), newAddr)
	runtime.ReadMemStats(&after)
	assertProbeFailure(t, result, report, "response header exceeds 65536 bytes")
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated >= 16<<20 {
		t.Fatalf("TotalAlloc delta = %d bytes, want < %d", allocated, 16<<20)
	}
	assertElapsed(t, elapsed, 0, 5*time.Second)
}

// Source: internal/probe/probe.go wraps the probe context with the per-request
// timeout. That timeout, not the 256 MiB MaxBodyBytes cap, stops a body that
// drips one tiny chunk every 100 ms and cannot reach the cap within one second.
func TestHostileEndlessChunkedBody(t *testing.T) {
	old := startServer(t, ok200Handler)
	newAddr := startRawHTTPServer(t, func(ctx context.Context, conn net.Conn) {
		writeAll(conn, []byte("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n"))
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := io.WriteString(conn, "1\r\na\r\n"); err != nil {
					return
				}
			}
		}
	})
	result, report, elapsed := runHostileCLI(t, "plan-hostile-http.json", old.Listener.Addr().String(), newAddr)
	assertProbeFailure(t, result, report, "")
	assertElapsed(t, elapsed, 900*time.Millisecond, 3*time.Second)
}

// Source: internal/probe/probe.go capture reports a short Content-Length body
// as "invalid HTTP response body"; ADR 0007 classifies probe read failures as INCONCLUSIVE.
func TestHostileConnectionCloseMidBody(t *testing.T) {
	old := startServer(t, ok200Handler)
	newAddr := startRawHTTPServer(t, func(_ context.Context, conn net.Conn) {
		writeAll(conn, []byte("HTTP/1.1 200 OK\r\nContent-Length: 1000\r\n\r\n0123456789"))
	})
	result, report, elapsed := runHostileCLI(t, "plan-hostile-http.json", old.Listener.Addr().String(), newAddr)
	assertProbeFailure(t, result, report, "invalid HTTP response body")
	assertElapsed(t, elapsed, 0, 5*time.Second)
}

// Source: internal/probe/probe.go readResponse replaces parser details with the
// stable "invalid HTTP response" error before engine/run.go assigns VAR-NET-0001.
func TestHostileMalformedResponse(t *testing.T) {
	responses := []struct {
		name, response string
	}{
		{"malformed status line", "NOT-HTTP/1.1 200 OK\r\n\r\n"},
		// net/http parses any single digit major.minor version, so only a
		// version it cannot read at all reaches the invalid-response path.
		{"invalid HTTP version", "HTTP/1.10 200 OK\r\n\r\n"},
	}
	for _, test := range responses {
		t.Run(test.name, func(t *testing.T) {
			old := startServer(t, ok200Handler)
			newAddr := startRawHTTPServer(t, func(_ context.Context, conn net.Conn) { writeAll(conn, []byte(test.response)) })
			result, report, elapsed := runHostileCLI(t, "plan-hostile-http.json", old.Listener.Addr().String(), newAddr)
			assertProbeFailure(t, result, report, "invalid HTTP response")
			assertElapsed(t, elapsed, 0, 5*time.Second)
		})
	}
}

// Source: RFC 9112 handling in net/http makes Transfer-Encoding override and
// remove Content-Length; internal/probe/wire_test.go documents the neighbouring
// wire-level body behavior. The new side carries both framing headers, so the
// run must not hang and must hash the chunked body in full.
func TestHostileSmugglingShapedResponse(t *testing.T) {
	oldAddr := startRawHTTPServer(t, func(_ context.Context, conn net.Conn) {
		writeAll(conn, []byte("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nHELLO\r\n0\r\n\r\n"))
	})
	newAddr := startRawHTTPServer(t, func(_ context.Context, conn net.Conn) {
		writeAll(conn, []byte("HTTP/1.1 200 OK\r\nContent-Length: 5\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nWORLD\r\n0\r\n\r\n"))
	})
	result, report, elapsed := runHostileCLI(t, "plan-hostile-http.json", oldAddr, newAddr)
	assertHostileOutcome(t, result, report, 1, "DIFFERENT", "DIFFERENT")
	caseReport := report.Cases[0]
	if caseReport.Error != "" || len(caseReport.Differences) != 1 {
		t.Fatalf("case = %+v, want one body difference and no error", caseReport)
	}
	oldHash, newHash := sha256.Sum256([]byte("HELLO")), sha256.Sum256([]byte("WORLD"))
	wantOld := fmt.Sprintf("sha256=%x size=5 truncated=false", oldHash)
	wantNew := fmt.Sprintf("sha256=%x size=5 truncated=false", newHash)
	difference := caseReport.Differences[0]
	if difference.Field != "body" || difference.Old != wantOld || difference.New != wantNew {
		t.Fatalf("difference = %+v, want body old=%q new=%q", difference, wantOld, wantNew)
	}
	assertElapsed(t, elapsed, 0, 5*time.Second)
}

func testTLSCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	certificate := server.TLS.Certificates[0]
	server.Close()
	return certificate
}

func readRequestHead(conn net.Conn) error {
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		if line == "\r\n" || line == "\n" {
			return nil
		}
	}
}

func startRawTLSServer(t *testing.T, afterHandshake func(net.Conn)) string {
	t.Helper()
	certificate := testTLSCertificate(t)
	return startRawServer(t, func(ctx context.Context, conn net.Conn) {
		tlsConn := tls.Server(conn, &tls.Config{
			Certificates: []tls.Certificate{certificate},
			MinVersion:   tls.VersionTLS12,
		})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return
		}
		afterHandshake(tlsConn)
	})
}

// Source: docs/adr/0003-probe-safety.md defines probe TLS behavior; ADR 0007
// classifies TLS and response-read failures as INCONCLUSIVE. Both sides point
// at the one TLS listener: the first failed probe ends the case, so the
// listener serves a single connection.
func TestHostileTLS(t *testing.T) {
	t.Run("garbage after handshake", func(t *testing.T) {
		addr := startRawTLSServer(t, func(conn net.Conn) {
			if readRequestHead(conn) == nil {
				writeAll(conn, []byte("this is not an HTTP response"))
			}
		})
		result, report, elapsed := runHostileCLI(t, "plan-hostile-https.json", addr, addr)
		assertProbeFailure(t, result, report, "read response")
		assertElapsed(t, elapsed, 0, 5*time.Second)
	})
	t.Run("plain HTTP to TLS port", func(t *testing.T) {
		addr := startRawTLSServer(t, func(net.Conn) {})
		result, report, elapsed := runHostileCLI(t, "plan-hostile-http.json", addr, addr)
		assertProbeFailure(t, result, report, "read response")
		assertElapsed(t, elapsed, 0, 5*time.Second)
	})
}

// Source: CONVENTIONS.md says requests go only to user-named endpoints, and
// docs/adr/0003-probe-safety.md says redirect responses are never followed.
func TestHostileRedirectToAnotherHost(t *testing.T) {
	var redirectedConnections atomic.Int64
	targetAddr := startRawHTTPServer(t, func(_ context.Context, _ net.Conn) { redirectedConnections.Add(1) })
	location := "http://" + targetAddr + "/elsewhere"
	old := startServer(t, ok200Handler)
	newAddr := startRawHTTPServer(t, func(_ context.Context, conn net.Conn) {
		writeAll(conn, []byte("HTTP/1.1 302 Found\r\nLocation: "+location+"\r\nContent-Length: 0\r\n\r\n"))
	})
	result, report, elapsed := runHostileCLI(t, "plan-hostile-http.json", old.Listener.Addr().String(), newAddr)
	assertHostileOutcome(t, result, report, 1, "DIFFERENT", "DIFFERENT")
	caseReport := report.Cases[0]
	if caseReport.Error != "" || len(caseReport.Differences) != 2 {
		t.Fatalf("case = %+v, want two differences and no error", caseReport)
	}
	locationDifference, statusDifference := caseReport.Differences[0], caseReport.Differences[1]
	if locationDifference.Field != "header.location" || locationDifference.Old != "(absent)" || locationDifference.New != location {
		t.Fatalf("location difference = %+v", locationDifference)
	}
	if statusDifference.Field != "status" || statusDifference.Old != "200" || statusDifference.New != "302" {
		t.Fatalf("status difference = %+v", statusDifference)
	}
	if got := redirectedConnections.Load(); got != 0 {
		t.Fatalf("redirect target connections = %d, want 0", got)
	}
	assertElapsed(t, elapsed, 0, 5*time.Second)
}
