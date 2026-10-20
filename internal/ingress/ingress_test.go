package ingress

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/HarnageaGabriel/varco/internal/diag"
)

// Every case cites its source: the Kubernetes Ingress API reference and the
// ingress-nginx retirement guidance at
// https://kubernetes.io/blog/2026/02/27/ingress-nginx-before-you-migrate.
func TestParseDocuments(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		want       []Ingress
		wantErr    bool
		wantErrSub string
	}{
		{
			name: "single v1 ingress",
			input: `
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  namespace: prod
  name: checkout
  annotations:
    nginx.ingress.kubernetes.io/use-regex: "true"
spec:
  ingressClassName: nginx
  tls:
    - hosts: [api.example.com]
      secretName: api-tls
  rules:
    - host: api.example.com
      http:
        paths:
          - path: /api
            pathType: Prefix
            backend:
              service:
                name: checkout
                port:
                  number: 80
`,
			want: []Ingress{{
				APIVersion:  "networking.k8s.io/v1",
				Kind:        "Ingress",
				Namespace:   "prod",
				Name:        "checkout",
				ClassName:   "nginx",
				Annotations: map[string]string{"nginx.ingress.kubernetes.io/use-regex": "true"},
				Rules:       []Rule{{Host: "api.example.com", Paths: []Path{{Path: "/api", PathType: "Prefix"}}}},
				TLS:         []TLS{{Hosts: []string{"api.example.com"}, SecretName: "api-tls"}},
			}},
		},
		{
			name: "list kind with a non-ingress item",
			input: `
apiVersion: v1
kind: List
items:
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: settings
  - apiVersion: networking.k8s.io/v1
    kind: Ingress
    metadata:
      name: status
    spec:
      rules:
        - host: status.example.com
          http:
            paths:
              - path: /status
                pathType: Prefix
`,
			want: []Ingress{{
				APIVersion: "networking.k8s.io/v1",
				Kind:       "Ingress",
				Name:       "status",
				Rules:      []Rule{{Host: "status.example.com", Paths: []Path{{Path: "/status", PathType: "Prefix"}}}},
			}},
		},
		{
			name: "multi document stream with ignored kinds and empty documents",
			input: `---
# comment only document
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: web
spec:
  rules:
    - host: web.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
---
null
`,
			want: []Ingress{{
				APIVersion: "networking.k8s.io/v1",
				Kind:       "Ingress",
				Name:       "web",
				Rules:      []Rule{{Host: "web.example.com", Paths: []Path{{Path: "/", PathType: "Prefix"}}}},
			}},
		},
		{
			name: "rule without http keeps an empty path list",
			input: `
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: tcp-only
spec:
  rules:
    - host: tcp.example.com
`,
			want: []Ingress{{
				APIVersion: "networking.k8s.io/v1",
				Kind:       "Ingress",
				Name:       "tcp-only",
				Rules:      []Rule{{Host: "tcp.example.com"}},
			}},
		},
		{
			name:  "json document",
			input: `{"apiVersion":"networking.k8s.io/v1","kind":"Ingress","metadata":{"namespace":"j","name":"json"},"spec":{"rules":[{"host":"json.example.com","http":{"paths":[{"path":"/j","pathType":"Prefix"}]}}]}}`,
			want: []Ingress{{
				APIVersion: "networking.k8s.io/v1",
				Kind:       "Ingress",
				Namespace:  "j",
				Name:       "json",
				Rules:      []Rule{{Host: "json.example.com", Paths: []Path{{Path: "/j", PathType: "Prefix"}}}},
			}},
		},
		{
			name:  "empty input",
			input: "",
			want:  nil,
		},
		{
			name: "extensions v1beta1 is rejected",
			input: `
apiVersion: extensions/v1beta1
kind: Ingress
metadata:
  name: legacy
`,
			wantErr:    true,
			wantErrSub: `VAR-PLAN-0003`,
		},
		{
			name: "networking v1beta1 is rejected",
			input: `
apiVersion: networking.k8s.io/v1beta1
kind: Ingress
metadata:
  name: beta
`,
			wantErr:    true,
			wantErrSub: "networking.k8s.io/v1beta1",
		},
		{
			name: "sequence document is rejected",
			input: `
- apiVersion: networking.k8s.io/v1
`,
			wantErr:    true,
			wantErrSub: "not a Kubernetes object",
		},
		{
			name: "malformed yaml is rejected",
			input: `
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: [unterminated
`,
			wantErr:    true,
			wantErrSub: "VAR-PLAN-0003",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Parse(strings.NewReader(test.input))
			if test.wantErr {
				if err == nil {
					t.Fatalf("Parse() error = nil, want an error")
				}
				if !strings.Contains(err.Error(), test.wantErrSub) {
					t.Fatalf("Parse() error = %q, want substring %q", err, test.wantErrSub)
				}
				var diagnostic *diag.Error
				if !errors.As(err, &diagnostic) {
					t.Fatalf("Parse() error %v does not carry a diagnostic", err)
				}
				if diagnostic.Diagnostic.Code != diag.CodePlanManifest {
					t.Fatalf("diagnostic code = %s, want %s", diagnostic.Diagnostic.Code, diag.CodePlanManifest)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("Parse() = %#v, want %#v", got, test.want)
			}
		})
	}
}

