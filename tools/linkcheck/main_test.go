package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinkChecks(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		files      map[string]string
		wantFailed bool
		want       string
	}{
		{
			name:  "valid link to existing file",
			files: map[string]string{"README.md": "[guide](guide.md)\n", "guide.md": "# Guide\n"},
		},
		{
			name:       "missing file is a problem",
			files:      map[string]string{"README.md": "[missing](missing.md)\n"},
			wantFailed: true,
			want:       "README.md:1: missing.md: path does not exist",
		},
		{
			name:  "valid heading anchor",
			files: map[string]string{"README.md": "[topic](guide.md#some-topic)\n", "guide.md": "# Some topic!\n"},
		},
		{
			name:       "missing heading anchor",
			files:      map[string]string{"README.md": "[topic](guide.md#absent)\n", "guide.md": "# Topic\n"},
			wantFailed: true,
			want:       "guide.md#absent: anchor does not exist",
		},
		{
			name: "duplicate headings use numbered anchors",
			files: map[string]string{
				"README.md": "[second](guide.md#topic-1)\n",
				"guide.md":  "# Topic\n# Topic\n# Topic\n",
			},
		},
		{
			name:  "fenced code links are ignored",
			files: map[string]string{"README.md": "```md\n[x](missing.md)\n```\n~~~\n[y](also.md)\n~~~\n"},
		},
		{
			name:  "inline code links are ignored",
			files: map[string]string{"README.md": "Use `[x](missing.md)` here.\n"},
		},
		{
			name:  "image links are checked",
			files: map[string]string{"README.md": "![alt](image.png)\n", "image.png": "image"},
		},
		{
			name:  "reference definition is checked",
			files: map[string]string{"README.md": "[label]: target.md\n", "target.md": "# Target\n"},
		},
		{
			name:  "query is excluded from path",
			files: map[string]string{"README.md": "[target](target.md?plain=1)\n", "target.md": "# Target\n"},
		},
		{
			name:  "angle brackets preserve target",
			files: map[string]string{"README.md": "[target](<my file.md>)\n", "my file.md": "# Target\n"},
		},
		{
			name:  "URL encoded path resolves",
			files: map[string]string{"README.md": "[target](my%20file.md)\n", "my file.md": "# Target\n"},
		},
		{
			name:       "case mismatch is a problem",
			files:      map[string]string{"README.md": "[wrong](readme.md)\n"},
			wantFailed: true,
			want:       "readme.md: case mismatch, file is README.md",
		},
		{
			name:  "existing directory is valid",
			files: map[string]string{"README.md": "[docs](docs)\n", "docs/guide.md": "# Guide\n"},
		},
		{
			name:       "missing directory is a problem",
			files:      map[string]string{"README.md": "[docs](missing-dir/)\n"},
			wantFailed: true,
			want:       "missing-dir/: path does not exist",
		},
		{
			name:  "absolute repository path resolves",
			files: map[string]string{"README.md": "[guide](/docs/guide.md)\n", "docs/guide.md": "# Guide\n"},
		},
		{
			name:  "explicit HTML anchor resolves",
			files: map[string]string{"README.md": "[custom](guide.md#custom)\n", "guide.md": "<a id=\"custom\"></a>\n"},
		},
		{
			name:  "reference target on next line resolves",
			files: map[string]string{"README.md": "[label]:\n  <target.md>\n", "target.md": "# Target\n"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeFiles(t, root, test.files)
			var output strings.Builder
			failed, err := run(root, false, &output)
			if err != nil {
				t.Fatalf("run link checker: %v", err)
			}
			if failed != test.wantFailed {
				t.Errorf("failed = %v, want %v; output: %s", failed, test.wantFailed, output.String())
			}
			if test.want == "" && output.Len() != 0 {
				t.Errorf("unexpected output: %s", output.String())
			}
			if test.want != "" && !strings.Contains(output.String(), test.want) {
				t.Errorf("output %q does not contain %q", output.String(), test.want)
			}
		})
	}
}

func TestWalkSkipsIgnoredDirectories(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	files := map[string]string{"README.md": "# Root\n"}
	for _, directory := range []string{".git", ".worktrees", ".notes", "dist", "bin", "node_modules", "testdata"} {
		files[filepath.Join(directory, "bad.md")] = "[missing](missing.md)\n"
	}
	writeFiles(t, root, files)
	var output strings.Builder
	failed, err := run(root, false, &output)
	if err != nil {
		t.Fatalf("run link checker: %v", err)
	}
	if failed || output.Len() != 0 {
		t.Fatalf("ignored directories produced findings: %s", output.String())
	}
}

func TestExternalLinksAreInformationalAndSorted(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"z.md": "[mail](mailto:dev@example.com)\n",
		"a.md": "[secure](https://example.com) and [plain](http://example.com)\n",
	})
	var output strings.Builder
	failed, err := run(root, true, &output)
	if err != nil {
		t.Fatalf("run link checker: %v", err)
	}
	if failed {
		t.Fatal("external links must not fail the check")
	}
	want := "a.md:1: external: http://example.com\na.md:1: external: https://example.com\nz.md:1: external: mailto:dev@example.com\n"
	if output.String() != want {
		t.Errorf("output = %q, want %q", output.String(), want)
	}
}

