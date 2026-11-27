package docsearch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTextTokenizationStemmingAndTranslation(t *testing.T) {
	raw := RawWords("spec.md model. `foo_bar` DFlash2 --flag")
	if len(raw) < 4 {
		t.Fatalf("RawWords returned too few words: %v", raw)
	}
	if raw[0] != "spec.md" || raw[1] != "model" {
		t.Fatalf("unexpected RawWords prefix: %v", raw)
	}

	if got := Stem("running"); got != "runn" {
		t.Fatalf("Stem(running) = %q, want runn", got)
	}
	if got := Stem("run"); got != "run" {
		t.Fatalf("Stem(run) = %q, want run (remaining < 4)", got)
	}

	cp := CamelParts("getUser2Profile")
	wantCP := []string{"get", "user", "2", "profile"}
	if strings.Join(cp, ",") != strings.Join(wantCP, ",") {
		t.Fatalf("CamelParts(getUser2Profile) = %v, want %v", cp, wantCP)
	}

	terms := Terms("getUserProfile DFlash2")
	has := func(s string) bool {
		for _, term := range terms {
			if term == s {
				return true
			}
		}
		return false
	}
	if !has("getuserprofile") || !has("user") || !has("profile") || !has("dflash") {
		t.Fatalf("Terms missing expected decomposed parts: %v", terms)
	}

	if !IsEntityWord("foo_bar") || !IsEntityWord("DFlash2") || !IsEntityWord("PPR") || IsEntityWord("the") {
		t.Fatalf("IsEntityWord failed basic checks")
	}

	ents := Entities("Use `my_func` and [docs](guide/plugin.md#intro) with MyService.", "server.go", true)
	entSet := make(map[string]bool)
	for _, e := range ents {
		entSet[e] = true
	}
	for _, want := range []string{"my_func", "plugin.md", "myservice", "server.go"} {
		if !entSet[want] {
			t.Fatalf("Entities missing %q in %v", want, ents)
		}
	}

	tr := WithTranslations("Fehler bei Datei Suche verteilt")
	if !strings.Contains(tr, "error") || !strings.Contains(tr, "file") || !strings.Contains(tr, "search") || !strings.Contains(tr, "distribute") {
		t.Fatalf("WithTranslations missing expected English expansions: %q", tr)
	}
}

func TestChunkMarkdownAndAllLanguages(t *testing.T) {
	md := `# Top
Intro text.

` + "```bash\n# not a heading\necho hi\n```" + `

## Sub
Sub body line.
`
	mdChunks := Split(md, true)
	if len(mdChunks) != 2 {
		t.Fatalf("expected 2 markdown chunks, got %d: %+v", len(mdChunks), mdChunks)
	}
	if mdChunks[0].Heading != "Top" || !strings.Contains(mdChunks[0].Text, "# not a heading") {
		t.Fatalf("fence inside markdown should not split: %+v", mdChunks[0])
	}
	if mdChunks[1].Heading != "Top › Sub" {
		t.Fatalf("expected heading stack 'Top › Sub', got %q", mdChunks[1].Heading)
	}

	// Long section windowing.
	var big strings.Builder
	for i := range 40 {
		fmt.Fprintf(&big, "Paragraph %d with enough text to exceed the target window size easily.\n\n", i)
	}
	bigChunks := Split("# Big\n"+big.String(), true)
	if len(bigChunks) < 2 {
		t.Fatalf("expected long section to window into >=2 chunks, got %d", len(bigChunks))
	}

	// Go
	goSrc := `package p

// Doc comment for Widget.
type Widget struct {
	X int
}

// Render draws the widget.
func (w *Widget) Render() string {
	return "ok"
}
`
	goChunks := SplitCode(goSrc, LangGo)
	if len(goChunks) < 2 {
		t.Fatalf("expected >=2 Go chunks, got %d", len(goChunks))
	}
	foundRender := false
	for _, c := range goChunks {
		if c.Heading == "Widget.Render" && strings.Contains(c.Text, "// Doc comment") == false && strings.Contains(c.Text, "// Render draws") {
			foundRender = true
		}
	}
	if !foundRender {
		t.Fatalf("expected Widget.Render with attached comment, got %+v", goChunks)
	}

	// C/C++
	cSrc := `// Vector helper
class Vec2 {
public:
    float x, y;
};

int compute_norm(int a, int b) {
    return a * a + b * b;
}
`
	cChunks := SplitCode(cSrc, LangC)
	if len(cChunks) < 2 || cChunks[0].Heading != "Vec2" || cChunks[1].Heading != "compute_norm" {
		t.Fatalf("unexpected C chunks: %+v", cChunks)
	}

	// Rust
	rsSrc := `pub struct Pool {
    size: usize,
}

impl Pool {
    pub fn allocate(&self) -> usize {
        self.size
    }
}
`
	rsChunks := SplitCode(rsSrc, LangRust)
	foundMerged := false
	for _, c := range rsChunks {
		if c.Heading == "Pool › allocate" {
			foundMerged = true
		}
	}
	if !foundMerged {
		t.Fatalf("expected nested Rust header merge 'Pool › allocate', got %+v", rsChunks)
	}

	// Python
	pySrc := `@dataclass
class Box:
    def open(self):
        return True
`
	pyChunks := SplitCode(pySrc, LangPython)
	if len(pyChunks) == 0 || pyChunks[0].Heading != "Box › open" {
		t.Fatalf("expected Python merged header 'Box › open', got %+v", pyChunks)
	}

	// JS/TS
	tsSrc := `export async function fetchConfig(id: string) {
  return id;
}

export class Client {
  private connect(url: string) {
    return url;
  }
}
`
	tsChunks := SplitCode(tsSrc, LangScript)
	if len(tsChunks) < 2 {
		t.Fatalf("expected >=2 TS chunks, got %+v", tsChunks)
	}

	// Shell
	shSrc := `#!/bin/bash
deploy_app() {
  echo "deploy"
}
`
	shChunks := SplitCode(shSrc, LangShell)
	if len(shChunks) == 0 || shChunks[0].Heading != "deploy_app" {
		t.Fatalf("expected shell chunk 'deploy_app', got %+v", shChunks)
	}
}

