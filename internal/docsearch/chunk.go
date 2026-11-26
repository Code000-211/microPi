package docsearch

import (
	"path/filepath"
	"strings"
	"unicode"
)

const (
	// MaxChars is the maximum character length of a chunk before windowing.
	MaxChars = 1600
	// TargetChars is the preferred split point at blank lines when windowing long sections.
	TargetChars = 1100
)

// Chunk represents a logical section of a document or source file.
type Chunk struct {
	Heading   string
	LineStart uint32
	LineEnd   uint32
	Text      string
}

// Lang identifies a source code language family for definition-aware chunking.
type Lang int

const (
	LangC Lang = iota + 1
	LangRust
	LangPython
	LangScript
	LangGo
	LangShell
)

// LangFromExt returns the Lang for a file extension (with or without leading dot).
func LangFromExt(ext string) (Lang, bool) {
	ext = strings.ToLower(strings.TrimPrefix(ext, "."))
	switch ext {
	case "c", "cc", "cpp", "cxx", "h", "hh", "hpp", "hip", "cu", "cuh":
		return LangC, true
	case "rs":
		return LangRust, true
	case "py":
		return LangPython, true
	case "ts", "tsx", "mts", "cts", "js", "jsx", "mjs", "cjs":
		return LangScript, true
	case "go":
		return LangGo, true
	case "sh", "bash", "zsh":
		return LangShell, true
	default:
		return 0, false
	}
}

// SplitChunks splits a file's text using definition-aware code chunking when its extension
// matches a supported language, or markdown/plain-text chunking otherwise.
func SplitChunks(path, text string) []Chunk {
	ext := strings.ToLower(filepath.Ext(path))
	if lang, ok := LangFromExt(ext); ok {
		return SplitCode(text, lang)
	}
	isMD := ext == ".md" || ext == ".markdown" || ext == ".mdx"
	return Split(text, isMD)
}

type headingEntry struct {
	level int
	title string
}

// Split splits markdown or plain text into heading-aware chunks, windowing long sections.
func Split(text string, markdown bool) []Chunk {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return nil
	}

	var chunks []Chunk
	var stack []headingEntry
	var secLines []string
	secHeading := ""
	secStart := uint32(1)
	inFence := false
	var fenceChar byte

	flushSection := func(endLine uint32) {
		if len(secLines) == 0 {
			return
		}
		hasContent := false
		for _, l := range secLines {
			if strings.TrimSpace(l) != "" {
				hasContent = true
				break
			}
		}
		if !hasContent {
			secLines = secLines[:0]
			return
		}
		windowLines(secHeading, secStart, secLines, &chunks)
		secLines = secLines[:0]
		_ = endLine
	}

	for idx, line := range lines {
		lineNo := uint32(idx + 1)
		trimmed := strings.TrimSpace(line)

		if markdown {
			if !inFence {
				if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
					inFence = true
					fenceChar = trimmed[0]
				}
			} else if len(trimmed) >= 3 && trimmed[0] == fenceChar && strings.Trim(trimmed, string(fenceChar)) == "" {
				inFence = false
			}
		}

		if markdown && !inFence {
			if level, title, ok := parseATXHeading(line); ok {
				flushSection(lineNo - 1)
				for len(stack) > 0 && stack[len(stack)-1].level >= level {
					stack = stack[:len(stack)-1]
				}
				stack = append(stack, headingEntry{level: level, title: title})
				secHeading = joinHeadingStack(stack)
				secStart = lineNo + 1
				continue
			}
		}

		if len(secLines) == 0 {
			secStart = lineNo
		}
		secLines = append(secLines, line)
	}
	flushSection(uint32(len(lines)))
	return chunks
}

func parseATXHeading(line string) (int, string, bool) {
	i := 0
	for i < len(line) && i < 6 && line[i] == '#' {
		i++
	}
	if i == 0 || i > 6 || i >= len(line) {
		return 0, "", false
	}
	if line[i] != ' ' && line[i] != '\t' {
		return 0, "", false
	}
	title := strings.TrimSpace(line[i+1:])
	title = strings.TrimRight(title, "# \t")
	if title == "" {
		return 0, "", false
	}
	return i, title, true
}

func joinHeadingStack(stack []headingEntry) string {
	if len(stack) == 0 {
		return ""
	}
	parts := make([]string, len(stack))
	for i, e := range stack {
		parts[i] = e.title
	}
	return strings.Join(parts, " › ")
}

