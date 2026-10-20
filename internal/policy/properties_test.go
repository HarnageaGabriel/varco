package policy_test

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/HarnageaGabriel/varco/internal/compare"
	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/model"
	"github.com/HarnageaGabriel/varco/internal/policy"
)

// propertySeed is the fixed seed of every generated case, so a failure is
// reproducible and the suite never flakes (CONVENTIONS.md: same input, same
// output).
const propertySeed int64 = 20261020

// propertyShuffles is the number of shuffles the order-independence property
// runs for every generated case.
const propertyShuffles = 50

// propertyNow is the injected clock every property test uses. The package
// never reads the wall clock (CONVENTIONS.md: time only through an injected
// clock; internal/policy/policy.go:8-10).
var propertyNow = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

// propertyHosts are the request hosts of the generated cases.
var propertyHosts = []string{
	"api.example.com",
	"app.example.com",
	"shop.example.com",
	"api.example.com:8443",
	"other.test",
}

// propertyPaths are the request paths of the generated cases.
var propertyPaths = []string{
	"/",
	"/api",
	"/api/v1",
	"/api/v1/items",
	"/api/v1/items/42",
	"/healthz",
}

// propertyFamilies are the case families of the generated cases
// (docs/adr/0005-plan-case-families.md).
var propertyFamilies = []string{"base", "trailing-slash", "prefix-boundary", "rewrite"}

// propertyFields are the comparator field names (docs/run.md:166-169).
var propertyFields = []string{
	"status",
	"header.location",
	"header.set-cookie",
	"header.server",
	"header.x-frame-options",
	"tls.version",
	"certificate.subject",
	"body",
}

// propertyRand returns the generator of one case. Every case gets its own
// stream, so adding a case never changes the values of the other cases.
func propertyRand(caseIndex int) *rand.Rand {
	return rand.New(rand.NewSource(propertySeed + int64(caseIndex)))
}

// propertyHostGlob returns a host glob or an empty string, which matches every
// host (docs/run.md:141: an omitted field matches anything).
func propertyHostGlob(rnd *rand.Rand) string {
	switch rnd.Intn(4) {
	case 0:
		return ""
	case 1:
		return "*.example.com"
	case 2:
		return propertyHosts[rnd.Intn(len(propertyHosts))]
	default:
		return "api.example.com"
	}
}

// propertyPathGlob returns a path glob or an empty string, which matches every
// path (docs/run.md:141).
func propertyPathGlob(rnd *rand.Rand) string {
	switch rnd.Intn(4) {
	case 0:
		return ""
	case 1:
		return "/api/*"
	case 2:
		return "/api/v1*"
	default:
		return propertyPaths[rnd.Intn(len(propertyPaths))]
	}
}

// propertyFamily returns a case family or an empty string.
func propertyFamily(rnd *rand.Rand) string {
	if rnd.Intn(3) == 0 {
		return ""
	}
	return propertyFamilies[rnd.Intn(len(propertyFamilies))]
}

// propertyCaseID returns a case id or an empty string.
func propertyCaseID(rnd *rand.Rand) string {
	if rnd.Intn(3) == 0 {
		return fmt.Sprintf("case-%d", rnd.Intn(3))
	}
	return ""
}

// propertyFieldPattern returns a comparator field name or one of the
// documented shorthands (docs/run.md:152-164).
func propertyFieldPattern(rnd *rand.Rand) string {
	switch rnd.Intn(5) {
	case 0:
		return ""
	case 1:
		return "header:Server"
	case 2:
		return "location"
	case 3:
		return "status"
	default:
		return propertyFields[rnd.Intn(len(propertyFields))]
	}
}

// propertyExpiryDate returns a date around the injected clock, so the
// generated files hold both expired and live exceptions.
func propertyExpiryDate(rnd *rand.Rand) time.Time {
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, rnd.Intn(400)-200)
}

// propertyException builds one exception. Every match field is left empty
// sometimes, because an omitted field matches anything (docs/run.md:141).
func propertyException(rnd *rand.Rand, index int) policy.Exception {
	exception := policy.Exception{
		ID:     fmt.Sprintf("exc-%d", index),
		Reason: fmt.Sprintf("reason %d", index),
		Match: policy.Match{
			Host:   propertyHostGlob(rnd),
			Path:   propertyPathGlob(rnd),
			Family: propertyFamily(rnd),
			Case:   propertyCaseID(rnd),
			Field:  propertyFieldPattern(rnd),
		},
	}
	if rnd.Intn(3) == 0 {
		exception.Expires = policy.Expiry{Set: true, Date: propertyExpiryDate(rnd)}
	}
	return exception
}

// propertyExceptions builds the exceptions of one generated case.
func propertyExceptions(rnd *rand.Rand) []policy.Exception {
	count := 1 + rnd.Intn(5)
	exceptions := make([]policy.Exception, 0, count)
	for index := range count {
		exceptions = append(exceptions, propertyException(rnd, index))
	}
	return exceptions
}

