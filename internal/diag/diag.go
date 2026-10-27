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
	// CodeExceptionsFile reports an exceptions file that cannot be parsed or is invalid.
	CodeExceptionsFile Code = "VAR-CFG-0002"
	// CodeEndpointUnreachable reports a failed connection to an endpoint.
	CodeEndpointUnreachable Code = "VAR-NET-0001"
	// CodeUnsupportedMethod reports an unsafe or unsupported HTTP method.
	CodeUnsupportedMethod Code = "VAR-RUN-0001"
	// CodeCompareStatus reports an HTTP status mismatch.
	CodeCompareStatus Code = "VAR-CMP-0001"
	// CodeCompareLocation reports a redirect location mismatch.
	CodeCompareLocation Code = "VAR-CMP-0002"
	// CodeCompareHeader reports a security or CORS header mismatch.
	CodeCompareHeader Code = "VAR-CMP-0003"
	// CodeCompareTLSVersion reports a TLS protocol mismatch.
	CodeCompareTLSVersion Code = "VAR-CMP-0004"
	// CodeCompareCertificate reports a leaf certificate mismatch.
	CodeCompareCertificate Code = "VAR-CMP-0005"
	// CodeCompareBody reports a response body mismatch.
	CodeCompareBody Code = "VAR-CMP-0006"
	// CodeCompareCookie reports a Set-Cookie attribute mismatch.
	CodeCompareCookie Code = "VAR-CMP-0007"
	// CodePlanAnnotationNotTestable reports an annotation varco cannot test black-box.
	CodePlanAnnotationNotTestable Code = "VAR-PLAN-0001"
	// CodePlanRegexSample reports a regex path varco cannot sample.
	CodePlanRegexSample Code = "VAR-PLAN-0002"
	// CodePlanManifest reports a manifest that cannot be parsed or is not supported.
	CodePlanManifest Code = "VAR-PLAN-0003"
	// CodePlanWildcardHost reports a wildcard host planned with a concrete label.
	CodePlanWildcardHost Code = "VAR-PLAN-0004"
	// CodePlanRuleWithoutHost reports a rule with no host and no default host.
	CodePlanRuleWithoutHost Code = "VAR-PLAN-0005"
	// CodePlanRegexHostWide reports host-wide regex scope in ingress-nginx.
	CodePlanRegexHostWide Code = "VAR-PLAN-0006"
	// CodePlanAnnotationBool reports an annotation whose value is not a boolean.
	CodePlanAnnotationBool Code = "VAR-PLAN-0007"
	// CodePlanNoPath reports an Ingress or rule with no plannable HTTP path.
	CodePlanNoPath Code = "VAR-PLAN-0008"
	// CodePlanConfigMap reports a controller ConfigMap setting that can change
	// observable request or response behavior.
	CodePlanConfigMap Code = "VAR-PLAN-0010"
	// CodePlanAnnotationsIgnored reports annotations ignored by the generic profile.
	CodePlanAnnotationsIgnored Code = "VAR-PLAN-0009"
	// CodePlanIngressClassExcluded reports an Ingress excluded from the plan
	// because its class does not match the requested one.
	CodePlanIngressClassExcluded Code = "VAR-PLAN-0011"
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
		{Code: CodeCompareStatus, Area: "CMP", Title: "Status differs", Severity: SeverityInfo, Description: "The endpoints returned different HTTP status codes.", Remediation: "Confirm that routing and application behavior are equivalent."},
		{Code: CodeCompareLocation, Area: "CMP", Title: "Location differs", Severity: SeverityInfo, Description: "The endpoints returned different Location header values.", Remediation: "Confirm that redirect targets are equivalent."},
		{Code: CodeCompareHeader, Area: "CMP", Title: "Header differs", Severity: SeverityInfo, Description: "A security or CORS response header differs between endpoints.", Remediation: "Align the response header policy or explicitly ignore the header."},
		{Code: CodeCompareTLSVersion, Area: "CMP", Title: "TLS version differs", Severity: SeverityInfo, Description: "The endpoints negotiated different TLS protocol versions.", Remediation: "Review the TLS configuration on both endpoints."},
		{Code: CodeCompareCertificate, Area: "CMP", Title: "Certificate differs", Severity: SeverityInfo, Description: "A compared leaf certificate field differs between endpoints.", Remediation: "Confirm that the certificate change is expected."},
		{Code: CodeCompareBody, Area: "CMP", Title: "Body differs", Severity: SeverityInfo, Description: "The response body hash, captured size, or truncation state differs.", Remediation: "Review the response content returned by each endpoint."},
		{Code: CodeCompareCookie, Area: "CMP", Title: "Cookie attributes differ", Severity: SeverityInfo, Description: "The endpoints set different cookie names or cookie security and scope attributes. Cookie values are never compared or reported.", Remediation: "Align the Set-Cookie attributes, or confirm that the difference is expected."},
		{Code: CodeInvalidFlag, Area: "CFG", Title: "Invalid flag", Severity: SeverityError, Description: "A command-line flag is unknown or has an invalid value.", Remediation: "Correct the flag name or value and retry."},
		{Code: CodeExceptionsFile, Area: "CFG", Title: "Invalid exceptions file", Severity: SeverityError, Description: "The --exceptions file cannot be parsed, uses an unsupported version, or defines an exception with a missing id, a duplicate id, a missing reason, an invalid date, or an invalid glob.", Remediation: "Fix the exceptions file and retry; see docs/run.md for the schema."},
		{Code: CodeEndpointUnreachable, Area: "NET", Title: "Endpoint unreachable", Severity: SeverityError, Description: "The probe could not connect to the configured endpoint.", Remediation: "Verify the endpoint address, port, and network reachability."},
		{Code: CodeUnsupportedMethod, Area: "RUN", Title: "Unsupported method", Severity: SeverityError, Description: "The requested HTTP method is not enabled for this run.", Remediation: "Use GET, HEAD, or OPTIONS, or explicitly opt in to the method for this case and run."},
		{Code: CodePlanAnnotationNotTestable, Area: "PLAN", Title: "Annotation not testable", Severity: SeverityWarning, Description: "An Ingress annotation changes behavior that varco cannot observe black-box, or is unknown to varco.", Remediation: "Verify the annotation manually against the target controller, or confirm it is intentionally out of scope."},
		{Code: CodePlanRegexSample, Area: "PLAN", Title: "Regex sample not derivable", Severity: SeverityWarning, Description: "A path uses regular expressions in a form varco cannot reduce to one literal request path.", Remediation: "Probe this path manually, or rewrite it with a literal prefix that varco can sample."},
		{Code: CodePlanManifest, Area: "PLAN", Title: "Manifest parse error", Severity: SeverityError, Description: "A manifest could not be decoded, or uses an Ingress API version varco does not support.", Remediation: "Fix the manifest, or export the Ingress with apiVersion networking.k8s.io/v1."},
		{Code: CodePlanWildcardHost, Area: "PLAN", Title: "Wildcard host substituted", Severity: SeverityWarning, Description: "A rule declares a wildcard host; cases use the concrete host varco-probe.<zone>.", Remediation: "Confirm that the wildcard certificate and routing cover an arbitrary single-label subdomain."},
		{Code: CodePlanRuleWithoutHost, Area: "PLAN", Title: "Rule without host", Severity: SeverityWarning, Description: "A rule declares no host and no default host was provided, so no case could be generated for it.", Remediation: "Pass --default-host, or give the rule an explicit host."},
		{Code: CodePlanRegexHostWide, Area: "PLAN", Title: "Host-wide regex scope", Severity: SeverityWarning, Description: "use-regex or rewrite-target on one Ingress makes ingress-nginx match every path of that host as regex, including paths declared by other Ingresses.", Remediation: "Check every path of this host explicitly; the gateway implementation may match them literally."},
		{Code: CodePlanAnnotationBool, Area: "PLAN", Title: "Annotation value is not a boolean", Severity: SeverityWarning, Description: "An annotation that ingress-nginx reads as a boolean does not parse as one, so varco treats it as unset.", Remediation: "Set the annotation to a boolean value such as true or false, or remove it."},
		{Code: CodePlanNoPath, Area: "PLAN", Title: "No plannable HTTP path", Severity: SeverityWarning, Description: "An Ingress serves only spec.defaultBackend, or a rule has no HTTP path, so varco can plan no case for it.", Remediation: "Add an HTTP path with a backend, or probe the default backend manually."},
		{Code: CodePlanConfigMap, Area: "PLAN", Title: "Controller ConfigMap setting", Severity: SeverityWarning, Description: "An ingress-nginx controller ConfigMap setting can change observable request or response behavior and is not encoded in an Ingress manifest.", Remediation: "Compare the value on both controllers and verify the behavior against the ingress-nginx ConfigMap documentation."},
		{Code: CodePlanAnnotationsIgnored, Area: "PLAN", Title: "Annotations ignored by profile", Severity: SeverityWarning, Description: "The generic profile ignores controller annotations instead of interpreting their routing behavior.", Remediation: "Use the nginx profile when annotation-driven behavior is under test, or verify the annotations against the target controller."},
		{Code: CodePlanIngressClassExcluded, Area: "PLAN", Title: "Ingress excluded by class filter", Severity: SeverityWarning, Description: "An Ingress was excluded from the plan because its class does not match the requested one, so no case was planned for it.", Remediation: "Plan without the class filter, or with the class the Ingress declares. The exclusion is reported because it can change the plan: an excluded use-regex Ingress makes ingress-nginx match every path of its host as a regex, and the planner computes that host scope over the included resources only, so the change would stay invisible."},
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
