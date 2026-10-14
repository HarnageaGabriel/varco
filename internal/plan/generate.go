package plan

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/HarnageaGabriel/varco/internal/diag"
	"github.com/HarnageaGabriel/varco/internal/ingress"
)

// HTTP schemes used by cases.
const (
	// SchemeHTTP is the plain-text scheme.
	SchemeHTTP = "http"
	// SchemeHTTPS is the TLS scheme, used for hosts with spec.tls or ssl-redirect.
	SchemeHTTPS = "https"
)

// HTTP methods used by cases. varco plans safe methods only.
const (
	// MethodGet is the base method for every family.
	MethodGet = "GET"
	// MethodHead is reserved for HEAD-only cases; no family emits it yet.
	MethodHead = "HEAD"
	// MethodOptions is used for CORS preflight.
	MethodOptions = "OPTIONS"
)

// Case families. Each family isolates one ingress-nginx behavior so a
// difference can be attributed to it. Numbered behaviors refer to
// https://kubernetes.io/blog/2026/02/27/ingress-nginx-before-you-migrate and
// docs/adr/0005-plan-case-families.md.
const (
	// FamilyBase is the request target exactly as declared in the manifest.
	FamilyBase = "base"
	// FamilyTrailingSlash toggles the trailing slash (blog behavior 4).
	FamilyTrailingSlash = "trailing-slash"
	// FamilySubpath appends the probe segment below a Prefix-like path.
	FamilySubpath = "subpath"
	// FamilyPrefixBoundary probes element-wise Prefix matching (blog behavior 1).
	FamilyPrefixBoundary = "prefix-boundary"
	// FamilyCaseFlip flips the letter case of the request path (blog behaviors 1 to 3).
	FamilyCaseFlip = "case-flip"
	// FamilyDotSegments probes dot-segment normalization (blog behavior 5).
	FamilyDotSegments = "dot-segments"
	// FamilyDoubleSlash probes repeated-slash normalization (blog behavior 5).
	FamilyDoubleSlash = "double-slash"
	// FamilyHTTPRedirect probes the plain-HTTP redirect to HTTPS.
	FamilyHTTPRedirect = "http-redirect"
	// FamilyCORSPreflight is an OPTIONS preflight for annotation enable-cors.
	FamilyCORSPreflight = "cors-preflight"
	// FamilyRewrite marks base and subpath cases for a rewrite-target (blog behavior 3).
	FamilyRewrite = "rewrite"
	// FamilyAppRoot probes the app-root redirect target.
	FamilyAppRoot = "app-root"
	// FamilyRedirectAnnotation probes permanent-redirect and temporal-redirect.
	FamilyRedirectAnnotation = "redirect-annotation"
)

// AnnotationIngressClass is the legacy class annotation varco honors next to
// spec.ingressClassName.
const AnnotationIngressClass = "kubernetes.io/ingress.class"

const (
	annotationPrefix = "nginx.ingress.kubernetes.io/"

	annotationUseRegex          = annotationPrefix + "use-regex"
	annotationRewriteTarget     = annotationPrefix + "rewrite-target"
	annotationSSLRedirect       = annotationPrefix + "ssl-redirect"
	annotationForceSSLRedirect  = annotationPrefix + "force-ssl-redirect"
	annotationEnableCORS        = annotationPrefix + "enable-cors"
	annotationCORSAllowHeaders  = annotationPrefix + "cors-allow-headers"
	annotationAppRoot           = annotationPrefix + "app-root"
	annotationPermanentRedirect = annotationPrefix + "permanent-redirect"
	annotationTemporalRedirect  = annotationPrefix + "temporal-redirect"
)

// wildcardProbeHost replaces the leading "*" of a wildcard rule host.
const wildcardProbeHost = "varco-probe"

// probeSegment is appended below a Prefix-like path to test subpath routing.
const probeSegment = "varco-probe"

// regexMeta are the characters that make ingress-nginx match a path as a
// regular expression. A dot is deliberately absent: /v1.0 is a common literal.
const regexMeta = "^$()[]{}*+?|\\"

