package checks

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// goCheckModule writes a module with the files and returns the diagnostics of the check of the file named first.
func goCheckModule(t *testing.T, files map[string]string, first string) []Diagnostic {
	t.Helper()
	if OnPath("go") == "" {
		t.Skip("go is not installed")
	}
	dir := t.TempDir()
	files["go.mod"] = "module example.com/m\n\ngo 1.21\n"
	for name, text := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	unit := goChecker.Unit(filepath.Join(dir, first))
	if unit == nil {
		t.Fatal("no check unit")
	}
	diags, err := goChecker.Run(t.Context(), unit)
	if err != nil {
		t.Fatal(err)
	}
	return diags
}

func goMessages(diags []Diagnostic) []string {
	var out []string
	for _, d := range diags {
		out = append(out, filepath.Base(d.File)+": "+d.Message)
	}
	slices.Sort(out)
	return out
}

// µPi: every type error of a test file is reported, not only the first one go vet stops at, also in a package of test files only and in
// a package whose build fails; a continuation line is not a diagnostic of its own.
func TestGoCheckReportsEveryTestFileError(t *testing.T) {
	twoErrors := "package p\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {\n\tundefinedOne()\n\tundefinedTwo()\n}\n"
	for name, files := range map[string]map[string]string{
		"tests beside a package that builds": {"p/p.go": "package p\n\nfunc P() {}\n", "p/p_test.go": twoErrors},
		"a package of test files only":       {"p/p_test.go": twoErrors},
		"a package that does not build":      {"p/p.go": "package p\n\nfunc P() { undefinedBuild() }\n", "p/p_test.go": twoErrors},
	} {
		t.Run(name, func(t *testing.T) {
			first := "p/p_test.go"
			got := strings.Join(goMessages(goCheckModule(t, files, first)), "\n")
			for _, want := range []string{"undefinedOne", "undefinedTwo"} {
				if !strings.Contains(got, want) {
					t.Errorf("%s not reported:\n%s", want, got)
				}
			}
			if _, ok := files["p/p.go"]; ok && strings.Contains(files["p/p.go"], "undefinedBuild") && !strings.Contains(got, "undefinedBuild") {
				t.Errorf("the build error is not reported:\n%s", got)
			}
		})
	}
	t.Run("a redeclaration's continuation line", func(t *testing.T) {
		diags := goCheckModule(t, map[string]string{"p/a.go": "package p\n\nvar a int\n", "p/b.go": "package p\n\nvar a int\n"}, "p/a.go")
		for _, d := range diags {
			if strings.Contains(d.Message, "other declaration") {
				t.Fatalf("continuation line reported as a diagnostic: %+v", diags)
			}
		}
		if len(diags) != 1 {
			t.Fatalf("diagnostics = %+v, want the redeclaration", diags)
		}
	})
}
