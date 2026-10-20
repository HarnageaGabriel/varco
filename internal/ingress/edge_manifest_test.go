package ingress

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/HarnageaGabriel/varco/internal/diag"
)

// edgeFixture reads a manifest corpus from testdata/edge.
func edgeFixture(t *testing.T, name string) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", "edge", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return content
}

// edgeManifestError asserts that err is the VAR-PLAN-0003 diagnostic with the
// exact message. Sources: internal/ingress/ingress.go manifestError (every
// manifest failure carries the VAR-PLAN-0003 code) and
// docs/diagnostics/VAR-PLAN-0003.md.
func edgeManifestError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("Parse() error = nil, want %q", want)
	}
	if err.Error() != want {
		t.Fatalf("Parse() error = %q, want %q", err, want)
	}
	var diagnostic *diag.Error
	if !errors.As(err, &diagnostic) {
		t.Fatalf("Parse() error %v does not carry a diagnostic", err)
	}
	if diagnostic.Diagnostic.Code != diag.CodePlanManifest {
		t.Fatalf("diagnostic code = %s, want %s", diagnostic.Diagnostic.Code, diag.CodePlanManifest)
	}
}

// TestParseEncodingEdges covers the encoding and whitespace edges of the
// manifest boundary: a byte order mark, CRLF line breaks, a tab used for
// indentation, trailing whitespace and the streams that hold no object at
// all. Sources: the YAML 1.2 specification (a stream may start with a byte
// order mark, may use CRLF line breaks, may not indent with a tab and may
// hold several documents) and docs/plan.md (a multi-document YAML stream is
// accepted).
func TestParseEncodingEdges(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []Ingress
		wantErr string
	}{
		{
			name:  "utf-8 byte order mark at the start",
			input: "\ufeffapiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata:\n  name: bom\n",
			want:  []Ingress{{APIVersion: "networking.k8s.io/v1", Kind: "Ingress", Name: "bom"}},
		},
		{
			name:  "crlf line endings",
			input: "apiVersion: networking.k8s.io/v1\r\nkind: Ingress\r\nmetadata:\r\n  name: crlf\r\n",
			want:  []Ingress{{APIVersion: "networking.k8s.io/v1", Kind: "Ingress", Name: "crlf"}},
		},
		{
			name:    "tab used for indentation",
			input:   "apiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata:\n\tname: tab\n",
			wantErr: "VAR-PLAN-0003: document 1: yaml: line 4: found character that cannot start any token",
		},
		{
			name:  "trailing whitespace",
			input: "apiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata:\n  name: trailing   \nspec:\n  ingressClassName: nginx   \n",
			want:  []Ingress{{APIVersion: "networking.k8s.io/v1", Kind: "Ingress", Name: "trailing", ClassName: "nginx"}},
		},
		{
			name:  "only comments",
			input: "# a comment\n# another comment\n",
			want:  nil,
		},
		{
			name:  "empty file",
			input: "",
			want:  nil,
		},
		{
			name:  "whitespace only",
			input: "   \n  \n",
			want:  nil,
		},
		{
			name:  "document separator at the start and at the end",
			input: "---\napiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata:\n  name: separated\n---\n",
			want:  []Ingress{{APIVersion: "networking.k8s.io/v1", Kind: "Ingress", Name: "separated"}},
		},
		{
			name:  "empty document between two separators",
			input: "---\n\n---\napiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata:\n  name: between\n---\n",
			want:  []Ingress{{APIVersion: "networking.k8s.io/v1", Kind: "Ingress", Name: "between"}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Parse(strings.NewReader(test.input))
			if test.wantErr != "" {
				edgeManifestError(t, err, test.wantErr)
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

// TestParseStructureEdges covers the structural edges of the manifest
// boundary: a List that holds a List, a List that mixes other kinds, the API
// versions varco refuses and the Ingress shapes that declare no rule or no
// path. Sources: docs/plan.md (the accepted manifests, the ignored kinds and
// the rejected API versions) and the Kubernetes Ingress API reference
// (https://kubernetes.io/docs/reference/kubernetes-api/networking/v1/).
func TestParseStructureEdges(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []Ingress
		wantErr string
	}{
		{
			name:  "list holding a nested list and other kinds",
			input: string(edgeFixture(t, "list-nested.yaml")),
			want: []Ingress{
				{
					APIVersion: "networking.k8s.io/v1",
					Kind:       "Ingress",
					Namespace:  "edge",
					Name:       "nested",
					Rules:      []Rule{{Host: "nested.example.com", Paths: []Path{{Path: "/nested", PathType: "Prefix"}}}},
				},
				{
					APIVersion: "networking.k8s.io/v1",
					Kind:       "Ingress",
					Namespace:  "edge",
					Name:       "sibling",
					Rules:      []Rule{{Host: "sibling.example.com", Paths: []Path{{Path: "/sibling", PathType: "Prefix"}}}},
				},
			},
		},
		{
			name: "list mixing a service and a config map with an ingress",
			input: `apiVersion: v1
kind: List
items:
  - apiVersion: v1
    kind: Service
    metadata:
      name: checkout
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: settings
  - apiVersion: networking.k8s.io/v1
    kind: Ingress
    metadata:
      name: only
    spec:
      rules:
        - host: only.example.com
          http:
            paths:
              - path: /only
                pathType: Prefix
`,
			want: []Ingress{{
				APIVersion: "networking.k8s.io/v1",
				Kind:       "Ingress",
				Name:       "only",
				Rules:      []Rule{{Host: "only.example.com", Paths: []Path{{Path: "/only", PathType: "Prefix"}}}},
			}},
		},
		{
			name: "extensions v1beta1 is rejected",
			input: `apiVersion: extensions/v1beta1
kind: Ingress
metadata:
  name: legacy
`,
			wantErr: `VAR-PLAN-0003: document 1: unsupported Ingress apiVersion "extensions/v1beta1"; only networking.k8s.io/v1 is supported`,
		},
		{
			name: "networking v1beta1 is rejected",
			input: `apiVersion: networking.k8s.io/v1beta1
kind: Ingress
metadata:
  name: beta
`,
			wantErr: `VAR-PLAN-0003: document 1: unsupported Ingress apiVersion "networking.k8s.io/v1beta1"; only networking.k8s.io/v1 is supported`,
		},
		{
			name: "ingress without a spec",
			input: `apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: no-spec
`,
			want: []Ingress{{APIVersion: "networking.k8s.io/v1", Kind: "Ingress", Name: "no-spec"}},
		},
		{
			name: "ingress with a spec but no rules",
			input: `apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: no-rules
spec:
  ingressClassName: nginx
`,
			want: []Ingress{{APIVersion: "networking.k8s.io/v1", Kind: "Ingress", Name: "no-rules", ClassName: "nginx"}},
		},
		{
			name: "rule without an http block",
			input: `apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: no-http
spec:
  rules:
    - host: tcp.example.com
`,
			want: []Ingress{{
				APIVersion: "networking.k8s.io/v1",
				Kind:       "Ingress",
				Name:       "no-http",
				Rules:      []Rule{{Host: "tcp.example.com"}},
			}},
		},
		{
			name: "path without a path type",
			input: `apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: no-path-type
spec:
  rules:
    - host: typed.example.com
      http:
        paths:
          - path: /typed
`,
			want: []Ingress{{
				APIVersion: "networking.k8s.io/v1",
				Kind:       "Ingress",
				Name:       "no-path-type",
				Rules:      []Rule{{Host: "typed.example.com", Paths: []Path{{Path: "/typed"}}}},
			}},
		},
		{
			name: "duplicate keys in a mapping",
			input: `apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: duplicate
  name: duplicate-again
`,
			wantErr: "VAR-PLAN-0003: document 1: cannot decode resource: yaml: unmarshal errors:\n  line 5: mapping key \"name\" already defined at line 4",
		},
		{
			name: "duplicate keys at the root of the document",
			input: `apiVersion: networking.k8s.io/v1
kind: Ingress
apiVersion: networking.k8s.io/v1
metadata:
  name: duplicate
`,
			wantErr: "VAR-PLAN-0003: document 1: cannot decode resource header: yaml: unmarshal errors:\n  line 3: mapping key \"apiVersion\" already defined at line 1",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Parse(strings.NewReader(test.input))
			if test.wantErr != "" {
				edgeManifestError(t, err, test.wantErr)
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

// TestParseIngressClassSources covers the two ways an Ingress declares its
// class: spec.ingressClassName and the legacy kubernetes.io/ingress.class
// annotation. The parser keeps them apart; the planner accepts either one
// (internal/plan/generate.go matchesClass). Sources: docs/plan.md (the
// --ingress-class flag matches spec.ingressClassName or the
// kubernetes.io/ingress.class annotation) and the Kubernetes Ingress API
// reference (https://kubernetes.io/docs/reference/kubernetes-api/networking/v1/).
func TestParseIngressClassSources(t *testing.T) {
	annotated, err := Parse(bytes.NewReader(edgeFixture(t, "class-annotation.yaml")))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(annotated) != 1 {
		t.Fatalf("resources = %#v, want one Ingress", annotated)
	}
	if annotated[0].ClassName != "" {
		t.Fatalf("ClassName = %q, want the empty string", annotated[0].ClassName)
	}
	if annotated[0].Annotations["kubernetes.io/ingress.class"] != "nginx" {
		t.Fatalf("annotations = %#v, want the class annotation", annotated[0].Annotations)
	}

	specified, err := Parse(bytes.NewReader(edgeFixture(t, "class-spec.yaml")))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(specified) != 1 {
		t.Fatalf("resources = %#v, want one Ingress", specified)
	}
	if specified[0].ClassName != "nginx" {
		t.Fatalf("ClassName = %q, want nginx", specified[0].ClassName)
	}
	if len(specified[0].Annotations) != 0 {
		t.Fatalf("annotations = %#v, want none", specified[0].Annotations)
	}
}

// TestParseHostAndPathEdges covers the hosts and paths the parser copies
// verbatim: a unicode host and path, an internationalized host, an upper-case
// host, a host with a trailing dot, a wildcard host, a percent-encoded path
// and a path with a space. Sources: internal/ingress/ingress.go (the parser
// copies the declared strings), RFC 3986 section 3.2.2 (a host is
// case-insensitive), RFC 1035 section 3.5 (the trailing dot is the fully
// qualified form of a name) and RFC 3986 section 6.2.2.1 (a path may carry
// percent-encoded octets).
func TestParseHostAndPathEdges(t *testing.T) {
	want := []struct {
		name string
		host string
		path string
	}{
		{name: "unicode", host: "café.example.com", path: "/café/☕"},
		{name: "idn", host: "münchen.example.com", path: "/münchen"},
		{name: "uppercase", host: "API.Example.COM", path: "/upper"},
		{name: "trailing-dot", host: "api.example.com.", path: "/trailing"},
		{name: "wildcard", host: "*.example.com", path: "/wildcard"},
		{name: "encoded", host: "encoded.example.com", path: "/a%2Fb%20c"},
		{name: "spaced", host: "spaced.example.com", path: "/a b"},
	}
	got, err := Parse(bytes.NewReader(edgeFixture(t, "hosts.yaml")))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("resources = %d, want %d", len(got), len(want))
	}
	for index, item := range want {
		resource := got[index]
		if resource.Name != item.name {
			t.Fatalf("resource %d name = %q, want %q", index, resource.Name, item.name)
		}
		if len(resource.Rules) != 1 {
			t.Fatalf("resource %d rules = %#v, want one", index, resource.Rules)
		}
		if resource.Rules[0].Host != item.host {
			t.Fatalf("resource %d host = %q, want %q", index, resource.Rules[0].Host, item.host)
		}
		if len(resource.Rules[0].Paths) != 1 {
			t.Fatalf("resource %d paths = %#v, want one", index, resource.Rules[0].Paths)
		}
		if resource.Rules[0].Paths[0].Path != item.path {
			t.Fatalf("resource %d path = %q, want %q", index, resource.Rules[0].Paths[0].Path, item.path)
		}
	}
}

// TestParseAnnotationScalarEdges covers the annotation values the parser
// accepts. Sources: internal/ingress/ingress.go stringifyAnnotation (a scalar
// annotation is accepted and stringified, a non-scalar is a manifest error
// naming the field) and docs/plan.md (VAR-PLAN-0003 covers an annotation that
// is not a scalar).
func TestParseAnnotationScalarEdges(t *testing.T) {
	tests := []struct {
		name        string
		annotations string
		want        map[string]string
		wantErr     string
	}{
		{name: "number", annotations: "weight: 10", want: map[string]string{"weight": "10"}},
		{name: "float", annotations: "ratio: 0.5", want: map[string]string{"ratio": "0.5"}},
		{name: "boolean", annotations: "enable-cors: true", want: map[string]string{"enable-cors": "true"}},
		{name: "null", annotations: "empty:", want: map[string]string{"empty": ""}},
		{name: "quoted string", annotations: `note: "true"`, want: map[string]string{"note": "true"}},
		{name: "quoted number", annotations: `port: "8080"`, want: map[string]string{"port": "8080"}},
		{
			name:        "mapping",
			annotations: "bad:\n  nested: 1",
			wantErr:     `VAR-PLAN-0003: document 1: metadata.annotations["bad"] must be a scalar value`,
		},
		{
			name:        "sequence",
			annotations: "bad: [a, b]",
			wantErr:     `VAR-PLAN-0003: document 1: metadata.annotations["bad"] must be a scalar value`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := "apiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata:\n  name: a\n  annotations:\n    " +
				strings.ReplaceAll(test.annotations, "\n", "\n    ") + "\nspec: {}\n"
			got, err := Parse(strings.NewReader(input))
			if test.wantErr != "" {
				edgeManifestError(t, err, test.wantErr)
				return
			}
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if len(got) != 1 || !reflect.DeepEqual(got[0].Annotations, test.want) {
				t.Fatalf("annotations = %#v, want %#v", got, test.want)
			}
		})
	}
}

// TestParseAnnotationScalarFixture covers the annotation scalars of the
// corpus in testdata/edge/scalars.yaml: an unquoted boolean, number and
// float, a quoted number and boolean, and an empty value.
func TestParseAnnotationScalarFixture(t *testing.T) {
	got, err := Parse(bytes.NewReader(edgeFixture(t, "scalars.yaml")))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("resources = %#v, want one Ingress", got)
	}
	want := map[string]string{
		"nginx.ingress.kubernetes.io/enable-cors":    "true",
		"nginx.ingress.kubernetes.io/weight":         "10",
		"nginx.ingress.kubernetes.io/ratio":          "0.5",
		"nginx.ingress.kubernetes.io/quoted-number":  "8080",
		"nginx.ingress.kubernetes.io/quoted-boolean": "true",
		"nginx.ingress.kubernetes.io/empty":          "",
	}
	if !reflect.DeepEqual(got[0].Annotations, want) {
		t.Fatalf("annotations = %#v, want %#v", got[0].Annotations, want)
	}
}

// TestParseAnchorAndAliasEdges covers YAML anchors and aliases. Sources: the
// YAML 1.2 specification (an alias refers to the node its anchor names) and
// the alias-expansion guard of the decoder varco reads
// (go.yaml.in/yaml/v3 decode.go, the aliasCount and allowedAliasRatio check),
// which refuses a document whose aliases expand far beyond the data they
// point at.
func TestParseAnchorAndAliasEdges(t *testing.T) {
	t.Run("aliases inside the documented limits", func(t *testing.T) {
		input := `apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: anchors
  annotations:
    host: &host shared.example.com
    copy: *host
spec:
  rules:
    - host: *host
      http:
        paths:
          - path: /anchors
            pathType: Prefix
`
		got, err := Parse(strings.NewReader(input))
		if err != nil {
			t.Fatalf("Parse() error = %v", err)
		}
		want := []Ingress{{
			APIVersion:  "networking.k8s.io/v1",
			Kind:        "Ingress",
			Name:        "anchors",
			Annotations: map[string]string{"host": "shared.example.com", "copy": "shared.example.com"},
			Rules:       []Rule{{Host: "shared.example.com", Paths: []Path{{Path: "/anchors", PathType: "Prefix"}}}},
		}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Parse() = %#v, want %#v", got, want)
		}
	})
	t.Run("aliases over the decoder alias limit", func(t *testing.T) {
		_, err := Parse(strings.NewReader(edgeAliasBomb(200, 1000)))
		edgeManifestError(t, err, "VAR-PLAN-0003: document 1: cannot decode resource: yaml: document contains excessive aliasing")
	})
}

// edgeAliasBomb returns a manifest whose aliases expand far beyond the data
// they point at: one anchored mapping with entries entries, referenced
// aliases times. The YAML decoder refuses the document once the alias-driven
// decodes dominate it (go.yaml.in/yaml/v3 decode.go).
func edgeAliasBomb(entries, aliases int) string {
	var builder strings.Builder
	builder.WriteString("apiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata:\n  name: bomb\n  annotations:\n    base: &bomb\n")
	for index := 0; index < entries; index++ {
		fmt.Fprintf(&builder, "      key%04d: value%04d\n", index, index)
	}
	for index := 0; index < aliases; index++ {
		fmt.Fprintf(&builder, "    alias%04d: *bomb\n", index)
	}
	return builder.String()
}

// deepManifest returns a manifest whose deepest YAML node sits at depth 64
// when levels is 60: the document node is depth 1, the root mapping depth 2,
// every mapping value depth 3 and every nested mapping adds one level. The
// limit is the maximum depth of 64 (internal/ingress/ingress.go
// maxDocumentDepth).
func deepManifest(levels int) string {
	var builder strings.Builder
	builder.WriteString("apiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata:\n  name: deep\nx-padding:\n")
	indent := "  "
	for index := 0; index < levels; index++ {
		builder.WriteString(indent + "k:\n")
		indent += "  "
	}
	builder.WriteString(indent + "leaf: 1\n")
	return builder.String()
}

// nodeManifest returns a manifest with exactly 12+elements YAML nodes: the
// document node, the root mapping, the apiVersion, kind and name pairs, the
// metadata mapping, the x-pad key and the sequence that holds the elements.
// The limit is the 100000-node limit (internal/ingress/ingress.go
// maxDocumentNodes).
func nodeManifest(elements int) string {
	var builder strings.Builder
	builder.WriteString("apiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata:\n  name: nodes\nx-pad: [")
	for index := 0; index < elements; index++ {
		if index > 0 {
			builder.WriteString(",")
		}
		builder.WriteString("0")
	}
	builder.WriteString("]\n")
	return builder.String()
}

// TestParseDepthLimitEdges covers the nesting limit at its exact boundary:
// a document whose deepest node is at depth 64 is read, one level deeper is
// refused. Source: internal/ingress/ingress.go validateDocument (the depth
// limit is 64) and docs/troubleshooting.md (a manifest that cannot be parsed
// is VAR-PLAN-0003).
func TestParseDepthLimitEdges(t *testing.T) {
	t.Run("exactly the depth limit", func(t *testing.T) {
		got, err := Parse(strings.NewReader(deepManifest(60)))
		if err != nil {
			t.Fatalf("Parse() error = %v", err)
		}
		if len(got) != 1 || got[0].Name != "deep" {
			t.Fatalf("resources = %#v, want one Ingress named deep", got)
		}
	})
	t.Run("one level over the depth limit", func(t *testing.T) {
		_, err := Parse(strings.NewReader(deepManifest(61)))
		edgeManifestError(t, err, "VAR-PLAN-0003: document 1: YAML document nesting exceeds the maximum depth of 64")
	})
}

// TestParseNodeLimitEdges covers the node limit at its exact boundary: a
// document with 100000 nodes is read, one node more is refused. Source:
// internal/ingress/ingress.go validateDocument (the node limit is 100000).
func TestParseNodeLimitEdges(t *testing.T) {
	t.Run("exactly the node limit", func(t *testing.T) {
		got, err := Parse(strings.NewReader(nodeManifest(99988)))
		if err != nil {
			t.Fatalf("Parse() error = %v", err)
		}
		if len(got) != 1 || got[0].Name != "nodes" {
			t.Fatalf("resources = %#v, want one Ingress named nodes", got)
		}
	})
	t.Run("one node over the limit", func(t *testing.T) {
		_, err := Parse(strings.NewReader(nodeManifest(99989)))
		edgeManifestError(t, err, "VAR-PLAN-0003: document 1: YAML document exceeds the 100000-node limit")
	})
}

// TestParseSizeLimitEdges covers the input cap at its exact boundary: an
// input of exactly 8 MiB is read, one byte more is refused. Source:
// internal/ingress/ingress.go MaxInputBytes (the cap is 8 MiB) and
// docs/plan.md (an input larger than the cap is VAR-PLAN-0003).
func TestParseSizeLimitEdges(t *testing.T) {
	t.Run("exactly the limit", func(t *testing.T) {
		content := "#" + strings.Repeat("a", MaxInputBytes-1)
		if len(content) != MaxInputBytes {
			t.Fatalf("content is %d bytes, want %d", len(content), MaxInputBytes)
		}
		got, err := Parse(strings.NewReader(content))
		if err != nil {
			t.Fatalf("Parse() error = %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("resources = %#v, want none", got)
		}
	})
	t.Run("one byte over the limit", func(t *testing.T) {
		content := "#" + strings.Repeat("a", MaxInputBytes)
		if len(content) != MaxInputBytes+1 {
			t.Fatalf("content is %d bytes, want %d", len(content), MaxInputBytes+1)
		}
		_, err := Parse(strings.NewReader(content))
		edgeManifestError(t, err, "VAR-PLAN-0003: manifest input exceeds the 8388608-byte limit")
	})
}
