package docsearch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gofrs/flock"
)

const (
	checkEvery         = 1 * time.Second
	quietBeforeRebuild = 30 * time.Second
	foldMinFiles       = 20
	foldAfter          = 600 * time.Second
	overlayMaxFiles    = 200
	cacheMaxEntries    = 256
	cachePruneAge      = 30 * 24 * time.Hour
	defaultResultsK    = 6
)

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

// indexCachePath is the file for these roots and extensions: the same configuration shares one, whichever process
// asks (engine.rs index_file hashes the roots and the extensions).
func indexCachePath(roots, extensions []string) string {
	sorted := append([]string(nil), roots...)
	sort.Strings(sorted)
	exts := append([]string(nil), extensions...)
	sort.Strings(exts)
	key := strings.Join(sorted, "\n")
	if extensions != nil {
		key += "\x00" + strings.Join(exts, ",")
	}
	h := sha256.Sum256([]byte(key))
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
	abs      string
	maxChars int
}

// Engine manages background index loading, incremental overlay updates, disk persistence, and search caching.
type Engine struct {
	roots   []string
	idxPath string
	// extensions are the indexed extensions, lowercase without the dot; nil indexes every text file.
	extensions []string

	mu         sync.RWMutex
	view       *View
	generation uint64
	lastCheck  time.Time
	lastChange time.Time
	// notified is set by NotifyFileChanged: a change check is due whatever the throttle says, and a view stored by a check or rebuild that
	// began before the notice leaves it due.
	notified bool
	// lastDiff is the change set (relative path, mtime, size of every changed or deleted file) the last check found. A check that finds the
	// same set has nothing new: files that are listed but give no section (blank, binary) would otherwise look changed on every check.
	lastDiff string
	// lastScan is how long the last change check's walk took (refreshThrottle).
	lastScan   atomic.Int64
	building   atomic.Bool
	refreshing atomic.Bool

	readyOnce sync.Once
	readyCh   chan struct{}

	cacheMu sync.Mutex
	cache   map[resultCacheKey]Results
	closed  atomic.Bool
	bgWG    sync.WaitGroup
}

// NewEngine creates and starts a background Engine for the given roots, indexing every text file.
func NewEngine(roots []string) *Engine { return NewEngineWithExtensions(roots, nil) }

// NewEngineWithExtensions creates and starts a background Engine indexing the files with these extensions (nil: every
// text file), as the docsearch addon does for the extensions it is given.
func NewEngineWithExtensions(roots, extensions []string) *Engine {
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
		roots:      normRoots,
		extensions: normalizeExtensions(extensions),
		idxPath:    indexCachePath(normRoots, normalizeExtensions(extensions)),
		readyCh:    make(chan struct{}),
		cache:      make(map[resultCacheKey]Results, cacheMaxEntries),
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
	// Close waits for it too, so nothing writes to the cache directory after the engine is closed.
	e.bgWG.Add(1)
	go func() {
		defer e.bgWG.Done()
		pruneCacheDir()
	}()

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
	if !e.notified {
		e.lastCheck = time.Now()
	}
	e.mu.Unlock()

	if old != nil && old.baseRef != nil {
		_ = old.baseRef.release()
	}
}

// setViewOnBase is setView for a view computed on base: it is stored only while base is still the current view's base, so a check that
// read an older base does not put it back over a rebuild that replaced it meanwhile.
func (e *Engine) setViewOnBase(base *Index, ov *Overlay) {
	e.mu.RLock()
	current := e.view != nil && e.view.Base == base
	e.mu.RUnlock()
	if current && !e.closed.Load() {
		e.setView(base, ov)
	}
}

// goRebuild runs rebuildSync in the background; Close waits for it.
func (e *Engine) goRebuild(forceSave bool) {
	if e.closed.Load() {
		return
	}
	e.bgWG.Add(1)
	go func() {
		defer e.bgWG.Done()
		e.rebuildSync(forceSave)
	}()
}

// Rebuild triggers an asynchronous full rebuild.
func (e *Engine) Rebuild() {
	e.goRebuild(true)
}

