package plan_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"math/rand"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/HarnageaGabriel/varco/internal/ingress"
	"github.com/HarnageaGabriel/varco/internal/plan"
)

// This file proves that plan generation is a pure function of the input
// resources: the same resources in any order produce the same plan.
//
// Sources: CONVENTIONS.md ("Determinism: Same input, same output. No map
// iteration in observable output; cases are sorted by a stable key."),
// docs/plan.md ("Determinism guarantees") and ADR 0005 ("cases carry
// hex(sha256(canonical case))[:12] as id, are de-duplicated by that ID, and
// are sorted by (host, path, scheme, method, family, id)").
//
// Exactly two parts of the output depend on the order of the input resources,
// and both are documented:
//  1. the head and host-variant cases use "that host's first planned path"
//     (docs/plan.md, "Case families"; ADR 0005): internal/plan/generate.go
//     records the first request emitted for a host in builder.firstPaths
//     (addPath -> emit), so the path, and with it the case id, depends on the
//     input order;
//  2. the VAR-PLAN-0006 warning names the Ingress that turned host-wide regex
//     matching on (internal/plan/nginx.go, markRegexHost). The corpora below
//     keep at most one resource per host carrying use-regex or rewrite-target,
//     so this warning never moves.
//
// Everything else - the remaining cases, the findings and the warnings - is
// identical whatever order the resources arrive in.

const (
	// determinismSeed is the fixed seed of the shuffle generator, so a failing
	// shuffle is reproducible on every machine.
	determinismSeed = 20261020
	// determinismShuffles is the number of input orders tried per corpus.
	determinismShuffles = 50
	// determinismRepeats is the number of times the same input is planned in
	// the repeatability test.
	determinismRepeats = 100
	// syntheticResourceCount is the number of resources of the synthetic corpus.
	syntheticResourceCount = 30
	// syntheticSharedHosts is the number of distinct hosts the shared synthetic
	// corpus declares, so the hosts app-00 to app-09 are declared twice.
	syntheticSharedHosts = 20
	// syntheticDuplicateCount is the number of requests the nginx profile
	// duplicates in the synthetic corpus: the 10 http-redirect requests of
	// app-01 (the documented ssl-redirect-without-spec.tls limitation of
	// docs/plan.md) and the 2 redirect-annotation requests of app-04 and the 2
	// of app-05 (a redirect annotation probes the same request as the base
	// case of its route).
	syntheticDuplicateCount = 14
)

// determinismCorpus is one input corpus for the determinism tests.
type determinismCorpus struct {
	// name is the sub-test name of the corpus.
	name string
	// resources are the parsed Ingress resources of the corpus.
	resources []ingress.Ingress
	// sharedHost reports whether two resources of the corpus declare the same
	// concrete host. That is the only situation in which the rendered plan may
	// legitimately differ between two input orders, because the head and
	// host-variant cases use the first planned path of the host.
	sharedHost bool
}

// determinismProfiles are the two planner profiles every determinism test runs
// on: the ingress-nginx profile and the profile that plans only behavior
// shared by HTTP proxies (docs/plan.md, "Profiles"; ADR 0008).
var determinismProfiles = []plan.Options{
	{Profile: plan.ProfileNginx},
	{Profile: plan.ProfileGeneric},
}

// fixtureManifests returns the manifest files under testdata/plan that the
// determinism corpora parse; the golden outputs (*.golden.json) and the
// rejected manifests (*.invalid) are not inputs (docs/plan.md, "Accepted
// manifests"; the CLI reads only the regular *.yaml, *.yml and *.json files).
func fixtureManifests(t *testing.T) []string {
	t.Helper()
	root := filepath.Join("..", "..", "testdata", "plan")
	found := make([]string, 0, 9)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".json") {
			return nil
		}
		if strings.HasSuffix(name, ".golden.json") || strings.HasSuffix(name, ".invalid") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		found = append(found, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(found)
	return found
}

// determinismCorpora returns every corpus the determinism tests run on: one
// corpus per manifest under testdata/plan, the combined fixture corpus, and the
// two synthetic corpora (the fixture manifests and ADR 0005).
func determinismCorpora(t *testing.T) []determinismCorpus {
	t.Helper()
	corpora := make([]determinismCorpus, 0, 12)
	combined := make([]ingress.Ingress, 0, 16)
	for _, manifest := range fixtureManifests(t) {
		resources := parseFile(t, manifest)
		combined = append(combined, resources...)
		// Only multi-regex-host.yaml declares a host twice: its two Ingresses
		// share api.example.com.
		wantShared := manifest == "multi-regex-host.yaml"
		if shared := hasSharedConcreteHost(resources); shared != wantShared {
			t.Fatalf("corpus %s: shared concrete host = %v, want %v", manifest, shared, wantShared)
		}
		corpora = append(corpora, determinismCorpus{name: strings.ReplaceAll(manifest, "/", "-"), resources: resources, sharedHost: wantShared})
	}
	if !hasSharedConcreteHost(combined) {
		t.Fatal("the combined fixture corpus must declare a host twice")
	}
	corpora = append(corpora,
		determinismCorpus{name: "fixtures-combined", resources: combined, sharedHost: true},
		determinismCorpus{name: "synthetic-shared-hosts", resources: syntheticResources(true), sharedHost: true},
		determinismCorpus{name: "synthetic-unique-hosts", resources: syntheticResources(false), sharedHost: false},
	)
	return corpora
}