func windowLines(heading string, startLine uint32, lines []string, out *[]Chunk) {
	if len(lines) == 0 {
		return
	}
	var wStart int
	wLen := 0
	for i, l := range lines {
		lineLen := len(l) + 1
		if wLen+lineLen > MaxChars && i > wStart {
			*out = append(*out, Chunk{
				Heading:   heading,
				LineStart: startLine + uint32(wStart),
				LineEnd:   startLine + uint32(i-1),
				Text:      strings.Join(lines[wStart:i], "\n"),
			})
			wStart = i
			wLen = 0
		}
		wLen += lineLen
		if wLen >= TargetChars && strings.TrimSpace(l) == "" && i+1 < len(lines) {
			*out = append(*out, Chunk{
				Heading:   heading,
				LineStart: startLine + uint32(wStart),
				LineEnd:   startLine + uint32(i),
				Text:      strings.Join(lines[wStart:i+1], "\n"),
			})
			wStart = i + 1
			wLen = 0
		}
	}
	if wStart < len(lines) {
		// Trim trailing blank lines on final window if non-empty.
		end := len(lines)
		for end > wStart && strings.TrimSpace(lines[end-1]) == "" {
			end--
		}
		if end > wStart {
			*out = append(*out, Chunk{
				Heading:   heading,
				LineStart: startLine + uint32(wStart),
				LineEnd:   startLine + uint32(end-1),
				Text:      strings.Join(lines[wStart:end], "\n"),
			})
		}
	}
}

// SplitCode splits source code into definition-aware chunks with attached comments/attributes.
func SplitCode(text string, lang Lang) []Chunk {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return nil
	}

	type defMark struct {
		start int // earliest attached comment/attribute line
		def   int // definition line itself
		name  string
	}

	var marks []defMark
	for i := 0; i < len(lines); i++ {
		name, ok := detectDef(lines, i, lang)
		if !ok {
			continue
		}
		start := walkUpComments(lines, i)
		if len(marks) > 0 && start <= marks[len(marks)-1].def {
			start = marks[len(marks)-1].def + 1
		}
		marks = append(marks, defMark{start: start, def: i, name: name})
	}

	if len(marks) == 0 {
		return Split(text, false)
	}

	var chunks []Chunk
	// Leading preamble before first definition.
	if marks[0].start > 0 {
		windowLines("", 1, lines[:marks[0].start], &chunks)
	}

	for m := 0; m < len(marks); m++ {
		cur := marks[m]
		nextStart := len(lines)
		if m+1 < len(marks) {
			nextStart = marks[m+1].start
		}

		// Nested header merging: if current definition has no non-empty body lines before nextStart,
		// merge its name into the next definition.
		if m+1 < len(marks) {
			hasBody := false
			for j := cur.def + 1; j < nextStart; j++ {
				t := strings.TrimSpace(lines[j])
				if t != "" && t != "{" && t != "(" {
					hasBody = true
					break
				}
			}
			if !hasBody {
				if cur.name != "" && marks[m+1].name != "" {
					marks[m+1].name = cur.name + " › " + marks[m+1].name
				} else if cur.name != "" {
					marks[m+1].name = cur.name
				}
				marks[m+1].start = cur.start
				continue
			}
		}

		windowLines(cur.name, uint32(cur.start+1), lines[cur.start:nextStart], &chunks)
	}

	return chunks
}

func walkUpComments(lines []string, i int) int {
	start := i
	for j := i - 1; j >= 0; j-- {
		t := strings.TrimSpace(lines[j])
		if t == "" {
			break
		}
		if strings.HasPrefix(t, "//") ||
			strings.HasPrefix(t, "/*") ||
			strings.HasPrefix(t, "*") ||
			strings.HasPrefix(t, "*/") ||
			(strings.HasPrefix(t, "#") && !strings.HasPrefix(t, "#include")) ||
			strings.HasPrefix(t, "@") ||
			strings.HasPrefix(t, "template") {
			start = j
			continue
		}
		break
	}
	return start
}

func lineIndent(line string) int {
	n := 0
	for i := 0; i < len(line); i++ {
		if line[i] == ' ' {
			n++
		} else if line[i] == '\t' {
			n += 4
		} else {
			break
		}
	}
	return n
}

func detectDef(lines []string, i int, lang Lang) (string, bool) {
	line := lines[i]
	indent := lineIndent(line)
	t := strings.TrimSpace(line)
	if t == "" || strings.HasPrefix(t, "//") || strings.HasPrefix(t, "/*") || strings.HasPrefix(t, "*") || strings.HasPrefix(t, "#") {
		return "", false
	}

	switch lang {
	case LangGo:
		if indent != 0 {
			return "", false
		}
		return detectGoDef(t)
	case LangPython:
		if indent > 4 {
			return "", false
		}
		return detectPythonDef(t)
	case LangRust:
		if indent > 4 {
			return "", false
		}
		if name, ok := detectRustDef(t); ok {
			return name, true
		}
		return detectCDef(lines, i, t)
	case LangC:
		if indent > 4 {
			return "", false
		}
		return detectCDef(lines, i, t)
	case LangScript:
		if indent > 4 {
			return "", false
		}
		return detectScriptDef(lines, i, t, indent)
	case LangShell:
		if indent > 4 {
			return "", false
		}
		return detectShellDef(t)
	}
	return "", false
}

