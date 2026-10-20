package plan

import (
	"reflect"
	"strings"
	"testing"

	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/ingress"
)

// The behaviors exercised here come from
// https://kubernetes.io/blog/2026/02/27/ingress-nginx-before-you-migrate and
// from docs/adr/0005-plan-case-families.md.

func pathIngress(name, host, path, pathType string, annotations map[string]string) ingress.Ingress {
	return ingress.Ingress{
		APIVersion:  ingress.APIVersion,
		Kind:        ingress.KindIngress,
		Namespace:   "ns",
		Name:        name,
		ClassName:   "nginx",
		Annotations: annotations,
		Rules:       []ingress.Rule{{Host: host, Paths: []ingress.Path{{Path: path, PathType: pathType}}}},
	}
}

func families(result Plan) []string {
	seen := make([]string, 0, len(result.Cases))
	for _, item := range result.Cases {
		if isSyntheticFamily(item.Family) {
			continue
		}
		seen = append(seen, item.Family)
	}
	return seen
}

func pathsByFamily(result Plan) map[string][]string {
	byFamily := make(map[string][]string, len(result.Cases))
	for _, item := range result.Cases {
		if isSyntheticFamily(item.Family) {
			continue
		}
		byFamily[item.Family] = append(byFamily[item.Family], item.Path)
	}
	return byFamily
}

// isSyntheticFamily reports whether a family varco derives from the plan as a
// whole rather than from one declared route. The per-route tests below assert on
// the declared-route families only; the synthetic families have their own test.
func isSyntheticFamily(family string) bool {
	switch family {
	case FamilyDefaultBackend, FamilyUnmatchedRoute, FamilyHead, FamilyHostVariant:
		return true
	default:
		return false
	}
}

// TestBuildSyntheticFamilies covers the default-backend and unmatched-route
// plan-wide families: one default-backend probe per scheme the plan uses, and
// one unmatched-route probe per host that declares no Prefix or
// ImplementationSpecific "/" path. head and host-variant have their own tests.
func TestBuildSyntheticFamilies(t *testing.T) {
	resource := pathIngress("checkout", "api.example.com", "/api", "Prefix", nil)
	resource.TLS = []ingress.TLS{{Hosts: []string{"api.example.com"}}}
	result := Build([]ingress.Ingress{resource}, Options{})

	byFamily := make(map[string][]Case)
	for _, item := range result.Cases {
		byFamily[item.Family] = append(byFamily[item.Family], item)
	}
	defaultBackend := byFamily[FamilyDefaultBackend]
	if len(defaultBackend) != 2 {
		t.Fatalf("default-backend cases = %#v, want one per scheme", defaultBackend)
	}
	for _, item := range defaultBackend {
		if item.Host != defaultBackendHost || item.Method != MethodGet || item.Path != "/" {
			t.Fatalf("default-backend case = %#v", item)
		}
		wantSNI := ""
		if item.Scheme == SchemeHTTPS {
			wantSNI = defaultBackendHost
		}
		if item.SNI != wantSNI {
			t.Fatalf("default-backend %s SNI = %q", item.Scheme, item.SNI)
		}
	}
	unmatched := byFamily[FamilyUnmatchedRoute]
	if len(unmatched) != 1 {
		t.Fatalf("unmatched-route cases = %#v, want one", unmatched)
	}
	if unmatched[0].Host != "api.example.com" || unmatched[0].Path != unmatchedRoutePath || unmatched[0].Scheme != SchemeHTTPS {
		t.Fatalf("unmatched-route case = %#v", unmatched[0])
	}
}

// TestBuildUnmatchedRouteSkippedForCatchAll verifies a host with a Prefix "/"
// path gets no unmatched-route case, because "/" already matches every request.
func TestBuildUnmatchedRouteSkippedForCatchAll(t *testing.T) {
	result := Build([]ingress.Ingress{pathIngress("root", "app.example.com", "/", "Prefix", nil)}, Options{})
	for _, item := range result.Cases {
		if item.Family == FamilyUnmatchedRoute {
			t.Fatalf("catch-all host planned an unmatched-route case: %#v", item)
		}
	}
}

func findingCodes(findings []Finding) []diag.Code {
	codes := make([]diag.Code, 0, len(findings))
	for _, finding := range findings {
		codes = append(codes, finding.Code)
	}
	return codes
}

func TestBuildPrefixPathFamilies(t *testing.T) {
	resource := pathIngress("checkout", "api.example.com", "/api", "Prefix", map[string]string{
		annotationEnableCORS:       "true",
		annotationCORSAllowHeaders: "X-Request-Id",
	})
	resource.TLS = []ingress.TLS{{Hosts: []string{"api.example.com"}, SecretName: "api-tls"}}
	result := Build([]ingress.Ingress{resource}, Options{})

	want := map[string][]string{
		FamilyBase:           {"/api"},
		FamilyTrailingSlash:  {"/api/"},
		FamilySubpath:        {"/api/varco-probe"},
		FamilyPrefixBoundary: {"/apivarco"},
		FamilyDotSegments:    {"/varco/../api"},
		FamilyDoubleSlash:    {"//api"},
		FamilyCORSPreflight:  {"/api"},
		// TLS is configured for the host, so plain HTTP must redirect (blog behavior 4).
		FamilyHTTPRedirect: {"//api", "/api", "/api/", "/api/varco-probe", "/apivarco", "/varco/../api"},
	}
	if got := pathsByFamily(result); !reflect.DeepEqual(got, want) {
		t.Fatalf("families = %#v, want %#v", got, want)
	}
	if len(result.Unsupported) != 0 || len(result.Warnings) != 0 {
		t.Fatalf("unexpected findings %#v or warnings %#v", result.Unsupported, result.Warnings)
	}
	for _, item := range result.Cases {
		if isSyntheticFamily(item.Family) {
			continue
		}
		if item.Scheme == SchemeHTTPS && item.SNI != "api.example.com" {
			t.Fatalf("https case %s has SNI %q", item.Path, item.SNI)
		}
		if item.Scheme == SchemeHTTP && item.SNI != "" {
			t.Fatalf("http case %s has SNI %q", item.Path, item.SNI)
		}
		if item.Method != MethodGet && item.Family != FamilyCORSPreflight {
			t.Fatalf("case %s/%s uses method %s", item.Family, item.Path, item.Method)
		}
	}
}

