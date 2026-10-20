package plan

import (
	"fmt"
	"strings"

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

// Planner profiles.
const (
	// ProfileNginx keeps the ingress-nginx-specific planner behavior.
	ProfileNginx = "nginx"
	// ProfileGeneric plans only behavior shared by HTTP proxies.
	ProfileGeneric = "generic"
)

// HTTP methods used by cases. varco plans safe methods only.
const (
	// MethodGet is the base method for every family.
	MethodGet = "GET"
	// MethodHead is used by the head family (RFC 9110 section 9.3.2).
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
	// FamilyDefaultBackend probes the default backend with an unknown host.
	FamilyDefaultBackend = "default-backend"
	// FamilyUnmatchedRoute probes an unknown path on a host with no catch-all.
	FamilyUnmatchedRoute = "unmatched-route"
	// FamilyHostVariant sends the same path with a modified Host header, so a
	// difference in host matching between the two controllers becomes visible.
	FamilyHostVariant = "host-variant"
	// FamilyHead sends a HEAD request on the smallest planned path of the
	// host, so a difference in HEAD handling between the two controllers
	// becomes visible.
	FamilyHead = "head"
)

// AnnotationIngressClass is the legacy class annotation varco honors next to
// spec.ingressClassName.
const AnnotationIngressClass = "kubernetes.io/ingress.class"

// wildcardProbeHost replaces the leading "*" of a wildcard rule host.
const wildcardProbeHost = "varco-probe"

// probeSegment is appended below a Prefix-like path to test subpath routing.
const probeSegment = "varco-probe"

// defaultBackendHost is the host of the default-backend probe: no rule declares
// it, so an ingress controller must answer it from its default backend.
const defaultBackendHost = "varco-unknown.invalid"

// unmatchedRoutePath is the unknown path of the unmatched-route case.
const unmatchedRoutePath = "/varco-unmatched-route"

// hostHeaderName is the header name of the host-variant cases. The probe layer
// writes this override on its own Host line so exactly one Host header is sent.
const hostHeaderName = "Host"

// syntheticIngress labels cases varco derives from the plan as a whole rather
// than from one Ingress resource.
const syntheticIngress = "varco"

// builder accumulates cases, findings, and warnings while planning.
type builder struct {
	options    Options
	profile    profileBehavior
	plan       Plan
	included   []ingress.Ingress
	hosts      [][]resolvedRule
	regexHosts map[string]bool
	// regexSources holds, per host matched as a regex, the Ingress that
	// turned the host-wide scope on.
	regexSources map[string]regexSource
	tlsHosts     map[string]bool
	// plannedPaths holds, per host, the request targets planned for it. The
	// head and host-variant families use the smallest of them, chosen once
	// every resource is processed, so the plan does not depend on the order
	// of the input.
	plannedPaths map[string][]string
}

// resolvedRule is the effective host of one rule, resolved once.
type resolvedRule struct {
	host string
	ok   bool
	// concrete records that the rule declared the host itself and it is not a
	// wildcard label. Only concrete hosts get the head and host-variant cases,
	// so those families compare the declared host, never varco's substitution.
	concrete bool
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

// profileBehavior isolates controller-specific planning from the generic HTTP
// case core. The generic profile leaves controller-only fields empty.
type profileBehavior interface {
	prepareHost(host string, resource ingress.Ingress, builder *builder)
	prepareResource(index int, resource ingress.Ingress, builder *builder)
	preparePath(resource ingress.Ingress, path ingress.Path, hostRegex bool, builder *builder) preparedPath
	addAppRoot(index int, resource ingress.Ingress, builder *builder)
	httpRedirect(resource ingress.Ingress, tlsHost bool, builder *builder) redirectPlan
	// addPlanWarnings appends the warnings that need the whole plan, so they
	// are added once every resource has been processed.
	addPlanWarnings(builder *builder)
}

type preparedPath struct {
	target             string
	prefixLike         bool
	allowTrailingSlash bool
	rewrite            bool
	prefixBoundaryNote string
	trailingSlashNote  string
	notes              []string
	extraRequests      []requested
	skip               bool
}

type redirectPlan struct {
	enabled    bool
	annotation string
	note       string
	sharedNote string
}

type genericProfile struct{}

func (genericProfile) prepareHost(string, ingress.Ingress, *builder) {}

func (profile genericProfile) prepareResource(_ int, resource ingress.Ingress, builder *builder) {
	if len(resource.Annotations) == 0 {
		return
	}
	builder.addWarning(Warning{
		Code:    diag.CodePlanAnnotationsIgnored,
		Ingress: resource.Key(),
		Message: fmt.Sprintf("%d annotations ignored by the generic profile", len(resource.Annotations)),
	})
}

func (genericProfile) preparePath(_ ingress.Ingress, path ingress.Path, _ bool, _ *builder) preparedPath {
	target := path.Path
	notes := []string(nil)
	if target == "" {
		target = "/"
		notes = append(notes, "an omitted path is planned as /")
	}
	prefixLike := strings.EqualFold(strings.TrimSpace(path.PathType), "prefix")
	boundaryNote := ""
	if prefixLike {
		boundaryNote = "Prefix matches by path element, so /api must not match /apivarco (Kubernetes Prefix semantics)"
	}
	trailingNote := "declared with a trailing slash; probes the same path without it"
	if !strings.HasSuffix(target, "/") {
		trailingNote = "declared without a trailing slash; probes the same path with an extra slash"
	}
	return preparedPath{
		target:             target,
		prefixLike:         prefixLike,
		allowTrailingSlash: true,
		prefixBoundaryNote: boundaryNote,
		trailingSlashNote:  trailingNote,
		notes:              notes,
	}
}

func (genericProfile) addAppRoot(int, ingress.Ingress, *builder) {}

func (genericProfile) addPlanWarnings(*builder) {}

func (genericProfile) httpRedirect(_ ingress.Ingress, tlsHost bool, _ *builder) redirectPlan {
	return redirectPlan{
		enabled: tlsHost,
		note:    "spec.tls covers the host, so varco plans the plain-HTTP redirect case",
	}
}

func newBuilder(options Options) *builder {
	profile := profileBehavior(nginxProfile{})
	planProfile := ProfileNginx
	if options.Profile == ProfileGeneric {
		profile = genericProfile{}
		planProfile = ProfileGeneric
	}
	if options.Profile == "" {
		options.Profile = ProfileNginx
	}
	return &builder{
		options:      options,
		profile:      profile,
		plan:         Plan{Profile: planProfile},
		regexHosts:   map[string]bool{},
		regexSources: map[string]regexSource{},
		tlsHosts:     map[string]bool{},
		plannedPaths: map[string][]string{},
	}
}

// run filters resources by class, resolves every rule host once, then generates
// the cases.
func (b *builder) run(resources []ingress.Ingress) {
	included := make([]ingress.Ingress, 0, len(resources))
	for _, resource := range resources {
		if !b.matchesClass(resource) {
			b.addWarning(b.classExclusionWarning(resource))
			continue
		}
		included = append(included, resource)
	}
	b.hosts = make([][]resolvedRule, len(included))
	for index, resource := range included {
		b.resolveHosts(index, resource)
	}
	// The warnings that name a host are added once every host is resolved,
	// so they do not depend on the order of the input.
	b.profile.addPlanWarnings(b)
	for index, resource := range included {
		b.addResource(index, resource)
	}
	b.included = included
	b.addSyntheticCases()
}

// addSyntheticCases appends the plan-wide families, all derived from the plan as
// a whole rather than from one Ingress and carried by the synthetic source:
// a default-backend probe per scheme the plan uses, one unmatched-route probe
// per host that declares no Prefix or ImplementationSpecific "/" path, and one
// head plus host-variant group per concrete host on the smallest planned path
// of that host.
func (b *builder) addSyntheticCases() {
	schemes := make(map[string]bool, 2)
	for _, item := range b.plan.Cases {
		schemes[item.Scheme] = true
	}
	for _, scheme := range []string{SchemeHTTP, SchemeHTTPS} {
		if !schemes[scheme] {
			continue
		}
		sni := ""
		if scheme == SchemeHTTPS {
			sni = defaultBackendHost
		}
		b.appendCase(Case{
			Host:   defaultBackendHost,
			SNI:    sni,
			Scheme: scheme,
			Method: MethodGet,
			Path:   "/",
			Family: FamilyDefaultBackend,
			Source: Source{Ingress: syntheticIngress},
			Note:   "an unknown host exercises the default backend and, over HTTPS, the default certificate",
		})
	}
	var hostOrder []string
	concrete := make(map[string]bool)
	seen := make(map[string]bool)
	for index := range b.hosts {
		for _, resolved := range b.hosts[index] {
			if !resolved.ok {
				continue
			}
			if !seen[resolved.host] {
				seen[resolved.host] = true
				hostOrder = append(hostOrder, resolved.host)
				if resolved.concrete {
					concrete[resolved.host] = true
				}
			}
		}
	}
	hasCatchAll := make(map[string]bool)
	for index, resource := range b.included {
		for ruleIndex, rule := range resource.Rules {
			resolved := b.hosts[index][ruleIndex]
			if !resolved.ok {
				continue
			}
			for _, candidate := range rule.Paths {
				if isCatchAllPath(candidate) {
					hasCatchAll[resolved.host] = true
				}
			}
		}
	}
	for _, host := range hostOrder {
		if hasCatchAll[host] {
			continue
		}
		scheme, sni := SchemeHTTP, ""
		if b.tlsHosts[host] {
			scheme, sni = SchemeHTTPS, host
		}
		b.appendCase(Case{
			Host:   host,
			SNI:    sni,
			Scheme: scheme,
			Method: MethodGet,
			Path:   unmatchedRoutePath,
			Family: FamilyUnmatchedRoute,
			Source: Source{Ingress: syntheticIngress},
			Note:   "the host declares no Prefix or ImplementationSpecific / path, so an unknown path exercises the default backend",
		})
	}
	b.addHostVariantAndHeadCases(hostOrder, concrete)
}

// addHostVariantAndHeadCases appends the host-variant and head cases for every
// concrete host on the smallest planned path of that host. The path is the
// lexicographically smallest request target planned for the host, chosen after
// every resource is processed, so the plan does not depend on the order of the
// input. Hosts substituted from a wildcard label or filled in from
// --default-host are skipped: comparing host handling for them would probe
// varco's substitution instead of the declared host. same-path cases carry the
// synthetic source.
func (b *builder) addHostVariantAndHeadCases(hostOrder []string, concrete map[string]bool) {
	for _, host := range hostOrder {
		if !concrete[host] {
			continue
		}
		path := smallestPath(b.plannedPaths[host])
		if path == "" {
			continue
		}
		scheme, sni := SchemeHTTP, ""
		if b.tlsHosts[host] {
			scheme, sni = SchemeHTTPS, host
		}
		port := ":443"
		if scheme == SchemeHTTP {
			port = ":80"
		}
		b.appendCase(Case{
			Host:   host,
			SNI:    sni,
			Scheme: scheme,
			Method: MethodHead,
			Path:   path,
			Family: FamilyHead,
			Source: Source{Ingress: syntheticIngress},
			Note:   "HEAD on the smallest planned path of the host; the comparison shows only whether old and new handle HEAD equivalently (RFC 9110 section 9.3.2)",
		})
		for _, variant := range []struct {
			value string
			note  string
		}{
			{strings.ToUpper(host), "the host is case-insensitive (RFC 3986 section 3.2.2)"},
			{host + ".", "the trailing dot is the fully-qualified form of the host"},
			{host + port, "the Host header may carry the port (RFC 9110 section 7.2), and this is its explicit default"},
		} {
			b.appendCase(Case{
				Host:    host,
				SNI:     sni,
				Scheme:  scheme,
				Method:  MethodGet,
				Path:    path,
				Headers: []Header{{Name: hostHeaderName, Values: []string{variant.value}}},
				Family:  FamilyHostVariant,
				Source:  Source{Ingress: syntheticIngress},
				Note:    joinNotes(variant.note, "SNI stays the plain host, so only the Host header differs; the comparison shows whether old and new are equivalent and asserts nothing about which behavior is right"),
			})
		}
	}
}

// smallestPath returns the lexicographically smallest path of a host in byte
// order, or "" when the host has no planned path.
func smallestPath(paths []string) string {
	smallest := ""
	for _, path := range paths {
		if smallest == "" || path < smallest {
			smallest = path
		}
	}
	return smallest
}

// isCatchAllPath reports whether a path matches every request on its host: a
// Prefix or ImplementationSpecific path of "/" (an omitted path counts as "/").
func isCatchAllPath(candidate ingress.Path) bool {
	if candidate.PathType != "Prefix" && candidate.PathType != "ImplementationSpecific" {
		return false
	}
	declared := candidate.Path
	if declared == "" {
		declared = "/"
	}
	return declared == "/"
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
			resolved[ruleIndex] = resolvedRule{host: rule.Host, ok: true, concrete: true}
		}
		b.profile.prepareHost(resolved[ruleIndex].host, resource, b)
		if resourceHasTLS(resource, rule.Host, resolved[ruleIndex].host) {
			b.tlsHosts[resolved[ruleIndex].host] = true
		}
	}
	b.hosts[index] = resolved
}