func (e *Engine) rebuildSync(forceSave bool) {
	if !e.building.CompareAndSwap(false, true) {
		return
	}
	defer e.building.Store(false)

	idx, err := Build(Config{Roots: e.roots, Extensions: e.extensions})
	if err != nil || e.closed.Load() {
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
	// The view is retained while the check reads its base: a rebuild that replaces it meanwhile must not unmap it underneath.
	v := e.acquireView()
	defer v.release()
	if v == nil || v.Base == nil || !e.checkDue() {
		return
	}
	now := time.Now()
	e.mu.RLock()
	lastChange := e.lastChange
	e.mu.RUnlock()

	e.mu.Lock()
	e.lastCheck = now
	e.notified = false
	e.mu.Unlock()

	scanStart := time.Now()
	files, err := ScanExtensions(e.roots, e.extensions, DefaultMaxFileBytes, DefaultMaxFiles)
	e.lastScan.Store(int64(time.Since(scanStart)))
	if err != nil {
		return
	}
	changed, deleted := v.Base.Diff(files)
	if len(changed) == 0 && len(deleted) == 0 {
		e.mu.Lock()
		e.lastDiff = ""
		e.mu.Unlock()
		if v.Overlay != nil {
			e.setViewOnBase(v.Base, nil)
		}
		return
	}

	diff := diffSignature(changed, deleted)
	e.mu.Lock()
	unchanged := diff == e.lastDiff
	if !unchanged {
		e.lastDiff = diff
		e.lastChange = now
		lastChange = now
	}
	e.mu.Unlock()

	if unchanged {
		// Nothing changed since the last check: the view is current. An overlay that has been quiet long enough is folded into a rebuild.
		if ov := v.Overlay; ov != nil && now.Sub(lastChange) >= quietBeforeRebuild &&
			(ov.FileCount >= foldMinFiles || now.Sub(ov.CreatedAt) >= foldAfter) {
			e.goRebuild(true)
		}
		return
	}

	totalDelta := len(changed) + len(deleted)
	_, statErr := os.Stat(e.idxPath)
	idxMissing := statErr != nil

	if v.Base.BuildMs < 100 || idxMissing || totalDelta > overlayMaxFiles {
		e.goRebuild(false)
		return
	}

	ov := BuildOverlay(v.Base, changed, deleted, DefaultMaxFileBytes)
	if v.Overlay != nil {
		// The overlay's age is the age of the oldest change it holds (foldAfter).
		ov.CreatedAt = v.Overlay.CreatedAt
	}
	e.setViewOnBase(v.Base, ov)
}

// diffSignature identifies a change set by every changed file's path, mtime and size and every deleted file's path.
func diffSignature(changed []FileMeta, deleted []string) string {
	parts := make([]string, 0, len(changed)+len(deleted))
	for _, f := range changed {
		parts = append(parts, f.Rel+"\x00"+strconv.FormatInt(f.MtimeNanos, 10)+"\x00"+strconv.FormatInt(f.Size, 10))
	}
	for _, rel := range deleted {
		parts = append(parts, rel+"\x00deleted")
	}
	slices.Sort(parts)
	return strings.Join(parts, "\x01")
}

// checkDue reports whether a change check is due: a file was reported changed (NotifyFileChanged), or the throttle has passed since the
// last check.
func (e *Engine) checkDue() bool {
	e.mu.RLock()
	lastCheck, notified := e.lastCheck, e.notified
	e.mu.RUnlock()
	return notified || time.Since(lastCheck) >= e.refreshThrottle()
}

// refreshThrottle is how long a change check waits after the last one: checkEvery, or five times the last check's walk when that is
// longer, so checking a large tree takes at most about a fifth of a CPU while the agent searches.
func (e *Engine) refreshThrottle() time.Duration {
	return max(checkEvery, 5*time.Duration(e.lastScan.Load()))
}

// MaybeRefreshAsync triggers MaybeRefresh in the background if the throttle window has elapsed.
func (e *Engine) MaybeRefreshAsync() {
	if e.closed.Load() {
		return
	}
	if v := e.CurrentView(); v == nil || v.Base == nil || !e.checkDue() {
		return
	}
	if !e.refreshing.CompareAndSwap(false, true) {
		return
	}
	e.bgWG.Add(1)
	go func() {
		defer e.bgWG.Done()
		defer e.refreshing.Store(false)
		e.MaybeRefresh()
	}()
}

// Search waits up to waitDur for the initial build (respecting ctx), triggers async refresh if needed, and executes the query.
func (e *Engine) Search(ctx context.Context, query string, opts SearchOpts, waitDur time.Duration) (Results, bool) {
	if waitDur > 0 {
		wCtx, cancel := context.WithTimeout(ctx, waitDur)
		e.WaitReady(wCtx)
		cancel()
	}
	if e.CurrentView() == nil {
		return Results{}, false
	}
	e.MaybeRefreshAsync()
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
		abs:      opts.AbsPathPrefix,
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