func TestBuildCORSPreflightHeaders(t *testing.T) {
	resource := pathIngress("checkout", "api.example.com", "/api", "Prefix", map[string]string{
		annotationEnableCORS:       "true",
		annotationCORSAllowHeaders: " X-Request-Id ",
	})
	result := Build([]ingress.Ingress{resource}, Options{})
	var preflight *Case
	for index := range result.Cases {
		if result.Cases[index].Family == FamilyCORSPreflight {
			preflight = &result.Cases[index]
		}
	}
	if preflight == nil {
		t.Fatal("no cors-preflight case")
	}
	if preflight.Method != MethodOptions {
		t.Fatalf("preflight method = %s", preflight.Method)
	}
	want := []Header{
		{Name: "Access-Control-Request-Headers", Values: []string{"X-Request-Id"}},
		{Name: "Access-Control-Request-Method", Values: []string{"GET"}},
		{Name: "Origin", Values: []string{"https://varco.invalid"}},
	}
	if !reflect.DeepEqual(preflight.Headers, want) {
		t.Fatalf("preflight headers = %#v, want %#v", preflight.Headers, want)
	}
	if preflight.Source.Ingress != "ns/checkout" || preflight.Source.Annotation != annotationEnableCORS {
		t.Fatalf("preflight source = %#v", preflight.Source)
	}
}

// TestBuildHostWideRegex covers blog behaviors 2 and 3: use-regex on one Ingress
// makes every path of that host regex-matched, across Ingress resources.
func TestBuildHostWideRegex(t *testing.T) {
	regex := pathIngress("regex", "api.example.com", "/api", "Prefix", map[string]string{annotationUseRegex: "true"})
	literal := pathIngress("literal", "api.example.com", "/header", "Exact", nil)
	result := Build([]ingress.Ingress{regex, literal}, Options{})

	var flipped []string
	for _, item := range result.Cases {
		if item.Family == FamilyCaseFlip {
			flipped = append(flipped, item.Path)
		}
	}
	if !reflect.DeepEqual(flipped, []string{"/API", "/HEADER"}) {
		t.Fatalf("case-flip paths = %#v", flipped)
	}
	want := []Warning{{
		Code:    diag.CodePlanRegexHostWide,
		Ingress: "ns/regex",
		Message: "use-regex or rewrite-target on ns/regex makes ingress-nginx match every path of host api.example.com as a case-insensitive regex",
	}}
	if !reflect.DeepEqual(result.Warnings, want) {
		t.Fatalf("warnings = %#v, want %#v", result.Warnings, want)
	}
	for _, item := range result.Cases {
		if item.Family != FamilyCaseFlip {
			continue
		}
		if item.Source.Ingress == "ns/literal" && !strings.Contains(item.Note, "host-wide") {
			t.Fatalf("host-wide case-flip note missing: %q", item.Note)
		}
	}
}

// TestBuildHostWideRegexNamesTheSmallestIngress proves that the VAR-PLAN-0006
// warning names the Ingress with the smallest namespace and name among those
// that turned the host-wide regex scope on, whatever the order of the input.
//
// Sources: docs/plan.md ("records a VAR-PLAN-0006 warning naming the host")
// and ADR 0005 ("the scope is surfaced as a VAR-PLAN-0006 warning"). The rule
// is implemented in internal/plan/nginx.go, markRegexHost and
// addRegexHostWarnings.
func TestBuildHostWideRegexNamesTheSmallestIngress(t *testing.T) {
	regex := map[string]string{annotationUseRegex: "true"}
	beta := pathIngress("beta", "api.example.com", "/b", "Prefix", regex)
	beta.Namespace = "prod"
	alpha := pathIngress("alpha", "api.example.com", "/a", "Prefix", regex)
	alpha.Namespace = "prod"
	// Another namespace sorts after "prod", so "prod/alpha" is the smallest
	// identity of the three.
	other := pathIngress("alpha", "api.example.com", "/c", "Prefix", regex)
	other.Namespace = "zzz"

	want := []Warning{{
		Code:    diag.CodePlanRegexHostWide,
		Ingress: "prod/alpha",
		Message: "use-regex or rewrite-target on prod/alpha makes ingress-nginx match every path of host api.example.com as a case-insensitive regex",
	}}
	for _, order := range [][]ingress.Ingress{
		{beta, alpha, other},
		{other, alpha, beta},
		{alpha, other, beta},
	} {
		result := Build(order, Options{})
		if !reflect.DeepEqual(result.Warnings, want) {
			t.Fatalf("warnings = %#v, want %#v", result.Warnings, want)
		}
	}
}

func TestBuildNoCaseFlipWithoutRegex(t *testing.T) {
	result := Build([]ingress.Ingress{pathIngress("plain", "plain.example.com", "/header", "Exact", nil)}, Options{})
	for _, item := range result.Cases {
		if isSyntheticFamily(item.Family) {
			continue
		}
		if item.Family == FamilyCaseFlip {
			t.Fatalf("unexpected case-flip case for a literal host: %#v", item)
		}
	}
	if got := families(result); !reflect.DeepEqual(got, []string{FamilyDoubleSlash, FamilyBase, FamilyTrailingSlash, FamilyDotSegments}) {
		t.Fatalf("families = %#v", got)
	}
}

func TestBuildRewriteFamilies(t *testing.T) {
	resource := pathIngress("catalog", "shop.example.com", "/cart", "Prefix", map[string]string{
		annotationRewriteTarget: "/$2",
	})
	result := Build([]ingress.Ingress{resource}, Options{})
	byFamily := pathsByFamily(result)
	if !reflect.DeepEqual(byFamily[FamilyRewrite], []string{"/cart", "/cart/varco-probe"}) {
		t.Fatalf("rewrite cases = %#v", byFamily[FamilyRewrite])
	}
	if len(byFamily[FamilyBase]) != 0 {
		t.Fatalf("rewrite-target must rename base cases: %#v", byFamily[FamilyBase])
	}
	for _, item := range result.Cases {
		if item.Source.Annotation != annotationRewriteTarget && item.Family == FamilyRewrite {
			t.Fatalf("rewrite case without annotation source: %#v", item.Source)
		}
		if item.Family == FamilyRewrite && !strings.Contains(item.Note, "rewrite-target") {
			t.Fatalf("rewrite note missing: %q", item.Note)
		}
	}
}

