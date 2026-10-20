package plan

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/HarnageaGabriel/varco/internal/ingress"
)

// The head and host-variant families are plan-wide: one HEAD request and three
// Host header variants per concrete host, on the smallest planned path of that
// host. Sources: docs/adr/0005-plan-case-families.md, RFC 3986 section 3.2.2
// (the host is case-insensitive), RFC 9110 section 7.2 (the Host header may
// carry the port), and RFC 9110 section 9.3.2 (HEAD).

// syntheticCase is the projection of one head or host-variant case the tests
// compare: the family, the request shape and the Host header value, without the
// derived id.
type syntheticCase struct {
	Family     string
	Host       string
	Scheme     string
	SNI        string
	Method     string
	Path       string
	HostHeader string
}

func hostHeaderOf(item Case) string {
	if len(item.Headers) != 1 || item.Headers[0].Name != hostHeaderName {
		return ""
	}
	if len(item.Headers[0].Values) != 1 {
		return "invalid"
	}
	return item.Headers[0].Values[0]
}

// syntheticSortKey orders projections without the opaque id, so one expected
// order can be written by hand.
func syntheticSortKey(item syntheticCase) string {
	return strings.Join([]string{item.Family, item.Method, item.Host, item.HostHeader}, "\x00")
}

// syntheticProjection collects the head and host-variant cases of a plan.
func syntheticProjection(result Plan) []syntheticCase {
	got := make([]syntheticCase, 0, 4)
	for _, item := range result.Cases {
		if item.Family != FamilyHead && item.Family != FamilyHostVariant {
			continue
		}
		got = append(got, syntheticCase{
			Family:     item.Family,
			Host:       item.Host,
			Scheme:     item.Scheme,
			SNI:        item.SNI,
			Method:     item.Method,
			Path:       item.Path,
			HostHeader: hostHeaderOf(item),
		})
	}
	sort.Slice(got, func(i, j int) bool {
		return syntheticSortKey(got[i]) < syntheticSortKey(got[j])
	})
	return got
}

func sortProjections(items []syntheticCase) []syntheticCase {
	ordered := append([]syntheticCase(nil), items...)
	sort.Slice(ordered, func(i, j int) bool {
		return syntheticSortKey(ordered[i]) < syntheticSortKey(ordered[j])
	})
	return ordered
}

// sameProjections compares two projections: explicit length check, then
// reflect.DeepEqual over the field structs.
func sameProjections(want, got []syntheticCase) bool {
	if len(want) == 0 || len(got) == 0 {
		return len(want) == len(got)
	}
	return reflect.DeepEqual(want, got)
}

func projectionsString(items []syntheticCase) string {
	if len(items) == 0 {
		return "(none)"
	}
	return fmt.Sprintf("%+v", items)
}

// TestBuildHeadAndHostVariantCasesDoNotDependOnInputOrder proves that the
// head and host-variant cases of a host are the same in every input order.
//
// Sources: docs/plan.md ("Determinism guarantees") and ADR 0005 ("Determinism
// is part of the contract"). The head and host-variant cases use the smallest
// planned path of the host (internal/plan/generate.go, builder.plannedPaths),
// so they do not depend on which resource is read first.
func TestBuildHeadAndHostVariantCasesDoNotDependOnInputOrder(t *testing.T) {
	small := pathIngress("small", "c.example.com", "/a", "Prefix", nil)
	large := pathIngress("large", "c.example.com", "/b", "Prefix", nil)
	forward := Build([]ingress.Ingress{small, large}, Options{})
	reversed := Build([]ingress.Ingress{large, small}, Options{})
	if !reflect.DeepEqual(forward.Cases, reversed.Cases) {
		t.Fatalf("the planned cases changed with the input order:\nforward: %#v\nreversed: %#v", forward.Cases, reversed.Cases)
	}
}

