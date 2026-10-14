package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/HarnageaGabriel/varco/internal/model"
	"github.com/HarnageaGabriel/varco/internal/plan"
	"github.com/HarnageaGabriel/varco/internal/policy"
)

func decodeRunReport(t *testing.T, data []byte) model.RunReport {
	t.Helper()
	var runReport model.RunReport
	if err := json.Unmarshal(data, &runReport); err != nil {
		t.Fatalf("cannot decode run report: %v; data=%s", err, data)
	}
	return runReport
}

func jsonBodyServer(body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
}

// Source: docs/run.md; the trust flags of the run command: --repeat,
// --suggest-exceptions, --body, --max-requests, --family, --exclude-path and
// --dry-run.
func TestRunCommandTrustFlags(t *testing.T) {
	oneCase := planJSON(t, []plan.Case{{ID: "aaaaaaaaaaaa", Host: "app.example", Scheme: "http", Method: "GET", Path: "/", Family: "base"}})
	twoCases := planJSON(t, []plan.Case{
		{ID: "aaaaaaaaaaaa", Host: "app.example", Scheme: "http", Method: "GET", Path: "/", Family: "base"},
		{ID: "bbbbbbbbbbbb", Host: "app.example", Scheme: "http", Method: "GET", Path: "/other", Family: "other"},
	})
	base := []string{"run", "--rate", "10000", "--concurrency", "4", "--quiet"}

	var hits atomic.Int64
	stable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("same"))
	}))
	defer stable.Close()
	stableAddr := stable.Listener.Addr().String()

	t.Run("dry-run makes zero connections", func(t *testing.T) {
		hits.Store(0)
		var stdout, stderr bytes.Buffer
		args := append(append([]string{}, base...), "--plan", "-", "--dry-run", "--old", stableAddr, "--new", stableAddr)
		if code := Run(args, strings.NewReader(oneCase), &stdout, &stderr); code != 0 {
			t.Fatalf("code=%d stderr=%q", code, stderr.String())
		}
		if hits.Load() != 0 {
			t.Fatalf("dry-run sent %d requests", hits.Load())
		}
		want := fmt.Sprintf("old http %s GET app.example - /", stableAddr)
		if !strings.Contains(stdout.String(), want) || !strings.Contains(stdout.String(), "TOTAL 2") {
			t.Fatalf("stdout = %q", stdout.String())
		}
	})

	t.Run("repeat out of range", func(t *testing.T) {
		for _, value := range []string{"0", "6"} {
			var stdout, stderr bytes.Buffer
			args := append(append([]string{}, base...), "--plan", "-", "--repeat", value, "--old", stableAddr, "--new", stableAddr)
			if code := Run(args, strings.NewReader(oneCase), &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "--repeat") {
				t.Fatalf("repeat=%s code=%d stderr=%q", value, code, stderr.String())
			}
		}
	})

	t.Run("invalid flag values", func(t *testing.T) {
		tests := []struct {
			args   []string
			stderr string
		}{
			{[]string{"--max-requests", "-1"}, "--max-requests"},
			{[]string{"--body", "bad"}, "--body"},
			{[]string{"--exclude-path", "["}, "--exclude-path"},
		}
		for _, test := range tests {
			var stdout, stderr bytes.Buffer
			args := append([]string{}, base...)
			args = append(args, "--plan", "-", "--old", stableAddr, "--new", stableAddr)
			args = append(args, test.args...)
			if code := Run(args, strings.NewReader(oneCase), &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), test.stderr) {
				t.Fatalf("args=%v code=%d stderr=%q", test.args, code, stderr.String())
			}
		}
	})

	t.Run("family filter excludes others", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		args := append(append([]string{}, base...), "--plan", "-", "--format", "json", "--family", "other", "--old", stableAddr, "--new", stableAddr)
		if code := Run(args, strings.NewReader(twoCases), &stdout, &stderr); code != 0 {
			t.Fatalf("code=%d stderr=%q", code, stderr.String())
		}
		runReport := decodeRunReport(t, stdout.Bytes())
		if runReport.Excluded != 1 || runReport.Coverage.Planned != 1 {
			t.Fatalf("excluded=%d planned=%d", runReport.Excluded, runReport.Coverage.Planned)
		}
	})

	t.Run("exclude-path removes every case", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		args := append(append([]string{}, base...), "--plan", "-", "--format", "json", "--exclude-path", "/", "--old", stableAddr, "--new", stableAddr)
		if code := Run(args, strings.NewReader(oneCase), &stdout, &stderr); code != 0 {
			t.Fatalf("code=%d stderr=%q", code, stderr.String())
		}
		runReport := decodeRunReport(t, stdout.Bytes())
		if runReport.Excluded != 1 || runReport.Coverage.Planned != 0 {
			t.Fatalf("excluded=%d planned=%d", runReport.Excluded, runReport.Coverage.Planned)
		}
	})

	t.Run("budget skips the case and reports inconclusive", func(t *testing.T) {
		hits.Store(0)
		var stdout, stderr bytes.Buffer
		args := append(append([]string{}, base...), "--plan", "-", "--max-requests", "1", "--old", stableAddr, "--new", stableAddr)
		if code := Run(args, strings.NewReader(oneCase), &stdout, &stderr); code != 3 {
			t.Fatalf("code=%d stderr=%q", code, stderr.String())
		}
		if hits.Load() != 0 {
			t.Fatalf("budget over-run: %d requests", hits.Load())
		}
		if !strings.Contains(stdout.String(), "budget") {
			t.Fatalf("stdout = %q", stdout.String())
		}
	})

	t.Run("body policy", func(t *testing.T) {
		oldServer, newServer := jsonBodyServer("aaaa"), jsonBodyServer("bbbb")
		defer oldServer.Close()
		defer newServer.Close()
		oldAddr, newAddr := oldServer.Listener.Addr().String(), newServer.Listener.Addr().String()

		var stdout, stderr bytes.Buffer
		args := append(append([]string{}, base...), "--plan", "-", "--old", oldAddr, "--new", newAddr)
		if code := Run(args, strings.NewReader(oneCase), &stdout, &stderr); code != 1 {
			t.Fatalf("2xx default code=%d stderr=%q", code, stderr.String())
		}

		stdout.Reset()
		stderr.Reset()
		args = append(append([]string{}, base...), "--plan", "-", "--format", "json", "--body", "none", "--old", oldAddr, "--new", newAddr)
		if code := Run(args, strings.NewReader(oneCase), &stdout, &stderr); code != 0 {
			t.Fatalf("body none code=%d stderr=%q", code, stderr.String())
		}
		if runReport := decodeRunReport(t, stdout.Bytes()); runReport.BodyPolicy != "none" {
			t.Fatalf("body_policy = %q", runReport.BodyPolicy)
		}
	})

	t.Run("suggest-exceptions writes once", func(t *testing.T) {
		var counter atomic.Int64
		varying := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			values := []string{"SAMEORIGIN", "DENY", "SAMEORIGIN"}
			w.Header().Set("X-Frame-Options", values[int(counter.Add(1)-1)%len(values)])
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("same"))
		}))
		defer varying.Close()
		suggestionPath := filepath.Join(t.TempDir(), "exceptions.yaml")
		args := append(append([]string{}, base...), "--plan", "-", "--repeat", "3", "--suggest-exceptions", suggestionPath, "--old", stableAddr, "--new", varying.Listener.Addr().String())
		var stdout, stderr bytes.Buffer
		if code := Run(args, strings.NewReader(oneCase), &stdout, &stderr); code != 0 {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
		content, err := os.ReadFile(suggestionPath)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content), "observed unstable across 3 observations") || !strings.Contains(string(content), "host: app.example") {
			t.Fatalf("suggestions = %s", content)
		}
		if _, err := policy.Load(content); err != nil {
			t.Fatalf("suggestions are not a valid exceptions file: %v", err)
		}
		stdout.Reset()
		stderr.Reset()
		if code := Run(args, strings.NewReader(oneCase), &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "already exists") {
			t.Fatalf("second run code=%d stderr=%q", code, stderr.String())
		}
	})
}