// propertyCase is one probe case with the differences the comparator reported.
type propertyCase struct {
	item        model.ProbeCase
	differences []compare.Difference
}

// propertyCases builds the probe cases and their observed differences.
func propertyCases(rnd *rand.Rand) []propertyCase {
	count := 1 + rnd.Intn(4)
	cases := make([]propertyCase, 0, count)
	for index := range count {
		item := model.ProbeCase{
			ID:     fmt.Sprintf("case-%d", index),
			Host:   propertyHosts[rnd.Intn(len(propertyHosts))],
			Path:   propertyPaths[rnd.Intn(len(propertyPaths))],
			Family: propertyFamilies[rnd.Intn(len(propertyFamilies))],
			Scheme: "https",
			Method: "GET",
		}
		fields := slices.Clone(propertyFields)
		rnd.Shuffle(len(fields), func(i, j int) {
			fields[i], fields[j] = fields[j], fields[i]
		})
		differences := make([]compare.Difference, 0, 3)
		for _, field := range fields[:1+rnd.Intn(3)] {
			differences = append(differences, compare.Difference{Code: diag.CodeCompareStatus, Field: field, Old: "200", New: "404"})
		}
		cases = append(cases, propertyCase{item: item, differences: differences})
	}
	return cases
}

// propertyUnstableFields builds unique (host, field) pairs, the pairs a run
// reports as unstable (internal/engine/run.go:330-353).
func propertyUnstableFields(rnd *rand.Rand) []model.UnstableField {
	count := 1 + rnd.Intn(4)
	fields := make([]model.UnstableField, 0, count)
	seen := make(map[string]bool, count)
	for len(fields) < count {
		host := propertyHosts[rnd.Intn(len(propertyHosts))]
		field := propertyFields[rnd.Intn(len(propertyFields))]
		key := host + "|" + field
		if seen[key] {
			continue
		}
		seen[key] = true
		fields = append(fields, model.UnstableField{Host: host, Field: field})
	}
	return fields
}

// propertyFile builds an exceptions file from a list of exceptions.
func propertyFile(exceptions []policy.Exception) policy.File {
	return policy.File{Version: policy.SchemaVersion, Exceptions: exceptions}
}

// propertyStatusDifference builds one status difference on a field.
func propertyStatusDifference(field string) compare.Difference {
	return compare.Difference{Code: diag.CodeCompareStatus, Field: field, Old: "200", New: "404"}
}

// propertyAccepted returns the sorted keys of the accepted differences of every
// case: the case id and the field name. Two runs accept the same differences
// exactly when their key lists are equal.
func propertyAccepted(t *testing.T, cases []propertyCase, file policy.File, now time.Time) []string {
	t.Helper()
	keys := make([]string, 0, len(cases))
	for _, item := range cases {
		evaluation := policy.Evaluate(item.item, item.differences, file, now, model.Unstable{})
		for _, difference := range evaluation.Differences {
			if difference.Accepted {
				keys = append(keys, item.item.ID+"|"+difference.Field)
			}
		}
	}
	slices.Sort(keys)
	return keys
}

// propertyOutcomes returns the outcome of every case, in case order.
func propertyOutcomes(cases []propertyCase, file policy.File, now time.Time) []string {
	outcomes := make([]string, 0, len(cases))
	for _, item := range cases {
		evaluation := policy.Evaluate(item.item, item.differences, file, now, model.Unstable{})
		outcomes = append(outcomes, item.item.ID+"="+string(evaluation.Outcome))
	}
	return outcomes
}

// propertyAccepts reports whether the file accepts the difference on the case.
func propertyAccepts(file policy.File, item model.ProbeCase, difference compare.Difference, now time.Time) bool {
	evaluation := policy.Evaluate(item, []compare.Difference{difference}, file, now, model.Unstable{})
	return evaluation.Differences[0].Accepted
}

// propertyMatchesAlone reports whether the exception accepts the difference
// when it is the only exception in the file. The evaluator is the oracle, so
// the test never repeats the matching rules of the package.
func propertyMatchesAlone(exception policy.Exception, item model.ProbeCase, difference compare.Difference, now time.Time) bool {
	return propertyAccepts(propertyFile([]policy.Exception{exception}), item, difference, now)
}

// propertyExpired reports whether the exception is expired at the injected
// clock. The package's own expiry report is the oracle
// (internal/policy/policy.go:157-166).
func propertyExpired(exception policy.Exception, now time.Time) bool {
	file := propertyFile([]policy.Exception{exception})
	return slices.Contains(file.ExpiredIDs(now), exception.ID)
}

// propertyWinner returns the id of the exception that accepts the difference
// in the given file order: the first exception that is not expired and matches
// the difference (internal/policy/policy.go:253-263).
func propertyWinner(exceptions []policy.Exception, item model.ProbeCase, difference compare.Difference, now time.Time) string {
	for _, exception := range exceptions {
		if propertyExpired(exception, now) {
			continue
		}
		if propertyMatchesAlone(exception, item, difference, now) {
			return exception.ID
		}
	}
	return ""
}

