// Package diag defines stable diagnostics and their registry.
package diag

import (
	"fmt"
	"sort"
	"sync"
)

// Code is a stable diagnostic identifier.
type Code string

const (
	// CodeInvalidFlag reports an invalid command-line flag.
	CodeInvalidFlag Code = "VAR-CFG-0001"
	// CodeEndpointUnreachable reports a failed connection to an endpoint.
	CodeEndpointUnreachable Code = "VAR-NET-0001"
	// CodeUnsupportedMethod reports an unsafe or unsupported HTTP method.
	CodeUnsupportedMethod Code = "VAR-RUN-0001"
)

// Severity is a diagnostic impact level.
type Severity string

const (
	// SeverityError prevents the requested operation from completing.
	SeverityError Severity = "error"
	// SeverityWarning records a condition requiring attention.
	SeverityWarning Severity = "warning"
	// SeverityInfo records informational context.
	SeverityInfo Severity = "info"
)

// Entry describes one registered diagnostic code.
type Entry struct {
	Code        Code
	Area        string
	Title       string
	Severity    Severity
	Description string
	Remediation string
}

// Diagnostic is one diagnostic occurrence.
type Diagnostic struct {
	Code     Code     `json:"code"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
}

// Error carries a diagnostic through an error chain.
type Error struct {
	Diagnostic Diagnostic
	Cause      error
}

// Error returns a human-readable diagnostic message.
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s: %s", e.Diagnostic.Code, e.Diagnostic.Message)
}

// Unwrap returns the underlying cause, if any.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// New constructs a diagnostic from a registered code.
func New(code Code, message string) (Diagnostic, error) {
	entry, ok := Lookup(code)
	if !ok {
		return Diagnostic{}, fmt.Errorf("unknown diagnostic code %q", code)
	}
	if message == "" {
		message = entry.Title
	}
	return Diagnostic{Code: code, Severity: entry.Severity, Message: message}, nil
}

// Lookup returns registry metadata for code.
func Lookup(code Code) (Entry, bool) {
	for _, entry := range List() {
		if entry.Code == code {
			return entry, true
		}
	}
	return Entry{}, false
}

// List returns registry entries sorted by code.
func List() []Entry {
	entries := []Entry{
		{Code: CodeInvalidFlag, Area: "CFG", Title: "Invalid flag", Severity: SeverityError, Description: "A command-line flag is unknown or has an invalid value.", Remediation: "Correct the flag name or value and retry."},
		{Code: CodeEndpointUnreachable, Area: "NET", Title: "Endpoint unreachable", Severity: SeverityError, Description: "The probe could not connect to the configured endpoint.", Remediation: "Verify the endpoint address, port, and network reachability."},
		{Code: CodeUnsupportedMethod, Area: "RUN", Title: "Unsupported method", Severity: SeverityError, Description: "The requested HTTP method is not enabled for this run.", Remediation: "Use GET, HEAD, or OPTIONS, or explicitly opt in to the method for this case and run."},
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Code < entries[j].Code })
	return entries
}

// Collector safely accumulates diagnostics in insertion order.
type Collector struct {
	mu    sync.Mutex
	items []Diagnostic
}

// Add records a diagnostic.
func (c *Collector) Add(diagnostic Diagnostic) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = append(c.items, diagnostic)
}

// List returns an independent snapshot of collected diagnostics.
func (c *Collector) List() []Diagnostic {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Diagnostic(nil), c.items...)
}