// stabilityCorpora returns the corpora the repeatability and the id-stability
// tests use: the combined fixture corpus and the two synthetic corpora.
func stabilityCorpora(t *testing.T) []determinismCorpus {
	t.Helper()
	kept := make([]determinismCorpus, 0, 3)
	for _, corpus := range determinismCorpora(t) {
		if corpus.name == "fixtures-combined" || strings.HasPrefix(corpus.name, "synthetic-") {
			kept = append(kept, corpus)
		}
	}
	return kept
}

// concreteHosts returns the hosts the rules declare literally. A wildcard
// label (*.example.com) and an empty host are not concrete hosts
// (internal/plan/generate.go, resolvedRule.concrete), so they get no head and
// no host-variant case.
func concreteHosts(resources []ingress.Ingress) map[string]bool {
	hosts := make(map[string]bool)
	for _, resource := range resources {
		for _, rule := range resource.Rules {
			if rule.Host == "" || strings.HasPrefix(rule.Host, "*.") {
				continue
			}
			hosts[rule.Host] = true
		}
	}
	return hosts
}

// hasSharedConcreteHost reports whether two resources of the corpus declare
// the same concrete host, the only situation in which the plan may legitimately
// differ between two input orders.
func hasSharedConcreteHost(resources []ingress.Ingress) bool {
	seen := make(map[string]bool)
	for _, resource := range resources {
		for host := range concreteHosts([]ingress.Ingress{resource}) {
			if seen[host] {
				return true
			}
			seen[host] = true
		}
	}
	return false
}

// syntheticResources builds the synthetic corpus: 30 Ingress resources in the
// namespace "synthetic", named app-00 to app-29.
//
// With sharedHosts the host of resource i is app-{i%20}.example.com, so the
// hosts app-00 to app-09 are declared by two resources each; without it every
// host is declared once. Every resource declares one rule with two paths, and
// the special resources below cover the behaviors of ADR 0005: use-regex,
// ssl-redirect with and without spec.tls, rewrite-target, enable-cors, the
// redirect annotations, an annotation varco cannot test, app-root, a regex
// path, a path that is not a request target, a rule without a path, a wildcard
// host and a rule without a host.
//
// At most one resource per host carries use-regex or rewrite-target, so the
// VAR-PLAN-0006 warning of internal/plan/nginx.go markRegexHost always names
// the same Ingress and never depends on the input order.
func syntheticResources(sharedHosts bool) []ingress.Ingress {
	resources := make([]ingress.Ingress, 0, syntheticResourceCount)
	for index := 0; index < syntheticResourceCount; index++ {
		host := fmt.Sprintf("app-%02d.example.com", index)
		if sharedHosts {
			host = fmt.Sprintf("app-%02d.example.com", index%syntheticSharedHosts)
		}
		paths := []ingress.Path{
			{Path: fmt.Sprintf("/svc-%02d/a", index), PathType: "Prefix"},
			{Path: fmt.Sprintf("/svc-%02d/b", index), PathType: "Exact"},
		}
		if index == 9 {
			// A regex path is sampled, never guessed (ADR 0005, escalations).
			paths[0] = ingress.Path{Path: "/legacy/(.*)", PathType: "Prefix"}
		}
		rules := make([]ingress.Rule, 0, 2)
		if index == 16 {
			// A rule without an HTTP path is reported as VAR-PLAN-0008
			// (internal/plan/generate.go, addResource).
			rules = append(rules, ingress.Rule{Host: host})
		} else {
			rules = append(rules, ingress.Rule{Host: host, Paths: paths})
		}
		switch index {
		case 15:
			// A path that is not a request target is dropped with a
			// VAR-PLAN-0003 finding (internal/plan/generate.go, addPath).
			rules = append(rules, ingress.Rule{Host: host, Paths: []ingress.Path{{Path: "no-slash", PathType: "Prefix"}}})
		case 28:
			// A wildcard host is planned as varco-probe.wild.example.com and
			// is not a concrete host (docs/plan.md, "Case families").
			rules[0].Host = "*.wild.example.com"
		case 29:
			// A rule without a host is reported as VAR-PLAN-0005
			// (internal/plan/generate.go, resolveHosts).
			rules = append(rules, ingress.Rule{Paths: []ingress.Path{{Path: "/svc-29/hostless", PathType: "Prefix"}}})
		}
		resource := ingress.Ingress{
			APIVersion: ingress.APIVersion, Kind: ingress.KindIngress,
			Namespace: "synthetic", Name: fmt.Sprintf("app-%02d", index), ClassName: "nginx",
			Rules: rules,
		}
		if annotations := syntheticAnnotations(index); len(annotations) > 0 {
			resource.Annotations = annotations
		}
		if index == 0 || index == 8 || (index >= 10 && index <= 14) {
			// spec.tls makes the host served over HTTPS
			// (internal/plan/generate.go, resolveHosts and resourceHasTLS).
			resource.TLS = []ingress.TLS{{Hosts: []string{host}, SecretName: host + "-tls"}}
		}
		resources = append(resources, resource)
	}
	return resources
}

