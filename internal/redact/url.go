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

// Text redacts every URL-shaped token inside s while leaving the rest of the
// message untouched. Probe diagnostics carry no URL today; this keeps them safe
// if one ever does, per ADR 0006.
func Text(s string) string {
	if s == "" {
		return s
	}
	fields := strings.Fields(s)
	changed := false
	for i, field := range fields {
		if strings.HasPrefix(field, "http://") || strings.HasPrefix(field, "https://") {
			fields[i] = URL(field)
			changed = true
		}
	}
	if !changed {
		return s
	}
	return strings.Join(fields, " ")
}
