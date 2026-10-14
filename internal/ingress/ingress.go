// Package ingress parses Kubernetes Ingress manifests into a small model.
//
// The parser is pure: callers pass bytes, the package performs no I/O and
// never reads the network. It understands apiVersion networking.k8s.io/v1 with
// kind Ingress, kind List, and multi-document streams; other kinds are ignored
// so a whole `kubectl get -o yaml` export can be fed in.
package ingress

import (
	"errors"
	"fmt"
	"io"

	"go.yaml.in/yaml/v3"

	"github.com/HarnageaGabriel/varco/internal/diag"
)

// APIVersion is the only Ingress API version varco understands.
const APIVersion = "networking.k8s.io/v1"

// Recognized resource kinds.
const (
	// KindIngress is the Kubernetes Ingress kind.
	KindIngress = "Ingress"
	// KindList is a Kubernetes list, for example the output of `kubectl get -o yaml`.
	KindList = "List"
)

// maxDocumentDepth bounds List nesting so a crafted manifest cannot recurse forever.
const maxDocumentDepth = 8

// Ingress is one Ingress resource reduced to the fields varco plans from.
type Ingress struct {
	APIVersion  string
	Kind        string
	Namespace   string
	Name        string
	ClassName   string
	Annotations map[string]string
	Rules       []Rule
	TLS         []TLS
}

// Key returns the "namespace/name" identity used by plan sources and findings.
// It is the bare name when the manifest declares no namespace.
func (i Ingress) Key() string {
	if i.Namespace == "" {
		return i.Name
	}
	return i.Namespace + "/" + i.Name
}

// Rule is one spec.rules entry.
type Rule struct {
	Host  string
	Paths []Path
}

// Path is one HTTP path entry of a rule.
type Path struct {
	Path     string
	PathType string
}

// TLS is one spec.tls entry.
type TLS struct {
	Hosts      []string
	SecretName string
}

// Parse decodes every document in r and returns the Ingress resources found, in
// document order. Documents of other kinds and API groups are ignored. An
// Ingress that uses an API version other than networking.k8s.io/v1 is rejected
// with a VAR-PLAN-0003 diagnostic, so extensions/v1beta1 is never silently
// planned with v1 semantics.
func Parse(r io.Reader) ([]Ingress, error) {
	decoder := yaml.NewDecoder(r)
	var resources []Ingress
	for index := 1; ; index++ {
		var document yaml.Node
		err := decoder.Decode(&document)
		if errors.Is(err, io.EOF) {
			return resources, nil
		}
		if err != nil {
			return nil, manifestError("document %d: %v", index, err)
		}
		found, err := parseRoot(document, 0)
		if err != nil {
			return nil, fmt.Errorf("document %d: %w", index, err)
		}
		resources = append(resources, found...)
	}
}

func parseRoot(document yaml.Node, depth int) ([]Ingress, error) {
	root, ok := documentRoot(document)
	if !ok {
		return nil, nil
	}
	if root.Kind != yaml.MappingNode {
		return nil, manifestError("document is not a Kubernetes object")
	}
	var raw rawDocument
	if err := root.Decode(&raw); err != nil {
		return nil, manifestError("cannot decode resource: %v", err)
	}
	return convert(raw, depth)
}

// documentRoot unwraps the document node and reports whether it holds content.
func documentRoot(document yaml.Node) (yaml.Node, bool) {
	node := document
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return yaml.Node{}, false
		}
		node = *node.Content[0]
	}
	if node.Kind == 0 {
		return yaml.Node{}, false
	}
	if node.Kind == yaml.ScalarNode && (node.Tag == "!!null" || node.Value == "") {
		return yaml.Node{}, false
	}
	return node, true
}

func convert(raw rawDocument, depth int) ([]Ingress, error) {
	switch raw.Kind {
	case KindList:
		if depth >= maxDocumentDepth {
			return nil, manifestError("list nesting deeper than %d documents", maxDocumentDepth)
		}
		var resources []Ingress
		for _, item := range raw.Items {
			found, err := convert(item, depth+1)
			if err != nil {
				return nil, err
			}
			resources = append(resources, found...)
		}
		return resources, nil
	case KindIngress:
	default:
		return nil, nil
	}
	if raw.APIVersion != APIVersion {
		return nil, manifestError("unsupported Ingress apiVersion %q; only %s is supported", raw.APIVersion, APIVersion)
	}
	return []Ingress{raw.ingress()}, nil
}

// manifestError builds a VAR-PLAN-0003 diagnostic error.
func manifestError(format string, args ...any) error {
	message := fmt.Sprintf(format, args...)
	diagnostic, err := diag.New(diag.CodePlanManifest, message)
	if err != nil {
		return errors.New(message)
	}
	return &diag.Error{Diagnostic: diagnostic}
}

// rawDocument is the wire shape of one YAML or JSON document.
type rawDocument struct {
	APIVersion string        `yaml:"apiVersion"`
	Kind       string        `yaml:"kind"`
	Metadata   rawMetadata   `yaml:"metadata"`
	Spec       rawSpec       `yaml:"spec"`
	Items      []rawDocument `yaml:"items"`
}

type rawMetadata struct {
	Namespace   string            `yaml:"namespace"`
	Name        string            `yaml:"name"`
	Annotations map[string]string `yaml:"annotations"`
}

type rawSpec struct {
	IngressClassName string    `yaml:"ingressClassName"`
	Rules            []rawRule `yaml:"rules"`
	TLS              []rawTLS  `yaml:"tls"`
}

type rawRule struct {
	Host string   `yaml:"host"`
	HTTP *rawHTTP `yaml:"http"`
}

type rawHTTP struct {
	Paths []rawPath `yaml:"paths"`
}

type rawPath struct {
	Path     string `yaml:"path"`
	PathType string `yaml:"pathType"`
}

type rawTLS struct {
	Hosts      []string `yaml:"hosts"`
	SecretName string   `yaml:"secretName"`
}

func (raw rawDocument) ingress() Ingress {
	resource := Ingress{
		APIVersion: raw.APIVersion,
		Kind:       raw.Kind,
		Namespace:  raw.Metadata.Namespace,
		Name:       raw.Metadata.Name,
		ClassName:  raw.Spec.IngressClassName,
	}
	if len(raw.Metadata.Annotations) != 0 {
		resource.Annotations = make(map[string]string, len(raw.Metadata.Annotations))
		for name, value := range raw.Metadata.Annotations {
			resource.Annotations[name] = value
		}
	}
	for _, rule := range raw.Spec.Rules {
		converted := Rule{Host: rule.Host}
		if rule.HTTP != nil {
			for _, path := range rule.HTTP.Paths {
				converted.Paths = append(converted.Paths, Path(path))
			}
		}
		resource.Rules = append(resource.Rules, converted)
	}
	for _, entry := range raw.Spec.TLS {
		resource.TLS = append(resource.TLS, TLS{Hosts: append([]string(nil), entry.Hosts...), SecretName: entry.SecretName})
	}
	return resource
}
