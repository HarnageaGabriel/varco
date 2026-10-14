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
	"sort"
	"strconv"

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
	// DefaultBackend reports whether spec.defaultBackend is set.
	DefaultBackend bool
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
	// The apiVersion/kind header is decoded first, so a non-Ingress document
	// whose body does not match the Ingress schema is ignored instead of
	// aborting the whole plan.
	var header rawHeader
	if err := root.Decode(&header); err != nil {
		return nil, manifestError("cannot decode resource header: %v", err)
	}
	switch header.Kind {
	case KindList:
		return parseList(root, depth)
	case KindIngress:
	default:
		return nil, nil
	}
	if header.APIVersion != APIVersion {
		return nil, manifestError("unsupported Ingress apiVersion %q; only %s is supported", header.APIVersion, APIVersion)
	}
	var raw rawDocument
	if err := root.Decode(&raw); err != nil {
		return nil, manifestError("cannot decode resource: %v", err)
	}
	resource, err := raw.ingress()
	if err != nil {
		return nil, err
	}
	return []Ingress{resource}, nil
}

// parseList walks the items of a kind: List document without decoding them into
// typed structs first, so a nested non-Ingress item is ignored cheaply. Nesting
// is bounded so a crafted manifest cannot recurse without end.
func parseList(root yaml.Node, depth int) ([]Ingress, error) {
	if depth >= maxDocumentDepth {
		return nil, manifestError("list nesting deeper than %d documents", maxDocumentDepth)
	}
	items, ok := mappingValue(root, "items")
	if !ok || items.Kind != yaml.SequenceNode {
		return nil, nil
	}
	var resources []Ingress
	for _, item := range items.Content {
		found, err := parseRoot(*item, depth+1)
		if err != nil {
			return nil, err
		}
		resources = append(resources, found...)
	}
	return resources, nil
}

// mappingValue returns the value node of a key in a mapping node.
func mappingValue(node yaml.Node, key string) (yaml.Node, bool) {
	if node.Kind != yaml.MappingNode {
		return yaml.Node{}, false
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		if node.Content[index].Value == key {
			return *node.Content[index+1], true
		}
	}
	return yaml.Node{}, false
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

// manifestError builds a VAR-PLAN-0003 diagnostic error.
func manifestError(format string, args ...any) error {
	message := fmt.Sprintf(format, args...)
	diagnostic, err := diag.New(diag.CodePlanManifest, message)
	if err != nil {
		return errors.New(message)
	}
	return &diag.Error{Diagnostic: diagnostic}
}

// rawHeader is the apiVersion/kind pair that decides whether a document is
// worth decoding into the typed Ingress shape.
type rawHeader struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
}

// rawDocument is the wire shape of one Ingress document.
type rawDocument struct {
	APIVersion string      `yaml:"apiVersion"`
	Kind       string      `yaml:"kind"`
	Metadata   rawMetadata `yaml:"metadata"`
	Spec       rawSpec     `yaml:"spec"`
}

type rawMetadata struct {
	Namespace   string         `yaml:"namespace"`
	Name        string         `yaml:"name"`
	Annotations map[string]any `yaml:"annotations"`
}

type rawSpec struct {
	IngressClassName string    `yaml:"ingressClassName"`
	Rules            []rawRule `yaml:"rules"`
	TLS              []rawTLS  `yaml:"tls"`
	// DefaultBackend only needs its presence, not its shape.
	DefaultBackend *yaml.Node `yaml:"defaultBackend"`
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

func (raw rawDocument) ingress() (Ingress, error) {
	resource := Ingress{
		APIVersion:     raw.APIVersion,
		Kind:           raw.Kind,
		Namespace:      raw.Metadata.Namespace,
		Name:           raw.Metadata.Name,
		ClassName:      raw.Spec.IngressClassName,
		DefaultBackend: hasDefaultBackend(raw.Spec.DefaultBackend),
	}
	if len(raw.Metadata.Annotations) != 0 {
		resource.Annotations = make(map[string]string, len(raw.Metadata.Annotations))
		names := make([]string, 0, len(raw.Metadata.Annotations))
		for name := range raw.Metadata.Annotations {
			names = append(names, name)
		}
		// Sorted so a manifest with more than one bad annotation always fails on
		// the same field.
		sort.Strings(names)
		for _, name := range names {
			value, err := stringifyAnnotation(name, raw.Metadata.Annotations[name])
			if err != nil {
				return Ingress{}, err
			}
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
	return resource, nil
}

// hasDefaultBackend reports whether spec.defaultBackend is present and not null.
func hasDefaultBackend(node *yaml.Node) bool {
	if node == nil {
		return false
	}
	return !(node.Kind == yaml.ScalarNode && (node.Tag == "!!null" || node.Value == ""))
}

// stringifyAnnotation converts a scalar annotation value to its string form, so
// an unquoted boolean or number (`enable-cors: true`) is honored. A non-scalar
// value is a manifest error naming the field.
func stringifyAnnotation(name string, value any) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "", nil
	case string:
		return typed, nil
	case bool:
		return strconv.FormatBool(typed), nil
	case int:
		return strconv.Itoa(typed), nil
	case int64:
		return strconv.FormatInt(typed, 10), nil
	case uint64:
		return strconv.FormatUint(typed, 10), nil
	case float64:
		return strconv.FormatFloat(typed, 'g', -1, 64), nil
	default:
		return "", manifestError("metadata.annotations[%q] must be a scalar value", name)
	}
}
