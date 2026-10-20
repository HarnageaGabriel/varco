package probe

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/result"
)

// The tests in this file cover the TLS handling of the prober, the security
// sensitive part of the tool (CONVENTIONS.md, review protocol). Every
// certificate is generated inside the test with crypto/x509 and crypto/ecdsa:
// no private key is written to disk and none is committed.
//
// Sources cited below:
//   - docs/adr/0003-probe-safety.md: TLS verification is disabled for probes
//     so certificate details can be captured; certificate trust is not
//     reported as pass or fail.
//   - CONVENTIONS.md: "the captured certificate fields are the output, never a
//     pass/fail on trust".
//   - internal/probe/probe.go: the TLS client, the SNI rules and the capture.

const (
	// probeErrorLimit bounds the error a failed probe may report. The longest
	// message the TLS stack produces for a handshake failure is a socket
	// error line, so anything beyond this limit is a leak.
	probeErrorLimit = 512
	// certificateMessageLimit is the size limit of a TLS certificate message
	// in crypto/tls (common.go: maxHandshakeCertificateMsg = 262144). It is
	// the only bound that applies to the captured certificate: the prober
	// itself applies none (internal/probe/probe.go, capture).
	certificateMessageLimit = 256 * 1024
)

// longHexBytes and longBase64Bytes match a run of hex or base64 characters,
// the shape a dumped certificate or key would take in an error message.
var (
	longHexBytes    = regexp.MustCompile(`[0-9a-fA-F]{40,}`)
	longBase64Bytes = regexp.MustCompile(`[A-Za-z0-9+/]{40,}={0,2}`)
)

// tlsCertificateSpec describes the certificate a test server presents.
type tlsCertificateSpec struct {
	commonName string
	dnsNames   []string
	notBefore  time.Time
	notAfter   time.Time
	serial     int64
	issuer     *x509.Certificate
	issuerKey  *ecdsa.PrivateKey
}

// newTLSCertificate builds a certificate and its key at test time and returns
// the parsed leaf, so a test can compare the captured fields with the
// certificate the server actually sent.
func newTLSCertificate(t *testing.T, spec tlsCertificateSpec) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(spec.serial),
		Subject:               pkix.Name{CommonName: spec.commonName, Organization: []string{"varco probe test"}},
		NotBefore:             spec.notBefore,
		NotAfter:              spec.notAfter,
		DNSNames:              spec.dnsNames,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	parent, signer := template, key
	if spec.issuer != nil {
		parent, signer = spec.issuer, spec.issuerKey
	}
	der, err := x509.CreateCertificate(rand.Reader, template, parent, &key.PublicKey, signer)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, leaf
}

// newTLSCertificateAuthority builds the untrusted issuer of the chained
// certificate case and returns its DER, the parsed certificate and its key.
func newTLSCertificateAuthority(t *testing.T, commonName string, notBefore, notAfter time.Time) ([]byte, *x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create authority: %v", err)
	}
	authority, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse authority: %v", err)
	}
	return der, authority, key
}

// clientHello is the part of a ClientHello a test server records.
type clientHello struct {
	serverName string
	protocols  []string
}

// helloRecorder records the ClientHello of every connection a test server
// receives.
type helloRecorder struct {
	mu     sync.Mutex
	hellos []clientHello
}

func (r *helloRecorder) record(hello *tls.ClientHelloInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hellos = append(r.hellos, clientHello{serverName: hello.ServerName, protocols: append([]string(nil), hello.SupportedProtos...)})
}

func (r *helloRecorder) snapshot() []clientHello {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]clientHello(nil), r.hellos...)
}

// tlsServerSpec describes the TLS server a test starts.
type tlsServerSpec struct {
	certificates []tls.Certificate
	minVersion   uint16
	maxVersion   uint16
	nextProtos   []string
}

// startProbeTLSServer starts a TLS server that records every ClientHello and
// answers with a fixed body. The server is closed in t.Cleanup.
func startProbeTLSServer(t *testing.T, spec tlsServerSpec) (*httptest.Server, *helloRecorder) {
	t.Helper()
	recorder := &helloRecorder{}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	server.TLS = &tls.Config{
		Certificates: spec.certificates,
		MinVersion:   spec.minVersion,
		MaxVersion:   spec.maxVersion,
		NextProtos:   spec.nextProtos,
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			recorder.record(hello)
			return nil, nil
		},
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server, recorder
}