// TestPropertyOrderIndependence checks that the accepted differences, the
// per-case outcomes and the expired-exception report do not depend on the
// order of the exceptions in the file (CONVENTIONS.md: same input, same
// output; docs/architecture.md:247). It shuffles the exceptions 50 times and
// checks the winner rule for every difference: the first exception in file
// order that matches it accepts it (internal/policy/policy.go:253-263).
func TestPropertyOrderIndependence(t *testing.T) {
	for caseIndex := range 50 {
		rnd := propertyRand(caseIndex)
		cases := propertyCases(rnd)
		exceptions := propertyExceptions(rnd)
		base := propertyFile(exceptions)
		wantAccepted := propertyAccepted(t, cases, base, propertyNow)
		wantOutcomes := propertyOutcomes(cases, base, propertyNow)
		wantExpired := base.ExpiredIDs(propertyNow)
		for shuffle := range propertyShuffles {
			shuffled := slices.Clone(exceptions)
			rnd.Shuffle(len(shuffled), func(i, j int) {
				shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
			})
			file := propertyFile(shuffled)
			if got := propertyAccepted(t, cases, file, propertyNow); !slices.Equal(got, wantAccepted) {
				t.Fatalf("case %d shuffle %d: accepted %v, want %v", caseIndex, shuffle, got, wantAccepted)
			}
			if got := propertyOutcomes(cases, file, propertyNow); !slices.Equal(got, wantOutcomes) {
				t.Fatalf("case %d shuffle %d: outcomes %v, want %v", caseIndex, shuffle, got, wantOutcomes)
			}
			if got := file.ExpiredIDs(propertyNow); !slices.Equal(got, wantExpired) {
				t.Fatalf("case %d shuffle %d: expired exceptions %v, want %v", caseIndex, shuffle, got, wantExpired)
			}
			for _, item := range cases {
				for _, difference := range item.differences {
					winner := propertyWinner(shuffled, item.item, difference, propertyNow)
					evaluation := policy.Evaluate(item.item, []compare.Difference{difference}, file, propertyNow, model.Unstable{})
					got := evaluation.Differences[0]
					if winner == "" {
						if got.Accepted {
							t.Fatalf("case %d shuffle %d: %q was accepted by %q, want no exception", caseIndex, shuffle, difference.Field, got.ExceptionID)
						}
						continue
					}
					if !got.Accepted || got.ExceptionID != winner {
						t.Fatalf("case %d shuffle %d: %q accepted by %q, want %q", caseIndex, shuffle, difference.Field, got.ExceptionID, winner)
					}
				}
			}
		}
	}
}

// TestPropertyWinnerRule checks which exception accepts a difference when two
// of them match it: the first one in file order
// (internal/policy/policy.go:253-263). An expired exception never wins, even
// when it comes first (internal/policy/policy.go:255-257).
func TestPropertyWinnerRule(t *testing.T) {
	item := model.ProbeCase{ID: "case-0", Host: "api.example.com", Path: "/api/v1", Family: "base"}
	difference := propertyStatusDifference("status")
	first := policy.Exception{ID: "first", Reason: "reason of the first", Match: policy.Match{Host: "api.example.com"}}
	second := policy.Exception{ID: "second", Reason: "reason of the second", Match: policy.Match{Host: "*.example.com"}}
	for _, exception := range []policy.Exception{first, second} {
		if !propertyMatchesAlone(exception, item, difference, propertyNow) {
			t.Fatalf("exception %q does not match the difference", exception.ID)
		}
	}
	for _, order := range [][]policy.Exception{{first, second}, {second, first}} {
		evaluation := policy.Evaluate(item, []compare.Difference{difference}, propertyFile(order), propertyNow, model.Unstable{})
		got := evaluation.Differences[0]
		if !got.Accepted || got.ExceptionID != order[0].ID || got.Reason != order[0].Reason {
			t.Fatalf("order %s: accepted by %q with reason %q, want %q with reason %q", propertyIDs(order), got.ExceptionID, got.Reason, order[0].ID, order[0].Reason)
		}
	}
	expired := policy.Exception{
		ID:      "expired",
		Reason:  "reason of the expired exception",
		Expires: policy.Expiry{Set: true, Date: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)},
		Match:   policy.Match{Host: "api.example.com"},
	}
	evaluation := policy.Evaluate(item, []compare.Difference{difference}, propertyFile([]policy.Exception{expired, second}), propertyNow, model.Unstable{})
	if got := evaluation.Differences[0].ExceptionID; got != second.ID {
		t.Fatalf("exception id = %q, want %q", got, second.ID)
	}
}

// propertyIDs lists the ids of a list of exceptions, for a failure message.
func propertyIDs(exceptions []policy.Exception) []string {
	ids := make([]string, 0, len(exceptions))
	for _, exception := range exceptions {
		ids = append(ids, exception.ID)
	}
	return ids
}

