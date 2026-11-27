package ignorerules

import "testing"

// The literal and suffix comparisons answer exactly as the compiled expression does.
func TestFastMatchersAgreeWithTheExpression(t *testing.T) {
	patterns := []string{
		"node_modules", "node_modules/", "Build", "docs/out", "a/b/c/", "*.log", "*.LOG", "*.min.js", "*~", "*.k", "*ß",
		"résumé", "*.tar.gz", ".env", "*.", "x", "*x/", "dir/*.go",
	}
	candidates := []string{
		"node_modules", "NODE_MODULES", "src/node_modules", "node_modules2", "build", "BUILD", "docs/out", "Docs/Out", "docs/out/x",
		"a/b/c", "app.log", "app.LOG", "dir/app.log", "x.min.js", "min.js", "file~", "~", "ab.K", "ab.K", "K", "straße",
		"STRASSE", "RÉSUMÉ", "x.tar.gz", "tar.gz", ".env", ".ENV", "a.", "x", "X", "dir/a.go", "", "a/", "\xff.log",
	}
	for _, pattern := range patterns {
		rule, ok := newRule(pattern)
		if !ok {
			t.Fatalf("pattern %q gave no rule", pattern)
		}
		slow := rule
		slow.fast = fastNone
		for _, candidate := range candidates {
			for _, dir := range []bool{false, true} {
				if got, want := rule.match(candidate, dir), slow.match(candidate, dir); got != want {
					t.Errorf("pattern %q candidate %q dir %v: fast %v, expression %v", pattern, candidate, dir, got, want)
				}
			}
		}
	}
}

func BenchmarkIgnoredLiteralRules(b *testing.B) {
	var rules []Rule
	for _, p := range []string{"node_modules", "dist/", "*.log", "*.tmp", "build/", ".cache/", "coverage/", "*.pyc", "__pycache__/", "tmp/"} {
		rules = appendRule(rules, p)
	}
	for b.Loop() {
		Ignored("/r/a/b/c/d/file.go", false, "/r", rules)
	}
}