// TestBuildRegexSample covers blog behavior 1 sample derivation.
func TestBuildRegexSample(t *testing.T) {
	tests := []struct {
		path     string
		wantPath string
		wantFind bool
	}{
		{path: "/catalog(/|$)(.*)", wantPath: "/catalog/varco-probe"},
		{path: "/catalog(/|$)", wantPath: "/catalog/varco-probe"},
		{path: "^/legacy", wantPath: "/legacy"},
		{path: "/plain", wantPath: "/plain"},
		{path: "/api(/|$)(.*)", wantPath: "/api/varco-probe"},
		{path: "/api/(.*)", wantPath: "/api/varco-probe"},
		{path: "/api/.*", wantPath: "/api/varco-probe"},
		{path: "/api(/.*)", wantPath: "/api/varco-probe"},
		{path: "/api(.*)", wantPath: "/api/varco-probe"},
		{path: "/v[0-9]{1,3}/items/(.*)", wantFind: true},
		{path: "^/a/[0-9]+$", wantFind: true},
		{path: "/a/[0-9]+", wantFind: true},
		{path: "/(a|b)", wantFind: true},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			resource := pathIngress("regex", "api.example.com", test.path, "ImplementationSpecific", nil)
			result := Build([]ingress.Ingress{resource}, Options{})
			if test.wantFind {
				if !reflect.DeepEqual(findingCodes(result.Unsupported), []diag.Code{diag.CodePlanRegexSample}) {
					t.Fatalf("findings = %#v", result.Unsupported)
				}
				if len(pathsByFamily(result)) != 0 {
					t.Fatalf("cases = %#v, want none", result.Cases)
				}
				return
			}
			if len(result.Unsupported) != 0 {
				t.Fatalf("findings = %#v", result.Unsupported)
			}
			if got := pathsByFamily(result)[FamilyBase]; !reflect.DeepEqual(got, []string{test.wantPath}) {
				t.Fatalf("base path = %#v, want %q", got, test.wantPath)
			}
		})
	}
}

func TestBuildPathWithoutSlashIsRejected(t *testing.T) {
	result := Build([]ingress.Ingress{pathIngress("bad", "api.example.com", "no-slash", "Prefix", nil)}, Options{})
	if !reflect.DeepEqual(findingCodes(result.Unsupported), []diag.Code{diag.CodePlanManifest}) {
		t.Fatalf("findings = %#v", result.Unsupported)
	}
	if len(pathsByFamily(result)) != 0 {
		t.Fatalf("cases = %#v, want none", result.Cases)
	}
}

