package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/HarnageaGabriel/varco/internal/clock"
	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/endpoint"
	"github.com/HarnageaGabriel/varco/internal/engine"
	"github.com/HarnageaGabriel/varco/internal/model"
	"github.com/HarnageaGabriel/varco/internal/plan"
	"github.com/HarnageaGabriel/varco/internal/policy"
	"github.com/HarnageaGabriel/varco/internal/probe"
	"github.com/HarnageaGabriel/varco/internal/report"
)

// runMethods are the only methods a plan may contain (ADR 0003).
var runMethods = map[string]bool{"GET": true, "HEAD": true, "OPTIONS": true}

func runRun(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	planPath := flags.String("plan", "", "plan JSON from `varco plan --format json` (- for stdin)")
	var inputs stringsFlag
	flags.Var(&inputs, "f", "Ingress manifest file, directory, or - for stdin (repeatable)")
	ingressClass := flags.String("ingress-class", "", "plan only Ingress resources of this class")
	defaultHost := flags.String("default-host", "", "host for rules that declare none")
	oldValue := flags.String("old", "", "old endpoint IP (port 80 for http, 443 for https)")
	newValue := flags.String("new", "", "new endpoint IP (port 80 for http, 443 for https)")
	oldHTTP := flags.String("old-http", "", "old endpoint for http cases (IP:port)")
	oldHTTPS := flags.String("old-https", "", "old endpoint for https cases (IP:port)")
	newHTTP := flags.String("new-http", "", "new endpoint for http cases (IP:port)")
	newHTTPS := flags.String("new-https", "", "new endpoint for https cases (IP:port)")
	exceptions := flags.String("exceptions", "", "exceptions YAML file")
	format := flags.String("format", "table", "table or json")
	output := flags.String("output", "", "write the report to this file (default stdout)")
	timeout := flags.Duration("timeout", 10*time.Second, "per-request timeout")
	rate := flags.Float64("rate", 5, "maximum requests per second to either endpoint")
	concurrency := flags.Int("concurrency", engine.MinConcurrency+3, "cases probed in parallel (1..64)")
	quiet := flags.Bool("quiet", false, "disable progress lines on stderr")
	showSensitive := flags.Bool("show-sensitive", false, "show raw sensitive values")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		return configFailure(stderr, "run takes no positional arguments")
	}
	if *planPath == "" && len(inputs) == 0 {
		return configFailure(stderr, "run requires --plan FILE or -f <file|directory|->")
	}
	if *planPath != "" && len(inputs) != 0 {
		return configFailure(stderr, "use either --plan or -f, not both")
	}
	if *format != "table" && *format != "json" {
		return configFailure(stderr, "--format must be table or json")
	}
	if *timeout <= 0 {
		return configFailure(stderr, "--timeout must be positive")
	}
	if err := engine.ValidateRate(*rate); err != nil {
		return configFailure(stderr, fmt.Sprintf("--rate: %v", err))
	}
	if err := engine.ValidateConcurrency(*concurrency); err != nil {
		return configFailure(stderr, fmt.Sprintf("--concurrency: %v", err))
	}
	if *showSensitive {
		_, _ = fmt.Fprintln(stderr, "warning: --show-sensitive exposes raw sensitive values")
	}
	oldEndpoints, code := endpointPair(*oldValue, *oldHTTP, *oldHTTPS, "--old", stderr)
	if code != 0 {
		return code
	}
	newEndpoints, code := endpointPair(*newValue, *newHTTP, *newHTTPS, "--new", stderr)
	if code != 0 {
		return code
	}
	manifest, code := loadRunPlan(*planPath, inputs, stdin, plan.Options{IngressClass: *ingressClass, DefaultHost: *defaultHost}, stderr)
	if code != 0 {
		return code
	}
	probeCases := model.FromPlanCases(manifest.Cases)
	for _, item := range probeCases {
		if !runMethods[strings.ToUpper(item.Method)] {
			diagnostic, _ := diag.New(diag.CodeUnsupportedMethod, fmt.Sprintf("case %s uses method %s", item.ID, item.Method))
			_, _ = fmt.Fprintf(stderr, "%s: %s\n", diagnostic.Code, diagnostic.Message)
			return 2
		}
	}
	var exceptionFile policy.File
	if *exceptions != "" {
		loaded, err := loadExceptions(*exceptions)
		if err != nil {
			return runDiagnosticFailure(stderr, err)
		}
		exceptionFile = loaded
	}
	config := engine.RunConfig{
		Cases:       probeCases,
		Old:         oldEndpoints,
		New:         newEndpoints,
		Timeout:     *timeout,
		Rate:        *rate,
		Concurrency: *concurrency,
		Exceptions:  exceptionFile,
		Unsupported: manifest.Unsupported,
		Now:         clock.Real{}.Now().UTC(),
	}
	if !*quiet {
		config.Progress = func(done, total int, item model.ProbeCase, outcome model.Outcome) {
			_, _ = fmt.Fprintf(stderr, "[%d/%d] %s %s %s ... %s\n", done, total, item.Host, item.Method, item.Path, outcome)
		}
	}
	runReport, err := engine.Run(context.Background(), config, clock.Real{}, probe.Do)
	if err != nil {
		var diagnosticError *diag.Error
		if errors.As(err, &diagnosticError) {
			_, _ = fmt.Fprintln(stderr, diagnosticError.Error())
			return 2
		}
		_, _ = fmt.Fprintln(stderr, "run:", err)
		return 4
	}
	if err := writeRunReport(*output, *format, runReport, report.Options{ShowSensitive: *showSensitive}, stdout); err != nil {
		_, _ = fmt.Fprintln(stderr, "report:", err)
		return 4
	}
	switch runReport.Verdict {
	case model.OutcomeDifferent:
		return 1
	case model.OutcomeInconclusive:
		return 3
	default:
		return 0
	}
}

