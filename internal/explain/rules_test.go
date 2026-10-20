package explain

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strings"
	"testing"
	"unicode"

	"github.com/HarnageaGabriel/varco/internal/model"
)

// The tests in this file enforce the shape of a rule: a complete metadata set,
// an unverified claim marked as such, a claim about another controller that is
// either verified against that controller's documentation or marked VERIFY, and
// a trigger that fires on its own example and on nothing else.

// kebabCaseID is the shape a rule id has: lowercase words joined by dashes.
var kebabCaseID = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// minVerifyWords is the number of words a VERIFY marker has to be followed by.
// A bare `VERIFY:` says that something is unverified without saying what, so
// the text around the marker has to name the behavior that must be checked.
const minVerifyWords = 3

// nonNginxControllers are the non-nginx controllers and projects a rule may
// talk about. A rule that describes one of their behaviors needs either a
// VERIFY marker or a source on that project's documentation.
var nonNginxControllers = []string{
	"envoy gateway",
	"gateway api",
	"haproxy",
	"ingress2gateway",
	"istio",
	"kong",
	"traefik",
}

// crossControllerSources are the sources that document how ingress-nginx
// differs from another controller, so a rule may cite them for a claim about
// that controller without naming the controller in the URL.
var crossControllerSources = map[string]string{
	blogSource: "the Kubernetes migration blog documents the ingress-nginx and Gateway API behaviors (docs/run.md, behaviors 1-5)",
}

// ruleExample is the positive and the negative example of one rule. The
// positive example is a DIFFERENT evaluation the rule has to explain; the
// negative one is a near miss the rule must not fire on.
type ruleExample struct {
	id       string
	family   string
	positive []model.Difference
	negative []model.Difference
}

// ruleExamples holds one example per rule. A rule without an entry here is
// a rule nobody checked, so the rule set and this table are kept in step.
var ruleExamples = []ruleExample{
	{
		id:       "nginx-regex-case-insensitive",
		family:   "case-flip",
		positive: []model.Difference{{Field: "status", Old: "200", New: "404"}},
		negative: []model.Difference{{Field: "body", Old: "old", New: "new"}},
	},
	{
		id:       "nginx-regex-prefix-host-wide",
		family:   "prefix-boundary",
		positive: []model.Difference{{Field: "status", Old: "404", New: "200"}},
		negative: []model.Difference{{Field: "body", Old: "old", New: "new"}},
	},
	{
		id:       "nginx-trailing-slash-redirect",
		family:   "trailing-slash",
		positive: []model.Difference{{Field: "status", Old: "301", New: "200"}},
		negative: []model.Difference{{Field: "status", Old: "302", New: "200"}},
	},
	{
		id:       "nginx-url-normalization",
		family:   "dot-segments",
		positive: []model.Difference{{Field: "status", Old: "301", New: "200"}},
		negative: []model.Difference{{Field: "body", Old: "old", New: "new"}},
	},
	{
		id:       "ingress2gateway-rewrite-target",
		family:   "rewrite",
		positive: []model.Difference{{Field: "body", Old: "old", New: "new"}},
		negative: []model.Difference{{Field: "status", Old: "200", New: "404"}},
	},
	{
		id:       "nginx-http-redirect",
		family:   "http-redirect",
		positive: []model.Difference{{Field: "status", Old: "308", New: "301"}},
		negative: []model.Difference{{Field: "body", Old: "old", New: "new"}},
	},
	{
		id:       "nginx-default-backend",
		family:   "default-backend",
		positive: []model.Difference{{Field: "status", Old: "404", New: "200"}},
		negative: []model.Difference{{Field: "header.location", Old: "/a", New: "/b"}},
	},
	{
		id:       "ingress-nginx-cors-defaults",
		family:   "cors-preflight",
		positive: []model.Difference{{Field: "header.access-control-allow-origin", Old: "*", New: "https://one.invalid"}},
		negative: []model.Difference{{Field: "header.x-custom", Old: "old", New: "new"}},
	},
}

