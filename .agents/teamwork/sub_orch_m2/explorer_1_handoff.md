# Explorer 1 Handoff: `packages/docsearch/` Investigation & Pure-Go Design

## 1. File Inventory (`packages/docsearch/`)

| Path | Purpose |
|---|---|
| `Cargo.toml`, `Cargo.lock` | Cargo workspace (`core`, `node`), release profile (`opt-level=3`, `lto="fat"`, `codegen-units=1`, `panic="abort"`, `strip=true`). |
| `core/Cargo.toml` | `docsearch-core` crate; deps: `ignore = "0.4"`, `memmap2 = "0.9"`, `rayon = "1"`, `rustc-hash = "2"`. |
| `core/src/lib.rs` | Re-exports `Engine`, `View`, `Config`, `Hit`, `Index`, `Overlay`, `Results`, `SearchOpts`. |
| `core/src/text.rs` | Tokenizer (`raw_words`, `terms`), light English/German stemmer, camelCase/digit splitter, rule-based entity extractor (`is_entity_word`, `entities`), and German→English query translator (`with_translations`). |
| `core/src/lexicon.rs` | Static `DE_EN` table (~176 German technical terms → English equivalents). |
| `core/src/chunk.rs` | Markdown heading-aware splitter (`split`), long-section paragraph windowing, and definition-aware code splitter (`split_code`) for C/C++/HIP/CUDA, Rust, Python, JS/TS, Go, and Shell. |
| `core/src/store.rs` | Zero-copy mmap-friendly flat array containers (`Buf<T>`, `Lists` CSR, `Strs`, `StrMap` open-addressing hash table with fixed 64-bit Murmur3-finalized hash), `Writer`, `Reader`, `BadFile`. |
| `core/src/index.rs` | `Config`, `Index` (build, save, mmap load, validate, diff, `search`, `search_with`), `Overlay`, BM25 + PPR + co-occurrence + definition + heading ranking, `scan()` via `ignore::WalkBuilder`. |
| `core/src/engine.rs` | Background `Engine` & `View` lifecycle: async initial build/mmap load, `maybe_refresh` (1s throttle), `Overlay` incremental updates, quiet-time fold/rebuild, `flock` coordination, `.ok` validation stamp, 30-day cache pruning. |
| `core/src/bin/dsq.rs` | CLI tool for ad-hoc search, `--bench`, and `--eval` against TSV ground-truth datasets. |
| `node/Cargo.toml`, `node/build.rs`, `node/src/lib.rs` | Node-API (`napi-rs`) wrapper `DocIndex` with `Options`, `QueryOptions`, `SearchResult`, `Stats`, `WaitReady` async task, and 256-entry generation-keyed `Cache`. |
| `extension/index.ts` | Pi extension: loads native `.node` addon, manages `docsearch.json` config, auto-disables `project_search` for small projects (`<= 150` files), registers `project_search` tool, `before_agent_start` auto-recall, `tool_result` bash grep/rg definition hint, `docsearch-recall` renderer, and `/docs` command. |
| `install.sh`, `README.md` | Platform-specific native addon build/install script and package documentation. |
| `eval/*` | Benchmark/eval TSVs (`code-eval.tsv`, `code-names.tsv`, `docs-eval.tsv`, `ts-names.tsv`, `compare.sh`, `fff-vs-graphrag.ts`). |

---

## 2. Exact Algorithms & Data Structures

### 2.1 Tokenization, Normalization, Stemming & Lexicon (`text.rs`, `lexicon.rs`)
- **Joiners**: `_`, `-`, `.`, `/`, `:`, `+`.
- **`raw_words(text)`**: Scans maximal runs of `char::is_alphanumeric() || is_joiner(c)` and trims leading/trailing joiners (`"spec.md"` → `"spec.md"`, `"model."` → `"model"`).
- **Stopwords**: 42 English (`a`, `an`, `and`, ..., `your`) + 47 German (`aber`, `als`, `am`, ..., `zur`).
- **Light Stemmer (`stem`)**: Applied **only** to single, non-compound, non-camelCase all-alphabetic lowercased words. Strips the first matching suffix from `["ungen", "ung", "ing", "ies", "ed", "es", "en", "er", "s", "e"]` iff remaining character count `>= 4`. Identifiers are never stemmed.
- **`camel_parts(part)`**: Splits at lowercase→uppercase, alphabetic→ASCII-digit, and ASCII-digit→alphabetic boundaries (e.g. `"DFlash2"` → `["d", "flash", "2"]`).
- **`terms(text)`**:
  - Single non-compound, non-mixed word: keep if `len >= 2 || has_ascii_digit` and not stopword; stem if all-alphabetic.
  - Compound or camelCase word:
    1. Always emit full `lower`.
    2. Split on joiners; for each non-empty part `pl`: if `keep(&pl) && !is_stopword(&pl) && pl != lower`, emit `pl`.
    3. For each part, if `camel_parts(part).len() > 1`, emit each sub-part `s` where `s.len() >= 2 && !all_digits(s) && !is_stopword(&s) && s != pl`.