func TestIndexSearchRankingStoreAndOverlay(t *testing.T) {
	dir := t.TempDir()

	// Write sample files.
	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("engine.go", `package main

// ComputePageRank runs Personalized PageRank over the section-entity graph.
func ComputePageRank(alpha float64) float64 {
	return alpha * 2.0
}

func helperUtil() int {
	return 42
}
`)
	write("docs/architecture.md", `# Architecture
## Graph Ranking
The system uses ComputePageRank and E2GraphRAG co-occurrence to connect sections.
`)
	write("docs/unrelated.md", `# Changelog
Fixed minor typos in readme.
`)
	// Binary file should be skipped.
	write("blob.bin", "hello\x00world")

	idx, err := Build(Config{Roots: []string{dir}})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	if idx.NumFiles() != 3 {
		t.Fatalf("expected 3 non-binary files, got %d", idx.NumFiles())
	}

	// Go-to-definition boost should rank ComputePageRank definition first.
	res := idx.Search("ComputePageRank", SearchOpts{K: 5})
	if len(res.Hits) == 0 {
		t.Fatalf("expected hits for ComputePageRank")
	}
	if res.Hits[0].Path != "engine.go" || res.Hits[0].Heading != "ComputePageRank" {
		t.Fatalf("expected engine.go ComputePageRank first, got %+v", res.Hits[0])
	}

	// PathPrefix filter.
	docRes := idx.Search("ComputePageRank", SearchOpts{K: 5, PathPrefix: "docs/"})
	if len(docRes.Hits) == 0 || !strings.HasPrefix(docRes.Hits[0].Path, "docs/") {
		t.Fatalf("PathPrefix filter failed: %+v", docRes.Hits)
	}

	// Save & mmap Load + .ok stamp verification.
	idxPath := filepath.Join(t.TempDir(), "test.idx")
	if err := idx.Save(idxPath); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	if _, err := os.Stat(idxPath + ".ok"); err != nil {
		t.Fatalf("expected .ok stamp file: %v", err)
	}
	loaded, err := Load(idxPath, []string{dir})
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	defer loaded.Close()

	resLoaded := loaded.Search("ComputePageRank", SearchOpts{K: 5})
	if len(resLoaded.Hits) == 0 || resLoaded.Hits[0].Heading != "ComputePageRank" {
		t.Fatalf("loaded index search mismatch: %+v", resLoaded.Hits)
	}

	// Corrupt file rejection.
	badPath := filepath.Join(t.TempDir(), "bad.idx")
	if err := os.WriteFile(badPath, []byte("NOT_AN_INDEX"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(badPath, []string{dir}); err == nil {
		t.Fatalf("expected error loading corrupt index")
	}

	// Overlay incremental add, update, delete.
	write("new_feature.go", `package main

func QuantumRouter(x int) int {
	return x + 1
}
`)
	if err := os.Remove(filepath.Join(dir, "docs", "unrelated.md")); err != nil {
		t.Fatal(err)
	}
	// Ensure mtime/size change on engine.go.
	write("engine.go", `package main

// ComputePageRankV2 replaces old pagerank.
func ComputePageRankV2(alpha float64) float64 {
	return alpha * 3.0
}
`)

	scanned, err := Scan([]string{dir}, DefaultMaxFileBytes, DefaultMaxFiles)
	if err != nil {
		t.Fatal(err)
	}
	changed, deleted := loaded.Diff(scanned)
	if len(changed) == 0 || len(deleted) != 1 || deleted[0] != "docs/unrelated.md" {
		t.Fatalf("unexpected diff: changed=%d deleted=%v", len(changed), deleted)
	}

	ov := BuildOverlay(loaded, changed, deleted, DefaultMaxFileBytes)
	ovRes := SearchCombined(loaded, ov, "QuantumRouter", SearchOpts{K: 5})
	if len(ovRes.Hits) == 0 || ovRes.Hits[0].Path != "new_feature.go" {
		t.Fatalf("expected QuantumRouter from overlay, got %+v", ovRes.Hits)
	}
	delRes := SearchCombined(loaded, ov, "Changelog typos", SearchOpts{K: 5})
	for _, h := range delRes.Hits {
		if h.Path == "docs/unrelated.md" {
			t.Fatalf("deleted file should be masked by overlay")
		}
	}
}

func TestCountFilesAndAutoDisable(t *testing.T) {
	dir := t.TempDir()
	for i := range 10 {
		p := filepath.Join(dir, fmt.Sprintf("f%03d.txt", i))
		if err := os.WriteFile(p, []byte("hi"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if n := CountFiles(dir, SmallProjectFiles); n != 10 {
		t.Fatalf("CountFiles = %d, want 10", n)
	}
	// Small project => NewTool returns nil.
	t.Setenv("PI_DOCSEARCH", "")
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	if tool := NewTool(dir); tool != nil {
		t.Fatalf("expected NewTool to return nil for <= 150 files")
	}
	// NewToolForce still returns a tool.
	if tool := NewToolForce(dir); tool == nil || tool.Name() != "project_search" {
		t.Fatalf("NewToolForce failed")
	}

	// Add 150 more files -> 160 > 150.
	for i := 10; i < 160; i++ {
		p := filepath.Join(dir, fmt.Sprintf("f%03d.txt", i))
		if err := os.WriteFile(p, []byte("hi"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if n := CountFiles(dir, SmallProjectFiles); n != 151 {
		t.Fatalf("CountFiles(limit=150) = %d, want 151", n)
	}
	if tool := NewTool(dir); tool == nil {
		t.Fatalf("expected NewTool non-nil for > 150 files")
	}

	// PI_DOCSEARCH=off disables it.
	t.Setenv("PI_DOCSEARCH", "off")
	if tool := NewTool(dir); tool != nil {
		t.Fatalf("expected NewTool nil when PI_DOCSEARCH=off")
	}
}

func TestEngineAndDocsCommand(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DOCSEARCH_CACHE_DIR", t.TempDir())
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PI_DOCSEARCH", "")

	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc AlphaBeta() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	eng := NewEngine([]string{dir})
	defer eng.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, ok := eng.Search(ctx, "AlphaBeta", SearchOpts{K: 5}, 5*time.Second)
	if !ok || len(res.Hits) == 0 {
		t.Fatalf("Engine.Search failed: ok=%v hits=%v", ok, res.Hits)
	}

	// Cache hit on second call.
	res2, ok2 := eng.Search(ctx, "AlphaBeta", SearchOpts{K: 5}, 5*time.Second)
	if !ok2 || len(res2.Hits) != len(res.Hits) {
		t.Fatalf("cached Engine.Search mismatch")
	}

	// HandleDocsCommand tests.
	if out := HandleDocsCommand("auto off", dir); !strings.Contains(out, "auto-recall off") {
		t.Fatalf("unexpected /docs auto off output: %q", out)
	}
	if out := HandleDocsCommand("auto on", dir); !strings.Contains(out, "auto-recall on") {
		t.Fatalf("unexpected /docs auto on output: %q", out)
	}
	// µPi: the status is `/docs` alone; `/docs status` is a query (index.ts).
	if out := HandleDocsCommand("", dir); !strings.Contains(out, "docsearch:") {
		t.Fatalf("unexpected /docs status output: %q", out)
	}
	if out := HandleDocsCommand("AlphaBeta", dir); !strings.Contains(out, "main.go") {
		t.Fatalf("unexpected /docs query output: %q", out)
	}
	if out := HandleDocsCommand("rebuild", dir); !strings.Contains(out, "rebuilding") {
		t.Fatalf("unexpected /docs rebuild output: %q", out)
	}
}
