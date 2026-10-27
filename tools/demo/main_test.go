package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HarnageaGabriel/varco/internal/cli"
)

type demoReport struct {
	Verdict       string   `json:"verdict"`
	IgnoredFields []string `json:"ignored_fields"`
	Cases         []struct {
		CaseID      string `json:"case_id"`
		Outcome     string `json:"outcome"`
		Differences []struct {
			Field string `json:"field"`
			Old   string `json:"old"`
			New   string `json:"new"`
		} `json:"differences"`
	} `json:"cases"`
}

type demoPlan struct {
	Cases []struct {
		ID     string `json:"id"`
		Family string `json:"family"`
		Path   string `json:"path"`
	} `json:"cases"`
}

func TestDemoWithVarco(t *testing.T) {
	oldHandler, newHandler := demoHandlers()
	oldServer := httptest.NewServer(oldHandler)
	newServer := httptest.NewServer(newHandler)
	t.Cleanup(oldServer.Close)
	t.Cleanup(newServer.Close)

	planPath := filepath.Join(t.TempDir(), "plan.json")
	manifestPath := filepath.Join("..", "..", "testdata", "demo", "ingress.yaml")
	var planOutput, planError bytes.Buffer
	if exit := cli.Run([]string{"plan", "-f", manifestPath, "--format", "json"}, strings.NewReader(""), &planOutput, &planError); exit != 0 {
		t.Fatalf("plan exit = %d; stderr=%s", exit, planError.String())
	}
	if err := os.WriteFile(planPath, planOutput.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	var generated demoPlan
	if err := json.Unmarshal(planOutput.Bytes(), &generated); err != nil {
		t.Fatalf("decode generated plan: %v", err)
	}
	planCases := make(map[string]struct{ family, path string }, len(generated.Cases))
	for _, item := range generated.Cases {
		planCases[item.ID] = struct{ family, path string }{item.Family, item.Path}
	}

	report, exit := runDemo(t, planPath, oldServer.URL, newServer.URL)
	if exit != 1 || report.Verdict != "DIFFERENT" {
		t.Fatalf("old/new exit = %d verdict = %s, want 1 DIFFERENT", exit, report.Verdict)
	}
	assertDifference(t, report, planCases, "case-flip", "status", "200", "404")
	assertPathDifference(t, report, planCases, "/app", "status", "301", "200")
	if !contains(report.IgnoredFields, "Server") {
		t.Fatalf("ignored_fields = %v, want Server", report.IgnoredFields)
	}
	for _, item := range report.Cases {
		for _, difference := range item.Differences {
			if difference.Field == "header.server" {
				t.Fatal("Server header was reported despite being ignored")
			}
		}
	}

	sameReport, sameExit := runDemo(t, planPath, oldServer.URL, oldServer.URL)
	if sameExit != 0 || sameReport.Verdict != "EQUIVALENT" {
		t.Fatalf("old/old exit = %d verdict = %s, want 0 EQUIVALENT", sameExit, sameReport.Verdict)
	}
}

func runDemo(t *testing.T, planPath, oldURL, newURL string) (demoReport, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	exit := cli.Run([]string{
		"run", "--plan", planPath,
		"--old", strings.TrimPrefix(oldURL, "http://"),
		"--new", strings.TrimPrefix(newURL, "http://"),
		"--rate", "10000", "--concurrency", "1", "--quiet", "--format", "json",
	}, strings.NewReader(""), &stdout, &stderr)
	if stderr.Len() != 0 {
		t.Fatalf("run stderr = %s", stderr.String())
	}
	var report demoReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}
	return report, exit
}

func assertDifference(t *testing.T, report demoReport, planCases map[string]struct{ family, path string }, family, field, oldValue, newValue string) {
	t.Helper()
	for _, item := range report.Cases {
		if planCases[item.CaseID].family != family {
			continue
		}
		for _, difference := range item.Differences {
			if difference.Field == field && difference.Old == oldValue && difference.New == newValue {
				return
			}
		}
	}
	t.Fatalf("missing %s %s difference %s -> %s in %+v", family, field, oldValue, newValue, report.Cases)
}

func assertPathDifference(t *testing.T, report demoReport, planCases map[string]struct{ family, path string }, path, field, oldValue, newValue string) {
	t.Helper()
	for _, item := range report.Cases {
		if planCases[item.CaseID].path != path {
			continue
		}
		for _, difference := range item.Differences {
			if difference.Field == field && difference.Old == oldValue && difference.New == newValue {
				return
			}
		}
	}
	t.Fatalf("missing %s %s difference %s -> %s", path, field, oldValue, newValue)
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
