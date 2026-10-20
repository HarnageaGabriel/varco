package e2e

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// runReportDoc is the shape of the versioned JSON run report the tests need.
// CONVENTIONS.md: JSON is stable and versioned (schema_version), so every
// scenario that renders JSON asserts schema_version 1 here.
type runReportDoc struct {
	SchemaVersion int    `json:"schema_version"`
	Verdict       string `json:"verdict"`
	Cases         []struct {
		CaseID      string `json:"case_id"`
		Outcome     string `json:"outcome"`
		Error       string `json:"error"`
		Differences []struct {
			Field       string `json:"field"`
			Old         string `json:"old"`
			New         string `json:"new"`
			Accepted    bool   `json:"accepted"`
			Reason      string `json:"reason"`
			ExceptionID string `json:"exception_id"`
		} `json:"differences"`
	} `json:"cases"`
}

// ok200Handler answers every request with a stable 200 body.
var ok200Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("app\n"))
})

// securedHandler answers like ok200Handler plus one security header.
var securedHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("X-Frame-Options", "DENY")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("app\n"))
})

// stableReferrerHandler answers with a constant Referrer-Policy.
var stableReferrerHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Referrer-Policy", "v1")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("app\n"))
})

// redirectHandler answers with the given status and Location header.
func redirectHandler(status int, location, body string) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", location)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
}

// rootRedirectHandler answers "/" with a 301 to /x/ and everything else at 200.
var rootRedirectHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("app\n"))
		return
	}
	w.Header().Set("Location", "/x/")
	w.WriteHeader(http.StatusMovedPermanently)
	_, _ = w.Write([]byte("moved\n"))
})

// startServer listens on 127.0.0.1:<random port> (docs/adr/0004-endpoint-syntax.md)
// and is closed by t.Cleanup; httptest.Server.Close is idempotent per its
// closed flag, so a pre-run Close plus the Cleanup is safe.
func startServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

// scenarioDescription carries each scenario's table-driven inputs.
type scenarioDescription struct {
	name string
	// source cites the doc or ADR the scenario asserts (CONVENTIONS.md).
	source      string
	plan        string
	handlers    func(t *testing.T) (*httptest.Server, *httptest.Server)
	closeNew    bool
	repeat      int
	maxRequests int
	exceptions  string
	wantExit    int
	// wantVerdict applies to the table verdict line and the JSON verdict.
	wantVerdict string
	// checks runs extra per-format assertions.
	checks func(t *testing.T, format string, result runResult, report *runReportDoc)
}

