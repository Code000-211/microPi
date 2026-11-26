package docsearch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/gofrs/flock"
)

const (
	SmallProjectFiles  = 150
	checkEvery         = 1 * time.Second
	quietBeforeRebuild = 30 * time.Second
	foldMinFiles       = 20
	foldAfter          = 600 * time.Second
	overlayMaxFiles    = 200
	cacheMaxEntries    = 256
	cachePruneAge      = 30 * 24 * time.Hour
	defaultResultsK    = 6
)

// Settings represents ~/.pi/agent/docsearch.json.
type Settings struct {
	AutoRecall *bool    `json:"autoRecall,omitempty"`
	Results    int      `json:"results,omitempty"`
	Roots      []string `json:"roots,omitempty"`
}

func (s Settings) autoRecallEnabled() bool {
	if s.AutoRecall == nil {
		return true
	}
	return *s.AutoRecall
}

func (s Settings) resultCount() int {
	if s.Results >= 1 && s.Results <= 20 {
		return s.Results
	}
	return defaultResultsK
}

// SettingsPath returns the path to docsearch.json.
func SettingsPath() string {
	if dir := os.Getenv("PI_CODING_AGENT_DIR"); dir != "" {
		return filepath.Join(dir, "docsearch.json")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".", ".pi", "agent", "docsearch.json")
	}
	return filepath.Join(home, ".pi", "agent", "docsearch.json")
}

// LoadSettings reads docsearch.json (returning defaults if missing).
func LoadSettings() Settings {
	b, err := os.ReadFile(SettingsPath())
	if err != nil {
		return Settings{}
	}
	var s Settings
	_ = json.Unmarshal(b, &s)
	return s
}

// SaveSettings writes docsearch.json atomically.
func SaveSettings(s Settings) error {
	p := SettingsPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(b, '\n'), 0o644)
}

func cacheIndexDir() string {
	if d := os.Getenv("DOCSEARCH_CACHE_DIR"); d != "" {
		return d
	}
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "docsearch", "index")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "docsearch", "index")
	}
	return filepath.Join(home, ".cache", "docsearch", "index")
}

func indexCachePath(roots []string) string {
	sorted := append([]string(nil), roots...)
	sort.Strings(sorted)
	h := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	name := hex.EncodeToString(h[:16]) + ".idx"
	return filepath.Join(cacheIndexDir(), name)
}

func pruneCacheDir() {
	dir := cacheIndexDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-cachePruneAge)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

type indexRef struct {
	idx  *Index
	refs atomic.Int64
}

func newIndexRef(idx *Index) *indexRef {
	if idx == nil {
		return nil
	}
	r := &indexRef{idx: idx}
	r.refs.Store(1)
	return r
}

func (r *indexRef) retain() {
	if r != nil {
		r.refs.Add(1)
	}
}

func (r *indexRef) release() error {
	if r == nil {
		return nil
	}
	if r.refs.Add(-1) == 0 && r.idx != nil {
		return r.idx.Close()
	}
	return nil
}

// View is a snapshot of base index + optional overlay.
type View struct {
	Base       *Index
	Overlay    *Overlay
	Generation uint64
	baseRef    *indexRef
}

func (v *View) release() {
	if v != nil && v.baseRef != nil {
		_ = v.baseRef.release()
	}
}

type resultCacheKey struct {
	gen      uint64
	query    string
	k        int
	prefix   string
	maxChars int
}

// Engine manages background index loading, incremental overlay updates, disk persistence, and search caching.
type Engine struct {
	roots   []string
	idxPath string

	mu         sync.RWMutex
	view       *View
	generation uint64
	lastCheck  time.Time
	lastChange time.Time
	building   atomic.Bool

	readyOnce sync.Once
	readyCh   chan struct{}

	cacheMu sync.Mutex
	cache   map[resultCacheKey]Results
	closed  atomic.Bool
	bgWG    sync.WaitGroup
}

