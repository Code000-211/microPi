package docsearch

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAdversarialConcurrencyStress(t *testing.T) {
	dir := t.TempDir()
	cacheDir := t.TempDir()
	t.Setenv("DOCSEARCH_CACHE_DIR", cacheDir)
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())

	// Create 30 initial files
	for i := range 30 {
		p := filepath.Join(dir, fmt.Sprintf("mod_%02d.go", i))
		content := fmt.Sprintf("package mod\n\n// Func%d does work.\nfunc Func%d() int {\n\treturn %d\n}\n", i, i, i)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Pre-build and save index to disk so NewEngine loads it via mmap!
	idx, err := Build(Config{Roots: []string{dir}})
	if err != nil {
		t.Fatal(err)
	}
	idxPath := indexCachePath([]string{dir}, nil)
	if err := idx.Save(idxPath); err != nil {
		t.Fatal(err)
	}
	_ = idx.Close()

	eng := NewEngine([]string{dir})
	defer eng.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if !eng.WaitReady(ctx) {
		t.Fatal("engine did not become ready")
	}

	var wg sync.WaitGroup
	var stop atomic.Bool

	// Searchers
	for g := range 6 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			queries := []string{"Func01", "Func15", "OverlayAdded", "does work", "mod_05"}
			for i := 0; !stop.Load(); i++ {
				q := queries[(id+i)%len(queries)]
				_, _ = eng.Search(ctx, q, SearchOpts{K: 5}, 100*time.Millisecond)
				_ = eng.Stats()
			}
		}(g)
	}

	// Refresher + Rebuilder (forces mmap unmap via setView while searches run!)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; !stop.Load(); i++ {
			eng.MaybeRefresh()
			if i%5 == 0 {
				eng.rebuildSync(true)
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()

	// File mutator: add, update, delete
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; !stop.Load(); i++ {
			p := filepath.Join(dir, fmt.Sprintf("dyn_%02d.go", i%10))
			if i%3 == 0 {
				_ = os.Remove(p)
			} else {
				content := fmt.Sprintf("package mod\n\nfunc OverlayAdded%d() string { return \"v%d\" }\n", i%10, i)
				_ = os.WriteFile(p, []byte(content), 0o644)
			}
			time.Sleep(3 * time.Millisecond)
		}
	}()

	time.Sleep(800 * time.Millisecond)
	stop.Store(true)
	wg.Wait()
}