// TestPropertyMonotonicity checks that adding an exception never turns an
// accepted difference back into a not-accepted one and that removing an
// exception never accepts more (internal/policy/policy.go:197: a difference is
// accepted when an exception matches it; docs/run.md:149: an accepted
// difference is never removed).
func TestPropertyMonotonicity(t *testing.T) {
	for caseIndex := range 50 {
		rnd := propertyRand(caseIndex)
		cases := propertyCases(rnd)
		exceptions := propertyExceptions(rnd)
		baseAccepted := propertyAccepted(t, cases, propertyFile(exceptions), propertyNow)
		baseOutcomes := propertyOutcomes(cases, propertyFile(exceptions), propertyNow)

		added := propertyFile(append(slices.Clone(exceptions), policy.Exception{ID: "added", Reason: "reason of the added exception"}))
		addedAccepted := propertyAccepted(t, cases, added, propertyNow)
		for _, key := range baseAccepted {
			if !slices.Contains(addedAccepted, key) {
				t.Fatalf("case %d: adding an exception un-accepted %q", caseIndex, key)
			}
		}
		addedOutcomes := propertyOutcomes(cases, added, propertyNow)
		for index, outcome := range baseOutcomes {
			if outcome == string(model.OutcomeEquivalent) && addedOutcomes[index] != string(model.OutcomeEquivalent) {
				t.Fatalf("case %d: adding an exception turned %q into %q", caseIndex, outcome, addedOutcomes[index])
			}
		}

		removed := slices.Clone(exceptions)
		index := rnd.Intn(len(removed))
		removed = append(removed[:index], removed[index+1:]...)
		removedAccepted := propertyAccepted(t, cases, propertyFile(removed), propertyNow)
		for _, key := range removedAccepted {
			if !slices.Contains(baseAccepted, key) {
				t.Fatalf("case %d: removing an exception accepted %q", caseIndex, key)
			}
		}
	}
}

// TestPropertyNarrowerExceptionIsNeverBroader checks that an exception with a
// narrower match never accepts a difference the broader one does not
// (docs/run.md:141-146: every field that is present must match, so the
// narrower match is a subset of the broader one).
func TestPropertyNarrowerExceptionIsNeverBroader(t *testing.T) {
	broad := policy.Exception{ID: "broad", Reason: "reason of the broad exception", Match: policy.Match{Host: "*.example.com"}}
	narrow := policy.Exception{ID: "narrow", Reason: "reason of the narrow exception", Match: policy.Match{Host: "api.example.com", Path: "/api/*", Field: "status"}}
	items := []model.ProbeCase{
		{ID: "case-0", Host: "api.example.com", Path: "/api/v1", Family: "base"},
		{ID: "case-1", Host: "api.example.com", Path: "/api/v1/items", Family: "base"},
		{ID: "case-2", Host: "app.example.com", Path: "/api/v1", Family: "base"},
		{ID: "case-3", Host: "other.test", Path: "/api/v1", Family: "base"},
	}
	exercised := false
	for _, item := range items {
		for _, field := range []string{"status", "header.server"} {
			difference := propertyStatusDifference(field)
			narrowAccepted := propertyAccepts(propertyFile([]policy.Exception{narrow}), item, difference, propertyNow)
			broadAccepted := propertyAccepts(propertyFile([]policy.Exception{broad}), item, difference, propertyNow)
			if narrowAccepted && !broadAccepted {
				t.Fatalf("the narrow exception accepted %q on %q, the broad one did not", field, item.Host)
			}
			if narrowAccepted {
				exercised = true
			}
		}
	}
	if !exercised {
		t.Fatal("the narrow exception accepted nothing, so the property was not exercised")
	}
}

// TestPropertyExpiryBoundary checks the expiry rule on the expiry date
// itself, the day before and the day after, with the injected clock only
// (docs/run.md:139-140: after the date passes the exception is ignored and
// listed under expired_exceptions; internal/policy/policy.go:168-174: an
// exception is expired once the injected date is past its expiry date, so the
// expiry date itself still applies).
func TestPropertyExpiryBoundary(t *testing.T) {
	item := model.ProbeCase{ID: "case-0", Host: "api.example.com", Path: "/api/v1", Family: "base"}
	difference := propertyStatusDifference("status")
	expiries := []time.Time{
		time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
		time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC),
	}
	for _, expiry := range expiries {
		exception := policy.Exception{
			ID:      "expiring",
			Reason:  "reason of the expiring exception",
			Expires: policy.Expiry{Set: true, Date: expiry},
			Match:   policy.Match{Field: "status"},
		}
		file := propertyFile([]policy.Exception{exception})
		clocks := []struct {
			name    string
			now     time.Time
			expired bool
		}{
			{"the day before", expiry.AddDate(0, 0, -1), false},
			{"the expiry date at midnight", expiry, false},
			{"the expiry date at the end of the day", expiry.Add(24*time.Hour - time.Nanosecond), false},
			{"the day after", expiry.AddDate(0, 0, 1), true},
		}
		for _, clock := range clocks {
			evaluation := policy.Evaluate(item, []compare.Difference{difference}, file, clock.now, model.Unstable{})
			got := evaluation.Differences[0]
			if got.Accepted == clock.expired {
				t.Fatalf("expiry %s, %s: accepted = %v, want %v", expiry.Format("2006-01-02"), clock.name, got.Accepted, !clock.expired)
			}
			wantOutcome := model.OutcomeEquivalent
			if clock.expired {
				wantOutcome = model.OutcomeDifferent
			}
			if evaluation.Outcome != wantOutcome {
				t.Fatalf("expiry %s, %s: outcome = %s, want %s", expiry.Format("2006-01-02"), clock.name, evaluation.Outcome, wantOutcome)
			}
			ids := file.ExpiredIDs(clock.now)
			if slices.Contains(ids, exception.ID) != clock.expired {
				t.Fatalf("expiry %s, %s: expired exceptions = %v", expiry.Format("2006-01-02"), clock.name, ids)
			}
		}
	}
}