// syntheticAnnotations returns the annotations of one synthetic resource. The
// annotation names are the ingress-nginx ones varco honors (docs/plan.md,
// "Findings" and "Warnings"): use-regex (app-00), ssl-redirect with and
// without spec.tls (app-01, app-08), rewrite-target (app-02), enable-cors
// (app-03), the redirect annotations (app-04, app-05), an annotation varco
// cannot test black-box (app-06) and app-root (app-07).
func syntheticAnnotations(index int) map[string]string {
	annotations := map[int]map[string]string{
		0: {"nginx.ingress.kubernetes.io/use-regex": "true"},
		1: {"nginx.ingress.kubernetes.io/ssl-redirect": "true"},
		2: {"nginx.ingress.kubernetes.io/rewrite-target": "/$2"},
		3: {"nginx.ingress.kubernetes.io/enable-cors": "true", "nginx.ingress.kubernetes.io/cors-allow-headers": "X-Request-Id"},
		4: {"nginx.ingress.kubernetes.io/permanent-redirect": "https://www.example.org$request_uri"},
		5: {"nginx.ingress.kubernetes.io/temporal-redirect": "https://www.example.org$request_uri"},
		6: {"nginx.ingress.kubernetes.io/proxy-body-size": "8m"},
		7: {"nginx.ingress.kubernetes.io/app-root": "/home"},
		8: {"nginx.ingress.kubernetes.io/ssl-redirect": "false"},
	}
	return annotations[index]
}

// shuffledResources returns a shuffled copy of the resources. The input slice
// is never mutated, so every shuffle starts from the same corpus.
func shuffledResources(resources []ingress.Ingress, rng *rand.Rand) []ingress.Ingress {
	shuffled := append([]ingress.Ingress(nil), resources...)
	rng.Shuffle(len(shuffled), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})
	return shuffled
}

// reversedResources returns the resources in the opposite order, a deterministic
// second input order for the corpora that declare a host twice.
func reversedResources(resources []ingress.Ingress) []ingress.Ingress {
	reversed := make([]ingress.Ingress, 0, len(resources))
	for index := len(resources) - 1; index >= 0; index-- {
		reversed = append(reversed, resources[index])
	}
	return reversed
}

// orderIndependentCases returns the cases that do not depend on the order of
// the input resources: every case whose family is neither head nor
// host-variant (internal/plan/generate.go, addHostVariantAndHeadCases).
func orderIndependentCases(cases []plan.Case) []plan.Case {
	kept := make([]plan.Case, 0, len(cases))
	for _, item := range cases {
		if item.Family == plan.FamilyHead || item.Family == plan.FamilyHostVariant {
			continue
		}
		kept = append(kept, item)
	}
	return kept
}

// hostScopedCases returns the two plan-wide families that depend on the order
// of the input resources: the head case and the three host-variant cases of
// every concrete host (ADR 0005, "Host-variant and head families").
func hostScopedCases(result plan.Plan) []plan.Case {
	kept := make([]plan.Case, 0, 8)
	for _, item := range result.Cases {
		if item.Family != plan.FamilyHead && item.Family != plan.FamilyHostVariant {
			continue
		}
		kept = append(kept, item)
	}
	return kept
}

// projectedJSON renders the plan exactly like the CLI, with the two
// order-sensitive families removed from the case list: the part of the plan
// that must not move when the input resources are reordered.
//
// The rendering is the one of renderPlanJSON in golden_test.go, which mirrors
// the CLI writer writePlanJSON in internal/cli/plan.go (same document struct,
// same json.Encoder with SetIndent("", "  ")); internal/cli/plan_test.go
// TestPlanJSONMatchesGolden proves the CLI emits exactly those bytes.
func projectedJSON(t *testing.T, result plan.Plan) []byte {
	t.Helper()
	projected := result
	projected.Cases = orderIndependentCases(result.Cases)
	return renderPlan(t, projected)
}

// renderPlan renders a plan with the shared CLI-shaped writer and fails the
// test when the encoding fails.
func renderPlan(t *testing.T, result plan.Plan) []byte {
	t.Helper()
	rendered, err := renderPlanJSON(result)
	if err != nil {
		t.Fatalf("render plan JSON: %v", err)
	}
	return rendered
}

