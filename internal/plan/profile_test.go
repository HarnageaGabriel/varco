package plan

import (
	"reflect"
	"strings"
	"testing"

	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/ingress"
)

func TestProfileDefaultsToNginx(t *testing.T) {
	result := Build([]ingress.Ingress{pathIngress("plain", "plain.example.com", "/plain", "Prefix", nil)}, Options{})
	if result.Profile != ProfileNginx {
		t.Fatalf("profile = %q, want %q", result.Profile, ProfileNginx)
	}
}

func TestGenericProfileExcludesNginxFamiliesAndFindings(t *testing.T) {
	annotations := map[string]string{
		annotationUseRegex:          "true",
		annotationRewriteTarget:     "/$1",
		annotationEnableCORS:        "true",
		annotationAppRoot:           "/app",
		annotationPermanentRedirect: "https://example.com/moved",
		"example.com/setting":       "ignored",
	}
	resource := pathIngress("mixed", "api.example.com", "/api", "Prefix", annotations)
	resource.TLS = []ingress.TLS{{Hosts: []string{"api.example.com"}}}

	result := Build([]ingress.Ingress{resource}, Options{Profile: ProfileGeneric})
	if result.Profile != ProfileGeneric {
		t.Fatalf("profile = %q, want %q", result.Profile, ProfileGeneric)
	}

	forbidden := map[string]bool{
		FamilyCaseFlip:           true,
		FamilyRewrite:            true,
		FamilyAppRoot:            true,
		FamilyCORSPreflight:      true,
		FamilyRedirectAnnotation: true,
	}
	allowed := map[string]bool{
		FamilyBase:           true,
		FamilySubpath:        true,
		FamilyPrefixBoundary: true,
		FamilyTrailingSlash:  true,
		FamilyDotSegments:    true,
		FamilyDoubleSlash:    true,
		FamilyHTTPRedirect:   true,
		FamilyDefaultBackend: true,
		FamilyUnmatchedRoute: true,
	}
	for _, item := range result.Cases {
		if forbidden[item.Family] {
			t.Fatalf("generic profile emitted forbidden family %q: %#v", item.Family, item)
		}
		if !allowed[item.Family] {
			t.Fatalf("generic profile emitted unexpected family %q: %#v", item.Family, item)
		}
	}
	for _, finding := range result.Unsupported {
		if finding.Code == diag.CodePlanAnnotationNotTestable {
			t.Fatalf("generic profile emitted annotation finding: %#v", finding)
		}
	}
	wantWarnings := []Warning{{
		Code:    diag.CodePlanAnnotationsIgnored,
		Ingress: resource.Key(),
		Message: "6 annotations ignored by the generic profile",
	}}
	if !reflect.DeepEqual(result.Warnings, wantWarnings) {
		t.Fatalf("warnings = %#v, want %#v", result.Warnings, wantWarnings)
	}
}

func TestGenericProfileHTTPRedirectOnlyForTLSSpec(t *testing.T) {
	withTLS := pathIngress("tls", "tls.example.com", "/secure", "Prefix", nil)
	withTLS.TLS = []ingress.TLS{{Hosts: []string{"tls.example.com"}}}
	annotationOnly := pathIngress("annotation", "plain.example.com", "/plain", "Prefix", map[string]string{
		annotationSSLRedirect: "true",
	})

	result := Build([]ingress.Ingress{withTLS, annotationOnly}, Options{Profile: ProfileGeneric})
	seen := map[string]bool{}
	for _, item := range result.Cases {
		if item.Family == FamilyHTTPRedirect {
			seen[item.Host] = true
		}
	}
	if !seen["tls.example.com"] {
		t.Fatal("spec.tls host has no http-redirect case")
	}
	if seen["plain.example.com"] {
		t.Fatal("generic profile read ssl-redirect annotation")
	}
	for _, warning := range result.Warnings {
		if strings.Contains(warning.Message, "use-regex") || warning.Code == diag.CodePlanRegexHostWide {
			t.Fatalf("generic profile emitted regex-scope warning: %#v", warning)
		}
	}
}