func TestAdversarialCorruptIdxAndOkStamp(t *testing.T) {
	dir := t.TempDir()
	cacheDir := t.TempDir()
	t.Setenv("DOCSEARCH_CACHE_DIR", cacheDir)
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())

	if err := os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package main\n\nfunc HelloWorld() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	idx, err := Build(Config{Roots: []string{dir}})
	if err != nil {
		t.Fatal(err)
	}
	goodPath := filepath.Join(t.TempDir(), "good.idx")
	if err := idx.Save(goodPath); err != nil {
		t.Fatal(err)
	}
	_ = idx.Close()

	goodBytes, err := os.ReadFile(goodPath)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Truncation at various offsets (must return error, never panic)
	truncOffsets := []int{0, 1, 7, 8, 55, 56, 64, len(goodBytes) / 2, len(goodBytes) - 1}
	for _, off := range truncOffsets {
		p := filepath.Join(t.TempDir(), fmt.Sprintf("trunc_%d.idx", off))
		if err := os.WriteFile(p, goodBytes[:off], 0o644); err != nil {
			t.Fatal(err)
		}
		loaded, err := Load(p, []string{dir})
		if err == nil {
			_ = loaded.Close()
			t.Fatalf("expected error loading truncated idx at %d/%d", off, len(goodBytes))
		}
	}

	// 2. Corrupt .ok stamp variants on a valid .idx (must still load cleanly via full validation)
	okVariants := [][]byte{
		{},
		{1, 2, 3},
		make([]byte, 23),
		make([]byte, 25),
		make([]byte, 24), // zeros: mismatch dev/ino/size
	}
	for i, okData := range okVariants {
		sub := t.TempDir()
		p := filepath.Join(sub, "test.idx")
		if err := os.WriteFile(p, goodBytes, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p+".ok", okData, 0o644); err != nil {
			t.Fatal(err)
		}
		loaded, err := Load(p, []string{dir})
		if err != nil {
			t.Fatalf("variant %d: Load failed with corrupt .ok stamp: %v", i, err)
		}
		_ = loaded.Close()
	}

	// 3. Crafted block length overflow (math.MaxInt64 - 3 -> align8 overflows to negative!)
	t.Run("CraftedBlockLenOverflow", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "overflow.idx")
		bad := make([]byte, len(goodBytes))
		copy(bad, goodBytes)
		// First block length is at offset headerSize (56)
		binary.LittleEndian.PutUint64(bad[headerSize:headerSize+8], uint64(math.MaxInt64-3))
		if err := os.WriteFile(p, bad, 0o644); err != nil {
			t.Fatal(err)
		}
		loaded, err := Load(p, []string{dir})
		if err == nil {
			_ = loaded.Close()
			t.Fatal("expected error for overflow block length")
		}
	})

	// 4. Engine recovery when cached .idx is corrupt
	t.Run("EngineRecoversFromCorruptCache", func(t *testing.T) {
		engIdxPath := indexCachePath([]string{dir}, nil)
		if err := os.MkdirAll(filepath.Dir(engIdxPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(engIdxPath, []byte("CORRUPT_GARBAGE_DATA_0123456789012345678901234567890123456789"), 0o644); err != nil {
			t.Fatal(err)
		}
		eng := NewEngine([]string{dir})
		defer eng.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if !eng.WaitReady(ctx) {
			t.Fatal("engine did not become ready after corrupt cache")
		}
		res, ready := eng.Search(ctx, "HelloWorld", SearchOpts{K: 5}, time.Second)
		if !ready || len(res.Hits) == 0 {
			t.Fatalf("expected valid search after rebuild from corrupt cache, ready=%v hits=%d", ready, len(res.Hits))
		}
	})
}

func TestAdversarialCountFilesAndNewToolBoundary(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PI_DOCSEARCH", "")

	dir := t.TempDir()

	// Create .git, node_modules, and .gitignore'd files that should NOT count
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range 10 {
		_ = os.WriteFile(filepath.Join(dir, ".git", fmt.Sprintf("g%d", i)), []byte("x"), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "node_modules", fmt.Sprintf("n%d", i)), []byte("x"), 0o644)
	}
	_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.ignored\n"), 0o644)
	for i := range 5 {
		_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("skip_%d.ignored", i)), []byte("x"), 0o644)
	}
	// µPi: as index.ts countFiles, a .git that git cannot list falls back to a walk past hidden names, node_modules
	// and target, which does not read .gitignore: the 5 ignored files count and .gitignore does not. 145 more -> 150.
	for i := range 145 {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f_%03d.txt", i)), []byte("hi"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if got := CountFiles(dir, SmallProjectFiles); got != 150 {
		t.Fatalf("CountFiles = %d, want 150", got)
	}
	if tool := NewTool(dir); tool != nil {
		t.Fatalf("NewTool at 150 files = %v, want nil", tool)
	}

	// Add 151st file
	if err := os.WriteFile(filepath.Join(dir, "f_150.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := CountFiles(dir, SmallProjectFiles); got != 151 {
		t.Fatalf("CountFiles = %d, want 151", got)
	}
	if tool := NewTool(dir); tool == nil {
		t.Fatal("NewTool at 151 files = nil, want non-nil")
	}

	// PI_DOCSEARCH=off (case-insensitive / whitespace)
	for _, val := range []string{"off", "OFF", " off "} {
		t.Setenv("PI_DOCSEARCH", val)
		if tool := NewTool(dir); tool != nil {
			t.Fatalf("NewTool with PI_DOCSEARCH=%q = %v, want nil", val, tool)
		}
	}
}

