package plan

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/ingress"
)

const (
	annotationPrefix = "nginx.ingress.kubernetes.io/"

	annotationUseRegex              = annotationPrefix + "use-regex"
	annotationRewriteTarget         = annotationPrefix + "rewrite-target"
	annotationSSLRedirect           = annotationPrefix + "ssl-redirect"
	annotationForceSSLRedirect      = annotationPrefix + "force-ssl-redirect"
	annotationEnableCORS            = annotationPrefix + "enable-cors"
	annotationCORSAllowHeaders      = annotationPrefix + "cors-allow-headers"
	annotationAppRoot               = annotationPrefix + "app-root"
	annotationPermanentRedirect     = annotationPrefix + "permanent-redirect"
	annotationTemporalRedirect      = annotationPrefix + "temporal-redirect"
	annotationPermanentRedirectCode = annotationPrefix + "permanent-redirect-code"
	annotationTemporalRedirectCode  = annotationPrefix + "temporal-redirect-code"
)

// regexMeta are the characters that make ingress-nginx match a path as a
// regular expression. A dot is deliberately absent: /v1.0 is a common literal.
const regexMeta = "^$()[]{}*+?|\\"

// nginxProfile contains every ingress-nginx-specific planning decision.
type nginxProfile struct{}

func (profile nginxProfile) prepareHost(host string, resource ingress.Ingress, builder *builder) {
	if profile.regexEnabled(builder, resource) {
		builder.markRegexHost(host, resource.Key())
	}
}

func (profile nginxProfile) prepareResource(_ int, resource ingress.Ingress, builder *builder) {
	builder.addAnnotationFindings(resource)
}

func (profile nginxProfile) preparePath(resource ingress.Ingress, path ingress.Path, hostRegex bool, builder *builder) preparedPath {
	declared := path.Path
	omitted := declared == ""
	if omitted {
		declared = "/"
	}
	prefixLike, typeNote := pathTypeBehaviour(path.PathType)
	regexIngress := profile.regexEnabled(builder, resource)
	regexPath := regexIngress || containsRegexMeta(declared)
	rewrite := resource.Annotations[annotationRewriteTarget] != ""
	regexMatch := regexPath || hostRegex

	target := declared
	derived := false
	var notes []string
	if typeNote != "" {
		notes = append(notes, typeNote)
	}
	if !prefixLike && hostRegex {
		prefixLike = true
		notes = append(notes, "an Exact path on a host where use-regex or rewrite-target is active is compiled as a prefix regex (blog behavior 2)")
	}
	if omitted {
		notes = append(notes, "an omitted path is treated as / by ingress-nginx")
	}
	if regexPath {
		sample, ok := regexSample(declared)
		if !ok {
			builder.addFinding(Finding{
				Code:    diag.CodePlanRegexSample,
				Ingress: resource.Key(),
				Path:    path.Path,
				Message: fmt.Sprintf("cannot derive a sample request path from regex %q; varco never guesses a matching path", path.Path),
			})
			return preparedPath{skip: true}
		}
		if derived = sample != path.Path; derived {
			notes = append(notes, fmt.Sprintf("path %q is a regex and is sampled as %q", path.Path, sample))
		}
		target = sample
	}
	if rewrite {
		notes = append(notes, "rewrite-target rewrites the upstream path (blog behavior 3)")
		if value := resource.Annotations[annotationRewriteTarget]; strings.Contains(value, "$") || regexPath {
			notes = append(notes, "rewrite-target capture groups enable regex matching for the whole host")
		}
	}

	var extraRequests []requested
	if regexPath || hostRegex {
		if flipped, ok := flipCase(target); ok {
			extraRequests = append(extraRequests, requested{
				family: FamilyCaseFlip,
				path:   flipped,
				method: MethodGet,
				note:   caseFlipNote(regexIngress, regexPath),
			})
		}
	}
	if enabled, _ := profile.annotationBool(builder, resource, annotationEnableCORS); enabled {
		extraRequests = append(extraRequests, requested{
			family:     FamilyCORSPreflight,
			path:       target,
			method:     MethodOptions,
			headers:    corsHeaders(resource),
			annotation: annotationEnableCORS,
			note:       "enable-cors answers an OPTIONS preflight",
		})
	}
	for _, redirect := range []struct {
		name        string
		codeName    string
		defaultCode string
	}{
		{annotationPermanentRedirect, annotationPermanentRedirectCode, "301"},
		{annotationTemporalRedirect, annotationTemporalRedirectCode, "302"},
	} {
		if _, ok := resource.Annotations[redirect.name]; !ok {
			continue
		}
		code := strings.TrimSpace(resource.Annotations[redirect.codeName])
		if code == "" {
			code = redirect.defaultCode
		}
		extraRequests = append(extraRequests, requested{
			family:     FamilyRedirectAnnotation,
			path:       target,
			method:     MethodGet,
			annotation: redirect.name,
			note:       fmt.Sprintf("%s answers with a redirect (expected code %s)", strings.TrimPrefix(redirect.name, annotationPrefix), code),
		})
	}

	return preparedPath{
		target:             target,
		prefixLike:         prefixLike && !derived,
		allowTrailingSlash: !regexPath,
		rewrite:            rewrite,
		prefixBoundaryNote: prefixBoundaryNote(regexMatch),
		trailingSlashNote:  trailingSlashNote(target),
		notes:              notes,
		extraRequests:      extraRequests,
	}
}