func TestBuildWildcardHost(t *testing.T) {
	result := Build([]ingress.Ingress{pathIngress("edge", "*.example.com", "/", "Prefix", nil)}, Options{})
	for _, item := range result.Cases {
		if isSyntheticFamily(item.Family) {
			continue
		}
		if item.Host != "varco-probe.example.com" {
			t.Fatalf("case host = %q", item.Host)
		}
	}
	if len(result.Warnings) != 1 || result.Warnings[0].Code != diag.CodePlanWildcardHost {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
	if !strings.Contains(result.Warnings[0].Message, "varco-probe.example.com") {
		t.Fatalf("warning message = %q", result.Warnings[0].Message)
	}
}

func TestBuildTLSHostSubstitutionKeepsWildcardWarningOnce(t *testing.T) {
	resource := pathIngress("edge", "*.example.com", "/", "Prefix", nil)
	resource.TLS = []ingress.TLS{{Hosts: []string{"*.example.com"}}}
	result := Build([]ingress.Ingress{resource}, Options{})
	if len(result.Warnings) != 1 {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
	for _, item := range result.Cases {
		if item.Family == FamilyHTTPRedirect {
			continue
		}
		if isSyntheticFamily(item.Family) {
			continue
		}
		if item.Scheme != SchemeHTTPS || item.SNI != "varco-probe.example.com" {
			t.Fatalf("wildcard TLS case = %#v", item)
		}
	}
}

func TestBuildDefaultHostAndHostlessFindings(t *testing.T) {
	resource := ingress.Ingress{
		APIVersion: ingress.APIVersion,
		Kind:       ingress.KindIngress,
		Namespace:  "dev",
		Name:       "internal",
		Rules:      []ingress.Rule{{Paths: []ingress.Path{{Path: "/internal", PathType: "Prefix"}}}},
	}
	without := Build([]ingress.Ingress{resource}, Options{})
	if !reflect.DeepEqual(findingCodes(without.Unsupported), []diag.Code{diag.CodePlanRuleWithoutHost}) {
		t.Fatalf("findings = %#v", without.Unsupported)
	}
	if len(without.Cases) != 0 {
		t.Fatalf("cases = %#v, want none", without.Cases)
	}
	with := Build([]ingress.Ingress{resource}, Options{DefaultHost: "internal.svc"})
	if len(with.Unsupported) != 0 {
		t.Fatalf("findings = %#v", with.Unsupported)
	}
	for _, item := range with.Cases {
		if isSyntheticFamily(item.Family) {
			continue
		}
		if item.Host != "internal.svc" {
			t.Fatalf("case host = %q", item.Host)
		}
	}
}

func TestBuildIngressClassFilter(t *testing.T) {
	resource := pathIngress("checkout", "api.example.com", "/api", "Prefix", nil)
	other := pathIngress("other", "other.example.com", "/other", "Prefix", nil)
	other.ClassName = "traefik"
	legacy := pathIngress("legacy", "legacy.example.com", "/legacy", "Prefix", map[string]string{AnnotationIngressClass: "nginx"})
	legacy.ClassName = ""

	result := Build([]ingress.Ingress{resource, other, legacy}, Options{IngressClass: "nginx"})
	hosts := make(map[string]bool, len(result.Cases))
	for _, item := range result.Cases {
		hosts[item.Host] = true
	}
	if !hosts["api.example.com"] || !hosts["legacy.example.com"] || hosts["other.example.com"] {
		t.Fatalf("hosts = %#v", hosts)
	}
	nothing := Build([]ingress.Ingress{resource}, Options{IngressClass: "missing"})
	if len(nothing.Cases) != 0 || len(nothing.Unsupported) != 0 {
		t.Fatalf("plan = %#v", nothing)
	}
}

func TestBuildAnnotationFindings(t *testing.T) {
	annotations := map[string]string{
		annotationUseRegex:                                 "true",
		annotationPrefix + "proxy-body-size":               "8m",
		annotationPrefix + "shield-routing":                "on",
		"kubectl.kubernetes.io/last-applied-configuration": "{}",
		"kubernetes.io/ingress.class":                      "nginx",
	}
	result := Build([]ingress.Ingress{pathIngress("checkout", "api.example.com", "/api", "Prefix", annotations)}, Options{})
	if len(result.Unsupported) != 2 {
		t.Fatalf("findings = %#v", result.Unsupported)
	}
	if result.Unsupported[0].Annotation != annotationPrefix+"proxy-body-size" ||
		!strings.Contains(result.Unsupported[0].Message, "cannot be tested black-box") {
		t.Fatalf("first finding = %#v", result.Unsupported[0])
	}
	if result.Unsupported[1].Annotation != annotationPrefix+"shield-routing" ||
		!strings.Contains(result.Unsupported[1].Message, "unknown to varco") {
		t.Fatalf("second finding = %#v", result.Unsupported[1])
	}
}

func TestBuildAppRootAndRedirectAnnotations(t *testing.T) {
	annotations := map[string]string{
		annotationAppRoot:           "/home",
		annotationPermanentRedirect: "https://example.org$request_uri",
	}
	result := Build([]ingress.Ingress{pathIngress("marketing", "www.example.org", "/landing", "Prefix", annotations)}, Options{})
	byFamily := pathsByFamily(result)
	if !reflect.DeepEqual(byFamily[FamilyAppRoot], []string{"/"}) {
		t.Fatalf("app-root cases = %#v", byFamily[FamilyAppRoot])
	}
	if !reflect.DeepEqual(byFamily[FamilyRedirectAnnotation], []string{"/landing"}) {
		t.Fatalf("redirect cases = %#v", byFamily[FamilyRedirectAnnotation])
	}
	for _, item := range result.Cases {
		switch item.Family {
		case FamilyAppRoot:
			if item.Source.Annotation != annotationAppRoot || !strings.Contains(item.Note, "app-root") {
				t.Fatalf("app-root case = %#v", item)
			}
		case FamilyRedirectAnnotation:
			if item.Source.Annotation != annotationPermanentRedirect {
				t.Fatalf("redirect case source = %#v", item.Source)
			}
		}
	}
}

func TestBuildSSLRedirectWithoutTLS(t *testing.T) {
	annotations := map[string]string{annotationForceSSLRedirect: "true"}
	result := Build([]ingress.Ingress{pathIngress("status", "status.example.com", "/status", "Prefix", annotations)}, Options{})
	byFamily := pathsByFamily(result)
	if !reflect.DeepEqual(byFamily[FamilyHTTPRedirect], []string{"//status", "/status", "/status/", "/status/varco-probe", "/statusvarco", "/varco/../status"}) {
		t.Fatalf("http-redirect cases = %#v", byFamily[FamilyHTTPRedirect])
	}
	for _, item := range result.Cases {
		if item.Scheme != SchemeHTTP {
			t.Fatalf("case without TLS uses scheme %s", item.Scheme)
		}
		if item.Family == FamilyHTTPRedirect && item.Source.Annotation != annotationForceSSLRedirect {
			t.Fatalf("redirect source = %#v", item.Source)
		}
	}
}

// TestBuildDisabledSSLRedirectSkipsRedirect covers fix B: ssl-redirect is true
// by default for a TLS host, but an explicit false turns the plain-HTTP redirect
// off and is recorded as a note instead.
func TestBuildDisabledSSLRedirectSkipsRedirect(t *testing.T) {
	tests := []struct {
		name       string
		annotation string
	}{
		{name: "ssl-redirect", annotation: annotationSSLRedirect},
		{name: "force-ssl-redirect", annotation: annotationForceSSLRedirect},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resource := pathIngress("checkout", "api.example.com", "/api", "Prefix", map[string]string{test.annotation: "false"})
			resource.TLS = []ingress.TLS{{Hosts: []string{"api.example.com"}}}
			result := Build([]ingress.Ingress{resource}, Options{})
			if got := pathsByFamily(result)[FamilyHTTPRedirect]; len(got) != 0 {
				t.Fatalf("%s false must plan no http-redirect case, got %#v", test.name, got)
			}
			sawNote := false
			for _, item := range result.Cases {
				if item.Scheme != SchemeHTTPS {
					t.Fatalf("case %s uses scheme %s", item.Path, item.Scheme)
				}
				if strings.Contains(item.Note, "explicitly false") {
					sawNote = true
				}
			}
			if !sawNote {
				t.Fatal("an explicitly disabled redirect must be explained in a note")
			}
		})
	}
}

// TestBuildTLSEntryWithoutHosts covers fix A: a spec.tls entry with no hosts is
// the default certificate and covers every rule host of the Ingress.
func TestBuildTLSEntryWithoutHosts(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		options  Options
		wantHost string
	}{
		{name: "literal host", host: "api.example.com", wantHost: "api.example.com"},
		{name: "wildcard host", host: "*.example.com", wantHost: "varco-probe.example.com"},
		{name: "default host", options: Options{DefaultHost: "internal.svc"}, wantHost: "internal.svc"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resource := pathIngress("checkout", test.host, "/api", "Prefix", nil)
			resource.TLS = []ingress.TLS{{SecretName: "default-tls"}}
			result := Build([]ingress.Ingress{resource}, test.options)
			if len(result.Cases) == 0 {
				t.Fatal("no cases planned")
			}
			redirects := 0
			for _, item := range result.Cases {
				if isSyntheticFamily(item.Family) {
					continue
				}
				if item.Family == FamilyHTTPRedirect {
					if item.Scheme != SchemeHTTP {
						t.Fatalf("redirect case uses scheme %s", item.Scheme)
					}
					redirects++
					continue
				}
				if item.Scheme != SchemeHTTPS || item.SNI != test.wantHost {
					t.Fatalf("case %#v is not served over TLS", item)
				}
			}
			if redirects == 0 {
				t.Fatal("a host covered by a hostless TLS entry must still plan the HTTP redirect")
			}
		})
	}
}

