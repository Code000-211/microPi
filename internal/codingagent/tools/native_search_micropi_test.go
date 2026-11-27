package tools

import (
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"
)

// µPi: native grep decodes UTF-16 with either byte order mark, as ripgrep does, also on the second search (the content cache holds the
// decoded text, not the "binary" verdict a NUL byte would give).
func TestNativeGrepDecodesBigEndianUTF16(t *testing.T) {
	ClearNativeSearchCache()
	dir := t.TempDir()
	path := filepath.Join(dir, "be.txt")
	var data []byte
	for _, unit := range utf16.Encode([]rune("\uFEFFfirst\nneedle ä\n")) {
		data = append(data, byte(unit>>8), byte(unit))
	}
	writeTestFile(t, path, data)
	for range 2 {
		if got := grepText(t, dir, map[string]any{"pattern": "needle"}); got != "be.txt:2: needle ä" {
			t.Fatalf("got %q", got)
		}
	}
}

// µPi: an absolute find pattern outside the search path matches the absolute paths; one under it is matched relative to it.
func TestNativeFindAbsolutePatterns(t *testing.T) {
	ClearNativeSearchCache()
	dir := t.TempDir()
	project := filepath.Join(dir, "proj")
	if err := os.MkdirAll(filepath.Join(project, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(project, "lib", "b.ts"), nil)
	for pattern, want := range map[string]string{
		filepath.ToSlash(project) + "/lib/*.ts": "lib/b.ts",
		filepath.ToSlash(dir) + "/*/lib/*.ts":   "lib/b.ts",
		filepath.ToSlash(dir) + "/other/*.ts":   "",
	} {
		res, err := nativeFindDetailed(t.Context(), pattern, project, 10, false)
		if err != nil {
			t.Fatal(err)
		}
		got := ""
		if len(res.paths) == 1 {
			got = res.paths[0]
		} else if len(res.paths) > 1 {
			t.Fatalf("%s = %q", pattern, res.paths)
		}
		if got != want {
			t.Errorf("%s = %q, want %q", pattern, got, want)
		}
	}
}