func TestRulesCarryCompleteMetadata(t *testing.T) {
	seen := make(map[string]bool, len(rules))
	for _, item := range rules {
		if item.id == "" {
			t.Errorf("a rule has an empty id")
			continue
		}
		if seen[item.id] {
			t.Errorf("rule id %q is used twice", item.id)
		}
		seen[item.id] = true
		if !kebabCaseID.MatchString(item.id) {
			t.Errorf("rule id %q is not lowercase kebab-case", item.id)
		}
		if strings.TrimSpace(item.summary) == "" {
			t.Errorf("rule %s has no summary; the summary is the `why` of the explanation", item.id)
		}
		if strings.TrimSpace(item.fix) == "" {
			t.Errorf("rule %s has no fix", item.id)
		}
		if strings.TrimSpace(item.source) == "" {
			t.Errorf("rule %s has no source", item.id)
		}
	}
}

func TestVerifyRulesSayWhatToVerify(t *testing.T) {
	sliceDoc, perRule := ruleVerifyNotes(t)
	for _, item := range rules {
		text := item.summary + "\n" + item.fix
		if !strings.Contains(text, "VERIFY") {
			continue
		}
		for _, field := range []struct {
			name string
			text string
		}{
			{name: "summary", text: item.summary},
			{name: "fix", text: item.fix},
		} {
			if !strings.Contains(field.text, "VERIFY") {
				continue
			}
			if words := verifyWords(field.text); words < minVerifyWords {
				t.Errorf("rule %s marks its %s as unverified without saying what to verify: %q",
					item.id, field.name, field.text)
			}
		}
		note := perRule[item.id]
		if strings.Contains(item.summary, "VERIFY") {
			// A rule whose summary states an unverified fact carries its own
			// `// VERIFY:` comment, as docs/dev/add-explanation-rule.md asks.
			if !strings.Contains(note, "VERIFY") {
				t.Errorf("rule %s states an unverified claim but its literal carries no `// VERIFY:` note",
					item.id)
				continue
			}
		} else if note == "" {
			// The note of the rules slice covers a rule that only marks its
			// fix as unverified.
			note = sliceDoc
		}
		if !strings.Contains(note, "VERIFY") {
			t.Errorf("rule %s carries an unverified claim but no `// VERIFY:` note says what is unverified",
				item.id)
			continue
		}
		if words := verifyWords(note); words < minVerifyWords {
			t.Errorf("the VERIFY note of rule %s does not say what to verify: %q", item.id, note)
		}
	}
}

func TestNonNginxControllerClaimsAreSourcedOrVerified(t *testing.T) {
	for _, item := range rules {
		text := item.summary + " " + item.fix
		for _, controller := range nonNginxControllers {
			if !mentionsController(text, controller) {
				continue
			}
			if strings.Contains(text, "VERIFY") {
				continue
			}
			if mentionsController(item.source, controller) {
				continue
			}
			if _, ok := crossControllerSources[item.source]; ok {
				continue
			}
			t.Errorf("rule %s claims a behavior of %s without a VERIFY marker or a source on "+
				"its documentation (source %q)", item.id, controller, item.source)
		}
	}
}

func TestEveryRuleHasAnExample(t *testing.T) {
	known := make(map[string]bool, len(rules))
	for _, item := range rules {
		known[item.id] = true
	}
	seen := make(map[string]bool, len(ruleExamples))
	for _, example := range ruleExamples {
		if seen[example.id] {
			t.Errorf("rule %s has more than one example", example.id)
		}
		seen[example.id] = true
		if !known[example.id] {
			t.Errorf("the example of rule %q refers to a rule that does not exist", example.id)
		}
	}
	for _, item := range rules {
		if !seen[item.id] {
			t.Errorf("rule %s has no example in ruleExamples", item.id)
		}
	}
}

func TestRulesFireOnTheirExampleOnly(t *testing.T) {
	for _, example := range ruleExamples {
		t.Run(example.id, func(t *testing.T) {
			positive := model.Evaluation{
				Outcome:     model.OutcomeDifferent,
				Differences: example.positive,
			}
			got := Explain(positive, example.family)
			if len(got) != 1 || got[0].ID != example.id {
				t.Fatalf("Explain(%q) = %#v, want exactly the explanation %q", example.family, got, example.id)
			}
			negative := model.Evaluation{
				Outcome:     model.OutcomeDifferent,
				Differences: example.negative,
			}
			if got := Explain(negative, example.family); len(got) != 0 {
				t.Fatalf("Explain(%q) on the negative example = %#v, want no explanation", example.family, got)
			}
			equivalent := model.Evaluation{
				Outcome:     model.OutcomeEquivalent,
				Differences: example.positive,
			}
			if got := Explain(equivalent, example.family); len(got) != 0 {
				t.Fatalf("Explain(%q) on an EQUIVALENT evaluation = %#v, want no explanation", example.family, got)
			}
		})
	}
}

