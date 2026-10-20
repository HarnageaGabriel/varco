package redact

import (
	"net/url"
	"slices"
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
		if allowed.User != nil {
			allowed.User = url.User(replacement)
		}
		want, wantErr := url.Parse(allowed.String())
		if wantErr != nil {
			t.Fatalf("allowed URL %q does not parse: %v", allowed.String(), wantErr)
		}
		reparsed, parseErr := url.Parse(first)
		if parseErr != nil {
			t.Fatalf("redacted URL %q does not parse: %v", first, parseErr)
		}
		if reparsed.Scheme != want.Scheme ||
			reparsed.Opaque != want.Opaque ||
			reparsed.Host != want.Host ||
			reparsed.Path != want.Path ||
			!sameUserinfo(reparsed.User, want.User) {
			t.Fatalf("redacted URL %q changed non-query parts: want %q", first, want.String())
		}
		for _, part := range strings.Split(reparsed.RawQuery, "&") {
			key, value, _ := strings.Cut(part, "=")
			decoded, decodeErr := url.QueryUnescape(value)
			if decodeErr != nil || (decoded != "" && decoded != replacement) {
				t.Fatalf("redacted URL %q has unredacted query value %q for key %q", first, decoded, key)
			}
		}
		inputKeys := queryKeys(parsed.RawQuery)
		outputKeys := queryKeys(reparsed.RawQuery)
		if !slices.Equal(inputKeys, outputKeys) {
			t.Fatalf("redacted URL %q changed query keys: want %v, got %v", first, inputKeys, outputKeys)
		}
		wantFragment := ""
		if parsed.Fragment != "" || parsed.RawFragment != "" {
			wantFragment = replacement
		}
		if reparsed.Fragment != wantFragment || reparsed.RawFragment != "" {
			t.Fatalf("redacted URL %q changed fragment: want %q, got %q", first, wantFragment, reparsed.Fragment)
		}
	})
}

func queryKeys(rawQuery string) []string {
	parts := strings.Split(rawQuery, "&")
	keys := make([]string, len(parts))
	for i, part := range parts {
		keys[i], _, _ = strings.Cut(part, "=")
	}
	return keys
}

func sameUserinfo(got, want *url.Userinfo) bool {
	if got == nil || want == nil {
		return got == want
	}
	return got.String() == want.String()
}