// TestPropertyExpiredIDsAreSorted checks that the expired exceptions are
// listed in a stable order whatever the order of the file
// (docs/architecture.md:247: ExpiredIDs sorts the expired ids).
func TestPropertyExpiredIDsAreSorted(t *testing.T) {
	exceptions := []policy.Exception{
		{ID: "zulu", Reason: "r", Expires: policy.Expiry{Set: true, Date: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}},
		{ID: "alpha", Reason: "r", Expires: policy.Expiry{Set: true, Date: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}},
		{ID: "mike", Reason: "r", Expires: policy.Expiry{Set: true, Date: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}},
		{ID: "live", Reason: "r", Expires: policy.Expiry{Set: true, Date: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}},
	}
	ids := propertyFile(exceptions).ExpiredIDs(propertyNow)
	want := []string{"alpha", "mike", "zulu"}
	if !slices.Equal(ids, want) {
		t.Fatalf("expired exceptions = %v, want %v", ids, want)
	}
}

// TestPropertyGlobSemantics checks that the host and path globs use path.Match
// semantics, so a star does not cross a slash (docs/run.md:142-143 and
// docs/run.md:113-115).
func TestPropertyGlobSemantics(t *testing.T) {
	difference := propertyStatusDifference("status")
	tests := []struct {
		name string
		host string
		path string
		item model.ProbeCase
		want bool
	}{
		{"a star matches one path segment", "", "/api/*", model.ProbeCase{Path: "/api/v1"}, true},
		{"a star does not cross a slash", "", "/api/*", model.ProbeCase{Path: "/api/v1/items"}, false},
		{"a star needs the whole segment", "", "/api/*", model.ProbeCase{Path: "/api"}, false},
		{"a question mark matches one character", "", "/api/?", model.ProbeCase{Path: "/api/x"}, true},
		{"a question mark matches exactly one character", "", "/api/?", model.ProbeCase{Path: "/api/xy"}, false},
		{"a character class matches one of its characters", "", "/api/[ab]", model.ProbeCase{Path: "/api/a"}, true},
		{"a character class rejects the other characters", "", "/api/[ab]", model.ProbeCase{Path: "/api/c"}, false},
		{"a host star matches the host", "*.example.com", "", model.ProbeCase{Host: "api.example.com"}, true},
		{"a host star rejects another host", "*.example.com", "", model.ProbeCase{Host: "other.test"}, false},
		{"an exact path matches itself", "", "/api/v1", model.ProbeCase{Path: "/api/v1"}, true},
		{"an exact path rejects another path", "", "/api/v1", model.ProbeCase{Path: "/api/v2"}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exception := policy.Exception{ID: "glob", Reason: "r", Match: policy.Match{Host: test.host, Path: test.path}}
			file := propertyFile([]policy.Exception{exception})
			if got := propertyAccepts(file, test.item, difference, propertyNow); got != test.want {
				t.Fatalf("host %q path %q on %q %q: accepted = %v, want %v", test.host, test.path, test.item.Host, test.item.Path, got, test.want)
			}
		})
	}
}

// TestPropertyInvalidGlobIsRejected checks that an invalid glob is refused at
// load with the documented diagnostic (docs/run.md:131-132: every problem in
// an exceptions file is VAR-CFG-0002; internal/policy/policy.go:137-145).
func TestPropertyInvalidGlobIsRejected(t *testing.T) {
	tests := []struct {
		name     string
		document string
	}{
		{"an unclosed host class", "version: 1\nexceptions:\n  - id: a\n    reason: r\n    match:\n      host: '['\n"},
		{"an unclosed path class", "version: 1\nexceptions:\n  - id: a\n    reason: r\n    match:\n      path: 'a[b'\n"},
		{"a dangling escape", "version: 1\nexceptions:\n  - id: a\n    reason: r\n    match:\n      host: 'a\\'\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := policy.Load([]byte(test.document))
			if err == nil {
				t.Fatal("expected an error")
			}
			var diagnosticError *diag.Error
			if !errors.As(err, &diagnosticError) {
				t.Fatalf("error %v is not a diagnostic", err)
			}
			if diagnosticError.Diagnostic.Code != diag.CodeExceptionsFile {
				t.Fatalf("code = %s, want %s", diagnosticError.Diagnostic.Code, diag.CodeExceptionsFile)
			}
			if !strings.Contains(diagnosticError.Diagnostic.Message, "invalid glob") {
				t.Fatalf("message %q does not name the invalid glob", diagnosticError.Diagnostic.Message)
			}
		})
	}
}