func TestParserDetails(t *testing.T) {
	t.Parallel()
	data := []byte("before ``[ignored](bad.md)`` [kept](ok.md)\n`multi\n[line](bad.md)\nend`\n")
	links := extractLinks(data)
	if len(links) != 1 || links[0].target != "ok.md" || links[0].line != 1 {
		t.Fatalf("links = %#v, want only ok.md on line 1", links)
	}
}

func TestAnchorRules(t *testing.T) {
	t.Parallel()
	anchors := anchorSet([]byte("# Hello, World! _One_\n# Same\n# Same\n# Same\n<a NAME='named'></a>\n```\n# Hidden\n<a id=\"hidden\"></a>\n```\n"))
	for _, want := range []string{"hello-world-_one_", "same", "same-1", "same-2", "named"} {
		if _, ok := anchors[want]; !ok {
			t.Errorf("missing anchor %q in %#v", want, anchors)
		}
	}
	if _, ok := anchors["hidden"]; ok {
		t.Error("code fence produced an anchor")
	}
}

func TestParserEdgeCases(t *testing.T) {
	t.Parallel()
	links := inlineLinks("[nested](guide_(one).md) [broken](missing", 7)
	if len(links) != 1 || links[0] != (link{line: 7, target: "guide_(one).md"}) {
		t.Fatalf("links = %#v", links)
	}
	if got := normalizeTarget("<file name.md> optional title"); got != "file name.md" {
		t.Errorf("angle target = %q", got)
	}
	if got := normalizeTarget("file.md optional title"); got != "file.md" {
		t.Errorf("titled target = %q", got)
	}
	if got := attributeValue("<a data-id='wrong' id = 'right' disabled>", "id"); got != "right" {
		t.Errorf("attribute = %q, want right", got)
	}
	for _, tag := range []string{"<a id>", "<a id=plain>", "<a id='open>"} {
		if got := attributeValue(tag, "id"); got != "" {
			t.Errorf("attributeValue(%q) = %q", tag, got)
		}
	}
}

func TestExactPathVariants(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"docs/Guide.md": "# Guide\n"})
	actual, mismatch, exists, err := exactPath(root, root)
	if err != nil || !exists || mismatch != "" || actual != root {
		t.Fatalf("root result = %q, %q, %v, %v", actual, mismatch, exists, err)
	}
	_, mismatch, exists, err = exactPath(root, filepath.Join(root, "docs", "guide.md"))
	if err != nil || !exists || mismatch != "Guide.md" {
		t.Fatalf("case result mismatch = %q, exists = %v, err = %v", mismatch, exists, err)
	}
	_, _, exists, err = exactPath(root, filepath.Join(root, "absent", "file.md"))
	if err != nil || exists {
		t.Fatalf("missing result exists = %v, err = %v", exists, err)
	}
}

func TestMissingRootReturnsWalkError(t *testing.T) {
	t.Parallel()
	var output strings.Builder
	_, err := run(filepath.Join(t.TempDir(), "absent"), false, &output)
	if err == nil || !strings.Contains(err.Error(), "walk Markdown files") {
		t.Fatalf("error = %v, want walk error", err)
	}
}

func TestInvalidTargetsAndAnchorReadErrors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"README.md":     "[bad](bad%ZZ.md)\n[dir](docs#anchor)\n[img](logo.png#alt)\n",
		"docs/file.txt": "content",
		"logo.png":      "binary",
	})
	var output strings.Builder
	failed, err := run(root, false, &output)
	if err != nil {
		t.Fatalf("run link checker: %v", err)
	}
	if !failed || !strings.Contains(output.String(), "invalid URL escape") {
		t.Fatalf("unexpected output: %s", output.String())
	}
	if strings.Contains(output.String(), "read anchors") {
		t.Errorf("a fragment on a non-Markdown target must be ignored: %s", output.String())
	}
}

func TestAnchorReadErrorIsReported(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"README.md": "[page](page.md#top)\n"})
	if err := os.Mkdir(filepath.Join(root, "page.md"), 0o755); err != nil {
		t.Fatalf("create directory named like a page: %v", err)
	}
	var output strings.Builder
	failed, err := run(root, false, &output)
	if err != nil {
		t.Fatalf("run link checker: %v", err)
	}
	if !failed || !strings.Contains(output.String(), "read anchors") {
		t.Fatalf("unexpected output: %s", output.String())
	}
}

func TestPathLeavingTheRootIsAProblem(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"README.md": "[up](../../outside.md)\n"})
	var output strings.Builder
	failed, err := run(root, false, &output)
	if err != nil {
		t.Fatalf("run link checker: %v", err)
	}
	if !failed || !strings.Contains(output.String(), "leaves the repository root") {
		t.Fatalf("unexpected output: %s", output.String())
	}
}

func TestOutputErrorIsReturned(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"README.md": "[missing](missing.md)\n"})
	_, err := run(root, false, errorWriter{})
	if err == nil || !strings.Contains(err.Error(), "write output") {
		t.Fatalf("error = %v, want output error", err)
	}
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, contents := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create directory for %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

var _ io.Writer = errorWriter{}
