package e2e

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// escapeSequence is the ANSI sequence a hostile server would try to print.
const escapeSequence = "\x1b[31m"

// hostileHeaderHandler answers with header values that carry an ANSI escape.
// Go's HTTP client refuses such a response, so the case ends inconclusive;
// the report must still print no escape byte.
var hostileHeaderHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain"+escapeSequence)
	w.Header().Set("X-Frame-Options", "DENY"+escapeSequence)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("app\n"))
})

// tabHeaderHandler answers with a compared header value that carries a tab.
// A tab is a legal header value byte, so it reaches the report and would
// shift the table columns if the table printed it raw.
var tabHeaderHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("X-Frame-Options", "DENY\tINJECTED")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("app\n"))
})

// assertNoEscape fails when an output stream carries a raw escape byte.
func assertNoEscape(t *testing.T, label, output string) {
	t.Helper()
	if strings.ContainsRune(output, 0x1b) {
		t.Fatalf("%s carries a raw escape byte: %q", label, output)
	}
}

// runEscapeCase runs one escape scenario in the given format and returns the
// result of the run.
func runEscapeCase(t *testing.T, planPath, oldAddr, newAddr, format string) runResult {
	t.Helper()
	result := runCLI(t, []string{
		"run",
		"--plan", planPath,
		// Endpoint syntax per docs/adr/0004-endpoint-syntax.md: a numeric IP
		// with an explicit port, no DNS.
		"--old", oldAddr,
		"--new", newAddr,
		"--rate", "10000",
		"--concurrency", "1",
		"--quiet",
		"--format", format,
	})
	return result
}

// Source: docs/adr/0006-redaction.md: the report layer serializes what a
// remote server sent, so it must not print a control byte a server chose. A
// hostile header value carries an ANSI escape; the run reports the refused
// response and no escape byte reaches stdout or stderr.
func TestRunTableEscapesHostileHeader(t *testing.T) {
	old := startServer(t, hostileHeaderHandler)
	candidate := startServer(t, hostileHeaderHandler)
	oldAddr, newAddr := old.Listener.Addr().String(), candidate.Listener.Addr().String()

	table := runEscapeCase(t, testDataPath("plan-header.json"), oldAddr, newAddr, "table")
	assertNoEscape(t, "table stdout", table.stdout)
	assertNoEscape(t, "table stderr", table.stderr)
	if !strings.Contains(table.stdout, "VAR-NET-0001") {
		t.Fatalf("table report does not report the refused response:\n%s", table.stdout)
	}
	golden(t, "escape-header.table.golden", normalizeOutput(table.stdout, oldAddr, newAddr))

	document := runEscapeCase(t, testDataPath("plan-header.json"), oldAddr, newAddr, "json")
	assertNoEscape(t, "json stdout", document.stdout)
	assertNoEscape(t, "json stderr", document.stderr)
	var parsed runReportDoc
	if err := json.Unmarshal([]byte(document.stdout), &parsed); err != nil {
		t.Fatalf("JSON report is not valid: %v\n%s", err, document.stdout)
	}
	if parsed.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d, want 1", parsed.SchemaVersion)
	}
	golden(t, "escape-header.json.golden", normalizeOutput(document.stdout, oldAddr, newAddr))
}

// Source: docs/adr/0006-redaction.md: a tab is a legal header value byte and
// reaches the report, so the table must show it as an escape instead of
// letting it shift the columns. The JSON report escapes it the way
// encoding/json does.
func TestRunTableEscapesTabInHeader(t *testing.T) {
	old := startServer(t, ok200Handler)
	candidate := startServer(t, tabHeaderHandler)
	oldAddr, newAddr := old.Listener.Addr().String(), candidate.Listener.Addr().String()

	table := runEscapeCase(t, testDataPath("plan-header.json"), oldAddr, newAddr, "table")
	assertNoEscape(t, "table stdout", table.stdout)
	if !strings.Contains(table.stdout, `DENY\x09INJECTED`) {
		t.Fatalf("table report does not show the escaped tab:\n%s", table.stdout)
	}
	golden(t, "escape-tab.table.golden", normalizeOutput(table.stdout, oldAddr, newAddr))

	document := runEscapeCase(t, testDataPath("plan-header.json"), oldAddr, newAddr, "json")
	var parsed runReportDoc
	if err := json.Unmarshal([]byte(document.stdout), &parsed); err != nil {
		t.Fatalf("JSON report is not valid: %v\n%s", err, document.stdout)
	}
	if !strings.Contains(document.stdout, `DENY\tINJECTED`) {
		t.Fatalf("JSON report does not escape the tab:\n%s", document.stdout)
	}
	golden(t, "escape-tab.json.golden", normalizeOutput(document.stdout, oldAddr, newAddr))
}

// selfSignedCertificate builds a certificate whose common name carries the
// given text. A server presents it to the prober, which captures the subject
// and the issuer as compared fields. The validity dates are fixed so the
// certificate does not depend on the clock (CONVENTIONS.md: inject a clock).
func selfSignedCertificate(t *testing.T, commonName string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:     time.Date(2036, time.January, 1, 0, 0, 0, 0, time.UTC),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// startTLSServer listens on 127.0.0.1:<random port> with a certificate whose
// common name is the given text, and is closed by t.Cleanup.
func startTLSServer(t *testing.T, commonName string) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("app\n"))
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{selfSignedCertificate(t, commonName)}}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

// httpsPlan writes a plan document with one https case and returns its path.
// The certificate fingerprint differs per run, so the certificate scenario
// asserts the output instead of comparing a golden.
func httpsPlan(t *testing.T) string {
	t.Helper()
	document := map[string]any{
		"schema_version": 1,
		"profile":        "nginx",
		"cases": []map[string]any{{
			"id":     "eeeeeeeeeeee",
			"host":   "app.example.com",
			"scheme": "https",
			"method": "GET",
			"path":   "/",
			"family": "base",
			"source": map[string]any{"ingress": "e2e/spec", "rule": 0, "path": 0},
		}},
		"unsupported": []any{},
		"warnings":    []any{},
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "plan-escape.json")
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// Source: docs/adr/0006-redaction.md: the certificate subject and the issuer
// come from the server, so a server that presents a certificate whose common
// name carries an ANSI escape must not put that escape into the terminal. The
// JSON report escapes the same value the way encoding/json does.
func TestRunTableEscapesHostileCertificate(t *testing.T) {
	old := startTLSServer(t, "old"+escapeSequence)
	candidate := startTLSServer(t, "new"+escapeSequence)
	oldAddr, newAddr := old.Listener.Addr().String(), candidate.Listener.Addr().String()
	planPath := httpsPlan(t)

	table := runEscapeCase(t, planPath, oldAddr, newAddr, "table")
	assertNoEscape(t, "table stdout", table.stdout)
	assertNoEscape(t, "table stderr", table.stderr)
	if !strings.Contains(table.stdout, `\x1b[31m`) {
		t.Fatalf("table report does not show the escaped certificate subject:\n%s", table.stdout)
	}

	document := runEscapeCase(t, planPath, oldAddr, newAddr, "json")
	assertNoEscape(t, "json stdout", document.stdout)
	assertNoEscape(t, "json stderr", document.stderr)
	var parsed runReportDoc
	if err := json.Unmarshal([]byte(document.stdout), &parsed); err != nil {
		t.Fatalf("JSON report is not valid: %v\n%s", err, document.stdout)
	}
	if !strings.Contains(document.stdout, `CN=old\u001b[31m`) {
		t.Fatalf("JSON report does not escape the certificate subject:\n%s", document.stdout)
	}
}