// TestPropertyFieldShorthands checks that the documented shorthands resolve to
// the exact comparator field and that a header name compares case
// insensitively (docs/run.md:152-169; internal/policy/policy.go:291-309).
func TestPropertyFieldShorthands(t *testing.T) {
	item := model.ProbeCase{ID: "case-0", Host: "api.example.com", Path: "/api/v1", Family: "base"}
	tests := []struct {
		name    string
		pattern string
		field   string
		want    bool
	}{
		{"status matches the status", "status", "status", true},
		{"status matches nothing else", "status", "status.code", false},
		{"location matches the location header", "location", "header.location", true},
		{"location matches no other header", "location", "header.set-cookie", false},
		{"cookie matches the set-cookie header", "cookie", "header.set-cookie", true},
		{"cookie matches no other header", "cookie", "header.location", false},
		{"set-cookie matches the set-cookie header", "set-cookie", "header.set-cookie", true},
		{"tls-version matches the tls version", "tls-version", "tls.version", true},
		{"tls-version matches no other tls field", "tls-version", "tls.version.name", false},
		{"certificate matches any certificate field", "certificate", "certificate.subject", true},
		{"certificate matches another certificate field", "certificate", "certificate.issuer", true},
		{"certificate matches no other field", "certificate", "status", false},
		{"a header name matches the lowercased field", "header:Server", "header.server", true},
		{"a header name matches in any case", "header:SERVER", "header.server", true},
		{"a header name matches in any case of the name", "header:Strict-Transport-Security", "header.strict-transport-security", true},
		{"a header name matches no other header", "header:Server", "header.set-cookie", false},
		{"a header name matches no longer field", "header:Server", "header.serverx", false},
		{"the exact comparator field matches itself", "header.server", "header.server", true},
		{"the exact comparator field matches no longer field", "header.server", "header.serverx", false},
		{"an unknown pattern matches nothing", "not-a-field", "status", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exception := policy.Exception{ID: "field", Reason: "r", Match: policy.Match{Field: test.pattern}}
			file := propertyFile([]policy.Exception{exception})
			if got := propertyAccepts(file, item, propertyStatusDifference(test.field), propertyNow); got != test.want {
				t.Fatalf("pattern %q on field %q: accepted = %v, want %v", test.pattern, test.field, got, test.want)
			}
		})
	}
}

// TestPropertyExactFamilyAndCaseMatch checks that the family and the case id
// of a match are exact, not globs (docs/run.md:144-145: an exact case family
// and an exact case id).
func TestPropertyExactFamilyAndCaseMatch(t *testing.T) {
	difference := propertyStatusDifference("status")
	tests := []struct {
		name  string
		match policy.Match
		item  model.ProbeCase
		want  bool
	}{
		{"the family matches itself", policy.Match{Family: "base"}, model.ProbeCase{ID: "case-0", Family: "base"}, true},
		{"the family matches no other family", policy.Match{Family: "base"}, model.ProbeCase{ID: "case-0", Family: "trailing-slash"}, false},
		{"the case id matches itself", policy.Match{Case: "case-0"}, model.ProbeCase{ID: "case-0", Family: "base"}, true},
		{"the case id matches no other case", policy.Match{Case: "case-0"}, model.ProbeCase{ID: "case-1", Family: "base"}, false},
		{"an empty family matches every family", policy.Match{}, model.ProbeCase{ID: "case-0", Family: "trailing-slash"}, true},
		{"an empty case id matches every case", policy.Match{}, model.ProbeCase{ID: "case-7", Family: "base"}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exception := policy.Exception{ID: "exact", Reason: "r", Match: test.match}
			file := propertyFile([]policy.Exception{exception})
			if got := propertyAccepts(file, test.item, difference, propertyNow); got != test.want {
				t.Fatalf("match %+v on %q: accepted = %v, want %v", test.match, test.item.ID, got, test.want)
			}
		})
	}
}