func TestRunScenario(t *testing.T) {
	scenarios := []scenarioDescription{
		{
			// Source: docs/adr/0007-verdict-model.md (verdict EQUIVALENT) and
			// docs/exit-codes.md (exit code 0).
			name:   "identical",
			source: "docs/adr/0007-verdict-model.md explains EQUIVALENT; docs/exit-codes.md documents exit code 0",
			plan:   "plan-identical.json",
			handlers: func(t *testing.T) (*httptest.Server, *httptest.Server) {
				return startServer(t, ok200Handler), startServer(t, ok200Handler)
			},
			wantExit:    0,
			wantVerdict: "EQUIVALENT",
		},
		{
			// Source: docs/adr/0007-verdict-model.md (DIFFERENT verdict, exit 1,
			// Location is a compared field) and docs/exit-codes.md (exit code 1).
			name:   "redirect-differs",
			source: "docs/adr/0007-verdict-model.md explains the DIFFERENT verdict; docs/exit-codes.md documents exit code 1",
			plan:   "plan-redirect.json",
			handlers: func(t *testing.T) (*httptest.Server, *httptest.Server) {
				old := startServer(t, redirectHandler(http.StatusMovedPermanently, "/x/", "moved\n"))
				candidate := startServer(t, ok200Handler)
				return old, candidate
			},
			wantExit:    1,
			wantVerdict: "DIFFERENT",
			checks: func(t *testing.T, format string, result runResult, report *runReportDoc) {
				if format == "table" {
					// The table report must name the case by id and host.
					if !strings.Contains(result.stdout, "bbbbbbbbbbbb") || !strings.Contains(result.stdout, "app.example.com") {
						t.Fatalf("table report does not name the case:\n%s", result.stdout)
					}
					return
				}
				caseReport := report.Cases[0]
				if caseReport.Outcome != "DIFFERENT" || len(caseReport.Differences) != 2 {
					t.Fatalf("case = %+v, want DIFFERENT with status and location differences", caseReport)
				}
				// Differences are sorted by field, so header.location precedes status.
				if caseReport.Differences[0].Field != "header.location" || caseReport.Differences[0].Old != "/x/" || caseReport.Differences[0].New != "(absent)" {
					t.Fatalf("location difference = %+v", caseReport.Differences[0])
				}
				if caseReport.Differences[1].Field != "status" || caseReport.Differences[1].Old != "301" || caseReport.Differences[1].New != "200" {
					t.Fatalf("status difference = %+v", caseReport.Differences[1])
				}
			},
		},
		{
			// Source: docs/adr/0007-verdict-model.md (DIFFERENT verdict, exit 1)
			// and docs/exit-codes.md; X-Frame-Options is a compared security
			// header per internal/compare comparedHeaders.
			name:   "header-differs",
			source: "docs/adr/0007-verdict-model.md explains the DIFFERENT verdict; docs/exit-codes.md documents exit code 1",
			plan:   "plan-header.json",
			handlers: func(t *testing.T) (*httptest.Server, *httptest.Server) {
				old := startServer(t, securedHandler)
				candidate := startServer(t, ok200Handler)
				return old, candidate
			},
			wantExit:    1,
			wantVerdict: "DIFFERENT",
			checks: func(t *testing.T, format string, result runResult, report *runReportDoc) {
				if format == "table" {
					if !strings.Contains(result.stdout, "header.x-frame-options") || !strings.Contains(result.stdout, "DENY") {
						t.Fatalf("table report does not show the header difference:\n%s", result.stdout)
					}
					return
				}
				difference := report.Cases[0].Differences[0]
				if difference.Field != "header.x-frame-options" || difference.Old != "DENY" || difference.New != "(absent)" {
					t.Fatalf("header difference = %+v", difference)
				}
			},
		},
		{
			// Source: docs/adr/0007-verdict-model.md (INCONCLUSIVE: one endpoint
			// could not be observed) and docs/exit-codes.md (exit code 3);
			// VAR-NET-0001 marks the unreachable endpoint.
			name:   "endpoint-down",
			source: "docs/adr/0007-verdict-model.md explains INCONCLUSIVE; docs/exit-codes.md documents exit code 3; VAR-NET-0001 is the unreachable-endpoint diagnostic",
			plan:   "plan-identical.json",
			handlers: func(t *testing.T) (*httptest.Server, *httptest.Server) {
				old := startServer(t, ok200Handler)
				candidate := startServer(t, ok200Handler)
				return old, candidate
			},
			closeNew:    true,
			wantExit:    3,
			wantVerdict: "INCONCLUSIVE",
			checks: func(t *testing.T, format string, result runResult, report *runReportDoc) {
				if format == "table" {
					if !strings.Contains(result.stdout, "VAR-NET-0001") {
						t.Fatalf("table report lacks VAR-NET-0001:\n%s", result.stdout)
					}
					return
				}
				caseReport := report.Cases[0]
				if caseReport.Outcome != "INCONCLUSIVE" || !strings.Contains(caseReport.Error, "VAR-NET-0001") || !strings.Contains(caseReport.Error, "dial tcp") {
					t.Fatalf("case = %+v, want INCONCLUSIVE with a VAR-NET-0001 dial error", caseReport)
				}
			},
		},
		{
			// Source: docs/adr/0007-verdict-model.md: when a run has both
			// different and inconclusive cases, DIFFERENT and exit code 1 win
			// while the report still lists the inconclusive case; docs/exit-codes.md.
			name:   "difference-plus-down",
			source: "docs/adr/0007-verdict-model.md: exit 1 wins over 3; docs/exit-codes.md states the same precedence",
			plan:   "plan-budget.json",
			handlers: func(t *testing.T) (*httptest.Server, *httptest.Server) {
				old := startServer(t, ok200Handler)
				candidate := startServer(t, rootRedirectHandler)
				return old, candidate
			},
			maxRequests: 2, // repeat=1 needs 2 requests per case, so /budgeted is skipped by budget
			wantExit:    1,
			wantVerdict: "DIFFERENT",
			checks: func(t *testing.T, format string, result runResult, report *runReportDoc) {
				if format == "table" {
					if !strings.Contains(result.stdout, "budget") || !strings.Contains(result.stdout, "INCONCLUSIVE") {
						t.Fatalf("table report does not list the budget-skipped case:\n%s", result.stdout)
					}
					return
				}
				if len(report.Cases) != 2 {
					t.Fatalf("case count = %d, want 2", len(report.Cases))
				}
				if report.Cases[0].Outcome != "DIFFERENT" {
					t.Fatalf("executed case = %+v, want DIFFERENT", report.Cases[0])
				}
				if report.Cases[1].Outcome != "INCONCLUSIVE" || report.Cases[1].Error != "budget" {
					t.Fatalf("skipped case = %+v, want INCONCLUSIVE with error budget", report.Cases[1])
				}
			},
		},
		{
			// Source: docs/adr/0007-verdict-model.md (accepted differences stay
			// visible with reason and exception id) and docs/run.md (Exceptions
			// file: match.host, match.path and match.field select differences).
			name:   "exception-accepts",
			source: "docs/adr/0007-verdict-model.md requires accepted differences to stay visible; docs/run.md documents the exceptions file",
			plan:   "plan-redirect.json",
			handlers: func(t *testing.T) (*httptest.Server, *httptest.Server) {
				old := startServer(t, redirectHandler(http.StatusMovedPermanently, "/x/", "moved\n"))
				candidate := startServer(t, ok200Handler)
				return old, candidate
			},
			exceptions:  "exceptions-redirect.yaml",
			wantExit:    0,
			wantVerdict: "EQUIVALENT",
			checks: func(t *testing.T, format string, result runResult, report *runReportDoc) {
				if format == "table" {
					if !strings.Contains(result.stdout, "accepted (accepted-status)") || !strings.Contains(result.stdout, "accepted (accepted-location)") {
						t.Fatalf("table report does not mark both accepted differences:\n%s", result.stdout)
					}
					return
				}
				for _, difference := range report.Cases[0].Differences {
					if !difference.Accepted || difference.Reason == "" || difference.ExceptionID == "" {
						t.Fatalf("difference = %+v, want accepted with reason and exception id", difference)
					}
				}
			},
		},
		{
			// Source: docs/run.md (Unstable fields): an unstable field never
			// makes a case DIFFERENT; only an unstable status makes a case
			// INCONCLUSIVE with error "unstable status". Status here is stable,
			// so the code treats the case as EQUIVALENT and keeps the
			// difference accepted with reason "unstable: differs between
			// repeated observations of new" and exception id auto:unstable
			// (docs/adr/0007-verdict-model.md).
			name:   "flaky-unstable-header",
			source: "docs/run.md: a difference on an unstable field is accepted, not DIFFERENT; only unstable status makes INCONCLUSIVE",
			plan:   "plan-flaky.json",
			handlers: func(t *testing.T) (*httptest.Server, *httptest.Server) {
				var calls atomic.Int64
				candidate := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Referrer-Policy", "call-"+strconv.FormatInt(calls.Add(1), 10))
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte("app\n"))
				}))
				old := startServer(t, stableReferrerHandler)
				return old, candidate
			},
			repeat:      3,
			wantExit:    0,
			wantVerdict: "EQUIVALENT",
			checks: func(t *testing.T, format string, result runResult, report *runReportDoc) {
				if format == "table" {
					if !strings.Contains(result.stdout, "accepted (auto:unstable): unstable: differs between repeated observations of new") {
						t.Fatalf("table report does not mark the unstable field:\n%s", result.stdout)
					}
					return
				}
				difference := report.Cases[0].Differences[0]
				if difference.Field != "header.referrer-policy" || !difference.Accepted || difference.ExceptionID != "auto:unstable" || difference.Reason != "unstable: differs between repeated observations of new" {
					t.Fatalf("difference = %+v, want accepted auto:unstable on header.referrer-policy", difference)
				}
			},
		},
		{
			// Source: docs/adr/0006-redaction.md: Location query values are
			// redacted and Set-Cookie values are never compared or reported,
			// in table, JSON and stderr alike unless --show-sensitive is used.
			name:   "redaction",
			source: "docs/adr/0006-redaction.md: Location query values and Set-Cookie values never leave the process",
			plan:   "plan-redaction.json",
			handlers: func(t *testing.T) (*httptest.Server, *httptest.Server) {
				old := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Location", "/login?token=secret-token-alpha&next=/")
					w.Header().Add("Set-Cookie", "session=cookie-old-secret; Path=/; Secure")
					w.WriteHeader(http.StatusFound)
					_, _ = w.Write([]byte("app\n"))
				}))
				candidate := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Location", "/login?token=secret-token-beta&next=/")
					w.Header().Add("Set-Cookie", "session=cookie-new-secret; Path=/api; Secure")
					w.WriteHeader(http.StatusFound)
					_, _ = w.Write([]byte("app\n"))
				}))
				return old, candidate
			},
			wantExit:    1,
			wantVerdict: "DIFFERENT",
			checks: func(t *testing.T, _ string, result runResult, _ *runReportDoc) {
				combined := result.stdout + result.stderr
				for _, secret := range []string{"secret-token-alpha", "secret-token-beta", "cookie-old-secret", "cookie-new-secret"} {
					if strings.Contains(combined, secret) {
						t.Fatalf("output contains secret %s:\n%s", secret, combined)
					}
				}
				if !strings.Contains(result.stdout, "REDACTED") || !strings.Contains(result.stdout, "(query values differ)") {
					t.Fatalf("output does not show the redacted location difference:\n%s", result.stdout)
				}
			},
		},
	}

	for _, scenario := range scenarios {
		for _, format := range []string{"table", "json"} {
			t.Run(scenario.name+"/"+format, func(t *testing.T) {
				t.Logf("source: %s", scenario.source)
				old, candidate := scenario.handlers(t)
				oldAddr, newAddr := old.Listener.Addr().String(), candidate.Listener.Addr().String()
				if scenario.closeNew {
					candidate.Close()
				}
				args := []string{
					"run",
					"--plan", testDataPath(scenario.plan),
					// Endpoint syntax per docs/adr/0004-endpoint-syntax.md: a
					// numeric IP with an explicit port, no DNS.
					"--old", oldAddr,
					"--new", newAddr,
					"--rate", "10000",
					"--concurrency", "1",
					"--quiet",
				}
				if scenario.repeat != 0 {
					args = append(args, "--repeat", strconv.Itoa(scenario.repeat))
				}
				if scenario.maxRequests != 0 {
					args = append(args, "--max-requests", strconv.Itoa(scenario.maxRequests))
				}
				if scenario.exceptions != "" {
					args = append(args, "--exceptions", testDataPath(scenario.exceptions))
				}
				args = append(args, "--format", format)

				result := runCLI(t, args)
				if result.exit != scenario.wantExit {
					t.Fatalf("Run() = %d, want %d; stdout=%q stderr=%q", result.exit, scenario.wantExit, result.stdout, result.stderr)
				}
				// --quiet keeps stderr free; CONVENTIONS.md: stderr for logs,
				// stdout for requested output only.
				if result.stderr != "" {
					t.Fatalf("stderr = %q, want empty", result.stderr)
				}
				var report *runReportDoc
				if format == "json" {
					var document runReportDoc
					if err := json.Unmarshal([]byte(result.stdout), &document); err != nil {
						t.Fatalf("JSON report is not valid: %v\n%s", err, result.stdout)
					}
					if document.SchemaVersion != 1 {
						t.Fatalf("schema_version = %d, want 1", document.SchemaVersion)
					}
					if document.Verdict != scenario.wantVerdict {
						t.Fatalf("verdict = %q, want %q", document.Verdict, scenario.wantVerdict)
					}
					report = &document
				} else if !strings.Contains(result.stdout, "VERDICT") || !strings.Contains(result.stdout, scenario.wantVerdict) {
					t.Fatalf("table report lacks verdict %q:\n%s", scenario.wantVerdict, result.stdout)
				}
				if scenario.checks != nil {
					scenario.checks(t, format, result, report)
				}
				golden(t, scenario.name+"."+format+".golden", normalizeOutput(result.stdout, oldAddr, newAddr))
			})
		}
	}
}

