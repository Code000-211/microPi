package docsearch

// Ports microPi packages/docsearch/extension/index.ts: the project_search tool, auto-recall, the definition hint
// for rg and grep, and /docs. µPi: the Rust addon's index is this package's Go index.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// SmallProjectFiles is how many files a small project has at most: the system prompt lists them all, and ls, cat
// and rg find anything in it as fast, so project_search is left out.
const SmallProjectFiles = 150

// ToolDescription, ToolPromptSnippet, ToolPromptGuidelines and ToolParameters are project_search's declaration.
const (
	ToolDescription = "Search this project's code and documentation and the configured doc folders. Returns the best-matching " +
		"sections with path and line range: a doc section under its headings, or a whole function, class or struct " +
		"under its name. A query naming a function or type returns its definition first. Ranks by terms and by the " +
		"names the query mentions (identifiers, file names, flags, products). ~1 ms."
	ToolPromptSnippet = "Find definitions (functions, classes) and doc sections by name or keywords; returns path:lines"
	ToolParameters    = `{"type":"object","required":["query"],"properties":{
	"query":{"type":"string","description":"What to find: a question or keywords; names and identifiers help most"},
	"k":{"type":"integer","minimum":1,"maximum":20,"description":"Results to return (default 6: the best three with their text, the rest as path:lines)"},
	"path":{"type":"string","description":"Only files under this path (relative to the project or absolute)"}
}}`
	ToolOutputSchema = `{"type":"object","properties":{
	"hits":{"type":"array","items":{"type":"object","properties":{
		"path":{"type":"string"},
		"lineStart":{"type":"integer"},
		"lineEnd":{"type":"integer"},
		"heading":{"type":"string"},
		"text":{"type":"string"},
		"truncated":{"type":"boolean"}
	}}}
}}`
)

// ToolPromptGuidelines are project_search's prompt guidelines.
var ToolPromptGuidelines = []string{
	"To find where a function, class or struct is defined, or what the docs, specs, READMEs or notes say about something, use project_search before grep, rg, find or read.",
	"project_search ranks by words, not meaning: put the names and identifiers you know in the query (function names, flags, file names, error text); a description in other words than the code uses finds little, so then try likely identifiers or use rg.",
	"Use rg instead for exact-text matches across all files, such as every call site of a function.",
	"project_search shows the best three results with their text and lists the rest as path:lines: read a listed result's lines only when its heading looks like the answer. For several lookups, run several project_search calls at once rather than one after another.",
}

// IsDisabled reports whether docsearch is disabled via PI_DOCSEARCH=off: no tool, no recall, no hint.
func IsDisabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("PI_DOCSEARCH")), "off")
}

// Project is what index.ts start() sets up for a working directory: the settings, the project root (base) and the
// index over the project and the configured roots. Engine is nil when there is nothing to index.
type Project struct {
	Settings Settings
	Base     string
	Engine   *Engine
}

var (
	enginesMu sync.Mutex
	engines   = make(map[string]*Engine)
)

// OpenProject reads the settings and returns the shared index for cwd (index.ts start): the project's git root, else
// cwd, unless that is the home folder or / or `project` is off, plus the configured roots that exist.
func OpenProject(cwd string) Project {
	settings := LoadSettings()
	var root string
	if settings.Project {
		root = ResolveRoot(cwd)
	}
	var roots []string
	add := func(r string) {
		if r == "" || slices.Contains(roots, r) {
			return
		}
		if _, err := os.Stat(r); err == nil {
			roots = append(roots, r)
		}
	}
	add(root)
	for _, r := range settings.Roots {
		if abs, err := filepath.Abs(expandHome(r)); err == nil {
			add(abs)
		}
	}
	project := Project{Settings: settings, Base: root}
	if len(roots) == 0 {
		return project
	}
	extensions := normalizeExtensions(settings.IndexedExtensions())
	key := strings.Join(roots, "|") + "\x00" + strings.Join(extensions, ",")
	enginesMu.Lock()
	defer enginesMu.Unlock()
	if eng, ok := engines[key]; ok && !eng.closed.Load() {
		project.Engine = eng
		return project
	}
	project.Engine = NewEngineWithExtensions(roots, extensions)
	engines[key] = project.Engine
	return project
}

// RebuildProject is `/docs rebuild` (index.ts start(ctx) again): the settings are read again and a new index builds.
func RebuildProject(cwd string) Project {
	project := OpenProject(cwd)
	if project.Engine != nil {
		project.Engine.Rebuild()
	}
	return project
}

var smallProjects sync.Map // cwd -> bool

