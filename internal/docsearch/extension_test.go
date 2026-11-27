package docsearch

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// Ports the behaviour of microPi packages/docsearch/extension/index.ts.

func TestSearchedNameFindsTheIdentifierOfAnRgOrGrepSearch(t *testing.T) {
	for _, c := range []struct{ command, want string }{
		{"rg ComputeTotal", "ComputeTotal"},
		{`rg -n "def compute_total" src/`, "compute_total"},
		{`grep -rn 'fn parse_args\(' .`, "parse_args"},
		{`git grep -e "\bHandleRequest\b"`, "HandleRequest"},
		{"rg -g '*.go' -t go ValidateToken", "ValidateToken"},
		{"cat x | grep -n 'func Foo('", "Foo"},
		{"/usr/bin/rg loadConfig:", "loadConfig"},
		{"rg 'two words'", ""},
		{"rg ab", ""},
		{"ls -la", ""},
		{"echo rg", ""},
	} {
		if got := SearchedName(c.command); got != c.want {
			t.Errorf("SearchedName(%q) = %q, want %q", c.command, got, c.want)
		}
	}
}

func TestCutTextCutsAtALineBreakInTheSecondHalf(t *testing.T) {
	text := strings.Repeat("a", 60) + "\n" + strings.Repeat("b", 60)
	if got, cut := cutText(text, 100); got != strings.Repeat("a", 60) || !cut {
		t.Fatalf("cut = %q %v", got, cut)
	}
	early := strings.Repeat("a", 10) + "\n" + strings.Repeat("b", 100)
	if got, cut := cutText(early, 50); len(got) != 50 || !cut {
		t.Fatalf("a break in the first half is not used: %q", got)
	}
	if got, cut := cutText("short", 50); got != "short" || cut {
		t.Fatalf("short text cut: %q", got)
	}
	// Characters, not bytes: 30 two-byte characters fit 30.
	if got, cut := cutText(strings.Repeat("ä", 30), 30); cut || got != strings.Repeat("ä", 30) {
		t.Fatalf("multibyte text cut: %q", got)
	}
}

func TestCutSectionTextKeepsCharacterBoundaries(t *testing.T) {
	text := strings.Repeat("ä", 200)
	got, cut := cutSectionText(text, 201)
	if !cut || len(got) != 200 || !strings.HasPrefix(text, got) {
		t.Fatalf("cut %d bytes, truncated %v", len(got), cut)
	}
	if got, _ := cutSectionText(strings.Repeat("x", 3000), 0); len(got) != 1500 {
		t.Fatalf("default limit %d", len(got))
	}
}

func TestFormatShowsTextThenPointers(t *testing.T) {
	p := Project{Base: "/p"}
	hits := []Hit{
		{AbsPath: "/p/a.md", LineStart: 1, LineEnd: 3, Heading: "A", Text: "alpha"},
		{AbsPath: "/p/b.md", LineStart: 4, LineEnd: 9, Text: strings.Repeat("x", 20), Truncated: true},
		{AbsPath: "/elsewhere/c.md", LineStart: 2, LineEnd: 2, Heading: "C"},
	}
	want := "── a.md:1-3 · A\nalpha\n\n── b.md:4-9\n" + strings.Repeat("x", 20) + "\n[cut — read b.md lines 4-9 for the rest]\n\nMore matches (read the lines to see them):\n   /elsewhere/c.md:2-2 · C"
	if got := p.Format(hits, []int{100, 100}); got != want {
		t.Fatalf("format:\n%s\nwant:\n%s", got, want)
	}
}

func setupDocsearchProject(t *testing.T) string {
	t.Helper()
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("DOCSEARCH_CACHE_DIR", t.TempDir())
	t.Setenv("PI_DOCSEARCH", "")
	return t.TempDir()
}

func writeProjectFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func waitForProject(t *testing.T, dir string) Project {
	t.Helper()
	p := OpenProject(dir)
	if p.Engine == nil {
		t.Fatal("no index")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p.Engine.WaitReady(ctx)
	return p
}

func TestAutoRecallAddsMatchingDocSectionsOnly(t *testing.T) {
	dir := setupDocsearchProject(t)
	writeProjectFile(t, dir, "docs/retry.md", "# Retry policy\n\nThe retry policy uses exponential backoff with jitter for transient upstream failures.\n")
	writeProjectFile(t, dir, "retry.go", "package main\n\n// exponential backoff jitter transient upstream failures retry policy\nfunc RetryPolicy() {}\n")
	waitForProject(t, dir)

	recall, ok := AutoRecall(context.Background(), dir, "how does the retry policy use exponential backoff with jitter for transient upstream failures")
	if !ok {
		t.Fatal("no recall for a prompt the doc covers")
	}
	if !strings.HasPrefix(recall.Content, "<doc_search_auto>\nSections from the docs that match this prompt (project_search finds more):\n\n── docs/retry.md:") ||
		!strings.HasSuffix(recall.Content, "\n</doc_search_auto>") || strings.Contains(recall.Content, "retry.go") {
		t.Fatalf("recall:\n%s", recall.Content)
	}
	if len(recall.Places) != 1 || !strings.HasPrefix(recall.Places[0], "docs/retry.md:") {
		t.Fatalf("places %v", recall.Places)
	}
	for _, prompt := range []string{"fix the test", "/docs retry", ""} {
		if _, ok := AutoRecall(context.Background(), dir, prompt); ok {
			t.Errorf("recall for %q", prompt)
		}
	}
	if err := SaveSettingsPatch(map[string]any{"autoRecall": false}); err != nil {
		t.Fatal(err)
	}
	if _, ok := AutoRecall(context.Background(), dir, "how does the retry policy use exponential backoff with jitter for transient upstream failures"); ok {
		t.Fatal("recall with autoRecall off")
	}
}

func TestDefinitionHintNamesTheDefinitionInALargeProject(t *testing.T) {
	dir := setupDocsearchProject(t)
	for i := range 160 {
		writeProjectFile(t, dir, fmt.Sprintf("pkg/f%03d.go", i), fmt.Sprintf("package pkg\n\nfunc Helper%03d() {}\n", i))
	}
	writeProjectFile(t, dir, "pkg/target.go", "package pkg\n\nfunc ValidateSessionToken(token string) bool {\n\treturn token != \"\"\n}\n")
	if IsSmallProject(dir) {
		t.Fatal("161 files counted small")
	}
	waitForProject(t, dir)
	hint, ok := DefinitionHint(context.Background(), dir, "rg ValidateSessionToken", "pkg/use.go:9: ValidateSessionToken(x)")
	if !ok || hint != "\n[project_search] Where ValidateSessionToken is defined:\npkg/target.go:3-5 · ValidateSessionToken" {
		t.Fatalf("hint %q %v", hint, ok)
	}
	if _, ok := DefinitionHint(context.Background(), dir, "rg ValidateSessionToken", "pkg/target.go:3:func ValidateSessionToken"); ok {
		t.Fatal("hint for a definition the output shows")
	}
	if _, ok := DefinitionHint(context.Background(), dir, "ls pkg", ""); ok {
		t.Fatal("hint for a command that searches nothing")
	}
}

func TestToolRefusesHomeAndCountsFromTheGitRoot(t *testing.T) {
	setupDocsearchProject(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	if p := OpenProject(home); p.Engine != nil {
		t.Fatalf("home folder indexed: %v", p.Engine.roots)
	}
	if _, err := ExecuteTool(context.Background(), home, ToolParams{Query: "x"}); err == nil || !strings.HasPrefix(err.Error(), `Nothing to search: no project root here and no "roots" in `) {
		t.Fatalf("home folder search error = %v", err)
	}

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	for i := range 160 {
		writeProjectFile(t, repo, fmt.Sprintf("src/f%03d.go", i), "package src\n")
	}
	writeProjectFile(t, repo, "sub/only.md", "# Only\n")
	if IsSmallProject(filepath.Join(repo, "sub")) {
		t.Fatal("a small subfolder of a large checkout counted small")
	}
}

func TestToolResultTextAndErrors(t *testing.T) {
	dir := setupDocsearchProject(t)
	writeProjectFile(t, dir, "main.go", "package main\n\nfunc AlphaBeta() {}\n")
	waitForProject(t, dir)
	result, err := ExecuteTool(context.Background(), dir, ToolParams{Query: "AlphaBeta"})
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].(ai.TextContent).Text
	if !strings.HasPrefix(text, "2 sections (") || !strings.Contains(text, "; names: alphabeta)") || !strings.Contains(text, "\n\n── main.go:3-3 · AlphaBeta\nfunc AlphaBeta() {}") {
		t.Fatalf("result text %q", text)
	}
	none, err := ExecuteTool(context.Background(), dir, ToolParams{Query: "ZZZ_NONEXISTENT_987"})
	if err != nil {
		t.Fatal(err)
	}
	if got := none.Content[0].(ai.TextContent).Text; got != `No sections match "ZZZ_NONEXISTENT_987". Try other words or names.` {
		t.Fatalf("no-match text %q", got)
	}
}

func TestSaveSettingsPatchKeepsTheFileAndItsOrder(t *testing.T) {
	setupDocsearchProject(t)
	if err := os.WriteFile(SettingsPath(), []byte(`{"roots":["~/notes"],"results":4}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveSettingsPatch(map[string]any{"autoRecall": false}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(SettingsPath())
	if want := "{\n\t\"roots\": [\n\t\t\"~/notes\"\n\t],\n\t\"results\": 4,\n\t\"autoRecall\": false\n}\n"; string(data) != want {
		t.Fatalf("settings file %q", data)
	}
	s := LoadSettings()
	if s.AutoRecall || s.Results != 4 || !s.Project || s.AutoRecallHits != 3 {
		t.Fatalf("settings %+v", s)
	}
}
