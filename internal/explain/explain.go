// Package explain maps known ingress-nginx behavior differences to stable,
// source-linked explanations for a DIFFERENT run case.
package explain

import (
	"strings"

	"github.com/HarnageaGabriel/varco/internal/model"
)

const (
	blogSource        = "https://kubernetes.io/blog/2026/02/27/ingress-nginx-before-you-migrate"
	rewriteSource     = "https://github.com/kubernetes-sigs/ingress2gateway/blob/main/pkg/i2gw/providers/ingressnginx/README.md"
	redirectSource    = "https://kubernetes.github.io/ingress-nginx/user-guide/nginx-configuration/configmap/"
	defaultBackSource = "https://kubernetes.github.io/ingress-nginx/user-guide/cli-arguments/"
	corsSource        = "https://kubernetes.github.io/ingress-nginx/user-guide/nginx-configuration/annotations/"
)

type rule struct {
	id      string
	summary string
	fix     string
	source  string
	matches func(family string, evaluation model.Evaluation) bool
}

// VERIFY: Envoy Gateway's ClientTrafficPolicy path-settings field name.
//
// The rules are an ordered slice so output never depends on map iteration.
// Rules without the Kubernetes migration blog as their primary source carry
// VERIFY in both their summary and this comment.
var rules = []rule{
	{
		id:      "nginx-regex-case-insensitive",
		summary: "ingress-nginx matches use-regex/rewrite-target paths case-insensitively; Gateway API matches are case-sensitive.",
		fix:     "Use a RegularExpression match with (?i), or correct the path casing.",
		source:  blogSource,
		matches: func(family string, evaluation model.Evaluation) bool {
			return family == "case-flip" && hasDifference(evaluation, func(field, _ string) bool {
				return field == "status"
			})
		},
	},
	{
		id:      "nginx-regex-prefix-host-wide",
		summary: "ingress-nginx regex paths are prefix matches and use-regex is host-wide.",
		fix:     "Make the intended match explicit with Exact, or use RegularExpression with an explicit suffix.",
		source:  blogSource,
		matches: func(family string, evaluation model.Evaluation) bool {
			return (family == "prefix-boundary" || family == "subpath") && hasDifference(evaluation, func(field, _ string) bool {
				return field == "status"
			})
		},
	},
	{
		id:      "nginx-trailing-slash-redirect",
		summary: "ingress-nginx redirects a path missing a trailing slash with 301.",
		fix:     "Add an explicit RequestRedirect filter.",
		source:  blogSource,
		matches: func(family string, evaluation model.Evaluation) bool {
			return family == "trailing-slash" && hasDifference(evaluation, func(field, oldValue string) bool {
				return field == "status" && oldValue == "301"
			})
		},
	},
	{
		id:      "nginx-url-normalization",
		summary: "ingress-nginx normalizes URL dot-segments and repeated slashes before matching.",
		fix:     "Check the new controller's path normalization settings (Envoy Gateway ClientTrafficPolicy path settings: VERIFY name).",
		source:  blogSource,
		matches: func(family string, evaluation model.Evaluation) bool {
			return (family == "dot-segments" || family == "double-slash") && hasDifference(evaluation, func(field, _ string) bool {
				return field == "status" || field == "header.location"
			})
		},
	},
	{
		id:      "ingress2gateway-rewrite-target",
		summary: "rewrite-target may be translated differently; capture groups are not supported by ingress2gateway.",
		fix:     "Compare the upstream path the backend receives; use URLRewrite ReplacePrefixMatch or ReplaceFullPath.",
		source:  rewriteSource,
		matches: func(family string, evaluation model.Evaluation) bool {
			return family == "rewrite" && hasDifference(evaluation, func(field, _ string) bool {
				return field == "body"
			})
		},
	},
	{
		// VERIFY: ingress-nginx's default plain-HTTP redirect status (308 vs 301).
		id:      "nginx-http-redirect",
		summary: "VERIFY: the plain-HTTP redirect differs in status code or presence.",
		fix:     "Configure an explicit HTTP-to-HTTPS RequestRedirect with the same status code; VERIFY the ingress-nginx default status code (308 vs 301).",
		source:  redirectSource,
		matches: func(family string, evaluation model.Evaluation) bool {
			return family == "http-redirect" && hasDifference(evaluation, func(field, _ string) bool {
				return field == "status"
			})
		},
	},
	{
		// VERIFY: the default backend and default certificate source for this rule.
		id:      "nginx-default-backend",
		summary: "VERIFY: unknown hosts may reach a different default backend or default certificate.",
		fix:     "Decide whether the new controller should serve a catch-all and which certificate; usually acceptable via an exception with a reason.",
		source:  defaultBackSource,
		matches: func(family string, evaluation model.Evaluation) bool {
			return family == "default-backend" && hasDifference(evaluation, func(field, _ string) bool {
				return field == "status" || field == "body" || strings.HasPrefix(field, "certificate")
			})
		},
	},
	{
		// VERIFY: the ingress-nginx enable-cors defaults source for this rule.
		id:      "ingress-nginx-cors-defaults",
		summary: "VERIFY: ingress-nginx enable-cors defaults differ.",
		fix:     "Configure the CORS filter explicitly with allowed origins, methods, headers and max-age.",
		source:  corsSource,
		matches: func(family string, evaluation model.Evaluation) bool {
			return family == "cors-preflight" && hasDifference(evaluation, func(field, _ string) bool {
				return strings.HasPrefix(field, "header.access-control-")
			})
		},
	},
}

// Explain returns the ordered explanations applicable to a DIFFERENT case.
func Explain(evaluation model.Evaluation, family string) []model.Explanation {
	if evaluation.Outcome != model.OutcomeDifferent {
		return nil
	}
	var explanations []model.Explanation
	for _, item := range rules {
		if item.matches(family, evaluation) {
			explanations = append(explanations, model.Explanation{
				ID:      item.id,
				Summary: item.summary,
				Fix:     item.fix,
				Source:  item.source,
			})
		}
	}
	return explanations
}

func hasDifference(evaluation model.Evaluation, matches func(field, oldValue string) bool) bool {
	for _, difference := range evaluation.Differences {
		if matches(difference.Field, difference.Old) {
			return true
		}
	}
	return false
}
