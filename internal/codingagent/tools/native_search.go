package tools

import (
	"bufio"
	"bytes"
	"container/list"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"regexp/syntax"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/ignorerules"
	"github.com/MichaelKinsy/PiG/internal/procpolicy"
)

const (
	treeCacheVerifyWindow = 2 * time.Second
	maxVerifiedDirs       = 4096
	maxContentCacheBytes  = 64 << 20  // 64 MB
	maxCachedFileBytes    = 512 << 10 // 512 KB
	// contentCacheKeepRecent: an entry used this recently is not evicted for a new one. A search over a tree larger than the cache then
	// keeps what the cache holds instead of evicting each file before the next search needs it (no hit at all); between searches further
	// apart the cache follows what is searched.
	contentCacheKeepRecent = 10 * time.Second
	contentCacheStatFree   = 1500 * time.Millisecond
	wholeFileBytes         = 2 << 20 // 2 MB
	pieceBytes             = 1 << 20 // 1 MB
	maxIgnoreFileBytes     = 4 << 20 // 4 MiB
	maxBraceExpansions     = 1024
	fuzzyFallbackLimit     = 20
	walkBatchConcurrency   = 32
)

type entryKind uint8

const (
	kindFile entryKind = iota + 1
	kindDir
	kindSymlink
)

type walkEntry struct {
	rel  string
	abs  string
	kind entryKind
}

type cachedTreeEntry struct {
	rootKey        string
	entries        []walkEntry
	ignoredFiles   []walkEntry
	displayPaths   []string
	knownAbsKeys   map[string]struct{}
	ignoredDirKeys map[string]struct{}
	dirMtimes      map[string]int64
	// ignoreMtimes holds the ignore files the walk read (.gitignore, .ignore, git's info/exclude and global excludes) with their mtimes:
	// editing one in place leaves its folder's mtime alone, so a revalidation checks them too.
	ignoreMtimes map[string]int64
	partial      bool
	timestamp    time.Time
}

type cachedFileBuffer struct {
	key        string
	mtimeNanos int64
	size       int64
	verifiedAt time.Time
	// usedAt is when a search last read the entry.
	usedAt time.Time
	buffer []byte // nil means binary file (contains NUL)
}

var (
	searchCacheMu    sync.Mutex
	treeCache        = make(map[string]*cachedTreeEntry)
	contentCacheList = list.New()
	contentCacheMap  = make(map[string]*list.Element)
	contentCacheSize int64
	externalChangeAt time.Time
)

func normalizeCacheKey(p string) string {
	cleaned := filepath.Clean(p)
	if runtime.GOOS == "windows" {
		return strings.ToLower(cleaned)
	}
	return cleaned
}

// ClearNativeSearchCache clears both the file tree cache and content cache.
func ClearNativeSearchCache() {
	searchCacheMu.Lock()
	defer searchCacheMu.Unlock()
	treeCache = make(map[string]*cachedTreeEntry)
	contentCacheList.Init()
	contentCacheMap = make(map[string]*list.Element)
	contentCacheSize = 0
	externalChangeAt = time.Time{}
}

// NotifySearchExternalChange marks that an external command may have modified files,
// forcing tree and content caches to revalidate on next access.
func NotifySearchExternalChange() {
	searchCacheMu.Lock()
	externalChangeAt = time.Now()
	searchCacheMu.Unlock()
}

// InvalidateSearchCache records that a command may have changed files (cwd names where it ran), so the next search revalidates the cached
// trees and file contents instead of trusting them for treeCacheVerifyWindow.
//
// The cached trees are not dropped: the next search revalidates them (getCachedTree), restating their folders and the ignore files they
// read, and walks again only when one of those changed. Dropping them made every grep after a command such as `go test` walk the whole
// tree again (µPi: microPi re-checks the trees, 8c2c0bac). A tree past maxVerifiedDirs is walked again, as before.
func InvalidateSearchCache(cwd string) {
	searchCacheMu.Lock()
	defer searchCacheMu.Unlock()
	externalChangeAt = time.Now()
}

// NotifySearchFileTouched updates the content cache (when content != nil) and
// invalidates any cached tree that doesn't already know about filePath (unless under an ignored dir).
func NotifySearchFileTouched(filePath string, content []byte, mtime time.Time) {
	if filePath == "" {
		return
	}
	abs, err := filepath.Abs(filePath)
	if err != nil {
		return
	}
	key := normalizeCacheKey(abs)

	searchCacheMu.Lock()
	defer searchCacheMu.Unlock()

	if content != nil {
		storeContentLocked(key, mtime.UnixNano(), int64(len(content)), content, false, time.Now())
	} else {
		removeContentLocked(key)
	}

	for tk, tree := range treeCache {
		if _, known := tree.knownAbsKeys[key]; known {
			continue
		}
		if key != tree.rootKey && !strings.HasPrefix(key, tree.rootKey+string(filepath.Separator)) {
			continue
		}
		underIgnored := false
		for igDir := range tree.ignoredDirKeys {
			if key == igDir || strings.HasPrefix(key, igDir+string(filepath.Separator)) {
				underIgnored = true
				break
			}
		}
		if !underIgnored {
			delete(treeCache, tk)
		}
	}
}

func removeContentLocked(key string) {
	if elem, ok := contentCacheMap[key]; ok {
		entry := elem.Value.(*cachedFileBuffer)
		if entry.buffer != nil {
			contentCacheSize -= int64(len(entry.buffer))
		}
		contentCacheList.Remove(elem)
		delete(contentCacheMap, key)
	}
}

// storeContentLocked caches raw for key. owned means raw belongs to the cache from now on (a buffer the search read itself); otherwise it is
// copied. It reports whether raw was kept.
func storeContentLocked(key string, mtimeNanos, size int64, raw []byte, owned bool, now time.Time) bool {
	removeContentLocked(key)
	if size > maxCachedFileBytes || !contentCacheAdmitsLocked(int64(len(raw)), now) {
		return false
	}
	var stored []byte
	if decoded, ok := decodeUTF16BOM(raw); ok {
		stored = decoded
		contentCacheSize += int64(len(stored))
	} else if bytes.IndexByte(raw, 0) == -1 {
		if owned {
			stored = raw[:len(raw):len(raw)]
		} else {
			stored = make([]byte, len(raw))
			copy(stored, raw)
		}
		contentCacheSize += int64(len(stored))
	}
	entry := &cachedFileBuffer{
		key:        key,
		mtimeNanos: mtimeNanos,
		size:       size,
		verifiedAt: now,
		usedAt:     now,
		buffer:     stored,
	}
	elem := contentCacheList.PushBack(entry)
	contentCacheMap[key] = elem

	for contentCacheSize > maxContentCacheBytes && contentCacheList.Len() > 0 {
		front := contentCacheList.Front()
		old := front.Value.(*cachedFileBuffer)
		if old.buffer != nil {
			contentCacheSize -= int64(len(old.buffer))
		}
		contentCacheList.Remove(front)
		delete(contentCacheMap, old.key)
	}
	return stored != nil
}

// contentCacheAdmitsLocked reports whether n more bytes may enter the cache: they fit, or the least recently used entry is older than
// contentCacheKeepRecent and so may make room.
func contentCacheAdmitsLocked(n int64, now time.Time) bool {
	if contentCacheSize+n <= maxContentCacheBytes {
		return true
	}
	front := contentCacheList.Front()
	return front != nil && now.Sub(front.Value.(*cachedFileBuffer).usedAt) >= contentCacheKeepRecent
}

