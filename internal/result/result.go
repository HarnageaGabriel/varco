// Package result defines value types shared by probing and pure comparison.
package result

import "time"

// Header is one canonical response header and its ordered values.
type Header struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

// Certificate describes the peer leaf certificate.
type Certificate struct {
	Subject     string    `json:"subject"`
	Issuer      string    `json:"issuer"`
	Serial      string    `json:"serial"`
	NotBefore   time.Time `json:"not_before"`
	NotAfter    time.Time `json:"not_after"`
	Fingerprint string    `json:"sha256_fingerprint"`
	DNSNames    []string  `json:"dns_sans"`
}

// Result is the captured response from one probe.
type Result struct {
	StatusCode  int          `json:"status_code"`
	Headers     []Header     `json:"headers"`
	TLSVersion  string       `json:"tls_version,omitempty"`
	CipherSuite string       `json:"cipher_suite,omitempty"`
	ALPN        string       `json:"alpn,omitempty"`
	Certificate *Certificate `json:"certificate,omitempty"`
	BodySHA256  string       `json:"body_sha256"`
	BodySize    int64        `json:"body_size"`
	Truncated   bool         `json:"truncated"`
}
