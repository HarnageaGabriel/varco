package explain

import (
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"unicode"
)

// Every rule of this package is a semantic claim about controller behavior, so
// CONVENTIONS.md applies to it ("Semantic claims without a source are
// rejected", docs/dev/add-explanation-rule.md). These tests enforce the part
// of that rule that can be checked mechanically: a rule carries a complete
// metadata set, its source is a real https URL on a host the project trusts,
// and docs/run.md documents exactly the rules the code has.

// allowedSourceHosts are the only hosts a rule source may point at.
//
// The first three are the hosts the rules use today: the Kubernetes blog, the
// ingress-nginx documentation and the ingress2gateway repository. The rest are
// reserved for the documentation the project already references elsewhere:
// gateway-api.sigs.k8s.io (Gateway API), nginx.org (nginx), datatracker.ietf.org
// and www.rfc-editor.org (the IETF datatracker and the RFC editor). A new host
// needs a conscious edit here, with the reason in the pull request.
var allowedSourceHosts = []string{
	"kubernetes.io",
	"kubernetes.github.io",
	"github.com",
	"gateway-api.sigs.k8s.io",
	"nginx.org",
	"datatracker.ietf.org",
	"www.rfc-editor.org",
}

// docRuleRow is one row of the `### Explanations` table in docs/run.md.
type docRuleRow struct {
	id  string
	raw string
}

// urlPattern finds the URLs a documentation row cites.
var urlPattern = regexp.MustCompile(`https?://[^\s)]+`)

func TestRuleSourcesAreAbsoluteHTTPSURLs(t *testing.T) {
	for _, item := range rules {
		t.Run(item.id, func(t *testing.T) {
			source := item.source
			if source == "" {
				t.Fatalf("rule %s has no source", item.id)
			}
			if strings.ContainsFunc(source, unicode.IsSpace) {
				t.Errorf("source must not contain spaces: %q", source)
			}
			parsed, err := url.Parse(source)
			if err != nil {
				t.Fatalf("source does not parse as a URL: %q: %v", source, err)
			}
			if parsed.Scheme != "https" {
				t.Errorf("source scheme = %q, want https: %q", parsed.Scheme, source)
			}
			if parsed.Host == "" {
				t.Errorf("source has no host: %q", source)
			}
			if parsed.User != nil {
				t.Errorf("source must not carry userinfo: %q", source)
			}
			if strings.HasPrefix(source, "#") {
				t.Errorf("source must not be a fragment-only reference: %q", source)
			}
			if parsed.Fragment != "" && parsed.Path == "" {
				t.Errorf("source must not be a fragment-only reference: %q", source)
			}
		})
	}
}

func TestRuleSourceHostsAreAllowListed(t *testing.T) {
	allowed := make(map[string]bool, len(allowedSourceHosts))
	for _, host := range allowedSourceHosts {
		if host == "" {
			t.Fatalf("the source host allow-list holds an empty host")
		}
		if allowed[host] {
			t.Errorf("host %q is listed twice in the source host allow-list", host)
		}
		allowed[host] = true
	}
	for _, item := range rules {
		parsed, err := url.Parse(item.source)
		if err != nil {
			t.Fatalf("source of rule %s does not parse as a URL: %q: %v", item.id, item.source, err)
		}
		host := parsed.Hostname()
		if !allowed[host] {
			t.Errorf("rule %s cites host %q, which is not in the source host allow-list; "+
				"add it to allowedSourceHosts consciously", item.id, host)
		}
	}
}

func TestDocumentedRulesMatchTheCode(t *testing.T) {
	rows := docExplanationRows(t)
	codeIDs := make([]string, 0, len(rules))
	for _, item := range rules {
		codeIDs = append(codeIDs, item.id)
	}
	docIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		docIDs = append(docIDs, row.id)
	}
	if !reflect.DeepEqual(codeIDs, docIDs) {
		t.Fatalf("the rules in the code and the rules in docs/run.md differ:\ncode: %v\ndocs: %v", codeIDs, docIDs)
	}
	for _, item := range rules {
		row := rowByID(t, rows, item.id)
		if !strings.Contains(row.raw, item.source) {
			t.Errorf("the docs/run.md row of rule %s does not cite its source %q", item.id, item.source)
		}
		cited := urlPattern.FindAllString(row.raw, -1)
		if len(cited) != 1 || cited[0] != item.source {
			t.Errorf("the docs/run.md row of rule %s cites %v, want exactly [%q]", item.id, cited, item.source)
		}
	}
}

// docExplanationRows parses the `### Explanations` table of docs/run.md. The
// table is hand-maintained (docs/dev/add-explanation-rule.md), so it is the
// documentation of the rules and their order.
func docExplanationRows(t *testing.T) []docRuleRow {
	t.Helper()
	path := filepath.Join("..", "..", "docs", "run.md")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	lines := strings.Split(string(content), "\n")
	start := -1
	for index, line := range lines {
		if strings.TrimSpace(line) == "### Explanations" {
			start = index
			break
		}
	}
	if start < 0 {
		t.Fatalf("%s has no `### Explanations` section", path)
	}
	rows := make([]docRuleRow, 0, len(rules))
	for _, line := range lines[start+1:] {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			break
		}
		if !strings.HasPrefix(trimmed, "|") {
			continue
		}
		cells := strings.Split(trimmed, "|")
		if len(cells) < 3 {
			continue
		}
		first := strings.TrimSpace(cells[1])
		if len(first) < 3 || !strings.HasPrefix(first, "`") || !strings.HasSuffix(first, "`") {
			continue
		}
		rows = append(rows, docRuleRow{id: first[1 : len(first)-1], raw: trimmed})
	}
	if len(rows) == 0 {
		t.Fatalf("the `### Explanations` table of %s holds no rule row", path)
	}
	return rows
}

func rowByID(t *testing.T, rows []docRuleRow, id string) docRuleRow {
	t.Helper()
	for _, row := range rows {
		if row.id == id {
			return row
		}
	}
	t.Fatalf("docs/run.md has no row for rule %q", id)
	return docRuleRow{}
}