// TestBuildOmittedPathIsRoot covers fix F: an omitted path is treated as "/".
func TestBuildOmittedPathIsRoot(t *testing.T) {
	tests := []struct {
		name     string
		pathType string
	}{
		{name: "implementation-specific", pathType: "ImplementationSpecific"},
		{name: "untyped", pathType: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resource := pathIngress("root", "www.example.com", "", test.pathType, nil)
			result := Build([]ingress.Ingress{resource}, Options{})
			if len(result.Unsupported) != 0 {
				t.Fatalf("an omitted path must not be a finding: %#v", result.Unsupported)
			}
			if got := pathsByFamily(result)[FamilyBase]; !reflect.DeepEqual(got, []string{"/"}) {
				t.Fatalf("base path = %#v, want [\"/\"]", got)
			}
			for _, item := range result.Cases {
				if item.Family == FamilyBase && !strings.Contains(item.Note, "omitted path") {
					t.Fatalf("omitted-path case note = %q", item.Note)
				}
				if !strings.HasPrefix(item.Path, "/") {
					t.Fatalf("case path %q is not rooted", item.Path)
				}
			}
		})
	}
}

func TestBuildTemporalRedirectAndAppRootWithoutHost(t *testing.T) {
	resource := ingress.Ingress{
		APIVersion:  ingress.APIVersion,
		Kind:        ingress.KindIngress,
		Namespace:   "web",
		Name:        "marketing",
		Annotations: map[string]string{annotationAppRoot: "/home", annotationTemporalRedirect: "https://example.org"},
		Rules:       []ingress.Rule{{Paths: []ingress.Path{{Path: "/old", PathType: "Prefix"}}}},
	}
	result := Build([]ingress.Ingress{resource}, Options{})
	for _, item := range result.Cases {
		if item.Family == FamilyAppRoot {
			t.Fatalf("app-root must not be planned without a host: %#v", item)
		}
	}
	if !reflect.DeepEqual(findingCodes(result.Unsupported), []diag.Code{diag.CodePlanRuleWithoutHost}) {
		t.Fatalf("findings = %#v", result.Unsupported)
	}
}

// TestBuildSafety asserts the plan never emits an unsafe method or a credential header.
func TestBuildSafety(t *testing.T) {
	annotations := map[string]string{
		annotationEnableCORS:       "true",
		annotationCORSAllowHeaders: "Authorization, X-Api-Key",
		annotationRewriteTarget:    "/$2",
		annotationUseRegex:         "true",
	}
	resource := pathIngress("checkout", "api.example.com", "/catalog(/|$)(.*)", "ImplementationSpecific", annotations)
	resource.TLS = []ingress.TLS{{Hosts: []string{"api.example.com"}}}
	result := Build([]ingress.Ingress{resource}, Options{})
	if len(result.Cases) == 0 {
		t.Fatal("no cases planned")
	}
	for _, item := range result.Cases {
		switch item.Method {
		case MethodGet, MethodHead, MethodOptions:
		default:
			t.Fatalf("case %#v uses an unsafe method", item)
		}
		for _, header := range item.Headers {
			switch strings.ToLower(header.Name) {
			case "authorization", "cookie", "proxy-authorization":
				t.Fatalf("case %#v carries a credential header", item)
			}
		}
	}
}

func TestBuildDeduplicatesAndSorts(t *testing.T) {
	resource := pathIngress("checkout", "api.example.com", "/api", "Prefix", nil)
	single := Build([]ingress.Ingress{resource}, Options{})
	double := Build([]ingress.Ingress{resource, resource}, Options{})
	if !reflect.DeepEqual(single.Cases, double.Cases) {
		t.Fatalf("identical resources must collapse: %d vs %d cases", len(single.Cases), len(double.Cases))
	}
	if got := ids(single); !reflect.DeepEqual(got, ids(double)) {
		t.Fatalf("ids = %#v vs %#v", got, ids(double))
	}
	reversed := Build([]ingress.Ingress{
		pathIngress("b", "b.example.com", "/b", "Prefix", nil),
		pathIngress("a", "a.example.com", "/a", "Prefix", nil),
	}, Options{})
	forward := Build([]ingress.Ingress{
		pathIngress("a", "a.example.com", "/a", "Prefix", nil),
		pathIngress("b", "b.example.com", "/b", "Prefix", nil),
	}, Options{})
	if !reflect.DeepEqual(ids(reversed), ids(forward)) {
		t.Fatalf("IDs depend on resource order: %#v vs %#v", ids(reversed), ids(forward))
	}
}

func ids(result Plan) []string {
	all := make([]string, 0, len(result.Cases))
	for _, item := range result.Cases {
		all = append(all, item.ID)
	}
	return all
}

func TestFinalizeSortsCases(t *testing.T) {
	result := Plan{Cases: []Case{
		{Host: "b.example.com", Path: "/x", Scheme: SchemeHTTP, Method: MethodGet, Family: FamilyBase},
		{Host: "a.example.com", Path: "/z", Scheme: SchemeHTTPS, Method: MethodGet, Family: FamilyBase},
		{Host: "a.example.com", Path: "/z", Scheme: SchemeHTTP, Method: MethodGet, Family: FamilyBase},
		{Host: "a.example.com", Path: "/a", Scheme: SchemeHTTP, Method: MethodGet, Family: FamilySubpath},
		{Host: "a.example.com", Path: "/a", Scheme: SchemeHTTP, Method: MethodGet, Family: FamilyBase},
	}}.finalize()
	want := []string{"a.example.com//a/base", "a.example.com//a/subpath", "a.example.com//z/base", "a.example.com//z/base", "b.example.com//x/base"}
	got := make([]string, 0, len(result.Cases))
	for _, item := range result.Cases {
		got = append(got, item.Host+"/"+item.Path+"/"+item.Family)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sorted cases = %#v, want %#v", got, want)
	}
	if result.Cases[2].Scheme != SchemeHTTP || result.Cases[3].Scheme != SchemeHTTPS {
		t.Fatalf("scheme ordering wrong: %#v", result.Cases)
	}
}

func TestCaseIDIgnoresHeaderOrder(t *testing.T) {
	first := Case{Host: "h", Path: "/", Scheme: SchemeHTTP, Method: MethodGet, Family: FamilyBase, Headers: []Header{{Name: "Origin", Values: []string{"o"}}, {Name: "Access-Control-Request-Method", Values: []string{"GET"}}}}
	second := first
	second.Headers = []Header{first.Headers[1], first.Headers[0]}
	if caseID(first) != caseID(second) {
		t.Fatalf("case IDs differ by header order: %s vs %s", caseID(first), caseID(second))
	}
	if len(caseID(first)) != 12 {
		t.Fatalf("case ID %q is not 12 hex characters", caseID(first))
	}
}

