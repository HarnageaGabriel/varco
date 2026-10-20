package redact

import (
	"strings"
	"unicode/utf8"
)

// EscapeControl makes text safe to print on a terminal. Every control
// character and every invalid UTF-8 byte becomes a visible \xNN escape, so a
// hostile or compromised server cannot put ANSI sequences, carriage returns or
// line breaks into the operator's terminal. Printable runes and valid UTF-8
// stay unchanged.
//
// Source: docs/adr/0006-redaction.md: the report layer is the only place that
// serializes what a remote server sent, so it is the layer that keeps a
// hostile response out of the operator's terminal. The helper lives in the
// pure redact package so every layer that prints remote text (the report
// tables and the CLI error output) escapes it the same way.
func EscapeControl(value string) string {
	if !needsEscape(value) {
		return value
	}
	var builder strings.Builder
	builder.Grow(len(value))
	for index := 0; index < len(value); {
		char := value[index]
		if char < utf8.RuneSelf {
			if isControlByte(char) {
				builder.WriteString(hexEscape(rune(char)))
			} else {
				builder.WriteByte(char)
			}
			index++
			continue
		}
		decoded, size := utf8.DecodeRuneInString(value[index:])
		switch {
		case decoded == utf8.RuneError && size == 1:
			// An invalid UTF-8 byte is escaped byte by byte.
			builder.WriteString(hexEscape(rune(char)))
			index++
		case isControlRune(decoded):
			// A C1 control is a control character too.
			builder.WriteString(hexEscape(decoded))
			index += size
		default:
			builder.WriteString(value[index : index+size])
			index += size
		}
	}
	return builder.String()
}

// needsEscape reports whether the value holds a byte or a rune that
// EscapeControl replaces.
func needsEscape(value string) bool {
	for index := 0; index < len(value); index++ {
		char := value[index]
		if char < utf8.RuneSelf {
			if isControlByte(char) {
				return true
			}
			continue
		}
		decoded, size := utf8.DecodeRuneInString(value[index:])
		if decoded == utf8.RuneError && size == 1 || isControlRune(decoded) {
			return true
		}
		index += size - 1
	}
	return false
}

// isControlByte reports whether the byte is a control character.
func isControlByte(char byte) bool {
	return char < 0x20 || char == 0x7f
}

// isControlRune reports whether the rune is a C1 control character.
func isControlRune(code rune) bool {
	return code >= 0x80 && code <= 0x9f
}

// hexEscape renders one byte or rune as a visible \xNN escape.
func hexEscape(code rune) string {
	const digits = "0123456789abcdef"
	return string([]byte{'\\', 'x', digits[(code>>4)&0xf], digits[code&0xf]})
}