// Source: docs/run.md (Dry run): --dry-run makes no network connection, prints
// one line per request plus a TOTAL line, and exits 0.
func TestRunDryRun(t *testing.T) {
	var oldSeen, newSeen atomic.Int64
	counterHandler := func(seen *atomic.Int64) http.HandlerFunc {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			seen.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("app\n"))
		})
	}
	old := startServer(t, counterHandler(&oldSeen))
	candidate := startServer(t, counterHandler(&newSeen))
	oldAddr, newAddr := old.Listener.Addr().String(), candidate.Listener.Addr().String()

	result := runCLI(t, []string{
		"run",
		"--plan", testDataPath("plan-identical.json"),
		"--old", oldAddr, // endpoint syntax per docs/adr/0004-endpoint-syntax.md
		"--new", newAddr,
		"--rate", "10000",
		"--concurrency", "1",
		"--quiet",
		"--dry-run",
	})
	if result.exit != 0 {
		t.Fatalf("Run() = %d, want %d; stdout=%q stderr=%q", result.exit, 0, result.stdout, result.stderr)
	}
	if oldSeen.Load() != 0 || newSeen.Load() != 0 {
		t.Fatalf("servers received requests: old=%d new=%d", oldSeen.Load(), newSeen.Load())
	}
	if !strings.Contains(result.stdout, "old http 127.0.0.1:") || !strings.Contains(result.stdout, "new http 127.0.0.1:") {
		t.Fatalf("dry-run output lacks both sides:\n%s", result.stdout)
	}
	golden(t, "dry-run.golden", normalizeOutput(result.stdout, oldAddr, newAddr))
}
