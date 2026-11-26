package filetracking

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type recordingChecksHook struct {
	reads  []string
	edited []string
}

func (r *recordingChecksHook) OnRead(path string)   { r.reads = append(r.reads, path) }
func (r *recordingChecksHook) OnEdited(path string) { r.edited = append(r.edited, path) }

func TestFileStateTrackerReadAndModifyGuards(t *testing.T) {
	st := NewFileStateTracker()
	p := filepath.Join(t.TempDir(), "a.go")

	// Unread file fails.
	mtime := 1000.0
	content := "hello\n"
	if err := st.AssertCanModifyExistingFile(p, &mtime, &content); !errors.Is(err, ErrUnreadFile) {
		t.Fatalf("expected ErrUnreadFile, got %v", err)
	}
	if st.HasBeenRead(p) {
		t.Fatal("HasBeenRead should be false before read")
	}

	// Record complete read.
	st.RecordFileRead(p, content, 1000.0, true)
	if !st.HasBeenRead(p) {
		t.Fatal("HasBeenRead should be true after read")
	}

	// Within 10ms rounding tolerance and same content -> allowed.
	mtimeWithin := 1009.0
	if err := st.AssertCanModifyExistingFile(p, &mtimeWithin, &content); err != nil {
		t.Fatalf("expected nil within 10ms tolerance, got %v", err)
	}

	// Newer mtime (>10ms) with identical content -> allowed and updates LastReadMtimeMs.
	mtimeNewer := 2000.0
	if !st.NeedsContentCompare(p, mtimeNewer) {
		t.Fatal("NeedsContentCompare should be true when mtime > lastRead + 10ms and contentComplete")
	}
	if err := st.AssertCanModifyExistingFile(p, &mtimeNewer, &content); err != nil {
		t.Fatalf("expected nil when content unchanged despite newer mtime, got %v", err)
	}
	gotState, ok := st.GetTrackedState(p)
	if !ok || gotState.LastReadMtimeMs != mtimeNewer {
		t.Fatalf("expected LastReadMtimeMs updated to %v, got %+v", mtimeNewer, gotState)
	}

	// Newer mtime (>10ms) with changed content -> ErrStaleFile / *StaleFileError.
	mtimeEvenNewer := 3000.0
	changed := "hello world\n"
	err := st.AssertCanModifyExistingFile(p, &mtimeEvenNewer, &changed)
	if !errors.Is(err, ErrStaleFile) {
		t.Fatalf("expected ErrStaleFile, got %v", err)
	}
	var staleErr *StaleFileError
	if !errors.As(err, &staleErr) {
		t.Fatalf("expected *StaleFileError via errors.As, got %T", err)
	}

	// Sub-millisecond modification (mtime unchanged, content changed) -> ErrStaleFile.
	if err := st.AssertCanModifyExistingFile(p, &mtimeNewer, &changed); !errors.Is(err, ErrStaleFile) {
		t.Fatalf("expected ErrStaleFile on sub-ms content change, got %v", err)
	}

	// Partial read (contentComplete=false) skips content comparison.
	st.RecordFileRead(p, "prefix", 4000.0, false)
	if st.NeedsContentCompare(p, 5000.0) {
		t.Fatal("NeedsContentCompare should be false when contentComplete is false")
	}
	mtimeSame := 4005.0
	if err := st.AssertCanModifyExistingFile(p, &mtimeSame, &changed); err != nil {
		t.Fatalf("partial read with unchanged mtime should allow modify, got %v", err)
	}
	mtimeTooNew := 4020.0
	if err := st.AssertCanModifyExistingFile(p, &mtimeTooNew, &changed); !errors.Is(err, ErrStaleFile) {
		t.Fatalf("partial read with newer mtime should return ErrStaleFile, got %v", err)
	}

	// RecordFileModifiedByAgent marks ModifiedByAgent and ContentComplete.
	st.RecordFileModifiedByAgent(p, changed, 5000.0)
	gotState, ok = st.GetTrackedState(p)
	if !ok || !gotState.ModifiedByAgent || !gotState.ContentComplete || gotState.Content != changed {
		t.Fatalf("unexpected state after RecordFileModifiedByAgent: %+v", gotState)
	}
}

func TestFileStateTrackerCppIncludesAndOverlays(t *testing.T) {
	dir := t.TempDir()
	st := NewFileStateTracker()

	src := filepath.Join(dir, "src", "main.cpp")
	hdr := filepath.Join(dir, "src", "local.h")
	incHdr := filepath.Join(dir, "include", "kernels", "gemm.h")

	st.RecordFileRead(src, "#include \"local.h\"\n#include \"kernels/gemm.h\"\n", 100, true)

	host, ok := st.FindHostSourceForHeader(hdr)
	if !ok || host != src {
		t.Fatalf("FindHostSourceForHeader = (%q, %v), want %q", host, ok, src)
	}

	matches := st.FindSourcesIncludingByPath(incHdr)
	if len(matches) == 0 || matches[0].Source != src || matches[0].Include != "kernels/gemm.h" {
		t.Fatalf("FindSourcesIncludingByPath = %+v", matches)
	}

	// Header only read -> not in unsaved overlays.
	st.RecordFileRead(hdr, "// read only", 100, true)
	if overlays := st.GetUnsavedHeaderOverlays(""); len(overlays) != 0 {
		t.Fatalf("expected 0 overlays for read-only header, got %+v", overlays)
	}

	// Header modified by agent -> in unsaved overlays unless excluded.
	st.RecordFileModifiedByAgent(hdr, "// modified", 200)
	overlays := st.GetUnsavedHeaderOverlays("")
	if len(overlays) != 1 || overlays[0].Filename != hdr {
		t.Fatalf("expected 1 overlay for modified header, got %+v", overlays)
	}
	if excluded := st.GetUnsavedHeaderOverlays(hdr); len(excluded) != 0 {
		t.Fatalf("expected 0 overlays when excluded, got %+v", excluded)
	}
}

