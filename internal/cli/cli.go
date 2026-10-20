// Package cli adapts command-line arguments to varco operations.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/HarnageaGabriel/varco/internal/clock"
	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/endpoint"
	"github.com/HarnageaGabriel/varco/internal/engine"
	"github.com/HarnageaGabriel/varco/internal/probe"
	"github.com/HarnageaGabriel/varco/internal/report"
	"github.com/HarnageaGabriel/varco/internal/result"
	"github.com/HarnageaGabriel/varco/internal/version"
)

const usage = `Usage: varco <command> [options]

Commands:
  version    print build version
  help       show this help
  diff       compare two endpoints
  plan       derive probe cases from Ingress manifests
  run        execute a probe plan against two endpoints
`

// Run executes args and returns a documented process exit code. stdin is only
// read by commands that accept a stream, such as `plan -f -`.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}

	switch args[0] {
	case "help", "-h", "--help":
		if len(args) != 1 {
			_, _ = fmt.Fprintln(stderr, "help: unexpected arguments")
			return 2
		}
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	case "version":
		return runVersion(args[1:], stdout, stderr)
	case "diff":
		return runDiff(args[1:], stdout, stderr)
	case "plan":
		return runPlan(args[1:], stdin, stdout, stderr)
	case "run":
		return runRun(args[1:], stdin, stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown subcommand %q\n", args[0])
		return 2
	}
}

type stringsFlag []string

func (values *stringsFlag) String() string         { return strings.Join(*values, ",") }
func (values *stringsFlag) Set(value string) error { *values = append(*values, value); return nil }

func runDiff(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("diff", flag.ContinueOnError)
	flags.SetOutput(stderr)
	oldValue := flags.String("old", "", "old numeric IP endpoint")
	newValue := flags.String("new", "", "new numeric IP endpoint")
	host := flags.String("host", "", "HTTP Host header")
	sni := flags.String("sni", "", "TLS server name (defaults to host)")
	scheme := flags.String("scheme", "https", "http or https")
	method := flags.String("method", http.MethodGet, "HTTP method")
	timeout := flags.Duration("timeout", 10*time.Second, "per-request timeout")
	rate := flags.Float64("rate", 5, "maximum requests per second")
	format := flags.String("format", "table", "table or json")
	allowUnsafe := flags.Bool("allow-unsafe-methods", false, "allow methods other than GET, HEAD, OPTIONS")
	showSensitive := flags.Bool("show-sensitive", false, "show raw sensitive Location values")
	var headerValues, ignoreHeaders stringsFlag
	flags.Var(&headerValues, "header", "request header NAME=VALUE (repeatable)")
	flags.Var(&ignoreHeaders, "ignore-header", "response header to ignore (repeatable)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showSensitive {
		_, _ = fmt.Fprintln(stderr, "warning: --show-sensitive exposes raw Location values")
	}
	if *oldValue == "" || *newValue == "" || *host == "" {
		return configFailure(stderr, "--old, --new, and --host are required")
	}
	if *scheme != "http" && *scheme != "https" {
		return configFailure(stderr, "--scheme must be http or https")
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
	if !probe.ValidHost(*host) {
		return configFailure(stderr, "--host must be non-empty and contain no whitespace, control bytes, / ? # @ or backslash")
	}
	defaultPort := uint16(443)
	if *scheme == "http" {
		defaultPort = 80
	}
	oldEndpoint, err := endpoint.Parse(*oldValue, defaultPort)
	if err != nil {
		return configFailure(stderr, fmt.Sprintf("invalid --old: %v", err))
	}
	newEndpoint, err := endpoint.Parse(*newValue, defaultPort)
	if err != nil {
		return configFailure(stderr, fmt.Sprintf("invalid --new: %v", err))
	}
	requestHeaders, err := parseHeaders(headerValues)
	if err != nil {
		return configFailure(stderr, err.Error())
	}
	paths := flags.Args()
	if len(paths) == 0 {
		paths = []string{"/"}
	}
	for _, path := range paths {
		if !strings.HasPrefix(path, "/") {
			return configFailure(stderr, fmt.Sprintf("path of length %d must start with /", len(path)))
		}
		if !probe.ValidPath(path) {
			return configFailure(stderr, fmt.Sprintf("path of length %d contains a space or control byte", len(path)))
		}
	}
	if *sni == "" {
		*sni = sniFromHost(*host)
	}
	if !probe.ValidSNI(*sni) {
		return configFailure(stderr, "--sni must be a hostname or IP address without a port")
	}
	config := engine.DiffConfig{Old: oldEndpoint, New: newEndpoint, Scheme: *scheme, Host: *host, SNI: *sni, Method: strings.ToUpper(*method), Headers: requestHeaders, Paths: paths, Timeout: *timeout, Rate: *rate, IgnoreHeaders: ignoreHeaders, AllowUnsafeMethod: *allowUnsafe}
	outcome, err := engine.Diff(context.Background(), config, clock.Real{}, probe.Do)
	if err != nil {
		var diagnosticError *diag.Error
		if errors.As(err, &diagnosticError) {
			_, _ = fmt.Fprintln(stderr, diagnosticError.Error())
			return 2
		}
		if code := diagFailure(stderr, diag.CodeEndpointUnreachable, err.Error()); code != 0 {
			return code
		}
		return 3
	}
	if *format == "json" {
		err = report.JSON(stdout, outcome.Differences, report.Options{ShowSensitive: *showSensitive})
	} else {
		err = report.Table(stdout, outcome.Differences, report.Options{ShowSensitive: *showSensitive})
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "report:", err)
		return 4
	}
	for _, failure := range outcome.Failures {
		message := fmt.Sprintf("path of length %d via %s: %v", len(failure.Path), failure.Endpoint, failure.Err)
		if code := diagFailure(stderr, diag.CodeEndpointUnreachable, message); code != 0 {
			return code
		}
	}
	if len(outcome.Differences) != 0 {
		return 1
	}
	if len(outcome.Failures) != 0 {
		return 3
	}
	return 0
}