// NewEngine creates and starts a background Engine for the given roots.
func NewEngine(roots []string) *Engine {
	normRoots := make([]string, 0, len(roots))
	seen := make(map[string]struct{}, len(roots))
	for _, r := range roots {
		if r == "" {
			continue
		}
		abs, err := filepath.Abs(r)
		if err != nil {
			continue
		}
		if _, ok := seen[abs]; !ok {
			seen[abs] = struct{}{}
			normRoots = append(normRoots, abs)
		}
	}
	e := &Engine{
		roots:   normRoots,
		idxPath: indexCachePath(normRoots),
		readyCh: make(chan struct{}),
		cache:   make(map[resultCacheKey]Results, cacheMaxEntries),
	}
	e.bgWG.Add(1)
	go func() {
		defer e.bgWG.Done()
		e.initBuildOrLoad()
	}()
	return e
}

func (e *Engine) markReady() {
	e.readyOnce.Do(func() {
		close(e.readyCh)
	})
}

// WaitReady blocks until the initial index is ready or ctx is canceled.
func (e *Engine) WaitReady(ctx context.Context) bool {
	select {
	case <-e.readyCh:
		return true
	case <-ctx.Done():
		return false
	}
}

// CurrentView returns the current View (nil if still building).
func (e *Engine) CurrentView() *View {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.view
}

func (e *Engine) acquireView() *View {
	e.mu.RLock()
	v := e.view
	if v != nil && v.baseRef != nil {
		v.baseRef.retain()
	}
	e.mu.RUnlock()
	return v
}

// Close releases mmap resources.
func (e *Engine) Close() error {
	e.closed.Store(true)
	e.bgWG.Wait()
	e.mu.Lock()
	v := e.view
	e.view = nil
	e.mu.Unlock()
	if v != nil && v.baseRef != nil {
		return v.baseRef.release()
	}
	return nil
}

func (e *Engine) initBuildOrLoad() {
	defer e.markReady()
	if len(e.roots) == 0 || e.closed.Load() {
		return
	}
	go pruneCacheDir()

	if idx, err := Load(e.idxPath, e.roots); err == nil {
		e.setView(idx, nil)
		return
	}
	e.rebuildSync(false)
}

func (e *Engine) setView(base *Index, ov *Overlay) {
	e.mu.Lock()
	old := e.view
	e.generation++
	var ref *indexRef
	if old != nil && old.Base == base && old.baseRef != nil {
		ref = old.baseRef
		ref.retain()
	} else {
		ref = newIndexRef(base)
	}
	e.view = &View{
		Base:       base,
		Overlay:    ov,
		Generation: e.generation,
		baseRef:    ref,
	}
	e.lastCheck = time.Now()
	e.mu.Unlock()

	if old != nil && old.baseRef != nil {
		_ = old.baseRef.release()
	}
}

// Rebuild triggers an asynchronous full rebuild.
func (e *Engine) Rebuild() {
	if e.closed.Load() {
		return
	}
	e.bgWG.Add(1)
	go func() {
		defer e.bgWG.Done()
		e.rebuildSync(true)
	}()
}

func (e *Engine) rebuildSync(forceSave bool) {
	if !e.building.CompareAndSwap(false, true) {
		return
	}
	defer e.building.Store(false)

	idx, err := Build(Config{Roots: e.roots})
	if err != nil {
		return
	}
	if idx.BuildMs >= 100 || forceSave {
		e.saveWithLock(idx)
	}
	e.setView(idx, nil)
}

func (e *Engine) saveWithLock(idx *Index) {
	if e.idxPath == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(e.idxPath), 0o755)
	fl := flock.New(e.idxPath + ".lock")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	locked, err := fl.TryLockContext(ctx, 20*time.Millisecond)
	if err != nil || !locked {
		return
	}
	defer func() { _ = fl.Unlock() }()
	_ = idx.Save(e.idxPath)
}

