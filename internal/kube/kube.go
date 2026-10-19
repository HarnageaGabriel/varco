// Package kube reads Kubernetes resources through the user's kubectl command.
package kube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/HarnageaGabriel/varco/internal/redact"
)

// MaxOutputBytes caps the JSON returned by one kubectl invocation.
const MaxOutputBytes = 16 << 20

const maxStderrBytes = 2 << 10

// dnsName matches a Kubernetes namespace or object name (DNS-1123 subdomain).
// Names are validated before they reach kubectl so a value such as
// "--token=x" can never be read as a flag.
var dnsName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]{0,251}[a-z0-9])?$`)

func validName(kind, value string) error {
	if !dnsName.MatchString(value) {
		return fmt.Errorf("kube: %s %q is not a valid Kubernetes name", kind, value)
	}
	return nil
}

// Runner executes one read-only kubectl command.
type Runner interface {
	Run(ctx context.Context, args []string) ([]byte, error)
}

// NotFound is the kubectl error for an object that does not exist.
type NotFound struct {
	Err error
}

func (e *NotFound) Error() string {
	if e == nil {
		return "<nil>"
	}
	return e.Err.Error()
}

func (e *NotFound) Unwrap() error { return e.Err }

// DefaultRunner runs kubectl with the user's kubeconfig and context.
type DefaultRunner struct {
	KubeConfig  string
	ContextName string
}

var errOutputTooLarge = errors.New("kube: kubectl output exceeds the configured limit")

type boundedBuffer struct {
	buffer []byte
	limit  int
	// truncate drops the overflow silently instead of failing the command.
	truncate bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - len(b.buffer)
	if len(p) <= remaining {
		b.buffer = append(b.buffer, p...)
		return len(p), nil
	}
	if remaining > 0 {
		b.buffer = append(b.buffer, p[:remaining]...)
	}
	if b.truncate {
		return len(p), nil
	}
	return len(p), errOutputTooLarge
}

func (b *boundedBuffer) Bytes() []byte  { return append([]byte(nil), b.buffer...) }
func (b *boundedBuffer) String() string { return string(b.buffer) }

// Run executes kubectl with bounded stdout and stderr buffers. Only the get
// verb is allowed; credentials are never set by varco.
func (r DefaultRunner) Run(ctx context.Context, args []string) ([]byte, error) {
	if len(args) == 0 || args[0] != "get" {
		return nil, fmt.Errorf("kube: only kubectl get is allowed, got %q", firstArg(args))
	}
	kubeArgs := append([]string(nil), args...)
	if r.KubeConfig != "" {
		kubeArgs = append([]string{"--kubeconfig", r.KubeConfig}, kubeArgs...)
	}
	if r.ContextName != "" {
		kubeArgs = append([]string{"--context", r.ContextName}, kubeArgs...)
	}
	command := exec.CommandContext(ctx, "kubectl", kubeArgs...) //nolint:gosec // user-requested read-only command
	outputBuffer := &boundedBuffer{limit: MaxOutputBytes}
	stderrBuffer := &boundedBuffer{limit: maxStderrBytes, truncate: true}
	command.Stdout = outputBuffer
	command.Stderr = stderrBuffer
	err := command.Run()
	if err != nil {
		if errors.Is(err, errOutputTooLarge) {
			return nil, fmt.Errorf("kube: kubectl output exceeds %d bytes", MaxOutputBytes)
		}
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return nil, &CommandError{Exit: exitError.ExitCode(), Stderr: stderrBuffer.String(), Err: err}
		}
		if errors.Is(err, exec.ErrNotFound) || strings.Contains(err.Error(), "not found") {
			return nil, &CommandError{Missing: true, Err: err}
		}
		return nil, &CommandError{Err: err}
	}
	return outputBuffer.Bytes(), nil
}

// CommandError records a failed kubectl execution without exposing its raw
// stderr beyond the bounded, redacted value used by the CLI.
type CommandError struct {
	Exit    int
	Stderr  string
	Missing bool
	Err     error
}

func (e *CommandError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Missing {
		return "kubectl not found: install kubectl or add it to PATH"
	}
	if e.Exit != 0 {
		stderr := redact.Text(e.Stderr)
		message := fmt.Sprintf("kubectl get failed (exit %d)", e.Exit)
		if stderr != "" {
			message += ": " + stderr
		}
		return message
	}
	return "kubectl get failed: " + e.Err.Error()
}

func (e *CommandError) Unwrap() error { return e.Err }

// FetchIngresses returns the raw Kubernetes List JSON for ingress.Parse.
func FetchIngresses(ctx context.Context, runner Runner, namespace string) ([]byte, error) {
	args := []string{"get", "ingress"}
	if namespace == "" {
		args = append(args, "-A")
	} else {
		if err := validName("namespace", namespace); err != nil {
			return nil, err
		}
		args = append(args, "-n", namespace)
	}
	args = append(args, "-o", "json")
	return runner.Run(ctx, args)
}

// FetchConfigMap returns the data map of a ConfigMap. A missing ConfigMap is
// represented by (nil, nil).
func FetchConfigMap(ctx context.Context, runner Runner, namespace, name string) (map[string]string, error) {
	if err := validName("namespace", namespace); err != nil {
		return nil, err
	}
	if err := validName("ConfigMap name", name); err != nil {
		return nil, err
	}
	args := []string{"get", "configmap", name, "-n", namespace, "-o", "json"}
	output, err := runner.Run(ctx, args)
	if err != nil {
		var notFound *NotFound
		var commandError *CommandError
		if errors.As(err, &notFound) || (errors.As(err, &commandError) && strings.Contains(commandError.Stderr, "NotFound")) {
			return nil, nil
		}
		return nil, err
	}
	var document struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(output, &document); err != nil {
		return nil, fmt.Errorf("decode ConfigMap JSON: %w", err)
	}
	return document.Data, nil
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}