func TestHelpers(t *testing.T) {
	regexSamples := []struct {
		pattern string
		want    string
		ok      bool
	}{
		{pattern: "/plain", want: "/plain", ok: true},
		{pattern: "^/plain", want: "/plain", ok: true},
		{pattern: "/plain(/|$)(.*)", want: "/plain/varco-probe", ok: true},
		{pattern: "/plain(/|$)", want: "/plain/varco-probe", ok: true},
		{pattern: "/plain/", want: "/plain/", ok: true},
		{pattern: "/api/(.*)", want: "/api/varco-probe", ok: true},
		{pattern: "/api/.*", want: "/api/varco-probe", ok: true},
		{pattern: "/api(/.*)", want: "/api/varco-probe", ok: true},
		{pattern: "/api(.*)", want: "/api/varco-probe", ok: true},
		{pattern: "/api(/|$)(.*)", want: "/api/varco-probe", ok: true},
		{pattern: "/[0-9]+", ok: false},
		{pattern: "/a/[0-9]+", ok: false},
		{pattern: "plain", ok: false},
		{pattern: "/(a|b)", ok: false},
		{pattern: "/x/(.*)/$", ok: false},
	}
	for _, test := range regexSamples {
		got, ok := regexSample(test.pattern)
		if ok != test.ok || got != test.want {
			t.Fatalf("regexSample(%q) = %q, %v; want %q, %v", test.pattern, got, ok, test.want, test.ok)
		}
	}
	flips := []struct {
		path string
		want string
		ok   bool
	}{
		{path: "/abc", want: "/ABC", ok: true},
		{path: "/123", ok: false},
		{path: "/a-b_c", want: "/A-B_C", ok: true},
	}
	for _, test := range flips {
		got, ok := flipCase(test.path)
		if ok != test.ok || got != test.want {
			t.Fatalf("flipCase(%q) = %q, %v; want %q, %v", test.path, got, ok, test.want, test.ok)
		}
	}
	toggles := []struct {
		path string
		want string
		ok   bool
	}{
		{path: "/a", want: "/a/", ok: true},
		{path: "/a/", want: "/a", ok: true},
		{path: "/", ok: false},
		{path: "", ok: false},
	}
	for _, test := range toggles {
		got, ok := toggleTrailingSlash(test.path)
		if ok != test.ok || got != test.want {
			t.Fatalf("toggleTrailingSlash(%q) = %q, %v; want %q, %v", test.path, got, ok, test.want, test.ok)
		}
	}
	if got := subpath("/a/"); got != "/a/varco-probe" {
		t.Fatalf("subpath(%q) = %q", "/a/", got)
	}
	if got := subpath("/a"); got != "/a/varco-probe" {
		t.Fatalf("subpath(%q) = %q", "/a", got)
	}
	if got := dotSegments("/a"); got != "/varco/../a" {
		t.Fatalf("dotSegments = %q", got)
	}
	if got := doubleSlash("/a"); got != "//a" {
		t.Fatalf("doubleSlash = %q", got)
	}
	if !containsRegexMeta("/a[0-9]") || containsRegexMeta("/a-b_c") {
		t.Fatal("containsRegexMeta misclassifies a path")
	}
}

func TestPathTypeBehaviour(t *testing.T) {
	tests := []struct {
		pathType   string
		prefixLike bool
		note       string
	}{
		{pathType: "Exact", prefixLike: false},
		{pathType: "Prefix", prefixLike: true},
		{pathType: "ImplementationSpecific", prefixLike: true, note: "pathType ImplementationSpecific is treated as a Prefix-like match"},
		{pathType: "prefix", prefixLike: true},
		{pathType: "Weird", prefixLike: true, note: `unknown pathType "Weird" is treated as a Prefix-like match`},
		{pathType: "", prefixLike: true, note: `unknown pathType "" is treated as a Prefix-like match`},
	}
	for _, test := range tests {
		prefixLike, note := pathTypeBehaviour(test.pathType)
		if prefixLike != test.prefixLike || note != test.note {
			t.Fatalf("pathTypeBehaviour(%q) = %v, %q; want %v, %q", test.pathType, prefixLike, note, test.prefixLike, test.note)
		}
	}
}

func TestAnnotationClassification(t *testing.T) {
	notTestable := []string{
		annotationPrefix + "proxy-body-size",
		annotationPrefix + "proxy-read-timeout",
		annotationPrefix + "canary-weight",
		annotationPrefix + "auth-type",
		annotationPrefix + "whitelist-source-range",
		annotationPrefix + "ssl-passthrough",
		annotationPrefix + "configuration-snippet",
		annotationPrefix + "custom-headers",
		annotationPrefix + "cors-allow-origin",
	}
	for _, name := range notTestable {
		if !notTestableAnnotation(name) {
			t.Fatalf("%s should be classified as not testable", name)
		}
	}
	if notTestableAnnotation(annotationPrefix + "shield-routing") {
		t.Fatal("an annotation unknown to varco is not testable-by-recognition")
	}
	handled := []string{
		annotationUseRegex, annotationRewriteTarget, annotationSSLRedirect, annotationForceSSLRedirect,
		annotationEnableCORS, annotationCORSAllowHeaders, annotationAppRoot,
		annotationPermanentRedirect, annotationTemporalRedirect, AnnotationIngressClass,
	}
	for _, name := range handled {
		if !isHandledAnnotation(name) {
			t.Fatalf("%s should be handled", name)
		}
	}
	if isHandledAnnotation(annotationPrefix + "proxy-body-size") {
		t.Fatal("proxy-body-size must not be handled")
	}
	if !strings.Contains(annotationMessage(annotationPrefix+"shield-routing"), "unknown to varco") {
		t.Fatal("unknown annotation message is wrong")
	}
}

func TestJoinNotes(t *testing.T) {
	if got := joinNotes("", "a", " ", "b"); got != "a; b" {
		t.Fatalf("joinNotes = %q", got)
	}
	if got := joinNotes(); got != "" {
		t.Fatalf("joinNotes() = %q", got)
	}
}