// MaybeRefresh checks for file changes (throttled to 1s or 5*buildMs for in-memory indexes)
// and updates the Overlay or triggers a background fold/rebuild.
func (e *Engine) MaybeRefresh() {
	if e.closed.Load() {
		return
	}
	e.mu.RLock()
	v := e.view
	lastCheck := e.lastCheck
	lastChange := e.lastChange
	e.mu.RUnlock()

	if v == nil || v.Base == nil {
		return
	}
	throttle := checkEvery
	if v.Base.BuildMs < 100 {
		memThrottle := time.Duration(v.Base.BuildMs*5) * time.Millisecond
		if memThrottle > throttle {
			throttle = memThrottle
		}
	}
	now := time.Now()
	if now.Sub(lastCheck) < throttle {
		return
	}

	e.mu.Lock()
	e.lastCheck = now
	e.mu.Unlock()

	files, err := Scan(e.roots, DefaultMaxFileBytes, DefaultMaxFiles)
	if err != nil {
		return
	}
	changed, deleted := v.Base.Diff(files)
	if len(changed) == 0 && len(deleted) == 0 {
		if v.Overlay != nil {
			e.setView(v.Base, nil)
		}
		return
	}

	e.mu.Lock()
	e.lastChange = now
	lastChange = now
	e.mu.Unlock()

	totalDelta := len(changed) + len(deleted)
	_, statErr := os.Stat(e.idxPath)
	idxMissing := statErr != nil

	if v.Base.BuildMs < 100 || idxMissing || totalDelta > overlayMaxFiles {
		go e.rebuildSync(false)
		return
	}

	ov := BuildOverlay(v.Base, changed, deleted, DefaultMaxFileBytes)
	e.setView(v.Base, ov)

	// Background fold if quiet for 30s and threshold met.
	if now.Sub(lastChange) >= quietBeforeRebuild &&
		(ov.FileCount >= foldMinFiles || now.Sub(ov.CreatedAt) >= foldAfter) {
		go e.rebuildSync(true)
	}
}

// Search waits up to waitDur for the initial build (respecting ctx), refreshes if needed, and executes the query.
func (e *Engine) Search(ctx context.Context, query string, opts SearchOpts, waitDur time.Duration) (Results, bool) {
	if waitDur > 0 {
		wCtx, cancel := context.WithTimeout(ctx, waitDur)
		e.WaitReady(wCtx)
		cancel()
	}
	if e.CurrentView() == nil {
		return Results{}, false
	}
	e.MaybeRefresh()
	v := e.acquireView()
	if v == nil || v.Base == nil {
		v.release()
		return Results{}, false
	}
	defer v.release()

	k := opts.K
	if k <= 0 {
		k = defaultResultsK
	}
	key := resultCacheKey{
		gen:      v.Generation,
		query:    query,
		k:        k,
		prefix:   opts.PathPrefix,
		maxChars: opts.MaxChars,
	}

	e.cacheMu.Lock()
	if cached, ok := e.cache[key]; ok {
		e.cacheMu.Unlock()
		return cached, true
	}
	e.cacheMu.Unlock()

	res := SearchCombined(v.Base, v.Overlay, query, opts)

	e.cacheMu.Lock()
	if len(e.cache) >= cacheMaxEntries {
		clear(e.cache)
	}
	e.cache[key] = res
	e.cacheMu.Unlock()

	return res, true
}

// Stats returns current index statistics.
type Stats struct {
	Ready    bool
	Files    int
	Sections int
	Entities int
	BuildMs  uint64
	Roots    []string
}

// Stats returns a snapshot of engine metrics.
func (e *Engine) Stats() Stats {
	v := e.CurrentView()
	if v == nil || v.Base == nil {
		return Stats{Ready: false, Roots: append([]string(nil), e.roots...)}
	}
	return Stats{
		Ready:    true,
		Files:    v.Base.NumFiles(),
		Sections: v.Base.NumSections(),
		Entities: v.Base.NumEntities(),
		BuildMs:  v.Base.BuildMs,
		Roots:    append([]string(nil), e.roots...),
	}
}

var (
	enginesMu sync.Mutex
	engines   = make(map[string]*Engine)
)

// SharedEngine returns a process-shared Engine for cwd (or creates one).
func SharedEngine(cwd string) *Engine {
	settings := LoadSettings()
	root := ResolveRoot(cwd)
	var roots []string
	if root != "" {
		roots = append(roots, root)
	}
	for _, r := range settings.Roots {
		if r != "" && !slices.Contains(roots, r) {
			roots = append(roots, r)
		}
	}
	if len(roots) == 0 && cwd != "" {
		roots = []string{cwd}
	}
	sorted := append([]string(nil), roots...)
	sort.Strings(sorted)
	key := strings.Join(sorted, "|")

	enginesMu.Lock()
	defer enginesMu.Unlock()
	if eng, ok := engines[key]; ok && !eng.closed.Load() {
		return eng
	}
	eng := NewEngine(roots)
	engines[key] = eng
	return eng
}