// builder accumulates cases, findings, and warnings while planning.
type builder struct {
	options    Options
	plan       Plan
	hosts      [][]resolvedRule
	regexHosts map[string]string
	tlsHosts   map[string]bool
}

// resolvedRule is the effective host of one rule, resolved once.
type resolvedRule struct {
	host string
	ok   bool
}

// requested is one case before the host scheme and SNI are resolved.
type requested struct {
	family     string
	path       string
	method     string
	headers    []Header
	annotation string
	note       string
}

func newBuilder(options Options) *builder {
	return &builder{options: options, regexHosts: map[string]string{}, tlsHosts: map[string]bool{}}
}

// run filters resources by class, resolves every rule host once, then generates
// the cases.
func (b *builder) run(resources []ingress.Ingress) {
	included := make([]ingress.Ingress, 0, len(resources))
	for _, resource := range resources {
		if !b.matchesClass(resource) {
			continue
		}
		included = append(included, resource)
	}
	b.hosts = make([][]resolvedRule, len(included))
	for index, resource := range included {
		b.resolveHosts(index, resource)
	}
	for index, resource := range included {
		b.addResource(index, resource)
	}
}

// resolveHosts resolves the host of every rule, records the findings for rules
// varco cannot plan, and classifies hosts that are regex-matched or served
// over TLS.
func (b *builder) resolveHosts(index int, resource ingress.Ingress) {
	resolved := make([]resolvedRule, len(resource.Rules))
	for ruleIndex, rule := range resource.Rules {
		switch {
		case rule.Host == "":
			if b.options.DefaultHost == "" {
				b.addFinding(Finding{
					Code:    diag.CodePlanRuleWithoutHost,
					Ingress: resource.Key(),
					Message: "rule has no host and no --default-host was given; no case can be planned for it",
				})
				continue
			}
			resolved[ruleIndex] = resolvedRule{host: b.options.DefaultHost, ok: true}
		case strings.HasPrefix(rule.Host, "*."):
			substituted := wildcardProbeHost + strings.TrimPrefix(rule.Host, "*")
			resolved[ruleIndex] = resolvedRule{host: substituted, ok: true}
			b.addWarning(Warning{
				Code:    diag.CodePlanWildcardHost,
				Ingress: resource.Key(),
				Message: fmt.Sprintf("wildcard host %q is planned as %q", rule.Host, substituted),
			})
		default:
			resolved[ruleIndex] = resolvedRule{host: rule.Host, ok: true}
		}
		if regexEnabled(resource) {
			b.markRegexHost(resolved[ruleIndex].host, resource.Key())
		}
		if resourceHasTLS(resource, rule.Host, resolved[ruleIndex].host) {
			b.tlsHosts[resolved[ruleIndex].host] = true
		}
	}
	b.hosts[index] = resolved
}

// markRegexHost records that a host is matched as a regex and warns once per
// host, because the scope is host-wide and spans Ingress resources (blog
// behaviors 2 and 3).
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