func TestMatchesClass(t *testing.T) {
	builder := newBuilder(Options{})
	if !builder.matchesClass(ingress.Ingress{}) {
		t.Fatal("an empty class filter must match every Ingress")
	}
	classed := newBuilder(Options{IngressClass: "nginx"})
	if !classed.matchesClass(ingress.Ingress{ClassName: "nginx"}) {
		t.Fatal("spec.ingressClassName must match")
	}
	if classed.matchesClass(ingress.Ingress{ClassName: "traefik"}) {
		t.Fatal("a different class must not match")
	}
	if !classed.matchesClass(ingress.Ingress{Annotations: map[string]string{AnnotationIngressClass: "nginx"}}) {
		t.Fatal("the legacy class annotation must match")
	}
}

// TestBuildExactOnRegexHostIsPrefixLike covers accepted finding Major 2: an Exact
// path on a host whose regex scope is active is compiled by ingress-nginx as a
// prefix regex, so varco emits the subpath and prefix-boundary cases too
// (blog behavior 2).
func TestBuildExactOnRegexHostIsPrefixLike(t *testing.T) {
	tests := []struct {
		name         string
		regexHost    bool
		wantSub      bool
		wantBoundary bool
	}{
		{name: "literal host stays literal", regexHost: false, wantSub: false, wantBoundary: false},
		{name: "regex host makes Exact prefix-like", regexHost: true, wantSub: true, wantBoundary: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exact := pathIngress("exact", "exact.example.com", "/header", "Exact", nil)
			resources := []ingress.Ingress{exact}
			if test.regexHost {
				resources = append(resources, pathIngress("regex", "exact.example.com", "/api", "Prefix", map[string]string{annotationUseRegex: "true"}))
			}
			result := Build(resources, Options{})
			byFamily := pathsByFamily(result)
			gotSub := len(byFamily[FamilySubpath]) > 0
			gotBoundary := len(byFamily[FamilyPrefixBoundary]) > 0
			if gotSub != test.wantSub || gotBoundary != test.wantBoundary {
				t.Fatalf("subpath=%v boundary=%v, want %v/%v", gotSub, gotBoundary, test.wantSub, test.wantBoundary)
			}
			sawRegexNote := false
			for _, item := range result.Cases {
				if item.Source.Ingress != "ns/exact" {
					continue
				}
				if item.Family == FamilySubpath && strings.Contains(item.Note, "blog behavior 2") {
					sawRegexNote = true
				}
			}
			if test.regexHost && !sawRegexNote {
				t.Fatalf("Exact subpath note does not cite blog behavior 2: %#v", result.Cases)
			}
		})
	}
}

// TestBuildTrailingSlashNoteDirection covers accepted finding Major 2's note fix:
// the trailing-slash case states the direction it probes.
func TestBuildTrailingSlashNoteDirection(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		wantContains string
	}{
		{name: "declared with a slash probes without it", path: "/api/", wantContains: "probes the same path without it"},
		{name: "declared without a slash probes an extra slash", path: "/api", wantContains: "probes the same path with an extra slash"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := Build([]ingress.Ingress{pathIngress("a", "a.example.com", test.path, "Prefix", nil)}, Options{})
			found := false
			for _, item := range result.Cases {
				if item.Family != FamilyTrailingSlash {
					continue
				}
				found = true
				if !strings.Contains(item.Note, test.wantContains) || !strings.Contains(item.Note, "blog behavior 4") {
					t.Fatalf("note = %q, want %q", item.Note, test.wantContains)
				}
			}
			if !found {
				t.Fatal("no trailing-slash case")
			}
		})
	}
}

// TestBuildBooleanAnnotationParsing covers accepted finding Major 3: annotation
// booleans use strconv.ParseBool semantics.
func TestBuildBooleanAnnotationParsing(t *testing.T) {
	for _, value := range []string{"1", "t", "T", "TRUE", "true", "True"} {
		t.Run("true/"+value, func(t *testing.T) {
			annotations := map[string]string{annotationUseRegex: value, annotationEnableCORS: value}
			result := Build([]ingress.Ingress{pathIngress("bool", "bool.example.com", "/api", "Prefix", annotations)}, Options{})
			if len(result.Warnings) != 1 || result.Warnings[0].Code != diag.CodePlanRegexHostWide {
				t.Fatalf("warnings = %#v", result.Warnings)
			}
			if len(pathsByFamily(result)[FamilyCORSPreflight]) != 1 {
				t.Fatalf("no cors-preflight for %q", value)
			}
		})
	}
	for _, value := range []string{"0", "f", "F", "FALSE", "false", "False"} {
		t.Run("false/"+value, func(t *testing.T) {
			resource := pathIngress("bool", "bool.example.com", "/api", "Prefix", map[string]string{annotationEnableCORS: value})
			resource.TLS = []ingress.TLS{{Hosts: []string{"bool.example.com"}}}
			result := Build([]ingress.Ingress{resource}, Options{})
			if len(pathsByFamily(result)[FamilyCORSPreflight]) != 0 {
				t.Fatalf("unexpected cors-preflight for %q", value)
			}
			if len(pathsByFamily(result)[FamilyHTTPRedirect]) == 0 {
				t.Fatalf("enable-cors %q must not disable the TLS redirect", value)
			}
			if len(result.Warnings) != 0 {
				t.Fatalf("warnings = %#v", result.Warnings)
			}
		})
	}
}

// TestBuildUnparsableBooleanWarning covers accepted finding Major 3: an
// unparsable boolean is unset and warned about with VAR-PLAN-0007.
func TestBuildUnparsableBooleanWarning(t *testing.T) {
	for _, name := range []string{annotationUseRegex, annotationSSLRedirect, annotationForceSSLRedirect, annotationEnableCORS} {
		t.Run(name, func(t *testing.T) {
			resource := pathIngress("bool", "bool.example.com", "/api", "Prefix", map[string]string{name: "yes"})
			result := Build([]ingress.Ingress{resource}, Options{})
			found := false
			for _, warning := range result.Warnings {
				if warning.Code == diag.CodePlanAnnotationBool && strings.Contains(warning.Message, "not a boolean") {
					found = true
				}
			}
			if !found {
				t.Fatalf("warnings = %#v", result.Warnings)
			}
			if len(pathsByFamily(result)[FamilyCORSPreflight]) != 0 {
				t.Fatal("an unparsable enable-cors must be unset")
			}
			for _, item := range result.Cases {
				if item.Family == FamilyCaseFlip {
					t.Fatal("an unparsable use-regex must be unset")
				}
			}
		})
	}
}

