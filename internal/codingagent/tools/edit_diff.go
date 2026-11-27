// Package tools: edit-diff helpers.
//
// Ports packages/coding-agent/src/core/tools/edit-diff.ts:
// line-ending detection, BOM stripping, fuzzy matching, and the
// position-based multi-file edit application algorithm.
package tools

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode/utf16"

	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/sortx"
	"github.com/MichaelKinsy/PiG/internal/text"
	"golang.org/x/text/unicode/norm"
)

// SplitBOM splits a leading UTF-8 BOM from s, returning (bom, rest).
func SplitBOM(s string) (string, string) {
	return text.SplitBom(s)
}

// DetectLineEnding returns "\r\n" if the first line ending in content is CRLF, otherwise "\n".
func DetectLineEnding(content string) string {
	crlfIdx := strings.Index(content, "\r\n")
	lfIdx := strings.Index(content, "\n")
	if lfIdx == -1 || crlfIdx == -1 {
		return "\n"
	}
	if crlfIdx < lfIdx {
		return "\r\n"
	}
	return "\n"
}

// NormalizeToLF converts \r\n and stray \r to \n.
func NormalizeToLF(s string) string {
	return normalizeToLF(s)
}

// normalizeToLF converts \r\n and stray \r to \n.
func normalizeToLF(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s
}

// RestoreLineEndings converts \n to ending ("\r\n" or "\n").
func RestoreLineEndings(s, ending string) string {
	if ending == "\r\n" {
		return strings.ReplaceAll(s, "\n", "\r\n")
	}
	return s
}

// NormalizeForFuzzyMatch normalizes text for fuzzy edit matching with an ASCII fast path.
func NormalizeForFuzzyMatch(s string) string {
	asciiOnly := true
	hasTrailingWS := false
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b >= utf8.RuneSelf {
			asciiOnly = false
			break
		}
		if (b == ' ' || b == '\t' || b == '\r' || b == '\v' || b == '\f') && (i+1 == len(s) || s[i+1] == '\n') {
			hasTrailingWS = true
		}
	}
	if asciiOnly {
		if !hasTrailingWS {
			return s
		}
		lines := strings.Split(s, "\n")
		for i, line := range lines {
			lines[i] = strings.TrimRight(line, " \t\r\v\f")
		}
		return strings.Join(lines, "\n")
	}
	s = norm.NFKC.String(s)
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t\r\v\f")
	}
	return fuzzyPunctReplacer.Replace(strings.Join(lines, "\n"))
}

var fuzzyPunctReplacer = strings.NewReplacer(
	"\u2018", "'", "\u2019", "'", "\u201A", "'", "\u201B", "'",
	"\u201C", "\"", "\u201D", "\"", "\u201E", "\"", "\u201F", "\"",
	"\u2010", "-", "\u2011", "-", "\u2012", "-",
	"\u2013", "-", "\u2014", "-", "\u2015", "-", "\u2212", "-",
	"\u00A0", " ",
	"\u2002", " ", "\u2003", " ", "\u2004", " ", "\u2005", " ",
	"\u2006", " ", "\u2007", " ", "\u2008", " ", "\u2009", " ", "\u200A", " ",
	"\u202F", " ", "\u205F", " ", "\u3000", " ",
)

// fuzzyMatchResult mirrors upstream FuzzyMatchResult.
type fuzzyMatchResult struct {
	found                 bool
	index                 int
	matchLength           int
	usedFuzzyMatch        bool
	contentForReplacement string
}

// fuzzyContent is the content an edit is matched against and its normalizeForFuzzyMatch form, which is computed once however many edits look at it (a 167 KB file with 50 edits normalized it 100 times). normalizeForFuzzyMatch is idempotent, so the normalized form of already-normalized content is itself.
type fuzzyContent struct {
	text       string
	normalized string
	ready      bool
}

func (c *fuzzyContent) fuzzy() string {
	if !c.ready {
		c.normalized, c.ready = normalizeForFuzzyMatch(c.text), true
	}
	return c.normalized
}