// firstPlannedPaths returns, for every host of the plan, the path the head and
// host-variant cases must use: the base (or rewrite, or app-root) case of the
// first resource in input order that declares the host.
//
// The rule comes from docs/plan.md ("that host's first planned path") and ADR
// 0005. internal/plan/generate.go sets builder.firstPaths[host] from the first
// request emitted for that host, and emit is called for the paths of a
// resource in rule and path order, after which the app-root case of the same
// Ingress is emitted: the candidates of a resource are therefore ordered by
// (rule index, path index), and the app-root case sorts last in its resource.
//
// The candidates are compared with a NUL separated key: the position of the
// resource in the input order, then the rule and path index inside it, then
// the request target, so the base request of a rewrite wins over the subpath
// request of the same route. NUL cannot occur in a request target
// (internal/plan/generate.go, validateCase).
func firstPlannedPaths(t *testing.T, resources []ingress.Ingress, result plan.Plan) map[string]string {
	t.Helper()
	positions := make(map[string]int, len(resources))
	for index, resource := range resources {
		positions[resource.Key()] = index
	}
	bestKey := make(map[string]string)
	bestPath := make(map[string]string)
	for _, item := range result.Cases {
		if item.Family != plan.FamilyBase && item.Family != plan.FamilyRewrite && item.Family != plan.FamilyAppRoot {
			continue
		}
		position, ok := positions[item.Source.Ingress]
		if !ok {
			t.Fatalf("case %#v names the source %q, which is not one of the input resources", item, item.Source.Ingress)
		}
		rule, path := item.Source.Rule, item.Source.Path
		if item.Family == plan.FamilyAppRoot {
			rule, path = math.MaxInt, math.MaxInt
		}
		key := fmt.Sprintf("%020d\x00%020d\x00%020d\x00%s\x00%s", position, rule, path, item.Path, item.Family)
		if current, seen := bestKey[item.Host]; !seen || key < current {
			bestKey[item.Host] = key
			bestPath[item.Host] = item.Path
		}
	}
	return bestPath
}

// canonicalCase is the canonical form of a case, mirrored from
// internal/plan/plan.go:148-173. The field order and the json tags are the
// documented ones, because the id is the hash of the JSON of this struct.
type canonicalCase struct {
	Host    string        `json:"host"`
	SNI     string        `json:"sni"`
	Scheme  string        `json:"scheme"`
	Method  string        `json:"method"`
	Path    string        `json:"path"`
	Headers []plan.Header `json:"headers"`
	Family  string        `json:"family"`
	Source  plan.Source   `json:"source"`
	Note    string        `json:"note"`
}

// canonicalCaseID recomputes the documented case id:
// hex(sha256(canonical case))[:12] over the host, SNI, scheme, method, path,
// the sorted headers, the family, the source and the note.
//
// Sources: docs/plan.md ("id is the first 12 hex characters of the SHA-256 of
// the canonical case (host, SNI, scheme, method, path, sorted headers, family,
// source, note), so an ID is stable across runs and independent of generation
// order") and internal/plan/plan.go:145-173.
func canonicalCaseID(item plan.Case) string {
	canonical := canonicalCase{
		Host: item.Host, SNI: item.SNI, Scheme: item.Scheme, Method: item.Method,
		Path: item.Path, Headers: sortedCaseHeaders(item.Headers),
		Family: item.Family, Source: item.Source, Note: item.Note,
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])[:12]
}

// sortedCaseHeaders returns the headers of a case ordered by name, so the
// canonical form of a case does not depend on the order the headers were
// added in (internal/plan/plan.go, sortedHeaders).
func sortedCaseHeaders(headers []plan.Header) []plan.Header {
	if len(headers) == 0 {
		return nil
	}
	ordered := append([]plan.Header(nil), headers...)
	for index := range ordered {
		ordered[index].Values = append([]string(nil), ordered[index].Values...)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Name < ordered[j].Name })
	return ordered
}

// requestKey is the request a case sends: the host, the scheme, the method and
// the path, followed by the headers ordered by name. Two cases that send the
// same request share a key, whatever family they belong to.
func requestKey(item plan.Case) string {
	key := strings.Join([]string{item.Host, item.Scheme, item.Method, item.Path}, "|") + "|"
	headers := sortedCaseHeaders(item.Headers)
	for index, header := range headers {
		if index > 0 {
			key += "|"
		}
		key += header.Name + "=" + strings.Join(header.Values, ",")
	}
	return key
}

// documentedDuplicate reports whether a group of cases that send the same
// request is one of the documented shapes:
//
//   - an http-redirect case and a plain-HTTP route case: the documented
//     limitation of docs/plan.md ("Known limitations"): "ssl-redirect on a host
//     without spec.tls plans an http-redirect case and cases that are still
//     served with scheme: http, so some cases differ only by family and note";
//   - a redirect-annotation case and the base or rewrite case of the same
//     route: both probe "GET path" (docs/plan.md, "Case families");
//   - an app-root case and the base case of a "/" route: both probe "GET /"
//     (docs/plan.md, "Case families").
//
// A group that repeats one family is never documented: the same request
// planned twice in one family would be a bug.
func documentedDuplicate(group []plan.Case) bool {
	seen := make(map[string]bool, len(group))
	extra := false
	for _, item := range group {
		if seen[item.Family] {
			return false
		}
		seen[item.Family] = true
		switch item.Family {
		case plan.FamilyHTTPRedirect:
			// The documented ssl-redirect-without-spec.tls case: a plain-HTTP
			// route request is probed twice, once per family.
			if item.Scheme != plan.SchemeHTTP {
				return false
			}
			extra = true
		case plan.FamilyRedirectAnnotation, plan.FamilyAppRoot:
			// The redirect annotation and the app-root probe send the request
			// of their route (docs/plan.md, "Case families").
			extra = true
		case plan.FamilyBase, plan.FamilyRewrite, plan.FamilyTrailingSlash, plan.FamilySubpath,
			plan.FamilyPrefixBoundary, plan.FamilyCaseFlip, plan.FamilyDotSegments,
			plan.FamilyDoubleSlash, plan.FamilyCORSPreflight:
		default:
			return false
		}
	}
	return extra
}

