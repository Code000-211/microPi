package tools

import (
	"bytes"
	"container/list"
	"fmt"
	"testing"
	"time"
)

// A full cache takes a new file only when its least recently used entry has not been used for contentCacheKeepRecent: a search over a
// tree larger than the cache keeps what it holds, and between searches further apart the cache follows what is searched.
func TestContentCacheAdmissionKeepsRecentlyUsedEntries(t *testing.T) {
	searchCacheMu.Lock()
	savedList, savedMap, savedSize := contentCacheList, contentCacheMap, contentCacheSize
	contentCacheList, contentCacheMap, contentCacheSize = list.New(), map[string]*list.Element{}, 0
	defer func() {
		contentCacheList, contentCacheMap, contentCacheSize = savedList, savedMap, savedSize
		searchCacheMu.Unlock()
	}()

	now := time.Now()
	chunk := bytes.Repeat([]byte("a"), maxCachedFileBytes)
	for i := 0; int64(i+1)*int64(len(chunk)) <= maxContentCacheBytes; i++ {
		if !storeContentLocked(fmt.Sprintf("f%d", i), 1, int64(len(chunk)), append([]byte(nil), chunk...), true, now) {
			t.Fatalf("file %d not stored while the cache had room", i)
		}
	}
	if contentCacheAdmitsLocked(int64(len(chunk)), now.Add(time.Second)) {
		t.Fatal("a full cache of recently used entries admitted a new file")
	}
	if storeContentLocked("late", 1, int64(len(chunk)), append([]byte(nil), chunk...), true, now.Add(time.Second)) {
		t.Fatal("a new file evicted a recently used entry")
	}
	later := now.Add(contentCacheKeepRecent + time.Second)
	if !storeContentLocked("after", 1, int64(len(chunk)), append([]byte(nil), chunk...), true, later) {
		t.Fatal("a new file was not stored once the oldest entry was stale")
	}
	if _, ok := contentCacheMap["f0"]; ok {
		t.Fatal("the least recently used entry was not evicted for it")
	}
	if contentCacheSize > maxContentCacheBytes {
		t.Fatalf("cache size %d over the limit", contentCacheSize)
	}
}