// ruleVerifyNotes reads the VERIFY notes of the rule literals and of the rules
// slice itself from the source file, so a rule that claims an unverified fact
// cannot do it without saying what is unverified.
//
// A comment inside a composite literal is not attached to an element of it by
// go/ast, so the notes are matched to a rule by their position.
func ruleVerifyNotes(t *testing.T) (string, map[string]string) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "explain.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse explain.go: %v", err)
	}
	sliceDoc := ""
	var rulesLiteral *ast.CompositeLit
	ast.Inspect(file, func(node ast.Node) bool {
		decl, ok := node.(*ast.GenDecl)
		if !ok || decl.Tok != token.VAR {
			return true
		}
		for _, spec := range decl.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for index, name := range value.Names {
				if name.Name != "rules" || index >= len(value.Values) {
					continue
				}
				if decl.Doc != nil {
					sliceDoc = decl.Doc.Text()
				}
				if value.Doc != nil {
					sliceDoc = value.Doc.Text()
				}
				if literal, ok := value.Values[index].(*ast.CompositeLit); ok {
					rulesLiteral = literal
				}
			}
		}
		return true
	})
	if sliceDoc == "" {
		t.Errorf("the rules slice of explain.go carries no documentation")
	}
	perRule := make(map[string]string, len(rules))
	if rulesLiteral == nil {
		return sliceDoc, perRule
	}
	for _, element := range rulesLiteral.Elts {
		rule, ok := element.(*ast.CompositeLit)
		if !ok {
			continue
		}
		id := ruleLiteralID(rule)
		if id == "" {
			continue
		}
		notes := make([]string, 0, 2)
		for _, group := range file.Comments {
			if group.Pos() < rule.Pos() || group.End() > rule.End() {
				continue
			}
			notes = append(notes, group.Text())
		}
		perRule[id] = strings.Join(notes, "\n")
	}
	return sliceDoc, perRule
}

// ruleLiteralID returns the id of a rule literal.
func ruleLiteralID(literal *ast.CompositeLit) string {
	for _, element := range literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := field.Key.(*ast.Ident)
		if !ok || key.Name != "id" {
			continue
		}
		if value, ok := field.Value.(*ast.BasicLit); ok && value.Kind == token.STRING {
			return strings.Trim(value.Value, `"`)
		}
	}
	return ""
}

// verifyWords counts the words a text has besides its VERIFY markers. A VERIFY
// marker on its own says that something is unverified without saying what, so
// the surrounding sentence has to carry the claim to check.
func verifyWords(text string) int {
	best := 0
	for _, sentence := range splitSentences(text) {
		if !strings.Contains(sentence, "VERIFY") {
			continue
		}
		words := countWords(markerPattern.ReplaceAllString(sentence, " "))
		if words > best {
			best = words
		}
	}
	return best
}

// markerPattern matches the VERIFY marker as a word.
var markerPattern = regexp.MustCompile(`(?i)\bVERIFY\b`)

// splitSentences splits text at the end of a sentence.
var sentencePattern = regexp.MustCompile(`\.\s+`)

func splitSentences(text string) []string {
	return sentencePattern.Split(text, -1)
}

// countWords counts the whitespace separated words that carry a letter.
func countWords(text string) int {
	count := 0
	for _, word := range strings.Fields(text) {
		if strings.ContainsFunc(word, unicode.IsLetter) {
			count++
		}
	}
	return count
}

// mentionsController reports whether text talks about the given controller. A
// single word controller has to appear as a word of its own; a multi word
// controller has to appear as adjacent words, so `Gateway API` is found and a
// stray `api` is not.
func mentionsController(text, controller string) bool {
	words := controllerWords(text)
	target := controllerWords(controller)
	if len(target) == 0 {
		return false
	}
	if len(target) == 1 {
		for _, word := range words {
			if word == target[0] {
				return true
			}
		}
		return false
	}
	return strings.Contains(strings.Join(words, ""), strings.Join(target, ""))
}

// controllerWords splits text into lowercase alphanumeric words.
func controllerWords(text string) []string {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	words := make([]string, 0, len(fields))
	for _, field := range fields {
		words = append(words, strings.ToLower(field))
	}
	return words
}
