package docsearch

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/ignorerules"
)

const (
	DefaultMaxFileBytes = 4 << 20 // 4 MiB
	DefaultMaxFiles     = 50_000
	binaryProbeBytes    = 8192
)

// FileMeta describes an indexed file for signature tracking and incremental diffing.
type FileMeta struct {
	Rel        string
	Abs        string
	MtimeNanos int64
	Size       int64
}

var skippedDirs = map[string]struct{}{
	".git":         {},
	"node_modules": {},
	"target":       {},
}

// ResolveRoot finds the git repository root above cwd, or returns cwd.
// Returns "" if the resolved root is $HOME or "/".
func ResolveRoot(cwd string) string {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return ""
		}
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return ""
	}
	root := abs
	for cur := abs; ; {
		if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
			root = cur
			break
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	if root == "/" || root == filepath.Dir("/") {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if absHome, err := filepath.Abs(home); err == nil && root == absHome {
			return ""
		}
	}
	return root
}

// CountFiles counts regular non-ignored files under root up to limit+1 using pure Go.
func CountFiles(root string, limit int) int {
	if root == "" || limit < 0 {
		return 0
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return 0
	}
	count := 0
	stopErr := fs.ErrPermission // sentinel to stop WalkDir early
	rulesByDir := make(map[string][]ignorerules.Rule)

	_ = filepath.WalkDir(absRoot, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if p != absRoot {
				if _, skip := skippedDirs[name]; skip {
					return filepath.SkipDir
				}
				if strings.HasPrefix(name, ".") {
					return filepath.SkipDir
				}
				parent := filepath.Dir(p)
				if ignorerules.Ignored(p, true, absRoot, rulesByDir[parent]) {
					return filepath.SkipDir
				}
			}
			parentRules := rulesByDir[filepath.Dir(p)]
			rules := make([]ignorerules.Rule, len(parentRules))
			copy(rules, parentRules)
			rules = ignorerules.Append(rules, p, absRoot)
			rulesByDir[p] = rules
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		parent := filepath.Dir(p)
		if ignorerules.Ignored(p, false, absRoot, rulesByDir[parent]) {
			return nil
		}
		count++
		if count > limit {
			return stopErr
		}
		return nil
	})
	return count
}

// Scan walks roots in pure Go, respecting .gitignore/.ignore rules and skipping
// .git, node_modules, target, oversized files, and binary files.
func Scan(roots []string, maxFileBytes int64, maxFiles int) ([]FileMeta, error) {
	if maxFileBytes <= 0 {
		maxFileBytes = DefaultMaxFileBytes
	}
	if maxFiles <= 0 {
		maxFiles = DefaultMaxFiles
	}
	multiRoot := len(roots) > 1
	var files []FileMeta
	seenAbs := make(map[string]struct{})

	for _, r := range roots {
		absRoot, err := filepath.Abs(r)
		if err != nil {
			continue
		}
		info, err := os.Stat(absRoot)
		if err != nil || !info.IsDir() {
			continue
		}
		rootPrefix := ""
		if multiRoot {
			rootPrefix = filepath.Base(absRoot) + "/"
		}
		rulesByDir := make(map[string][]ignorerules.Rule)

		err = filepath.WalkDir(absRoot, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if len(files) >= maxFiles {
				return fs.SkipAll
			}
			name := d.Name()
			if d.IsDir() {
				if p != absRoot {
					if _, skip := skippedDirs[name]; skip {
						return filepath.SkipDir
					}
					parent := filepath.Dir(p)
					if ignorerules.Ignored(p, true, absRoot, rulesByDir[parent]) {
						return filepath.SkipDir
					}
				}
				parentRules := rulesByDir[filepath.Dir(p)]
				rules := make([]ignorerules.Rule, len(parentRules))
				copy(rules, parentRules)
				rules = ignorerules.Append(rules, p, absRoot)
				rulesByDir[p] = rules
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			parent := filepath.Dir(p)
			if ignorerules.Ignored(p, false, absRoot, rulesByDir[parent]) {
				return nil
			}
			if _, dup := seenAbs[p]; dup {
				return nil
			}
			fi, err := d.Info()
			if err != nil || fi.Size() > maxFileBytes {
				return nil
			}
			rel, err := filepath.Rel(absRoot, p)
			if err != nil {
				return nil
			}
			rel = rootPrefix + filepath.ToSlash(rel)
			seenAbs[p] = struct{}{}
			files = append(files, FileMeta{
				Rel:        rel,
				Abs:        p,
				MtimeNanos: fi.ModTime().UnixNano(),
				Size:       fi.Size(),
			})
			return nil
		})
		if err != nil {
			return nil, err
		}
		if len(files) >= maxFiles {
			break
		}
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].Rel < files[j].Rel
	})
	return files, nil
}

// readTextFile reads a file if it is <= maxBytes and not binary (no NUL in first 8192 bytes).
// Returns ("", false, nil) if binary or too large.
func readTextFile(absPath string, maxBytes int64) (string, bool, error) {
	f, err := os.Open(absPath)
	if err != nil {
		return "", false, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxBytes {
		return "", false, nil
	}

	var head [binaryProbeBytes]byte
	n, err := io.ReadFull(f, head[:])
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", false, err
	}
	if bytes.IndexByte(head[:n], 0) >= 0 {
		return "", false, nil
	}
	if n < len(head) {
		return string(head[:n]), true, nil
	}
	rest, err := io.ReadAll(io.LimitReader(f, maxBytes-int64(n)+1))
	if err != nil {
		return "", false, err
	}
	if int64(n+len(rest)) > maxBytes {
		return "", false, nil
	}
	buf := make([]byte, n+len(rest))
	copy(buf, head[:n])
	copy(buf[n:], rest)
	return string(buf), true, nil
}