// addResource plans one Ingress: annotation findings, one pass over rule hosts
// and paths, then the Ingress-wide app-root case.
func (b *builder) addResource(index int, resource ingress.Ingress) {
	b.profile.prepareResource(index, resource, b)
	beforeCases := len(b.plan.Cases)
	beforeFindings := len(b.plan.Unsupported)
	ruleExplained := false
	for ruleIndex, rule := range resource.Rules {
		resolved := b.hosts[index][ruleIndex]
		if !resolved.ok {
			// resolveHosts already recorded why this rule cannot be planned.
			ruleExplained = true
			continue
		}
		if len(rule.Paths) == 0 {
			ruleExplained = true
			b.addFinding(Finding{
				Code:    diag.CodePlanNoPath,
				Ingress: resource.Key(),
				Message: fmt.Sprintf("rule %d has no HTTP path; no case can be planned for it", ruleIndex),
			})
			continue
		}
		for pathIndex, path := range rule.Paths {
			source := Source{Ingress: resource.Key(), Rule: ruleIndex, Path: pathIndex}
			b.addPath(resource, resolved.host, path, source)
		}
	}
	b.profile.addAppRoot(index, resource, b)
	if len(b.plan.Cases) == beforeCases && len(b.plan.Unsupported) == beforeFindings && !ruleExplained {
		message := "Ingress has no plannable HTTP path"
		if resource.DefaultBackend {
			message = "Ingress serves spec.defaultBackend only; varco plans no case for it"
		}
		b.addFinding(Finding{Code: diag.CodePlanNoPath, Ingress: resource.Key(), Message: message})
	}
}