// loadRunPlan reads either a plan JSON document or manifests and returns the
// plan to execute.
func loadRunPlan(planPath string, inputs stringsFlag, stdin io.Reader, options plan.Options, stderr io.Writer) (plan.Plan, int) {
	if planPath != "" {
		content, err := readRunInput(planPath, stdin)
		if err != nil {
			return plan.Plan{}, configFailure(stderr, fmt.Sprintf("cannot read plan: %v", err))
		}
		var document report.PlanDocument
		if err := json.Unmarshal(content, &document); err != nil {
			return plan.Plan{}, configFailure(stderr, fmt.Sprintf("cannot parse plan JSON: %v", err))
		}
		if document.SchemaVersion != 1 {
			return plan.Plan{}, configFailure(stderr, fmt.Sprintf("unsupported plan schema_version %d (want 1)", document.SchemaVersion))
		}
		return plan.Plan{Cases: document.Cases, Unsupported: document.Unsupported, Warnings: document.Warnings}, 0
	}
	paths, code := expandInputs(inputs, stderr)
	if code != 0 {
		return plan.Plan{}, code
	}
	resources, code := parseInputs(paths, stdin, stderr)
	if code != 0 {
		return plan.Plan{}, code
	}
	return plan.Build(resources, options), 0
}

// readRunInput reads the plan document from a file or, for -, from stdin.
func readRunInput(path string, stdin io.Reader) ([]byte, error) {
	if path == stdinInput {
		return io.ReadAll(stdin)
	}
	return os.ReadFile(path) //nolint:gosec // the path is the plan file the user named
}

// endpointPair resolves one side's --<label> base plus its per-scheme
// overrides. The base defaults to port 80 for http and 443 for https.
func endpointPair(base, httpOverride, httpsOverride, label string, stderr io.Writer) (engine.Endpoints, int) {
	if base == "" && (httpOverride == "" || httpsOverride == "") {
		return engine.Endpoints{}, configFailure(stderr, fmt.Sprintf("%s is required (or both %s-http and %s-https)", label, label, label))
	}
	var pair engine.Endpoints
	httpValue, httpName := base, label
	if httpOverride != "" {
		httpValue, httpName = httpOverride, label+"-http"
	}
	httpsValue, httpsName := base, label
	if httpsOverride != "" {
		httpsValue, httpsName = httpsOverride, label+"-https"
	}
	httpEndpoint, err := endpoint.Parse(httpValue, 80)
	if err != nil {
		return engine.Endpoints{}, configFailure(stderr, fmt.Sprintf("invalid %s: %v", httpName, err))
	}
	httpsEndpoint, err := endpoint.Parse(httpsValue, 443)
	if err != nil {
		return engine.Endpoints{}, configFailure(stderr, fmt.Sprintf("invalid %s: %v", httpsName, err))
	}
	pair.HTTP = httpEndpoint
	pair.HTTPS = httpsEndpoint
	return pair, 0
}

// loadExceptions reads and validates the exceptions file. Errors carry their
// VAR-CFG-0002 diagnostic.
func loadExceptions(path string) (policy.File, error) {
	content, err := os.ReadFile(path) //nolint:gosec // the path is the exceptions file the user named
	if err != nil {
		return policy.File{}, err
	}
	return policy.Load(content)
}

// writeRunReport renders the report to --output, or to stdout when unset.
func writeRunReport(output, format string, runReport model.RunReport, options report.Options, stdout io.Writer) error {
	writer := stdout
	if output != "" {
		file, err := os.Create(output) //nolint:gosec // the path is the output file the user named
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()
		writer = file
	}
	if format == "json" {
		return report.RunJSON(writer, runReport, options)
	}
	return report.RunTable(writer, runReport, options)
}

// runDiagnosticFailure prints a diagnostic-wrapped error and returns its exit code.
func runDiagnosticFailure(stderr io.Writer, err error) int {
	var diagnosticError *diag.Error
	if errors.As(err, &diagnosticError) {
		_, _ = fmt.Fprintln(stderr, diagnosticError.Error())
	} else {
		_, _ = fmt.Fprintln(stderr, err)
	}
	return 2
}
