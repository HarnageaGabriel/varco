# Add a plan case family

Goal: add a new request shape to the planner so a run can attribute a
difference to it, and keep the plan, the goldens, the schema and the docs in
sync.

A case family is a request shape that isolates exactly one matching rule, so a
difference is attributed by the `family` field instead of by guesswork. The
rules live in
[ADR 0005: plan case families](../adr/0005-plan-case-families.md), and the
profile rules in
[ADR 0008: planner profiles](../adr/0008-planner-profiles.md). Read both before
you start.

This guide serves the open issue
[#12](https://github.com/HarnageaGabriel/varco/issues/12)
(`plan: derive samples for more regex path forms`) and
[#19](https://github.com/HarnageaGabriel/varco/issues/19)
(`fixtures: real-world Ingress manifests (anonymized)`). The other guides are
in the [dev guide index](README.md).

## How the planner works today

`plan.Build(resources, options)` in
[internal/plan/plan.go](../../internal/plan/plan.go) is the only exported
constructor of the package:

```go
// Build derives cases and findings from parsed Ingress resources. Resources are
// processed in the given order, which makes the output deterministic.
func Build(resources []ingress.Ingress, options Options) Plan {
	builder := newBuilder(options)
	builder.run(resources)
	return builder.plan.finalize()
}
```

A case carries the family as a plain string
([internal/plan/plan.go](../../internal/plan/plan.go)):

```go
type Case struct {
	ID      string   `json:"id"`
	Host    string   `json:"host"`
	SNI     string   `json:"sni,omitempty"`
	Scheme  string   `json:"scheme"`
	Method  string   `json:"method"`
	Path    string   `json:"path"`
	Headers []Header `json:"headers,omitempty"`
	Family  string   `json:"family"`
	Source  Source   `json:"source"`
	Note    string   `json:"note,omitempty"`
}
```

The family constants live in one `const` block in
[internal/plan/generate.go](../../internal/plan/generate.go). There are 16 today:

```go
// Case families. Each family isolates one ingress-nginx behavior so a
// difference can be attributed to it. Numbered behaviors refer to
// https://kubernetes.io/blog/2026/02/27/ingress-nginx-before-you-migrate and
// docs/adr/0005-plan-case-families.md.
const (
	// FamilyBase is the request target exactly as declared in the manifest.
	FamilyBase = "base"
	// FamilyTrailingSlash toggles the trailing slash (blog behavior 4).
	FamilyTrailingSlash = "trailing-slash"
	...
	// FamilyHostVariant sends the same path with a modified Host header, so a
	// difference in host matching between the two controllers becomes visible.
	FamilyHostVariant = "host-variant"
	// FamilyHead sends a HEAD request on the smallest planned path of the
	// host, so a difference in HEAD handling between the two controllers
	// becomes visible.
	FamilyHead = "head"
)
```

There is no switch on the family value. A family is chosen where the case is
created and travels with the case:

- `(*builder).addPath` in [internal/plan/generate.go](../../internal/plan/generate.go)
  picks the per-path families: `base`, `trailing-slash`, `subpath`,
  `prefix-boundary`, `dot-segments`, `double-slash` and `rewrite`.
- `(*builder).emit` in the same file picks `http-redirect`.
- `(*builder).addSyntheticCases` picks `default-backend` and `unmatched-route`.
- `(*builder).addHostVariantAndHeadCases` picks `head` and `host-variant`.
- `nginxProfile.preparePath` in [internal/plan/nginx.go](../../internal/plan/nginx.go)
  picks the ingress-nginx-only families: `case-flip`, `cors-preflight` and
  `redirect-annotation`. `nginxProfile.addAppRoot` picks `app-root`.

Every case is validated by `validateCase` inside `(*builder).appendCase`; an
invalid case is dropped and reported as a `VAR-PLAN-0003` finding instead of
being sent.

## Which profile emits a family

A profile is the planner behavior, not the family.
`plan.Options.Profile` is `nginx` (the default) or `generic`
([internal/plan/plan.go](../../internal/plan/plan.go)). `newBuilder` in
[internal/plan/generate.go](../../internal/plan/generate.go) picks the
implementation of the `profileBehavior` interface: `nginxProfile` for
`nginx`, `genericProfile` for `generic`.

The rule is simple:

- A **portable** family (meaningful for any HTTP proxy) goes into the generic
  core in [internal/plan/generate.go](../../internal/plan/generate.go), so both
  profiles emit it.
- A **controller-specific** family goes into
  [internal/plan/nginx.go](../../internal/plan/nginx.go), so only the `nginx`
  profile emits it.

The generic profile must never read an `nginx.ingress.kubernetes.io/*`
annotation. It emits one `VAR-PLAN-0009` warning per Ingress that has
annotations, and it derives `http-redirect` only from `spec.tls`.

The lists that encode this are in
[internal/plan/profile_test.go](../../internal/plan/profile_test.go). A new
family must be added to one of them, or `TestGenericProfileExcludesNginxFamiliesAndFindings`
fails:

```go
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
	FamilyHead:           true,
	FamilyHostVariant:    true,
}
```

## Files to touch

| File | What changes |
|---|---|
| [internal/plan/generate.go](../../internal/plan/generate.go) | the new `Family<Name>` constant, and the emit site (portable family) |
| [internal/plan/nginx.go](../../internal/plan/nginx.go) | the emit site, when the family is ingress-nginx specific |
| [internal/plan/profile_test.go](../../internal/plan/profile_test.go) | the `allowed` or `forbidden` map |
| [internal/plan/plan_test.go](../../internal/plan/plan_test.go) | the table-driven test for the new family |
| [internal/plan/hostvariant_test.go](../../internal/plan/hostvariant_test.go) | only for a plan-wide family, next to the `head` and `host-variant` tests |
| [internal/plan/plan.go](../../internal/plan/plan.go) | only if a new case field is needed; `Case.Family` already exists |
| [docs/plan.md](../plan.md) | one row in the `## Case families` table |
| [README.md](../../README.md) | one row in the `## How it works` family table |
| [docs/schema/plan.schema.json](../schema/plan.schema.json) | the `family` enum |
| [docs/adr/0005-plan-case-families.md](../adr/0005-plan-case-families.md) | the closed family list and the rule that explains the new family |

## Worked example: the `head` family

The `head` and `host-variant` families are the most recent addition (PR #34)
and are the smallest complete example of the whole change. This is what
`head` looks like end to end.

**1. The constant** in [internal/plan/generate.go](../../internal/plan/generate.go):

```go
// FamilyHead sends a HEAD request on the smallest planned path of the
// host, so a difference in HEAD handling between the two controllers
// becomes visible.
FamilyHead = "head"
```

**2. The emit site.** `head` is a plan-wide family: it is planned once per
concrete declared host, on the smallest planned path of that host: the
lexicographically smallest planned request path of the host, so the plan does
not depend on the order of the input. It is emitted by
`(*builder).addHostVariantAndHeadCases` in
[internal/plan/generate.go](../../internal/plan/generate.go), and the case
carries the synthetic source:

```go
const syntheticIngress = "varco"
```

**3. The plan table row** in [docs/plan.md](../plan.md):

```
| `head` | `HEAD` on the host's smallest planned path | HEAD handling (RFC 9110 section 9.3.2) must be equivalent between old and new; the case probes and asserts nothing |
```

**4. The README row** in [README.md](../../README.md):

```
| `head`, `host-variant` | HEAD handling (RFC 9110 section 9.3.2) and host matching (RFC 3986 section 3.2.2, RFC 9110 section 7.2): one `HEAD` request and three `Host` header variants (upper-cased, trailing dot, explicit default port with SNI kept plain) per concrete host on the smallest planned path of that host (the lexicographically smallest planned request path, so the plan does not depend on the order of the input) (synthetic plan-wide cases) |
```

**5. The schema enum** in
[docs/schema/plan.schema.json](../schema/plan.schema.json). The `family`
property is a closed enum, so a new family that is missing here fails every
plan golden in `internal/schematest`:

```json
"family": {
  "description": "The behavior the case isolates (ADR 0005).",
  "type": "string",
  "enum": [
    "base",
    "trailing-slash",
    ...
    "host-variant",
    "head"
  ]
}
```

**6. The profile test** in [internal/plan/profile_test.go](../../internal/plan/profile_test.go):
`head` is portable, so it is in the `allowed` map.

**7. The test** in
[internal/plan/hostvariant_test.go](../../internal/plan/hostvariant_test.go),
table-driven, like the rest of the package:

```go
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
```

A per-route family is tested in
[internal/plan/plan_test.go](../../internal/plan/plan_test.go) instead. The
helpers there are `pathIngress`, `families`, `pathsByFamily` and
`isSyntheticFamily`. A new plan-wide family must be added to
`isSyntheticFamily`, because the per-route tests assert on the declared-route
families only.

**8. The goldens.** Every golden under `testdata/plan/` now contains the new
family, for example
[testdata/plan/multi-regex-host.golden.json](../../testdata/plan/multi-regex-host.golden.json)
and its generic twin
[testdata/plan/multi-regex-host.generic.golden.json](../../testdata/plan/multi-regex-host.generic.golden.json).

## The `--family` flag

`--family` is a `varco run` flag, not a `varco plan` flag
([internal/cli/run.go](../../internal/cli/run.go)):

```go
var families, excludePaths stringsFlag
flags.Var(&families, "family", "run only cases of this family (repeatable)")
flags.Var(&excludePaths, "exclude-path", "exclude cases whose request path matches this glob (repeatable)")
```

It is repeatable and it filters the planned cases through `engine.FilterCases`
in [internal/engine/run.go](../../internal/engine/run.go):

```go
// FilterCases drops the cases a run must not plan: those whose family is not in
// the requested set, and those whose path matches an excluded glob.
func FilterCases(cases []model.ProbeCase, families, excludePaths []string) ([]model.ProbeCase, int) {
```

The value is matched against `Case.Family` as an exact string. **It is not
validated against the known family list**: a name that matches nothing removes
every case and the run reports the count in `excluded`. `--profile`, in
contrast, *is* validated and an invalid value is `VAR-CFG-0001` with exit
code 2.

So the family name is a public string: the constant, the JSON, the schema
enum, the `--family` value and the docs must all agree.

## Tests and goldens to update

| Artifact | Command |
|---|---|
| the new family test | `go test ./internal/plan -run TestBuild -v` |
| the plan goldens | `go test ./internal/plan -update` |
| the schema check | `go test ./internal/schematest` |
| the CLI plan golden | `go test ./internal/cli -run TestPlanJSONMatchesGolden` |
| the whole tree | `go test ./...` |
| the coverage floor | `go test -cover ./internal/plan` (floor: 90 %) |

After `go test ./internal/plan -update`, read the diff of every changed
golden. The plan test asserts determinism itself, and
`TestGoldenPlansCiteTheirSource` requires every golden input under
`testdata/plan/` to name the ingress-nginx behavior source, so a new fixture
must cite it too.

## Checklist

- [ ] You read [ADR 0005](../adr/0005-plan-case-families.md) and
      [ADR 0008](../adr/0008-planner-profiles.md).
- [ ] The family isolates exactly one behavior, and the behavior has a citable
      source (a URL or a capture).
- [ ] `Family<Name>` is defined in
      [internal/plan/generate.go](../../internal/plan/generate.go) with a doc
      comment naming the source.
- [ ] The family is emitted in the generic core or in the nginx profile, and
      the choice matches the profile rules.
- [ ] The family is in the `allowed` or the `forbidden` map in
      [internal/plan/profile_test.go](../../internal/plan/profile_test.go).
- [ ] A plan-wide family is in `isSyntheticFamily` in
      [internal/plan/plan_test.go](../../internal/plan/plan_test.go).
- [ ] A table-driven test covers the new family.
- [ ] `docs/plan.md` has a row and `README.md` has a row.
- [ ] The `family` enum in
      [docs/schema/plan.schema.json](../schema/plan.schema.json) lists the new
      family.
- [ ] [ADR 0005](../adr/0005-plan-case-families.md) lists the new family.
- [ ] `go test ./internal/plan -update` was run and the golden diff was read.
- [ ] `go test ./...` passes and `internal/plan` is at or above 90 %
      coverage.
- [ ] The commit is signed off (`git commit -s`).
