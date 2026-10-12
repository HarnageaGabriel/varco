// Package probe sends constrained HTTP probes directly to numeric endpoints.
package probe

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/endpoint"
	"github.com/HarnageaGabriel/varco/internal/result"
)

const maxBodyBytes int64 = 8 << 20

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

// Do sends one HTTP/1.1 request without DNS or redirect following.
func Do(ctx context.Context, request Request) (result.Result, error) {
	method := strings.ToUpper(request.Method)
	if !safeMethod(method) && !request.AllowUnsafeMethod {
		return result.Result{}, diagnosticError(diag.CodeUnsupportedMethod, fmt.Sprintf("method %s requires --allow-unsafe-methods", method), nil)
	}
	if request.Scheme != "http" && request.Scheme != "https" {
		return result.Result{}, fmt.Errorf("unsupported scheme %q", request.Scheme)
	}
	for _, header := range request.Headers {
		if credentialHeader(header.Name) {
			return result.Result{}, diagnosticError(diag.CodeInvalidFlag, "credential request headers are not allowed", nil)
		}
	}

	tlsConfig := &tls.Config{ //nolint:gosec // probes capture certificates; they do not assert trust.
		InsecureSkipVerify: true,
		ServerName:         request.SNI,
		NextProtos:         []string{"http/1.1"},
	}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			dialer := net.Dialer{Timeout: request.Timeout}
			return dialer.DialContext(ctx, network, request.Endpoint.String())
		},
		TLSClientConfig:   tlsConfig,
		ForceAttemptHTTP2: false,
		TLSNextProto:      make(map[string]func(string, *tls.Conn) http.RoundTripper),
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: request.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	u := &url.URL{Scheme: request.Scheme, Host: request.Host, Path: request.Path}
	httpRequest, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return result.Result{}, fmt.Errorf("create request: %w", err)
	}
	httpRequest.Host = request.Host
	for _, header := range request.Headers {
		for _, value := range header.Values {
			httpRequest.Header.Add(header.Name, value)
		}
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return result.Result{}, fmt.Errorf("probe %s: %w", request.Endpoint.String(), err)
	}
	defer response.Body.Close()
	return capture(response)
}

func capture(response *http.Response) (result.Result, error) {
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes+1))
	if err != nil {
		return result.Result{}, fmt.Errorf("read response body: %w", err)
	}
	truncated := int64(len(body)) > maxBodyBytes
	if truncated {
		body = body[:maxBodyBytes]
	}
	digest := sha256.Sum256(body)
	captured := result.Result{StatusCode: response.StatusCode, Headers: canonicalHeaders(response.Header), BodySHA256: hex.EncodeToString(digest[:]), BodySize: int64(len(body)), Truncated: truncated}
	if state := response.TLS; state != nil {
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

func credentialHeader(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Authorization", "Cookie", "Proxy-Authorization":
		return true
	}
	return false
}

func diagnosticError(code diag.Code, message string, cause error) error {
	diagnostic, err := diag.New(code, message)
	if err != nil {
		return err
	}
	return &diag.Error{Diagnostic: diagnostic, Cause: cause}
}
