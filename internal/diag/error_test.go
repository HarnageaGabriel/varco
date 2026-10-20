package diag

import (
	"errors"
	"testing"
)

// Sources:
//   - docs/adr/0002-diagnostic-codes.md: every diagnostic carries a stable
//     VAR-<AREA>-NNNN code, so the rendered message is "<code>: <message>".
//   - CONVENTIONS.md "Diagnostics": wrap errors with %w; never compare error
//     strings. The cause is therefore found with errors.Is, not by matching
//     the message text.
func TestError(t *testing.T) {
	cause := errors.New("network failure")

	tests := []struct {
		name        string
		err         *Error
		wantMessage string
		wantCause   error
	}{
		{
			name:        "nil receiver",
			err:         nil,
			wantMessage: "<nil>",
		},
		{
			name: "diagnostic with cause",
			err: &Error{
				Diagnostic: Diagnostic{Code: CodeEndpointUnreachable, Severity: SeverityError, Message: "dial failed"},
				Cause:      cause,
			},
			wantMessage: "VAR-NET-0001: dial failed",
			wantCause:   cause,
		},
		{
			name: "diagnostic without cause",
			err: &Error{
				Diagnostic: Diagnostic{Code: CodeInvalidFlag, Severity: SeverityError, Message: "unknown flag"},
			},
			wantMessage: "VAR-CFG-0001: unknown flag",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.err.Error(); got != test.wantMessage {
				t.Fatalf("Error() = %q, want %q", got, test.wantMessage)
			}
			if got := test.err.Unwrap(); !errors.Is(got, test.wantCause) {
				t.Fatalf("Unwrap() = %v, want %v", got, test.wantCause)
			}
			if test.wantCause != nil && !errors.Is(test.err, test.wantCause) {
				t.Fatalf("errors.Is(%v, %v) = false, want true", test.err, test.wantCause)
			}
		})
	}
}