// TestPropertyAutoUnstableException checks the automatic acceptance of a
// difference on an unstable field: it is accepted with the exception id
// auto:unstable and the documented reason, and the automatic acceptance wins
// over an exception file entry (docs/run.md:75-81;
// internal/policy/policy.go:35 and internal/policy/policy.go:193-201).
func TestPropertyAutoUnstableException(t *testing.T) {
	item := model.ProbeCase{ID: "case-0", Host: "api.example.com", Path: "/api/v1", Family: "base"}
	unstableField := "header.x-frame-options"
	differences := []compare.Difference{
		propertyStatusDifference(unstableField),
		propertyStatusDifference("status"),
	}
	unstable := model.Unstable{Old: []string{unstableField}, New: []string{unstableField}}
	evaluation := policy.Evaluate(item, differences, policy.File{}, propertyNow, unstable)
	accepted := evaluation.Differences[0]
	if !accepted.Accepted || accepted.ExceptionID != policy.UnstableExceptionID {
		t.Fatalf("unstable difference = %+v", accepted)
	}
	if accepted.Reason != "unstable: differs between repeated observations of old and new" {
		t.Fatalf("reason = %q", accepted.Reason)
	}
	if evaluation.Differences[1].Accepted {
		t.Fatalf("a difference on a stable field was accepted: %+v", evaluation.Differences[1])
	}
	if evaluation.Outcome != model.OutcomeDifferent {
		t.Fatalf("outcome = %s, want DIFFERENT", evaluation.Outcome)
	}
	only := policy.Evaluate(item, differences[:1], policy.File{}, propertyNow, unstable)
	if only.Outcome != model.OutcomeEquivalent {
		t.Fatalf("outcome = %s, want EQUIVALENT", only.Outcome)
	}
	newOnly := model.Unstable{New: []string{unstableField}}
	side := policy.Evaluate(item, differences[:1], policy.File{}, propertyNow, newOnly)
	if side.Differences[0].Reason != "unstable: differs between repeated observations of new" {
		t.Fatalf("reason = %q", side.Differences[0].Reason)
	}
	file := propertyFile([]policy.Exception{{ID: "written", Reason: "r", Match: policy.Match{Field: unstableField}}})
	overridden := policy.Evaluate(item, differences[:1], file, propertyNow, unstable)
	if overridden.Differences[0].ExceptionID != policy.UnstableExceptionID {
		t.Fatalf("exception id = %q, want %q", overridden.Differences[0].ExceptionID, policy.UnstableExceptionID)
	}
}

// TestPropertySuggestedExceptionScope checks that the exception
// --suggest-exceptions writes for a flaky field accepts only that
// (host, field) pair (docs/run.md:83-88: one entry per (host, field) pair,
// with the exact host as its match.host;
// internal/policy/policy.go:212-227).
func TestPropertySuggestedExceptionScope(t *testing.T) {
	host := "api.example.com"
	field := "header.x-frame-options"
	data, err := policy.RenderSuggestions([]model.UnstableField{{Host: host, Field: field}}, 3, propertyNow)
	if err != nil {
		t.Fatal(err)
	}
	file, err := policy.Load(data)
	if err != nil {
		t.Fatalf("the suggestion does not load: %v\n%s", err, data)
	}
	sameHost := model.ProbeCase{ID: "case-0", Host: host, Path: "/api/v1", Family: "base"}
	otherHost := model.ProbeCase{ID: "case-1", Host: "app.example.com", Path: "/api/v1", Family: "base"}
	if !propertyAccepts(file, sameHost, propertyStatusDifference(field), propertyNow) {
		t.Fatalf("the suggested exception does not accept %q on %q", field, host)
	}
	if propertyAccepts(file, otherHost, propertyStatusDifference(field), propertyNow) {
		t.Fatalf("the suggested exception accepts %q on another host", field)
	}
	if propertyAccepts(file, sameHost, propertyStatusDifference("status"), propertyNow) {
		t.Fatalf("the suggested exception accepts another field on %q", host)
	}
}

// TestPropertySuggestionRoundTrip checks that the YAML the suggestion writer
// produces always loads and describes the pairs it was given
// (internal/policy/policy.go:212-227; docs/run.md:83-88).
func TestPropertySuggestionRoundTrip(t *testing.T) {
	for caseIndex := range 50 {
		rnd := propertyRand(caseIndex)
		unstable := propertyUnstableFields(rnd)
		data, err := policy.RenderSuggestions(unstable, 3, propertyNow)
		if err != nil {
			t.Fatalf("case %d: %v", caseIndex, err)
		}
		file, err := policy.Load(data)
		if err != nil {
			t.Fatalf("case %d: the suggestions do not load: %v\n%s", caseIndex, err, data)
		}
		if len(file.Exceptions) != len(unstable) {
			t.Fatalf("case %d: %d exceptions, want %d", caseIndex, len(file.Exceptions), len(unstable))
		}
		wantExpiry := propertyNow.UTC().AddDate(0, 0, 30).Format("2006-01-02")
		ids := make([]string, 0, len(file.Exceptions))
		for index, exception := range file.Exceptions {
			if exception.Match.Host != unstable[index].Host || exception.Match.Field != unstable[index].Field {
				t.Fatalf("case %d: exception %d matches %+v, want %q and %q", caseIndex, index, exception.Match, unstable[index].Host, unstable[index].Field)
			}
			if exception.Reason != "observed unstable across 3 observations" {
				t.Fatalf("case %d: reason = %q", caseIndex, exception.Reason)
			}
			if !exception.Expires.Set || exception.Expires.Date.Format("2006-01-02") != wantExpiry {
				t.Fatalf("case %d: expiry = %+v, want %s", caseIndex, exception.Expires, wantExpiry)
			}
			if !strings.HasPrefix(exception.ID, "unstable-") {
				t.Fatalf("case %d: id = %q", caseIndex, exception.ID)
			}
			ids = append(ids, exception.ID)
		}
		slices.Sort(ids)
		if len(slices.Compact(ids)) != len(ids) {
			t.Fatalf("case %d: the ids are not unique: %v", caseIndex, ids)
		}
	}
}