// IsDisabled reports whether docsearch is disabled via PI_DOCSEARCH=off.
func IsDisabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("PI_DOCSEARCH")), "off")
}

// ShouldAutoEnable reports whether project_search should be active for cwd.
func ShouldAutoEnable(cwd string) bool {
	if IsDisabled() {
		return false
	}
	settings := LoadSettings()
	if len(settings.Roots) > 0 {
		return true
	}
	root := ResolveRoot(cwd)
	if root == "" {
		return false
	}
	return CountFiles(root, SmallProjectFiles) > SmallProjectFiles
}

// FormatToolResult formats search results for the project_search tool.
func FormatToolResult(query string, res Results) string {
	if len(res.Hits) == 0 {
		return fmt.Sprintf("No sections match %q. Try other words or names.", query)
	}
	ms := float64(res.Micros) / 1000.0
	var b strings.Builder
	if len(res.Entities) > 0 {
		fmt.Fprintf(&b, "%d sections (%.1f ms; names: %s)", len(res.Hits), ms, strings.Join(res.Entities, ", "))
	} else {
		fmt.Fprintf(&b, "%d sections (%.1f ms)", len(res.Hits), ms)
	}

	resultChars := []int{1500, 1000, 600}
	var more []string
	for i, h := range res.Hits {
		headSuffix := ""
		if h.Heading != "" {
			headSuffix = " · " + h.Heading
		}
		if i < len(resultChars) {
			maxC := resultChars[i]
			txt := h.Text
			cut := false
			if len(txt) > maxC {
				txt = txt[:maxC]
				cut = true
			}
			fmt.Fprintf(&b, "\n\n── %s:%d-%d%s\n%s", h.Path, h.LineStart, h.LineEnd, headSuffix, txt)
			if cut || h.Truncated {
				fmt.Fprintf(&b, "\n[cut — read %s lines %d-%d for the rest]", h.Path, h.LineStart, h.LineEnd)
			}
		} else {
			more = append(more, fmt.Sprintf("   %s:%d-%d%s", h.Path, h.LineStart, h.LineEnd, headSuffix))
		}
	}
	if len(more) > 0 {
		b.WriteString("\n\nMore matches (read the lines to see them):\n")
		b.WriteString(strings.Join(more, "\n"))
	}
	return b.String()
}

