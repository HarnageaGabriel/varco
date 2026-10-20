package kube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/HarnageaGabriel/varco/internal/ingress"
)

// FuzzDecodeConfigMap feeds arbitrary bytes to the ConfigMap decoder of
// kube.FetchConfigMap and checks that a document is either decoded into the
// data map it declares or reported as an error, never raised as a panic.
//
// Sources cited per invariant:
//   - internal/kube/kube.go:166-190 (FetchConfigMap: name check, decode, data)
//   - internal/kube/kube.go:16-17 (MaxOutputBytes bounds one kubectl call)
//   - internal/kube/kube.go:87-118 (DefaultRunner refuses oversized output)
//   - testdata/kube/configmap.json (a ConfigMap kubectl returned)
func FuzzDecodeConfigMap(f *testing.F) {
	for _, seed := range fuzzConfigMapSeeds {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		runner := &fuzzRunner{out: data}
		config, err := FetchConfigMap(context.Background(), runner, "ingress-nginx", "ingress-nginx-controller")
		if got := strings.Join(runner.args, " "); got != "get configmap ingress-nginx-controller -n ingress-nginx -o json" {
			t.Fatalf("kubectl args = %q", got)
		}
		if err != nil {
			// A failure is returned, never raised, and never with data.
			if config != nil {
				t.Fatalf("failed decode returned data: %#v", config)
			}
			if errors.Is(err, errOutputTooLarge) {
				return
			}
			var syntaxError *json.SyntaxError
			var typeError *json.UnmarshalTypeError
			if !errors.As(err, &syntaxError) && !errors.As(err, &typeError) {
				t.Fatalf("error = %v, want a JSON error", err)
			}
			return
		}

		// An accepted document holds exactly the data it declares.
		var document struct {
			Data map[string]string `json:"data"`
		}
		if err := json.Unmarshal(data, &document); err != nil {
			t.Fatalf("loader accepted a document the decoder rejects: %v", err)
		}
		if !reflect.DeepEqual(config, document.Data) {
			t.Fatalf("data = %#v, want %#v", config, document.Data)
		}
	})
}

// FuzzFetchIngresses feeds arbitrary bytes to the List JSON path of
// kube.FetchIngresses and to the ingress parser that reads it, and checks that
// both report a bad document instead of raising it.
//
// Sources cited per invariant:
//   - internal/kube/kube.go:149-162 (FetchIngresses: the kubectl get call)
//   - internal/cli/plan.go:110-133 (loadClusterPlanWithRunner: fetch, then parse)
//   - internal/ingress/ingress.go:86-115 (Parse: bounded read, per-document errors)
//   - internal/ingress/ingress.go:25-26 (MaxInputBytes bounds the manifest)
//   - testdata/kube/ingresses.json (the List kubectl returned)
func FuzzFetchIngresses(f *testing.F) {
	for _, seed := range fuzzIngressSeeds {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		runner := &fuzzRunner{out: data}
		output, err := FetchIngresses(context.Background(), runner, "")
		if got := strings.Join(runner.args, " "); got != "get ingress -A -o json" {
			t.Fatalf("kubectl args = %q", got)
		}
		if err != nil {
			// The fake runner only fails on oversized output.
			if !errors.Is(err, errOutputTooLarge) {
				t.Fatalf("error = %v, want the output limit", err)
			}
			return
		}

		resources, err := ingress.Parse(bytes.NewReader(output))
		if err != nil {
			// A document the parser rejects is reported, never raised.
			return
		}
		// The parser reads at most MaxInputBytes, so a document it accepted
		// has to fit in that limit.
		if len(output) > ingress.MaxInputBytes {
			t.Fatalf("the parser accepted %d bytes, over the %d-byte limit", len(output), ingress.MaxInputBytes)
		}

		// The same bytes must always produce the same resources.
		again, err := ingress.Parse(bytes.NewReader(output))
		if err != nil {
			t.Fatalf("second parse failed: %v", err)
		}
		if !reflect.DeepEqual(resources, again) {
			t.Fatalf("parse is not deterministic: %#v vs %#v", resources, again)
		}
	})
}

// fuzzRunner is a kube.Runner that answers with the fuzz bytes. It mirrors
// DefaultRunner: output larger than MaxOutputBytes is refused instead of
// returned, so the fuzzed document never grows without a bound.
type fuzzRunner struct {
	out  []byte
	args []string
}