// readBufPool holds buffers for files a search reads without caching them.
var readBufPool = sync.Pool{New: func() any { b := make([]byte, 0, 64<<10); return &b }}

// readWholeFile reads the size bytes of f into buf's storage when it is large enough, else into a new buffer.
func readWholeFile(f *os.File, size int64, buf []byte) ([]byte, error) {
	if int64(cap(buf)) < size+1 {
		buf = make([]byte, 0, size+512)
	}
	buf = buf[:0]
	for {
		n, err := f.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if err == io.EOF {
			return buf, nil
		}
		if err != nil {
			return nil, err
		}
		if len(buf) == cap(buf) {
			// The file grew since its stat: read the rest into a larger buffer.
			buf = append(buf, 0)[:len(buf)]
		}
	}
}

// ─── Ignore & Git Repository Discovery ───────────────────────────────────────

type taggedIgnoreRule struct {
	patterns []string
	isGit    bool
	root     string
	rules    []ignorerules.Rule
}

func readSmallFile(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxIgnoreFileBytes {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}

func repositoryOf(root string) string {
	for cur := root; ; {
		if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

// recordIgnoreFileMtimes adds each of paths that exists to into, with its mtime.
func recordIgnoreFileMtimes(into map[string]int64, paths ...string) {
	for _, path := range paths {
		if path == "" {
			continue
		}
		if fi, err := os.Stat(path); err == nil {
			into[path] = fi.ModTime().UnixNano()
		}
	}
}

func excludeFileOf(repoDir string) string {
	dotGit := filepath.Join(repoDir, ".git")
	info, err := os.Stat(dotGit)
	if err != nil {
		return ""
	}
	if info.IsDir() {
		p := filepath.Join(dotGit, "info", "exclude")
		if _, err := os.Stat(p); err == nil {
			return p
		}
		return ""
	}
	// .git is a file (worktree / submodule): "gitdir: <path>"
	content, ok := readSmallFile(dotGit)
	if !ok {
		return ""
	}
	var gitDir string
	for line := range strings.SplitSeq(content, "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "gitdir:"); ok {
			gitDir = strings.TrimSpace(rest)
			break
		}
	}
	if gitDir == "" {
		return ""
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(repoDir, gitDir)
	}
	if commonRaw, ok := readSmallFile(filepath.Join(gitDir, "commondir")); ok {
		common := strings.TrimSpace(commonRaw)
		if common != "" {
			if !filepath.IsAbs(common) {
				common = filepath.Join(gitDir, common)
			}
			p := filepath.Join(common, "info", "exclude")
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	p := filepath.Join(gitDir, "info", "exclude")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

func globalExcludesFile() string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		if cfg, ok := readSmallFile(filepath.Join(home, ".gitconfig")); ok {
			inCore := false
			for line := range strings.SplitSeq(cfg, "\n") {
				t := strings.TrimSpace(line)
				if strings.HasPrefix(t, "[") {
					inCore = strings.EqualFold(t, "[core]")
					continue
				}
				if inCore {
					if idx := strings.IndexByte(t, '='); idx > 0 {
						k := strings.TrimSpace(t[:idx])
						if strings.EqualFold(k, "excludesfile") {
							v := strings.TrimSpace(t[idx+1:])
							v = strings.Trim(v, `"'`)
							if strings.HasPrefix(v, "~/") || strings.HasPrefix(v, `~\`) {
								v = filepath.Join(home, v[2:])
							}
							if v != "" {
								if _, err := os.Stat(v); err == nil {
									return v
								}
							}
						}
					}
				}
			}
		}
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		p := filepath.Join(xdg, "git", "ignore")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if home != "" {
		p := filepath.Join(home, ".config", "git", "ignore")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func loadIgnorePatternsFromFile(path, prefix string) []string {
	content, ok := readSmallFile(path)
	if !ok {
		return nil
	}
	var patterns []string
	for line := range strings.SplitSeq(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || (strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, `\#`)) {
			continue
		}
		pat := line
		negated := false
		if rest, ok := strings.CutPrefix(pat, "!"); ok {
			negated, pat = true, rest
		} else if strings.HasPrefix(pat, `\!`) {
			pat = pat[1:]
		}
		pat = strings.TrimPrefix(pat, "/")
		if prefix != "" {
			pat = prefix + pat
		}
		if negated {
			pat = "!" + pat
		}
		patterns = append(patterns, pat)
	}
	return patterns
}

// isIgnoredByStack is asked only by walkTree, which does not enter ignored directories, so each layer matches the entry alone
// (ignorerules.IgnoredInWalk).
func isIgnoredByStack(absPath string, isDir bool, stack []taggedIgnoreRule) bool {
	ignored := false
	for _, layer := range stack {
		if len(layer.rules) == 0 {
			continue
		}
		if ignorerules.IgnoredInWalk(absPath, isDir, layer.root, layer.rules) {
			ignored = true
		}
	}
	return ignored
}

func appendIgnoreLayer(stack []taggedIgnoreRule, dir, baseRoot string, includeGit bool) []taggedIgnoreRule {
	relDir, err := filepath.Rel(baseRoot, dir)
	if err != nil {
		return stack
	}
	prefix := ""
	if relDir != "." {
		prefix = filepath.ToSlash(relDir) + "/"
	}
	if includeGit {
		if pats := loadIgnorePatternsFromFile(filepath.Join(dir, ".gitignore"), prefix); len(pats) > 0 {
			rules := ignorerules.AppendPatterns(nil, pats)
			stack = append(stack, taggedIgnoreRule{isGit: true, root: baseRoot, rules: rules})
		}
	}
	if pats := loadIgnorePatternsFromFile(filepath.Join(dir, ".ignore"), prefix); len(pats) > 0 {
		rules := ignorerules.AppendPatterns(nil, pats)
		stack = append(stack, taggedIgnoreRule{isGit: false, root: baseRoot, rules: rules})
	}
	return stack
}

// ─── Concurrent Walker & Tree Cache ──────────────────────────────────────────

func getCachedTree(ctx context.Context, root string, requireGit bool) (*cachedTreeEntry, error) {
	resolvedRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	cacheKey := normalizeCacheKey(resolvedRoot) + "|"
	if requireGit {
		cacheKey += "1"
	} else {
		cacheKey += "0"
	}

	now := time.Now()
	searchCacheMu.Lock()
	cached := treeCache[cacheKey]
	extAt := externalChangeAt
	// timestamp is rewritten under searchCacheMu by a concurrent revalidation, so it is read under it too.
	var cachedAt time.Time
	if cached != nil {
		cachedAt = cached.timestamp
	}
	searchCacheMu.Unlock()

	if cached != nil {
		changedSince := !extAt.IsZero() && !cachedAt.After(extAt)
		if !changedSince && now.Sub(cachedAt) < treeCacheVerifyWindow {
			return cached, nil
		}
		valid := !(changedSince && cached.partial)
		if valid {
			for _, mtimes := range []map[string]int64{cached.dirMtimes, cached.ignoreMtimes} {
				for path, prevMtime := range mtimes {
					if ctx.Err() != nil {
						return nil, ctx.Err()
					}
					fi, statErr := os.Stat(path)
					if statErr != nil || fi.ModTime().UnixNano() != prevMtime {
						valid = false
						break
					}
				}
				if !valid {
					break
				}
			}
		}
		if valid {
			searchCacheMu.Lock()
			cached.timestamp = time.Now()
			searchCacheMu.Unlock()
			return cached, nil
		}
	}

	fresh, err := walkTree(ctx, resolvedRoot, requireGit)
	if err != nil {
		return nil, err
	}
	searchCacheMu.Lock()
	treeCache[cacheKey] = fresh
	searchCacheMu.Unlock()
	return fresh, nil
}

func walkTree(ctx context.Context, root string, requireGit bool) (*cachedTreeEntry, error) {
	defer procpolicy.Widen()()
	repo := repositoryOf(root)
	top := root
	inGit := repo != ""
	if inGit {
		top = repo
	}
	applyGit := !requireGit || inGit

	var initRules []taggedIgnoreRule
	ignoreMtimes := make(map[string]int64)
	if inGit {
		recordIgnoreFileMtimes(ignoreMtimes, globalExcludesFile(), excludeFileOf(repo))
		if gex := globalExcludesFile(); gex != "" {
			if pats := loadIgnorePatternsFromFile(gex, ""); len(pats) > 0 {
				initRules = append(initRules, taggedIgnoreRule{isGit: true, root: top, rules: ignorerules.AppendPatterns(nil, pats)})
			}
		}
		if rex := excludeFileOf(repo); rex != "" {
			if pats := loadIgnorePatternsFromFile(rex, ""); len(pats) > 0 {
				initRules = append(initRules, taggedIgnoreRule{isGit: true, root: top, rules: ignorerules.AppendPatterns(nil, pats)})
			}
		}
		// Ancestor .gitignore and .ignore from repo down to root's parent.
		if rel, err := filepath.Rel(repo, root); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			parts := strings.Split(filepath.ToSlash(rel), "/")
			cur := repo
			initRules = appendIgnoreLayer(initRules, cur, top, applyGit)
			recordIgnoreFileMtimes(ignoreMtimes, filepath.Join(cur, ".gitignore"), filepath.Join(cur, ".ignore"))
			for i := 0; i < len(parts)-1; i++ {
				cur = filepath.Join(cur, parts[i])
				initRules = appendIgnoreLayer(initRules, cur, top, applyGit)
				recordIgnoreFileMtimes(ignoreMtimes, filepath.Join(cur, ".gitignore"), filepath.Join(cur, ".ignore"))
			}
		}
	}

	type dirTask struct {
		abs     string
		rel     string
		rules   []taggedIgnoreRule
		gitRoot string
		inGit   bool
	}

	var (
		mu           sync.Mutex
		entries      []walkEntry
		ignoredFiles []walkEntry
		knownAbs     = make(map[string]struct{})
		ignoredDirs  = make(map[string]struct{})
		dirMtimes    = make(map[string]int64)
	)

	if fi, err := os.Stat(root); err == nil {
		dirMtimes[root] = fi.ModTime().UnixNano()
	}

	queue := []dirTask{{abs: root, rel: "", rules: initRules, gitRoot: top, inGit: inGit}}

	for len(queue) > 0 {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		batchN := min(len(queue), walkBatchConcurrency)
		batch := queue[:batchN]
		queue = queue[batchN:]

		var nextTasks []dirTask
		var wg sync.WaitGroup
		wg.Add(batchN)
		for _, task := range batch {
			go func(t dirTask) {
				defer wg.Done()
				if ctx.Err() != nil {
					return
				}
				rules := t.rules
				curGitRoot := t.gitRoot
				curInGit := t.inGit

				// Check nested .git in subdirectory.
				if t.rel != "" {
					if _, err := os.Stat(filepath.Join(t.abs, ".git")); err == nil {
						var filtered []taggedIgnoreRule
						for _, r := range rules {
							if !r.isGit {
								filtered = append(filtered, r)
							}
						}
						rules = filtered
						curGitRoot = t.abs
						curInGit = true
						if rex := excludeFileOf(t.abs); rex != "" {
							if pats := loadIgnorePatternsFromFile(rex, ""); len(pats) > 0 {
								rules = append(rules, taggedIgnoreRule{isGit: true, root: curGitRoot, rules: ignorerules.AppendPatterns(nil, pats)})
							}
						}
					}
				}

				rules = appendIgnoreLayer(rules, t.abs, curGitRoot, !requireGit || curInGit)
				localIgnoreFiles := make(map[string]int64, 2)
				recordIgnoreFileMtimes(localIgnoreFiles, filepath.Join(t.abs, ".gitignore"), filepath.Join(t.abs, ".ignore"))
				if curGitRoot == t.abs && t.rel != "" {
					recordIgnoreFileMtimes(localIgnoreFiles, excludeFileOf(t.abs))
				}

				dirEntries, err := os.ReadDir(t.abs)
				if err != nil {
					return
				}

				var localEntries []walkEntry
				var localIgnored []walkEntry
				var localSubdirs []dirTask
				var localIgDirs []string
				var localMtimes [][2]any

				for _, de := range dirEntries {
					name := de.Name()
					if name == ".git" {
						continue
					}
					absChild := filepath.Join(t.abs, name)
					relChild := name
					if t.rel != "" {
						relChild = t.rel + "/" + name
					}
					typ := de.Type()
					isSym := typ&os.ModeSymlink != 0
					isDir := !isSym && de.IsDir()

					if isIgnoredByStack(absChild, isDir, rules) {
						if isDir {
							localIgDirs = append(localIgDirs, normalizeCacheKey(absChild))
						} else if !isSym && typ.IsRegular() {
							localIgnored = append(localIgnored, walkEntry{rel: relChild, abs: absChild, kind: kindFile})
						}
						continue
					}

					if isSym {
						localEntries = append(localEntries, walkEntry{rel: relChild, abs: absChild, kind: kindSymlink})
						continue
					}
					if isDir {
						localEntries = append(localEntries, walkEntry{rel: relChild, abs: absChild, kind: kindDir})
						if fi, err := de.Info(); err == nil {
							localMtimes = append(localMtimes, [2]any{absChild, fi.ModTime().UnixNano()})
						}
						rulesCopy := make([]taggedIgnoreRule, len(rules))
						copy(rulesCopy, rules)
						localSubdirs = append(localSubdirs, dirTask{
							abs:     absChild,
							rel:     relChild,
							rules:   rulesCopy,
							gitRoot: curGitRoot,
							inGit:   curInGit,
						})
						continue
					}
					localEntries = append(localEntries, walkEntry{rel: relChild, abs: absChild, kind: kindFile})
				}

				mu.Lock()
				entries = append(entries, localEntries...)
				ignoredFiles = append(ignoredFiles, localIgnored...)
				for _, e := range localEntries {
					knownAbs[normalizeCacheKey(e.abs)] = struct{}{}
				}
				for _, e := range localIgnored {
					knownAbs[normalizeCacheKey(e.abs)] = struct{}{}
				}
				for _, d := range localIgDirs {
					ignoredDirs[d] = struct{}{}
				}
				for _, m := range localMtimes {
					if len(dirMtimes) < maxVerifiedDirs {
						dirMtimes[m[0].(string)] = m[1].(int64)
					}
				}
				for path, mtime := range localIgnoreFiles {
					ignoreMtimes[path] = mtime
				}
				nextTasks = append(nextTasks, localSubdirs...)
				mu.Unlock()
			}(task)
		}
		wg.Wait()
		queue = append(queue, nextTasks...)
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })
	sort.Slice(ignoredFiles, func(i, j int) bool { return ignoredFiles[i].rel < ignoredFiles[j].rel })

	displayPaths := make([]string, len(entries))
	for i, e := range entries {
		if e.kind == kindDir {
			displayPaths[i] = e.rel + "/"
		} else {
			displayPaths[i] = e.rel
		}
	}

	return &cachedTreeEntry{
		rootKey:        normalizeCacheKey(root),
		entries:        entries,
		ignoredFiles:   ignoredFiles,
		displayPaths:   displayPaths,
		knownAbsKeys:   knownAbs,
		ignoredDirKeys: ignoredDirs,
		dirMtimes:      dirMtimes,
		ignoreMtimes:   ignoreMtimes,
		partial:        len(dirMtimes) >= maxVerifiedDirs,
		timestamp:      time.Now(),
	}, nil
}

// ─── Linear-Time Glob Matcher & Brace Expansion ──────────────────────────────

func expandBraces(pattern string) ([]string, error) {
	openIdx := -1
	depth := 0
	for i := 0; i < len(pattern); i++ {
		if pattern[i] == '\\' && i+1 < len(pattern) {
			i++
			continue
		}
		if pattern[i] == '{' {
			if depth == 0 {
				openIdx = i
			}
			depth++
		} else if pattern[i] == '}' {
			depth--
			if depth == 0 && openIdx >= 0 {
				prefix := pattern[:openIdx]
				inner := pattern[openIdx+1 : i]
				suffix := pattern[i+1:]
				parts := splitBraceAlternatives(inner)
				if len(parts) <= 1 && !strings.Contains(inner, ",") {
					// Not a brace alternation; treat literally.
					break
				}
				var out []string
				for _, alt := range parts {
					sub, err := expandBraces(prefix + alt + suffix)
					if err != nil {
						return nil, err
					}
					out = append(out, sub...)
					if len(out) > maxBraceExpansions {
						return nil, fmt.Errorf("glob pattern exceeds %d brace expansions", maxBraceExpansions)
					}
				}
				return out, nil
			}
		}
	}
	return []string{pattern}, nil
}

func splitBraceAlternatives(inner string) []string {
	var parts []string
	start := 0
	depth := 0
	for i := 0; i < len(inner); i++ {
		if inner[i] == '\\' && i+1 < len(inner) {
			i++
			continue
		}
		switch inner[i] {
		case '{':
			depth++
		case '}':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				parts = append(parts, inner[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, inner[start:])
	return parts
}

type compiledGlob struct {
	re *regexp.Regexp
}

func compileGlob(pattern string, ignoreCase bool) (*compiledGlob, error) {
	var b strings.Builder
	if ignoreCase {
		b.WriteString("(?i)")
	}
	b.WriteByte('^')
	n := len(pattern)
	for i := 0; i < n; i++ {
		c := pattern[i]
		switch c {
		case '\\':
			if i+1 < n {
				b.WriteString(regexp.QuoteMeta(string(pattern[i+1])))
				i++
			} else {
				b.WriteString(`\\`)
			}
		case '*':
			if i+1 < n && pattern[i+1] == '*' {
				if i+2 < n && pattern[i+2] == '/' {
					b.WriteString("(?:.*/)?")
					i += 2
				} else {
					b.WriteString(".*")
					i++
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '[':
			end := i + 1
			if end < n && (pattern[end] == '!' || pattern[end] == '^') {
				end++
			}
			if end < n && pattern[end] == ']' {
				end++
			}
			for end < n && pattern[end] != ']' {
				end++
			}
			if end >= n {
				b.WriteString(`\[`)
			} else {
				cls := pattern[i+1 : end]
				if strings.HasPrefix(cls, "!") {
					cls = "^" + cls[1:]
				}
				b.WriteByte('[')
				b.WriteString(cls)
				b.WriteByte(']')
				i = end
			}
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteByte('$')
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil, err
	}
	return &compiledGlob{re: re}, nil
}

func (g *compiledGlob) match(s string) bool {
	return g.re.MatchString(s)
}

type globMatcherSet struct {
	positive []*compiledGlob
	negative []*compiledGlob
	hasSlash bool
}

func compileGrepGlob(glob string) (*globMatcherSet, error) {
	if glob == "" {
		return nil, nil
	}
	negated := false
	if rest, ok := strings.CutPrefix(glob, "!"); ok {
		negated = true
		glob = rest
	}
	glob = strings.TrimPrefix(glob, "./")
	// A glob with a slash is matched against the whole path relative to the search path, anchored there (microPi native-search.ts
	// globFilter; Pi #11807): "migrations/*.js" does not match nested/migrations/a.js, "**/migrations/*.js" does.
	hasSlash := strings.Contains(glob, "/")
	glob = strings.TrimPrefix(glob, "/")
	expanded, err := expandBraces(glob)
	if err != nil {
		return nil, err
	}
	set := &globMatcherSet{hasSlash: hasSlash}
	for _, pat := range expanded {
		cg, err := compileGlob(pat, false)
		if err != nil {
			return nil, err
		}
		if negated {
			set.negative = append(set.negative, cg)
		} else {
			set.positive = append(set.positive, cg)
		}
	}
	return set, nil
}

func (s *globMatcherSet) match(rel string) bool {
	if s == nil {
		return true
	}
	target := rel
	if !s.hasSlash {
		if idx := strings.LastIndexByte(rel, '/'); idx >= 0 {
			target = rel[idx+1:]
		}
	}
	for _, ng := range s.negative {
		if ng.match(target) {
			return false
		}
	}
	if len(s.positive) == 0 {
		return true
	}
	for _, pg := range s.positive {
		if pg.match(target) {
			return true
		}
	}
	return false
}

// ─── fff Fuzzy Path Scoring & Fallback ───────────────────────────────────────

var fuzzyStripRe = regexp.MustCompile(`[*?[\]{}]`)

func asciiLetterMask(s string) uint32 {
	var m uint32
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			m |= 1 << (c - 'a')
		} else if c >= 'A' && c <= 'Z' {
			m |= 1 << (c - 'A')
		}
	}
	return m
}

// scoreFuzzyPathFallback scores pathStr against lowercased query qLower with bitmask qMask.
// Returns 0 if qLower is not an ordered subsequence of pathStr.
func scoreFuzzyPathFallback(pathStr, qLower string, qMask uint32) int {
	pLen := len(pathStr)
	qLen := len(qLower)
	if qLen == 0 || pLen < qLen {
		return 0
	}

	var pMask uint32
	lastSlash := -1
	qi := 0
	for i := 0; i < pLen; i++ {
		c := pathStr[i]
		if c == '/' {
			lastSlash = i
		}
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c >= 'a' && c <= 'z' {
			pMask |= 1 << (c - 'a')
		}
		if qi < qLen && c == qLower[qi] {
			qi++
		}
	}
	if (pMask&qMask) != qMask || qi < qLen {
		return 0
	}

	lastPossible := make([]int, qLen)
	qi = qLen - 1
	for i := pLen - 1; i >= 0 && qi >= 0; i-- {
		c := pathStr[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c == qLower[qi] {
			lastPossible[qi] = i
			qi--
		}
	}

	baseStart := lastSlash + 1
	score := 100
	searchFrom := 0
	prevMatch := -1
	matchedInBasename := 0

	for k := 0; k < qLen; k++ {
		qc := qLower[k]
		limit := lastPossible[k]
		bestPos := -1
		bestBonus := -1

		for i := searchFrom; i <= limit; i++ {
			pc := pathStr[i]
			pcLower := pc
			if pcLower >= 'A' && pcLower <= 'Z' {
				pcLower += 'a' - 'A'
			}
			if pcLower != qc {
				continue
			}

			bonus := 0
			if i == baseStart {
				bonus = 50
			} else if prevMatch != -1 && i == prevMatch+1 {
				bonus = 35
			} else if i > 0 {
				prev := pathStr[i-1]
				if prev == '/' || prev == '_' || prev == '-' || prev == '.' {
					bonus = 28
				} else if prev >= 'a' && prev <= 'z' && pc >= 'A' && pc <= 'Z' {
					bonus = 24
				} else if i >= baseStart {
					bonus = 15
				}
			}
			if bonus > bestBonus {
				bestBonus = bonus
				bestPos = i
				if bonus >= 50 {
					break
				}
			}
		}
		if bestPos < 0 {
			return 0
		}
		score += 20 + bestBonus
		if prevMatch != -1 && bestPos > prevMatch+1 {
			score -= (bestPos - prevMatch - 1) * 2
		}
		if bestPos >= baseStart {
			matchedInBasename++
		}
		prevMatch = bestPos
		searchFrom = bestPos + 1
	}

	score -= pLen >> 2
	if matchedInBasename == qLen && pLen-baseStart == qLen {
		score += 80
	}
	if score < 1 {
		score = 1
	}
	return score
}

func fuzzyFilterPaths(displayPaths []string, pattern string, limit int) []string {
	cleaned := strings.TrimSpace(fuzzyStripRe.ReplaceAllString(pattern, ""))
	if cleaned == "" {
		return nil
	}
	if limit <= 0 || limit > fuzzyFallbackLimit {
		limit = fuzzyFallbackLimit
	}
	qLower := strings.ToLower(cleaned)
	qMask := asciiLetterMask(qLower)

	type scoredPath struct {
		path  string
		score int
	}
	var scored []scoredPath
	for _, p := range displayPaths {
		if s := scoreFuzzyPathFallback(p, qLower, qMask); s > 0 {
			scored = append(scored, scoredPath{path: p, score: s})
		}
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].path < scored[j].path
	})
	if len(scored) > limit {
		scored = scored[:limit]
	}
	out := make([]string, len(scored))
	for i, s := range scored {
		out[i] = s.path
	}
	return out
}

// ─── Native Find ─────────────────────────────────────────────────────────────

type nativeFindResult struct {
	paths         []string
	limitReached  bool
	fuzzyFallback bool
}

func nativeFindDetailed(ctx context.Context, pattern, searchPath string, limit int, fuzzyFallback bool) (nativeFindResult, error) {
	defer procpolicy.Widen()()
	info, err := os.Stat(searchPath)
	if err != nil {
		return nativeFindResult{}, fmt.Errorf("Path not found: %s", searchPath)
	}
	if !info.IsDir() {
		return nativeFindResult{}, fmt.Errorf("Path is not a directory: %s", searchPath)
	}

	tree, err := getCachedTree(ctx, searchPath, false)
	if err != nil {
		return nativeFindResult{}, err
	}

	effPattern := pattern
	if runtime.GOOS == "windows" {
		effPattern = strings.ReplaceAll(effPattern, `\`, "/")
	}
	for strings.HasPrefix(effPattern, "./") {
		effPattern = effPattern[2:]
	}
	isPathPattern := strings.Contains(effPattern, "/")
	ignoreCase := effPattern == strings.ToLower(effPattern)
	// An absolute pattern is matched against absolute paths (fd --full-path); one under the search path becomes relative to it.
	absoluteRoot := ""
	if isPathPattern && filepath.IsAbs(filepath.FromSlash(effPattern)) {
		root := filepath.ToSlash(searchPath)
		if !strings.HasSuffix(root, "/") {
			root += "/"
		}
		if strings.HasPrefix(effPattern, root) {
			effPattern = effPattern[len(root):]
			isPathPattern = strings.Contains(effPattern, "/")
		} else {
			absoluteRoot = root
		}
	}
	if strings.HasSuffix(effPattern, "/**") {
		effPattern = strings.TrimSuffix(effPattern, "**") + "**/*"
	}

	expanded, err := expandBraces(effPattern)
	if err != nil {
		return nativeFindResult{}, err
	}
	matchers := make([]*compiledGlob, 0, len(expanded))
	for _, pat := range expanded {
		cg, err := compileGlob(pat, ignoreCase)
		if err != nil {
			return nativeFindResult{}, err
		}
		matchers = append(matchers, cg)
	}

	var matched []string
	for i, e := range tree.entries {
		if ctx.Err() != nil {
			return nativeFindResult{}, ctx.Err()
		}
		target := e.rel
		if absoluteRoot != "" {
			target = absoluteRoot + e.rel
		} else if !isPathPattern {
			if idx := strings.LastIndexByte(e.rel, '/'); idx >= 0 {
				target = e.rel[idx+1:]
			}
		}
		ok := false
		for _, m := range matchers {
			if m.match(target) {
				ok = true
				break
			}
		}
		if ok {
			matched = append(matched, tree.displayPaths[i])
			if len(matched) > limit {
				break
			}
		}
	}

	if len(matched) == 0 && fuzzyFallback {
		fuzzy := fuzzyFilterPaths(tree.displayPaths, pattern, min(limit, fuzzyFallbackLimit))
		if len(fuzzy) > 0 {
			return nativeFindResult{paths: fuzzy, fuzzyFallback: true}, nil
		}
	}

	limitReached := len(matched) > limit
	if limitReached {
		matched = matched[:limit]
	}
	return nativeFindResult{paths: matched, limitReached: limitReached}, nil
}

// ─── Native Grep ─────────────────────────────────────────────────────────────

type nativeGrepOpts struct {
	Pattern     string
	SearchPath  string
	IsDirectory bool
	Glob        string
	IgnoreCase  bool
	Literal     bool
	Context     int
	Limit       int
	ReadFile    func(string) (string, error)
}

type nativeGrepEvent struct {
	absPath    string
	lineNumber int
	lineText   []byte
	isMatch    bool
}

type lineMatcher interface {
	prefilter(buf []byte) bool
	matchLine(line []byte) bool
}

type literalBytesMatcher struct {
	needle     []byte
	ignoreCase bool
	ascii      bool
}

func isASCIIBytes(b []byte) bool {
	for _, c := range b {
		if c >= 0x80 {
			return false
		}
	}
	return true
}

func indexFoldASCII(haystack, needle []byte) int {
	nLen := len(needle)
	if nLen == 0 {
		return 0
	}
	if len(haystack) < nLen {
		return -1
	}
	firstL := needle[0]
	if firstL >= 'A' && firstL <= 'Z' {
		firstL += 'a' - 'A'
	}
	firstU := firstL
	if firstL >= 'a' && firstL <= 'z' {
		firstU = firstL - ('a' - 'A')
	}
	limit := len(haystack) - nLen
	for i := 0; i <= limit; i++ {
		c := haystack[i]
		if c != firstL && c != firstU {
			continue
		}
		match := true
		for j := 1; j < nLen; j++ {
			hc := haystack[i+j]
			nc := needle[j]
			if hc >= 'A' && hc <= 'Z' {
				hc += 'a' - 'A'
			}
			if nc >= 'A' && nc <= 'Z' {
				nc += 'a' - 'A'
			}
			if hc != nc {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func (m *literalBytesMatcher) prefilter(buf []byte) bool {
	if !m.ignoreCase {
		return bytes.Index(buf, m.needle) >= 0
	}
	if m.ascii {
		return indexFoldASCII(buf, m.needle) >= 0
	}
	return true
}

func (m *literalBytesMatcher) matchLine(line []byte) bool {
	if !m.ignoreCase {
		return bytes.Index(line, m.needle) >= 0
	}
	if m.ascii {
		return indexFoldASCII(line, m.needle) >= 0
	}
	return bytes.Contains(bytes.ToLower(line), bytes.ToLower(m.needle))
}

type regexLineMatcher struct {
	re  *regexp.Regexp
	pre []byte
}

func requiredLiteral(pat string) []byte {
	// Extract longest literal run >= 3 chars outside groups/alternations/quantifiers/classes.
	var best, cur []byte
	flush := func() {
		if len(cur) > len(best) {
			best = append([]byte(nil), cur...)
		}
		cur = cur[:0]
	}
	depth := 0
	inClass := false
	for i := 0; i < len(pat); i++ {
		c := pat[i]
		if c == '\\' && i+1 < len(pat) {
			flush()
			i++
			continue
		}
		if inClass {
			if c == ']' {
				inClass = false
			}
			continue
		}
		switch c {
		case '[':
			flush()
			inClass = true
		case '(':
			flush()
			depth++
		case ')':
			flush()
			if depth > 0 {
				depth--
			}
		case '|', '^', '$', '.', '*', '+', '?', '{', '}':
			if (c == '*' || c == '+' || c == '?' || c == '{') && len(cur) > 0 {
				cur = cur[:len(cur)-1]
			}
			flush()
		default:
			if depth == 0 && c < 0x80 {
				cur = append(cur, c)
			} else {
				flush()
			}
		}
	}
	flush()
	if len(best) >= 3 {
		return best
	}
	return nil
}

func (m *regexLineMatcher) prefilter(buf []byte) bool {
	if len(m.pre) == 0 {
		return true
	}
	return bytes.Index(buf, m.pre) >= 0
}

func (m *regexLineMatcher) matchLine(line []byte) bool {
	return m.re.Match(line)
}

func buildLineMatcher(pattern string, ignoreCase, literal bool) (lineMatcher, error) {
	if literal {
		b := []byte(pattern)
		if ignoreCase && !isASCIIBytes(b) {
			re, err := regexp.Compile("(?i)" + regexp.QuoteMeta(pattern))
			if err != nil {
				return nil, err
			}
			return &regexLineMatcher{re: re}, nil
		}
		return &literalBytesMatcher{needle: b, ignoreCase: ignoreCase, ascii: isASCIIBytes(b)}, nil
	}
	expr := pattern
	if ignoreCase && !strings.HasPrefix(expr, "(?i)") {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		// Worded as ripgrep words it, so a model that knows rg's message recognizes it.
		var syntaxErr *syntax.Error
		if errors.As(err, &syntaxErr) {
			return nil, fmt.Errorf("regex parse error: %s: `%s`", syntaxErr.Code, syntaxErr.Expr)
		}
		return nil, err
	}
	var pre []byte
	if !ignoreCase && !strings.HasPrefix(pattern, "(?i)") && !strings.Contains(pattern, "|") {
		pre = requiredLiteral(pattern)
	}
	return &regexLineMatcher{re: re, pre: pre}, nil
}

func nativeGrep(ctx context.Context, opts nativeGrepOpts, emit func(nativeGrepEvent) (stop bool)) (matchCount int, matchLimitReached bool, err error) {
	defer procpolicy.Widen()()
	matcher, err := buildLineMatcher(opts.Pattern, opts.IgnoreCase, opts.Literal)
	if err != nil {
		return 0, false, err
	}

	var files []walkEntry
	if !opts.IsDirectory {
		files = []walkEntry{{rel: filepath.Base(opts.SearchPath), abs: opts.SearchPath, kind: kindFile}}
	} else {
		tree, err := getCachedTree(ctx, opts.SearchPath, true)
		if err != nil {
			return 0, false, err
		}
		globSet, err := compileGrepGlob(opts.Glob)
		if err != nil {
			return 0, false, err
		}
		files = make([]walkEntry, 0, len(tree.entries))
		for _, e := range tree.entries {
			if e.kind == kindFile && globSet.match(e.rel) {
				files = append(files, e)
			}
		}
		// ripgrep --glob override: include explicitly matched ignored files.
		if globSet != nil && len(globSet.positive) > 0 && len(tree.ignoredFiles) > 0 {
			for _, e := range tree.ignoredFiles {
				if globSet.match(e.rel) {
					files = append(files, e)
				}
			}
			sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
		}
	}

	if len(files) >= 8 && opts.ReadFile == nil && opts.Limit > 0 {
		workers := min(max(runtime.GOMAXPROCS(0), 2), 8)
		type fileBatch struct {
			events []nativeGrepEvent
			limit  bool
		}
		results := make([]fileBatch, len(files))
		// Process files in ordered chunks to bound memory and stop early once Limit is reached.
		chunkSize := max(workers*4, 16)
		for base := 0; base < len(files); base += chunkSize {
			if ctx.Err() != nil {
				return matchCount, matchLimitReached, ctx.Err()
			}
			end := min(base+chunkSize, len(files))
			var nextIdx sync.Mutex
			cursor := base
			var wg sync.WaitGroup
			wg.Add(workers)
			for range workers {
				go func() {
					defer wg.Done()
					for {
						if ctx.Err() != nil {
							return
						}
						nextIdx.Lock()
						idx := cursor
						cursor++
						nextIdx.Unlock()
						if idx >= end {
							return
						}
						var evs []nativeGrepEvent
						// The lines are copied (a pooled read buffer is reused once grepOneFile returns) into one growing buffer per file, not
						// one allocation per line: 200 lines of context cost about what 2 do. A slice taken before the buffer grows keeps the old
						// array alive and unchanged.
						var lineBuf []byte
						localCount := 0
						localLimit := false
						_, _ = grepOneFile(ctx, files[idx].abs, matcher, opts, &localCount, &localLimit, func(ev nativeGrepEvent) bool {
							start := len(lineBuf)
							lineBuf = append(lineBuf, ev.lineText...)
							ev.lineText = lineBuf[start:len(lineBuf):len(lineBuf)]
							evs = append(evs, ev)
							return matchCount+localCount >= opts.Limit
						})
						if len(evs) > 0 || localLimit {
							results[idx] = fileBatch{events: evs, limit: localLimit}
						}
					}
				}()
			}
			wg.Wait()
			if ctx.Err() != nil {
				return matchCount, matchLimitReached, ctx.Err()
			}
			for i := base; i < end; i++ {
				for _, ev := range results[i].events {
					if ev.isMatch {
						if matchCount >= opts.Limit {
							matchLimitReached = true
							return matchCount, matchLimitReached, nil
						}
						matchCount++
					}
					if emit(ev) {
						return matchCount, matchLimitReached, nil
					}
				}
				if results[i].limit || matchCount >= opts.Limit {
					matchLimitReached = true
					return matchCount, matchLimitReached, nil
				}
			}
		}
		return matchCount, matchLimitReached, nil
	}

	for _, f := range files {
		if ctx.Err() != nil {
			return matchCount, matchLimitReached, ctx.Err()
		}
		stop, err := grepOneFile(ctx, f.abs, matcher, opts, &matchCount, &matchLimitReached, emit)
		if err != nil {
			continue
		}
		if stop {
			break
		}
	}
	return matchCount, matchLimitReached, nil
}

func grepOneFile(
	ctx context.Context,
	absPath string,
	matcher lineMatcher,
	opts nativeGrepOpts,
	matchCount *int,
	matchLimitReached *bool,
	emit func(nativeGrepEvent) (stop bool),
) (bool, error) {
	if opts.ReadFile != nil {
		content, err := opts.ReadFile(absPath)
		if err != nil {
			return false, err
		}
		buf := []byte(content)
		if decoded, ok := decodeUTF16BOM(buf); ok {
			buf = decoded
		} else if bytes.IndexByte(buf, 0) != -1 {
			return false, nil
		}
		return searchBufferLines(absPath, buf, matcher, opts, matchCount, matchLimitReached, emit), nil
	}

	key := normalizeCacheKey(absPath)
	now := time.Now()

	searchCacheMu.Lock()
	var cached *cachedFileBuffer
	if elem, ok := contentCacheMap[key]; ok {
		cached = elem.Value.(*cachedFileBuffer)
	}
	extAt := externalChangeAt
	if cached != nil && (extAt.IsZero() || cached.verifiedAt.After(extAt)) &&
		now.Sub(cached.verifiedAt) < contentCacheStatFree && cached.size <= wholeFileBytes {
		contentCacheList.MoveToBack(contentCacheMap[key])
		cached.usedAt = now
		buf := cached.buffer
		searchCacheMu.Unlock()
		if buf == nil {
			return false, nil // binary
		}
		return searchBufferLines(absPath, buf, matcher, opts, matchCount, matchLimitReached, emit), nil
	}
	searchCacheMu.Unlock()

	// A file the cache holds is checked by its path (a hit needs no open); any other is opened at once and checked on its handle, one path
	// lookup instead of two.
	searchCacheMu.Lock()
	_, known := contentCacheMap[key]
	searchCacheMu.Unlock()
	var file *os.File
	var fi os.FileInfo
	var err error
	if known {
		fi, err = os.Stat(absPath)
	} else if file, err = os.Open(absPath); err == nil {
		defer func() { _ = file.Close() }()
		fi, err = file.Stat()
	}
	if err != nil || !fi.Mode().IsRegular() {
		return false, err
	}
	size := fi.Size()
	mtime := fi.ModTime().UnixNano()

	if size > wholeFileBytes {
		return searchInPieces(ctx, absPath, matcher, opts, matchCount, matchLimitReached, emit)
	}

	searchCacheMu.Lock()
	if elem, ok := contentCacheMap[key]; ok {
		c := elem.Value.(*cachedFileBuffer)
		if c.mtimeNanos == mtime && c.size == size {
			c.verifiedAt = now
			c.usedAt = now
			contentCacheList.MoveToBack(elem)
			buf := c.buffer
			searchCacheMu.Unlock()
			if buf == nil {
				return false, nil
			}
			return searchBufferLines(absPath, buf, matcher, opts, matchCount, matchLimitReached, emit), nil
		}
	}
	searchCacheMu.Unlock()

	// A file the cache will take is read into its own buffer, which the cache keeps; any other into a pooled buffer.
	searchCacheMu.Lock()
	admit := size <= maxCachedFileBytes && contentCacheAdmitsLocked(size, now)
	searchCacheMu.Unlock()
	var pooled *[]byte
	var scratch []byte
	if !admit {
		pooled = readBufPool.Get().(*[]byte)
		scratch = *pooled
	}
	if file == nil {
		if file, err = os.Open(absPath); err != nil {
			if pooled != nil {
				readBufPool.Put(pooled)
			}
			return false, err
		}
		defer func() { _ = file.Close() }()
	}
	raw, err := readWholeFile(file, size, scratch)
	if err != nil {
		if pooled != nil {
			readBufPool.Put(pooled)
		}
		return false, err
	}
	if decoded, ok := decodeUTF16BOM(raw); ok {
		if admit {
			searchCacheMu.Lock()
			storeContentLocked(key, mtime, int64(len(raw)), raw, false, now)
			searchCacheMu.Unlock()
		} else if pooled != nil {
			readBufPool.Put(pooled)
		}
		return searchBufferLines(absPath, decoded, matcher, opts, matchCount, matchLimitReached, emit), nil
	}
	if admit {
		searchCacheMu.Lock()
		storeContentLocked(key, mtime, int64(len(raw)), raw, true, now)
		searchCacheMu.Unlock()
	} else {
		defer func() {
			if cap(raw) <= 4<<20 {
				*pooled = raw[:0]
				readBufPool.Put(pooled)
			}
		}()
	}

	if bytes.IndexByte(raw, 0) != -1 {
		return false, nil
	}
	return searchBufferLines(absPath, raw, matcher, opts, matchCount, matchLimitReached, emit), nil
}

func searchBufferLines(
	absPath string,
	buf []byte,
	matcher lineMatcher,
	opts nativeGrepOpts,
	matchCount *int,
	matchLimitReached *bool,
	emit func(nativeGrepEvent) (stop bool),
) bool {
	if len(buf) == 0 || !matcher.prefilter(buf) {
		return false
	}
	if opts.Context <= 0 {
		pos := 0
		lineNo := 1
		for pos < len(buf) {
			relNl := bytes.IndexByte(buf[pos:], '\n')
			var line []byte
			if relNl < 0 {
				line = buf[pos:]
				pos = len(buf)
			} else {
				line = buf[pos : pos+relNl]
				pos += relNl + 1
			}
			clean := bytes.TrimSuffix(line, []byte("\r"))
			if matcher.matchLine(clean) {
				if *matchCount >= opts.Limit {
					*matchLimitReached = true
					return true
				}
				*matchCount++
				if emit(nativeGrepEvent{absPath: absPath, lineNumber: lineNo, lineText: line, isMatch: true}) {
					return true
				}
			}
			lineNo++
		}
		return false
	}
	lines := bytes.Split(buf, []byte("\n"))
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}

	var matchedLines []int
	for idx, line := range lines {
		lineNo := idx + 1
		clean := bytes.TrimSuffix(line, []byte("\r"))
		if matcher.matchLine(clean) {
			matchedLines = append(matchedLines, lineNo)
		}
	}
	if len(matchedLines) == 0 {
		return false
	}

	// Emit with context windows (no duplicate lines across overlapping windows).
	isMatchSet := make(map[int]bool, len(matchedLines))
	for _, ln := range matchedLines {
		isMatchSet[ln] = true
	}
	lastEmitted := 0
	for _, ln := range matchedLines {
		if *matchCount >= opts.Limit {
			*matchLimitReached = true
			return true
		}
		start := max(1, ln-opts.Context)
		end := min(len(lines), ln+opts.Context)
		for cur := max(start, lastEmitted+1); cur <= end; cur++ {
			isM := isMatchSet[cur]
			if isM {
				if *matchCount >= opts.Limit {
					// A match inside the last allowed match's trailing context is shown as context, as microPi's native-search.ts
					// (context from the file's lines) and rg do.
					*matchLimitReached = true
					isM = false
				} else {
					*matchCount++
				}
			}
			if emit(nativeGrepEvent{absPath: absPath, lineNumber: cur, lineText: lines[cur-1], isMatch: isM}) {
				return true
			}
			lastEmitted = cur
		}
		if *matchLimitReached {
			return true
		}
	}
	return false
}

// pieceMatch is a line searchInPieces found, held until the file was read to its end.
type pieceMatch struct {
	lineNumber int
	lineText   []byte
}

// searchInPieces searches a file too large to read whole a piece at a time, with the outcome of a search of the file
// read whole (microPi's searchInPieces): each piece is searched up to its last line break and the rest goes with the
// next, so a match is within a line; and a file with a NUL byte anywhere (binary) has no matches, so the matches are
// held until the file was read to its end (or to its first NUL byte). It holds at most the matches the limit leaves
// room for, and one more, which tells that the limit stopped the search. With context, the lines around the matches
// are read in a second pass, as microPi's grep reads them from the file (formatGrepMatches, getFileLines).
func searchInPieces(
	ctx context.Context,
	absPath string,
	matcher lineMatcher,
	opts nativeGrepOpts,
	matchCount *int,
	matchLimitReached *bool,
	emit func(nativeGrepEvent) (stop bool),
) (bool, error) {
	f, err := os.Open(absPath)
	if err != nil {
		return false, err
	}
	defer f.Close()

	budget := opts.Limit - *matchCount
	var found []pieceMatch
	buf := make([]byte, pieceBytes)
	var carry []byte
	lineNo := 1

	for {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		n, readErr := io.ReadFull(f, buf)
		if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
			return false, readErr
		}
		atEnd := readErr != nil
		if n == 0 && len(carry) == 0 {
			break
		}
		chunk := buf[:n]
		if bytes.IndexByte(chunk, 0) != -1 {
			return false, nil // (Binary.)
		}
		if len(found) > budget {
			// The limit is reached: the rest is only read for a NUL byte.
			if atEnd {
				break
			}
			continue
		}

		combined := chunk
		if len(carry) > 0 {
			combined = make([]byte, len(carry)+len(chunk))
			copy(combined, carry)
			copy(combined[len(carry):], chunk)
		}

		var process []byte
		if atEnd {
			process = combined
			carry = nil
		} else {
			lastNL := bytes.LastIndexByte(combined, '\n')
			if lastNL < 0 {
				carry = append(carry[:0], combined...)
				continue
			}
			process = combined[:lastNL+1]
			carry = append(carry[:0], combined[lastNL+1:]...)
		}

		if len(process) > 0 {
			if matcher.prefilter(process) {
				lines := bytes.Split(process, []byte("\n"))
				if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
					lines = lines[:len(lines)-1]
				}
				for _, l := range lines {
					if matcher.matchLine(bytes.TrimSuffix(l, []byte("\r"))) {
						found = append(found, pieceMatch{lineNumber: lineNo, lineText: bytes.Clone(l)})
						if len(found) > budget {
							break
						}
					}
					lineNo++
				}
			} else {
				lineNo += bytes.Count(process, []byte("\n"))
			}
		}

		if atEnd {
			break
		}
	}
	if len(found) == 0 {
		return false, nil
	}
	if opts.Context > 0 {
		return emitPieceContext(ctx, f, absPath, found, opts, matchCount, matchLimitReached, emit)
	}
	for _, m := range found {
		if *matchCount >= opts.Limit {
			*matchLimitReached = true
			return true, nil
		}
		*matchCount++
		if emit(nativeGrepEvent{absPath: absPath, lineNumber: m.lineNumber, lineText: m.lineText, isMatch: true}) {
			return true, nil
		}
	}
	return false, nil
}

// plannedLine is a line emitPieceContext emits, or the point where the match limit stops it.
type plannedLine struct {
	lineNumber int
	isMatch    bool
	stop       bool
}

// emitPieceContext emits found with opts.Context lines around each, read from f again, in the order and with the
// limit searchBufferLines applies to a file read whole: lines shared by two windows once.
func emitPieceContext(
	ctx context.Context,
	f *os.File,
	absPath string,
	found []pieceMatch,
	opts nativeGrepOpts,
	matchCount *int,
	matchLimitReached *bool,
	emit func(nativeGrepEvent) (stop bool),
) (bool, error) {
	isMatch := make(map[int]bool, len(found))
	for _, m := range found {
		isMatch[m.lineNumber] = true
	}
	// The lines to emit, as searchBufferLines walks the windows; lines past the end of the file are never read.
	var plan []plannedLine
	count := *matchCount
	lastEmitted := 0
planning:
	for _, m := range found {
		if count >= opts.Limit {
			plan = append(plan, plannedLine{stop: true})
			break
		}
		overLimit := false
		for cur := max(1, m.lineNumber-opts.Context, lastEmitted+1); cur <= m.lineNumber+opts.Context; cur++ {
			lineIsMatch := isMatch[cur]
			if lineIsMatch {
				if count >= opts.Limit {
					// A match past the limit inside the window is the last allowed match's context.
					overLimit = true
					lineIsMatch = false
				} else {
					count++
				}
			}
			plan = append(plan, plannedLine{lineNumber: cur, isMatch: lineIsMatch})
			lastEmitted = cur
		}
		if overLimit {
			plan = append(plan, plannedLine{stop: true})
			break planning
		}
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return false, err
	}
	reader := bufio.NewReaderSize(f, 64*1024)
	lineNo := 0
	var line []byte
	for _, step := range plan {
		if step.stop {
			*matchLimitReached = true
			return true, nil
		}
		for lineNo < step.lineNumber {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			read, ok, err := readPieceLine(reader, lineNo+1 == step.lineNumber)
			if err != nil {
				return false, err
			}
			if !ok {
				return false, nil // (Past the end of the file.)
			}
			line = read
			lineNo++
		}
		if step.isMatch {
			*matchCount++
		}
		if emit(nativeGrepEvent{absPath: absPath, lineNumber: step.lineNumber, lineText: line, isMatch: step.isMatch}) {
			return true, nil
		}
	}
	return false, nil
}

// readPieceLine reads one line without its line break; keep false skips it without holding it. ok is false past the
// last line (as a file read whole, the nothing after a last line break is no line).
func readPieceLine(reader *bufio.Reader, keep bool) (line []byte, ok bool, err error) {
	for {
		part, err := reader.ReadSlice('\n')
		if len(part) > 0 {
			ok = true
			if keep {
				line = append(line, part...)
			}
		}
		switch {
		case err == nil:
			if keep {
				line = line[:len(line)-1]
			}
			return line, true, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return line, ok, nil
		default:
			return nil, false, err
		}
	}
}

// unused guard for utf8/strconv if needed by future helpers
var _ = utf8.RuneCount
var _ = strconv.Itoa

// decodeUTF16BOM decodes raw to UTF-8 when it starts with a UTF-16 byte order mark, as ripgrep does by default; the mark itself is dropped.
func decodeUTF16BOM(raw []byte) ([]byte, bool) {
	if len(raw) < 2 {
		return nil, false
	}
	var order binary.ByteOrder
	switch {
	case raw[0] == 0xFF && raw[1] == 0xFE:
		order = binary.LittleEndian
	case raw[0] == 0xFE && raw[1] == 0xFF:
		order = binary.BigEndian
	default:
		return nil, false
	}
	body := raw[2:]
	units := make([]uint16, len(body)/2)
	for i := range units {
		units[i] = order.Uint16(body[2*i:])
	}
	out := make([]byte, 0, len(body))
	for _, r := range utf16.Decode(units) {
		out = utf8.AppendRune(out, r)
	}
	if len(body)%2 == 1 {
		out = utf8.AppendRune(out, utf8.RuneError)
	}
	return out, true
}