// addPath emits the cases for one rule host and one HTTP path.
func (b *builder) addPath(resource ingress.Ingress, host string, path ingress.Path, source Source) {
	hostRegex := b.regexHosts[host]
	prepared := b.profile.preparePath(resource, path, hostRegex, b)
	if prepared.skip {
		return
	}
	target := prepared.target
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
	if prepared.rewrite {
		baseFamily = FamilyRewrite
		baseAnnotation = annotationRewriteTarget
	}

	requests := make([]requested, 0, 8+len(prepared.extraRequests))
	requests = append(requests, requested{family: baseFamily, path: target, method: MethodGet, annotation: baseAnnotation})
	if prepared.allowTrailingSlash {
		if alternate, ok := toggleTrailingSlash(target); ok {
			requests = append(requests, requested{family: FamilyTrailingSlash, path: alternate, method: MethodGet, note: prepared.trailingSlashNote})
		}
	}
	if prepared.prefixLike {
		subpathFamily := FamilySubpath
		if prepared.rewrite {
			subpathFamily = FamilyRewrite
		}
		requests = append(requests, requested{family: subpathFamily, path: subpath(target), method: MethodGet, annotation: baseAnnotation})
		if target != "/" {
			requests = append(requests, requested{family: FamilyPrefixBoundary, path: target + "varco", method: MethodGet, note: prepared.prefixBoundaryNote})
		}
	}
	requests = append(requests,
		requested{family: FamilyDotSegments, path: dotSegments(target), method: MethodGet, note: "dot segments are resolved before matching (blog behavior 5)"},
		requested{family: FamilyDoubleSlash, path: doubleSlash(target), method: MethodGet, note: "repeated slashes are normalized or answered with 301 (blog behavior 5)"},
	)
	requests = append(requests, prepared.extraRequests...)
	b.emit(resource, host, source, requests, joinNotes(prepared.notes...))
}