// Source: docs/adr/0006-redaction.md; probe and run diagnostics pass through
// Text so no URL-shaped value reaches output unredacted. The scheme may start
// at any offset inside a token, so a URL that is quoted, prefixed or wrapped
// in punctuation is redacted as well.
func TestText(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		secrets []string
	}{
		{
			name:  "kubectl error with server url",
			input: "error: failed to list: Get https://admin:sup3rs3cret@api.cluster.local:6443/api?timeout=32s: dial tcp 10.0.0.1:6443: connect: connection refused",
			want:  "error: failed to list: Get https://REDACTED@api.cluster.local:6443/api?timeout=REDACTED: dial tcp 10.0.0.1:6443: connect: connection refused",
			secrets: []string{
				"sup3rs3cret",
				"32s",
			},
		},
		{
			name:  "url after a prefix",
			input: "error: proxy=https://admin:sup3rs3cret@api.cluster.local/api",
			want:  "error: proxy=https://REDACTED@api.cluster.local/api",
			secrets: []string{
				"sup3rs3cret",
			},
		},
		{
			name:  "url in single quotes",
			input: "error: url='https://admin:sup3rs3cret@api.cluster.local/api'",
			want:  "error: url='https://REDACTED@api.cluster.local/api'",
			secrets: []string{
				"sup3rs3cret",
			},
		},
		{
			name:  "plain url token",
			input: "https://api.example.com/x?token=secret",
			want:  "https://api.example.com/x?token=REDACTED",
			secrets: []string{
				"secret",
			},
		},
		{
			name:  "url in double quotes",
			input: `see "https://api.example.com/x?token=secret"`,
			want:  `see "https://api.example.com/x?token=REDACTED"`,
			secrets: []string{
				"secret",
			},
		},
		{
			name:  "url in parentheses",
			input: "(https://api.example.com/x?token=secret)",
			want:  "(https://api.example.com/x?token=REDACTED)",
			secrets: []string{
				"secret",
			},
		},
		{
			name:  "url followed by a comma",
			input: "error: failed for https://api.example.com/x?token=secret, retrying",
			want:  "error: failed for https://api.example.com/x?token=REDACTED, retrying",
			secrets: []string{
				"secret",
			},
		},
		{
			name:  "two urls in one token",
			input: "https://u1:p1@a.example/x,https://u2:p2@b.example/y",
			want:  "https://REDACTED@a.example/x,https://REDACTED@b.example/y",
			secrets: []string{
				"p1",
				"p2",
			},
		},
		{
			name:  "uppercase scheme",
			input: "error: HTTPS://api.example.com/x?token=secret",
			want:  "error: https://api.example.com/x?token=REDACTED",
			secrets: []string{
				"secret",
			},
		},
		{
			name:  "url with a fragment",
			input: "error: see https://api.example.com/x#token=secret",
			want:  "error: see https://api.example.com/x#REDACTED",
			secrets: []string{
				"secret",
			},
		},
		{
			name:  "unparseable url after a prefix",
			input: "error: fetch https://example.com:notaport/x failed",
			want:  "error: fetch REDACTED failed",
			secrets: []string{
				"notaport",
			},
		},
		{
			name:  "no url",
			input: "probe 10.0.0.2:80: dial tcp: connection refused",
			want:  "probe 10.0.0.2:80: dial tcp: connection refused",
		},
		{
			name:  "embedded url",
			input: "probe failed for https://api.example.com/x?token=secret",
			want:  "probe failed for https://api.example.com/x?token=REDACTED",
			secrets: []string{
				"secret",
			},
		},
		{
			name:  "empty",
			input: "",
			want:  "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Text(test.input)
			if got != test.want {
				t.Fatalf("Text(%q) = %q, want %q", test.input, got, test.want)
			}
			for _, secret := range test.secrets {
				if strings.Contains(got, secret) {
					t.Fatalf("Text(%q) = %q exposes %q", test.input, got, secret)
				}
			}
			if second := Text(got); second != got {
				t.Fatalf("Text is not idempotent: %q != %q", got, second)
			}
		})
	}
}

// Source: docs/adr/0006-redaction.md - a URL anywhere in a message keeps its
// userinfo out of the output, whatever surrounds the URL.
func FuzzText(f *testing.F) {
	for _, seed := range []struct {
		prefix   string
		password string
		suffix   string
	}{
		{"error: failed to list: Get ", "sup3rs3cret", ": dial tcp 10.0.0.1:6443: connect: connection refused"},
		{"error: proxy=", "sup3rs3cret", ""},
		{"error: url='", "sup3rs3cret", "'"},
		{"", "s3cr3t", ""},
		{"https://", "s3cr3t", ""},
	} {
		f.Add(seed.prefix, seed.password, seed.suffix)
	}
	f.Fuzz(func(t *testing.T, prefix, password, suffix string) {
		input := prefix + "http://u:" + password + "@host/path" + suffix
		first := Text(input)
		if second := Text(first); second != first {
			t.Fatalf("Text is not idempotent: %q != %q", first, second)
		}
		if !userinfoPassword(password) {
			return
		}
		if strings.Contains(prefix, password) || strings.Contains(suffix, password) {
			return
		}
		// The userinfo is redacted as a whole, so the output cannot depend on
		// the password. A control password of the same shape proves that the
		// password of the input does not survive in the output.
		control := Text(prefix + "http://u:" + password + "x@host/path" + suffix)
		if first != control {
			t.Fatalf("Text(%q) = %q exposes the userinfo password", input, first)
		}
	})
}

// userinfoPassword reports whether password is the password of the userinfo
// of the generated URL, so that only a real credential is checked.
func userinfoPassword(password string) bool {
	if password == "" {
		return false
	}
	parsed, err := url.Parse("http://u:" + password + "@host/path")
	if err != nil || parsed.User == nil {
		return false
	}
	value, set := parsed.User.Password()
	return set && value == password
}
