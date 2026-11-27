package checks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/codingagent/filetracking"
)

// µPi: ports of the C++ engine cases of packages/coding-agent/test/libclang.test.ts (a18b202f), run through the clang binary that
// CppChecker uses in place of libclang.

func requireClang(t *testing.T) {
	t.Helper()
	if clangBinary() == "" {
		t.Skip("no clang on PATH")
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func recordRead(t *testing.T, tracker *filetracking.FileStateTracker, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tracker.RecordFileRead(path, string(data), float64(time.Now().UnixMilli()), true)
}

func cppMessages(t *testing.T, checker Checker, path string) []string {
	t.Helper()
	diags, err := checker.Run(context.Background(), checker.Unit(path))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range diags {
		if d.Severity == SeverityError {
			out = append(out, d.Message)
		}
	}
	return out
}

func TestCppCheckerChecksAHeaderInsideTheSourceThatIncludesItThroughAnIFolder(t *testing.T) {
	requireClang(t)
	dir := t.TempDir()
	header := filepath.Join(dir, "include", "kernels", "gemm.h")
	source := filepath.Join(dir, "src", "gemm.cpp")
	unrelated := filepath.Join(dir, "other", "unrelated.cpp")
	// The header relies on a type its includer declares first: on its own it does not compile.
	writeTestFile(t, header, "inline Scalar twice(Scalar x) { return x + x; }\n")
	writeTestFile(t, source, "#include <cstddef>\nusing Scalar = float;\n#include \"kernels/gemm.h\"\nint main() { return 0; }\n")
	writeTestFile(t, unrelated, "int other() { return 1; }\n")
	db, err := json.Marshal([]map[string]any{
		{"directory": dir, "file": source, "arguments": []string{"clang++", "-std=c++17", "-Iinclude", "-c", source}},
		{"directory": dir, "file": unrelated, "arguments": []string{"clang++", "-std=c++17", "-c", unrelated}},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(dir, "compile_commands.json"), string(db))
	tracker := filetracking.NewFileStateTracker()
	for _, file := range []string{source, header, unrelated} {
		recordRead(t, tracker, file)
	}
	checker := CppChecker(tracker, dir)
	writeTestFile(t, header, "inline Scalar twice(Scalar x) { return x + x; }\nint broken = \"text\";\n")
	errors := cppMessages(t, checker, header)
	if !slices.ContainsFunc(errors, func(m string) bool { return strings.Contains(m, "cannot initialize a variable of type 'int'") }) {
		t.Fatalf("errors = %q, want the int initialization", errors)
	}
	// Inside its includer `Scalar` is known: no error for it.
	if slices.ContainsFunc(errors, func(m string) bool { return strings.Contains(m, "Scalar") }) {
		t.Fatalf("errors = %q, want none for Scalar", errors)
	}
}

func TestCppCheckerFindsHeadersThroughTheMakefilesIFolders(t *testing.T) {
	requireClang(t)
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "Makefile"), "CFLAGS ?= -std=c11 -Wall -Iinclude\napp: src/user.c\n\tcc $(CFLAGS) -o app src/user.c\n")
	header := filepath.Join(dir, "include", "user.h")
	source := filepath.Join(dir, "src", "user.c")
	writeTestFile(t, header, "#define NAME_MAX 32\nstruct user { char name[NAME_MAX]; };\n")
	writeTestFile(t, source, "#include \"user.h\"\nint size(struct user *u) { return (int)sizeof u->name + NAME_MAX; }\n")
	tracker := filetracking.NewFileStateTracker()
	recordRead(t, tracker, header)
	recordRead(t, tracker, source)
	checker := CppChecker(tracker, dir)
	if errors := cppMessages(t, checker, source); len(errors) != 0 {
		t.Fatalf("errors = %q, want none", errors)
	}
	// A real error is still one: the header the agent changed no longer defines NAME_MAX.
	edited := "struct user { char *name; };\n"
	writeTestFile(t, header, edited)
	tracker.RecordFileModifiedByAgent(header, edited, float64(time.Now().UnixMilli()))
	if errors := cppMessages(t, checker, source); !slices.Equal(errors, []string{"use of undeclared identifier 'NAME_MAX'"}) {
		t.Fatalf("errors = %q, want NAME_MAX undeclared", errors)
	}
}

func TestCppCheckerReportsOnlyTheMissingHeader(t *testing.T) {
	requireClang(t)
	dir := t.TempDir()
	source := filepath.Join(dir, "a.c")
	writeTestFile(t, source, "#include \"nowhere.h\"\nint f(struct thing *t) { return t->x + t->y + LIMIT; }\n")
	checker := CppChecker(filetracking.NewFileStateTracker(), dir)
	if errors := cppMessages(t, checker, source); !slices.Equal(errors, []string{"'nowhere.h' file not found"}) {
		t.Fatalf("errors = %q", errors)
	}
}