func extractIdent(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			b.WriteByte(c)
		} else {
			break
		}
	}
	return b.String()
}

func detectGoDef(t string) (string, bool) {
	if rest, ok := strings.CutPrefix(t, "func "); ok {
		rest = strings.TrimSpace(rest)
		if strings.HasPrefix(rest, "(") {
			// Method receiver: func (r *Type) Method(
			closeIdx := strings.IndexByte(rest, ')')
			if closeIdx < 0 {
				return "", false
			}
			recv := strings.TrimSpace(rest[1:closeIdx])
			if br := strings.IndexByte(recv, '['); br >= 0 {
				recv = strings.TrimSpace(recv[:br])
			}
			fields := strings.Fields(recv)
			typeName := ""
			if len(fields) > 0 {
				typeName = strings.TrimLeft(fields[len(fields)-1], "*")
			}
			after := strings.TrimSpace(rest[closeIdx+1:])
			meth := extractIdent(after)
			if meth == "" {
				return "", false
			}
			if typeName != "" {
				return typeName + "." + meth, true
			}
			return meth, true
		}
		name := extractIdent(rest)
		if name != "" {
			return name, true
		}
		return "", false
	}
	if rest, ok := strings.CutPrefix(t, "type "); ok {
		name := extractIdent(strings.TrimSpace(rest))
		if name != "" {
			return name, true
		}
	}
	return "", false
}

func detectPythonDef(t string) (string, bool) {
	for _, pfx := range []string{"async def ", "def ", "class "} {
		if rest, ok := strings.CutPrefix(t, pfx); ok {
			name := extractIdent(strings.TrimSpace(rest))
			if name != "" {
				return name, true
			}
		}
	}
	return "", false
}

func detectRustDef(t string) (string, bool) {
	s := t
	for _, vis := range []string{"pub(crate) ", "pub(super) ", "pub ", "async ", "unsafe ", "const ", "extern \"C\" "} {
		s = strings.TrimPrefix(s, vis)
	}
	for _, kw := range []string{"fn ", "struct ", "enum ", "trait ", "impl ", "impl<"} {
		if rest, ok := strings.CutPrefix(s, kw); ok {
			rest = strings.TrimSpace(rest)
			if kw == "impl " || kw == "impl<" {
				// impl<T> Type or impl Trait for Type
				if kw == "impl<" {
					if end := strings.IndexByte(rest, '>'); end >= 0 {
						rest = strings.TrimSpace(rest[end+1:])
					}
				} else if strings.HasPrefix(rest, "<") {
					if end := strings.IndexByte(rest, '>'); end >= 0 {
						rest = strings.TrimSpace(rest[end+1:])
					}
				}
				fields := strings.Fields(rest)
				if len(fields) > 0 {
					if ident := extractIdent(fields[0]); ident != "" {
						return ident, true
					}
					return strings.TrimRight(fields[0], "{<"), true
				}
			}
			name := extractIdent(rest)
			if name != "" {
				return name, true
			}
		}
	}
	return "", false
}

var controlKeywords = map[string]struct{}{
	"if": {}, "else": {}, "for": {}, "while": {}, "switch": {}, "catch": {},
	"return": {}, "sizeof": {}, "typeof": {}, "delete": {}, "new": {}, "throw": {},
}

func detectCDef(lines []string, i int, t string) (string, bool) {
	// class/struct with { within 3 lines
	for _, kw := range []string{"class ", "struct "} {
		if rest, ok := strings.CutPrefix(t, kw); ok {
			name := extractIdent(strings.TrimSpace(rest))
			if name != "" && hasCharWithin(lines, i, 3, '{', ';') {
				return name, true
			}
		}
	}
	// type name(...) with { before ; within 6 lines
	paren := strings.IndexByte(t, '(')
	if paren <= 0 {
		return "", false
	}
	beforeParen := strings.TrimSpace(t[:paren])
	if beforeParen == "" {
		return "", false
	}
	// Skip trailing template argument list like specializedFoo<int>.
	if strings.HasSuffix(beforeParen, ">") {
		depth := 0
		k := len(beforeParen) - 1
		for k >= 0 {
			if beforeParen[k] == '>' {
				depth++
			} else if beforeParen[k] == '<' {
				depth--
				if depth == 0 {
					beforeParen = strings.TrimSpace(beforeParen[:k])
					break
				}
			}
			k--
		}
	}
	// Find last identifier before '('.
	end := len(beforeParen)
	start := end
	for start > 0 {
		c := beforeParen[start-1]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == ':' {
			start--
		} else {
			break
		}
	}
	fullName := beforeParen[start:end]
	if fullName == "" {
		return "", false
	}
	shortName := fullName
	if idx := strings.LastIndex(fullName, "::"); idx >= 0 {
		shortName = fullName[idx+2:]
	}
	if _, isCtrl := controlKeywords[shortName]; isCtrl || isStopword(strings.ToLower(shortName)) {
		return "", false
	}
	prefix := strings.TrimSpace(beforeParen[:start])
	if prefix == "" && !strings.Contains(fullName, "::") {
		return "", false
	}
	if hasCharWithin(lines, i, 6, '{', ';') {
		return fullName, true
	}
	return "", false
}

