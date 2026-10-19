package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/plan"
)

func TestLoadRunPlanFromClusterGenericIgnoresConfigMapWarnings(t *testing.T) {
	kubeIngressFixture = readKubeFixture(t, "ingresses.json")
	kubeConfigMapFixture = readKubeFixture(t, "configmap.json")
	runner := clusterRunnerWithFixtures()
	var stderr strings.Builder
	result, code := loadRunPlanFromClusterWithRunner(runner, "", time.Second, "ingress-nginx/ingress-nginx-controller", plan.Options{Profile: plan.ProfileGeneric}, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	for _, warning := range result.Warnings {
		if warning.Code == diag.CodePlanConfigMap {
			t.Fatalf("generic profile emitted VAR-PLAN-0010: %#v", warning)
		}
	}
}
