package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func TestNativeSearchTreeAndContentCache(t *testing.T) {
	ClearNativeSearchCache()
	defer ClearNativeSearchCache()

	dir := t.TempDir()
	f1 := filepath.Join(dir, "alpha.txt")
	if err := os.WriteFile(f1, []byte("hello cache\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	tree1, err := getCachedTree(ctx, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree1.entries) != 1 || tree1.entries[0].rel != "alpha.txt" {
		t.Fatalf("unexpected tree1 entries: %+v", tree1.entries)
	}

	// Second call within 2s TTL returns same cached pointer.
	tree2, err := getCachedTree(ctx, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if tree1 != tree2 {
		t.Fatalf("expected tree cache hit within 2s TTL")
	}

	// Touching an existing file's content updates contentCache without dropping tree.
	NotifySearchFileTouched(f1, []byte("updated cache\n"), time.Now())
	tree3, err := getCachedTree(ctx, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if tree3 != tree1 {
		t.Fatalf("expected tree cache preserved for known file")
	}

	// Adding a new non-ignored file invalidates the tree cache.
	f2 := filepath.Join(dir, "beta.txt")
	if err := os.WriteFile(f2, []byte("new file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	NotifySearchFileTouched(f2, []byte("new file\n"), time.Now())
	tree4, err := getCachedTree(ctx, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if tree4 == tree1 || len(tree4.entries) != 2 {
		t.Fatalf("expected tree cache invalidated for new file, got %d entries", len(tree4.entries))
	}

	// µPi: InvalidateSearchCache (a command ran) makes the next lookup revalidate the tree: an unchanged tree is kept, one whose folder
	// changed is walked again.
	InvalidateSearchCache(dir)
	tree5, err := getCachedTree(ctx, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if tree5 != tree4 {
		t.Fatalf("expected InvalidateSearchCache to keep an unchanged tree")
	}
	time.Sleep(20 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(dir, "gamma.txt"), []byte("by a command\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(dir, later, later)
	InvalidateSearchCache(dir)
	tree6, err := getCachedTree(ctx, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if tree6 == tree5 || len(tree6.entries) != 3 {
		t.Fatalf("expected a walk after a command added a file, got %d entries", len(tree6.entries))
	}
}

func TestContentCacheEvictionAndBinary(t *testing.T) {
	ClearNativeSearchCache()
	defer ClearNativeSearchCache()

	// Binary file stores nil buffer and 0 bytes.
	binPath := filepath.Join(t.TempDir(), "bin.dat")
	NotifySearchFileTouched(binPath, []byte{1, 2, 0, 3}, time.Now())
	searchCacheMu.Lock()
	if contentCacheSize != 0 {
		searchCacheMu.Unlock()
		t.Fatalf("binary file should add 0 bytes to contentCacheSize, got %d", contentCacheSize)
	}
	elem, ok := contentCacheMap[normalizeCacheKey(binPath)]
	if !ok || elem.Value.(*cachedFileBuffer).buffer != nil {
		searchCacheMu.Unlock()
		t.Fatalf("expected nil buffer stored for binary file")
	}
	searchCacheMu.Unlock()

	// Oversized file (> 512 KB) is not cached.
	bigPath := filepath.Join(t.TempDir(), "big.txt")
	bigData := make([]byte, maxCachedFileBytes+10)
	for i := range bigData {
		bigData[i] = 'a'
	}
	NotifySearchFileTouched(bigPath, bigData, time.Now())
	searchCacheMu.Lock()
	if _, ok := contentCacheMap[normalizeCacheKey(bigPath)]; ok {
		searchCacheMu.Unlock()
		t.Fatalf("oversized file should not be cached")
	}
	searchCacheMu.Unlock()

	// Eviction when exceeding 64 MB pool.
	chunk := make([]byte, 500*1024)
	for i := range chunk {
		chunk[i] = 'x'
	}
	baseDir := t.TempDir()
	for i := range 140 { // 140 * 500 KB = ~70 MB > 64 MB
		p := filepath.Join(baseDir, fmt.Sprintf("f%03d.txt", i))
		NotifySearchFileTouched(p, chunk, time.Now())
	}
	searchCacheMu.Lock()
	sz := contentCacheSize
	searchCacheMu.Unlock()
	if sz > maxContentCacheBytes {
		t.Fatalf("contentCacheSize %d exceeded cap %d", sz, maxContentCacheBytes)
	}
}

func TestNativeGrepModesContextGlobOverrideAndLargeFile(t *testing.T) {
	ClearNativeSearchCache()
	defer ClearNativeSearchCache()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("ignored.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "code.go"), []byte("line1\nfunc TargetFunc() {}\nline3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ignored.log"), []byte("TargetFunc in ignored log\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &GrepTool{CWD: dir}
	ctx := context.Background()

	// Literal + context=1.
	raw, _ := json.Marshal(map[string]any{
		"pattern": "TargetFunc",
		"literal": true,
		"context": 1,
	})
	res, err := tool.Execute(ctx, "c1", raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := res.Content[0].(ai.TextContent).Text
	if !strings.Contains(out, "code.go:2: func TargetFunc() {}") ||
		!strings.Contains(out, "code.go-1- line1") ||
		!strings.Contains(out, "code.go-3- line3") {
		t.Fatalf("unexpected grep context output:\n%s", out)
	}
	if strings.Contains(out, "ignored.log") {
		t.Fatalf("ignored.log should not appear without glob override:\n%s", out)
	}

	// Regex + ignoreCase + glob override on ignored.log.
	raw2, _ := json.Marshal(map[string]any{
		"pattern":    "target[a-z]+",
		"ignoreCase": true,
		"glob":       "*.log",
	})
	res2, err := tool.Execute(ctx, "c2", raw2, nil)
	if err != nil {
		t.Fatal(err)
	}
	out2 := res2.Content[0].(ai.TextContent).Text
	if !strings.Contains(out2, "ignored.log:1: TargetFunc in ignored log") {
		t.Fatalf("expected glob override to match ignored.log, got:\n%s", out2)
	}

	// Large file (> 2 MB) streaming via searchInPieces.
	largePath := filepath.Join(dir, "huge.txt")
	var lb strings.Builder
	for i := range 50000 {
		fmt.Fprintf(&lb, "padding line number %06d with enough bytes to exceed two megabytes\n", i)
	}
	lb.WriteString("NEEDLE_AT_END\n")
	if err := os.WriteFile(largePath, []byte(lb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	InvalidateSearchCache(dir)

	raw3, _ := json.Marshal(map[string]any{
		"pattern": "NEEDLE_AT_END",
		"literal": true,
	})
	res3, err := tool.Execute(ctx, "c3", raw3, nil)
	if err != nil {
		t.Fatal(err)
	}
	out3 := res3.Content[0].(ai.TextContent).Text
	if !strings.Contains(out3, "huge.txt:50001: NEEDLE_AT_END") {
		t.Fatalf("expected huge.txt line 50001 match, got:\n%s", out3)
	}
}

func TestNativeFindAndFuzzyFallback(t *testing.T) {
	ClearNativeSearchCache()
	defer ClearNativeSearchCache()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src", "auth"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.tmp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "auth", "token_manager.go"), []byte("package auth\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".hidden.env"), []byte("SECRET=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skip.tmp"), []byte("tmp\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &FindTool{CWD: dir}
	ctx := context.Background()

	// Hidden file included, .gitignore respected.
	raw, _ := json.Marshal(map[string]any{"pattern": "*"})
	res, err := tool.Execute(ctx, "f1", raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := res.Content[0].(ai.TextContent).Text
	if !strings.Contains(out, ".hidden.env") || strings.Contains(out, "skip.tmp") {
		t.Fatalf("unexpected find output:\n%s", out)
	}

	// Fuzzy fallback when glob doesn't match literally.
	rawFuzzy, _ := json.Marshal(map[string]any{"pattern": "tknmgr"})
	resFuzzy, err := tool.Execute(ctx, "f2", rawFuzzy, nil)
	if err != nil {
		t.Fatal(err)
	}
	outFuzzy := resFuzzy.Content[0].(ai.TextContent).Text
	if !strings.Contains(outFuzzy, "No files match the pattern. The closest paths by name") ||
		!strings.Contains(outFuzzy, "src/auth/token_manager.go") {
		t.Fatalf("expected fuzzy fallback match for tknmgr, got:\n%s", outFuzzy)
	}
	fd, ok := resFuzzy.Details.(*FindDetails)
	if !ok || !fd.FuzzyFallback {
		t.Fatalf("expected FindDetails.FuzzyFallback = true, got %#v", resFuzzy.Details)
	}
}

func TestProjectSearchToolContract(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DOCSEARCH_CACHE_DIR", t.TempDir())
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PI_DOCSEARCH", "")

	if err := os.WriteFile(filepath.Join(dir, "router.go"), []byte("package main\n\n// HandleDispatch routes incoming requests.\nfunc HandleDispatch(req string) string {\n\treturn req\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := CreateProjectSearchTool(dir, nil)
	if tool.Name() != "project_search" {
		t.Fatalf("Name() = %q", tool.Name())
	}
	if tool.ExecutionMode() != agent.ToolModeParallel {
		t.Fatalf("ExecutionMode() = %v", tool.ExecutionMode())
	}
	if len(tool.OutputSchema()) == 0 {
		t.Fatalf("OutputSchema() empty")
	}
	_ = tool.BorrowedSchema()

	raw, _ := json.Marshal(map[string]any{"query": "HandleDispatch", "k": 3})
	res, err := tool.Execute(context.Background(), "ps1", raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := res.Content[0].(ai.TextContent).Text
	if !strings.Contains(out, "router.go:") || !strings.Contains(out, "HandleDispatch") {
		t.Fatalf("unexpected project_search output:\n%s", out)
	}
	var sc struct {
		Hits []struct {
			Path string `json:"path"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(res.StructuredContent, &sc); err != nil || len(sc.Hits) == 0 {
		t.Fatalf("unexpected structuredContent: %s (%v)", string(res.StructuredContent), err)
	}
}