// addResource plans one Ingress: annotation findings, one pass over rule hosts
// and paths, then the Ingress-wide app-root case.
func (b *builder) addResource(index int, resource ingress.Ingress) {
	b.addAnnotationFindings(resource)
	for ruleIndex, rule := range resource.Rules {
		resolved := b.hosts[index][ruleIndex]
		if !resolved.ok {
			continue
		}
		for pathIndex, path := range rule.Paths {
			source := Source{Ingress: resource.Key(), Rule: ruleIndex, Path: pathIndex}
			b.addPath(resource, resolved.host, path, source)
		}
	}
	b.addAppRoot(index, resource)
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
		if !strings.HasPrefix(name, annotationPrefix) || isHandledAnnotation(name) {
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

// addPath emits the cases for one rule host and one HTTP path.
func (b *builder) addPath(resource ingress.Ingress, host string, path ingress.Path, source Source) {
	prefixLike, typeNote := pathTypeBehaviour(path.PathType)
	regexIngress := regexEnabled(resource)
	regexPath := regexIngress || containsRegexMeta(path.Path)
	rewrite := resource.Annotations[annotationRewriteTarget] != ""
	_, hostRegex := b.regexHosts[host]

	target := path.Path
	derived := false
	var notes []string
	if typeNote != "" {
		notes = append(notes, typeNote)
	}
	if regexPath {
		sample, ok := regexSample(path.Path)
		if !ok {
			b.addFinding(Finding{
				Code:    diag.CodePlanRegexSample,
				Ingress: resource.Key(),
				Path:    path.Path,
				Message: fmt.Sprintf("cannot derive a sample request path from regex %q; varco never guesses a matching path", path.Path),
			})
			return
		}
		if derived = sample != path.Path; derived {
			notes = append(notes, fmt.Sprintf("path %q is a regex and is sampled as %q", path.Path, sample))
		}
		target = sample
	}
	if !strings.HasPrefix(target, "/") {
		b.addFinding(Finding{
			Code:    diag.CodePlanManifest,
			Ingress: resource.Key(),
			Path:    path.Path,
			Message: fmt.Sprintf("path %q does not start with / and is not a valid request target", path.Path),
		})
		return
	}
	baseFamily := FamilyBase
	baseAnnotation := ""
	if rewrite {
		baseFamily = FamilyRewrite
		baseAnnotation = annotationRewriteTarget
		notes = append(notes, "rewrite-target rewrites the upstream path (blog behavior 3)")
		if value := resource.Annotations[annotationRewriteTarget]; strings.Contains(value, "$") || regexPath {
			notes = append(notes, "rewrite-target capture groups enable regex matching for the whole host")
		}
	}

	requests := make([]requested, 0, 8)
	requests = append(requests, requested{family: baseFamily, path: target, method: MethodGet, annotation: baseAnnotation})
	// Regex paths never answer with a redirect for a missing trailing slash
	// (blog behavior 4), so that family only applies to literal paths.
	if !regexPath {
		if alternate, ok := toggleTrailingSlash(target); ok {
			requests = append(requests, requested{family: FamilyTrailingSlash, path: alternate, method: MethodGet, note: "ingress-nginx answers a missing trailing slash with 301 (blog behavior 4)"})
		}
	}
	if prefixLike && !derived {
		subpathFamily := FamilySubpath
		if rewrite {
			subpathFamily = FamilyRewrite
		}
		requests = append(requests, requested{family: subpathFamily, path: subpath(target), method: MethodGet, annotation: baseAnnotation})
		if target != "/" {
			requests = append(requests, requested{family: FamilyPrefixBoundary, path: target + "varco", method: MethodGet, note: prefixBoundaryNote(regexPath)})
		}
	}
	if regexPath || hostRegex {
		if flipped, ok := flipCase(target); ok {
			requests = append(requests, requested{family: FamilyCaseFlip, path: flipped, method: MethodGet, note: caseFlipNote(regexIngress, regexPath)})
		}
	}
	requests = append(requests,
		requested{family: FamilyDotSegments, path: dotSegments(target), method: MethodGet, note: "dot segments are resolved before matching (blog behavior 5)"},
		requested{family: FamilyDoubleSlash, path: doubleSlash(target), method: MethodGet, note: "repeated slashes are normalized or answered with 301 (blog behavior 5)"},
	)
	if strings.EqualFold(strings.TrimSpace(resource.Annotations[annotationEnableCORS]), "true") {
		requests = append(requests, requested{family: FamilyCORSPreflight, path: target, method: MethodOptions, headers: corsHeaders(resource), annotation: annotationEnableCORS, note: "enable-cors answers an OPTIONS preflight"})
	}
	for _, annotation := range []string{annotationPermanentRedirect, annotationTemporalRedirect} {
		if _, ok := resource.Annotations[annotation]; !ok {
			continue
		}
		requests = append(requests, requested{family: FamilyRedirectAnnotation, path: target, method: MethodGet, annotation: annotation, note: fmt.Sprintf("%s answers with a redirect", strings.TrimPrefix(annotation, annotationPrefix))})
	}
	b.emit(resource, host, source, requests, joinNotes(notes...))
}

// addAppRoot plans the app-root redirect, which applies to the whole Ingress:
// the case targets the Ingress first rule that has a host.
func (b *builder) addAppRoot(index int, resource ingress.Ingress) {
	value, ok := resource.Annotations[annotationAppRoot]
	if !ok {
		return
	}
	for ruleIndex, rule := range b.hosts[index] {
		if !rule.ok {
			continue
		}
		source := Source{Ingress: resource.Key(), Rule: ruleIndex, Annotation: annotationAppRoot}
		note := fmt.Sprintf("app-root %q redirects / to the application root (source points at the Ingress first rule with a host)", value)
		b.emit(resource, rule.host, source, []requested{{family: FamilyAppRoot, path: "/", method: MethodGet, annotation: annotationAppRoot}}, note)
		return
	}
}

// emit resolves the scheme and SNI for the host, appends the cases, and adds
// the plain-HTTP redirect case when the host serves TLS or enables ssl-redirect.
func (b *builder) emit(resource ingress.Ingress, host string, source Source, requests []requested, shared string) {
	scheme, sni := SchemeHTTP, ""
	if b.tlsHosts[host] {
		scheme, sni = SchemeHTTPS, host
	}
	for _, item := range requests {
		caseSource := source
		caseSource.Annotation = item.annotation
		b.plan.Cases = append(b.plan.Cases, Case{
			Host:    host,
			SNI:     sni,
			Scheme:  scheme,
			Method:  item.method,
			Path:    item.path,
			Headers: item.headers,
			Family:  item.family,
			Source:  caseSource,
			Note:    joinNotes(item.note, shared),
		})
	}
	if !b.tlsHosts[host] && !sslRedirect(resource) {
		return
	}
	seen := make(map[string]bool, len(requests))
	for _, item := range requests {
		if item.method != MethodGet || seen[item.path] {
			continue
		}
		seen[item.path] = true
		caseSource := source
		caseSource.Annotation = redirectAnnotation(resource)
		b.plan.Cases = append(b.plan.Cases, Case{
			Host:   host,
			Scheme: SchemeHTTP,
			Method: MethodGet,
			Path:   item.path,
			Family: FamilyHTTPRedirect,
			Source: caseSource,
			Note:   "ingress-nginx answers plain HTTP with a redirect to HTTPS when TLS or ssl-redirect is configured for the host",
		})
	}
}

func (b *builder) addFinding(finding Finding) {
	b.plan.Unsupported = append(b.plan.Unsupported, finding)
}

func (b *builder) addWarning(warning Warning) { b.plan.Warnings = append(b.plan.Warnings, warning) }

// matchesClass reports whether an Ingress belongs to the requested class, using
// spec.ingressClassName with the legacy annotation as a fallback.
func (b *builder) matchesClass(resource ingress.Ingress) bool {
	if b.options.IngressClass == "" {
		return true
	}
	return resource.ClassName == b.options.IngressClass || resource.Annotations[AnnotationIngressClass] == b.options.IngressClass
}

// pathTypeBehaviour maps a manifest pathType to Prefix-like matching and to the
// note explaining any approximation.
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

// regexEnabled reports whether an Ingress turns on regex matching for its host:
// use-regex, or rewrite-target which enables it silently (blog behaviors 2 and 3).
func regexEnabled(resource ingress.Ingress) bool {
	if value, ok := resource.Annotations[annotationUseRegex]; ok && strings.EqualFold(strings.TrimSpace(value), "true") {
		return true
	}
	return resource.Annotations[annotationRewriteTarget] != ""
}

// sslRedirect reports whether an Ingress explicitly enables the HTTP to HTTPS
// redirect through ssl-redirect or force-ssl-redirect.
func sslRedirect(resource ingress.Ingress) bool {
	for _, name := range []string{annotationSSLRedirect, annotationForceSSLRedirect} {
		if value, ok := resource.Annotations[name]; ok {
			return strings.EqualFold(strings.TrimSpace(value), "true")
		}
	}
	return false
}

// redirectAnnotation names the annotation responsible for the HTTP redirect.
func redirectAnnotation(resource ingress.Ingress) string {
	for _, name := range []string{annotationSSLRedirect, annotationForceSSLRedirect} {
		if _, ok := resource.Annotations[name]; ok {
			return name
		}
	}
	return ""
}

// resourceHasTLS reports whether spec.tls covers the rule host.
func resourceHasTLS(resource ingress.Ingress, ruleHost, effectiveHost string) bool {
	for _, entry := range resource.TLS {
		for _, host := range entry.Hosts {
			if host == ruleHost || host == effectiveHost {
				return true
			}
		}
	}
	return false
}

// containsRegexMeta reports whether a path uses regex metacharacters.
func containsRegexMeta(path string) bool {
	return strings.ContainsAny(path, regexMeta)
}

// regexSample derives one literal request path from a supported regex form.
// Only the two trivial forms documented in docs/adr/0005-plan-case-families.md
// are sampled; anything else is reported as unsupported.
func regexSample(pattern string) (string, bool) {
	candidate := strings.TrimPrefix(pattern, "^")
	if !strings.HasPrefix(candidate, "/") {
		return "", false
	}
	for _, suffix := range []string{"(/|$)(.*)", "(/|$)"} {
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

// flipCase returns the path with the case of every letter swapped.
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

// prefixBoundaryNote explains what a prefix boundary case probes. In regex mode
// ingress-nginx matches the declared path as a prefix regex, so a boundary that
// K8s Prefix matching rejects is expected to match there (blog behavior 1).
func prefixBoundaryNote(regexPath bool) string {
	if regexPath {
		return "with use-regex the path is matched as a prefix regex, so /api may match /apivarco (blog behavior 1)"
	}
	return "Prefix matches by path element, so /api must not match /apivarco (blog behavior 1)"
}

// caseFlipNote explains why a case flipped the case of a path.
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

// toggleTrailingSlash returns the path with its trailing slash toggled and
// reports whether an alternate exists.
func toggleTrailingSlash(path string) (string, bool) {
	if path == "" || path == "/" {
		return "", false
	}
	if strings.HasSuffix(path, "/") {
		return strings.TrimSuffix(path, "/"), true
	}
	return path + "/", true
}

// subpath appends the probe segment below a Prefix-like path.
func subpath(path string) string {
	if strings.HasSuffix(path, "/") {
		return path + probeSegment
	}
	return path + "/" + probeSegment
}

// dotSegments resolves a dot segment before the path (blog behavior 5).
func dotSegments(path string) string {
	return "/varco/../" + strings.TrimPrefix(path, "/")
}

// doubleSlash repeats the leading slash before the path (blog behavior 5).
func doubleSlash(path string) string {
	return "//" + strings.TrimPrefix(path, "/")
}

// corsHeaders builds the deterministic preflight headers. cors-allow-headers
// is echoed as request headers; no header carries credentials.
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

// joinNotes drops empty notes and joins the rest with a semicolon.
func joinNotes(notes ...string) string {
	kept := make([]string, 0, len(notes))
	for _, note := range notes {
		if trimmed := strings.TrimSpace(note); trimmed != "" {
			kept = append(kept, trimmed)
		}
	}
	return strings.Join(kept, "; ")
}

// isHandledAnnotation reports whether varco consumes an annotation and
// therefore must not report it as unsupported.
func isHandledAnnotation(name string) bool {
	switch name {
	case annotationUseRegex, annotationRewriteTarget, annotationSSLRedirect, annotationForceSSLRedirect,
		annotationEnableCORS, annotationCORSAllowHeaders, annotationAppRoot,
		annotationPermanentRedirect, annotationTemporalRedirect, AnnotationIngressClass:
		return true
	default:
		return false
	}
}

// annotationMessage explains why an annotation produced no case.
func annotationMessage(name string) string {
	if notTestableAnnotation(name) {
		return fmt.Sprintf("annotation %s cannot be tested black-box by varco", name)
	}
	return fmt.Sprintf("annotation %s is unknown to varco; no case is generated from it", name)
}

// notTestableAnnotation reports whether varco recognizes an annotation but
// cannot test it black-box. Recognized annotations are listed in docs/plan.md.
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