func (nginxProfile) addAppRoot(index int, resource ingress.Ingress, builder *builder) {
	value, ok := resource.Annotations[annotationAppRoot]
	if !ok {
		return
	}
	for ruleIndex, rule := range builder.hosts[index] {
		if !rule.ok {
			continue
		}
		source := Source{Ingress: resource.Key(), Rule: ruleIndex, Annotation: annotationAppRoot}
		note := fmt.Sprintf("app-root %q redirects / to the application root (source points at the Ingress first rule with a host)", value)
		builder.emit(resource, rule.host, source, []requested{{family: FamilyAppRoot, path: "/", method: MethodGet, annotation: annotationAppRoot}}, note)
		return
	}
}

func (profile nginxProfile) httpRedirect(resource ingress.Ingress, tlsHost bool, builder *builder) redirectPlan {
	enabled, set := profile.sslRedirectAnnotation(builder, resource)
	if set && !enabled && tlsHost {
		return redirectPlan{sharedNote: "ssl-redirect/force-ssl-redirect is explicitly false, so varco plans no plain-HTTP redirect for this TLS host"}
	}
	return redirectPlan{
		enabled:    enabled || tlsHost,
		annotation: redirectAnnotation(resource),
		note:       "ingress-nginx answers plain HTTP with a redirect to HTTPS when TLS or ssl-redirect is configured for the host",
	}
}

// markRegexHost records that a host is matched as a regex and warns once per
// host, because the scope is host-wide and spans Ingress resources.
func (b *builder) markRegexHost(host, ingressKey string) {
	if _, ok := b.regexHosts[host]; ok {
		return
	}
	b.regexHosts[host] = ingressKey
	b.addWarning(Warning{
		Code:    diag.CodePlanRegexHostWide,
		Ingress: ingressKey,
		Message: fmt.Sprintf("use-regex or rewrite-target on %s makes ingress-nginx match every path of host %s as a case-insensitive regex", ingressKey, host),
	})
}

// addAnnotationFindings reports every nginx annotation varco cannot test
// black-box, once per Ingress and annotation name.
func (b *builder) addAnnotationFindings(resource ingress.Ingress) {
	names := make([]string, 0, len(resource.Annotations))
	for name := range resource.Annotations {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !strings.HasPrefix(name, annotationPrefix) {
			continue
		}
		if name == annotationCORSAllowHeaders {
			if enabled, _ := (nginxProfile{}).annotationBool(b, resource, annotationEnableCORS); !enabled {
				b.addFinding(Finding{
					Code:       diag.CodePlanAnnotationNotTestable,
					Ingress:    resource.Key(),
					Annotation: name,
					Message:    "annotation cors-allow-headers has no effect without enable-cors: true; no case is generated from it",
				})
			}
			continue
		}
		if isHandledAnnotation(name) {
			continue
		}
		b.addFinding(Finding{
			Code:       diag.CodePlanAnnotationNotTestable,
			Ingress:    resource.Key(),
			Annotation: name,
			Message:    annotationMessage(name),
		})
	}
}

func trailingSlashNote(declared string) string {
	if strings.HasSuffix(declared, "/") {
		return "declared with a trailing slash; probes the same path without it (blog behavior 4)"
	}
	return "declared without a trailing slash; probes the same path with an extra slash (blog behavior 4); probes slash handling, expected behavior unverified (VERIFY)"
}

func pathTypeBehaviour(pathType string) (prefixLike bool, note string) {
	switch strings.ToLower(strings.TrimSpace(pathType)) {
	case "exact":
		return false, ""
	case "prefix":
		return true, ""
	case "implementationspecific":
		return true, "pathType ImplementationSpecific is treated as a Prefix-like match"
	default:
		return true, fmt.Sprintf("unknown pathType %q is treated as a Prefix-like match", pathType)
	}
}

func (profile nginxProfile) regexEnabled(builder *builder, resource ingress.Ingress) bool {
	if enabled, _ := profile.annotationBool(builder, resource, annotationUseRegex); enabled {
		return true
	}
	return resource.Annotations[annotationRewriteTarget] != ""
}