func (r *fuzzRunner) Run(_ context.Context, args []string) ([]byte, error) {
	r.args = append([]string(nil), args...)
	if len(r.out) > MaxOutputBytes {
		return nil, fmt.Errorf("kube: kubectl output exceeds %d bytes: %w", MaxOutputBytes, errOutputTooLarge)
	}
	return r.out, nil
}

// fuzzConfigMapSeeds are the hand-written seeds of the ConfigMap decoder. The
// same documents are committed under testdata/fuzz/FuzzDecodeConfigMap.
var fuzzConfigMapSeeds = []string{
	// A ConfigMap kubectl returned (testdata/kube/configmap.json).
	`{"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "ingress-nginx-controller", "namespace": "ingress-nginx"}, "data": {"ssl-redirect": "true", "unknown-key": "ignored"}}`,
	// A document without a data map.
	`{"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "ingress-nginx-controller"}}`,
	// A truncated document.
	`{"apiVersion": "v1", "kind": "ConfigMap", "data": {"ssl-redirect": "tru`,
	// A document followed by trailing garbage.
	`{"data": {}} trailing`,
	// Deeply nested arrays, past the depth the JSON decoder accepts.
	`{"data": ` + strings.Repeat("[", 10001) + strings.Repeat("]", 10001) + `}`,
	// A number too large for a string.
	`{"data": {"ssl-redirect": 99999999999999999999}}`,
	// The same key twice, with different values.
	`{"data": {"ssl-redirect": "true", "ssl-redirect": "false"}}`,
	// A data map of the wrong type.
	`{"data": "not-a-map"}`,
	// A NUL byte inside a string.
	`{"data": {"ssl-redirect": "true\u0000false"}}`,
	// A very long value.
	`{"data": {"ssl-redirect": "` + strings.Repeat("a", 16384) + `"}}`,
	// A null data map.
	`{"data": null}`,
	// A document that is not an object.
	`[]`,
	// An empty document.
	``,
}

// fuzzIngressSeeds are the hand-written seeds of the List JSON path. The same
// documents are committed under testdata/fuzz/FuzzFetchIngresses.
var fuzzIngressSeeds = []string{
	// A List kubectl returned (testdata/kube/ingresses.json).
	`{"apiVersion": "v1", "kind": "List", "items": [{"apiVersion": "networking.k8s.io/v1", "kind": "Ingress", "metadata": {"name": "web", "namespace": "apps"}, "spec": {"rules": [{"host": "web.example.test", "http": {"paths": [{"path": "/", "pathType": "Prefix"}]}}]}}]}`,
	// A single Ingress, not a List.
	`{"apiVersion": "networking.k8s.io/v1", "kind": "Ingress", "metadata": {"name": "web", "namespace": "apps"}, "spec": {"rules": [{"host": "web.example.test"}]}}`,
	// An Ingress varco does not support.
	`{"apiVersion": "extensions/v1beta1", "kind": "Ingress", "metadata": {"name": "legacy"}}`,
	// A truncated document.
	`{"apiVersion": "v1", "kind": "List", "items": [{"apiVersion": "networking.k8s.io/v1"`,
	// A document followed by trailing garbage.
	`{"kind": "List", "items": []} trailing`,
	// Deeply nested arrays, past the depth the YAML parser accepts.
	`{"kind": "List", "items": ` + strings.Repeat("[", 10001) + strings.Repeat("]", 10001) + `}`,
	// A number too large for a string.
	`{"kind": "List", "items": [{"metadata": {"name": 99999999999999999999}}]}`,
	// The same key twice, with different values.
	`{"kind": "List", "kind": "Ingress", "items": []}`,
	// A field of the wrong type.
	`{"kind": "List", "items": "not-a-list"}`,
	// A NUL byte inside a string.
	`{"kind": "List", "items": [{"metadata": {"name": "we\u0000b"}}]}`,
	// A very long annotation.
	`{"kind": "List", "items": [{"metadata": {"name": "web", "annotations": {"nginx.ingress.kubernetes.io/proxy-body-size": "` + strings.Repeat("a", 16384) + `"}}}]}`,
	// A document that is not an object.
	`[]`,
	// An empty document.
	``,
}
