package checks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Without ty the Python check parses the file with python3's ast: the syntax error is reported on its own line, not on the line of the -c
// script that the traceback names first.
func TestPythonFallbackReportsTheSyntaxErrorsLine(t *testing.T) {
	if OnPath("ty") != "" || OnPath("python3") == "" {
		t.Skip("needs python3 without ty")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "p.py")
	if err := os.WriteFile(path, []byte("a = 1\nb = 2\ndef f(:\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	unit := pythonChecker.Unit(path)
	diags, err := pythonChecker.Run(t.Context(), unit)
	if err != nil {
		t.Fatal(err)
	}
	var syntax []Diagnostic
	for _, d := range diags {
		if d.Severity == SeverityError {
			syntax = append(syntax, d)
		}
	}
	if len(syntax) != 1 || syntax[0].Line != 3 || !strings.Contains(syntax[0].Message, "SyntaxError") {
		t.Fatalf("diagnostics = %+v, want one SyntaxError on line 3", syntax)
	}
}
