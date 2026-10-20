package report

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"unicode/utf8"
)

// safeCell makes one table cell safe to print. Every control character and
// every invalid UTF-8 byte becomes a visible \xNN escape, so a hostile or
// compromised server cannot put ANSI sequences, carriage returns or line
// breaks into the operator's terminal through the table report. Printable
// runes and valid UTF-8 stay unchanged.
func safeCell(value string) string {
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
// safeCell replaces.
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

// writeRow writes one row of cells to the table. Every cell is escaped on the
// way in, so this is the one place where text reaches the table writer.
func writeRow(writer *tabwriter.Writer, cells ...string) error {
	escaped := make([]string, len(cells))
	for index, cell := range cells {
		escaped[index] = safeCell(cell)
	}
	_, err := fmt.Fprintln(writer, strings.Join(escaped, "\t"))
	return err
}