// firstDifference returns the index of the first byte where two renderings
// differ, or -1 when the renderings are identical.
func firstDifference(a, b []byte) int {
	limit := len(a)
	if len(b) < limit {
		limit = len(b)
	}
	for index := 0; index < limit; index++ {
		if a[index] != b[index] {
			return index
		}
	}
	if len(a) != len(b) {
		return limit
	}
	return -1
}

// assertHostScopedCases checks the documented head and host-variant rule for
// one input order: every concrete host that has a head case has exactly one
// head case and exactly three host-variant cases, and all of them use the
// first planned path of that host (docs/plan.md, "Case families"; ADR 0005).
func assertHostScopedCases(t *testing.T, resources []ingress.Ingress, result plan.Plan) {
	t.Helper()
	first := firstPlannedPaths(t, resources, result)
	byHost := make(map[string][]plan.Case)
	for _, item := range hostScopedCases(result) {
		byHost[item.Host] = append(byHost[item.Host], item)
	}
	for host, cases := range byHost {
		heads, variants := 0, 0
		for _, item := range cases {
			switch item.Family {
			case plan.FamilyHead:
				heads++
			case plan.FamilyHostVariant:
				variants++
			}
			want, ok := first[host]
			if !ok {
				t.Fatalf("host %q has a head case but no first planned path", host)
			}
			if item.Path != want {
				t.Fatalf("host %q: the %s case uses the path %q, want the first planned path %q", host, item.Family, item.Path, want)
			}
		}
		if heads != 1 {
			t.Fatalf("host %q has %d head cases, want exactly one", host, heads)
		}
		if variants != 3 {
			t.Fatalf("host %q has %d host-variant cases, want exactly three", host, variants)
		}
	}
}

// assertOnlyHostScopedPathsChanged checks that every case outside the head and
// host-variant families keeps its id and its path between two input orders.
//
// The head and host-variant cases are the only ones that may move, and they
// move as a group: their id is the hash of the case, so a moved case gets a
// new id (internal/plan/plan.go, caseID). Their number stays the same.
func assertOnlyHostScopedPathsChanged(t *testing.T, first plan.Plan, second plan.Plan) {
	t.Helper()
	pathsOf := func(result plan.Plan) map[string]string {
		paths := make(map[string]string)
		for _, item := range orderIndependentCases(result.Cases) {
			paths[item.ID] = item.Path
		}
		return paths
	}
	stable, moved := pathsOf(first), pathsOf(second)
	if len(stable) != len(moved) {
		t.Fatalf("the order-independent cases changed count: %d vs %d", len(stable), len(moved))
	}
	for id, path := range stable {
		other, ok := moved[id]
		if !ok {
			t.Fatalf("case %s disappeared when the input order changed", id)
		}
		if other != path {
			t.Fatalf("case %s changed its path from %q to %q when the input order changed", id, path, other)
		}
	}
	if len(hostScopedCases(first)) != len(hostScopedCases(second)) {
		t.Fatalf("the number of host-scoped cases changed: %d vs %d", len(hostScopedCases(first)), len(hostScopedCases(second)))
	}
}

// assertSomeHostScopedPathMoved checks that two input orders really do produce
// different head paths, so the documented order sensitivity is observed and
// not merely allowed.
func assertSomeHostScopedPathMoved(t *testing.T, first plan.Plan, second plan.Plan) {
	t.Helper()
	headPaths := func(result plan.Plan) map[string]string {
		paths := make(map[string]string)
		for _, item := range result.Cases {
			if item.Family == plan.FamilyHead {
				paths[item.Host] = item.Path
			}
		}
		return paths
	}
	firstPaths, secondPaths := headPaths(first), headPaths(second)
	if len(firstPaths) != len(secondPaths) {
		t.Fatalf("the number of hosts with a head case changed: %d vs %d", len(firstPaths), len(secondPaths))
	}
	for host, path := range firstPaths {
		other, ok := secondPaths[host]
		if !ok {
			t.Fatalf("host %q lost its head case when the input order changed", host)
		}
		if other != path {
			return
		}
	}
	t.Fatal("no head path moved when the input order changed, so the order sensitivity was not observed")
}

