package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/HarnageaGabriel/varco/internal/clock"
	"github.com/HarnageaGabriel/varco/internal/compare"
	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/endpoint"
	"github.com/HarnageaGabriel/varco/internal/engine"
	"github.com/HarnageaGabriel/varco/internal/ingress"
	"github.com/HarnageaGabriel/varco/internal/kube"
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
	fromCluster := flags.Bool("from-cluster", false, "read Ingresses and the controller ConfigMap with kubectl get")
	namespace := flags.String("namespace", "", "read Ingresses from this namespace (default all namespaces)")
	kubeContext := flags.String("kube-context", "", "kubectl context to use")
	kubeconfig := flags.String("kubeconfig", "", "kubeconfig file to use")
	kubeTimeout := flags.Duration("kube-timeout", 30*time.Second, "timeout for one kubectl get")
	controllerConfigmap := flags.String("controller-configmap", "ingress-nginx/ingress-nginx-controller", "controller ConfigMap as NAMESPACE/NAME (empty disables)")
	profile := flags.String("profile", plan.ProfileNginx, "nginx or generic")
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
	repeat := flags.Int("repeat", 1, "probe each endpoint this many times per case (1..5)")
	body := flags.String("body", string(compare.BodyPolicy2xx), "compare the body: 2xx, hash, or none")
	maxRequests := flags.Int("max-requests", 0, "stop after this many requests (0 means unlimited)")
	var families, excludePaths stringsFlag
	flags.Var(&families, "family", "run only cases of this family (repeatable)")
	flags.Var(&excludePaths, "exclude-path", "exclude cases whose request path matches this glob (repeatable)")
	dryRun := flags.Bool("dry-run", false, "print the requests a run would send, without sending any")
	suggestExceptions := flags.String("suggest-exceptions", "", "write an exceptions file for unstable fields (must not already exist)")
	quiet := flags.Bool("quiet", false, "disable progress lines on stderr")
	showSensitive := flags.Bool("show-sensitive", false, "show raw sensitive values")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if !validProfile(*profile) {
		return configFailure(stderr, "--profile must be nginx or generic")
	}
	if flags.NArg() != 0 {
		return configFailure(stderr, "run takes no positional arguments")
	}
	if *fromCluster && (*planPath != "" || len(inputs) != 0) {
		return configFailure(stderr, "use either --from-cluster, --plan, or -f, not both")
	}
	if !*fromCluster && *planPath == "" && len(inputs) == 0 {
		return configFailure(stderr, "run requires --plan FILE or -f <file|directory|->, or --from-cluster")
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
	if *kubeTimeout <= 0 {
		return configFailure(stderr, "--kube-timeout must be positive")
	}
	if err := engine.ValidateRate(*rate); err != nil {
		return configFailure(stderr, fmt.Sprintf("--rate: %v", err))
	}
	if err := engine.ValidateConcurrency(*concurrency); err != nil {
		return configFailure(stderr, fmt.Sprintf("--concurrency: %v", err))
	}
	if err := engine.ValidateRepeat(*repeat); err != nil {
		return configFailure(stderr, fmt.Sprintf("--repeat: %v", err))
	}
	bodyPolicy, err := compare.ParseBodyPolicy(*body)
	if err != nil {
		return configFailure(stderr, fmt.Sprintf("--body: %v", err))
	}
	if *maxRequests < 0 {
		return configFailure(stderr, "--max-requests must not be negative")
	}
	for _, pattern := range excludePaths {
		if _, err := path.Match(pattern, ""); err != nil {
			return configFailure(stderr, fmt.Sprintf("--exclude-path %q is not a valid glob: %v", pattern, err))
		}
	}
	if *showSensitive {
		_, _ = fmt.Fprintln(stderr, "warning: --show-sensitive exposes raw sensitive values")
	}
	oldEndpoints, oldCode := endpointPair(*oldValue, *oldHTTP, *oldHTTPS, "--old", stderr)
	if oldCode != 0 {
		return oldCode
	}
	newEndpoints, newCode := endpointPair(*newValue, *newHTTP, *newHTTPS, "--new", stderr)
	if newCode != 0 {
		return newCode
	}
	var manifest plan.Plan
	var code int
	if *fromCluster {
		manifest, code = loadRunPlanFromCluster(*namespace, *kubeContext, *kubeconfig, *kubeTimeout, *controllerConfigmap, plan.Options{IngressClass: *ingressClass, DefaultHost: *defaultHost, Profile: *profile}, stderr)
	} else {
		manifest, code = loadRunPlan(*planPath, inputs, stdin, plan.Options{IngressClass: *ingressClass, DefaultHost: *defaultHost, Profile: *profile}, stderr)
	}
	if code != 0 {
		return code
	}
	probeCases, excluded := engine.FilterCases(model.FromPlanCases(manifest.Cases), families, excludePaths)
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
		Repeat:      *repeat,
		BodyPolicy:  bodyPolicy,
		MaxRequests: *maxRequests,
		Excluded:    excluded,
		Exceptions:  exceptionFile,
		Unsupported: manifest.Unsupported,
		Now:         clock.Real{}.Now().UTC(),
	}
	if *dryRun {
		requests := engine.DryRun(config)
		for _, request := range requests {
			_, _ = fmt.Fprintln(stdout, request.Line())
		}
		_, _ = fmt.Fprintf(stdout, "TOTAL %d\n", len(requests))
		return 0
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
	if *suggestExceptions != "" {
		if code := writeSuggestions(*suggestExceptions, runReport, *repeat, config.Now, stderr); code != 0 {
			return code
		}
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

func loadRunPlanFromCluster(namespace, contextName, kubeconfig string, timeout time.Duration, controllerRef string, options plan.Options, stderr io.Writer) (plan.Plan, int) {
	return loadRunPlanFromClusterWithRunner(kube.DefaultRunner{KubeConfig: kubeconfig, ContextName: contextName}, namespace, timeout, controllerRef, options, stderr)
}

func loadRunPlanFromClusterWithRunner(runner kube.Runner, namespace string, timeout time.Duration, controllerRef string, options plan.Options, stderr io.Writer) (plan.Plan, int) {
	resources, controllerConfig, code := loadClusterPlanWithRunner(runner, namespace, timeout, controllerRef, stderr)
	if code != 0 {
		return plan.Plan{}, code
	}
	result := plan.Build(resources, options)
	if result.Profile == plan.ProfileNginx {
		result.Warnings = append(result.Warnings, plan.ConfigMapWarnings(controllerConfig)...)
	}
	sortWarnings(result.Warnings)
	return result, 0
}

func sortWarnings(warnings []plan.Warning) {
	sort.Slice(warnings, func(i, j int) bool { return warningLess(warnings[i], warnings[j]) })
}

// writeSuggestions writes an exceptions file for every field the run observed
// unstable. It never overwrites an existing file (exit code 2).
func writeSuggestions(output string, runReport model.RunReport, repeat int, now time.Time, stderr io.Writer) int {
	data, err := policy.RenderSuggestions(runReport.Unstable, repeat, now)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "suggest-exceptions:", err)
		return 4
	}
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // the path is the file the user named
	if errors.Is(err, os.ErrExist) {
		return configFailure(stderr, fmt.Sprintf("--suggest-exceptions: %s already exists; varco never overwrites it", output))
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "suggest-exceptions:", err)
		return 4
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Write(data); err != nil {
		_, _ = fmt.Fprintln(stderr, "suggest-exceptions:", err)
		return 4
	}
	return 0
}

