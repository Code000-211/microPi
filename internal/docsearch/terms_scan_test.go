package docsearch

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

var _ = unicode.IsLetter
var _ = utf8.RuneLen

// The allocation-free word scan and camel-boundary check give Terms, RawWords and the boundary answer exactly as before, over the
// repository's own text and some Unicode samples.
func TestTermsUnchangedByTheFasterScan(t *testing.T) {
	samples := []string{"", "   ", "--x--", "getHTTPResponse2Code foo_bar.baz/qux", "Straße ÜBER über naïve café 東京タワー", "a1b2C3", "ID x2 X", "\xff\xfe bad utf8", "K\u212a s\u017f"}
	root, _ := filepath.Abs("../..")
	_ = filepath.WalkDir(filepath.Join(root, "internal", "docsearch"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && (strings.HasSuffix(p, ".go") || strings.HasSuffix(p, ".md")) {
			if data, err := os.ReadFile(p); err == nil {
				samples = append(samples, string(data))
			}
		}
		return nil
	})
	_ = filepath.WalkDir(filepath.Join(root, "docs", "site", "docs"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".md") {
			if data, err := os.ReadFile(p); err == nil {
				samples = append(samples, string(data))
			}
		}
		return nil
	})
	if len(samples) < 20 {
		t.Fatalf("only %d samples", len(samples))
	}
	for i, text := range samples {
		if got, want := RawWords(text), oldRawWords(text); !slices.Equal(got, want) {
			t.Fatalf("sample %d: RawWords differs", i)
		}
		if got, want := Terms(text), oldTerms(text); !slices.Equal(got, want) {
			t.Fatalf("sample %d: Terms differs: %d vs %d terms", i, len(got), len(want))
		}
		for _, w := range oldRawWords(text) {
			if got, want := hasCamelBoundary(w), len(oldCamelParts(w)) > 1; got != want {
				t.Fatalf("hasCamelBoundary(%q) = %v, want %v", w, got, want)
			}
		}
	}
}

// RawWords scans maximal runs of alphanumeric or joiner characters and trims leading/trailing joiners.
func oldRawWords(text string) []string {
	var words []string
	n := len(text)
	i := 0
	for i < n {
		r, size := utf8.DecodeRuneInString(text[i:])
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !isJoiner(r) {
			i += size
			continue
		}
		start := i
		i += size
		for i < n {
			r2, s2 := utf8.DecodeRuneInString(text[i:])
			if !unicode.IsLetter(r2) && !unicode.IsDigit(r2) && !isJoiner(r2) {
				break
			}
			i += s2
		}
		word := text[start:i]
		// Trim leading and trailing joiners.
		for len(word) > 0 && isJoinerByte(word[0]) {
			word = word[1:]
		}
		for len(word) > 0 && isJoinerByte(word[len(word)-1]) {
			word = word[:len(word)-1]
		}
		if len(word) > 0 {
			words = append(words, word)
		}
	}
	return words
}

// CamelParts splits a token at lowercase->uppercase, letter->digit, and digit->letter boundaries,
// returning lowercased non-empty parts.
func oldCamelParts(part string) []string {
	if part == "" {
		return nil
	}
	var parts []string
	start := 0
	var prevR rune
	for i, r := range part {
		if i > 0 {
			prevLower := unicode.IsLower(prevR)
			prevLetter := unicode.IsLetter(prevR)
			prevDigit := prevR >= '0' && prevR <= '9'
			curUpper := unicode.IsUpper(r)
			curLetter := unicode.IsLetter(r)
			curDigit := r >= '0' && r <= '9'
			if (prevLower && curUpper) || (prevLetter && curDigit) || (prevDigit && curLetter) {
				if p := strings.ToLower(part[start:i]); p != "" {
					parts = append(parts, p)
				}
				start = i
			}
		}
		prevR = r
	}
	if p := strings.ToLower(part[start:]); p != "" {
		parts = append(parts, p)
	}
	return parts
}

// Terms tokenizes text into normalized search terms (with light stemming on plain words
// and full + split + camelCase decomposition on compound/camelCase identifiers).
func oldTerms(text string) []string {
	raw := oldRawWords(text)
	if len(raw) == 0 {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, w := range raw {
		lower := strings.ToLower(w)
		compound := strings.ContainsFunc(w, isJoiner)
		cparts := oldCamelParts(w)
		mixed := len(cparts) > 1
		if !compound && !mixed {
			if keepTerm(lower) && !isStopword(lower) {
				if allAlpha(lower) {
					out = append(out, Stem(lower))
				} else {
					out = append(out, lower)
				}
			}
			continue
		}
		out = append(out, lower)
		jparts := splitJoiners(w)
		for _, p := range jparts {
			if p == "" {
				continue
			}
			pl := strings.ToLower(p)
			if keepTerm(pl) && !isStopword(pl) && pl != lower {
				out = append(out, pl)
			}
			sub := oldCamelParts(p)
			if len(sub) > 1 {
				for _, s := range sub {
					if utf8.RuneCountInString(s) >= 2 && !allDigits(s) && !isStopword(s) && s != pl {
						out = append(out, s)
					}
				}
			}
		}
	}
	return out
}