func TestAdversarialLanguageChunking(t *testing.T) {
	// 1. C++/HIP
	cpp := `template<typename T>
class Vec {
public:
    T* data;
};

__global__ void saxpyKernel(int n, float a, float* x, float* y) {
    int i = blockIdx.x * blockDim.x + threadIdx.x;
    if (i < n) y[i] = a * x[i] + y[i];
}

template<>
void specializedFoo<int>(int x) {
    return;
}

__global__
void splitGlobalKernel(int n) {
    return;
}
`
	cppChunks := SplitChunks("kernel.hip", cpp)
	checkHasHeading := func(lang string, chunks []Chunk, want string) {
		t.Helper()
		for _, c := range chunks {
			if c.Heading == want || strings.Contains(c.Heading, want) {
				return
			}
		}
		var got []string
		for _, c := range chunks {
			got = append(got, fmt.Sprintf("%q", c.Heading))
		}
		t.Errorf("%s: missing heading %q; got headings: %v", lang, want, got)
	}
	checkHasHeading("HIP", cppChunks, "Vec")
	checkHasHeading("HIP", cppChunks, "saxpyKernel")
	checkHasHeading("HIP template<>", cppChunks, "specializedFoo")
	checkHasHeading("HIP split __global__", cppChunks, "splitGlobalKernel")

	// 2. Rust
	rs := `#[inline]
pub(crate) fn internal_helper(x: i32) -> i32 {
    x + 1
}

pub(crate) async fn async_helper() -> u32 {
    42
}

impl<T> MyStruct<T> {
    pub fn new() -> Self { todo!() }
}
`
	rsChunks := SplitChunks("lib.rs", rs)
	checkHasHeading("Rust", rsChunks, "internal_helper")
	checkHasHeading("Rust", rsChunks, "async_helper")
	checkHasHeading("Rust", rsChunks, "MyStruct")

	// 3. Python
	py := `class Client:
    async def fetch_data(self, url):
        return url

@staticmethod
async def top_async(x):
    return x
`
	pyChunks := SplitChunks("client.py", py)
	checkHasHeading("Python", pyChunks, "fetch_data")
	checkHasHeading("Python", pyChunks, "top_async")

	// 4. TypeScript
	ts := `export async function handleReq(req: Request): Promise<void> {
    console.log(req);
}

export const typedArrow: Handler = async (x: number) => {
    return x * 2;
};
`
	tsChunks := SplitChunks("handler.ts", ts)
	checkHasHeading("TS", tsChunks, "handleReq")
	checkHasHeading("TS typedArrow", tsChunks, "typedArrow")

	// 5. Go receiver methods
	goSrc := `package p

func (r *MyReceiver) Handle(ctx int) error {
	return nil
}

func (r *GenericRec[K, V]) Get(k K) V {
	var zero V
	return zero
}
`
	goChunks := SplitChunks("recv.go", goSrc)
	checkHasHeading("Go", goChunks, "MyReceiver.Handle")
	checkHasHeading("Go generic", goChunks, "GenericRec.Get")

	// 6. Shell
	sh := `my_func() {
  echo hi
}

spaced_func () {
  echo ok
}
`
	shChunks := SplitChunks("run.sh", sh)
	checkHasHeading("Shell", shChunks, "my_func")
	checkHasHeading("Shell spaced", shChunks, "spaced_func")

	// 7. Markdown fenced # comments
	md := "# Real Heading\n\n" +
		"```bash\n# fake comment heading\n## fake subheading\n```\n\n" +
		"~~~python\n# python comment\n~~~\n\n" +
		"````markdown\n```bash\n# nested fence heading\n```\n````\n"
	mdChunks := SplitChunks("README.md", md)
	for _, c := range mdChunks {
		if strings.Contains(c.Heading, "fake") || strings.Contains(c.Heading, "python comment") || strings.Contains(c.Heading, "nested fence") {
			t.Errorf("Markdown fenced # leaked as heading: %q", c.Heading)
		}
	}
}

func TestAdversarialTranslationsAndDocsCmd(t *testing.T) {
	// German -> English
	cases := []struct {
		in       string
		wantWord string
	}{
		{"speicher", "memory"},
		{"fehler", "error"},
		{"konfiguration", "configuration"},
		{"gespeichert", "memory"},
		{"gelöscht", "delete"},
		{"abgebrochen", "abort"},
		{"dateipfad", "filepath"},
		{"datei-pfad", "file"},
	}
	for _, tc := range cases {
		got := WithTranslations(tc.in)
		if !strings.Contains(strings.ToLower(got), tc.wantWord) {
			t.Errorf("WithTranslations(%q) = %q, want to contain %q", tc.in, got, tc.wantWord)
		}
	}
	if got := WithTranslations(""); got != "" {
		t.Errorf("WithTranslations(\"\") = %q, want \"\"", got)
	}

	// /docs subcommands
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("DOCSEARCH_CACHE_DIR", t.TempDir())
	t.Setenv("PI_DOCSEARCH", "")
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "doc.md"), []byte("# Alpha\n\nUniqueSearchTokenHere\n"), 0o644)

	eng := OpenProject(dir).Engine
	defer eng.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	eng.WaitReady(ctx)

	// µPi: `/docs status` is a query; the status is `/docs` alone (index.ts).
	if out := HandleDocsCommand("status", dir); !strings.Contains(out, " sections in ") {
		t.Errorf("/docs status unexpected: %q", out)
	}
	if out := HandleDocsCommand("", dir); !strings.Contains(out, "docsearch:") {
		t.Errorf("/docs (empty) unexpected: %q", out)
	}
	if out := HandleDocsCommand("auto off", dir); out != "docsearch auto-recall off" {
		t.Errorf("/docs auto off = %q", out)
	}
	if LoadSettings().AutoRecall {
		t.Error("autoRecallEnabled should be false after /docs auto off")
	}
	if out := HandleDocsCommand("auto on", dir); out != "docsearch auto-recall on" {
		t.Errorf("/docs auto on = %q", out)
	}
	if !LoadSettings().AutoRecall {
		t.Error("autoRecallEnabled should be true after /docs auto on")
	}
	if out := HandleDocsCommand("rebuild", dir); out != "docsearch: rebuilding" {
		t.Errorf("/docs rebuild = %q", out)
	}
	if out := HandleDocsCommand("UniqueSearchTokenHere", dir); !strings.Contains(out, "doc.md") {
		t.Errorf("/docs <query> unexpected: %q", out)
	}

	t.Setenv("PI_DOCSEARCH", "off")
	if out := HandleDocsCommand("status", dir); out != "docsearch: disabled (PI_DOCSEARCH=off)" {
		t.Errorf("/docs with PI_DOCSEARCH=off = %q", out)
	}
}