- **German→English Query Expansion (`with_translations`)**:
  - Builds a static map from stemmed `DE_EN` keys (~176 entries) to English synonyms.
  - For each query term `t`, strips optional `"ge"` prefix (if remainder `>= 4` bytes) and optional trailing `'t'` (handles German participles/verbs like `verteilt`, `geteilt`, `freigegeben`). Any matching English words are appended to the query string.

### 2.2 BM25 Parameters & Field Boosting (`index.rs`)
- **Parameters**:
  - `K1 = 1.2`
  - `B = 0.75` (docs / non-code)
  - `B_CODE = 0.5` (code files; long functions are not penalized as heavily as long prose pages)
  - `HEADING_BOOST = 2`
- **Term Frequency (`tf`) & Field Boosting**:
  - Body text terms: `+1` per occurrence; `len` = count of body tokens (`>= 1`).
  - Heading terms + filename stem terms (extension stripped): `+HEADING_BOOST` (`+2`) added directly to `tf` (note: heading/filename terms do not inflate `len`).
  - `sec_heading_terms` stores deduplicated term IDs from `c.heading` separately for an additional post-fusion heading boost.
- **Precomputed Posting Weights (`post_w`)**:
  - `avg_len = total_len / max(ns, 1)` (or inherited from `base` when building an `Overlay`).
  - `len_norm[s] = K1 * (1.0 - b + b * sec_len[s] / avg_len)` where `b = B_CODE` if `is_code(file)` else `B`.
  - `idf(df) = ln((nf - df + 0.5) / (df + 0.5) + 1.0)` (Lucene/BM25+ non-negative IDF), where `nf` is `base.section_count()` (for overlays, `base` stats are used).
  - `post_w` per `(s, tf)`: `idf * tf * (K1 + 1.0) / (tf + len_norm[s])`.
- **Coordination Factor at Query Time**:
  - Query terms are deduplicated (`n_query = max(qterms.len(), 1)`).
  - Raw BM25 sum `v` and matched query-term count `m` are accumulated per section:
    `lexical_score = v * (0.5 + 0.5 * (m / n_query))`.
  - Top 300 lexical hits are selected (`top_sorted`) and normalized by `top_lex = max(lexical[0].score, 1e-6)`.