// TestPropertyLoadRejectsBadDocuments checks that every problem in an
// exceptions file is a VAR-CFG-0002 diagnostic (docs/run.md:130-132:
// unknown keys, a missing or duplicate id, a missing reason, a bad date, or
// an invalid glob; docs/troubleshooting.md:69-92). The input cap that turns
// an oversize file into the same diagnostic belongs to the caller
// (internal/cli/run.go:511-519), which covers it in
// internal/cli/run_input_test.go:99.
func TestPropertyLoadRejectsBadDocuments(t *testing.T) {
	tests := []struct {
		name     string
		document string
		fragment string
	}{
		{"an empty file", "", "empty"},
		{"a blank file", "  \n\t\n", "empty"},
		{"an unsupported version", "version: 2\nexceptions: []\n", "unsupported exceptions version"},
		{"a missing version", "exceptions: []\n", "unsupported exceptions version"},
		{"an unknown top level key", "version: 1\nunknown: true\n", "cannot parse"},
		{"an unknown exception key", "version: 1\nexceptions:\n  - id: a\n    reason: r\n    unknown: true\n", "cannot parse"},
		{"an unknown match key", "version: 1\nexceptions:\n  - id: a\n    reason: r\n    match:\n      unknown: true\n", "cannot parse"},
		{"a missing id", "version: 1\nexceptions:\n  - reason: r\n", "missing a required id"},
		{"a duplicate id", "version: 1\nexceptions:\n  - id: a\n    reason: r\n  - id: a\n    reason: s\n", "duplicate exception id"},
		{"an empty reason", "version: 1\nexceptions:\n  - id: a\n    reason: ''\n", "missing a required reason"},
		{"an empty date", "version: 1\nexceptions:\n  - id: a\n    reason: r\n    expires: ''\n", "cannot parse"},
		{"a date that is not a date", "version: 1\nexceptions:\n  - id: a\n    reason: r\n    expires: tomorrow\n", "not a YYYY-MM-DD"},
		{"a date without the padding", "version: 1\nexceptions:\n  - id: a\n    reason: r\n    expires: 2026-6-1\n", "not a YYYY-MM-DD"},
		{"a date with a time", "version: 1\nexceptions:\n  - id: a\n    reason: r\n    expires: 2026-06-01T00:00:00Z\n", "not a YYYY-MM-DD"},
		{"a date that does not exist", "version: 1\nexceptions:\n  - id: a\n    reason: r\n    expires: 2026-02-30\n", "not a YYYY-MM-DD"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := policy.Load([]byte(test.document))
			if err == nil {
				t.Fatal("expected an error")
			}
			var diagnosticError *diag.Error
			if !errors.As(err, &diagnosticError) {
				t.Fatalf("error %v is not a diagnostic", err)
			}
			if diagnosticError.Diagnostic.Code != diag.CodeExceptionsFile {
				t.Fatalf("code = %s, want %s", diagnosticError.Diagnostic.Code, diag.CodeExceptionsFile)
			}
			if !strings.Contains(diagnosticError.Diagnostic.Message, test.fragment) {
				t.Fatalf("message %q does not contain %q", diagnosticError.Diagnostic.Message, test.fragment)
			}
		})
	}
}

// TestPropertyLoadAcceptsTheDocumentedLimits checks the limits the loader
// enforces on a valid file: the version is the one varco understands, the
// dates are YYYY-MM-DD, and an omitted expiry never expires
// (internal/policy/policy.go:30-31 and internal/policy/policy.go:61-77;
// docs/run.md:133-147).
func TestPropertyLoadAcceptsTheDocumentedLimits(t *testing.T) {
	document := "version: 1\nexceptions:\n  - id: a\n    reason: r\n    expires: 2026-12-31\n  - id: b\n    reason: r\n    expires:\n"
	file, err := policy.Load([]byte(document))
	if err != nil {
		t.Fatal(err)
	}
	if file.Version != policy.SchemaVersion {
		t.Fatalf("version = %d, want %d", file.Version, policy.SchemaVersion)
	}
	if len(file.Exceptions) != 2 {
		t.Fatalf("exceptions = %d, want 2", len(file.Exceptions))
	}
	if !file.Exceptions[0].Expires.Set || file.Exceptions[0].Expires.Date.Format("2006-01-02") != "2026-12-31" {
		t.Fatalf("expiry = %+v", file.Exceptions[0].Expires)
	}
	if file.Exceptions[1].Expires.Set {
		t.Fatalf("an omitted expiry is set: %+v", file.Exceptions[1].Expires)
	}
	if ids := file.ExpiredIDs(propertyNow); len(ids) != 0 {
		t.Fatalf("expired exceptions = %v, want none", ids)
	}
}