func hasCharWithin(lines []string, startIdx, maxLines int, target, stop byte) bool {
	limit := min(startIdx+maxLines, len(lines))
	for j := startIdx; j < limit; j++ {
		for k := 0; k < len(lines[j]); k++ {
			c := lines[j][k]
			if c == target {
				return true
			}
			if c == stop {
				return false
			}
		}
	}
	return false
}

func detectScriptDef(lines []string, i int, t string, indent int) (string, bool) {
	s := t
	for {
		trimmed := false
		for _, mod := range []string{"export ", "default ", "declare ", "abstract ", "async "} {
			if rest, ok := strings.CutPrefix(s, mod); ok {
				s = strings.TrimSpace(rest)
				trimmed = true
			}
		}
		if !trimmed {
			break
		}
	}
	for _, kw := range []string{"function* ", "function ", "class ", "interface ", "enum ", "const enum ", "namespace "} {
		if rest, ok := strings.CutPrefix(s, kw); ok {
			name := extractIdent(strings.TrimSpace(rest))
			if name != "" {
				return name, true
			}
		}
	}
	if rest, ok := strings.CutPrefix(s, "type "); ok {
		name := extractIdent(strings.TrimSpace(rest))
		if name != "" {
			after := strings.TrimSpace(rest[len(name):])
			if strings.HasPrefix(after, "=") || strings.HasPrefix(after, "<") {
				return name, true
			}
		}
	}
	for _, kw := range []string{"const ", "let ", "var "} {
		if rest, ok := strings.CutPrefix(s, kw); ok {
			name := extractIdent(strings.TrimSpace(rest))
			if name != "" {
				after := strings.TrimSpace(rest[len(name):])
				if strings.HasPrefix(after, ":") {
					if eqIdx := strings.IndexByte(after, '='); eqIdx >= 0 {
						after = after[eqIdx:]
					}
				}
				if eq, ok := strings.CutPrefix(after, "="); ok {
					rhs := strings.TrimSpace(eq)
					if strings.HasPrefix(rhs, "function") || strings.HasPrefix(rhs, "async") ||
						strings.HasPrefix(rhs, "(") || strings.Contains(rhs, "=>") {
						return name, true
					}
				}
			}
		}
	}
	// Indented (1..=4 spaces) class methods.
	if indent >= 1 && indent <= 4 {
		m := s
		for {
			trimmed := false
			for _, mod := range []string{"public ", "private ", "protected ", "static ", "override ", "readonly ", "get ", "set "} {
				if rest, ok := strings.CutPrefix(m, mod); ok {
					m = strings.TrimSpace(rest)
					trimmed = true
				}
			}
			if !trimmed {
				break
			}
		}
		name := extractIdent(m)
		if name != "" {
			if _, isCtrl := controlKeywords[name]; !isCtrl {
				after := strings.TrimSpace(m[len(name):])
				if strings.HasPrefix(after, "(") || strings.HasPrefix(after, "<") {
					if hasBraceOrArrowWithin(lines, i, 6) {
						return name, true
					}
				}
			}
		}
	}
	return "", false
}

func hasBraceOrArrowWithin(lines []string, startIdx, maxLines int) bool {
	limit := min(startIdx+maxLines, len(lines))
	for j := startIdx; j < limit; j++ {
		l := lines[j]
		for k := 0; k < len(l); k++ {
			if l[k] == '{' {
				return true
			}
			if l[k] == ';' {
				return false
			}
			if l[k] == '=' && k+1 < len(l) && l[k+1] == '>' {
				return true
			}
		}
	}
	return false
}

func detectShellDef(t string) (string, bool) {
	if rest, ok := strings.CutPrefix(t, "function "); ok {
		name := extractIdent(strings.TrimSpace(rest))
		if name != "" {
			return name, true
		}
	}
	if paren := strings.Index(t, "()"); paren > 0 {
		name := strings.TrimSpace(t[:paren])
		if name != "" && !strings.ContainsFunc(name, unicode.IsSpace) {
			after := strings.TrimSpace(t[paren+2:])
			if strings.HasPrefix(after, "{") || strings.HasPrefix(after, "(") {
				return name, true
			}
		}
	}
	return "", false
}
