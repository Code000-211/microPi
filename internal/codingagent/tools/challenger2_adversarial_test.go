package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

func extractText(res agentToolTextGetter) string {
	var parts []string
	for _, c := range res {
		if tc, ok := c.(ai.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

type agentToolTextGetter = []ai.ToolResultMessageContent

// ─── 1. Tree Cache 2s TTL + Mtime Revalidation + Invalidation APIs ───────────

func TestChallenger2_TreeCacheTTLAndInvalidationAPIs(t *testing.T) {
	ClearNativeSearchCache()
	defer ClearNativeSearchCache()

	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	sibling := t.TempDir()
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "s.txt"), []byte("sub\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sibling, "sib.txt"), []byte("sib\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ft := &FindTool{CWD: root}
	ctx := context.Background()

	// Warm tree cache for root.
	raw, _ := json.Marshal(findParams{Pattern: "*.txt"})
	res, err := ft.Execute(ctx, "1", raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	txt := extractText(res.Content)
	if !strings.Contains(txt, "a.txt") || !strings.Contains(txt, "sub/s.txt") {
		t.Fatalf("initial find missing files: %q", txt)
	}

	// Create b.txt without invalidating. Within 2s TTL, cache should serve stale tree.
	bPath := filepath.Join(root, "b.txt")
	if err := os.WriteFile(bPath, []byte("beta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res2, err := ft.Execute(ctx, "2", raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	txt2 := extractText(res2.Content)
	if strings.Contains(txt2, "b.txt") {
		t.Logf("NOTE: b.txt appeared immediately within 2s TTL (unexpected if cached, got: %q)", txt2)
	}

	// Wait past 2s TTL -> mtime revalidation must detect root dir mtime change and pick up b.txt.
	time.Sleep(2100 * time.Millisecond)
	res3, err := ft.Execute(ctx, "3", raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	txt3 := extractText(res3.Content)
	if !strings.Contains(txt3, "b.txt") {
		t.Fatalf("after 2s TTL + dir mtime change, expected b.txt, got: %q", txt3)
	}

	// Test NotifySearchExternalChange: create c.txt and notify, should be visible immediately (< 2s).
	// Ensure dir mtime actually advances (some filesystems have coarse granularity, though Linux ext4/tmpfs is ns).
	time.Sleep(10 * time.Millisecond)
	cPath := filepath.Join(root, "c.txt")
	if err := os.WriteFile(cPath, []byte("gamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	NotifySearchExternalChange()
	res4, err := ft.Execute(ctx, "4", raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	txt4 := extractText(res4.Content)
	if !strings.Contains(txt4, "c.txt") {
		t.Fatalf("after NotifySearchExternalChange, expected c.txt, got: %q", txt4)
	}

	// Test InvalidateSearchCache(sub) invalidates sub and parent root, but NOT sibling.
	ftSub := &FindTool{CWD: sub}
	ftSib := &FindTool{CWD: sibling}
	_, _ = ftSub.Execute(ctx, "5", raw, nil)
	_, _ = ftSib.Execute(ctx, "6", raw, nil)

	searchCacheMu.Lock()
	beforeCount := len(treeCache)
	searchCacheMu.Unlock()
	if beforeCount < 3 {
		t.Fatalf("expected >= 3 cached trees, got %d", beforeCount)
	}

	InvalidateSearchCache(sub)

	// µPi: a command no longer drops the cached trees; each is revalidated on its next lookup (native_search_revalidate_test.go covers
	// what a search then sees).
	searchCacheMu.Lock()
	_, hasRoot := treeCache[normalizeCacheKey(root)+"|0"]
	_, hasSub := treeCache[normalizeCacheKey(sub)+"|0"]
	_, hasSib := treeCache[normalizeCacheKey(sibling)+"|0"]
	searchCacheMu.Unlock()

	if !hasRoot || !hasSub || !hasSib {
		t.Fatalf("InvalidateSearchCache(sub) should keep the trees for revalidation, got hasRoot=%v hasSub=%v hasSib=%v", hasRoot, hasSub, hasSib)
	}

	// Test NotifySearchFileTouched:
	// 1. Warm root again.
	_, _ = ft.Execute(ctx, "7", raw, nil)
	// 2. Touch a new unignored file -> invalidates tree + populates content cache.
	dPath := filepath.Join(root, "d.txt")
	dContent := []byte("delta_touched\n")
	if err := os.WriteFile(dPath, dContent, 0o644); err != nil {
		t.Fatal(err)
	}
	NotifySearchFileTouched(dPath, dContent, time.Now())

	res5, err := ft.Execute(ctx, "8", raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(extractText(res5.Content), "d.txt") {
		t.Fatalf("after NotifySearchFileTouched for new file, expected d.txt in find results, got: %q", extractText(res5.Content))
	}

	// 3. Touch an ignored dir file -> should NOT invalidate tree cache.
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored_dir/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	igDir := filepath.Join(root, "ignored_dir")
	_ = os.MkdirAll(igDir, 0o755)
	InvalidateSearchCache(root)
	_, _ = ft.Execute(ctx, "9", raw, nil) // warm with ignored_dir in ignoredDirKeys

	igFile := filepath.Join(igDir, "x.txt")
	_ = os.WriteFile(igFile, []byte("x\n"), 0o644)
	searchCacheMu.Lock()
	treeBefore := treeCache[normalizeCacheKey(root)+"|0"]
	searchCacheMu.Unlock()

	NotifySearchFileTouched(igFile, []byte("x\n"), time.Now())

	searchCacheMu.Lock()
	treeAfter := treeCache[normalizeCacheKey(root)+"|0"]
	searchCacheMu.Unlock()
	if treeBefore == nil || treeAfter != treeBefore {
		t.Fatalf("NotifySearchFileTouched on file in ignored dir should not invalidate tree cache")
	}
}

// ─── 2. Content Cache Caps, Binary NUL Detection, >2MB Piece Streaming ───────

func TestChallenger2_ContentCacheCapsAndBinaryAndPieceStreaming(t *testing.T) {
	ClearNativeSearchCache()
	defer ClearNativeSearchCache()

	// 2a. 64 MB cap eviction + 512 KB per-file cap + binary nil cache entry
	dir := t.TempDir()

	// Create 140 files of 500 KB each (~70 MB > 64 MB cap)
	const fileSize = 500 * 1024
	payload := bytes.Repeat([]byte("a"), fileSize-10)
	payload = append(payload, []byte("NEEDLE_64M\n")...)

	for i := 0; i < 140; i++ {
		p := filepath.Join(dir, fmt.Sprintf("f_%03d.txt", i))
		if err := os.WriteFile(p, payload, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Also create exact 512 KB and 512 KB + 1 byte files
	exact512 := bytes.Repeat([]byte("b"), maxCachedFileBytes-len("NEEDLE_512\n"))
	exact512 = append(exact512, []byte("NEEDLE_512\n")...)
	exact512Path := filepath.Join(dir, "exact_512k.txt")
	if err := os.WriteFile(exact512Path, exact512, 0o644); err != nil {
		t.Fatal(err)
	}

	over512 := append([]byte("x"), exact512...)
	over512Path := filepath.Join(dir, "over_512k.txt")
	if err := os.WriteFile(over512Path, over512, 0o644); err != nil {
		t.Fatal(err)
	}

	// Small binary file with NUL byte
	binPath := filepath.Join(dir, "bin_small.dat")
	if err := os.WriteFile(binPath, []byte("NEEDLE_BIN\x00rest"), 0o644); err != nil {
		t.Fatal(err)
	}

	gt := &GrepTool{CWD: dir}
	ctx := context.Background()
	limit := 200.0
	raw, _ := json.Marshal(grepParams{Pattern: "NEEDLE_", Literal: true, Limit: &limit})
	res, err := gt.Execute(ctx, "1", raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	txt := extractText(res.Content)
	if strings.Contains(txt, "bin_small.dat") {
		t.Fatalf("binary file with NUL byte should not match: %q", txt)
	}
	// Every match line is ~500 KB, so the 50 KB output limit cuts this listing short: the 64 MB walk only fills the
	// cache. What matched where is asked below, with a search whose output fits.
	if !strings.Contains(txt, "exact_512k.txt") {
		t.Fatalf("expected exact_512k.txt (the first text file in path order) to match, got: %q", txt)
	}

	searchCacheMu.Lock()
	cacheSz := contentCacheSize
	// A walk larger than the cache keeps what it cached first (entries used within contentCacheKeepRecent are not evicted), so the
	// next search finds those files cached instead of none: f_000.txt stays, f_139.txt did not fit.
	_, hasFirst := contentCacheMap[normalizeCacheKey(filepath.Join(dir, "f_000.txt"))]
	_, hasLast := contentCacheMap[normalizeCacheKey(filepath.Join(dir, "f_139.txt"))]
	searchCacheMu.Unlock()
	if cacheSz > maxContentCacheBytes {
		t.Fatalf("contentCacheSize %d exceeds maxContentCacheBytes %d", cacheSz, maxContentCacheBytes)
	}
	if !hasFirst {
		t.Fatalf("f_000.txt, cached first in the walk, should still be cached")
	}
	if hasLast {
		t.Fatalf("f_139.txt should not have evicted a recently used entry")
	}

	// A search of the three small files, after the walk: the 512 KB cap per file and the binary entry.
	limited, _ := json.Marshal(grepParams{Pattern: "NEEDLE_(512|BIN)", Glob: "{exact_512k.txt,over_512k.txt,bin_small.dat}"})
	res, err = gt.Execute(ctx, "2", limited, nil)
	if err != nil {
		t.Fatal(err)
	}
	txt = extractText(res.Content)
	if strings.Contains(txt, "bin_small.dat") {
		t.Fatalf("binary file with NUL byte should not match: %q", txt)
	}
	if !strings.Contains(txt, "exact_512k.txt") || !strings.Contains(txt, "over_512k.txt") {
		t.Fatalf("expected both exact_512k.txt and over_512k.txt to match, got: %q", txt)
	}

	searchCacheMu.Lock()
	_, hasExact := contentCacheMap[normalizeCacheKey(exact512Path)]
	_, hasOver := contentCacheMap[normalizeCacheKey(over512Path)]
	binElem, hasBin := contentCacheMap[normalizeCacheKey(binPath)]
	var binBufNil bool
	if hasBin {
		binBufNil = binElem.Value.(*cachedFileBuffer).buffer == nil
	}
	searchCacheMu.Unlock()

	if !hasExact {
		t.Fatalf("exact 512KB file should be cached")
	}
	if hasOver {
		t.Fatalf("512KB+1 file should NOT be cached")
	}
	if !hasBin || !binBufNil {
		t.Fatalf("small binary file should have nil buffer cache entry (hasBin=%v, binBufNil=%v)", hasBin, binBufNil)
	}
}

func TestChallenger2_LargeFilePieceStreamingBoundariesAndContext(t *testing.T) {
	ClearNativeSearchCache()
	defer ClearNativeSearchCache()

	dir := t.TempDir()
	largePath := filepath.Join(dir, "huge.txt")

	// Build a ~2.5 MB file with known line numbers and patterns straddling 1 MB and 2 MB chunk boundaries.
	// Use 100-byte lines: "0000000...000\n" (99 'x' + '\n' = 100 bytes).
	// Byte 1048576 (1 MB) falls inside line 10486 (0-indexed line 10485: bytes 1048500..1048599).
	// Byte 2097152 (2 MB) falls inside line 20972 (0-indexed line 20971: bytes 2097100..2097199).
	var buf bytes.Buffer
	line1M := 10485 // 0-indexed -> 1-indexed line 10486
	line2M := 20971 // 0-indexed -> 1-indexed line 20972
	totalLines := 25000

	for i := 0; i < totalLines; i++ {
		if i == line1M {
			// Put pattern right across byte 1048576 (offset 76 in this line)
			// Line starts at 1048500. Offset 70..88 crosses 1048576!
			l := strings.Repeat("a", 70) + "STRADDLE_1MB_MATCH" + strings.Repeat("a", 11) + "\n"
			if len(l) != 100 {
				t.Fatalf("bad len %d", len(l))
			}
			buf.WriteString(l)
		} else if i == line2M {
			// Line starts at 2097100. Byte 2097152 is offset 52.
			l := strings.Repeat("b", 44) + "STRADDLE_2MB_MATCH" + strings.Repeat("b", 37) + "\n"
			if len(l) != 100 {
				t.Fatalf("bad len %d", len(l))
			}
			buf.WriteString(l)
		} else if i == totalLines-1 {
			buf.WriteString(strings.Repeat("c", 85) + "END_MATCH_LINE\n")
		} else {
			buf.WriteString(strings.Repeat("z", 99) + "\n")
		}
	}
	if err := os.WriteFile(largePath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	gt := &GrepTool{CWD: dir}
	ctx := context.Background()

	// Test straddling matches & line numbers
	raw, _ := json.Marshal(grepParams{Pattern: "STRADDLE_.*_MATCH|END_MATCH_LINE"})
	res, err := gt.Execute(ctx, "1", raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	txt := extractText(res.Content)
	want1 := fmt.Sprintf("huge.txt:%d:", line1M+1)
	want2 := fmt.Sprintf("huge.txt:%d:", line2M+1)
	want3 := fmt.Sprintf("huge.txt:%d:", totalLines)
	if !strings.Contains(txt, want1) || !strings.Contains(txt, want2) || !strings.Contains(txt, want3) {
		t.Fatalf("large file piece streaming missed or misnumbered matches:\ngot:\n%s\nwant substrings: %s, %s, %s", txt, want1, want2, want3)
	}

	// Test context on >2MB file!
	ctxVal := 2.0
	rawCtx, _ := json.Marshal(grepParams{Pattern: "STRADDLE_1MB_MATCH", Literal: true, Context: &ctxVal})
	resCtx, err := gt.Execute(ctx, "2", rawCtx, nil)
	if err != nil {
		t.Fatal(err)
	}
	txtCtx := extractText(resCtx.Content)
	wantBefore := fmt.Sprintf("huge.txt-%d-", line1M)
	wantAfter := fmt.Sprintf("huge.txt-%d-", line1M+2)
	if !strings.Contains(txtCtx, wantBefore) || !strings.Contains(txtCtx, wantAfter) {
		t.Errorf("BUG FOUND: >2MB file piece-streaming ignores Context=%v! got:\n%s", ctxVal, txtCtx)
	}
}

func TestChallenger2_LargeBinaryNULInSecondChunk(t *testing.T) {
	ClearNativeSearchCache()
	defer ClearNativeSearchCache()

	dir := t.TempDir()
	p := filepath.Join(dir, "late_nul.bin")
	// 2.2 MB file: match at byte 10, NUL byte at 1.5 MB
	data := bytes.Repeat([]byte("a"), 2_300_000)
	copy(data[0:], "EARLY_MATCH\n")
	data[1_500_000] = 0
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}

	gt := &GrepTool{CWD: dir}
	raw, _ := json.Marshal(grepParams{Pattern: "EARLY_MATCH", Literal: true})
	res, err := gt.Execute(context.Background(), "1", raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	txt := extractText(res.Content)
	if strings.Contains(txt, "EARLY_MATCH") {
		t.Errorf("BUG FOUND: >2MB binary file with NUL byte in chunk 2 still emitted match from chunk 1: %q", txt)
	}
}

// ─── 3. .gitignore, .ignore, Nested .git Boundary, grep --glob Override ──────

func TestChallenger2_IgnoreRulesNestedGitAndGlobOverride(t *testing.T) {
	ClearNativeSearchCache()
	defer ClearNativeSearchCache()

	root := t.TempDir()
	// Root git repo
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("*.log\nignored_dir/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ignore"), []byte("*.tmp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("MARKER\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "root.log"), []byte("MARKER_LOG\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "root.tmp"), []byte("MARKER_TMP\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Ignored dir with a .log file inside
	if err := os.MkdirAll(filepath.Join(root, "ignored_dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ignored_dir", "inside.log"), []byte("MARKER_INSIDE_DIR\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Nested git repo: should NOT inherit parent's .gitignore (*.log), SHOULD inherit .ignore (*.tmp)
	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(filepath.Join(nested, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "sub.log"), []byte("MARKER_NESTED_LOG\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "sub.tmp"), []byte("MARKER_NESTED_TMP\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	gt := &GrepTool{CWD: root}
	ft := &FindTool{CWD: root}

	// 1. Find with "*" should see main.go and nested/sub.log, NOT root.log, root.tmp, nested/sub.tmp, or ignored_dir/
	fRaw, _ := json.Marshal(findParams{Pattern: "*"})
	fRes, err := ft.Execute(ctx, "1", fRaw, nil)
	if err != nil {
		t.Fatal(err)
	}
	fTxt := extractText(fRes.Content)
	if !strings.Contains(fTxt, "main.go") || !strings.Contains(fTxt, "nested/sub.log") {
		t.Fatalf("find missing expected files (nested .git boundary): %q", fTxt)
	}
	if strings.Contains(fTxt, "root.log") || strings.Contains(fTxt, "root.tmp") || strings.Contains(fTxt, "sub.tmp") || strings.Contains(fTxt, "inside.log") {
		t.Fatalf("find included ignored files: %q", fTxt)
	}

	// 2. Grep without glob should NOT see root.log
	gRaw1, _ := json.Marshal(grepParams{Pattern: "MARKER"})
	gRes1, _ := gt.Execute(ctx, "2", gRaw1, nil)
	gTxt1 := extractText(gRes1.Content)
	if strings.Contains(gTxt1, "root.log") || strings.Contains(gTxt1, "root.tmp") {
		t.Fatalf("grep without glob matched ignored file: %q", gTxt1)
	}
	if !strings.Contains(gTxt1, "nested/sub.log") {
		t.Fatalf("grep without glob missed nested .git repo file: %q", gTxt1)
	}

	// 3. Grep with glob="*.log" SHOULD override .gitignore for root.log
	gRaw2, _ := json.Marshal(grepParams{Pattern: "MARKER", Glob: "*.log"})
	gRes2, _ := gt.Execute(ctx, "3", gRaw2, nil)
	gTxt2 := extractText(gRes2.Content)
	if !strings.Contains(gTxt2, "root.log") {
		t.Fatalf("grep --glob '*.log' failed to override .gitignore for root.log: %q", gTxt2)
	}
	if !strings.Contains(gTxt2, "ignored_dir/inside.log") {
		t.Logf("NOTE: grep --glob '*.log' did NOT descend into ignored_dir/ (ignored_dir/inside.log not matched): %q", gTxt2)
	}
}

// ─── 4. Find 0 Matches -> fff Fuzzy Fallback + Empty/Wildcard Patterns ───────

func TestChallenger2_FindFuzzyFallbackAndEmptyWildcard(t *testing.T) {
	ClearNativeSearchCache()
	defer ClearNativeSearchCache()

	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	// Create 25 files matching "usrctrl"
	for i := 1; i <= 25; i++ {
		p := filepath.Join(src, fmt.Sprintf("user_controller_%02d.go", i))
		if err := os.WriteFile(p, []byte("package src\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ft := &FindTool{CWD: dir}
	ctx := context.Background()

	// 4a. 0 glob matches -> fuzzy fallback
	raw, _ := json.Marshal(findParams{Pattern: "usrctrl"})
	res, err := ft.Execute(ctx, "1", raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	txt := extractText(res.Content)
	wantPrefix := "No files match the pattern. The closest paths by name (not matches of the pattern):\n"
	if !strings.HasPrefix(txt, wantPrefix) {
		t.Fatalf("fuzzy fallback missing exact notice prefix.\ngot: %q\nwant prefix: %q", txt, wantPrefix)
	}
	d, ok := res.Details.(*FindDetails)
	if !ok || d == nil || !d.FuzzyFallback {
		t.Fatalf("expected Details.FuzzyFallback == true, got %#v", res.Details)
	}
	rest := strings.TrimPrefix(txt, wantPrefix)
	lines := strings.Split(strings.TrimSpace(rest), "\n")
	if len(lines) > 20 || len(lines) == 0 {
		t.Fatalf("expected 1..20 fuzzy results, got %d: %v", len(lines), lines)
	}

	// 4b. Empty pattern ""
	rawEmpty, _ := json.Marshal(findParams{Pattern: ""})
	resEmpty, err := ft.Execute(ctx, "2", rawEmpty, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := extractText(resEmpty.Content); got != "No files found matching pattern" {
		t.Fatalf("empty pattern: got %q, want %q", got, "No files found matching pattern")
	}

	// 4c. Wildcard-only patterns that match 0 files (e.g. "????", "*/*/*/*/*")
	for _, pat := range []string{"????", "*/*/*/*/*"} {
		rawW, _ := json.Marshal(findParams{Pattern: pat})
		resW, err := ft.Execute(ctx, "3", rawW, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := extractText(resW.Content); got != "No files found matching pattern" {
			t.Fatalf("wildcard pattern %q: got %q, want %q", pat, got, "No files found matching pattern")
		}
	}

	// 4d. Wildcard-only "*" and "***" match files normally
	for _, pat := range []string{"*", "***"} {
		rawW, _ := json.Marshal(findParams{Pattern: pat})
		resW, err := ft.Execute(ctx, "4", rawW, nil)
		if err != nil {
			t.Fatal(err)
		}
		if d, ok := resW.Details.(*FindDetails); ok && d != nil && d.FuzzyFallback {
			t.Fatalf("wildcard pattern %q unexpectedly triggered fuzzy fallback", pat)
		}
		if !strings.Contains(extractText(resW.Content), "user_controller_01.go") {
			t.Fatalf("wildcard pattern %q did not match expected files: %q", pat, extractText(resW.Content))
		}
	}
}

// ─── 5. Concurrent Grep/Find + Context Cancellation ──────────────────────────

func TestChallenger2_ConcurrencyAndContextCancellation(t *testing.T) {
	ClearNativeSearchCache()
	defer ClearNativeSearchCache()

	dir := t.TempDir()
	for i := 0; i < 30; i++ {
		p := filepath.Join(dir, fmt.Sprintf("file_%02d.go", i))
		if err := os.WriteFile(p, []byte("package p\nfunc TargetFunc() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	gt := &GrepTool{CWD: dir}
	ft := &FindTool{CWD: dir}

	var wg sync.WaitGroup
	var errCount atomic.Int32
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ctx := context.Background()
			for iter := 0; iter < 15; iter++ {
				switch (id + iter) % 5 {
				case 0:
					raw, _ := json.Marshal(grepParams{Pattern: "TargetFunc"})
					res, err := gt.Execute(ctx, "g", raw, nil)
					if err != nil || res.IsError {
						errCount.Add(1)
					}
				case 1:
					raw, _ := json.Marshal(findParams{Pattern: "*.go"})
					res, err := ft.Execute(ctx, "f", raw, nil)
					if err != nil || res.IsError {
						errCount.Add(1)
					}
				case 2:
					p := filepath.Join(dir, "file_00.go")
					NotifySearchFileTouched(p, []byte("package p\nfunc TargetFunc() {}\n"), time.Now())
				case 3:
					NotifySearchExternalChange()
				case 4:
					if iter%3 == 0 {
						InvalidateSearchCache(dir)
					}
				}
			}
		}(g)
	}
	wg.Wait()
	if errCount.Load() > 0 {
		t.Fatalf("concurrent grep/find had %d errors", errCount.Load())
	}

	// Pre-canceled context
	cCtx, cancel := context.WithCancel(context.Background())
	cancel()
	gRaw, _ := json.Marshal(grepParams{Pattern: "TargetFunc"})
	gRes, err := gt.Execute(cCtx, "c1", gRaw, nil)
	if err != nil || !gRes.IsError || extractText(gRes.Content) != "Operation aborted" {
		t.Fatalf("pre-canceled grep: err=%v res=%#v", err, gRes)
	}
	fRaw, _ := json.Marshal(findParams{Pattern: "*.go"})
	fRes, err := ft.Execute(cCtx, "c2", fRaw, nil)
	if err != nil || !fRes.IsError || extractText(fRes.Content) != "Operation aborted" {
		t.Fatalf("pre-canceled find: err=%v res=%#v", err, fRes)
	}

	// Mid-search cancellation via Operations.ReadFile hook on grep
	midCtx, midCancel := context.WithCancel(context.Background())
	var reads atomic.Int32
	gtMid := &GrepTool{
		CWD: dir,
		Operations: &GrepOperations{
			IsDirectory: func(p string) (bool, error) {
				fi, err := os.Stat(p)
				if err != nil {
					return false, err
				}
				return fi.IsDir(), nil
			},
			ReadFile: func(p string) (string, error) {
				if reads.Add(1) == 2 {
					midCancel()
				}
				b, err := os.ReadFile(p)
				return string(b), err
			},
		},
	}
	gMidRes, err := gtMid.Execute(midCtx, "c3", gRaw, nil)
	if err != nil || !gMidRes.IsError || extractText(gMidRes.Content) != "Operation aborted" {
		t.Fatalf("mid-search canceled grep: err=%v res=%#v", err, gMidRes)
	}
}