// TestBuildHeadAndHostVariantCases covers the head and host-variant families.
func TestBuildHeadAndHostVariantCases(t *testing.T) {
	tlsHost := pathIngress("api", "api.example.com", "/api", "Prefix", nil)
	tlsHost.TLS = []ingress.TLS{{Hosts: []string{"api.example.com"}}}
	firstRule := pathIngress("a", "a.example.com", "/a", "Prefix", nil)
	secondRule := pathIngress("b", "a.example.com", "/b", "Prefix", nil)
	// The same host declared by two resources, the one with the smaller path
	// planned last: the smallest planned path wins, not the first one.
	smallPath := pathIngress("small", "c.example.com", "/a", "Prefix", nil)
	largePath := pathIngress("large", "c.example.com", "/b", "Prefix", nil)
	hostless := ingress.Ingress{
		APIVersion: ingress.APIVersion,
		Kind:       ingress.KindIngress,
		Namespace:  "dev",
		Name:       "internal",
		Rules:      []ingress.Rule{{Paths: []ingress.Path{{Path: "/internal", PathType: "Prefix"}}}},
	}
	tests := []struct {
		name      string
		resources []ingress.Ingress
		options   Options
		want      []syntheticCase
	}{
		{
			name:      "single http host plans three variants and one head case with the http default port",
			resources: []ingress.Ingress{pathIngress("shop", "shop.example.com", "/api", "Prefix", nil)},
			want: []syntheticCase{
				{Family: FamilyHostVariant, Host: "shop.example.com", Scheme: SchemeHTTP, Method: MethodGet, Path: "/api", HostHeader: "SHOP.EXAMPLE.COM"},
				{Family: FamilyHostVariant, Host: "shop.example.com", Scheme: SchemeHTTP, Method: MethodGet, Path: "/api", HostHeader: "shop.example.com."},
				{Family: FamilyHostVariant, Host: "shop.example.com", Scheme: SchemeHTTP, Method: MethodGet, Path: "/api", HostHeader: "shop.example.com:80"},
				{Family: FamilyHead, Host: "shop.example.com", Scheme: SchemeHTTP, Method: MethodHead, Path: "/api"},
			},
		},
		{
			name:      "tls host keeps SNI plain and uses the https default port",
			resources: []ingress.Ingress{tlsHost},
			want: []syntheticCase{
				{Family: FamilyHostVariant, Host: "api.example.com", Scheme: SchemeHTTPS, SNI: "api.example.com", Method: MethodGet, Path: "/api", HostHeader: "API.EXAMPLE.COM"},
				{Family: FamilyHostVariant, Host: "api.example.com", Scheme: SchemeHTTPS, SNI: "api.example.com", Method: MethodGet, Path: "/api", HostHeader: "api.example.com."},
				{Family: FamilyHostVariant, Host: "api.example.com", Scheme: SchemeHTTPS, SNI: "api.example.com", Method: MethodGet, Path: "/api", HostHeader: "api.example.com:443"},
				{Family: FamilyHead, Host: "api.example.com", Scheme: SchemeHTTPS, SNI: "api.example.com", Method: MethodHead, Path: "/api"},
			},
		},
		{
			name: "two hosts each get their own head and variants",
			resources: []ingress.Ingress{
				pathIngress("a", "a.example.com", "/a", "Prefix", nil),
				pathIngress("b", "b.example.com", "/b", "Prefix", nil),
			},
			want: []syntheticCase{
				{Family: FamilyHostVariant, Host: "a.example.com", Scheme: SchemeHTTP, Method: MethodGet, Path: "/a", HostHeader: "A.EXAMPLE.COM"},
				{Family: FamilyHostVariant, Host: "a.example.com", Scheme: SchemeHTTP, Method: MethodGet, Path: "/a", HostHeader: "a.example.com."},
				{Family: FamilyHostVariant, Host: "a.example.com", Scheme: SchemeHTTP, Method: MethodGet, Path: "/a", HostHeader: "a.example.com:80"},
				{Family: FamilyHead, Host: "a.example.com", Scheme: SchemeHTTP, Method: MethodHead, Path: "/a"},
				{Family: FamilyHead, Host: "b.example.com", Scheme: SchemeHTTP, Method: MethodHead, Path: "/b"},
				{Family: FamilyHostVariant, Host: "b.example.com", Scheme: SchemeHTTP, Method: MethodGet, Path: "/b", HostHeader: "B.EXAMPLE.COM"},
				{Family: FamilyHostVariant, Host: "b.example.com", Scheme: SchemeHTTP, Method: MethodGet, Path: "/b", HostHeader: "b.example.com."},
				{Family: FamilyHostVariant, Host: "b.example.com", Scheme: SchemeHTTP, Method: MethodGet, Path: "/b", HostHeader: "b.example.com:80"},
			},
		},
		{
			name:      "the variants send the smallest planned path of the host",
			resources: []ingress.Ingress{firstRule, secondRule},
			want: []syntheticCase{
				{Family: FamilyHostVariant, Host: "a.example.com", Scheme: SchemeHTTP, Method: MethodGet, Path: "/a", HostHeader: "A.EXAMPLE.COM"},
				{Family: FamilyHostVariant, Host: "a.example.com", Scheme: SchemeHTTP, Method: MethodGet, Path: "/a", HostHeader: "a.example.com."},
				{Family: FamilyHostVariant, Host: "a.example.com", Scheme: SchemeHTTP, Method: MethodGet, Path: "/a", HostHeader: "a.example.com:80"},
				{Family: FamilyHead, Host: "a.example.com", Scheme: SchemeHTTP, Method: MethodHead, Path: "/a"},
			},
		},
		{
			name:      "the smallest planned path wins over the first planned one",
			resources: []ingress.Ingress{largePath, smallPath},
			want: []syntheticCase{
				{Family: FamilyHostVariant, Host: "c.example.com", Scheme: SchemeHTTP, Method: MethodGet, Path: "/a", HostHeader: "C.EXAMPLE.COM"},
				{Family: FamilyHostVariant, Host: "c.example.com", Scheme: SchemeHTTP, Method: MethodGet, Path: "/a", HostHeader: "c.example.com."},
				{Family: FamilyHostVariant, Host: "c.example.com", Scheme: SchemeHTTP, Method: MethodGet, Path: "/a", HostHeader: "c.example.com:80"},
				{Family: FamilyHead, Host: "c.example.com", Scheme: SchemeHTTP, Method: MethodHead, Path: "/a"},
			},
		},
		{
			name:      "the smallest planned path wins in either input order",
			resources: []ingress.Ingress{smallPath, largePath},
			want: []syntheticCase{
				{Family: FamilyHostVariant, Host: "c.example.com", Scheme: SchemeHTTP, Method: MethodGet, Path: "/a", HostHeader: "C.EXAMPLE.COM"},
				{Family: FamilyHostVariant, Host: "c.example.com", Scheme: SchemeHTTP, Method: MethodGet, Path: "/a", HostHeader: "c.example.com."},
				{Family: FamilyHostVariant, Host: "c.example.com", Scheme: SchemeHTTP, Method: MethodGet, Path: "/a", HostHeader: "c.example.com:80"},
				{Family: FamilyHead, Host: "c.example.com", Scheme: SchemeHTTP, Method: MethodHead, Path: "/a"},
			},
		},
		{
			name:      "a wildcard host is skipped",
			resources: []ingress.Ingress{pathIngress("edge", "*.example.com", "/", "Prefix", nil)},
		},
		{
			name:      "a hostless rule is skipped",
			resources: []ingress.Ingress{hostless},
		},
		{
			name:      "a hostless rule is skipped even with --default-host",
			resources: []ingress.Ingress{hostless},
			options:   Options{DefaultHost: "internal.svc"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := Build(test.resources, test.options)
			got := syntheticProjection(result)
			want := sortProjections(test.want)
			if !sameProjections(want, got) {
				t.Fatalf("head and host-variant cases mismatch (-want +got)\nwant: %s\ngot:  %s", projectionsString(want), projectionsString(got))
			}
			// Determinism: the same resources always plan the same cases and ids.
			again := Build(test.resources, test.options)
			if !reflect.DeepEqual(again.Cases, result.Cases) {
				t.Fatalf("planning the same input twice differed\nfirst: %s\nagain: %s", projectionsString(syntheticProjection(result)), projectionsString(syntheticProjection(again)))
			}
			// Sources and the comparison nature are visible in the notes.
			sawCaseInsensitive := false
			sawPortCitation := false
			for _, item := range result.Cases {
				switch item.Family {
				case FamilyHostVariant:
					if !strings.Contains(item.Note, "only the Host header differs") {
						t.Fatalf("host-variant note = %q", item.Note)
					}
					if strings.Contains(item.Note, "RFC 3986 section 3.2.2") {
						sawCaseInsensitive = true
					}
					if strings.Contains(item.Note, "RFC 9110 section 7.2") {
						sawPortCitation = true
					}
				case FamilyHead:
					if !strings.Contains(item.Note, "RFC 9110 section 9.3.2") {
						t.Fatalf("head note = %q", item.Note)
					}
					if item.Method != MethodHead {
						t.Fatalf("head case method = %s", item.Method)
					}
				}
			}
			if len(syntheticProjection(result)) > 0 && (!sawCaseInsensitive || !sawPortCitation) {
				t.Fatal("host-variant notes must cite RFC 3986 section 3.2.2 and RFC 9110 section 7.2")
			}
		})
	}
}
