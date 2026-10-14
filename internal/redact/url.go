// Package redact removes sensitive values from data before serialization.
package redact

import (
	"net/url"
	"strings"
)

const replacement = "REDACTED"

// URL removes sensitive URL components while preserving the scheme, host,
// path, query keys, and query-key order. Invalid URLs are replaced wholesale.
func URL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return replacement
	}
	if parsed.User != nil {
		parsed.User = url.User(replacement)
	}
	if parsed.RawQuery != "" {
		parts := strings.Split(parsed.RawQuery, "&")
		for i, part := range parts {
			key, _, _ := strings.Cut(part, "=")
			parts[i] = key + "=" + replacement
		}
		parsed.RawQuery = strings.Join(parts, "&")
	}
	if parsed.Fragment != "" || parsed.RawFragment != "" {
		parsed.Fragment = replacement
		parsed.RawFragment = ""
	}
	return parsed.String()
}