// TestPlanJSONIsIndependentOfInputOrder proves that the rendered plan does not
// depend on the order of the input resources.
//
// Sources: CONVENTIONS.md ("Determinism: Same input, same output. No map
// iteration in observable output; cases are sorted by a stable key."),
// docs/plan.md ("Determinism guarantees") and ADR 0005 ("Determinism is part of
// the contract"). The rendered bytes are the ones the CLI writes:
// renderPlanJSON in golden_test.go mirrors writePlanJSON in internal/cli/plan.go
// and internal/cli/plan_test.go TestPlanJSONMatchesGolden proves the CLI emits
// exactly those bytes.
//
// A corpus that declares a host twice is the only case in which the full
// rendering may legitimately differ: the head and host-variant cases use "that
// host's first planned path" (docs/plan.md, "Case families";
// internal/plan/generate.go, addHostVariantAndHeadCases), the first request
// emitted for the host. The order-independent projection - every other case,
// the findings and the warnings - must be byte-identical in every order.
func TestPlanJSONIsIndependentOfInputOrder(t *testing.T) {
	for _, corpus := range determinismCorpora(t) {
		for _, options := range determinismProfiles {
			t.Run(corpus.name+"/"+options.Profile, func(t *testing.T) {
				baseline := plan.Build(corpus.resources, options)
				baselineFull := renderPlan(t, baseline)
				baselineProjected := projectedJSON(t, baseline)
				rng := rand.New(rand.NewSource(determinismSeed))
				for shuffle := 1; shuffle <= determinismShuffles; shuffle++ {
					shuffled := shuffledResources(corpus.resources, rng)
					result := plan.Build(shuffled, options)
					projected := projectedJSON(t, result)
					if !bytes.Equal(projected, baselineProjected) {
						t.Fatalf("corpus %s profile %s shuffle %d: the order-independent projection changed, first difference at byte %d", corpus.name, options.Profile, shuffle, firstDifference(baselineProjected, projected))
					}
					if corpus.sharedHost {
						continue
					}
					full := renderPlan(t, result)
					if !bytes.Equal(full, baselineFull) {
						t.Fatalf("corpus %s profile %s shuffle %d: the rendered plan changed, first difference at byte %d", corpus.name, options.Profile, shuffle, firstDifference(baselineFull, full))
					}
				}
			})
		}
	}
}

// TestOrderSensitivityIsLimitedToTheHostScopedFamilies proves that the only
// part of the plan that depends on the order of the input resources is the
// host-scoped pair of families.
//
// Exactly two parts of the output are order-sensitive, and both are documented:
//  1. the head and host-variant cases use "that host's first planned path"
//     (docs/plan.md, "Case families"; ADR 0005, "Host-variant and head
//     families"): internal/plan/generate.go records the first request emitted
//     for a host in builder.firstPaths (addPath -> emit) and
//     addHostVariantAndHeadCases reads it, so the path, and with it the case
//     id, depends on which resource is read first;
//  2. the VAR-PLAN-0006 warning names the Ingress that turned host-wide regex
//     matching on: internal/plan/nginx.go markRegexHost records the first
//     resource it sees. The corpora used here keep at most one resource per
//     host carrying use-regex or rewrite-target, so this warning never moves.
//
// Everything else - the remaining cases, the findings and the warnings - is
// byte-identical in every input order.
func TestOrderSensitivityIsLimitedToTheHostScopedFamilies(t *testing.T) {
	for _, corpus := range determinismCorpora(t) {
		if !corpus.sharedHost {
			continue
		}
		for _, options := range determinismProfiles {
			t.Run(corpus.name+"/"+options.Profile, func(t *testing.T) {
				forward := corpus.resources
				reversed := reversedResources(corpus.resources)
				planForward := plan.Build(forward, options)
				planReversed := plan.Build(reversed, options)
				projectedForward := projectedJSON(t, planForward)
				projectedReversed := projectedJSON(t, planReversed)
				if !bytes.Equal(projectedForward, projectedReversed) {
					t.Fatalf("corpus %s profile %s: the order-independent projection changed between two input orders, first difference at byte %d", corpus.name, options.Profile, firstDifference(projectedForward, projectedReversed))
				}
				assertHostScopedCases(t, forward, planForward)
				assertHostScopedCases(t, reversed, planReversed)
				assertOnlyHostScopedPathsChanged(t, planForward, planReversed)
				assertSomeHostScopedPathMoved(t, planForward, planReversed)
				rng := rand.New(rand.NewSource(determinismSeed))
				for shuffle := 1; shuffle <= determinismShuffles; shuffle++ {
					shuffled := shuffledResources(corpus.resources, rng)
					result := plan.Build(shuffled, options)
					projected := projectedJSON(t, result)
					if !bytes.Equal(projected, projectedForward) {
						t.Fatalf("corpus %s profile %s shuffle %d: the order-independent projection changed, first difference at byte %d", corpus.name, options.Profile, shuffle, firstDifference(projectedForward, projected))
					}
					assertHostScopedCases(t, shuffled, result)
					assertOnlyHostScopedPathsChanged(t, planForward, result)
				}
			})
		}
	}
}