func TestNestedInstructionsDiscoveryPriorityAndDeduplication(t *testing.T) {
	ws := t.TempDir()
	sub := filepath.Join(ws, "pkg", "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// Root AGENTS.md should NOT be included (strictly below workspaceCwd).
	if err := os.WriteFile(filepath.Join(ws, "AGENTS.md"), []byte("root instructions"), 0o644); err != nil {
		t.Fatal(err)
	}
	// pkg has both AGENTS.md and AGENTS.override.md -> AGENTS.override.md wins, with BOM stripped.
	pkgOverride := filepath.Join(ws, "pkg", "AGENTS.override.md")
	if err := os.WriteFile(pkgOverride, []byte("\uFEFFpkg override\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "pkg", "AGENTS.md"), []byte("pkg agents"), 0o644); err != nil {
		t.Fatal(err)
	}
	// sub has CLAUDE.md.
	subClaude := filepath.Join(sub, "CLAUDE.md")
	if err := os.WriteFile(subClaude, []byte("sub claude\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(sub, "file.go")
	if err := os.WriteFile(target, []byte("package sub\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ni := NewNestedInstructions()

	// Self-read of sub/CLAUDE.md marks it seen without returning it, but still returns pkg/AGENTS.override.md.
	selfFiles := ni.Discover(subClaude, ws)
	if len(selfFiles) != 1 || selfFiles[0].Path != pkgOverride || selfFiles[0].Content != "pkg override\n" {
		t.Fatalf("self-read Discover = %+v", selfFiles)
	}

	// Reading target in sub now sees both already sent -> returns empty.
	if again := ni.Discover(target, ws); len(again) != 0 {
		t.Fatalf("expected 0 after deduplication, got %+v", again)
	}

	// Fresh NestedInstructions discovers root-first: pkg/AGENTS.override.md then pkg/sub/CLAUDE.md.
	ni2 := NewNestedInstructions()
	files := ni2.Discover(target, ws)
	if len(files) != 2 || files[0].Path != pkgOverride || files[1].Path != subClaude {
		t.Fatalf("root-first Discover = %+v", files)
	}
	reminder := FormatNestedInstructionsReminder(files)
	if !strings.Contains(reminder, "<system-reminder>") || !strings.Contains(reminder, "pkg override") || !strings.Contains(reminder, "sub claude") {
		t.Fatalf("unexpected reminder: %q", reminder)
	}
	if strings.Contains(reminder, "root instructions") {
		t.Fatalf("workspace root AGENTS.md must not be included: %q", reminder)
	}
}

func TestTrackerCoordinationAndRemoteMode(t *testing.T) {
	ws := t.TempDir()
	sub := filepath.Join(ws, "a")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "AGENTS.md"), []byte("nested rule"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(sub, "foo.go")

	hook := &recordingChecksHook{}
	tr := NewTracker()
	tr.SetBackgroundChecks(hook)

	reminder := tr.OnFileRead(target, "line1\nline2\n", 100, ws, nil, true)
	if !strings.Contains(reminder, "nested rule") {
		t.Fatalf("expected nested reminder, got %q", reminder)
	}
	if len(hook.reads) != 1 || hook.reads[0] != target {
		t.Fatalf("expected checks OnRead, got %+v", hook.reads)
	}

	// Remote tracker disables nested instructions.
	remote := NewTracker(WithLocal(false))
	if r := remote.OnFileRead(target, "line1\n", 100, ws, nil, true); r != "" {
		t.Fatalf("remote tracker should return empty reminder, got %q", r)
	}

	// OnFileModified with snippet.
	snippet := tr.OnFileModified(FileModifiedParams{
		AbsolutePath:           target,
		NewContent:             "l1\nl2\nl3\nl4\nl5\nl6\nl7\n",
		NewMtimeMs:             200,
		FirstChangedLine:       4,
		IncludePostEditSnippet: true,
	})
	if len(hook.edited) != 1 || hook.edited[0] != target {
		t.Fatalf("expected checks OnEdited, got %+v", hook.edited)
	}
	if !strings.Contains(snippet, "Here is a snippet of the edited file around line 4:") || !strings.Contains(snippet, "     4→l4") {
		t.Fatalf("unexpected snippet: %q", snippet)
	}
}