// probeRequest builds a probe request aimed at a local test server.
func probeRequest(t *testing.T, address string) Request {
	t.Helper()
	return Request{
		Endpoint: endpointOf(t, address),
		Scheme:   "https",
		Host:     "probe.example.com",
		SNI:      "sni.example.com",
		Method:   "GET",
		Path:     "/healthz",
		Timeout:  5 * time.Second,
	}
}

// probeContext returns the context of one test with a hard deadline, so a
// hanging server cannot stall the suite.
func probeContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// rawFailureServer starts a TCP listener that answers a TLS client with the
// given behaviour and returns its address. The listener is closed in
// t.Cleanup.
func rawFailureServer(t *testing.T, handle func(net.Conn)) string {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handle(conn)
		}
	}()
	return listener.Addr().String()
}

// acceptCounter counts the connections a listener accepts, so a rejected
// request can be proven not to have opened one.
type acceptCounter struct {
	accepted atomic.Int32
}

// newAcceptCounter starts a listener that counts the connections it accepts
// and closes every connection. The listener is closed in t.Cleanup.
func newAcceptCounter(ctx context.Context, t *testing.T) (*acceptCounter, string) {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	counter := &acceptCounter{}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			counter.accepted.Add(1)
			_ = conn.Close()
		}
	}()
	t.Cleanup(func() { _ = listener.Close() })
	return counter, listener.Addr().String()
}

// assertNoTrustVerdict fails when a captured value could carry a pass or fail
// on certificate trust. ADR 0003 requires the captured fields to be the
// output, never a verdict.
func assertNoTrustVerdict(t *testing.T, value any) {
	t.Helper()
	typ := reflect.TypeOf(value)
	for i := 0; i < typ.NumField(); i++ {
		name := strings.ToLower(typ.Field(i).Name)
		for _, forbidden := range []string{"trust", "verif", "valid", "pass", "fail"} {
			if strings.Contains(name, forbidden) {
				t.Fatalf("%s.%s can report a trust verdict", typ, typ.Field(i).Name)
			}
		}
	}
}

// assertBoundedRedactedError fails when a probe error is unbounded or carries
// certificate bytes or request bytes.
func assertBoundedRedactedError(t *testing.T, err error, forbidden []string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	message := err.Error()
	if len(message) > probeErrorLimit {
		t.Fatalf("the error message is %d bytes, want at most %d: %q", len(message), probeErrorLimit, message)
	}
	for _, secret := range forbidden {
		if secret != "" && strings.Contains(message, secret) {
			t.Fatalf("the error message leaks %q: %q", secret, message)
		}
	}
	if strings.Contains(message, "-----BEGIN") {
		t.Fatalf("the error message carries a PEM block: %q", message)
	}
	if longHexBytes.MatchString(message) || longBase64Bytes.MatchString(message) {
		t.Fatalf("the error message carries encoded bytes: %q", message)
	}
}

