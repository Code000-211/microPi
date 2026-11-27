package docsearch

import (
	"path/filepath"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

func isJoiner(r rune) bool {
	switch r {
	case '_', '-', '.', '/', ':', '+':
		return true
	default:
		return false
	}
}

func isJoinerByte(b byte) bool {
	switch b {
	case '_', '-', '.', '/', ':', '+':
		return true
	default:
		return false
	}
}

var stopwords = map[string]struct{}{
	// English (42)
	"a": {}, "an": {}, "and": {}, "are": {}, "as": {}, "at": {}, "be": {}, "but": {},
	"by": {}, "do": {}, "for": {}, "from": {}, "has": {}, "have": {}, "he": {}, "in": {},
	"is": {}, "it": {}, "its": {}, "may": {}, "no": {}, "not": {}, "of": {}, "on": {},
	"or": {}, "our": {}, "she": {}, "so": {}, "that": {}, "the": {}, "their": {}, "them": {},
	"there": {}, "these": {}, "they": {}, "this": {}, "to": {}, "was": {}, "we": {}, "were": {},
	"which": {}, "with": {}, "you": {}, "your": {},
	// German (47)
	"aber": {}, "als": {}, "am": {}, "auch": {}, "auf": {}, "aus": {}, "bei": {},
	"bin": {}, "bis": {}, "da": {}, "das": {}, "dass": {}, "dein": {}, "dem": {}, "den": {},
	"der": {}, "des": {}, "die": {}, "dies": {}, "doch": {}, "du": {}, "durch": {}, "ein": {},
	"eine": {}, "einem": {}, "einen": {}, "einer": {}, "er": {}, "es": {}, "fuer": {}, "für": {},
	"hat": {}, "ich": {}, "ihr": {}, "im": {}, "ist": {}, "ja": {}, "kann": {},
	"mit": {}, "nach": {}, "nicht": {}, "noch": {}, "nur": {}, "oder": {}, "sie": {}, "sind": {},
	"ueber": {}, "über": {}, "um": {}, "und": {}, "uns": {}, "unter": {}, "vom": {},
	"von": {}, "vor": {}, "war": {}, "wenn": {}, "wie": {}, "wir": {}, "wird": {},
	"zu": {}, "zum": {}, "zur": {},
}

func isStopword(w string) bool {
	_, ok := stopwords[w]
	return ok
}

// RawWords scans maximal runs of alphanumeric or joiner characters and trims leading/trailing joiners.
func RawWords(text string) []string {
	var words []string
	eachRawWord(text, func(word string) { words = append(words, word) })
	return words
}

// wordByte classifies ASCII bytes for eachRawWord: letters, digits and joiners continue a word.
var wordByte = func() (table [utf8.RuneSelf]bool) {
	for c := 0; c < utf8.RuneSelf; c++ {
		r := rune(c)
		table[c] = unicode.IsLetter(r) || unicode.IsDigit(r) || isJoiner(r)
	}
	return table
}()

// isWordRuneAt reports whether the rune at text[i:] continues a word, and its size; ASCII is looked up in wordByte.
func isWordRuneAt(text string, i int) (bool, int) {
	if c := text[i]; c < utf8.RuneSelf {
		return wordByte[c], 1
	}
	r, size := utf8.DecodeRuneInString(text[i:])
	return unicode.IsLetter(r) || unicode.IsDigit(r) || isJoiner(r), size
}

// eachRawWord calls fn with each word RawWords returns, in order, without collecting them.
func eachRawWord(text string, fn func(word string)) {
	n := len(text)
	i := 0
	for i < n {
		ok, size := isWordRuneAt(text, i)
		if !ok {
			i += size
			continue
		}
		start := i
		i += size
		for i < n {
			ok2, s2 := isWordRuneAt(text, i)
			if !ok2 {
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
			fn(word)
		}
	}
}

var stemSuffixes = []string{"ungen", "ung", "ing", "ies", "ed", "es", "en", "er", "s", "e"}

// Stem applies light English/German suffix stripping to a lowercased all-alphabetic word.
// Strips the first matching suffix iff remaining character count >= 4.
func Stem(w string) string {
	runeLen := utf8.RuneCountInString(w)
	for _, suf := range stemSuffixes {
		if strings.HasSuffix(w, suf) {
			sufRunes := utf8.RuneCountInString(suf)
			if runeLen-sufRunes >= 4 {
				return w[:len(w)-len(suf)]
			}
		}
	}
	return w
}

// hasCamelBoundary reports whether CamelParts(part) returns more than one part, without building them: a part ends at each
// lowercase->uppercase, letter->digit or digit->letter boundary, and every part is non-empty.
func hasCamelBoundary(part string) bool {
	var prevR rune
	for i, r := range part {
		if i > 0 {
			prevDigit := prevR >= '0' && prevR <= '9'
			curDigit := r >= '0' && r <= '9'
			if (unicode.IsLower(prevR) && unicode.IsUpper(r)) || (unicode.IsLetter(prevR) && curDigit) || (prevDigit && unicode.IsLetter(r)) {
				return true
			}
		}
		prevR = r
	}
	return false
}

// CamelParts splits a token at lowercase->uppercase, letter->digit, and digit->letter boundaries,
// returning lowercased non-empty parts.
func CamelParts(part string) []string {
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

func hasASCIIDigit(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			return true
		}
	}
	return false
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func allAlpha(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

func keepTerm(s string) bool {
	return utf8.RuneCountInString(s) >= 2 || hasASCIIDigit(s)
}

func splitJoiners(w string) []string {
	return strings.FieldsFunc(w, isJoiner)
}

// Terms tokenizes text into normalized search terms (with light stemming on plain words
// and full + split + camelCase decomposition on compound/camelCase identifiers).
func Terms(text string) []string {
	var out []string
	eachRawWord(text, func(w string) {
		lower := strings.ToLower(w)
		compound := strings.ContainsFunc(w, isJoiner)
		mixed := hasCamelBoundary(w)
		if !compound && !mixed {
			if keepTerm(lower) && !isStopword(lower) {
				if allAlpha(lower) {
					out = append(out, Stem(lower))
				} else {
					out = append(out, lower)
				}
			}
			return
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
			if hasCamelBoundary(p) {
				for _, s := range CamelParts(p) {
					if utf8.RuneCountInString(s) >= 2 && !allDigits(s) && !isStopword(s) && s != pl {
						out = append(out, s)
					}
				}
			}
		}
	})
	return out
}

// IsEntityWord reports whether w looks like an identifier, flag, product name, or acronym.
func IsEntityWord(w string) bool {
	rc := utf8.RuneCountInString(w)
	if rc < 2 || rc > 64 {
		return false
	}
	var alpha int
	var hasLower, hasNonInitialUpper, hasJoiner, hasDigit bool
	for i, r := range w {
		if unicode.IsLetter(r) {
			alpha++
			if unicode.IsLower(r) {
				hasLower = true
			} else if unicode.IsUpper(r) && i > 0 {
				hasNonInitialUpper = true
			}
		} else if r >= '0' && r <= '9' {
			hasDigit = true
		} else if isJoiner(r) {
			hasJoiner = true
		}
	}
	if alpha == 0 {
		return false
	}
	if hasJoiner {
		return true
	}
	if hasLower && hasNonInitialUpper {
		return true
	}
	if hasDigit {
		return true
	}
	// Acronym: all-uppercase alphabetic of length 2..=6 and not a stopword.
	if rc >= 2 && rc <= 6 && alpha == rc && !hasLower && !isStopword(strings.ToLower(w)) {
		return true
	}
	return false
}

// Entities extracts deduplicated lowercased entity names from text and optional filename.
func Entities(text, filename string, isFirstChunk bool) []string {
	seen := make(map[string]struct{})
	var out []string
	add := func(e string) {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			return
		}
		if _, ok := seen[e]; ok {
			return
		}
		seen[e] = struct{}{}
		out = append(out, e)
	}

	// 1. Inline code spans (`...`, not fenced ```).
	n := len(text)
	i := 0
	for i < n {
		if text[i] == '`' {
			if i+2 < n && text[i+1] == '`' && text[i+2] == '`' {
				// Skip fence marker.
				i += 3
				for i < n && text[i] == '`' {
					i++
				}
				continue
			}
			end := strings.IndexByte(text[i+1:], '`')
			if end >= 0 {
				span := text[i+1 : i+1+end]
				if !strings.ContainsAny(span, "\n\r") {
					span = strings.TrimSpace(span)
					for len(span) > 0 && isJoinerByte(span[0]) {
						span = span[1:]
					}
					for len(span) > 0 && isJoinerByte(span[len(span)-1]) {
						span = span[:len(span)-1]
					}
					rc := utf8.RuneCountInString(span)
					if rc >= 2 && rc <= 64 {
						fields := strings.Fields(span)
						if len(fields) >= 2 && len(fields) <= 3 {
							add(span)
						} else if len(fields) == 1 && IsEntityWord(fields[0]) {
							add(fields[0])
						}
					}
				}
				i = i + 1 + end + 1
				continue
			}
		}
		i++
	}

	// 2. Markdown link targets ](target#anchor)
	searchFrom := 0
	for searchFrom < n {
		idx := strings.Index(text[searchFrom:], "](")
		if idx < 0 {
			break
		}
		start := searchFrom + idx + 2
		endRel := strings.IndexAny(text[start:], ")\n\r")
		if endRel < 0 {
			break
		}
		if text[start+endRel] == ')' {
			target := strings.TrimSpace(text[start : start+endRel])
			if hash := strings.IndexByte(target, '#'); hash >= 0 {
				target = target[:hash]
			}
			if !strings.Contains(target, "://") && !strings.HasPrefix(target, "mailto:") && strings.Contains(target, ".") {
				base := filepath.Base(target)
				if base != "" && base != "." && base != ".." {
					add(base)
				}
			}
		}
		searchFrom = start + endRel + 1
	}

	// 3. Identifier-like words per line (skipping all-caps banner/license lines).
	for line := range strings.SplitSeq(text, "\n") {
		var letters, upper int
		for _, r := range line {
			if unicode.IsLetter(r) {
				letters++
				if unicode.IsUpper(r) {
					upper++
				}
			}
		}
		if letters >= 20 && upper*10 > letters*6 {
			continue
		}
		eachRawWord(line, func(w string) {
			if IsEntityWord(w) {
				add(w)
			}
		})
	}

	// 4. File identity for chunk 0.
	if isFirstChunk && filename != "" {
		base := filepath.Base(filename)
		add(base)
		ext := filepath.Ext(base)
		stem := strings.TrimSuffix(base, ext)
		if IsEntityWord(stem) {
			add(stem)
		}
	}

	return out
}

// EntityAlias returns the space-joined raw-words form of name if it differs from name.
func EntityAlias(name string) string {
	words := RawWords(strings.ToLower(name))
	if len(words) == 0 {
		return ""
	}
	joined := strings.Join(words, " ")
	if joined != strings.ToLower(name) {
		return joined
	}
	return ""
}

var (
	deEnOnce sync.Once
	deEnMap  map[string]string
)

func initDeEnMap() {
	deEnOnce.Do(func() {
		deEnMap = make(map[string]string, len(deEnLexicon)*2)
		for _, entry := range deEnLexicon {
			key := Stem(strings.ToLower(entry.de))
			if prev, ok := deEnMap[key]; ok {
				deEnMap[key] = prev + " " + entry.en
			} else {
				deEnMap[key] = entry.en
			}
		}
	})
}

// WithTranslations expands German technical terms in query with English equivalents.
func WithTranslations(query string) string {
	initDeEnMap()
	raw := RawWords(query)
	if len(raw) == 0 {
		return query
	}
	var extra []string
	seen := make(map[string]struct{})
	addExtra := func(en string) {
		for w := range strings.FieldsSeq(en) {
			if _, ok := seen[w]; !ok {
				seen[w] = struct{}{}
				extra = append(extra, w)
			}
		}
	}
	for _, w := range raw {
		lower := strings.ToLower(w)
		parts := []string{lower}
		if strings.ContainsFunc(lower, isJoiner) {
			parts = append(parts, splitJoiners(lower)...)
		}
		for _, part := range parts {
			if !allAlpha(part) {
				continue
			}
			candidates := []string{Stem(part)}
			if rest, ok := strings.CutPrefix(part, "ge"); ok && len(rest) >= 4 {
				candidates = append(candidates, Stem(rest))
				if strings.HasSuffix(rest, "t") && len(rest) > 4 {
					candidates = append(candidates, Stem(rest[:len(rest)-1]))
				}
			}
			if strings.HasSuffix(part, "t") && len(part) > 4 {
				candidates = append(candidates, Stem(part[:len(part)-1]))
			}
			for _, c := range candidates {
				if en, ok := deEnMap[c]; ok {
					addExtra(en)
				}
			}
		}
	}
	if len(extra) == 0 {
		return query
	}
	return query + " " + strings.Join(extra, " ")
}

// IsCodeExt reports whether ext (with or without leading dot, lowercased) is a code extension.
func IsCodeExt(ext string) bool {
	ext = strings.ToLower(strings.TrimPrefix(ext, "."))
	switch ext {
	case "c", "cc", "cpp", "cxx", "h", "hh", "hpp", "hip", "cu", "cuh",
		"rs", "py", "ts", "tsx", "mts", "cts", "js", "jsx", "mjs", "cjs",
		"go", "sh", "bash", "zsh":
		return true
	default:
		return false
	}
}

// IsCodePath reports whether a file path has a recognized code extension.
func IsCodePath(p string) bool {
	return IsCodeExt(filepath.Ext(p))
}
