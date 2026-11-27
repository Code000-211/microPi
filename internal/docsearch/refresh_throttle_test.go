package docsearch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The change check waits five times its last walk, at least checkEvery; a file the agent wrote makes the next search check at once.
func TestRefreshThrottleFollowsTheWalkAndAWriteResetsIt(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DOCSEARCH_CACHE_DIR", t.TempDir())
	t.Cleanup(closeSharedEngines)
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("# A\nalpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng := NewEngine([]string{dir})
	defer eng.Close()
	enginesMu.Lock()
	engines["test"] = eng
	enginesMu.Unlock()

	eng.lastScan.Store(int64(10 * time.Millisecond))
	if got := eng.refreshThrottle(); got != checkEvery {
		t.Fatalf("throttle after a 10 ms walk = %v, want %v", got, checkEvery)
	}
	eng.lastScan.Store(int64(800 * time.Millisecond))
	if got := eng.refreshThrottle(); got != 4*time.Second {
		t.Fatalf("throttle after a 0.8 s walk = %v, want 4s", got)
	}

	eng.mu.Lock()
	eng.lastCheck = time.Now()
	eng.mu.Unlock()
	NotifyFileChanged(filepath.Join(dir, "a.md"))
	eng.mu.RLock()
	reset := eng.lastCheck.IsZero()
	eng.mu.RUnlock()
	if !reset {
		t.Fatal("a write inside the index's root did not make the next search check")
	}
	eng.mu.Lock()
	eng.lastCheck = time.Now()
	eng.mu.Unlock()
	NotifyFileChanged(filepath.Join(t.TempDir(), "elsewhere.md"))
	eng.mu.RLock()
	untouched := !eng.lastCheck.IsZero()
	eng.mu.RUnlock()
	if !untouched {
		t.Fatal("a write outside the index's roots reset its check")
	}
}