// Source: docs/adr/0003-probe-safety.md ("TLS certificate verification is
// disabled only for probes so certificate details can be captured. Certificate
// trust is not reported as pass or fail.") and CONVENTIONS.md ("the captured
// certificate fields are the output, never a pass/fail on trust"). An expired
// certificate, a certificate for another name, a self-signed certificate and
// an untrusted chain are all probed without failure, and the captured fields
// are the certificate's own fields.
func TestDoCapturesCertificateWithoutTrustVerdict(t *testing.T) {
	ctx := probeContext(t)
	assertNoTrustVerdict(t, result.Result{})
	assertNoTrustVerdict(t, result.Certificate{})

	notBefore := time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)
	notAfter := time.Date(2031, time.January, 1, 0, 0, 0, 0, time.UTC)
	expiredAfter := time.Date(2021, time.January, 1, 0, 0, 0, 0, time.UTC)
	const serial = 42

	expired, expiredLeaf := newTLSCertificate(t, tlsCertificateSpec{
		commonName: "expired.example.com",
		dnsNames:   []string{"expired.example.com"},
		notBefore:  notBefore,
		notAfter:   expiredAfter,
		serial:     serial,
	})
	otherName, otherNameLeaf := newTLSCertificate(t, tlsCertificateSpec{
		commonName: "other.example.com",
		dnsNames:   []string{"other.example.com"},
		notBefore:  notBefore,
		notAfter:   notAfter,
		serial:     serial,
	})
	selfSigned, selfSignedLeaf := newTLSCertificate(t, tlsCertificateSpec{
		commonName: "self.example.com",
		dnsNames:   []string{"self.example.com"},
		notBefore:  notBefore,
		notAfter:   notAfter,
		serial:     serial,
	})
	authorityDER, authority, authorityKey := newTLSCertificateAuthority(t, "varco untrusted authority", notBefore, notAfter)
	chained, chainedLeaf := newTLSCertificate(t, tlsCertificateSpec{
		commonName: "chained.example.com",
		dnsNames:   []string{"chained.example.com"},
		notBefore:  notBefore,
		notAfter:   notAfter,
		serial:     serial,
		issuer:     authority,
		issuerKey:  authorityKey,
	})
	chained.Certificate = append(chained.Certificate, authorityDER)

	//nolint:forbidigo // the wall clock decides whether a certificate is expired at probe time.
	if !expiredLeaf.NotAfter.Before(time.Now()) {
		t.Fatalf("the expired case is not expired: NotAfter=%s", expiredLeaf.NotAfter)
	}

	cases := []struct {
		name        string
		certificate tls.Certificate
		leaf        *x509.Certificate
		sni         string
	}{
		{name: "expired certificate", certificate: expired, leaf: expiredLeaf, sni: "expired.example.com"},
		{name: "certificate for another name", certificate: otherName, leaf: otherNameLeaf, sni: "probe.example.com"},
		{name: "self-signed certificate", certificate: selfSigned, leaf: selfSignedLeaf, sni: "self.example.com"},
		{name: "untrusted chain", certificate: chained, leaf: chainedLeaf, sni: "chained.example.com"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server, _ := startProbeTLSServer(t, tlsServerSpec{certificates: []tls.Certificate{test.certificate}})
			request := probeRequest(t, server.Listener.Addr().String())
			request.SNI = test.sni
			got, err := Do(ctx, request)
			if err != nil {
				t.Fatalf("the probe failed: %v", err)
			}
			if got.StatusCode != 200 || got.Certificate == nil {
				t.Fatalf("result=%+v", got)
			}
			certificate := got.Certificate
			leaf := test.leaf
			if certificate.Subject != leaf.Subject.String() {
				t.Fatalf("subject = %q, want %q", certificate.Subject, leaf.Subject.String())
			}
			if certificate.Issuer != leaf.Issuer.String() {
				t.Fatalf("issuer = %q, want %q", certificate.Issuer, leaf.Issuer.String())
			}
			if certificate.Serial != "2a" {
				t.Fatalf("serial = %q, want %q", certificate.Serial, "2a")
			}
			if !certificate.NotBefore.Equal(leaf.NotBefore) || !certificate.NotAfter.Equal(leaf.NotAfter) {
				t.Fatalf("validity = %s..%s, want %s..%s", certificate.NotBefore, certificate.NotAfter, leaf.NotBefore, leaf.NotAfter)
			}
			fingerprint := sha256.Sum256(leaf.Raw)
			if certificate.Fingerprint != hex.EncodeToString(fingerprint[:]) {
				t.Fatalf("fingerprint = %q, want %q", certificate.Fingerprint, hex.EncodeToString(fingerprint[:]))
			}
			if len(certificate.Fingerprint) != 64 {
				t.Fatalf("fingerprint = %q", certificate.Fingerprint)
			}
			want := slices.Clone(leaf.DNSNames)
			slices.Sort(want)
			if !slices.Equal(certificate.DNSNames, want) {
				t.Fatalf("dns sans = %q, want %q", certificate.DNSNames, want)
			}
		})
	}
}

