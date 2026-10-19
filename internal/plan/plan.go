// Package plan derives deterministic probe cases from Ingress resources.
//
// The package is pure: it performs no I/O, reads no clock, and keeps no
// package-level state. The same resources in the same order always produce the
// same plan. What varco cannot test is reported as a finding, never guessed.
//
// The case families and their sources are documented in
// docs/adr/0005-plan-case-families.md; the ingress-nginx behaviors they probe
// come from
// https://kubernetes.io/blog/2026/02/27/ingress-nginx-before-you-migrate.
package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/ingress"
)

// Header is one request header of a case. Values are literals chosen by varco
// and never contain credentials.
type Header struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

// Source records where a case came from: the Ingress identity, the rule and
// path indexes inside it, and the annotation that triggered it, if any.
type Source struct {
	Ingress    string `json:"ingress"`
	Rule       int    `json:"rule"`
	Path       int    `json:"path"`
	Annotation string `json:"annotation,omitempty"`
}

// Case is one probe varco can send later: one request target for one host.
type Case struct {
	ID      string   `json:"id"`
	Host    string   `json:"host"`
	SNI     string   `json:"sni,omitempty"`
	Scheme  string   `json:"scheme"`
	Method  string   `json:"method"`
	Path    string   `json:"path"`
	Headers []Header `json:"headers,omitempty"`
	Family  string   `json:"family"`
	Source  Source   `json:"source"`
	Note    string   `json:"note,omitempty"`
}

// Finding is something varco cannot plan or test, with the reason why.
type Finding struct {
	Code       diag.Code `json:"code"`
	Ingress    string    `json:"ingress"`
	Annotation string    `json:"annotation,omitempty"`
	Path       string    `json:"path,omitempty"`
	Message    string    `json:"message"`
}

// Warning qualifies the generated cases, for example host-wide regex scope.
type Warning struct {
	Code    diag.Code `json:"code"`
	Ingress string    `json:"ingress,omitempty"`
	Message string    `json:"message"`
}

// Plan is the complete result: the cases to run, what could not be planned,
// and the warnings that qualify the cases.
type Plan struct {
	Profile     string
	Cases       []Case
	Unsupported []Finding
	Warnings    []Warning
}

// Options configures plan generation.
type Options struct {
	// IngressClass plans only Ingress resources of this class; empty plans every class.
	IngressClass string
	// DefaultHost is used for rules that declare no host; empty leaves them unplanned.
	DefaultHost string
	// ControllerConfig is the ingress-nginx controller ConfigMap data used by
	// ConfigMapWarnings when the CLI loads a cluster.
	ControllerConfig map[string]string
	// Profile selects the planner behavior: nginx (default) or generic.
	Profile string
}

// Build derives cases and findings from parsed Ingress resources. Resources are
// processed in the given order, which makes the output deterministic.
func Build(resources []ingress.Ingress, options Options) Plan {
	builder := newBuilder(options)
	builder.run(resources)
	return builder.plan.finalize()
}

// finalize assigns case IDs, de-duplicates identical cases, and sorts every
// list so no output order can depend on map iteration.
func (p Plan) finalize() Plan {
	return Plan{
		Profile:     p.Profile,
		Cases:       finalizeCases(p.Cases),
		Unsupported: finalizeFindings(p.Unsupported),
		Warnings:    finalizeWarnings(p.Warnings),
	}
}

func finalizeCases(cases []Case) []Case {
	seen := make(map[string]bool, len(cases))
	unique := make([]Case, 0, len(cases))
	for _, item := range cases {
		item.ID = caseID(item)
		if seen[item.ID] {
			continue
		}
		seen[item.ID] = true
		unique = append(unique, item)
	}
	sort.Slice(unique, func(i, j int) bool { return lessCase(unique[i], unique[j]) })
	return unique
}

// lessCase orders cases by host, path, scheme, method, family, then ID.
func lessCase(a, b Case) bool {
	if a.Host != b.Host {
		return a.Host < b.Host
	}
	if a.Path != b.Path {
		return a.Path < b.Path
	}
	if a.Scheme != b.Scheme {
		return a.Scheme < b.Scheme
	}
	if a.Method != b.Method {
		return a.Method < b.Method
	}
	if a.Family != b.Family {
		return a.Family < b.Family
	}
	return a.ID < b.ID
}

// caseID is the first 12 hex characters of the SHA-256 of the canonical case:
// identical cases always share an ID, and an ID never depends on the order the
// case was generated in.
func caseID(item Case) string {
	canonical := struct {
		Host    string   `json:"host"`
		SNI     string   `json:"sni"`
		Scheme  string   `json:"scheme"`
		Method  string   `json:"method"`
		Path    string   `json:"path"`
		Headers []Header `json:"headers"`
		Family  string   `json:"family"`
		Source  Source   `json:"source"`
		Note    string   `json:"note"`
	}{
		Host:    item.Host,
		SNI:     item.SNI,
		Scheme:  item.Scheme,
		Method:  item.Method,
		Path:    item.Path,
		Headers: sortedHeaders(item.Headers),
		Family:  item.Family,
		Source:  item.Source,
		Note:    item.Note,
	}
	encoded, _ := json.Marshal(canonical)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])[:12]
}

// sortedHeaders returns the headers ordered by name, so the canonical form of
// a case does not depend on the order headers were added in.
func sortedHeaders(headers []Header) []Header {
	if len(headers) == 0 {
		return nil
	}
	ordered := append([]Header(nil), headers...)
	for index := range ordered {
		ordered[index].Values = append([]string(nil), ordered[index].Values...)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Name < ordered[j].Name })
	return ordered
}

func finalizeFindings(findings []Finding) []Finding {
	ordered := append([]Finding(nil), findings...)
	sort.Slice(ordered, func(i, j int) bool { return lessFinding(ordered[i], ordered[j]) })
	deduped := make([]Finding, 0, len(ordered))
	seen := make(map[string]bool, len(ordered))
	for _, finding := range ordered {
		key := string(finding.Code) + "\x00" + finding.Ingress + "\x00" + finding.Annotation + "\x00" + finding.Path + "\x00" + finding.Message
		if seen[key] {
			continue
		}
		seen[key] = true
		deduped = append(deduped, finding)
	}
	return deduped
}

// lessFinding orders findings by code, Ingress, annotation, path, then message.
func lessFinding(a, b Finding) bool {
	for _, pair := range [][2]string{
		{string(a.Code), string(b.Code)},
		{a.Ingress, b.Ingress},
		{a.Annotation, b.Annotation},
		{a.Path, b.Path},
		{a.Message, b.Message},
	} {
		if pair[0] != pair[1] {
			return pair[0] < pair[1]
		}
	}
	return false
}

func finalizeWarnings(warnings []Warning) []Warning {
	ordered := append([]Warning(nil), warnings...)
	sort.Slice(ordered, func(i, j int) bool { return lessWarning(ordered[i], ordered[j]) })
	deduped := make([]Warning, 0, len(ordered))
	seen := make(map[string]bool, len(ordered))
	for _, warning := range ordered {
		key := string(warning.Code) + "\x00" + warning.Ingress + "\x00" + warning.Message
		if seen[key] {
			continue
		}
		seen[key] = true
		deduped = append(deduped, warning)
	}
	return deduped
}

// lessWarning orders warnings by code, Ingress, then message.
func lessWarning(a, b Warning) bool {
	if a.Code != b.Code {
		return a.Code < b.Code
	}
	if a.Ingress != b.Ingress {
		return a.Ingress < b.Ingress
	}
	return a.Message < b.Message
}