func (profile nginxProfile) annotationBool(builder *builder, resource ingress.Ingress, name string) (value, set bool) {
	raw, ok := resource.Annotations[name]
	if !ok {
		return false, false
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		if builder != nil {
			builder.addWarning(Warning{
				Code:    diag.CodePlanAnnotationBool,
				Ingress: resource.Key(),
				Message: fmt.Sprintf("annotation %s has value %q which is not a boolean; varco treats it as unset", name, raw),
			})
		}
		return false, false
	}
	return parsed, true
}

func (profile nginxProfile) sslRedirectAnnotation(builder *builder, resource ingress.Ingress) (enabled, set bool) {
	for _, name := range []string{annotationSSLRedirect, annotationForceSSLRedirect} {
		if value, ok := profile.annotationBool(builder, resource, name); ok {
			set = true
			enabled = enabled || value
		}
	}
	return enabled, set
}

func redirectAnnotation(resource ingress.Ingress) string {
	for _, name := range []string{annotationSSLRedirect, annotationForceSSLRedirect} {
		if _, ok := resource.Annotations[name]; ok {
			return name
		}
	}
	return ""
}

func containsRegexMeta(path string) bool {
	return strings.ContainsAny(path, regexMeta)
}

func regexSample(pattern string) (string, bool) {
	candidate := strings.TrimPrefix(pattern, "^")
	if !strings.HasPrefix(candidate, "/") {
		return "", false
	}
	for _, suffix := range []string{"(/|$)(.*)", "(/|$)", "(/.*)", "/(.*)", "/.*", "(.*)"} {
		if literal, ok := strings.CutSuffix(candidate, suffix); ok {
			if literal == "" || containsRegexMeta(literal) {
				return "", false
			}
			return strings.TrimSuffix(literal, "/") + "/" + probeSegment, true
		}
	}
	if containsRegexMeta(candidate) {
		return "", false
	}
	return candidate, true
}

func flipCase(path string) (string, bool) {
	var flipped strings.Builder
	flipped.Grow(len(path))
	changed := false
	for _, r := range path {
		switch {
		case unicode.IsUpper(r):
			flipped.WriteRune(unicode.ToLower(r))
			changed = true
		case unicode.IsLower(r):
			flipped.WriteRune(unicode.ToUpper(r))
			changed = true
		default:
			flipped.WriteRune(r)
		}
	}
	if !changed {
		return "", false
	}
	return flipped.String(), true
}

func prefixBoundaryNote(regexPath bool) string {
	if regexPath {
		return "with use-regex the path is matched as a prefix regex, so /api may match /apivarco (blog behavior 1)"
	}
	return "Prefix matches by path element, so /api must not match /apivarco (blog behavior 1)"
}

func caseFlipNote(regexIngress, regexPath bool) string {
	switch {
	case regexIngress:
		return "use-regex or rewrite-target makes matching case-insensitive (blog behaviors 1 to 3)"
	case regexPath:
		return "the path looks like a regex, so matching may be case-insensitive (blog behaviors 1 to 3)"
	default:
		return "another Ingress of this host enables regex matching, which is host-wide (blog behavior 2)"
	}
}

func corsHeaders(resource ingress.Ingress) []Header {
	headers := []Header{
		{Name: "Access-Control-Request-Method", Values: []string{MethodGet}},
		{Name: "Origin", Values: []string{"https://varco.invalid"}},
	}
	if value, ok := resource.Annotations[annotationCORSAllowHeaders]; ok {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			headers = append(headers, Header{Name: "Access-Control-Request-Headers", Values: []string{trimmed}})
		}
	}
	sort.Slice(headers, func(i, j int) bool { return headers[i].Name < headers[j].Name })
	return headers
}

func isHandledAnnotation(name string) bool {
	switch name {
	case annotationUseRegex, annotationRewriteTarget, annotationSSLRedirect, annotationForceSSLRedirect,
		annotationEnableCORS, annotationCORSAllowHeaders, annotationAppRoot,
		annotationPermanentRedirect, annotationTemporalRedirect,
		annotationPermanentRedirectCode, annotationTemporalRedirectCode, AnnotationIngressClass:
		return true
	default:
		return false
	}
}

func annotationMessage(name string) string {
	if notTestableAnnotation(name) {
		return fmt.Sprintf("annotation %s cannot be tested black-box by varco", name)
	}
	return fmt.Sprintf("annotation %s is unknown to varco; no case is generated from it", name)
}

func notTestableAnnotation(name string) bool {
	trimmed := strings.TrimPrefix(name, annotationPrefix)
	switch trimmed {
	case "upstream-vhost", "backend-protocol", "ssl-passthrough",
		"configuration-snippet", "server-snippet", "custom-headers",
		"whitelist-source-range", "denylist-source-range",
		"service-upstream", "affinity", "server-alias", "keepalive", "upstream-hash-by":
		return true
	}
	for _, prefix := range []string{"proxy-", "auth-", "canary", "limit-", "session-cookie", "opentelemetry", "modsecurity", "cors-", "server-"} {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}