// TestPlanIsRepeatable proves that planning the same input twice produces the
// very same bytes.
//
// Sources: CONVENTIONS.md ("Same input, same output") and docs/plan.md
// ("Determinism guarantees"). The planner keeps no package-level state and
// reads no clock (internal/plan/plan.go, package comment).
func TestPlanIsRepeatable(t *testing.T) {
	for _, corpus := range stabilityCorpora(t) {
		for _, options := range determinismProfiles {
			t.Run(corpus.name+"/"+options.Profile, func(t *testing.T) {
				first := renderPlan(t, plan.Build(corpus.resources, options))
				for iteration := 2; iteration <= determinismRepeats; iteration++ {
					again := renderPlan(t, plan.Build(corpus.resources, options))
					if !bytes.Equal(first, again) {
						t.Fatalf("corpus %s profile %s: iteration %d differs from the first, first difference at byte %d", corpus.name, options.Profile, iteration, firstDifference(first, again))
					}
				}
			})
		}
	}
}

// TestCaseIDsAreStableWhenUnrelatedResourcesAreAdded proves that a case id
// depends on the case itself and on nothing else, so adding an unrelated
// resource never renumbers the existing cases.
//
// Sources: docs/plan.md ("id is the first 12 hex characters of the SHA-256 of
// the canonical case (host, SNI, scheme, method, path, sorted headers, family,
// source, note), so an ID is stable across runs and independent of generation
// order") and internal/plan/plan.go:145-173.
func TestCaseIDsAreStableWhenUnrelatedResourcesAreAdded(t *testing.T) {
	for _, corpus := range stabilityCorpora(t) {
		for _, options := range determinismProfiles {
			t.Run(corpus.name+"/"+options.Profile, func(t *testing.T) {
				before := plan.Build(corpus.resources, options)
				extended := append(append([]ingress.Ingress(nil), corpus.resources...), ingress.Ingress{
					APIVersion: ingress.APIVersion, Kind: ingress.KindIngress,
					Namespace: "unrelated", Name: "extra", ClassName: "nginx",
					Rules: []ingress.Rule{{Host: "unrelated.example.com", Paths: []ingress.Path{{Path: "/extra", PathType: "Prefix"}}}},
				})
				after := plan.Build(extended, options)
				byID := make(map[string]plan.Case, len(after.Cases))
				for _, item := range after.Cases {
					byID[item.ID] = item
				}
				for _, item := range before.Cases {
					other, ok := byID[item.ID]
					if !ok {
						t.Fatalf("case %s (%s %s %s) disappeared after an unrelated resource was added", item.ID, item.Host, item.Path, item.Family)
					}
					// The whole case must be the same: host, SNI, scheme,
					// method, path, headers, family, source and note.
					if !reflect.DeepEqual(item, other) {
						t.Fatalf("case %s changed when an unrelated resource was added:\nfirst:  %#v\nsecond: %#v", item.ID, item, other)
					}
				}
				if len(after.Cases) <= len(before.Cases) {
					t.Fatalf("the plan did not grow: %d cases before, %d after", len(before.Cases), len(after.Cases))
				}
				for _, item := range after.Cases {
					if len(item.ID) != 12 || item.ID != strings.ToLower(item.ID) {
						t.Fatalf("case id %q is not 12 lowercase hex characters", item.ID)
					}
					if _, err := hex.DecodeString(item.ID); err != nil {
						t.Fatalf("case id %q is not hexadecimal", item.ID)
					}
					if want := canonicalCaseID(item); item.ID != want {
						t.Fatalf("case id %q does not match the documented id %q of the case %#v", item.ID, want, item)
					}
				}
			})
		}
	}
}

// TestPlanHasNoDuplicateCases proves that the plan never sends the same
// request twice, except for the requests the documentation sends twice on
// purpose.
//
// Sources: docs/plan.md ("Cases are ordered by (host, path, scheme, method,
// family, id) and identical cases are de-duplicated") and its "Known
// limitations": "ssl-redirect on a host without spec.tls plans an http-redirect
// case and cases that are still served with scheme: http, so some cases differ
// only by family and note".
func TestPlanHasNoDuplicateCases(t *testing.T) {
	for _, corpus := range determinismCorpora(t) {
		for _, options := range determinismProfiles {
			t.Run(corpus.name+"/"+options.Profile, func(t *testing.T) {
				result := plan.Build(corpus.resources, options)
				ids := make(map[string]bool, len(result.Cases))
				for _, item := range result.Cases {
					if ids[item.ID] {
						t.Fatalf("case id %q appears twice", item.ID)
					}
					ids[item.ID] = true
				}
				groups := make(map[string][]plan.Case)
				for _, item := range result.Cases {
					key := requestKey(item)
					groups[key] = append(groups[key], item)
				}
				for key, group := range groups {
					if len(group) < 2 {
						continue
					}
					if !documentedDuplicate(group) {
						t.Fatalf("cases send the same request %q: %#v", key, group)
					}
				}
				assertDuplicateShape(t, corpus, options, groups)
			})
		}
	}
}