// loadRunPlan reads either a plan JSON document or manifests and returns the
// plan to execute.
func loadRunPlan(planPath string, inputs stringsFlag, stdin io.Reader, options plan.Options, stderr io.Writer) (plan.Plan, int) {
	if planPath != "" {
		content, err := readRunInput(planPath, stdin)
		if err != nil {
			if errors.Is(err, errInputTooLarge) {
				return plan.Plan{}, planFailure(stderr, planPath, fmt.Sprintf("plan is larger than the %d-byte limit varco reads", ingress.MaxInputBytes))
			}
			return plan.Plan{}, configFailure(stderr, fmt.Sprintf("cannot read plan: %v", err))
		}
		var document struct {
			SchemaVersion int            `json:"schema_version"`
			Profile       string         `json:"profile"`
			Cases         []plan.Case    `json:"cases"`
			Unsupported   []plan.Finding `json:"unsupported"`
			Warnings      []plan.Warning `json:"warnings"`
		}
		if err := json.Unmarshal(content, &document); err != nil {
			return plan.Plan{}, configFailure(stderr, fmt.Sprintf("cannot parse plan JSON: %v", err))
		}
		if document.SchemaVersion != 1 {
			return plan.Plan{}, configFailure(stderr, fmt.Sprintf("unsupported plan schema_version %d (want 1)", document.SchemaVersion))
		}
		for index, item := range document.Cases {
			if reason := planCaseProblem(item); reason != "" {
				return plan.Plan{}, configFailure(stderr, fmt.Sprintf("plan case at index %d has an invalid %s", index, reason))
			}
		}
		return plan.Plan{Profile: document.Profile, Cases: document.Cases, Unsupported: document.Unsupported, Warnings: document.Warnings}, 0
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

// planCaseProblem reports why a planned case cannot be sent as written, or ""
// when the case is valid. The reason names the offending field and never
// repeats its value, so an untrusted plan cannot smuggle bytes into a
// terminal through the error message.
func planCaseProblem(item plan.Case) string {
	if !probe.ValidHost(item.Host) {
		return "host"
	}
	if !probe.ValidPath(item.Path) {
		return "path"
	}
	if !validPlanText(item.ID) {
		return "id"
	}
	if !validPlanText(item.Family) {
		return "family"
	}
	if !validPlanText(item.Note) {
		return "note"
	}
	return planHeadersProblem(item.Headers)
}

// planHeadersProblem reports why the planned headers of a case cannot be sent
// as written, or "". One Host header is allowed: the prober writes it on its
// own Host line, so the host-variant family keeps working.
func planHeadersProblem(headers []plan.Header) string {
	hostHeaders := 0
	for _, header := range headers {
		if !probe.ValidHeaderName(header.Name) {
			return "header name"
		}
		if strings.EqualFold(header.Name, "Host") {
			hostHeaders++
			if hostHeaders > 1 {
				return "Host header"
			}
			if len(header.Values) != 1 || !probe.ValidHost(header.Values[0]) {
				return "Host header"
			}
			continue
		}
		for _, value := range header.Values {
			if !probe.ValidHeaderValue(value) {
				return "header value"
			}
		}
	}
	return ""
}

// validPlanText reports whether a plan text field carries no control byte:
// CR, LF, ESC and DEL are all rejected, so the field cannot rewrite a
// terminal line.
func validPlanText(value string) bool {
	for i := 0; i < len(value); i++ {
		if c := value[i]; c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

// planFailure prints a VAR-PLAN-0003 diagnostic for a plan document the CLI
// cannot use and returns the usage exit code.
func planFailure(stderr io.Writer, path string, message string) int {
	diagnostic, _ := diag.New(diag.CodePlanManifest, message)
	_, _ = fmt.Fprintf(stderr, "%s: %s: %s\n", diagnostic.Code, path, diagnostic.Message)
	return 2
}

// errInputTooLarge reports that an input exceeded the cap varco reads.
var errInputTooLarge = errors.New("input is larger than the limit varco reads")

// readRunInput reads the plan document from a file or, for -, from stdin. The
// input is read through a limit reader, so an endless or oversized plan can
// never exhaust memory.
func readRunInput(path string, stdin io.Reader) ([]byte, error) {
	var reader io.Reader
	if path == stdinInput {
		reader = stdin
	} else {
		file, err := os.Open(path) //nolint:gosec // the path is the plan file the user named
		if err != nil {
			return nil, err
		}
		defer func() { _ = file.Close() }()
		reader = file
	}
	return readCapped(reader)
}

// readCapped reads a reader through the input cap, the same limit the manifest
// parser uses. A larger input is refused instead of read.
func readCapped(reader io.Reader) ([]byte, error) {
	content, err := io.ReadAll(io.LimitReader(reader, ingress.MaxInputBytes+1))
	if err != nil {
		return nil, err
	}
	if len(content) > ingress.MaxInputBytes {
		return nil, errInputTooLarge
	}
	return content, nil
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
	file, err := os.Open(path) //nolint:gosec // the path is the exceptions file the user named
	if err != nil {
		return policy.File{}, err
	}
	defer func() { _ = file.Close() }()
	content, err := readCapped(file)
	if err != nil {
		if errors.Is(err, errInputTooLarge) {
			return policy.File{}, exceptionsTooLarge()
		}
		return policy.File{}, err
	}
	return policy.Load(content)
}

// exceptionsTooLarge builds the VAR-CFG-0002 diagnostic for an exceptions file
// above the input cap.
func exceptionsTooLarge() error {
	message := fmt.Sprintf("exceptions file is larger than the %d-byte limit varco reads", ingress.MaxInputBytes)
	diagnostic, err := diag.New(diag.CodeExceptionsFile, message)
	if err != nil {
		return errors.New(message)
	}
	return &diag.Error{Diagnostic: diagnostic}
}

// writeRunReport renders the report to --output, or to stdout when unset.
func writeRunReport(output, format string, runReport model.RunReport, options report.Options, stdout io.Writer) error {
	writer := stdout
	if output != "" {
		file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // the path is the output file the user named
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