func sniFromHost(host string) string {
	if name, _, err := net.SplitHostPort(host); err == nil {
		return name
	}
	return strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
}

func parseHeaders(values []string) ([]result.Header, error) {
	headers := make([]result.Header, 0, len(values))
	for _, value := range values {
		name, headerValue, ok := strings.Cut(value, "=")
		if !ok || name == "" {
			return nil, fmt.Errorf("--header must use NAME=VALUE")
		}
		if !probe.ValidHeaderName(name) {
			return nil, fmt.Errorf("invalid request header name %q", name)
		}
		if !probe.ValidHeaderValue(headerValue) {
			return nil, fmt.Errorf("invalid value for request header %s", name)
		}
		canonical := http.CanonicalHeaderKey(name)
		if probe.CredentialHeader(canonical) {
			return nil, fmt.Errorf("request header %s is not allowed", canonical)
		}
		if canonical == "Host" {
			return nil, fmt.Errorf("request header Host is not allowed; use --host")
		}
		if probe.ForbiddenHeader(canonical) {
			return nil, fmt.Errorf("request header %s is set by varco and cannot be overridden", canonical)
		}
		headers = append(headers, result.Header{Name: canonical, Values: []string{headerValue}})
	}
	return headers, nil
}

// diagFailure prints the diagnostic for code and message. It returns 0 when
// the diagnostic was printed, and the internal error exit code 4 when the
// diagnostic registry does not know the code: the plain message is printed
// instead of a zero diagnostic, and the caller must fail with that code
// (docs/exit-codes.md).
func diagFailure(stderr io.Writer, code diag.Code, message string) int {
	diagnostic, err := diag.New(code, message)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, message)
		return 4
	}
	_, _ = fmt.Fprintf(stderr, "%s: %s\n", diagnostic.Code, diagnostic.Message)
	return 0
}

func configFailure(stderr io.Writer, message string) int {
	if code := diagFailure(stderr, diag.CodeInvalidFlag, message); code != 0 {
		return code
	}
	return 2
}

func runVersion(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("version", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "print version as JSON")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "version: unexpected arguments")
		return 2
	}

	info := version.Current()
	if *jsonOutput {
		if err := json.NewEncoder(stdout).Encode(info); err != nil {
			_, _ = fmt.Fprintln(stderr, "version:", err)
			return 4
		}
		return 0
	}

	_, _ = fmt.Fprintf(stdout, "varco %s (commit %s, built %s)\n", info.Version, info.Commit, info.Date)
	return 0
}