// Source: internal/probe/probe.go sets MinVersion: tls.VersionTLS10 on the
// probe client, so a server that offers only TLS 1.0 or TLS 1.1 is probed and
// not rejected. The crypto/tls docs state that an explicit MinVersion below
// TLS 1.2 is honoured on the server side as well (the tls10server GODEBUG
// only changes the default), which the Go 1.26 toolchain of this repository
// still does: both legacy servers are probed. Should a future toolchain
// refuse to serve TLS 1.0, these two cases have to be skipped with that
// reason, not asserted as a rejection. The captured version is the negotiated
// one.
func TestDoReportsNegotiatedTLSVersion(t *testing.T) {
	ctx := probeContext(t)
	certificate, _ := newTLSCertificate(t, tlsCertificateSpec{
		commonName: "version.example.com",
		dnsNames:   []string{"version.example.com"},
		notBefore:  time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC),
		notAfter:   time.Date(2031, time.January, 1, 0, 0, 0, 0, time.UTC),
		serial:     42,
	})
	cases := []struct {
		name    string
		min     uint16
		max     uint16
		version string
	}{
		{name: "TLS 1.0 only", min: tls.VersionTLS10, max: tls.VersionTLS10, version: "TLS 1.0"},
		{name: "TLS 1.1 only", min: tls.VersionTLS11, max: tls.VersionTLS11, version: "TLS 1.1"},
		{name: "TLS 1.2 only", min: tls.VersionTLS12, max: tls.VersionTLS12, version: "TLS 1.2"},
		{name: "TLS 1.3 only", min: tls.VersionTLS13, max: tls.VersionTLS13, version: "TLS 1.3"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server, _ := startProbeTLSServer(t, tlsServerSpec{
				certificates: []tls.Certificate{certificate},
				minVersion:   test.min,
				maxVersion:   test.max,
			})
			got, err := Do(ctx, probeRequest(t, server.Listener.Addr().String()))
			if err != nil {
				t.Fatalf("the probe failed: %v", err)
			}
			if got.TLSVersion != test.version {
				t.Fatalf("TLSVersion = %q, want %q", got.TLSVersion, test.version)
			}
			if got.Certificate == nil || got.CipherSuite == "" {
				t.Fatalf("result=%+v", got)
			}
		})
	}
}

// Source: internal/probe/probe.go sends the planned SNI as the TLS server
// name and validates it with validSNIOnWire, which is more permissive than
// ValidSNI so that a host with an underscore stays probeable. The crypto/tls
// client never puts an IP literal in the server name extension and sends no
// extension at all for an empty name.
func TestDoSNIOnWire(t *testing.T) {
	ctx := probeContext(t)
	certificate, _ := newTLSCertificate(t, tlsCertificateSpec{
		commonName: "sni.example.com",
		dnsNames:   []string{"sni.example.com"},
		notBefore:  time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC),
		notAfter:   time.Date(2031, time.January, 1, 0, 0, 0, 0, time.UTC),
		serial:     42,
	})
	cases := []struct {
		name string
		sni  string
		want string
	}{
		{name: "planned SNI", sni: "sni.example.com", want: "sni.example.com"},
		{name: "underscore label", sni: "my_app.example.com", want: "my_app.example.com"},
		{name: "trailing dot", sni: "sni.example.com.", want: "sni.example.com"},
		{name: "empty SNI", sni: "", want: ""},
		{name: "IPv4 literal", sni: "127.0.0.1", want: ""},
		{name: "IPv6 literal", sni: "::1", want: ""},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server, recorder := startProbeTLSServer(t, tlsServerSpec{certificates: []tls.Certificate{certificate}})
			request := probeRequest(t, server.Listener.Addr().String())
			request.SNI = test.sni
			if _, err := Do(ctx, request); err != nil {
				t.Fatalf("the probe failed: %v", err)
			}
			hellos := recorder.snapshot()
			if len(hellos) != 1 {
				t.Fatalf("client hellos = %d, want 1", len(hellos))
			}
			if hellos[0].serverName != test.want {
				t.Fatalf("the server saw the server name %q, want %q", hellos[0].serverName, test.want)
			}
		})
	}
}

// Source: internal/probe/probe.go rejects an unsafe SNI before the connection
// is opened and never echoes the rejected value. The counter on the listener
// proves that no connection was made.
func TestDoRejectsUnsafeSNIBeforeConnecting(t *testing.T) {
	ctx := probeContext(t)
	cases := []struct {
		name string
		sni  string
	}{
		{name: "space", sni: "api example.com"},
		{name: "NUL byte", sni: "api\x00.example.com"},
		{name: "colon", sni: "api.example.com:443"},
		{name: "slash", sni: "api.example.com/path"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			counter, address := newAcceptCounter(ctx, t)
			request := probeRequest(t, address)
			request.SNI = test.sni
			_, err := Do(ctx, request)
			var diagnostic *diag.Error
			if !errors.As(err, &diagnostic) || diagnostic.Diagnostic.Code != diag.CodeInvalidFlag {
				t.Fatalf("err = %v", err)
			}
			if strings.Contains(err.Error(), test.sni) {
				t.Fatalf("the error echoes the SNI: %v", err)
			}
			if counter.accepted.Load() != 0 {
				t.Fatalf("the rejected SNI opened %d connections", counter.accepted.Load())
			}
		})
	}
}

