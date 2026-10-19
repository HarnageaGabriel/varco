package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/ingress"
	"github.com/HarnageaGabriel/varco/internal/kube"
	"github.com/HarnageaGabriel/varco/internal/plan"
	"github.com/HarnageaGabriel/varco/internal/redact"
	"github.com/HarnageaGabriel/varco/internal/report"
)

// stdinInput is the -f value that reads a manifest stream from standard input.
const stdinInput = "-"

func runPlan(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("plan", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var inputs stringsFlag
	flags.Var(&inputs, "f", "Ingress manifest file, directory, or - for stdin (repeatable)")
	format := flags.String("format", "table", "table or json")
	ingressClass := flags.String("ingress-class", "", "plan only Ingress resources of this class")
	defaultHost := flags.String("default-host", "", "host for rules that declare none")
	fromCluster := flags.Bool("from-cluster", false, "read Ingresses and the controller ConfigMap with kubectl get")
	namespace := flags.String("namespace", "", "read Ingresses from this namespace (default all namespaces)")
	kubeContext := flags.String("kube-context", "", "kubectl context to use")
	kubeconfig := flags.String("kubeconfig", "", "kubeconfig file to use")
	kubeTimeout := flags.Duration("kube-timeout", 30*time.Second, "timeout for one kubectl get")
	controllerConfigmap := flags.String("controller-configmap", "ingress-nginx/ingress-nginx-controller", "controller ConfigMap as NAMESPACE/NAME (empty disables)")
	profile := flags.String("profile", plan.ProfileNginx, "nginx or generic")
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
		return configFailure(stderr, "plan takes no positional arguments; pass manifests with -f")
	}
	if *fromCluster && len(inputs) != 0 {
		return configFailure(stderr, "use either --from-cluster or -f, not both")
	}
	if !*fromCluster && len(inputs) == 0 {
		return configFailure(stderr, "plan requires at least one -f <file|directory|-> or --from-cluster")
	}
	if *format != "table" && *format != "json" {
		return configFailure(stderr, "--format must be table or json")
	}
	if *kubeTimeout <= 0 {
		return configFailure(stderr, "--kube-timeout must be positive")
	}
	var resources []ingress.Ingress
	var controllerConfig map[string]string
	if *fromCluster {
		loaded, configMap, code := loadClusterPlan(*namespace, *kubeContext, *kubeconfig, *kubeTimeout, *controllerConfigmap, stderr)
		if code != 0 {
			return code
		}
		resources, controllerConfig = loaded, configMap
	} else {
		paths, code := expandInputs(inputs, stderr)
		if code != 0 {
			return code
		}
		resources, code = parseInputs(paths, stdin, stderr)
		if code != 0 {
			return code
		}
	}
	result := plan.Build(resources, plan.Options{IngressClass: *ingressClass, DefaultHost: *defaultHost, ControllerConfig: controllerConfig, Profile: *profile})
	if result.Profile == plan.ProfileNginx {
		result.Warnings = append(result.Warnings, plan.ConfigMapWarnings(controllerConfig)...)
	}
	sort.Slice(result.Warnings, func(i, j int) bool { return warningLess(result.Warnings[i], result.Warnings[j]) })
	var err error
	if *format == "json" {
		err = writePlanJSON(stdout, result)
	} else {
		err = report.PlanTable(stdout, result)
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "report:", err)
		return 4
	}
	return 0
}

// loadClusterPlan is the read-only cluster edge used by both plan and run.
// It invokes only kubectl get and never writes to the cluster.
func loadClusterPlan(namespace, contextName, kubeconfig string, timeout time.Duration, controllerRef string, stderr io.Writer) ([]ingress.Ingress, map[string]string, int) {
	return loadClusterPlanWithRunner(kube.DefaultRunner{KubeConfig: kubeconfig, ContextName: contextName}, namespace, timeout, controllerRef, stderr)
}

func loadClusterPlanWithRunner(runner kube.Runner, namespace string, timeout time.Duration, controllerRef string, stderr io.Writer) ([]ingress.Ingress, map[string]string, int) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ingressJSON, err := kube.FetchIngresses(ctx, runner, namespace)
	if err != nil {
		return nil, nil, kubeFailure(stderr, err)
	}
	resources, err := ingress.Parse(bytes.NewReader(ingressJSON))
	if err != nil {
		return nil, nil, configFailure(stderr, fmt.Sprintf("cannot parse cluster Ingress JSON: %v", err))
	}
	var controllerConfig map[string]string
	if controllerRef != "" {
		controllerNamespace, controllerName, ok := strings.Cut(controllerRef, "/")
		if !ok || controllerNamespace == "" || controllerName == "" {
			return nil, nil, configFailure(stderr, "--controller-configmap must use NAMESPACE/NAME")
		}
		controllerConfig, err = kube.FetchConfigMap(ctx, runner, controllerNamespace, controllerName)
		if err != nil {
			return nil, nil, kubeFailure(stderr, err)
		}
	}
	return resources, controllerConfig, 0
}

func kubeFailure(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintln(stderr, "kube:", redact.Text(truncateError(err.Error(), 2<<10)))
	return 2
}

