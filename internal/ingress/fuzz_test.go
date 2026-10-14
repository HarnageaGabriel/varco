package ingress

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// FuzzParse checks that no manifest can panic the parser and that parsing is
// deterministic for any input.
//
// Seeds cover the document shapes varco promises to accept: a v1 Ingress, a
// List, a multi-document stream, a rejected extensions/v1beta1 Ingress
// (https://kubernetes.io/blog/2026/02/27/ingress-nginx-before-you-migrate) and
// malformed YAML.
func FuzzParse(f *testing.F) {
	seeds := []string{
		"apiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata:\n  name: a\nspec:\n  rules:\n    - host: a.example.com\n      http:\n        paths:\n          - path: /a\n            pathType: Prefix\n",
		"apiVersion: v1\nkind: List\nitems:\n  - apiVersion: networking.k8s.io/v1\n    kind: Ingress\n    metadata:\n      name: b\n",
		"---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: c\n---\n",
		"apiVersion: extensions/v1beta1\nkind: Ingress\nmetadata:\n  name: legacy\n",
		"apiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata:\n  name: [unterminated\n",
		"null",
		"- a\n- b\n",
		"{",
		"",
		strings.Repeat("[", 10000) + "0" + strings.Repeat("]", 10000),
	}
	for _, seed := range seeds {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		first, err := Parse(bytes.NewReader(data))
		if err != nil {
			return
		}
		second, err := Parse(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("second parse of the same input failed: %v", err)
		}
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("parse is not deterministic: %#v vs %#v", first, second)
		}
	})
}