### 2.3 Entity Extraction & Section–Entity Graph (`text.rs`, `index.rs`)
- **Entity Extraction (`text::entities`)**:
  1. **Inline code spans** (single backticks `` `...` ``, not fences ```` ``` ````): trimmed of whitespace and outer joiners; kept if `2..=64` chars and either `2..=3` words OR `1` word passing `is_entity_word`.
  2. **Markdown link targets** `](target#anchor)`: non-URL relative targets containing `.` → basename lowercased (e.g. `plugin.md`).
  3. **Identifier-like words** (`raw_words` per line, skipping all-caps banner/license lines where `letters >= 20 && upper * 10 > letters * 6`): `is_entity_word(w)` requires `2..=64` chars, `alpha > 0`, and at least one of:
     - `compound`: contains a joiner (`_`, `-`, `.`, `/`, `:`, `+`)
     - `camel`: has both lowercase and non-initial uppercase
     - `acronym`: all-uppercase alphabetic of length `2..=6` and not a stopword
     - `alnum`: contains an ASCII digit
  4. **File identity**: For chunk `0` (or single-chunk files), add `filename.to_lowercase()`, and `stem.to_lowercase()` if `is_entity_word(stem)`.
  5. **Aliases (`entity_keys`)**: If `raw_words(entity_name).join(" ") != entity_name`, register the space-joined form as an alias in `entity_keys`.
- **Graph Construction**:
  - Bipartite + sequential graph: nodes `0..ns` are sections, `ns..ns+ne` are entities.
  - `hub_limit = max(ns / 40, 30)`. Entities appearing in `> hub_limit` sections are excluded from graph edges (prevents hub explosion).
  - Each section `u` connects to: its non-hub entities (`ns + e`), plus its immediate predecessor (`u - 1`) and successor (`u + 1`) sections **in the same file**.
  - Each non-hub entity connects to all sections in `entity_sections[e]`.
- **Query Entities (`query_entities`)**:
  - Longest-match (1..=5 consecutive `raw_words` of lowercased query) against `entity_keys`; if a single word doesn't match, also checks its joiner-split parts.
- **Personalized PageRank (PPR) & Final Score Fusion**:
  - **Seeds**:
    - Entity seeds: `(ns + e, max(ln(ns / df), 0.1))` for each query entity `e` with `df > 0`.
    - Lexical seeds: top 5 non-delta lexical hits, total mass `lex_weight = 1.0` if no entity seeds else `0.3 * entity_mass`, distributed proportionally to their lexical scores.
  - **Forward Push PPR** (`ALPHA = 0.5`, `EPS = 1e-4`, `MAX_PUSHES = 200_000`): standard Andersen–Chung–Lang local push on CSR `graph`.
  - **Score Components summed per section `s`**:
    1. **Lexical (top 300)**: `v / top_lex` (range `(0, 1.0]`).
    2. **PPR**: `0.4 * ppr[s] / top_ppr` (range `(0, 0.4]`).
    3. **E²GraphRAG Co-occurrence**: if `graph && q_entities.len() >= 2`, for any section mentioning `c >= 2` query entities (including hubs via `entity_sections`): `+0.25 * (c - 1)`.
    4. **Go-to-Definition Boost**: if query is a single word OR a word looks like a name (contains `_` or `:`, has a digit, or is camelCase), look up `last = w.rsplit("::").next().to_lowercase()` in `defs` (code section headings' final symbol name, `len >= 3`, `[A-Za-z0-9_]+`): `+1.0`.
    5. **Heading Term Boost**: `+0.3 * sum(qidf[t] for t in sec_heading_terms[s]) / idf_total`.
- **Result Collection**:
  - Incremental `top_sorted` in chunks of `max(64, sorted)`.
  - Filters by `path_prefix` (`path.starts_with(prefix)`) and `max_per_file` (default 3).
  - Computes `coverage = min(sum(qidf of section terms in query) / idf_total, 1.0)` and `named` (count of non-hub query entities in section).

### 2.4 Definition-Aware Chunking (`chunk.rs`)
- **Size Limits**: `MAX_CHARS = 1600`, `TARGET_CHARS = 1100`.
- **Markdown / Plain Text (`split`)**:
  - Tracks fenced code blocks (` ``` ` / `~~~`) so `#` inside fences is not treated as a heading.
  - Splits at ATX headings (`1..=6` `#` followed by space/tab), maintaining a heading stack joined with `" › "`.
  - Empty sections (heading with only blank lines before next heading) are skipped (their title remains on the stack for child sections).
  - Sections `> MAX_CHARS` are split into windows at blank lines once `w_len >= TARGET_CHARS`, or forced at `w_len >= MAX_CHARS`.
- **Source Code (`split_code`)**:
  - Supported languages by extension:
    - `C` (`c`, `cc`, `cpp`, `cxx`, `h`, `hh`, `hpp`, `hip`, `cu`, `cuh`): `class`/`struct` with `{` within 3 lines, or `type name(...)` with `{` before `;` within 6 lines (excludes control keywords & stopwords, requires qualifier/type before name unless `::` present), `indent <= 4`.
    - `Rust` (`rs`): `pub fn `, `fn `, `pub(crate) fn `, `pub struct `, `struct `, `pub enum `, `enum `, `impl `, `pub trait `, `trait ` at `indent <= 4` (plus falls through to C-like check if needed).
    - `Python` (`py`): `def `, `async def `, `class ` at `indent <= 4`.
    - `Script` (`ts`, `tsx`, `mts`, `cts`, `js`, `jsx`, `mjs`, `cjs`): strips `export`, `default`, `declare`, `abstract`, `async`; matches `function* `, `function `, `class `, `interface `, `enum `, `const enum `, `namespace `, `type T =|<`, `const|let|var f = function|async|(...)=>|x=>`; plus indented (`1..=4` spaces) class methods (`public/private/protected/static/override/readonly/get/set` stripped, `name(` or `name<` with `{` before `;` or `=>` within 6 lines, excluding control keywords).
    - `Go` (`go`): `indent == 0` only; `func Name(`, `func (r *T) Method(` (formatted as `T.Method`), `type Name ...`.
    - `Shell` (`sh`, `bash`, `zsh`): `indent <= 4`; `function name` or `name() {` / `name() (`.
  - **Attached Comments/Attributes**: Walks upward from definition line `i` to include contiguous `//`, `///`, `*`, `/*`, `#` (excluding `#include`), `@`, and `template` lines.
  - **Nested Header Merging**: If an outer block header (`class Box:`, `impl Pool {`) has no body lines before the next definition, its name merges as `"Outer › Inner"`.

### 2.5 Incremental Updates, Persistence & Concurrency (`store.rs`, `engine.rs`)
- **Flat File Format (`DSINDEX\x02`, `FORMAT = 3`)**: All tables serialized as 8-byte-aligned `[u64 byte_len][bytes][pad]` blocks; mmap'd read-only for instant startup and cross-process page sharing.
- **Validation & `.ok` Stamp**: First process to open a new `(dev, ino, size)` validates all offsets, bounds, power-of-two map slots, and UTF-8 strings (~12 ms for 110 MB), then atomically writes `<hash>.ok`. Subsequent processes matching the stamp skip full-page validation (`~0.3 ms`).
- **Signature & Diff**: `Signature` = `[(path, mtime_nanos, size)]` from gitignore-respecting walk (`max_file_bytes = 4 MiB`, `max_files = 50_000`, skips binary files with `\0` in first 8192 bytes).
- **Overlay Mechanism**:
  - `maybe_refresh()` is throttled to `CHECK_EVERY = 1s` (or `5 * build_ms` if in-memory only).
  - `View` holds `base: Arc<Index>` + `Option<Overlay>` (`index` built from changed/added files using `base`'s `nf`, `df`, and `avg_len`; `excluded: Vec<bool>` masking replaced/deleted sections in `base`).
  - **Rebuild / Fold Triggers**:
    - Immediate full rebuild if `base.build_ms < 100ms` (small indexes stay in-memory only, never saved to disk), or `!file.exists()`, or `changed + gone > OVERLAY_MAX_FILES (200)`.
    - Background fold into saved `.idx` (under `.lock` flock) when quiet for `QUIET_BEFORE_REBUILD = 30s` AND (`overlay_files >= FOLD_MIN_FILES (20)` OR `overlay_age >= FOLD_AFTER (600s)`).
  - **Cache**: `~/.cache/docsearch/index/<hash>.idx` (`DOCSEARCH_CACHE_DIR` or `XDG_CACHE_HOME`), pruned after 30 days unused.

### 2.6 Auto-Disable & Extension Behavior (`extension/index.ts`)
- **Disabled via env**: `PI_DOCSEARCH=off` disables everything.
- **Root Resolution**: Git root above `cwd` (walking up for `.git`), else `cwd`; returns `undefined` if root is `$HOME` or `/`.
- **Small Project Auto-Disable (`SMALL_PROJECT_FILES = 150`)**:
  - Counts files up to `151`: uses `git ls-files --cached --others --exclude-standard -z` if `.git` exists, else walks up to depth 6 skipping hidden dirs, `node_modules`, `target`.
  - If `root` exists, `config.roots.length === 0`, and `countFiles(root, 150) <= 150`, `project_search` is removed from active tools (saving ~400 schema/guideline tokens per request). It is restored if switching to a larger project.
- **Auto-Recall (`before_agent_start`)**:
  - Runs when `autoRecall` is `true`, `messageType === "user"`, prompt doesn't start with `/`.
  - Searches up to `autoRecallHits * 2` (default 6) with `waitMs = 300`, `maxChars = floor(2400 / 3) = 800`.
  - Filters to non-code files (`!isCode(path)`) where `(named > 0 && coverage >= 0.3)` OR `(queryTerms >= 3 && coverage >= 0.85)`, takes top `3`.
  - Injects `<doc_search_auto>` message with `customType: "docsearch-recall"`.
- **Bash Definition Hint (`tool_result`)**:
  - When `bash` runs `rg`/`grep`/`egrep`/`git grep` for a single identifier (in non-small projects), appends up to 2 matching code definitions (`path:start-end · heading`) if not already in the command output.

### 2.7 `project_search` Tool Spec (`extension/index.ts`)
- **Name**: `project_search` (Label: `Project search`)
- **Description**: `"Search this project's code and documentation and the configured doc folders. Returns the best-matching sections with path and line range: a doc section under its headings, or a whole function, class or struct under its name. A query naming a function or type returns its definition first. Ranks by terms and by the names the query mentions (identifiers, file names, flags, products). ~1 ms."`
- **PromptSnippet**: `"Find definitions (functions, classes) and doc sections by name or keywords; returns path:lines"`
- **PromptGuidelines**: 4 guidelines instructing the model to use `project_search` before `grep`/`rg`/`find`/`read` for definitions/docs, to put identifiers/names in queries, to use `rg` for exact-text/call-sites, and to batch parallel calls.
- **Parameters**:
  - `query` (string, required)
  - `k` (integer, optional, `1..=20`, default `config.results = 6`)
  - `path` (string, optional, path prefix relative to project or absolute)
- **Output Formatting**:
  - Header: `"{N} sections ({ms} ms[; names: e1, e2])"` or `'No sections match "{query}". Try other words or names.'`
  - Top `resultChars` (`[1500, 1000, 600]`) hits formatted as:
    `── {relPath}:{lineStart}-{lineEnd}[ · {heading}]\n{text}[\n[cut — read {relPath} lines {lineStart}-{lineEnd} for the rest]]`
  - Remaining hits formatted as pointers:
    `More matches (read the lines to see them):\n   {relPath}:{lineStart}-{lineEnd}[ · {heading}]`
  - Also returns `structuredContent.hits` (`path`, `lineStart`, `lineEnd`, `heading`, `text`, `truncated`) and `details` (`count`, `micros`, `entities`).

### 2.8 `/docs` Slash Command (`extension/index.ts`)
- `/docs` (no args): Status — `"docsearch: {files} files, {sections} sections, {entities} names, built in {buildMs} ms\nroots: ...\nauto-recall: on|off · settings: {CONFIG_PATH}"` (or `"building"`).
- `/docs auto on` | `/docs auto off`: Updates `autoRecall` in `~/.pi/agent/docsearch.json` and notifies.
- `/docs rebuild`: Restarts index build and notifies `"docsearch: rebuilding"`.
- `/docs <query>`: Searches with `k=8, maxChars=200`, notifies `"{N} sections in {ms} ms\n{path}:{start}-{end}  {heading}"`.

---

## 3. Recommended Pure-Go Design (`CGO_ENABLED=0`, Zero External Binaries)

### 3.1 Package Layout: `internal/docsearch/`
Keep the core engine completely independent of `internal/codingagent` so it can be unit-tested, benchmarked, and reused cleanly:
1. `internal/docsearch/text.go`:
   - Port `raw_words`, `terms`, `stem`, `camel_parts`, `is_entity_word`, `entities`, and `with_translations` + `lexicon.go` (`deEnLexicon` map).
   - Use `unicode` package (`unicode.IsLetter`, `unicode.IsDigit`, `unicode.IsUpper`, `unicode.IsLower`) and `strings.Builder` to minimize allocations.
2. `internal/docsearch/chunk.go`:
   - `Chunk{Heading string, LineStart, LineEnd uint32, Text string}` and `Lang` enum (`LangC`, `LangRust`, `LangPython`, `LangScript`, `LangGo`, `LangShell`).
   - Exact port of `Split(text string, markdown bool) []Chunk` and `SplitCode(text string, lang Lang) []Chunk`.
3. `internal/docsearch/store.go`:
   - Pure-Go binary format (`DSINDEX\x02`, `FORMAT = 3`) using `encoding/binary` (LittleEndian) + `golang.org/x/sys/unix` (`Mmap`/`Munmap` on Unix) and `syscall`/`windows` build-tagged fallback (or `os.ReadFile` fallback on Windows if mmap is omitted, though `golang.org/x/sys/windows` supports `CreateFileMapping`/`MapViewOfFile` with zero CGO).
   - **Important Go safety note**: Avoid `unsafe.Slice` over unaligned mmap offsets—the Rust writer already pads every array to 8-byte boundaries! Still, in pure Go, we can either use `unsafe.Slice((*uint32)(unsafe.Pointer(&mmap[off])), n)` safely (since `Reader` verifies `off % align == 0` and bounds) OR `binary.LittleEndian` accessors if we want zero `unsafe`. Using `unsafe.Slice` on 8-byte-aligned validated offsets gives true zero-copy page-cache sharing identical to Rust, with `runtime.SetFinalizer` (or explicit `Close()`) to `Munmap`.
   - Port `StrMap` with the exact 64-bit Murmur3-finalized `hash([]byte)` so index files remain deterministic.
4. `internal/docsearch/walk.go`:
   - **Zero external binaries**: Instead of shelling out to `git ls-files`, reuse PiG's existing `internal/ignorerules` + `filepath.WalkDir` (with `runtime.NumCPU()` worker goroutines via `errgroup` or bounded worker pool for `parseFile`).
   - Note: `CountFiles(root string, limit int) int` for the `<= 150` check should also use `filepath.WalkDir` + `internal/ignorerules` (skipping `.git`, `node_modules`, `target`) instead of `exec.Command("git", ...)` so there are **zero** external binary invocations.
5. `internal/docsearch/index.go`:
   - `Index`, `Overlay`, `Config`, `SearchOpts`, `Hit`, `Results`.
   - Use a `sync.Pool` of `scratch` buffers instead of Rust's `thread_local!` `RefCell<Scratch>` (idiomatic Go, goroutine-safe, zero contention).
   - Exact BM25 (`K1=1.2`, `B=0.75`, `B_CODE=0.5`, `HEADING_BOOST=2`), PPR forward-push (`ALPHA=0.5`, `EPS=1e-4`, `MAX_PUSHES=200_000`), E²GraphRAG co-occurrence (`0.25*(c-1)`), definition boost (`+1.0`), and heading boost (`+0.3`).
6. `internal/docsearch/engine.go`:
   - Background `Engine` with `sync.Mutex` + `sync/atomic.Bool` + channel/condvar `ready` broadcast (`close(readyCh)` pattern for first-build notification, which works cleanly with `context.Context` and `select` timeouts).
   - Cross-process lock using `github.com/gofrs/flock` (already in `go.mod`!).
   - Generation-keyed 256-entry LRU/map result cache in `Engine.Search`.

### 3.2 Tool & Agent Integration: `internal/codingagent/tools/docsearch.go`
1. **Tool Registration**:
   - Implement `agent.AgentTool` + `agent.OutputSchemaProvider` (`ProjectSearchTool`), wrapped via `BuiltinToolDefinition` in `internal/codingagent/tools/tool_definition.go` with `PromptSnippet` and `PromptGuidelines` matching `extension/index.ts`.
   - Parameters struct with JSON tags (`query`, `k`, `path`) + JSON schema matching `Type.Object(...)`.
   - `OutputSchema()` returning the `hits` array schema.
   - `Execute(ctx, id, args, onUpdate)` calling `engine.Search(ctx, ...)` (respecting `ctx.Done()` while waiting up to 5s for initial build), formatting the `content` text (`── path:start-end · heading`, `resultChars = [1500, 1000, 600]`, pointers for the rest) and `structuredContent`.
2. **Lifecycle, Auto-Disable & Hooks**:
   - Start `docsearch.Engine` at session start (respecting `PI_DOCSEARCH=off` and `~/.pi/agent/docsearch.json`).
   - Run `CountFiles(root, 150)`: if `<= 150` and `len(config.Roots) == 0`, omit `project_search` from the active tool list.
   - Wire `before_agent_start` auto-recall (non-code hits, `named > 0 && coverage >= 0.3` or `queryTerms >= 3 && coverage >= 0.85`, max 3 hits / 2400 chars).
   - Wire `tool_result` bash definition hint (`SearchedName(cmd)` pure-Go parser for `rg`/`grep`/`egrep`/`git grep`).
   - Register `/docs` slash command (`status`, `rebuild`, `auto on|off`, `<query>`) in the command registry.