// Source: internal/probe/probe.go offers exactly http/1.1 in ALPN and
// captures the negotiated protocol of the connection.
func TestDoOffersOnlyHTTP11OverALPN(t *testing.T) {
	ctx := probeContext(t)
	certificate, _ := newTLSCertificate(t, tlsCertificateSpec{
		commonName: "alpn.example.com",
		dnsNames:   []string{"alpn.example.com"},
		notBefore:  time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC),
		notAfter:   time.Date(2031, time.January, 1, 0, 0, 0, 0, time.UTC),
		serial:     42,
	})
	server, recorder := startProbeTLSServer(t, tlsServerSpec{
		certificates: []tls.Certificate{certificate},
		nextProtos:   []string{"http/1.1"},
	})
	got, err := Do(ctx, probeRequest(t, server.Listener.Addr().String()))
	if err != nil {
		t.Fatalf("the probe failed: %v", err)
	}
	hellos := recorder.snapshot()
	if len(hellos) != 1 {
		t.Fatalf("client hellos = %d, want 1", len(hellos))
	}
	if len(hellos[0].protocols) != 1 || hellos[0].protocols[0] != "http/1.1" {
		t.Fatalf("the server saw the offered protocols %q, want [http/1.1]", hellos[0].protocols)
	}
	if got.ALPN != "http/1.1" {
		t.Fatalf("ALPN = %q, want http/1.1", got.ALPN)
	}
}

// Source: internal/probe/probe.go reports a failed TLS handshake as a bounded
// probe error that names the stage and carries no verdict. The error must not
// carry the certificate bytes the server sent or the request bytes the prober
// never wrote.
func TestDoReportsHandshakeFailures(t *testing.T) {
	ctx := probeContext(t)
	const (
		hostCanary = "canary-host.example"
		pathCanary = "/canary-path-9f2b"
		sniCanary  = "canary-sni.example"
		certCanary = "canary-cert.example"
	)
	certificate, _ := newTLSCertificate(t, tlsCertificateSpec{
		commonName: certCanary,
		dnsNames:   []string{certCanary},
		notBefore:  time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC),
		notAfter:   time.Date(2031, time.January, 1, 0, 0, 0, 0, time.UTC),
		serial:     42,
	})
	corrupted := make([]byte, len(certificate.Certificate[0]))
	copy(corrupted, certificate.Certificate[0])
	corrupted[len(corrupted)/2] ^= 0xff
	corruptedCertificate := tls.Certificate{Certificate: [][]byte{corrupted}, PrivateKey: certificate.PrivateKey}

	// alertRecord is a TLS alert record: handshake (0x15), TLS 1.2 record
	// version (0x0303), length 2, fatal level (0x02), handshake_failure
	// (0x28).
	alertRecord := []byte{0x15, 0x03, 0x03, 0x00, 0x02, 0x02, 0x28}

	cases := []struct {
		name  string
		serve func(t *testing.T) string
	}{
		{
			name: "server closes during the handshake",
			serve: func(t *testing.T) string {
				return rawFailureServer(t, func(conn net.Conn) { _ = conn.Close() })
			},
		},
		{
			name: "server sends an alert",
			serve: func(t *testing.T) string {
				return rawFailureServer(t, func(conn net.Conn) {
					defer func() { _ = conn.Close() }()
					reader := bufio.NewReader(conn)
					_, _ = reader.Peek(1)
					_, _ = conn.Write(alertRecord)
				})
			},
		},
		{
			name: "non-TLS server on the port",
			serve: func(t *testing.T) string {
				return rawFailureServer(t, func(conn net.Conn) {
					defer func() { _ = conn.Close() }()
					reader := bufio.NewReader(conn)
					_, _ = reader.Read(make([]byte, 1))
					_, _ = conn.Write([]byte("HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n"))
				})
			},
		},
		{
			name: "server presents a corrupted certificate",
			serve: func(t *testing.T) string {
				return rawFailureServer(t, func(conn net.Conn) {
					defer func() { _ = conn.Close() }()
					server := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{corruptedCertificate}, MinVersion: tls.VersionTLS12})
					_ = server.HandshakeContext(context.Background())
				})
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			address := test.serve(t)
			request := probeRequest(t, address)
			request.Host = hostCanary
			request.Path = pathCanary
			request.SNI = sniCanary
			_, err := Do(ctx, request)
			if err == nil {
				t.Fatal("expected a handshake failure")
			}
			if !strings.Contains(err.Error(), "tls handshake") {
				t.Fatalf("err = %v", err)
			}
			var diagnostic *diag.Error
			if errors.As(err, &diagnostic) {
				t.Fatalf("the probe attached the diagnostic %s to a handshake failure", diagnostic.Diagnostic.Code)
			}
			assertBoundedRedactedError(t, err, []string{
				hostCanary,
				pathCanary,
				sniCanary,
				certCanary,
				hex.EncodeToString(certificate.Certificate[0]),
				base64.StdEncoding.EncodeToString(certificate.Certificate[0]),
				hex.EncodeToString(corrupted),
				base64.StdEncoding.EncodeToString(corrupted),
			})
		})
	}
}