// fuzzyFindText tries exact match first, then fuzzy. When fuzzy is used,
// contentForReplacement is the normalized content.
func fuzzyFindText(content *fuzzyContent, oldText string) fuzzyMatchResult {
	if i := strings.Index(content.text, oldText); i != -1 {
		return fuzzyMatchResult{
			found:                 true,
			index:                 i,
			matchLength:           len(oldText),
			usedFuzzyMatch:        false,
			contentForReplacement: content.text,
		}
	}
	fc := content.fuzzy()
	fo := normalizeForFuzzyMatch(oldText)
	i := strings.Index(fc, fo)
	if i == -1 {
		return fuzzyMatchResult{contentForReplacement: content.text}
	}
	return fuzzyMatchResult{
		found:                 true,
		index:                 i,
		matchLength:           len(fo),
		usedFuzzyMatch:        true,
		contentForReplacement: fc,
	}
}

// countOccurrences counts occurrences via fuzzy-normalized strings, matching upstream.
func countOccurrences(content *fuzzyContent, oldText string) int {
	fc := content.fuzzy()
	fo := normalizeForFuzzyMatch(oldText)
	if fo == "" {
		// fuzzyContent.split("") yields one element per UTF-16 code unit (none for an empty string), so an oldText that normalizes to nothing "occurs" once less than the content has code units, which the caller reports as a duplicate.
		return len(utf16.Encode([]rune(fc))) - 1
	}
	return strings.Count(fc, fo)
}

// matchedEdit is the internal edit-with-position record.
type matchedEdit struct {
	editIndex   int
	matchIndex  int
	matchLength int
	newText     string
}

// appliedEditsResult mirrors upstream AppliedEditsResult.
type appliedEditsResult struct {
	baseContent string
	newContent  string
	// replacements are the sorted, disjoint replacements of baseContent that turn it into newContent (upstream AppliedEditsResult.replacements).
	replacements []matchedEdit
}

// applyEditsToNormalizedContent ports upstream's algorithm:
//   - normalize each edit's old/new text to LF
//   - reject empty oldText
//   - probe matches against the original normalized content; if any edit
//     needs fuzzy matching, switch baseContent to the fuzzy-normalized form
//   - error: not found / duplicate / overlap / no-change
//   - apply edits in reverse-position order so indices stay valid
func applyEditsToNormalizedContent(normalizedContent string, edits []editEntry, path string) (appliedEditsResult, error) {
	normEdits := make([]editEntry, len(edits))
	for i, e := range edits {
		normEdits[i] = editEntry{
			OldText: normalizeToLF(e.OldText),
			NewText: normalizeToLF(e.NewText),
		}
	}

	for i, e := range normEdits {
		if e.OldText == "" {
			return appliedEditsResult{}, emptyOldTextErr(path, i, len(normEdits))
		}
	}

	// First probe: decide whether any edit needs fuzzy matching.
	usedFuzzyAny := false
	original := &fuzzyContent{text: normalizedContent}
	firstMatches := make([]fuzzyMatchResult, len(normEdits))
	for i, e := range normEdits {
		m := fuzzyFindText(original, e.OldText)
		firstMatches[i] = m
		if m.usedFuzzyMatch {
			usedFuzzyAny = true
		}
	}
	base := original
	if usedFuzzyAny {
		// The base is the normalized form; normalizing it again gives it back.
		base = &fuzzyContent{text: original.fuzzy(), normalized: original.fuzzy(), ready: true}
	}
	baseContent := base.text

	// Second probe: locate each edit in baseContent (with seek_sequence fallback).
	matched := make([]matchedEdit, 0, len(normEdits))
	for i, e := range normEdits {
		m := firstMatches[i]
		if usedFuzzyAny {
			m = fuzzyFindText(base, e.OldText)
		}
		if !m.found {
			trimmedMatch, ok := findTrimmedLineSequence(baseContent, e.OldText, e.NewText)
			if !ok {
				return appliedEditsResult{}, notFoundErr(path, i, len(normEdits))
			}
			if trimmedMatch.occurrences > 1 {
				return appliedEditsResult{}, duplicateErr(path, i, len(normEdits), trimmedMatch.occurrences)
			}
			matched = append(matched, matchedEdit{
				editIndex:   i,
				matchIndex:  trimmedMatch.matchIndex,
				matchLength: trimmedMatch.matchLength,
				newText:     trimmedMatch.adjustedNewText,
			})
			continue
		}
		// Count in baseContent (or its fuzzy normalization, same as upstream).
		occ := countOccurrences(base, e.OldText)
		if occ > 1 {
			return appliedEditsResult{}, duplicateErr(path, i, len(normEdits), occ)
		}
		matched = append(matched, matchedEdit{
			editIndex:   i,
			matchIndex:  m.index,
			matchLength: m.matchLength,
			newText:     e.NewText,
		})
	}

	// Sort ascending by position, check overlaps.
	sortx.Stable(matched, func(a, b matchedEdit) int { return cmp.Compare(a.matchIndex, b.matchIndex) })
	for i := 1; i < len(matched); i++ {
		prev := matched[i-1]
		cur := matched[i]
		if prev.matchIndex+prev.matchLength > cur.matchIndex {
			return appliedEditsResult{}, fmt.Errorf(
				"edits[%d] and edits[%d] overlap in %s. Merge them into one edit or target disjoint regions.",
				prev.editIndex, cur.editIndex, path)
		}
	}

	// A fuzzy match rewrites only the lines its replacements touch, from the normalized base; every other line keeps its original text.
	replacements := matched
	if usedFuzzyAny {
		var err error
		replacements, err = widenReplacementsToLines(normalizedContent, baseContent, matched)
		if err != nil {
			return appliedEditsResult{}, err
		}
	}
	newContent := applyReplacements(normalizedContent, replacements, 0)

	if normalizedContent == newContent {
		return appliedEditsResult{}, noChangeErr(path, len(normEdits))
	}

	return appliedEditsResult{baseContent: normalizedContent, newContent: newContent, replacements: replacements}, nil
}