// HandleDocsCommand serves the /docs slash command (status, rebuild, auto on|off, <query>).
func HandleDocsCommand(args string, cwd string) string {
	if IsDisabled() {
		return "docsearch: disabled (PI_DOCSEARCH=off)"
	}
	trimmed := strings.TrimSpace(args)
	settings := LoadSettings()
	eng := SharedEngine(cwd)

	switch {
	case trimmed == "" || trimmed == "status":
		st := eng.Stats()
		if !st.Ready {
			return fmt.Sprintf("docsearch: building\nroots: %s\nauto-recall: %s · settings: %s",
				strings.Join(st.Roots, ", "),
				onOff(settings.autoRecallEnabled()),
				SettingsPath(),
			)
		}
		return fmt.Sprintf("docsearch: %d files, %d sections, %d names, built in %d ms\nroots: %s\nauto-recall: %s · settings: %s",
			st.Files, st.Sections, st.Entities, st.BuildMs,
			strings.Join(st.Roots, ", "),
			onOff(settings.autoRecallEnabled()),
			SettingsPath(),
		)

	case trimmed == "rebuild":
		eng.Rebuild()
		return "docsearch: rebuilding"

	case trimmed == "auto on":
		v := true
		settings.AutoRecall = &v
		if err := SaveSettings(settings); err != nil {
			return fmt.Sprintf("docsearch: failed to save settings: %v", err)
		}
		return "docsearch: auto-recall on"

	case trimmed == "auto off":
		v := false
		settings.AutoRecall = &v
		if err := SaveSettings(settings); err != nil {
			return fmt.Sprintf("docsearch: failed to save settings: %v", err)
		}
		return "docsearch: auto-recall off"

	default:
		res, ready := eng.Search(context.Background(), trimmed, SearchOpts{K: 8, MaxChars: 200}, 5*time.Second)
		if !ready {
			return "docsearch: index is still building"
		}
		ms := float64(res.Micros) / 1000.0
		var b strings.Builder
		fmt.Fprintf(&b, "%d sections in %.1f ms", len(res.Hits), ms)
		for _, h := range res.Hits {
			fmt.Fprintf(&b, "\n%s:%d-%d  %s", h.Path, h.LineStart, h.LineEnd, h.Heading)
		}
		return b.String()
	}
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// NewTool returns the project_search AgentTool for cwd, or nil if auto-disabled
// (PI_DOCSEARCH=off or <= 150 files with no extra configured roots).
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

func (t *agentTool) Name() string  { return "project_search" }
func (t *agentTool) Label() string { return "Project search" }
func (t *agentTool) ExecutionMode() agent.ToolExecutionMode {
	return agent.ToolModeParallel
}

var projectSearchParamsSchema = func() map[string]any {
	var m map[string]any
	_ = json.Unmarshal([]byte(`{"type":"object","required":["query"],"properties":{
	"query":{"type":"string","description":"Search query (identifiers, function/type names, or keywords)"},
	"k":{"type":"integer","minimum":1,"maximum":20,"description":"Maximum number of sections to return (default: 6)"},
	"path":{"type":"string","description":"Optional path prefix filter relative to project root"}
}}`), &m)
	return m
}()

var projectSearchOutputSchemaJSON = json.RawMessage(`{"type":"object","properties":{
	"hits":{"type":"array","items":{"type":"object","properties":{
		"path":{"type":"string"},
		"lineStart":{"type":"integer"},
		"lineEnd":{"type":"integer"},
		"heading":{"type":"string"},
		"text":{"type":"string"},
		"truncated":{"type":"boolean"}
	}}}
}}`)

func (t *agentTool) Schema() ai.ToolSchema {
	params := make(map[string]any, len(projectSearchParamsSchema))
	for k, v := range projectSearchParamsSchema {
		params[k] = v
	}
	return ai.ToolSchema{
		Name:        "project_search",
		Description: "Search this project's code and documentation and the configured doc folders. Returns the best-matching sections with path and line range: a doc section under its headings, or a whole function, class or struct under its name. A query naming a function or type returns its definition first. Ranks by terms and by the names the query mentions (identifiers, file names, flags, products). ~1 ms.",
		Parameters:  params,
		PromptGuidelines: []string{
			"Use project_search before grep/find/read when looking up function/type definitions or documentation sections.",
			"Include exact identifiers, file names, flags, or type names in the query.",
			"Use grep for exact-text or call-site searches.",
			"Batch independent project_search calls in parallel.",
		},
	}
}

func (t *agentTool) BorrowedSchema() ai.ToolSchema {
	s := t.Schema()
	s.Parameters = projectSearchParamsSchema
	return s
}

func (t *agentTool) OutputSchema() json.RawMessage {
	return projectSearchOutputSchemaJSON
}

type toolSearchParams struct {
	Query string `json:"query"`
	K     int    `json:"k,omitempty"`
	Path  string `json:"path,omitempty"`
}

func (t *agentTool) Execute(ctx context.Context, _ string, rawParams json.RawMessage, _ agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	var p toolSearchParams
	if err := json.Unmarshal(rawParams, &p); err != nil {
		return agent.AgentToolResult{}, fmt.Errorf("project_search: invalid params: %w", err)
	}
	if ctx.Err() != nil {
		return agent.AgentToolResult{
			Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "Operation aborted"}},
			IsError: true,
			Thrown:  true,
		}, nil
	}
	k := p.K
	if k <= 0 {
		k = LoadSettings().resultCount()
	}
	if k > 20 {
		k = 20
	}
	eng := SharedEngine(t.cwd)
	res, ready := eng.Search(ctx, p.Query, SearchOpts{
		K:          k,
		PathPrefix: p.Path,
	}, 5*time.Second)
	if !ready {
		return agent.AgentToolResult{
			Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "project_search: index is still building; try again shortly."}},
		}, nil
	}
	text := FormatToolResult(p.Query, res)
	structured, _ := json.Marshal(map[string]any{"hits": res.Hits})
	return agent.AgentToolResult{
		Content:           []ai.ToolResultMessageContent{ai.TextContent{Text: text}},
		StructuredContent: structured,
		Details: map[string]any{
			"count":    len(res.Hits),
			"micros":   res.Micros,
			"entities": res.Entities,
		},
	}, nil
}
