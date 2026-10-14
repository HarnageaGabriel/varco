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
		{
			name: "nested list depth is bounded",
			input: `
apiVersion: v1
kind: List
items:
  - apiVersion: v1
    kind: List
    items:
      - apiVersion: v1
        kind: List
        items:
          - apiVersion: v1
            kind: List
            items:
              - apiVersion: v1
                kind: List
                items:
                  - apiVersion: v1
                    kind: List
                    items:
                      - apiVersion: v1
                        kind: List
                        items:
                          - apiVersion: v1
                            kind: List
                            items:
                              - apiVersion: v1
                                kind: List
                                items:
                                  - apiVersion: v1
                                    kind: List
                                    items: []
`,
			wantErr:    true,
			wantErrSub: "list nesting deeper",
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
