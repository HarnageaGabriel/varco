// Package compare deterministically compares probe results.
package compare

import (
	"fmt"
	"maps"
	"net/http" //nolint:depguard // ParseSetCookie is used only as a pure standards parser.
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
	if !ignored["set-cookie"] {
		oldValues, oldOK := header(oldResult, "set-cookie")
		newValues, newOK := header(newResult, "set-cookie")
		oldCookies := parseSetCookies(oldValues, oldOK)
		newCookies := parseSetCookies(newValues, newOK)
		if !equalCookieSets(oldCookies, newCookies) {
			differences = append(differences, Difference{
				Path: path, Code: diag.CodeCompareCookie, Field: "header.set-cookie",
				Old: renderCookieSet(oldCookies), New: renderCookieSet(newCookies),
			})
		}
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

type cookieAttributes struct {
	Secure, HTTPOnly, HasMaxAge, HasExpires, Partitioned bool
	SameSite, Path, Domain                               string
}

type cookieSet struct {
	present bool
	invalid int
	cookies map[string]cookieAttributes
}

func parseSetCookies(values []string, present bool) cookieSet {
	set := cookieSet{present: present, cookies: make(map[string]cookieAttributes)}
	for _, value := range values {
		cookie, err := http.ParseSetCookie(value)
		if err != nil {
			set.invalid++
			continue
		}
		set.cookies[cookie.Name] = cookieAttributes{
			Secure: cookie.Secure, HTTPOnly: cookie.HttpOnly,
			SameSite: sameSiteName(cookie.SameSite), Path: cookie.Path, Domain: cookie.Domain,
			HasMaxAge: hasCookieAttribute(value, "Max-Age"), HasExpires: hasCookieAttribute(value, "Expires"), Partitioned: cookie.Partitioned,
		}
	}
	return set
}

func hasCookieAttribute(raw, name string) bool {
	parts := strings.Split(raw, ";")
	for _, part := range parts[1:] {
		attribute, _, _ := strings.Cut(strings.TrimSpace(part), "=")
		if strings.EqualFold(attribute, name) {
			return true
		}
	}
	return false
}

func equalCookieSets(oldSet, newSet cookieSet) bool {
	return oldSet.present == newSet.present && oldSet.invalid == newSet.invalid && maps.Equal(oldSet.cookies, newSet.cookies)
}

func renderCookieSet(set cookieSet) string {
	if !set.present {
		return "(absent)"
	}
	names := make([]string, 0, len(set.cookies))
	for name := range set.cookies {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names)+1)
	for _, name := range names {
		attributes := set.cookies[name]
		parts = append(parts, fmt.Sprintf(
			"%s{secure=%t,http_only=%t,same_site=%s,path=%q,domain=%q,max_age=%t,expires=%t,partitioned=%t}",
			name, attributes.Secure, attributes.HTTPOnly, attributes.SameSite, attributes.Path, attributes.Domain,
			attributes.HasMaxAge, attributes.HasExpires, attributes.Partitioned,
		))
	}
	if set.invalid != 0 {
		parts = append(parts, fmt.Sprintf("invalid=%d", set.invalid))
	}
	if len(parts) == 0 {
		return "(empty)"
	}
	return strings.Join(parts, ", ")
}

func sameSiteName(sameSite http.SameSite) string {
	switch sameSite {
	case http.SameSiteDefaultMode:
		return "Default"
	case http.SameSiteLaxMode:
		return "Lax"
	case http.SameSiteStrictMode:
		return "Strict"
	case http.SameSiteNoneMode:
		return "None"
	default:
		return "Unknown"
	}
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
