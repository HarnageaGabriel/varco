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
	redacted, _ := redactURL(raw)
	return redacted
}

// redactURL removes the sensitive parts of raw and reports whether raw parsed
// as a URL. An invalid URL is replaced wholesale.
func redactURL(raw string) (string, bool) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return replacement, false
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
	return parsed.String(), true
}

// Text redacts every URL-shaped token inside s while leaving the rest of the
// message untouched. The scheme may start at any offset inside a token, so a
// URL that is quoted, prefixed or wrapped in punctuation is redacted too.
// Probe diagnostics carry no URL today; this keeps them safe if one ever
// does, per ADR 0006.
func Text(s string) string {
	if s == "" || indexScheme(s, 0) < 0 {
		return s
	}
	spans := urlSpans(s)
	var out strings.Builder
	out.Grow(len(s))
	offset := 0
	for index := 0; index < len(spans); {
		run := index
		unparsable := false
		index++
		for index < len(spans) && spans[index].start == spans[index-1].end {
			if !spans[index].parsed {
				unparsable = true
			}
			index++
		}
		out.WriteString(s[offset:spans[run].start])
		if unparsable {
			// A URL that cannot be parsed loses its scheme, so the URLs
			// next to it are replaced as a whole. A second pass then
			// redacts the same text.
			out.WriteString(replacement)
		} else {
			for _, span := range spans[run:index] {
				out.WriteString(span.redacted)
				out.WriteString(span.trailing)
			}
		}
		offset = spans[index-1].end
	}
	out.WriteString(s[offset:])
	return out.String()
}

// urlSpan is one URL candidate inside a message.
type urlSpan struct {
	start    int
	end      int
	redacted string
	trailing string
	parsed   bool
}

// urlSpans returns the URL candidates of s in the order they appear.
func urlSpans(s string) []urlSpan {
	var spans []urlSpan
	for offset := 0; offset < len(s); {
		start := indexScheme(s, offset)
		if start < 0 {
			break
		}
		end := urlEnd(s, start)
		body, trailing := splitTrailing(s[start:end], end == len(s) || isSpace(s[end]))
		redacted, parsed := redactURL(body)
		if parsed {
			redacted = keepSeparator(body, redacted)
		}
		spans = append(spans, urlSpan{
			start:    start,
			end:      end,
			redacted: redacted,
			trailing: trailing,
			parsed:   parsed,
		})
		offset = end
	}
	return spans
}

// keepSeparator puts the scheme separator back when the serialization of a
// URL without a host dropped it. The text stays a URL, so the boundary of
// the next URL does not move when the message is redacted again.
func keepSeparator(original, redacted string) string {
	scheme := "http"
	if len(original) >= 8 && strings.EqualFold(original[:8], "https://") {
		scheme = "https"
	}
	if strings.HasPrefix(redacted, scheme+"://") {
		return redacted
	}
	return scheme + "://" + strings.TrimPrefix(redacted, scheme+":")
}

// indexScheme returns the offset of the first http or https scheme at or
// after from, or -1 when the string holds none. The scheme is matched
// case-insensitively.
func indexScheme(s string, from int) int {
	for i := from; i+4 <= len(s); i++ {
		if schemeAt(s, i) {
			return i
		}
	}
	return -1
}

// schemeAt reports whether s holds an http or https scheme at offset i.
func schemeAt(s string, i int) bool {
	if i+4 > len(s) || !strings.EqualFold(s[i:i+4], "http") {
		return false
	}
	rest := s[i+4:]
	if len(rest) >= 3 && rest[0] == ':' && rest[1] == '/' && rest[2] == '/' {
		return true
	}
	if len(rest) >= 4 && (rest[0] == 's' || rest[0] == 'S') &&
		rest[1] == ':' && rest[2] == '/' && rest[3] == '/' {
		return true
	}
	return false
}

// urlEnd returns the offset just past the URL that starts at start. The URL
// ends at the next whitespace or at the next scheme, so that every URL of a
// token is redacted on its own. The boundaries depend on the scheme only, so
// the redaction of one URL never moves the boundary of the next one.
func urlEnd(s string, start int) int {
	for i := start + 1; i < len(s); i++ {
		if isSpace(s[i]) {
			return i
		}
		if schemeAt(s, i) {
			return i
		}
	}
	return len(s)
}

// splitTrailing separates the delimiters that close a URL from the URL
// itself. The delimiters are re-appended to the redacted URL so the message
// keeps its punctuation. A colon only closes a URL when a space follows it,
// which keeps a host:port pair intact. A delimiter the URL needs (the
// bracket of an IPv6 host for example) stays with the URL.
func splitTrailing(candidate string, spaceAfter bool) (string, string) {
	end := len(candidate)
	if spaceAfter && end > 0 && candidate[end-1] == ':' {
		end--
	}
	for end > 0 && isDelimiter(candidate[end-1]) {
		end--
	}
	body, trailing := candidate[:end], candidate[end:]
	if trailing == "" {
		return body, trailing
	}
	if _, err := url.Parse(body); err != nil {
		// The URL needs the delimiter that was taken. The shortest tail that
		// parses again is kept with the URL.
		for keep := 1; keep <= 2 && keep <= len(trailing); keep++ {
			restored := candidate[:end+keep]
			if _, err := url.Parse(restored); err == nil {
				return restored, candidate[end+keep:]
			}
		}
		return candidate, ""
	}
	return body, trailing
}

// isDelimiter reports whether c closes or separates a URL without being part
// of it.
func isDelimiter(c byte) bool {
	switch c {
	case '"', '\'', ')', ']', ',', ';', '.':
		return true
	default:
		return false
	}
}

// isSpace reports whether c separates two tokens.
func isSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\v', '\f', '\r':
		return true
	default:
		return false
	}
}