// IsSmallProject is index.ts `small`, which start() sets when a session starts: the project root has at most
// SmallProjectFiles files, counted from the git root, and no doc folders are configured. The definition hint then
// uses the count of the latest session start for cwd.
func IsSmallProject(cwd string) bool {
	settings := LoadSettings()
	small := false
	if settings.Project && len(settings.Roots) == 0 {
		if root := ResolveRoot(cwd); root != "" {
			small = CountFiles(root, SmallProjectFiles) <= SmallProjectFiles
		}
	}
	smallProjects.Store(cwd, small)
	return small
}

// sessionSmallProject is IsSmallProject as the latest session start for cwd counted it.
func sessionSmallProject(cwd string) bool {
	if cached, ok := smallProjects.Load(cwd); ok {
		return cached.(bool)
	}
	return IsSmallProject(cwd)
}

// ShouldAutoEnable reports whether project_search stays among the active tools for cwd: not with PI_DOCSEARCH=off,
// and not in a small project.
func ShouldAutoEnable(cwd string) bool {
	return !IsDisabled() && !IsSmallProject(cwd)
}

// search is index.ts search: the index's results once its first build is there, waiting up to wait for it.
func (p Project) search(ctx context.Context, query string, k, maxChars int, path string, wait time.Duration) (Results, bool) {
	var abs string
	if path != "" {
		abs = expandHome(path)
		if !filepath.IsAbs(abs) {
			base := p.Base
			if base == "" {
				base, _ = os.Getwd()
			}
			abs = filepath.Join(base, path)
		}
	}
	return p.Engine.Search(ctx, query, SearchOpts{K: k, MaxChars: maxChars, AbsPathPrefix: abs}, wait)
}

