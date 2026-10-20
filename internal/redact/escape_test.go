package redact

import (
	"testing"
	"unicode/utf8"
)

// Source: docs/adr/0006-redaction.md: the report layer is the only place that
// serializes what a remote server sent, so it is the layer that keeps a
// hostile response out of the operator's terminal. Every control character
// and every invalid UTF-8 byte becomes a visible \xNN escape; printable text
// stays unchanged.
func TestEscapeControl(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"empty", "", ""},
		{"printable", "plain text", "plain text"},
		{"non ascii stays", "grüße 日本語", "grüße 日本語"},
		{"replacement character stays", "a\ufffdb", "a\ufffdb"},
		{"escape sequence", "\x1b[31m", `\x1b[31m`},
		{"carriage return", "a\rb", `a\x0db`},
		{"line feed", "a\nb", `a\x0ab`},
		{"tab", "a\tb", `a\x09b`},
		{"null", "a\x00b", `a\x00b`},
		{"bell", "a\x07b", `a\x07b`},
		{"delete", "a\x7fb", `a\x7fb`},
		{"c1 control", "a\u009bb", `a\x9bb`},
		{"invalid utf8", "a\xffb", `a\xffb`},
		{"several control bytes", "a\x00b\x1bc\nd", `a\x00b\x1bc\x0ad`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := EscapeControl(test.value); got != test.want {
				t.Fatalf("EscapeControl(%q) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}

// Source: docs/adr/0006-redaction.md: an escaped value never carries a control
// character, so nothing a remote server sent can move the cursor, clear the
// screen or rewrite a line of the operator's terminal.
func TestEscapeControlRemovesEveryControlCharacter(t *testing.T) {
	value := "text\x1b[31m\r\n\x00\x07\x7f\u009b\xff end"
	escaped := EscapeControl(value)
	for _, r := range escaped {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			t.Fatalf("EscapeControl(%q) = %q, want no control character", value, escaped)
		}
	}
	if !utf8.ValidString(escaped) {
		t.Fatalf("EscapeControl(%q) = %q, want valid UTF-8", value, escaped)
	}
}

// Source: docs/adr/0006-redaction.md: escaping is a pure function of the
// value, so the same text always prints the same way.
func TestEscapeControlIsDeterministic(t *testing.T) {
	value := "a\x1bb\x00c"
	if first, second := EscapeControl(value), EscapeControl(value); first != second {
		t.Fatalf("EscapeControl(%q) = %q, then %q, want the same value", value, first, second)
	}
}