func truncateError(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func warningLess(a, b plan.Warning) bool {
	if a.Code != b.Code {
		return a.Code < b.Code
	}
	if a.Ingress != b.Ingress {
		return a.Ingress < b.Ingress
	}
	return a.Message < b.Message
}

// expandInputs turns -f values into a sorted, de-duplicated list of manifest

func validProfile(value string) bool {
	return value == plan.ProfileNginx || value == plan.ProfileGeneric
}

// writePlanJSON adds the additive top-level profile while preserving schema
// version 1 and the stable field order of the plan envelope.
func writePlanJSON(writer io.Writer, result plan.Plan) error {
	document := struct {
		SchemaVersion int            `json:"schema_version"`
		Profile       string         `json:"profile"`
		Cases         []plan.Case    `json:"cases"`
		Unsupported   []plan.Finding `json:"unsupported"`
		Warnings      []plan.Warning `json:"warnings"`
	}{
		SchemaVersion: 1,
		Profile:       result.Profile,
		Cases:         result.Cases,
		Unsupported:   result.Unsupported,
		Warnings:      result.Warnings,
	}
	if document.Cases == nil {
		document.Cases = []plan.Case{}
	}
	if document.Unsupported == nil {
		document.Unsupported = []plan.Finding{}
	}
	if document.Warnings == nil {
		document.Warnings = []plan.Warning{}
	}
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(document)
}

// files. Directories are walked recursively and only *.yaml, *.yml and *.json
// regular files are collected, so the plan never depends on directory order.
func expandInputs(inputs []string, stderr io.Writer) ([]string, int) {
	seen := make(map[string]bool, len(inputs))
	paths := make([]string, 0, len(inputs))
	appendPath := func(path string) {
		if seen[path] {
			return
		}
		seen[path] = true
		paths = append(paths, path)
	}
	for _, input := range inputs {
		if input == stdinInput {
			appendPath(input)
			continue
		}
		info, err := os.Stat(input)
		if err != nil {
			return nil, configFailure(stderr, fmt.Sprintf("cannot read %s: %v", input, err))
		}
		if !info.IsDir() {
			appendPath(input)
			continue
		}
		err = filepath.WalkDir(input, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !walkableManifest(entry) {
				return nil
			}
			appendPath(path)
			return nil
		})
		if err != nil {
			return nil, configFailure(stderr, fmt.Sprintf("cannot walk %s: %v", input, err))
		}
	}
	sort.Strings(paths)
	return paths, 0
}

// parseInputs parses every path, in order. A manifest error is reported with
// its VAR-PLAN-0003 code and fails the command; unreadable files are usage
// errors.
func parseInputs(paths []string, stdin io.Reader, stderr io.Writer) ([]ingress.Ingress, int) {
	var resources []ingress.Ingress
	for _, path := range paths {
		found, err := parseInput(path, stdin)
		if err != nil {
			var diagnostic *diag.Error
			if errors.As(err, &diagnostic) {
				_, _ = fmt.Fprintf(stderr, "%s: %s: %s\n", diagnostic.Diagnostic.Code, path, diagnostic.Diagnostic.Message)
				return nil, 2
			}
			return nil, configFailure(stderr, fmt.Sprintf("cannot read %s: %v", path, err))
		}
		resources = append(resources, found...)
	}
	return resources, 0
}

// parseInput reads one manifest input: stdin for -, a file otherwise. The
// input is read through io.LimitReader so it can never exceed the parser's cap.
func parseInput(path string, stdin io.Reader) ([]ingress.Ingress, error) {
	var reader io.Reader
	if path == stdinInput {
		reader = stdin
	} else {
		file, err := os.Open(path) //nolint:gosec // the path is the manifest the user asked to plan
		if err != nil {
			return nil, err
		}
		defer func() { _ = file.Close() }()
		reader = file
	}
	content, err := io.ReadAll(io.LimitReader(reader, ingress.MaxInputBytes+1))
	if err != nil {
		return nil, err
	}
	if len(content) > ingress.MaxInputBytes {
		return nil, manifestTooLarge()
	}
	return ingress.Parse(bytes.NewReader(content))
}

// manifestTooLarge builds the VAR-PLAN-0003 diagnostic for an oversized input.
func manifestTooLarge() error {
	message := fmt.Sprintf("manifest is larger than the %d-byte limit varco reads", ingress.MaxInputBytes)
	diagnostic, err := diag.New(diag.CodePlanManifest, message)
	if err != nil {
		return errors.New(message)
	}
	return &diag.Error{Diagnostic: diagnostic}
}

// walkableManifest reports whether a directory entry is a manifest varco reads:
// a regular file with a manifest extension. Directories, symlinks (which
// filepath.WalkDir does not follow), and other non-regular files are skipped.
func walkableManifest(entry fs.DirEntry) bool {
	return !entry.IsDir() && entry.Type().IsRegular() && manifestFile(entry.Name())
}

// manifestFile reports whether a directory entry looks like a manifest varco reads.
func manifestFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".yaml", ".yml", ".json":
		return true
	default:
		return false
	}
}