// TestBuildRejectsControlBytes covers accepted finding Minor 4: host, path and
// header value control bytes drop the case with a VAR-PLAN-0003 finding.
func TestBuildRejectsControlBytes(t *testing.T) {
	tests := []struct {
		name       string
		resource   ingress.Ingress
		wantFamily string
	}{
		{
			name: "header value",
			resource: pathIngress("cors", "cors.example.com", "/api", "Prefix", map[string]string{
				annotationEnableCORS:       "true",
				annotationCORSAllowHeaders: "X-Bad\r\nX-Injected: 1",
			}),
			wantFamily: FamilyCORSPreflight,
		},
		{
			name:       "path control byte",
			resource:   pathIngress("bad", "bad.example.com", "/a\nb", "Prefix", nil),
			wantFamily: FamilyBase,
		},
		{
			name:       "path space",
			resource:   pathIngress("bad", "bad.example.com", "/a b", "Prefix", nil),
			wantFamily: FamilyBase,
		},
		{
			name:       "host control byte",
			resource:   pathIngress("bad", "bad\n.example.com", "/a", "Prefix", nil),
			wantFamily: FamilyBase,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := Build([]ingress.Ingress{test.resource}, Options{})
			sawFinding := false
			for _, finding := range result.Unsupported {
				if finding.Code == diag.CodePlanManifest && strings.Contains(finding.Message, "control character") {
					sawFinding = true
				}
			}
			if !sawFinding {
				t.Fatalf("findings = %#v", result.Unsupported)
			}
			for _, item := range result.Cases {
				if item.Family == test.wantFamily {
					t.Fatalf("an invalid case was not dropped: %#v", item)
				}
			}
		})
	}
}

// TestBuildNoPlannablePathFinding covers accepted finding Minor 5: an Ingress or
// rule with no plannable HTTP path yields a finding, never silence.
func TestBuildNoPlannablePathFinding(t *testing.T) {
	tests := []struct {
		name     string
		resource ingress.Ingress
		wantCode diag.Code
		wantMsg  string
	}{
		{
			name:     "rule without http",
			resource: ingress.Ingress{APIVersion: ingress.APIVersion, Kind: ingress.KindIngress, Namespace: "ns", Name: "tcp", Rules: []ingress.Rule{{Host: "tcp.example.com"}}},
			wantCode: diag.CodePlanNoPath,
			wantMsg:  "no HTTP path",
		},
		{
			name:     "defaultBackend only",
			resource: ingress.Ingress{APIVersion: ingress.APIVersion, Kind: ingress.KindIngress, Namespace: "ns", Name: "default", DefaultBackend: true},
			wantCode: diag.CodePlanNoPath,
			wantMsg:  "defaultBackend",
		},
		{
			name:     "plannable path",
			resource: pathIngress("ok", "ok.example.com", "/ok", "Prefix", nil),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := Build([]ingress.Ingress{test.resource}, Options{})
			if test.wantCode == "" {
				if len(result.Unsupported) != 0 {
					t.Fatalf("findings = %#v", result.Unsupported)
				}
				return
			}
			found := false
			for _, finding := range result.Unsupported {
				if finding.Code == test.wantCode && strings.Contains(finding.Message, test.wantMsg) {
					found = true
				}
			}
			if !found {
				t.Fatalf("findings = %#v", result.Unsupported)
			}
		})
	}
}

// TestBuildRedirectCodeAnnotations covers accepted finding Minor 6: the expected
// redirect code is read from permanent-redirect-code/temporal-redirect-code and
// recorded in the case note.
func TestBuildRedirectCodeAnnotations(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		wantCode    string
	}{
		{name: "permanent default", annotations: map[string]string{annotationPermanentRedirect: "https://example.org"}, wantCode: "301"},
		{name: "permanent override", annotations: map[string]string{annotationPermanentRedirect: "https://example.org", annotationPermanentRedirectCode: "308"}, wantCode: "308"},
		{name: "temporal default", annotations: map[string]string{annotationTemporalRedirect: "https://example.org"}, wantCode: "302"},
		{name: "temporal override", annotations: map[string]string{annotationTemporalRedirect: "https://example.org", annotationTemporalRedirectCode: "307"}, wantCode: "307"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := Build([]ingress.Ingress{pathIngress("r", "r.example.com", "/r", "Prefix", test.annotations)}, Options{})
			seen := false
			for _, item := range result.Cases {
				if item.Family != FamilyRedirectAnnotation {
					continue
				}
				seen = true
				if !strings.Contains(item.Note, "expected code "+test.wantCode) {
					t.Fatalf("note = %q, want code %s", item.Note, test.wantCode)
				}
			}
			if !seen {
				t.Fatalf("no redirect-annotation case: %#v", result.Cases)
			}
			if len(result.Unsupported) != 0 {
				t.Fatalf("findings = %#v", result.Unsupported)
			}
		})
	}
}

// TestBuildCORSHeadersWithoutEnableCORS covers accepted finding Minor 7:
// cors-allow-headers without enable-cors is reported instead of dropped.
func TestBuildCORSHeadersWithoutEnableCORS(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		wantFinding bool
		wantCase    bool
	}{
		{name: "no enable-cors", annotations: map[string]string{annotationCORSAllowHeaders: "X-Request-Id"}, wantFinding: true},
		{name: "enable-cors false", annotations: map[string]string{annotationCORSAllowHeaders: "X-Request-Id", annotationEnableCORS: "false"}, wantFinding: true},
		{name: "enable-cors true", annotations: map[string]string{annotationCORSAllowHeaders: "X-Request-Id", annotationEnableCORS: "true"}, wantCase: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := Build([]ingress.Ingress{pathIngress("cors", "cors.example.com", "/api", "Prefix", test.annotations)}, Options{})
			sawFinding := false
			for _, finding := range result.Unsupported {
				if finding.Annotation == annotationCORSAllowHeaders {
					sawFinding = true
				}
			}
			if sawFinding != test.wantFinding {
				t.Fatalf("finding = %v, want %v (%#v)", sawFinding, test.wantFinding, result.Unsupported)
			}
			gotCase := len(pathsByFamily(result)[FamilyCORSPreflight]) > 0
			if gotCase != test.wantCase {
				t.Fatalf("cors-preflight = %v, want %v", gotCase, test.wantCase)
			}
		})
	}
}
