package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HarnageaGabriel/varco/internal/kube"
	"github.com/HarnageaGabriel/varco/internal/plan"
)

type clusterResponse struct {
	out []byte
	err error
}

type clusterRunner struct {
	calls     [][]string
	responses map[string]clusterResponse
}

func (r *clusterRunner) Run(_ context.Context, args []string) ([]byte, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	response, ok := r.responses[strings.Join(args, " ")]
	if !ok {
		return nil, errors.New("unexpected kubectl command")
	}
	return response.out, response.err
}

func readKubeFixture(t *testing.T, name string) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("..", "..", "testdata", "kube", name))
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func clusterRunnerWithFixtures() *clusterRunner {
	return &clusterRunner{responses: map[string]clusterResponse{
		"get ingress -A -o json": {out: readKubeFixtureForTest()},
		"get configmap ingress-nginx-controller -n ingress-nginx -o json": {out: readKubeFixtureForConfigMap()},
	}}
}

var (
	kubeIngressFixture   []byte
	kubeConfigMapFixture []byte
)

func readKubeFixtureForTest() []byte      { return kubeIngressFixture }
func readKubeFixtureForConfigMap() []byte { return kubeConfigMapFixture }

func TestLoadClusterPlanWithRunner(t *testing.T) {
	kubeIngressFixture = readKubeFixture(t, "ingresses.json")
	kubeConfigMapFixture = readKubeFixture(t, "configmap.json")
	runner := clusterRunnerWithFixtures()
	var stderr strings.Builder
	resources, config, code := loadClusterPlanWithRunner(runner, "", time.Second, "ingress-nginx/ingress-nginx-controller", &stderr)
	if code != 0 || len(resources) != 1 || config["ssl-redirect"] != "true" {
		t.Fatalf("code=%d resources=%#v config=%#v stderr=%q", code, resources, config, stderr.String())
	}
	if len(runner.calls) != 2 || strings.Join(runner.calls[0], " ") != "get ingress -A -o json" || strings.Join(runner.calls[1], " ") != "get configmap ingress-nginx-controller -n ingress-nginx -o json" {
		t.Fatalf("calls = %#v", runner.calls)
	}
}

func TestLoadClusterPlanWithRunnerErrors(t *testing.T) {
	kubeIngressFixture = readKubeFixture(t, "ingresses.json")
	runner := &clusterRunner{responses: map[string]clusterResponse{
		"get ingress -A -o json": {err: &kube.CommandError{Missing: true, Err: errors.New("kubectl not found")}},
	}}
	var stderr strings.Builder
	_, _, code := loadClusterPlanWithRunner(runner, "", time.Second, "", &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "kubectl not found") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}

	runner = &clusterRunner{responses: map[string]clusterResponse{
		"get ingress -A -o json": {err: &kube.CommandError{Exit: 1, Stderr: "https://user:secret@example.test " + strings.Repeat("x", 4<<10)}},
	}}
	stderr.Reset()
	_, _, code = loadClusterPlanWithRunner(runner, "", time.Second, "", &stderr)
	if code != 2 || strings.Contains(stderr.String(), "secret") || len(stderr.String()) > 2<<10+64 {
		t.Fatalf("code=%d stderr=%q len=%d", code, stderr.String(), len(stderr.String()))
	}

	runner = &clusterRunner{responses: map[string]clusterResponse{
		"get ingress -A -o json": {out: kubeIngressFixture},
		"get configmap ingress-nginx-controller -n ingress-nginx -o json": {err: &kube.CommandError{Exit: 1, Stderr: `Error from server (NotFound): configmaps "missing" not found`}},
	}}
	stderr.Reset()
	_, config, code := loadClusterPlanWithRunner(runner, "", time.Second, "ingress-nginx/ingress-nginx-controller", &stderr)
	if code != 0 || config != nil {
		t.Fatalf("missing configmap code=%d config=%#v stderr=%q", code, config, stderr.String())
	}
}

func TestLoadRunPlanFromClusterWarnings(t *testing.T) {
	kubeIngressFixture = readKubeFixture(t, "ingresses.json")
	kubeConfigMapFixture = readKubeFixture(t, "configmap.json")
	runner := clusterRunnerWithFixtures()
	var stderr strings.Builder
	result, code := loadRunPlanFromClusterWithRunner(runner, "", time.Second, "ingress-nginx/ingress-nginx-controller", plan.Options{}, &stderr)
	if code != 0 || len(result.Cases) == 0 || len(result.Warnings) == 0 {
		t.Fatalf("code=%d cases=%d warnings=%d stderr=%q", code, len(result.Cases), len(result.Warnings), stderr.String())
	}
	found := false
	for _, warning := range result.Warnings {
		if warning.Code == "VAR-PLAN-0010" && strings.Contains(warning.Message, "ssl-redirect=true") {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
}
