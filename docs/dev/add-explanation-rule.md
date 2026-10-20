# Add an explanation rule

Goal: explain a known migration difference with a stable rule id, a suggested
fix and a citable source, so a `DIFFERENT` case says why it happened instead of
only what changed.

The rules live in [internal/explain](../../internal/explain). Read
[CONVENTIONS.md](../../CONVENTIONS.md) (mandatory for every contributor) and
[ADR 0007: verdict model](../adr/0007-verdict-model.md) first: an explanation
only ever attaches to a `DIFFERENT` case.

This guide serves the open issues
[#15](https://github.com/HarnageaGabriel/varco/issues/15)
(`probe: report the TLS versions an endpoint accepts`) and
[#16](https://github.com/HarnageaGabriel/varco/issues/16)
(`probe: detect HTTP/2 (ALPN) differences`): both add a new compared field, and
a new field is exactly what a new explanation rule explains. The other guides
are in the [dev guide index](README.md).

## How an explanation works

A rule is one entry of an ordered slice in
[internal/explain/explain.go](../../internal/explain/explain.go):

```go
type rule struct {
	id      string
	summary string
	fix     string
	source  string
	matches func(family string, evaluation model.Evaluation) bool
}
```

The slice is ordered on purpose:

```go
// VERIFY: Envoy Gateway's ClientTrafficPolicy path-settings field name.
//
// The rules are an ordered slice so output never depends on map iteration.
// Rules without the Kubernetes migration blog as their primary source carry
// VERIFY in both their summary and this comment.
var rules = []rule{
```

`Explain` is the only exported identifier of the package:

```go
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
```

The `matches` closure decides when the rule applies. It gets the case family
and the evaluation, and looks for a difference with `hasDifference`:

```go
func hasDifference(evaluation model.Evaluation, matches func(field, oldValue string) bool) bool {
	for _, difference := range evaluation.Differences {
		if matches(difference.Field, difference.Old) {
			return true
		}
	}
	return false
}
```

`Explain` is called from exactly one place,
[internal/engine/run.go](../../internal/engine/run.go):

```go
if evaluation.Outcome == model.OutcomeDifferent {
	evaluation.Explanations = explain.Explain(evaluation, evaluation.Family)
}
```

The output lands in two places. The table prints one indented line per
explanation after the difference rows
([internal/report/run.go](../../internal/report/run.go)):

```
why: <summary>  fix: <fix>  see: <source>
```

and the JSON report carries the additive `explanations` array on the case
([internal/model/model.go](../../internal/model/model.go)):

```go
// Explanation is a known migration behavior that can explain a difference.
type Explanation struct {
	ID      string `json:"id"`
	Summary string `json:"summary"`
	Fix     string `json:"fix"`
	Source  string `json:"source"`
}
```

## The source URL rule

The rule that makes an explanation trustworthy comes from
[CONVENTIONS.md](../../CONVENTIONS.md):

> - Table-driven, `go-cmp`, goldens under `testdata/` (`-update`). Every case
>   cites its source (URL or capture).
> - CI green, adversarial review, maintainer reads every change touching
>   request sending, TLS handling or redaction. Semantic claims without a
>   source are rejected.

and from the AI agent rule:

> - Never use `git stash`; use a WIP commit. Stay in task scope. Mark
>   unverified claims `VERIFY:`.

So a rule has two legal shapes:

1. **Verified.** The `source` is a real URL that says what the summary claims.
   The summary states the behavior as fact.
2. **Not verified.** The `source` is still a URL, but the claim is not checked
   against it. Then the summary must start with `VERIFY:` and the rule literal
   must carry a `// VERIFY:` comment saying what exactly is unverified.

The package comment states the same rule: *Rules without the Kubernetes
migration blog as their primary source carry VERIFY in both their summary and
this comment.* The blog is
[Five Surprising Ingress-NGINX Behaviors](https://kubernetes.io/blog/2026/02/27/ingress-nginx-before-you-migrate),
and the other source constants in the file are:

```go
const (
	blogSource        = "https://kubernetes.io/blog/2026/02/27/ingress-nginx-before-you-migrate"
	rewriteSource     = "https://github.com/kubernetes-sigs/ingress2gateway/blob/main/pkg/i2gw/providers/ingressnginx/README.md"
	redirectSource    = "https://kubernetes.github.io/ingress-nginx/user-guide/nginx-configuration/configmap/"
	defaultBackSource = "https://kubernetes.github.io/ingress-nginx/user-guide/cli-arguments/"
	corsSource        = "https://kubernetes.github.io/ingress-nginx/user-guide/nginx-configuration/annotations/"
)
```

A new rule may add a new constant, but it must be a URL that a human can
open. `TestUnsourcedRulesCarryVerifyWording` enforces the VERIFY marker for the
three rules that are not blog-sourced:

```go
func TestUnsourcedRulesCarryVerifyWording(t *testing.T) {
	for _, id := range []string{"nginx-http-redirect", "nginx-default-backend", "ingress-nginx-cors-defaults"} {
		item := ruleByID(t, id)
		if !strings.Contains(item.summary, "VERIFY") {
			t.Fatalf("%s summary must contain VERIFY: summary=%q", id, item.summary)
		}
	}
}
```

## Coverage

`internal/explain` is at **100 % statement coverage** today. Verified:

```sh
go test -cover ./internal/explain
```

```
ok  	github.com/HarnageaGabriel/varco/internal/explain	(cached)	coverage: 100.0% of statements
```

The CI floor for the package is 90 %
([.github/workflows/ci.yml](../../.github/workflows/ci.yml)), so a new rule
without a test still passes CI but breaks the 100 % the package has today. The
`matches` closure is a statement, so it is only counted as covered when a test
reaches it. Keep the package at 100 %: every new rule needs a case in
`TestExplainRules`.

## Files to touch

| File | What changes |
|---|---|
| [internal/explain/explain.go](../../internal/explain/explain.go) | the new `rule` literal, and a source constant when the URL is new |
| [internal/explain/explain_test.go](../../internal/explain/explain_test.go) | a case in `TestExplainRules`, and the new id in `TestRuleOrderIsStable` |
| [docs/run.md](../run.md) | the `### Explanations` table, which is hand-maintained |
| [testdata/run/table.golden](../../testdata/run/table.golden) and [testdata/run/json.golden](../../testdata/run/json.golden) | regenerated, when the rendered output changes |

## Worked example: `nginx-trailing-slash-redirect`

This rule explains the 301 that ingress-nginx returns for a path that is
missing its trailing slash. It shows the whole shape of a rule.

**1. The rule** in [internal/explain/explain.go](../../internal/explain/explain.go):

```go
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
```

Note the trigger: the family, the differing field, and the value the old
endpoint returned. A rule that fires on every difference of a family is too
broad and produces noise.

**2. The test case** in
[internal/explain/explain_test.go](../../internal/explain/explain_test.go). The
table is driven by family and differences, and the expected explanation is
built from the rule itself, so the test asserts the mapping and the faithful
copy of `id`, `summary`, `fix` and `source`:

```go
	tests := []struct {
		name        string
		family      string
		differences []model.Difference
		wantID      string
	}{
		...
		{
			name:        "trailing slash old 301",
			family:      "trailing-slash",
			differences: []model.Difference{{Field: "status", Old: "301", New: "200"}},
			wantID:      "nginx-trailing-slash-redirect",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evaluation := model.Evaluation{
				Outcome:     model.OutcomeDifferent,
				Differences: test.differences,
			}
			got := Explain(evaluation, test.family)
			if len(got) != 1 {
				t.Fatalf("Explain() returned %d explanations, want 1: %#v", len(got), got)
			}
			want := ruleByID(t, test.wantID)
			wantExplanation := model.Explanation{ID: want.id, Summary: want.summary, Fix: want.fix, Source: want.source}
			if !reflect.DeepEqual(got[0], wantExplanation) {
				t.Fatalf("Explain() = %#v, want %#v", got[0], wantExplanation)
			}
		})
	}
```

**3. The negative case.** `TestExplainNegativeCases` asserts that the rule does
not fire for an `EQUIVALENT` evaluation, for an unrelated family, and for a
trailing-slash difference whose old status is not `301`. Add the equivalent
negative case for a new rule; a rule that fires too often is the most common
defect here.

**4. The order list.** `TestRuleOrderIsStable` pins the exact order of the
rule ids, so a new rule must be added to that list:

```go
	want := []string{
		"nginx-regex-case-insensitive",
		"nginx-regex-prefix-host-wide",
		"nginx-trailing-slash-redirect",
		"nginx-url-normalization",
		"ingress2gateway-rewrite-target",
		"nginx-http-redirect",
		"nginx-default-backend",
		"ingress-nginx-cors-defaults",
	}
```

**5. The docs table.** [docs/run.md](../run.md) has a hand-maintained
`### Explanations` table with one row per rule, its trigger and its source.
This table is **not** generated, so it has to be updated by hand:

```
| `nginx-trailing-slash-redirect` | `trailing-slash` with a `status` difference where the old status is `301`; ingress-nginx redirects a missing trailing slash. | [Before you migrate](https://kubernetes.io/blog/2026/02/27/ingress-nginx-before-you-migrate), behavior 4 |
```

**6. The goldens.** The rendered table and JSON of the run report contain the
explanation text, so the goldens change when a rule or its text changes:

[testdata/run/table.golden](../../testdata/run/table.golden) holds the
rendered line

```
...  why: ingress-nginx matches use-regex/rewrite-target paths case-insensitively; Gateway API matches are case-sensitive.  fix: Use a RegularExpression match with (?i), or correct the path casing.  see: https://kubernetes.io/blog/2026/02/27/ingress-nginx-before-you-migrate
```

## Tests and commands

| What | Command |
|---|---|
| the rule tests | `go test ./internal/explain -v` |
| the coverage of the package | `go test -cover ./internal/explain` (keep it at 100 %) |
| regenerate the run goldens | `go test ./internal/report -update` |
| verify the run goldens | `go test ./internal/report` |
| the whole tree | `go test ./...` |
| the lint gate | `golangci-lint run ./...` |

After `go test ./internal/report -update`, read the diff of
`testdata/run/table.golden` and `testdata/run/json.golden`. The JSON golden is
checked against the published schema by
[internal/schematest](../../internal/schematest), so a rule that produces an
explanation without a `source` fails there.

## Checklist

- [ ] You read [CONVENTIONS.md](../../CONVENTIONS.md) and
      [ADR 0007](../adr/0007-verdict-model.md).
- [ ] The rule has a stable kebab-case id, a summary, a fix and a source.
- [ ] The source is a URL a human can open, and it really says what the
      summary claims.
- [ ] If the claim is not verified, the summary starts with `VERIFY:` and the
      rule carries a `// VERIFY:` comment saying what is unverified.
- [ ] The trigger is narrow: the family, the differing field and, where it
      matters, the old value.
- [ ] A positive case is in `TestExplainRules` and a negative case is in
      `TestExplainNegativeCases`.
- [ ] The new id is in the `want` list of `TestRuleOrderIsStable`.
- [ ] `go test -cover ./internal/explain` still reports 100.0 %.
- [ ] The `### Explanations` table in [docs/run.md](../run.md) has the new row.
- [ ] `go test ./internal/report -update` was run and the golden diff was
      read.
- [ ] `go test ./...` passes and `golangci-lint run ./...` is clean.
- [ ] The commit is signed off (`git commit -s`).
