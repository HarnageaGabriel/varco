// Package compare deterministically compares probe results.
package compare

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/result"
)

// Difference describes one observable mismatch.
type Difference struct {
	Path  string    `json:"path"`
	Code  diag.Code `json:"code"`
	Field string    `json:"field"`
	Old   string    `json:"old"`
	New   string    `json:"new"`
}

var comparedHeaders = []string{
	"access-control-allow-credentials", "access-control-allow-headers", "access-control-allow-methods",
	"access-control-allow-origin", "access-control-expose-headers", "access-control-max-age",
	"content-encoding", "cross-origin-embedder-policy", "cross-origin-opener-policy", "cross-origin-resource-policy",
	"content-security-policy", "permissions-policy", "referrer-policy", "strict-transport-security",
	"x-content-type-options", "x-frame-options",
}

// Results returns differences sorted by field, honoring additional ignored headers.
func Results(path string, oldResult, newResult result.Result, ignoreHeaders []string) []Difference {
	ignored := make(map[string]bool, len(ignoreHeaders)+5)
	for _, name := range ignoreHeaders {
		ignored[strings.ToLower(name)] = true
	}
	var differences []Difference
	add := func(code diag.Code, field, oldValue, newValue string) {
		if oldValue != newValue {
			differences = append(differences, Difference{Path: path, Code: code, Field: field, Old: oldValue, New: newValue})
		}
	}
	addHeader := func(code diag.Code, name string) {
		oldValues, oldOK := header(oldResult, name)
		newValues, newOK := header(newResult, name)
		if oldOK == newOK && slices.Equal(oldValues, newValues) {
			return
		}
		differences = append(differences, Difference{Path: path, Code: code, Field: "header." + name, Old: renderHeader(oldValues, oldOK), New: renderHeader(newValues, newOK)})
	}
	add(diag.CodeCompareStatus, "status", fmt.Sprint(oldResult.StatusCode), fmt.Sprint(newResult.StatusCode))
	if !ignored["location"] {
		addHeader(diag.CodeCompareLocation, "location")
	}
	for _, name := range comparedHeaders {
		if !ignored[name] {
			addHeader(diag.CodeCompareHeader, name)
		}
	}
	add(diag.CodeCompareTLSVersion, "tls.version", oldResult.TLSVersion, newResult.TLSVersion)
	compareCertificate(add, oldResult.Certificate, newResult.Certificate)
	oldBody := fmt.Sprintf("sha256=%s size=%d truncated=%t", oldResult.BodySHA256, oldResult.BodySize, oldResult.Truncated)
	newBody := fmt.Sprintf("sha256=%s size=%d truncated=%t", newResult.BodySHA256, newResult.BodySize, newResult.Truncated)
	add(diag.CodeCompareBody, "body", oldBody, newBody)
	sort.Slice(differences, func(i, j int) bool {
		if differences[i].Path != differences[j].Path {
			return differences[i].Path < differences[j].Path
		}
		if differences[i].Field != differences[j].Field {
			return differences[i].Field < differences[j].Field
		}
		return differences[i].Code < differences[j].Code
	})
	return differences
}

// header returns the ordered values of a response header and whether it is
// present; an absent header differs from one present with an empty value.
func header(r result.Result, name string) ([]string, bool) {
	for _, h := range r.Headers {
		if strings.EqualFold(h.Name, name) {
			return h.Values, true
		}
	}
	return nil, false
}

func renderHeader(values []string, present bool) string {
	if !present {
		return "(absent)"
	}
	if len(values) == 0 {
		return `""`
	}
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = value
		if value == "" || len(values) > 1 {
			quoted[i] = fmt.Sprintf("%q", value)
		}
	}
	return strings.Join(quoted, ", ")
}

func compareCertificate(add func(diag.Code, string, string, string), oldCert, newCert *result.Certificate) {
	if oldCert == nil || newCert == nil {
		add(diag.CodeCompareCertificate, "certificate", fmt.Sprintf("present=%t", oldCert != nil), fmt.Sprintf("present=%t", newCert != nil))
		return
	}
	fields := []struct{ name, old, new string }{
		{"subject", oldCert.Subject, newCert.Subject},
		{"issuer", oldCert.Issuer, newCert.Issuer},
		{"not_after", oldCert.NotAfter.UTC().Format("2006-01-02T15:04:05Z"), newCert.NotAfter.UTC().Format("2006-01-02T15:04:05Z")},
		{"fingerprint", oldCert.Fingerprint, newCert.Fingerprint},
		{"dns_sans", strings.Join(oldCert.DNSNames, ","), strings.Join(newCert.DNSNames, ",")},
	}
	for _, field := range fields {
		add(diag.CodeCompareCertificate, "certificate."+field.name, field.old, field.new)
	}
}
