// Package policy decides which observed differences a run accepts.
//
// The comparator never decides; it reports every observed difference. This
// package is the separate policy layer of ADR 0007: it marks differences a
// profile or an exception permits as accepted while keeping them visible in
// the report with the reason and the exception id.
//
// The package is pure: it performs no I/O, reads no clock (the caller injects
// "now"), and keeps no package-level state. Exceptions-file parsing and
// validation produce VAR-CFG-0002 diagnostics.
package policy

import (
	"bytes"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/HarnageaGabriel/varco/internal/compare"
	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/model"
)

// SchemaVersion is the only exceptions-file version varco understands.
const SchemaVersion = 1

// ignoredFields are response fields the comparator leaves out of every case.
// They are reported once per run so nothing is invisible. Source: ADR 0006
// (Set-Cookie values) and docs/usage.md (volatile headers).
var ignoredFields = []string{"Date", "Server", "Via", "X-Request-Id", "Set-Cookie values"}

// IgnoredFields returns the built-in ignored field names, in a stable order.
func IgnoredFields() []string { return append([]string(nil), ignoredFields...) }

// Match selects the cases and differences an exception applies to. Every
// present field must match; an omitted field matches anything.
type Match struct {
	Host   string `yaml:"host"`
	Path   string `yaml:"path"`
	Family string `yaml:"family"`
	Case   string `yaml:"case"`
	Field  string `yaml:"field"`
}

// Expiry is an optional calendar date after which an exception stops applying.
type Expiry struct {
	Set  bool
	Date time.Time
}

// UnmarshalYAML accepts a YYYY-MM-DD scalar and rejects anything else.
func (e *Expiry) UnmarshalYAML(node *yaml.Node) error {
	if node.Tag == "!!null" {
		return nil
	}
	raw := strings.TrimSpace(node.Value)
	if raw == "" {
		return fmt.Errorf("expires is empty")
	}
	parsed, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return fmt.Errorf("expires %q is not a YYYY-MM-DD date", raw)
	}
	e.Set = true
	e.Date = parsed
	return nil
}

// Exception is one accepted difference, with its reason.
type Exception struct {
	ID      string `yaml:"id"`
	Reason  string `yaml:"reason"`
	Expires Expiry `yaml:"expires"`
	Match   Match  `yaml:"match"`
}

// File is a parsed and validated exceptions file.
type File struct {
	Version    int         `yaml:"version"`
	Exceptions []Exception `yaml:"exceptions"`
}

// Load parses and validates an exceptions file. Every problem is a
// VAR-CFG-0002 diagnostic so the failure is actionable.
func Load(data []byte) (File, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return File{}, configError("exceptions file is empty")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var file File
	if err := decoder.Decode(&file); err != nil {
		return File{}, configError(fmt.Sprintf("cannot parse exceptions file: %v", err))
	}
	if err := file.validate(); err != nil {
		return File{}, err
	}
	return file, nil
}

func (f File) validate() error {
	if f.Version != SchemaVersion {
		return configError(fmt.Sprintf("unsupported exceptions version %d (want %d)", f.Version, SchemaVersion))
	}
	seen := make(map[string]bool, len(f.Exceptions))
	for i, exception := range f.Exceptions {
		if exception.ID == "" {
			return configError(fmt.Sprintf("exception %d is missing a required id", i+1))
		}
		if seen[exception.ID] {
			return configError(fmt.Sprintf("duplicate exception id %q", exception.ID))
		}
		seen[exception.ID] = true
		if exception.Reason == "" {
			return configError(fmt.Sprintf("exception %q is missing a required reason", exception.ID))
		}
		if err := checkGlob(exception.Match.Host); err != nil {
			return configError(fmt.Sprintf("exception %q match.host: %v", exception.ID, err))
		}
		if err := checkGlob(exception.Match.Path); err != nil {
			return configError(fmt.Sprintf("exception %q match.path: %v", exception.ID, err))
		}
	}
	return nil
}

func checkGlob(pattern string) error {
	if pattern == "" {
		return nil
	}
	if _, err := path.Match(pattern, ""); err != nil {
		return fmt.Errorf("invalid glob %q", pattern)
	}
	return nil
}

func configError(message string) error {
	diagnostic, err := diag.New(diag.CodeExceptionsFile, message)
	if err != nil {
		return err
	}
	return &diag.Error{Diagnostic: diagnostic}
}

// ExpiredIDs returns the ids of exceptions whose expiry date is in the past at
// now, sorted and de-duplicated. Expired exceptions never accept a difference.
func (f File) ExpiredIDs(now time.Time) []string {
	ids := make([]string, 0, len(f.Exceptions))
	for _, exception := range f.Exceptions {
		if exception.expired(now) {
			ids = append(ids, exception.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

func (e Exception) expired(now time.Time) bool {
	if !e.Expires.Set {
		return false
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return today.After(e.Expires.Date)
}

// Evaluate applies the policy layer to one case's observed differences. The
// comparator's differences are all kept; those an exception accepts are marked
// accepted with the reason and the exception id.
func Evaluate(item model.ProbeCase, differences []compare.Difference, file File, now time.Time) model.Evaluation {
	evaluation := model.Evaluation{CaseID: item.ID, Outcome: model.OutcomeEquivalent, Differences: make([]model.Difference, 0, len(differences))}
	accepted := true
	for _, difference := range differences {
		converted := model.Difference{Code: difference.Code, Field: difference.Field, Old: difference.Old, New: difference.New}
		if exception, ok := file.accepting(item, difference, now); ok {
			converted.Accepted = true
			converted.Reason = exception.Reason
			converted.ExceptionID = exception.ID
		} else {
			accepted = false
		}
		evaluation.Differences = append(evaluation.Differences, converted)
	}
	if !accepted {
		evaluation.Outcome = model.OutcomeDifferent
	}
	return evaluation
}

func (f File) accepting(item model.ProbeCase, difference compare.Difference, now time.Time) (Exception, bool) {
	for _, exception := range f.Exceptions {
		if exception.expired(now) {
			continue
		}
		if exception.matches(item, difference) {
			return exception, true
		}
	}
	return Exception{}, false
}

func (e Exception) matches(item model.ProbeCase, difference compare.Difference) bool {
	if e.Match.Host != "" {
		if ok, _ := path.Match(e.Match.Host, item.Host); !ok {
			return false
		}
	}
	if e.Match.Path != "" {
		if ok, _ := path.Match(e.Match.Path, item.Path); !ok {
			return false
		}
	}
	if e.Match.Family != "" && e.Match.Family != item.Family {
		return false
	}
	if e.Match.Case != "" && e.Match.Case != item.ID {
		return false
	}
	if e.Match.Field != "" && !fieldMatch(e.Match.Field, difference.Field) {
		return false
	}
	return true
}

// fieldMatch matches the match.field value against a comparator field name. The
// exact comparator name always matches, and the documented shorthand forms map
// onto it so an operator can write `header:Server` or `location`.
func fieldMatch(pattern, field string) bool {
	if pattern == field {
		return true
	}
	if name, ok := strings.CutPrefix(pattern, "header:"); ok {
		return strings.EqualFold(field, "header."+name)
	}
	switch pattern {
	case "location":
		return field == "header.location"
	case "cookie", "set-cookie":
		return field == "header.set-cookie"
	case "tls-version":
		return field == "tls.version"
	case "certificate":
		return strings.HasPrefix(field, "certificate")
	}
	return false
}