// emit resolves the scheme and SNI for the host, appends the cases, and adds
// the plain-HTTP redirect case selected by the active profile.
func (b *builder) emit(resource ingress.Ingress, host string, source Source, requests []requested, shared string) {
	scheme, sni := SchemeHTTP, ""
	tls := b.tlsHosts[host]
	if tls {
		scheme, sni = SchemeHTTPS, host
	}
	redirect := b.profile.httpRedirect(resource, tls, b)
	if redirect.sharedNote != "" {
		shared = joinNotes(shared, redirect.sharedNote)
	}
	for index, item := range requests {
		caseSource := source
		caseSource.Annotation = item.annotation
		planned := b.appendCase(Case{
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
		if index == 0 && planned {
			// The planned request target is a candidate for the head and
			// host-variant cases of this host; the smallest one is chosen
			// once every resource has been processed.
			b.plannedPaths[host] = append(b.plannedPaths[host], item.path)
		}
	}
	if !redirect.enabled {
		return
	}
	seen := make(map[string]bool, len(requests))
	for _, item := range requests {
		if item.method != MethodGet || seen[item.path] {
			continue
		}
		seen[item.path] = true
		caseSource := source
		caseSource.Annotation = redirect.annotation
		b.appendCase(Case{
			Host:   host,
			Scheme: SchemeHTTP,
			Method: MethodGet,
			Path:   item.path,
			Family: FamilyHTTPRedirect,
			Source: caseSource,
			Note:   redirect.note,
		})
	}
}

// appendCase validates a planned case and appends it, or records why varco drops
// it. It reports whether the case was appended. Validation lives in this pure
// package so a manifest value can never become a malformed request in a later
// run.
func (b *builder) appendCase(item Case) bool {
	if reason := validateCase(item); reason != "" {
		b.addFinding(Finding{
			Code:    diag.CodePlanManifest,
			Ingress: item.Source.Ingress,
			Path:    item.Path,
			Message: fmt.Sprintf("%s; varco drops this case", reason),
		})
		return false
	}
	b.plan.Cases = append(b.plan.Cases, item)
	return true
}

// validateCase reports why a case is not a valid HTTP request, or "".
func validateCase(item Case) string {
	if hasControlBytes(item.Host) {
		return fmt.Sprintf("host %q contains a control character", item.Host)
	}
	if !strings.HasPrefix(item.Path, "/") {
		return fmt.Sprintf("path %q does not start with /", item.Path)
	}
	if hasControlBytes(item.Path) || strings.ContainsRune(item.Path, ' ') {
		return fmt.Sprintf("path %q contains a control character or space", item.Path)
	}
	for _, header := range item.Headers {
		if header.Name == "" || hasControlBytes(header.Name) || strings.ContainsAny(header.Name, " :") {
			return fmt.Sprintf("header name %q is not a valid field name", header.Name)
		}
		for _, value := range header.Values {
			if hasControlBytes(value) {
				return fmt.Sprintf("header %s has a value with a control character", header.Name)
			}
		}
	}
	return ""
}

// hasControlBytes reports whether s contains CR, LF or any other control byte.
func hasControlBytes(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
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

// classExclusionWarning reports one Ingress the class filter left out of the
// plan. The excluded resource is planned for no case at all, so this warning
// is the only trace it ever had: without it a migration can look complete
// while a whole Ingress was never planned.
//
// The warning names the Ingress as namespace/name, the class it declares and
// the class that was requested. The regex scope of the plan is still computed
// over the included resources only, so an excluded use-regex Ingress can still
// change the host scope of the included ones; that is why the exclusion is
// reported.
//
// Sources: docs/plan.md (Known limitations: the excluded resource is dropped
// and the regex scope is computed over the included resources only) and ADR 0002
// (every condition varco reports carries a stable VAR- code).
func (b *builder) classExclusionWarning(resource ingress.Ingress) Warning {
	return Warning{
		Code:    diag.CodePlanIngressClassExcluded,
		Ingress: resource.Key(),
		Message: fmt.Sprintf("%s is excluded by --ingress-class %q: %s", resource.Key(), b.options.IngressClass, declaredClass(resource)),
	}
}

// declaredClass describes the class an Ingress declares: spec.ingressClassName
// first, the legacy kubernetes.io/ingress.class annotation next, and no class at
// all when it declares none. Both sources are named when they disagree,
// because the planner matches either one (matchesClass).
func declaredClass(resource ingress.Ingress) string {
	spec := resource.ClassName
	legacy := resource.Annotations[AnnotationIngressClass]
	switch {
	case spec != "" && legacy != "" && spec != legacy:
		return fmt.Sprintf("it declares class %q (spec.ingressClassName) and class %q (%s)", spec, legacy, AnnotationIngressClass)
	case spec != "":
		return fmt.Sprintf("it declares class %q (spec.ingressClassName)", spec)
	case legacy != "":
		return fmt.Sprintf("it declares class %q (%s)", legacy, AnnotationIngressClass)
	default:
		return "it declares no class"
	}
}

// resourceHasTLS reports whether spec.tls covers the rule host. A spec.tls
// entry with no hosts applies to every host of the Ingress: Kubernetes reads a
// secret-only entry as the default certificate, so every rule host is served
// over TLS.
func resourceHasTLS(resource ingress.Ingress, ruleHost, effectiveHost string) bool {
	for _, entry := range resource.TLS {
		if len(entry.Hosts) == 0 {
			return true
		}
		for _, host := range entry.Hosts {
			if host == ruleHost || host == effectiveHost {
				return true
			}
		}
	}
	return false
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
