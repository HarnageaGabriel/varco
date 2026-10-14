package redact

import (
	"net/url"
	"strings"
	"testing"
)

// Sources:
//   - RFC 6749 section 4.1.2: authorization codes are returned in redirect
//     query parameters.
//   - AWS Signature Version 4 query authentication: presigned URLs carry
//     credentials and signatures in query values.
func TestURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"OAuth redirect", "https://client.example/cb?code=secret-code&state=opaque", "https://client.example/cb?code=REDACTED&state=REDACTED"},
		{"presigned URL keeps query order", "https://bucket.example/object?X-Amz-Credential=credential&X-Amz-Signature=signature", "https://bucket.example/object?X-Amz-Credential=REDACTED&X-Amz-Signature=REDACTED"},
		{"relative Location", "/cb?code=secret", "/cb?code=REDACTED"},
		{"repeated key", "/cb?code=one&code=two", "/cb?code=REDACTED&code=REDACTED"},
		{"empty and valueless parameters", "/cb?empty=&flag", "/cb?empty=REDACTED&flag=REDACTED"},
		{"userinfo and fragment", "https://user:password@example.com/a#access-token", "https://REDACTED@example.com/a#REDACTED"},
		{"path unchanged", "/public/path", "/public/path"},
		{"invalid query escape is removed", "/cb?code=%zz", "/cb?code=REDACTED"},
		{"invalid path escape", "/cb%zz?code=secret", "REDACTED"},
		{"control byte", "/cb?code=abc\n", "REDACTED"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := URL(test.raw); got != test.want {
				t.Fatalf("URL(%q) = %q, want %q", test.raw, got, test.want)
			}
		})
	}
}

func FuzzURL(f *testing.F) {
	for _, seed := range []string{
		"/cb?code=secret",
		"https://user:pass@example.com/object?token=abcdef#fragment",
		"/path?one=first&two=second",
		"/path?empty=&flag",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		first, second := URL(raw), URL(raw)
		if first != second {
			t.Fatalf("URL is not deterministic: %q != %q", first, second)
		}

		parsed, err := url.Parse(raw)
		if err != nil || parsed.RawQuery == "" {
			return
		}
		allowed := *parsed
		allowed.RawQuery = ""
		allowed.ForceQuery = false
		allowed.Fragment = ""
		allowed.RawFragment = ""
		if allowed.User != nil {
			allowed.User = url.User("REDACTED")
		}
		allowedOutput := allowed.String()
		for _, part := range strings.Split(parsed.RawQuery, "&") {
			key, value, ok := strings.Cut(part, "=")
			if !ok {
				value = ""
			}
			decoded, decodeErr := url.QueryUnescape(value)
			if decodeErr != nil || len(decoded) < 3 || decoded == "REDACTED" {
				continue
			}
			// A value repeated in a deliberately retained component is not a
			// query leak. Query keys are also retained by contract.
			if strings.Contains(allowedOutput, decoded) || strings.Contains(key, decoded) {
				continue
			}
			if strings.Contains(first, decoded) {
				t.Fatalf("redacted URL %q contains query value %q", first, decoded)
			}
		}
	})
}

// Source: docs/adr/0006-redaction.md; probe and run diagnostics pass through
// Text so no URL-shaped value reaches output unredacted.
func TestText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"no url", "probe 10.0.0.2:80: dial tcp: connection refused", "probe 10.0.0.2:80: dial tcp: connection refused"},
		{"embedded url", "probe failed for https://api.example.com/x?token=secret", "probe failed for https://api.example.com/x?token=REDACTED"},
		{"empty", "", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Text(test.input); got != test.want {
				t.Fatalf("Text(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}
