package plan

import (
	"strings"
	"testing"

	"github.com/HarnageaGabriel/varco/internal/diag"
)

func TestConfigMapWarningsPerKey(t *testing.T) {
	keys := []string{
		"ssl-redirect", "hsts", "hsts-max-age", "hsts-include-subdomains",
		"hsts-preload", "ssl-protocols", "ssl-ciphers", "use-forwarded-headers",
		"proxy-body-size", "server-tokens", "enable-cors", "cors-allow-origin",
		"use-http2", "global-auth-url", "allow-snippet-annotations", "custom-http-errors",
	}
	config := make(map[string]string, len(keys)+1)
	for _, key := range keys {
		config[key] = "value-" + key
	}
	config["unknown"] = "ignored"
	warnings := ConfigMapWarnings(config)
	if len(warnings) != len(keys) {
		t.Fatalf("warnings = %d, want %d", len(warnings), len(keys))
	}
	for _, warning := range warnings {
		if warning.Code != diag.CodePlanConfigMap {
			t.Fatalf("code = %s", warning.Code)
		}
		if !strings.Contains(warning.Message, "controller ConfigMap sets ") {
			t.Fatalf("message = %q", warning.Message)
		}
		if strings.Contains(warning.Message, "unknown=") {
			t.Fatalf("unknown key reported: %q", warning.Message)
		}
	}
	global := ConfigMapWarnings(map[string]string{"global-auth-url": "/auth"})
	if len(global) != 1 || !strings.Contains(global[0].Message, "authentication happens before routing") {
		t.Fatalf("global-auth-url warning = %#v", global)
	}
}

// Source: ADR 0006 (redaction): ConfigMap values such as global-auth-url may carry credentials.
func TestConfigMapWarningsRedactURLValues(t *testing.T) {
	warnings := ConfigMapWarnings(map[string]string{"global-auth-url": "https://user:pw@auth.example.com/check?token=secret123"})
	if len(warnings) != 1 {
		t.Fatalf("warnings = %d, want 1", len(warnings))
	}
	for _, leaked := range []string{"pw@", "secret123"} {
		if strings.Contains(warnings[0].Message, leaked) {
			t.Fatalf("message leaks %q: %s", leaked, warnings[0].Message)
		}
	}
}