// Source: internal/ingress/ingress.go documentError; every manifest failure
// names the diagnostic code first and the document after it, so a decode
// error, a validation error and a parse error read the same way:
// VAR-PLAN-0003: document 1: ...
func TestManifestErrorsPutTheCodeFirst(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "yaml decode error",
			input: "apiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata:\n\tname: tab\n",
		},
		{
			name:  "unsupported api version",
			input: "apiVersion: extensions/v1beta1\nkind: Ingress\nmetadata:\n  name: legacy\n",
		},
		{
			name:  "annotation is not a scalar",
			input: "apiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata:\n  name: annotated\n  annotations:\n    bad:\n      nested: value\n",
		},
		{
			name:  "document is not a kubernetes object",
			input: "- apiVersion: networking.k8s.io/v1\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(test.input))
			if err == nil {
				t.Fatal("Parse() error = nil, want a manifest error")
			}
			if !strings.HasPrefix(err.Error(), "VAR-PLAN-0003: document 1: ") {
				t.Fatalf("Parse() error = %q, want the code first", err)
			}
			var diagnostic *diag.Error
			if !errors.As(err, &diagnostic) {
				t.Fatalf("Parse() error %v does not carry a diagnostic", err)
			}
			if diagnostic.Diagnostic.Code != diag.CodePlanManifest {
				t.Fatalf("diagnostic code = %s, want %s", diagnostic.Diagnostic.Code, diag.CodePlanManifest)
			}
			if !strings.HasPrefix(diagnostic.Diagnostic.Message, "document 1: ") {
				t.Fatalf("diagnostic message = %q, want the document first", diagnostic.Diagnostic.Message)
			}
		})
	}
}

func TestParseLimits(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		message string
	}{
		{
			name:    "deep flow sequence",
			input:   strings.Repeat("[", 10000) + "0" + strings.Repeat("]", 10000),
			message: "maximum depth of 64",
		},
		{
			name:    "many nodes",
			input:   "[" + strings.Repeat("0,", maxDocumentNodes) + "0]",
			message: "100000-node limit",
		},
		{
			name:    "oversized input",
			input:   "#" + strings.Repeat("a", MaxInputBytes),
			message: "8388608-byte limit",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(test.input))
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("Parse() error = %v, want message containing %q", err, test.message)
			}
			var diagnostic *diag.Error
			if !errors.As(err, &diagnostic) || diagnostic.Diagnostic.Code != diag.CodePlanManifest {
				t.Fatalf("Parse() error = %v, want %s", err, diag.CodePlanManifest)
			}
		})
	}
}