// Show is index.ts show: a model-facing path with forward slashes, relative to the project when it is in it, else
// under ~/ when it is in the home folder.
func (p Project) Show(path string) string {
	s := strings.ReplaceAll(path, `\`, "/")
	b := strings.ReplaceAll(p.Base, `\`, "/")
	if b != "" && (s == b || strings.HasPrefix(s, b+"/")) {
		if rel := strings.TrimPrefix(s[len(b):], "/"); rel != "" {
			return rel
		}
		return "."
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		h := strings.ReplaceAll(home, `\`, "/")
		if strings.HasPrefix(s, h+"/") {
			return "~/" + s[len(h)+1:]
		}
	}
	return s
}

func (p Project) hitPath(h Hit) string {
	if h.AbsPath != "" {
		return p.Show(h.AbsPath)
	}
	return h.Path
}

// cutText is index.ts cut: at most max characters (UTF-16 code units, as JavaScript counts), at a line break when one
// is in the second half.
func cutText(text string, max int) (string, bool) {
	if utf16Len(text) <= max {
		return text, false
	}
	end := -1 // lastIndexOf("\n", max), in code units
	units := 0
	for _, r := range text {
		if units > max {
			break
		}
		if r == '\n' {
			end = units
		}
		units += utf16.RuneLen(r)
	}
	if float64(end) < float64(max)/2 {
		end = max
	}
	return utf16Prefix(text, end), true
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// utf16Prefix is the first n code units of s; a character that n splits is left out.
func utf16Prefix(s string, n int) string {
	units := 0
	for i, r := range s {
		size := utf16.RuneLen(r)
		if units+size > n {
			return s[:i]
		}
		units += size
	}
	return s
}

// Format is index.ts format: each hit with the text chars[i] allows (all of it when chars is nil); past chars, a
// pointer.
func (p Project) Format(hits []Hit, chars []int) string {
	var full, pointers []string
	for i, h := range hits {
		where := fmt.Sprintf("%s:%d-%d", p.hitPath(h), h.LineStart, h.LineEnd)
		if h.Heading != "" {
			where += " · " + h.Heading
		}
		limit := math.MaxInt
		if chars != nil {
			if i >= len(chars) {
				pointers = append(pointers, "   "+where)
				continue
			}
			limit = chars[i]
		}
		text, shortened := cutText(h.Text, limit)
		more := ""
		if h.Truncated || shortened {
			more = fmt.Sprintf("\n[cut — read %s lines %d-%d for the rest]", p.hitPath(h), h.LineStart, h.LineEnd)
		}
		full = append(full, "── "+where+"\n"+text+more)
	}
	out := strings.Join(full, "\n\n")
	if len(pointers) > 0 {
		out += "\n\nMore matches (read the lines to see them):\n" + strings.Join(pointers, "\n")
	}
	return out
}

func formatMillis(micros int64) string { return fmt.Sprintf("%.1f", float64(micros)/1000) }

// ToolParams are project_search's arguments.
type ToolParams struct {
	Query string `json:"query"`
	K     *int   `json:"k,omitempty"`
	Path  string `json:"path,omitempty"`
}

// ExecuteTool is project_search's execute for cwd.
func ExecuteTool(ctx context.Context, cwd string, params ToolParams) (agent.AgentToolResult, error) {
	p := OpenProject(cwd)
	if p.Engine == nil {
		return agent.AgentToolResult{}, fmt.Errorf("Nothing to search: no project root here and no \"roots\" in %s.", SettingsPath())
	}
	k := p.Settings.Results
	if params.K != nil {
		k = *params.K
	}
	maxChars := 200
	for _, c := range p.Settings.ResultChars {
		maxChars = max(maxChars, c)
	}
	r, ready := p.search(ctx, params.Query, k, maxChars, params.Path, 5*time.Second)
	if !ready {
		return agent.AgentToolResult{}, errors.New("The index is still being built; try again in a moment.")
	}
	type structuredHit struct {
		Path      string `json:"path"`
		LineStart uint32 `json:"lineStart"`
		LineEnd   uint32 `json:"lineEnd"`
		Heading   string `json:"heading"`
		Text      string `json:"text"`
		Truncated bool   `json:"truncated"`
	}
	hits := make([]structuredHit, 0, len(r.Hits))
	for _, h := range r.Hits {
		hits = append(hits, structuredHit{p.hitPath(h), h.LineStart, h.LineEnd, h.Heading, h.Text, h.Truncated})
	}
	var text string
	if len(hits) > 0 {
		names := ""
		if len(r.Entities) > 0 {
			names = "; names: " + strings.Join(r.Entities, ", ")
		}
		text = fmt.Sprintf("%d sections (%s ms%s)\n\n%s", len(hits), formatMillis(r.Micros), names, p.Format(r.Hits, p.Settings.ResultChars))
	} else {
		text = `No sections match "` + params.Query + `". Try other words or names.`
	}
	entities := r.Entities
	if entities == nil {
		entities = []string{}
	}
	structured, _ := json.Marshal(map[string]any{"hits": hits})
	return agent.AgentToolResult{
		Content:           []ai.ToolResultMessageContent{ai.TextContent{Text: text}},
		StructuredContent: structured,
		Details:           map[string]any{"count": len(hits), "micros": r.Micros, "entities": entities},
	}, nil
}

// RecallCustomType is the custom message type auto-recall adds.
const RecallCustomType = "docsearch-recall"

// Recall is the message auto-recall adds before a run.
type Recall struct {
	Content string
	Places  []string
	Micros  int64
}

// AutoRecall is index.ts before_agent_start: for a typed prompt, the doc sections (code is looked up when needed)
// that cover nearly all of the prompt's rare terms or mention a name the prompt names, added before the run. ok is
// false when nothing is added.
func AutoRecall(ctx context.Context, cwd, prompt string) (Recall, bool) {
	if IsDisabled() {
		return Recall{}, false
	}
	p := OpenProject(cwd)
	s := p.Settings
	if !s.AutoRecall || prompt == "" || p.Engine == nil {
		return Recall{}, false
	}
	prompt = strings.TrimSpace(prompt)
	if strings.HasPrefix(prompt, "/") || s.AutoRecallHits <= 0 {
		return Recall{}, false
	}
	r, ready := p.search(ctx, prompt, s.AutoRecallHits*2, s.AutoRecallChars/s.AutoRecallHits, "", 300*time.Millisecond)
	if !ready {
		return Recall{}, false
	}
	var hits []Hit
	for _, h := range r.Hits {
		if isCodePath(h.Path) {
			continue
		}
		if (h.Named > 0 && h.Coverage >= s.AutoRecallNamedCoverage) || (r.QTerms >= 3 && h.Coverage >= s.AutoRecallMinCoverage) {
			hits = append(hits, h)
		}
	}
	hits = hits[:min(len(hits), s.AutoRecallHits)]
	if len(hits) == 0 {
		return Recall{}, false
	}
	places := make([]string, len(hits))
	for i, h := range hits {
		places[i] = fmt.Sprintf("%s:%d", p.hitPath(h), h.LineStart)
	}
	return Recall{
		Content: "<doc_search_auto>\nSections from the docs that match this prompt (project_search finds more):\n\n" + p.Format(hits, nil) + "\n</doc_search_auto>",
		Places:  places,
		Micros:  r.Micros,
	}, true
}

// isCodePath is index.ts isCode: the text after the last dot is one of CodeExtensions.
func isCodePath(p string) bool {
	return slices.Contains(CodeExtensions, strings.ToLower(p[strings.LastIndexByte(p, '.')+1:]))
}

var definitionKeywords = regexp.MustCompile(`^(def|fn|class|struct|function|func|impl|type|interface|enum|trait|const|let|var|pub)$`)

// shellWords splits a command into words, quotes removed; `|`, `;`, `&` and newlines become words of their own.
func shellWords(command string) []string {
	var words []string
	var word strings.Builder
	quote := rune(0)
	inWord := false
	for _, ch := range command {
		switch {
		case quote != 0:
			if ch == quote {
				quote = 0
			} else {
				word.WriteRune(ch)
			}
		case ch == '\'' || ch == '"':
			quote = ch
			inWord = true
		case isJSSpace(ch) || strings.ContainsRune("|;&()", ch):
			if inWord {
				words = append(words, word.String())
			}
			if ch == '\n' || strings.ContainsRune("|;&", ch) {
				words = append(words, string(ch))
			}
			word.Reset()
			inWord = false
		default:
			word.WriteRune(ch)
			inWord = true
		}
	}
	if inWord {
		words = append(words, word.String())
	}
	return words
}

// isJSSpace is JavaScript's \s.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// searchValueOptions are grep/rg options that take a value, which is not the pattern.
var searchValueOptions = []string{"-g", "--glob", "-t", "--type", "-T", "--type-not", "-A", "-B", "-C", "-m", "--max-count", "--include", "--exclude", "--exclude-dir", "-f"}

var (
	regexDecoration = regexp.MustCompile(`\\[bB<>]|\\s[+*]?|[\^$]`)
	callSuffix      = regexp.MustCompile(`\\?\(.*$|:$`)
	identifier      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{2,}$`)
)

// SearchedName is index.ts searchedName: the name a command searches for with rg, grep or git grep, when its pattern
// is one identifier (optionally after a definition keyword, as in `def foo` or `fn foo\(`).
func SearchedName(command string) string {
	words := shellWords(command)
	for i := range words {
		name := words[i][strings.LastIndexByte(words[i], '/')+1:]
		isGit := name == "git" && i+1 < len(words) && words[i+1] == "grep"
		if name != "rg" && name != "grep" && name != "egrep" && !isGit {
			continue
		}
		start := i + 1
		if name == "git" {
			start = i + 2
		}
		pattern, found := "", false
		for j := start; j < len(words) && !strings.Contains("|;&\n", words[j]); j++ {
			arg := words[j]
			if arg == "-e" || arg == "--regexp" {
				if j+1 < len(words) {
					pattern, found = words[j+1], true
				}
				break
			}
			if slices.Contains(searchValueOptions, arg) {
				j++
			} else if !strings.HasPrefix(arg, "-") {
				pattern, found = arg, true
				break
			}
		}
		if !found || pattern == "" {
			continue
		}
		plain := regexDecoration.ReplaceAllString(pattern, " ")
		plain = strings.TrimFunc(callSuffix.ReplaceAllString(plain, ""), isJSSpace)
		var kept []string
		for _, w := range strings.FieldsFunc(plain, isJSSpace) {
			if !definitionKeywords.MatchString(w) {
				kept = append(kept, w)
			}
		}
		if len(kept) == 1 && identifier.MatchString(kept[0]) {
			return kept[0]
		}
	}
	return ""
}

// DefinitionHint is index.ts tool_result: for a bash search of one name with rg or grep in a large project, the
// places the index knows the name is defined, unless output already shows them. It waits for no build. ok is false
// when nothing is added.
func DefinitionHint(ctx context.Context, cwd, command, output string) (string, bool) {
	if IsDisabled() || sessionSmallProject(cwd) {
		return "", false
	}
	name := SearchedName(command)
	if name == "" {
		return "", false
	}
	p := OpenProject(cwd)
	if p.Engine == nil {
		return "", false
	}
	r, ready := p.search(ctx, name, 5, 200, "", 0)
	if !ready {
		return "", false
	}
	defined := regexp.MustCompile(`(^|[^A-Za-z0-9_])` + name + `([^A-Za-z0-9_]|$)`)
	var places []string
	for _, h := range r.Hits {
		if len(places) == 2 {
			break
		}
		if !isCodePath(h.Path) || !defined.MatchString(h.Heading) || strings.Contains(output, fmt.Sprintf("%s:%d:", p.hitPath(h), h.LineStart)) {
			continue
		}
		places = append(places, fmt.Sprintf("%s:%d-%d · %s", p.hitPath(h), h.LineStart, h.LineEnd, h.Heading))
	}
	if len(places) == 0 {
		return "", false
	}
	return "\n[project_search] Where " + name + " is defined:\n" + strings.Join(places, "\n"), true
}

// HandleDocsCommand serves /docs: `<query>` searches, nothing shows the status, `rebuild` builds the index again
// and `auto on|off` switches auto-recall.
func HandleDocsCommand(args string, cwd string) string {
	if IsDisabled() {
		return "docsearch: disabled (PI_DOCSEARCH=off)"
	}
	a := strings.TrimSpace(args)
	if a == "auto on" || a == "auto off" {
		if err := SaveSettingsPatch(map[string]any{"autoRecall": a == "auto on"}); err != nil {
			return fmt.Sprintf("docsearch: failed to save settings: %v", err)
		}
		return "docsearch auto-recall " + onOff(LoadSettings().AutoRecall)
	}
	if a == "rebuild" {
		RebuildProject(cwd)
		return "docsearch: rebuilding"
	}
	p := OpenProject(cwd)
	if p.Engine == nil {
		return fmt.Sprintf("docsearch: nothing to index here. Add \"roots\" in %s.", SettingsPath())
	}
	if a == "" {
		st := p.Engine.Stats()
		state := "building"
		if st.Ready {
			state = fmt.Sprintf("%d files, %d sections, %d names, built in %d ms", st.Files, st.Sections, st.Entities, st.BuildMs)
		}
		roots := make([]string, len(st.Roots))
		for i, root := range st.Roots {
			roots[i] = p.Show(root)
		}
		return fmt.Sprintf("docsearch: %s\nroots: %s\nauto-recall: %s · settings: %s", state, strings.Join(roots, ", "), onOff(p.Settings.AutoRecall), SettingsPath())
	}
	r, ready := p.search(context.Background(), a, 8, 200, "", 5*time.Second)
	if !ready {
		return "docsearch: still building"
	}
	lines := make([]string, len(r.Hits))
	for i, h := range r.Hits {
		lines[i] = fmt.Sprintf("%s:%d-%d  %s", p.hitPath(h), h.LineStart, h.LineEnd, h.Heading)
	}
	return fmt.Sprintf("%d sections in %s ms\n%s", len(lines), formatMillis(r.Micros), strings.Join(lines, "\n"))
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// NewTool returns the project_search AgentTool for cwd, or nil when docsearch is off or the project is small.
func NewTool(cwd string) agent.AgentTool {
	if !ShouldAutoEnable(cwd) {
		return nil
	}
	return &agentTool{cwd: cwd}
}

// NewToolForce returns the project_search AgentTool for cwd regardless of project file count.
func NewToolForce(cwd string) agent.AgentTool {
	return &agentTool{cwd: cwd}
}

type agentTool struct {
	cwd string
}

func (t *agentTool) Name() string                           { return "project_search" }
func (t *agentTool) Label() string                          { return "Project search" }
func (t *agentTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }
func (t *agentTool) OutputSchema() json.RawMessage          { return json.RawMessage(ToolOutputSchema) }

func (t *agentTool) Schema() ai.ToolSchema {
	var params map[string]any
	_ = json.Unmarshal([]byte(ToolParameters), &params)
	return ai.ToolSchema{Name: "project_search", Description: ToolDescription, Parameters: params, PromptGuidelines: slices.Clone(ToolPromptGuidelines)}
}

func (t *agentTool) BorrowedSchema() ai.ToolSchema { return t.Schema() }

func (t *agentTool) Execute(ctx context.Context, _ string, rawParams json.RawMessage, _ agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	var p ToolParams
	if err := json.Unmarshal(rawParams, &p); err != nil {
		return agent.AgentToolResult{}, fmt.Errorf("project_search: invalid params: %w", err)
	}
	return ExecuteTool(ctx, t.cwd, p)
}

// NotifyFileChanged tells the open indexes that cover path that it changed (the agent wrote it): their next search checks for changes at
// once instead of after refreshThrottle.
func NotifyFileChanged(path string) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return
	}
	enginesMu.Lock()
	open := make([]*Engine, 0, len(engines))
	for _, eng := range engines {
		open = append(open, eng)
	}
	enginesMu.Unlock()
	for _, eng := range open {
		for _, root := range eng.roots {
			if rel, err := filepath.Rel(root, abs); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				eng.mu.Lock()
				eng.lastCheck = time.Time{}
				eng.notified = true
				eng.mu.Unlock()
				break
			}
		}
	}
}