// assertDuplicateShape checks the duplicated requests of the synthetic corpus.
//
// The generic profile ignores the nginx annotations (docs/plan.md,
// "Profiles"), so it never plans an http-redirect case for a host without
// spec.tls and never plans a redirect-annotation case: every request key of
// the synthetic corpus is unique there.
//
// The nginx profile does plan them: the synthetic corpus carries
// ssl-redirect without spec.tls (app-01) and the redirect annotations
// (app-04, app-05), so the only duplicated requests are the documented ones of
// those three hosts: the 10 http-redirect requests of app-01, one per
// plain-HTTP route request of its two paths, and the 2 redirect-annotation
// requests of app-04 and the 2 of app-05.
func assertDuplicateShape(t *testing.T, corpus determinismCorpus, options plan.Options, groups map[string][]plan.Case) {
	t.Helper()
	if !strings.HasPrefix(corpus.name, "synthetic-") {
		return
	}
	duplicated := make([]string, 0, 8)
	for key, group := range groups {
		if len(group) > 1 {
			duplicated = append(duplicated, key)
		}
	}
	sort.Strings(duplicated)
	if options.Profile == plan.ProfileGeneric {
		if len(duplicated) != 0 {
			t.Fatalf("the generic profile planned %d duplicated requests: %#v", len(duplicated), duplicated)
		}
		return
	}
	allowed := map[string]bool{"app-01.example.com": true, "app-04.example.com": true, "app-05.example.com": true}
	for _, key := range duplicated {
		host := strings.SplitN(key, "|", 2)[0]
		if !allowed[host] {
			t.Fatalf("the request %q is duplicated on an unexpected host", key)
		}
	}
	if len(duplicated) != syntheticDuplicateCount {
		t.Fatalf("the synthetic corpus planned %d duplicated requests, want exactly the %d documented ones: %#v", len(duplicated), syntheticDuplicateCount, duplicated)
	}
}

// TestPlanSizeIsBoundedByThePathCount proves that the plan cannot grow without
// bound, so a hostile manifest cannot explode it.
//
// The bound follows the case families of ADR 0005: at most 11 cases per path
// (base or rewrite, trailing-slash, subpath, prefix-boundary, case-flip,
// dot-segments, double-slash, cors-preflight, permanent-redirect,
// temporal-redirect and http-redirect), at most 5 per host (head, three
// host-variant and unmatched-route), at most one per resource (app-root) and at
// most two per plan (default-backend).
func TestPlanSizeIsBoundedByThePathCount(t *testing.T) {
	for _, corpus := range determinismCorpora(t) {
		for _, options := range determinismProfiles {
			t.Run(corpus.name+"/"+options.Profile, func(t *testing.T) {
				result := plan.Build(corpus.resources, options)
				paths := 0
				for _, resource := range corpus.resources {
					for _, rule := range resource.Rules {
						paths += len(rule.Paths)
					}
				}
				hosts := make(map[string]bool)
				for _, item := range result.Cases {
					hosts[item.Host] = true
				}
				bound := 11*paths + 5*len(hosts) + len(corpus.resources) + 2
				if len(result.Cases) > bound {
					t.Fatalf("corpus %s profile %s planned %d cases, above the bound %d (%d paths, %d hosts, %d resources)", corpus.name, options.Profile, len(result.Cases), bound, paths, len(hosts), len(corpus.resources))
				}
			})
		}
	}
	t.Run("growth", func(t *testing.T) {
		counts := make(map[int]int)
		for _, size := range []int{1, 2, 4, 8, 16, 32, 64, 128, 256} {
			counts[size] = len(plan.Build(growthResource(size), plan.Options{}).Cases)
			if bound := 11*size + 8; counts[size] > bound {
				t.Fatalf("%d paths planned %d cases, above the bound %d", size, counts[size], bound)
			}
		}
		for _, size := range []int{1, 2, 4, 8, 16, 32, 64, 128} {
			grown := 2 * size
			if counts[grown] > 2*counts[size]+8 {
				t.Fatalf("the case count grew faster than linearly: %d paths planned %d cases, %d paths planned %d cases", size, counts[size], grown, counts[grown])
			}
		}
	})
	t.Run("hostile-manifest", func(t *testing.T) {
		planned := len(plan.Build(growthResource(1000), plan.Options{}).Cases)
		if bound := 11*1000 + 8; planned > bound {
			t.Fatalf("a manifest with 1000 paths planned %d cases, above the bound %d", planned, bound)
		}
	})
}

// growthResource returns one resource on one host with the given number of
// paths, each of them a Prefix path.
func growthResource(size int) []ingress.Ingress {
	paths := make([]ingress.Path, 0, size)
	for index := 0; index < size; index++ {
		paths = append(paths, ingress.Path{Path: fmt.Sprintf("/p%d-%d", size, index), PathType: "Prefix"})
	}
	return []ingress.Ingress{{
		APIVersion: ingress.APIVersion, Kind: ingress.KindIngress,
		Namespace: "growth", Name: "sized", ClassName: "nginx",
		Rules: []ingress.Rule{{Host: "growth.example.com", Paths: paths}},
	}}
}