// Source: internal/probe/probe.go maps a probe error that carries no
// diagnostic to VAR-NET-0001 (internal/engine/run.go) and CONVENTIONS.md
// requires every VAR- code to have a docs page under docs/diagnostics/.
func TestHandshakeFailureDiagnosticCodeIsDocumented(t *testing.T) {
	entry, ok := diag.Lookup(diag.CodeEndpointUnreachable)
	if !ok {
		t.Fatalf("%s is not registered", diag.CodeEndpointUnreachable)
	}
	if entry.Severity != diag.SeverityError {
		t.Fatalf("severity = %q, want %q", entry.Severity, diag.SeverityError)
	}
	page, err := os.ReadFile(filepath.Join("..", "..", "docs", "diagnostics", string(diag.CodeEndpointUnreachable)+".md"))
	if err != nil {
		t.Fatalf("read the docs page: %v", err)
	}
	if !strings.Contains(string(page), string(diag.CodeEndpointUnreachable)) {
		t.Fatalf("the docs page does not mention %s", diag.CodeEndpointUnreachable)
	}
}

// Source: internal/probe/probe.go copies the subject and every SAN of the leaf
// into the result. A CommonName with control bytes and a long SAN list are
// captured as they are, without a panic and without a bound of the prober's
// own: the only limit is the TLS certificate message size of crypto/tls
// (common.go: maxHandshakeCertificateMsg = 262144). The report layer escapes
// the control bytes (internal/report/escape.go).
func TestDoCapturesHostileCertificateFields(t *testing.T) {
	ctx := probeContext(t)
	commonName := "hostile\nname\t\x00end"
	names := make([]string, 0, 256)
	for i := 0; i < 256; i++ {
		names = append(names, fmt.Sprintf("host-%03d.example.com", i))
	}
	certificate, leaf := newTLSCertificate(t, tlsCertificateSpec{
		commonName: commonName,
		dnsNames:   names,
		notBefore:  time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC),
		notAfter:   time.Date(2031, time.January, 1, 0, 0, 0, 0, time.UTC),
		serial:     42,
	})
	if len(certificate.Certificate[0]) > certificateMessageLimit {
		t.Fatalf("the test certificate is %d bytes, more than the TLS certificate message limit", len(certificate.Certificate[0]))
	}
	server, _ := startProbeTLSServer(t, tlsServerSpec{certificates: []tls.Certificate{certificate}})
	got, err := Do(ctx, probeRequest(t, server.Listener.Addr().String()))
	if err != nil {
		t.Fatalf("the probe failed: %v", err)
	}
	if got.Certificate == nil {
		t.Fatalf("result=%+v", got)
	}
	if got.Certificate.Subject != leaf.Subject.String() {
		t.Fatalf("subject = %q, want %q", got.Certificate.Subject, leaf.Subject.String())
	}
	for _, control := range []string{"\n", "\t", "\x00"} {
		if !strings.Contains(got.Certificate.Subject, control) {
			t.Fatalf("the captured subject drops the control byte %q: %q", control, got.Certificate.Subject)
		}
	}
	want := slices.Clone(leaf.DNSNames)
	slices.Sort(want)
	if !slices.Equal(got.Certificate.DNSNames, want) {
		t.Fatalf("dns sans = %d entries, want %d", len(got.Certificate.DNSNames), len(want))
	}
	if len(got.Certificate.DNSNames) != len(names) {
		t.Fatalf("dns sans = %d entries, want %d", len(got.Certificate.DNSNames), len(names))
	}
	captured := len(got.Certificate.Subject)
	for _, name := range got.Certificate.DNSNames {
		captured += len(name)
	}
	if captured > certificateMessageLimit {
		t.Fatalf("the captured certificate fields are %d bytes, more than the TLS certificate message limit", captured)
	}
}
