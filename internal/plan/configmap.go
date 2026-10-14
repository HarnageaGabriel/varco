package plan

import (
	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/redact"
)

// ConfigMapWarnings reports ingress-nginx ConfigMap settings that are visible
// on the wire. The fixed table keeps output deterministic and ignores unknown
// keys. Source: https://kubernetes.github.io/ingress-nginx/user-guide/nginx-configuration/configmap/
func ConfigMapWarnings(config map[string]string) []Warning {
	table := []struct{ key, what string }{
		{"ssl-redirect", "plain-HTTP redirects to HTTPS may differ; VERIFY the effective value and per-Ingress overrides"},
		{"hsts", "HSTS response headers may be present or absent"},
		{"hsts-max-age", "HSTS max-age response header may differ"},
		{"hsts-include-subdomains", "HSTS includeSubDomains response header may differ"},
		{"hsts-preload", "HSTS preload response header may differ"},
		{"ssl-protocols", "TLS protocol negotiation may differ"},
		{"ssl-ciphers", "TLS cipher negotiation may differ"},
		{"use-forwarded-headers", "X-Forwarded-* request context may change upstream behavior"},
		{"proxy-body-size", "request-body rejection and upstream body handling may differ"},
		{"server-tokens", "Server response headers may include controller version information"},
		{"enable-cors", "CORS responses and preflight behavior may differ"},
		{"cors-allow-origin", "Access-Control-Allow-Origin response values may differ"},
		{"use-http2", "HTTP/2 negotiation may differ"},
		{"global-auth-url", "authentication happens before routing so anonymous requests may all return 401 or a redirect"},
		{"allow-snippet-annotations", "annotation snippets may change request or response behavior"},
		{"custom-http-errors", "error status codes may be replaced by controller error pages"},
	}
	warnings := make([]Warning, 0, len(table))
	for _, item := range table {
		value, ok := config[item.key]
		if !ok {
			continue
		}
		warnings = append(warnings, Warning{
			Code:    diag.CodePlanConfigMap,
			Message: "controller ConfigMap sets " + item.key + "=" + shown(value) + ": " + item.what,
		})
	}
	return warnings
}

// shown prepares a ConfigMap value for a report: URL-shaped tokens are redacted
// (global-auth-url and similar can carry credentials, ADR 0006) and the value
// is capped so a huge setting cannot flood the output.
func shown(value string) string {
	value = redact.Text(value)
	if len(value) > 120 {
		return value[:120] + "..."
	}
	return value
}