func TestIngressKey(t *testing.T) {
	if got := (Ingress{Name: "api"}).Key(); got != "api" {
		t.Fatalf("Key() = %q, want api", got)
	}
	if got := (Ingress{Namespace: "prod", Name: "api"}).Key(); got != "prod/api" {
		t.Fatalf("Key() = %q, want prod/api", got)
	}
}

func TestStringifyAnnotationScalarTypes(t *testing.T) {
	tests := []struct {
		value any
		want  string
	}{
		{value: "value", want: "value"},
		{value: true, want: "true"},
		{value: int(2), want: "2"},
		{value: int64(-3), want: "-3"},
		{value: uint64(4), want: "4"},
		{value: float64(1.25), want: "1.25"},
	}
	for _, test := range tests {
		got, err := stringifyAnnotation("test", test.value)
		if err != nil || got != test.want {
			t.Fatalf("stringifyAnnotation(%T(%v)) = %q, %v; want %q, nil", test.value, test.value, got, err, test.want)
		}
	}
}

// TestParseIsDeterministic guards the convention that the same manifest always
// yields the same resources, in the same order.
func TestParseIsDeterministic(t *testing.T) {
	input := `
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: one
  annotations:
    a: "1"
    b: "2"
spec:
  rules:
    - host: one.example.com
      http:
        paths:
          - path: /one
            pathType: Prefix
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: two
`
	first, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("Parse() is not deterministic: %#v vs %#v", first, second)
	}
}

// TestParseAnnotationScalars covers accepted finding Minor 3: annotations are
// decoded as map[string]any and scalars are stringified, so an unquoted
// `enable-cors: true` works and a non-scalar is a targeted VAR-PLAN-0003.
func TestParseAnnotationScalars(t *testing.T) {
	tests := []struct {
		name        string
		annotations string
		want        map[string]string
		wantErr     bool
	}{
		{name: "unquoted bool", annotations: "enable-cors: true", want: map[string]string{"enable-cors": "true"}},
		{name: "unquoted int", annotations: "weight: 10", want: map[string]string{"weight": "10"}},
		{name: "unquoted float", annotations: "ratio: 0.5", want: map[string]string{"ratio": "0.5"}},
		{name: "null is empty", annotations: "empty:", want: map[string]string{"empty": ""}},
		{name: "mapping is an error", annotations: "bad:\n  nested: 1", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := "apiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata:\n  name: a\n  annotations:\n    " +
				strings.ReplaceAll(test.annotations, "\n", "\n    ") + "\nspec: {}\n"
			got, err := Parse(strings.NewReader(input))
			if test.wantErr {
				if err == nil || !strings.Contains(err.Error(), "VAR-PLAN-0003") {
					t.Fatalf("Parse() error = %v, want VAR-PLAN-0003", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || !reflect.DeepEqual(got[0].Annotations, test.want) {
				t.Fatalf("annotations = %#v, want %#v", got, test.want)
			}
		})
	}
}

// TestParseIgnoresNonIngressWithUnexpectedTypes covers accepted finding Minor 3:
// the kind is checked before the body is decoded, so a non-Ingress document
// whose fields do not match the Ingress schema never aborts the plan.
func TestParseIgnoresNonIngressWithUnexpectedTypes(t *testing.T) {
	input := `apiVersion: v1
kind: ConfigMap
metadata:
  name: settings
  annotations:
    retries: 3
    hosts: [a, b]
data:
  nested: {a: [1, 2]}
`
	got, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("a non-Ingress document must be ignored: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("resources = %#v, want none", got)
	}
}

// TestParseDefaultBackend covers accepted finding Minor 5: spec.defaultBackend is
// recorded so plan can report an Ingress that serves only the default backend.
func TestParseDefaultBackend(t *testing.T) {
	input := `apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: catch-all
spec:
  defaultBackend:
    service:
      name: fallback
      port:
        number: 80
`
	got, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].DefaultBackend {
		t.Fatalf("resources = %#v, want one Ingress with DefaultBackend", got)
	}
}