// splitLinesWithEndings mirrors upstream splitLinesWithEndings
// (content.match(/[^\n]*\n|[^\n]+/g)): each line keeps its "\n".
func splitLinesWithEndings(content string) []string {
	if content == "" {
		return nil
	}
	lines := strings.SplitAfter(content, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// lineSpan mirrors upstream LineSpan.
type lineSpan struct{ start, end int }

// getLineSpans mirrors upstream getLineSpans.
func getLineSpans(content string) []lineSpan {
	lines := splitLinesWithEndings(content)
	spans := make([]lineSpan, len(lines))
	offset := 0
	for i, line := range lines {
		spans[i] = lineSpan{start: offset, end: offset + len(line)}
		offset += len(line)
	}
	return spans
}

var errReplacementOutsideBase = errors.New("Replacement range is outside the base content.")

// getReplacementLineRange mirrors upstream getReplacementLineRange: the
// half-open line range [startLine, endLine) a replacement touches.
func getReplacementLineRange(lines []lineSpan, r matchedEdit) (int, int, error) {
	replacementStart := r.matchIndex
	replacementEnd := r.matchIndex + r.matchLength
	// The spans are contiguous and every one is non-empty, so the line holding an offset is the first whose end is past it.
	startLine := sort.Search(len(lines), func(i int) bool { return lines[i].end > replacementStart })
	if startLine == len(lines) || replacementStart < lines[startLine].start {
		return 0, 0, errReplacementOutsideBase
	}
	endLine := startLine + sort.Search(len(lines)-startLine, func(i int) bool { return lines[startLine+i].end >= replacementEnd })
	if endLine >= len(lines) {
		return 0, 0, errReplacementOutsideBase
	}
	return startLine, endLine + 1, nil
}

// applyReplacements mirrors upstream applyReplacements: replacements sorted
// by position, applied from the end so offsets stay valid.
func applyReplacements(content string, replacements []matchedEdit, offset int) string {
	// The replacements are in ascending order and do not overlap (the caller checked), so one forward pass builds what applying them from the end builds, without copying the content once per replacement.
	var result strings.Builder
	result.Grow(len(content))
	position := 0
	for _, r := range replacements {
		i := r.matchIndex - offset
		//portlint:allow utf16units matchIndex and matchLength are byte offsets from strings.Index and len over this same content (or its line spans), so the slice cuts the same text as Pi's UTF-16 offsets would
		result.WriteString(content[position:i])
		result.WriteString(r.newText)
		position = i + r.matchLength
	}
	//portlint:allow utf16units position is a byte offset produced by the same byte-offset arithmetic as the loop above
	result.WriteString(content[position:])
	return result.String()
}

// widenReplacementsToLines turns replacements matched against baseContent (a normalized view of originalContent) into replacements of
// originalContent that keep its unchanged line blocks: each replacement is widened to the lines it touches, and those lines are
// rewritten from the base; all other lines keep their original text. The actual replacement ranges drive preservation, so duplicate
// normalized lines cannot be aligned to the wrong occurrence. Upstream widenReplacementsToLines.
func widenReplacementsToLines(originalContent, baseContent string, replacements []matchedEdit) ([]matchedEdit, error) {
	originalLines := getLineSpans(originalContent)
	baseLines := getLineSpans(baseContent)
	if len(originalLines) != len(baseLines) {
		return nil, errors.New("Cannot preserve unchanged lines because the base content has a different line count.")
	}
	type group struct {
		startLine, endLine int
		replacements       []matchedEdit
	}
	var groups []*group
	sorted := slices.Clone(replacements)
	sortx.Stable(sorted, func(a, b matchedEdit) int { return a.matchIndex - b.matchIndex })
	for _, r := range sorted {
		startLine, endLine, err := getReplacementLineRange(baseLines, r)
		if err != nil {
			return nil, err
		}
		if n := len(groups); n > 0 && startLine < groups[n-1].endLine {
			current := groups[n-1]
			current.endLine = max(current.endLine, endLine)
			current.replacements = append(current.replacements, r)
			continue
		}
		groups = append(groups, &group{startLine: startLine, endLine: endLine, replacements: []matchedEdit{r}})
	}
	widened := make([]matchedEdit, len(groups))
	for i, g := range groups {
		groupStart := baseLines[g.startLine].start
		groupEnd := baseLines[g.endLine-1].end
		originalStart := originalLines[g.startLine].start
		widened[i] = matchedEdit{
			matchIndex:  originalStart,
			matchLength: originalLines[g.endLine-1].end - originalStart,
			newText:     applyReplacements(baseContent[groupStart:groupEnd], g.replacements, groupStart),
		}
	}
	return widened, nil
}

// applyReplacementsToOriginal applies replacements matched in normalizeToLF(originalContent) to originalContent itself. Text outside the
// replaced ranges keeps its bytes, including its line endings. Line breaks in the new text get the line ending of the line where the
// replaced range starts. The result, normalized to LF, equals the replacements applied to the normalized content.
func applyReplacementsToOriginal(originalContent string, replacements []matchedEdit) string {
	// Only CRLF changes length when normalizing: the LF of the n-th CRLF (at original offset p) is at p - n in the normalized text. Adding
	// the number of CRLFs before a normalized offset gives the original offset, and a range that covers that LF covers the whole CRLF.
	crlfBefore := 0
	nextCrlf := strings.Index(originalContent, "\r\n")
	toOriginal := func(offset int) int {
		for nextCrlf != -1 && nextCrlf-crlfBefore < offset {
			crlfBefore++
			if next := strings.Index(originalContent[nextCrlf+2:], "\r\n"); next == -1 {
				nextCrlf = -1
			} else {
				nextCrlf += 2 + next
			}
		}
		return offset + crlfBefore
	}

	var result strings.Builder
	result.Grow(len(originalContent))
	endsWithCr := false
	push := func(part string) {
		if part == "" {
			return
		}
		// A CR before a seam and an LF after it would merge into one CRLF line break, for example a kept lone CR followed by inserted
		// text that starts with LF, or both sides of a deleted range. Adding an LF turns the CR into a CRLF of its own, so the written
		// file has the line breaks the diff shows.
		if endsWithCr && part[0] == '\n' {
			result.WriteByte('\n')
		}
		result.WriteString(part)
		endsWithCr = part[len(part)-1] == '\r'
	}
	position := 0
	for _, r := range replacements {
		start := toOriginal(r.matchIndex)
		//portlint:allow utf16units start and position are byte offsets of originalContent, from toOriginal over the byte offsets of matchIndex and matchLength
		push(originalContent[position:start])
		ending := getLineEndingAt(originalContent, start)
		if ending == "\n" {
			push(r.newText)
		} else {
			push(strings.ReplaceAll(r.newText, "\n", ending))
		}
		position = toOriginal(r.matchIndex + r.matchLength)
	}
	//portlint:allow utf16units position is a byte offset produced by toOriginal
	push(originalContent[position:])
	return result.String()
}

// getLineEndingAt is the line ending of the line containing offset: its own LF or CRLF, or that of the line before for an unterminated
// last line. A lone CR only counts in files without any LF (classic Mac line endings); elsewhere it is more likely a control character
// inside a line.
func getLineEndingAt(content string, offset int) string {
	var lineFeed int
	//portlint:allow utf16units offset is a byte offset into content, the start of a matched range
	if next := strings.IndexByte(content[offset:], '\n'); next != -1 {
		lineFeed = offset + next
	} else {
		//portlint:allow utf16units offset is a byte offset into content, the start of a matched range
		lineFeed = strings.LastIndexByte(content[:min(offset+1, len(content))], '\n')
	}
	if lineFeed == -1 {
		if strings.Contains(content, "\r") {
			return "\r"
		}
		return "\n"
	}
	if lineFeed > 0 && content[lineFeed-1] == '\r' {
		return "\r\n"
	}
	return "\n"
}

// ─── seek_sequence & indentation normalization ─────────────────────────────────

type trimmedSequenceMatch struct {
	matchIndex      int
	matchLength     int
	occurrences     int
	adjustedNewText string
}

func getLeadingWhitespace(line string) string {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	return line[:i]
}

func gcdInt(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func indentUnit(indents []string, base string) string {
	spaces := 0
	for _, indent := range indents {
		if len(indent) <= len(base) || !strings.HasPrefix(indent, base) {
			continue
		}
		extra := indent[len(base):]
		if strings.HasPrefix(extra, "\t") {
			return "\t"
		}
		onlySpaces := true
		for i := 0; i < len(extra); i++ {
			if extra[i] != ' ' {
				onlySpaces = false
				break
			}
		}
		if onlySpaces && len(extra) > 0 {
			spaces = gcdInt(spaces, len(extra))
		}
	}
	if spaces > 0 {
		return strings.Repeat(" ", spaces)
	}
	return ""
}

func reindentNewTextForMatchedLines(matchedOriginalSlice, oldText, newText string) string {
	hasTrailingNewline := strings.HasSuffix(newText, "\n")
	origLines := strings.Split(strings.TrimSuffix(matchedOriginalSlice, "\n"), "\n")
	oldLines := strings.Split(strings.TrimSuffix(oldText, "\n"), "\n")
	newLines := strings.Split(strings.TrimSuffix(newText, "\n"), "\n")

	mapped := make(map[string]string)
	var origAnchorIndent, oldAnchorIndent string
	hasAnchor := false
	differs := false
	pairCount := min(len(origLines), len(oldLines))
	for i := 0; i < pairCount; i++ {
		if strings.TrimSpace(origLines[i]) == "" || strings.TrimSpace(oldLines[i]) == "" {
			continue
		}
		orig := getLeadingWhitespace(origLines[i])
		old := getLeadingWhitespace(oldLines[i])
		if !hasAnchor {
			origAnchorIndent = orig
			oldAnchorIndent = old
			hasAnchor = true
		}
		if _, ok := mapped[old]; !ok {
			mapped[old] = orig
		}
		if orig != old {
			differs = true
		}
	}
	if !hasAnchor || !differs {
		return newText
	}

	var oldAndNewIndents []string
	for _, l := range oldLines {
		if strings.TrimSpace(l) != "" {
			oldAndNewIndents = append(oldAndNewIndents, getLeadingWhitespace(l))
		}
	}
	for _, l := range newLines {
		if strings.TrimSpace(l) != "" {
			oldAndNewIndents = append(oldAndNewIndents, getLeadingWhitespace(l))
		}
	}
	oldUnit := indentUnit(oldAndNewIndents, oldAnchorIndent)

	var origIndents []string
	for _, l := range origLines {
		if strings.TrimSpace(l) != "" {
			origIndents = append(origIndents, getLeadingWhitespace(l))
		}
	}
	fileUnit := indentUnit(origIndents, origAnchorIndent)
	if fileUnit == "" {
		if strings.Contains(origAnchorIndent, "\t") {
			fileUnit = "\t"
		} else {
			fileUnit = oldUnit
		}
	}

	reindent := func(indent string) (string, bool) {
		if known, ok := mapped[indent]; ok {
			return known, true
		}
		if !strings.HasPrefix(indent, oldAnchorIndent) {
			return "", false
		}
		extra := indent[len(oldAnchorIndent):]
		if oldUnit == "" || fileUnit == "" || oldUnit == fileUnit {
			return origAnchorIndent + extra, true
		}
		levels := 0
		for strings.HasPrefix(extra, oldUnit) {
			extra = extra[len(oldUnit):]
			levels++
		}
		return origAnchorIndent + strings.Repeat(fileUnit, levels) + extra, true
	}

	adjusted := make([]string, len(newLines))
	for i, line := range newLines {
		if strings.TrimSpace(line) == "" {
			adjusted[i] = line
			continue
		}
		indent := getLeadingWhitespace(line)
		if replaced, ok := reindent(indent); ok {
			adjusted[i] = replaced + line[len(indent):]
		} else {
			adjusted[i] = line
		}
	}
	joined := strings.Join(adjusted, "\n")
	if hasTrailingNewline {
		return joined + "\n"
	}
	return joined
}

func findTrimmedLineSequence(content, oldText, newText string) (trimmedSequenceMatch, bool) {
	rawLines := splitLinesWithEndings(content)
	if len(rawLines) == 0 {
		return trimmedSequenceMatch{}, false
	}
	lineOffsets := make([]int, len(rawLines))
	trimmedFileLines := make([]string, len(rawLines))
	offset := 0
	for i, line := range rawLines {
		lineOffsets[i] = offset
		withoutNL := strings.TrimSuffix(line, "\n")
		trimmedFileLines[i] = strings.TrimSpace(NormalizeForFuzzyMatch(withoutNL))
		offset += len(line)
	}

	patternEndsWithNL := strings.HasSuffix(oldText, "\n")
	rawPatternLines := strings.Split(strings.TrimSuffix(oldText, "\n"), "\n")
	if len(rawPatternLines) == 0 || len(rawPatternLines) > len(rawLines) {
		return trimmedSequenceMatch{}, false
	}
	trimmedPatternLines := make([]string, len(rawPatternLines))
	allBlank := true
	for i, line := range rawPatternLines {
		trimmedPatternLines[i] = strings.TrimSpace(NormalizeForFuzzyMatch(line))
		if trimmedPatternLines[i] != "" {
			allBlank = false
		}
	}
	if allBlank {
		return trimmedSequenceMatch{}, false
	}

	maxStart := len(rawLines) - len(trimmedPatternLines)
	firstMatchStartLine := -1
	occurrences := 0
	for i := 0; i <= maxStart; i++ {
		ok := true
		for p := 0; p < len(trimmedPatternLines); p++ {
			if trimmedFileLines[i+p] != trimmedPatternLines[p] {
				ok = false
				break
			}
		}
		if ok {
			if firstMatchStartLine == -1 {
				firstMatchStartLine = i
			}
			occurrences++
		}
	}
	if occurrences == 0 {
		return trimmedSequenceMatch{}, false
	}

	endLineIdx := firstMatchStartLine + len(trimmedPatternLines) - 1
	matchIndex := lineOffsets[firstMatchStartLine]
	lastLine := rawLines[endLineIdx]
	lastLineLen := len(lastLine)
	if !patternEndsWithNL && strings.HasSuffix(lastLine, "\n") {
		lastLineLen--
	}
	matchEnd := lineOffsets[endLineIdx] + lastLineLen
	matchLength := matchEnd - matchIndex
	matchedSlice := content[matchIndex:matchEnd]

	return trimmedSequenceMatch{
		matchIndex:      matchIndex,
		matchLength:     matchLength,
		occurrences:     occurrences,
		adjustedNewText: reindentNewTextForMatchedLines(matchedSlice, oldText, newText),
	}, true
}

// ─── Closest-text description on EditNotFoundError ────────────────────────────

const (
	closestMaxLines      = 20
	closestMaxLineChars  = 200
	closestMinSimilarity = 0.5
)

// EditNotFoundError is returned when an edit's oldText cannot be matched anywhere in the file.
type EditNotFoundError struct {
	Message   string
	EditIndex int
}

func (e *EditNotFoundError) Error() string { return e.Message }

func linePairs(line string) map[uint32]int {
	runes := []rune(line)
	pairs := make(map[uint32]int, max(0, len(runes)-1))
	for i := 0; i+1 < len(runes); i++ {
		pair := (uint32(runes[i]) << 16) | uint32(uint16(runes[i+1]))
		pairs[pair]++
	}
	return pairs
}

func lineSimilarity(a, b string, pairsOfB map[uint32]int) float64 {
	if a == b {
		return 1
	}
	ra := []rune(a)
	rb := []rune(b)
	if len(ra) < 2 || len(rb) < 2 {
		return 0
	}
	if float64(2*(min(len(ra), len(rb))-1))/float64(len(ra)+len(rb)-2) < closestMinSimilarity {
		return 0
	}
	if pairsOfB == nil {
		pairsOfB = linePairs(b)
	}
	used := make(map[uint32]int, len(ra))
	shared := 0
	for i := 0; i+1 < len(ra); i++ {
		pair := (uint32(ra[i]) << 16) | uint32(uint16(ra[i+1]))
		cnt := used[pair]
		if cnt < pairsOfB[pair] {
			shared++
			used[pair] = cnt + 1
		}
	}
	return float64(2*shared) / float64(len(ra)+len(rb)-2)
}

// DescribeClosestText finds the region of content most similar to oldText using Dice bigram
// similarity and exact line anchors, returning a line-numbered hint or "" if nothing is >= 0.5 similar.
func DescribeClosestText(content, oldText string) string {
	fileLines := strings.Split(content, "\n")
	trimmedFile := make([]string, len(fileLines))
	for i, line := range fileLines {
		trimmedFile[i] = strings.TrimSpace(NormalizeForFuzzyMatch(line))
	}
	patternLines := strings.Split(strings.TrimSuffix(normalizeToLF(oldText), "\n"), "\n")
	pattern := make([]string, len(patternLines))
	allBlank := true
	for i, line := range patternLines {
		pattern[i] = strings.TrimSpace(NormalizeForFuzzyMatch(line))
		if pattern[i] != "" {
			allBlank = false
		}
	}
	n := min(len(pattern), len(fileLines))
	if n == 0 || allBlank {
		return ""
	}

	starts := make(map[int]struct{})
	lineIndex := make(map[string][]int)
	for i, t := range trimmedFile {
		if t != "" {
			lineIndex[t] = append(lineIndex[t], i)
		}
	}
	for j := 0; j < n; j++ {
		if len([]rune(pattern[j])) < 4 {
			continue
		}
		for _, i := range lineIndex[pattern[j]] {
			if len(starts) >= 200 {
				break
			}
			s := max(0, min(i-j, len(fileLines)-n))
			starts[s] = struct{}{}
		}
	}
	if len(starts) == 0 {
		first := -1
		for i, p := range pattern {
			if p != "" {
				first = i
				break
			}
		}
		if first >= 0 {
			firstPairs := linePairs(pattern[first])
			type scoredStart struct {
				start int
				score float64
			}
			var best []scoredStart
			for i := 0; i < len(trimmedFile); i++ {
				score := lineSimilarity(trimmedFile[i], pattern[first], firstPairs)
				if score < closestMinSimilarity {
					continue
				}
				best = append(best, scoredStart{
					start: max(0, min(i-first, len(fileLines)-n)),
					score: score,
				})
			}
			sortx.Stable(best, func(a, b scoredStart) int {
				return cmp.Compare(b.score, a.score)
			})
			for i := 0; i < min(5, len(best)); i++ {
				starts[best[i].start] = struct{}{}
			}
		}
	}

	sortedStarts := make([]int, 0, len(starts))
	for s := range starts {
		sortedStarts = append(sortedStarts, s)
	}
	slices.Sort(sortedStarts)

	bestStart := -1
	bestScore := 0.0
	for _, start := range sortedStarts {
		score := 0.0
		for j := 0; j < n; j++ {
			score += lineSimilarity(trimmedFile[start+j], pattern[j], nil)
		}
		score /= float64(n)
		if score > bestScore {
			bestScore = score
			bestStart = start
		}
	}
	if bestStart == -1 || bestScore < closestMinSimilarity {
		return ""
	}

	firstDifference := -1
	for j := 0; j < n; j++ {
		if pattern[j] != trimmedFile[bestStart+j] {
			firstDifference = j
			break
		}
	}
	shown := min(n, closestMaxLines)
	width := len(itoa(bestStart + shown))
	lines := make([]string, shown)
	for j := 0; j < shown; j++ {
		line := fileLines[bestStart+j]
		runes := []rune(line)
		if len(runes) > closestMaxLineChars {
			line = string(runes[:closestMaxLineChars]) + "..."
		}
		lines[j] = fmt.Sprintf("%*d→%s", width, bestStart+j+1, line)
	}
	var rangeDesc string
	if n == 1 {
		rangeDesc = fmt.Sprintf("line %d", bestStart+1)
	} else {
		rangeDesc = fmt.Sprintf("lines %d-%d", bestStart+1, bestStart+n)
	}
	diffNote := ""
	if firstDifference != -1 {
		diffNote = fmt.Sprintf(" It differs from oldText first at line %d.", bestStart+firstDifference+1)
	}
	more := ""
	if shown < n {
		more = fmt.Sprintf("\n[%d more lines]", n-shown)
	}
	return fmt.Sprintf("The closest text in the file is at %s.%s\n%s%s", rangeDesc, diffNote, strings.Join(lines, "\n"), more)
}

// ─── error messages (match upstream wording) ──────────────────────────────────

func notFoundErr(path string, i, total int) error {
	if total == 1 {
		return &EditNotFoundError{
			Message:   fmt.Sprintf("Could not find the exact text in %s. The old text must match exactly including all whitespace and newlines.", path),
			EditIndex: i,
		}
	}
	return &EditNotFoundError{
		Message:   fmt.Sprintf("Could not find edits[%d] in %s. The oldText must match exactly including all whitespace and newlines.", i, path),
		EditIndex: i,
	}
}

func duplicateErr(path string, i, total, occ int) error {
	if total == 1 {
		return fmt.Errorf("Found %d occurrences of the text in %s. The text must be unique. Please provide more context to make it unique.", occ, path)
	}
	return fmt.Errorf("Found %d occurrences of edits[%d] in %s. Each oldText must be unique. Please provide more context to make it unique.", occ, i, path)
}

func emptyOldTextErr(path string, i, total int) error {
	if total == 1 {
		return fmt.Errorf("oldText must not be empty in %s.", path)
	}
	return fmt.Errorf("edits[%d].oldText must not be empty in %s.", i, path)
}

func noChangeErr(path string, total int) error {
	if total == 1 {
		return fmt.Errorf("No changes made to %s. The replacement produced identical content. This might indicate an issue with special characters or the text not existing as expected.", path)
	}
	return fmt.Errorf("No changes made to %s. The replacements produced identical content.", path)
}
