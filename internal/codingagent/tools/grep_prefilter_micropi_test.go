package tools

import (
	"testing"
)

// µPi: the prefilters and the literal fast path give the regex's answer on every line: case-insensitive patterns with a required
// literal (also the non-ASCII letters (?i) folds to k and s), inline flags, and case-sensitive patterns without regex characters.
func TestLineMatcherPrefiltersAgreeWithTheRegex(t *testing.T) {
	lines := []string{"mayHoldAliasKey(raw)", "MAYHOLDALIASKEY", "mayholdalias\u212aey", "\u017ftart here", "start here", "nothing", "func NormalizeToolArguments(", "abCDEF", "abcdef", "x.y"}
	for _, c := range []struct {
		pattern    string
		ignoreCase bool
		want       []bool
	}{
		{"mayholdaliaskey", true, []bool{true, true, true, false, false, false, false, false, false, false}},
		{"start here", true, []bool{false, false, false, true, true, false, false, false, false, false}},
		{"func NormalizeToolArguments", false, []bool{false, false, false, false, false, false, true, false, false, false}},
		{"ab(?i)cdef", false, []bool{false, false, false, false, false, false, false, true, true, false}},
		{`x\.y`, false, []bool{false, false, false, false, false, false, false, false, false, true}},
	} {
		m, err := buildLineMatcher(c.pattern, c.ignoreCase, false)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range lines {
			got := m.prefilter([]byte(line)) && m.matchLine([]byte(line))
			if got != c.want[i] {
				t.Errorf("pattern %q (ignoreCase %v) on %q = %v, want %v", c.pattern, c.ignoreCase, line, got, c.want[i])
			}
		}
	}
}
