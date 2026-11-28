<h1 align="center">µPi</h1>

<p align="center">
  <b>She said it's the perfect size. This time it's true.</b><br>
  The Pi coding agent, tuned so the model wastes less time. Now one static Go binary, built on PiG.<br>
  Native executable · sees its compiler errors after every edit · never sleeps on a build · cheaper context
</p>

<p align="center">
  <b>12–18× faster start</b> · <b>4–7× less CPU per turn</b> · <b>30–50% of Pi's memory</b> · fewer turns<br>
  <sub>Harness cost against Pi 1.3.0 on Node, same scripted model. <a href="#benchmarks">How it was measured</a></sub>
</p>

---

Most of a coding agent's time is the model, not the program around it. µPi works on the program anyway, because it
decides **how many turns the model needs**: what it learns after an edit, how it waits for a build, how fast it finds
code and how much context it sends again. And the program itself should cost next to nothing: µPi 0.2 is
**[PiG](https://github.com/MichaelKinsy/PiG)** (Pi in Go) with µPi's features built in, one executable with no
runtime to install. Every claim below has a measurement, or says it has none.

## What you get

| | Stock Pi | µPi |
|---|---|---|
| ⚡ **The program itself** | Node.js, `npm install`, ~280 ms to start | **One static executable** (`upi`), no runtime: **~15 ms** to start, ~3 ms of CPU per turn |
| 🛠️ **After an edit** | Finds out at the next build that it broke something | Gets the compiler's **new** errors before its next step, from 10 languages' checkers in the background |
| ⏳ **Long builds and tests** | `sleep 295; tail build.log`: 97 real sleeps waited 3.2 h | The command becomes a **background job that wakes the agent** when it ends. A `wait` tool, a job hub (`alt+j`), timeouts move builds to the background instead of killing them |
| ✏️ **Editing** | Refuses edits of files it saw only via `cat`/`grep`: 21% of real edits | Exact edits apply directly. A missed edit shows **the closest lines** with line numbers. `sed -i` is checked like an edit |
| 🔍 **Finding code** | `grep` and hope | `project_search`: the definition first in **83%** of lookups (grep: 55%), in **0.03–0.05 ms**. `grep`/`find` in the process with a file cache |
| 🧠 **Long sessions** | Stops 14–95 s for every compaction summary | Summary made **ahead, in the background, from the prompt cache**. Your task survives compaction word for word. Old tool results shortened: **60% smaller requests** on a resumed long session |
| 💬 **Turns** | Starts by listing the folder; one call per response | Project listing in the prompt, edit and test in one response, smaller system prompt |
| 🌐 **Remote work** | Extensions that replace the tools with plain ones | `/remote host:folder`: read, edit, write and bash over ssh with every feature above |
| 📏 **Cut-off answers** | An answer cut off at the output limit ends the run | The model is told and goes on, thinking one level less |

Everything is on by default. Pi's skills, settings, sessions and TypeScript extensions work as they are (extensions
through a Node.js sidecar, see [Extensions](#extensions)); µPi keeps its own in `~/.micropi`.

## Benchmarks

### Harness cost (µPi 0.2 on PiG)

[Buffon](https://michaelkinsy.github.io/buffon/) runs every harness against the same scripted model, so a number is the
cost of the harness itself: start-up, CPU, memory. µPi 0.2 against Pi 1.3.0 on Node 24, no extensions, medians:

| Measured | Pi 1.3.0 (Node) | µPi 0.2 | |
|---|---:|---:|---:|
| `-p`, one prompt, one reply: wall time | 284 ms | **15 ms** | 18× |
| RPC: ready to take a prompt | 245 ms | **21 ms** | 12× |
| RPC: CPU per turn (a tool call and a reply) | 11.3 ms | **3.0 ms** | 3.7× |
| `-p` with 8 tool-call rounds: wall time | 333 ms | **31 ms** | 11× |
| `-p` with 8 tool-call rounds: CPU per turn | 9.7 ms | **1.3 ms** | 7× |
| TUI: first frame | 268 ms | **18 ms** | 15× |
| TUI: a key press to the reply on screen | 54 ms | **4.5 ms** | 12× |
| TUI: first line of a streamed answer on screen | 7–24 ms | **0.4–0.6 ms** | 17–49× |
| TUI: CPU per 1,000 streamed tokens | 119–397 ms | **44–72 ms** | 2.2–5.5× |
| `-p`: CPU per 1,000 streamed tokens | 8.5–52 ms | **1.7–2.8 ms** | 4.5–19× |
| One `write` call of 200 KB, streamed 16 characters at a time | **29 s** | **131 ms** | 220× |
| … its peak memory | 581 MiB | **61 MiB** | 9.6× |
| Resume a 100-turn session: ready | 241 ms | **44 ms** | 5.5× |
| … CPU of the first turn on it | 50 ms | **6 ms** | 8× |
| … request size of that turn | 590 KB | **237 KB** | 2.5× |
| Peak memory, RPC / TUI | 140 / 123 MiB | **58 / 53 MiB** | 2.4× |

<sub>Buffon at commit `1e37f70`, 4–6 rounds per cell plus a warm-up, interleaved; on a shared 4-CPU cloud container
(Xeon 2.1 GHz, cgroup v1), so Buffon labels these numbers PROVISIONAL: the ratios are solid, the absolute values are this
host's. The request size is smaller because old tool results are shortened (`toolResultPruning`), not because the session
was cut: the same 100 turns are sent. Not in the table: with ten TypeScript extensions loaded, see [Extensions](#extensions).</sub>

### Solving tasks (µPi 0.1, the TypeScript build)

[harnessbench](bench/harness): 14 small projects (Python, C, Rust, TypeScript), each with hidden tests that decide
whether the task is solved. Both harnesses run with no extensions, skills or instruction files, the same provider config
and their own agent folder. Pi 1.4.0 from npm (Node 24) against **µPi 0.1** (the Pi-Bolt build this one replaces), 10
runs of each task, 5 November 2026:

| Model | | Solved | Time, sum | Turns, median | Cost |
|---|---|---:|---:|---:|---:|
| glm-5.3-flash | Pi 1.4.0 | 140/140 | 2,215 s | 6 | $0.23 |
| | **µPi 0.1** | 138/140 | **1,883 s** | **5** | **$0.17** |
| deepseek-v4.1-flash | Pi 1.4.0 | 137/140 | 1,745 s | 5 | $0.30 |
| | **µPi 0.1** | 137/140 | **1,466 s** | **4** | **$0.25** |

µPi 0.2 has the same features and the same prompt, so it takes the same turns; it has not been run through harnessbench
yet. The time here is almost all the model's: the harness's own cost (above) is a few milliseconds per turn either way.

<details>
<summary>Statistics and caveats</summary>

Time ratio 0.846 (95% CI 0.709–0.934, bootstrap over task/model pairs and runs); geometric mean of the 28 per-pair
ratios 0.781 (0.701–0.867); 26 of the 28 pairs faster by median. With glm the same task takes 4 to 44 s, by how long
the model thinks: which one or two glm pairs come out slower changes from run to run. Failures: a rounding case for both
harnesses, and twice glm printing the program name with the arguments in a C task. Provider latency varies by run:
compare within a run, not across.

`--suite quick` compares two builds in about 3 minutes: 9 tasks (Python, C, Rust, TypeScript, search, a slow test suite)
twice each, with the paired ratios of time, cost and turns and their bootstrap intervals. See [bench/harness](bench/harness).

</details>

## Install

Download the executable for your platform from the [latest release](https://github.com/Code000-211/microPi/releases/latest)
and put it on your `PATH`. Linux x86-64 (for ARM64 take `upi-linux-arm64`):

```bash
mkdir -p ~/.local/bin && curl -fsSL -o ~/.local/bin/upi https://github.com/Code000-211/microPi/releases/latest/download/upi-linux-amd64 && chmod +x ~/.local/bin/upi
```

macOS on Apple silicon (Intel: `upi-darwin-amd64`):

```bash
mkdir -p ~/.local/bin && curl -fsSL -o ~/.local/bin/upi https://github.com/Code000-211/microPi/releases/latest/download/upi-darwin-arm64 && chmod +x ~/.local/bin/upi
```

Windows x64, in PowerShell:

```powershell
New-Item -ItemType Directory -Force "$env:USERPROFILE\.micropi" | Out-Null; Invoke-WebRequest https://github.com/Code000-211/microPi/releases/latest/download/upi-windows-amd64.exe -OutFile "$env:USERPROFILE\.micropi\upi.exe"
```

and add `%USERPROFILE%\.micropi` to your user `PATH`. Every release has a `SHA256SUMS` beside the executables
(`sha256sum -c SHA256SUMS --ignore-missing` in the download folder). Then `upi --version`, and `upi update` updates to
the next release. The one-line installer of µPi 0.1 (`install.sh`, `install.ps1`) installs the 0.1 builds only.

## Build from source

Needs Go 1.27 (`go.mod` names the exact toolchain; `GOTOOLCHAIN=auto` fetches it) and nothing else:

```bash
git clone https://github.com/Code000-211/microPi.git && cd microPi
```

```bash
CGO_ENABLED=0 go build -trimpath -o upi ./cmd/upi && ./upi --version
```

The release executables are built the same way for each platform (`GOOS`/`GOARCH`), see
[.github/workflows/release.yml](.github/workflows/release.yml).

## Pi, PiG and µPi

[PiG](https://github.com/MichaelKinsy/PiG) is Pi rewritten in Go, checked against Pi's own behaviour by its parity
tests. µPi 0.2 is PiG with µPi's features built into the harness: no extension, no second process, nothing to install.
It uses Pi's file formats for settings, skills, sessions, models and extensions, and keeps them in `~/.micropi/agent`
(the `upi` command), so it installs next to Pi or PiG without touching theirs. Copy `~/.pi/agent` over to take your Pi
setup along, or set `MICROPI_USE_PI_DIRS=1` to work in Pi's folders.

µPi 0.1 was a fork of [Pi-Bolt](https://github.com/opensec-git/Pi-Bolt), Pi's TypeScript compiled ahead of time; it
stays on the `pi-bolt` branch. 0.2 replaces it on the default branch, `PiG`.

### Extensions

- **TypeScript extensions** written for Pi load as they are. PiG runs them in a Node.js process beside it (Node 22.13
  or later on the `PATH`). That costs: with ten trivial extensions, a start takes about Pi's time (511 ms against
  Pi's 477 ms), and each turn about 11 ms more than in Pi, where an extension is a function call in the same process.
  The first start on a new home folder unpacks the Node side once next to the `upi` executable.
- **Go extensions** (`upi extension init`, PiG's Go SDK) run without Node, and can be compiled into one executable
  with them (a Piglet, `upi piglet`). The most used extensions are being ported to Go for that reason.
- µPi's own features are not extensions: they cost nothing when they are not used.

## How it works

Each feature in detail, with its measurements and limits.

<details>
<summary><b>Finding code</b>: grep/find in the process, project_search</summary>

### Finding code

`grep` and `find` run in µPi's own process instead of starting `rg` and `fd`: they keep the project's file tree and up to
64 MB of file contents in memory between calls. They are not on by default (`--tools +grep,+find` or the `defaultTools`
setting): their schemas cost every request about 400 tokens, and models search with `rg` in bash anyway.

| Measured, warm, median of 11 | 2,500 files (17 MB) | | 27,000 files (repository) | |
|---|---:|---:|---:|---:|
| | µPi | `rg` | µPi | `rg` |
| `grep` a function name | **8 ms** | 16 ms | 242 ms | **99 ms** |
| `grep` a rare word, ignoring case | **14 ms** | 18 ms | 525 ms | **116 ms** |
| `grep` a common word, ignoring case (stops at 100 matches) | **2 ms** | 18 ms | **6 ms** | 116 ms |
| `grep` a regex | 23 ms | **19 ms** | 328 ms | **103 ms** |
| `find` a glob | | | **1.4 ms** | 16 ms (`fd`) |

<sub>This repository and its `internal/codingagent` folder, Linux x86-64, 4 CPUs; `rg` with the same match limit.
While the project's text fits the 64 MB cache, µPi needs no process and reads no file again. A tree with more text,
like this whole repository, is read again on every search, and `rg` is faster there.</sub>

**Never stale.** After every `bash` command (the agent's or a `!` command) and after every tool that is not built in,
the next search checks the tree and the files' times at once, so files a command created or changed (`sed -i`, a code
generator) are found right away. Within 2 s of a check, the tree is reused without one.

- `grep` decodes UTF-16 files with a byte order mark, as ripgrep does; a glob with a `/` is anchored at the search path.
- A case-insensitive pattern searches for its literal part first, a pattern without regex characters as plain bytes.
- A `find` glob that matches nothing returns the closest paths by fuzzy match instead.

#### project_search

docsearch searches the project's code and documentation, in the process. The agent gets the tool `project_search`,
matching doc sections are added to a typed prompt, and `/docs` searches by hand. No model runs at search time: BM25 over
sections plus a graph of sections and the names they mention. Code is split at its definitions (C/C++/HIP, Rust, Python,
TypeScript/JavaScript, Go, shell), so a function's name finds its definition first.

| Measured | `grep` | `project_search` |
|---|---:|---:|
| Doc question: the answer among the first 3 results | 40% | **83%** |
| Function by name: the definition first | 55% | **83%** |
| Function by name: the definition among the first 3 | 77% | **97%** |
| Time per search, this repository (27,000 files) | | **0.03–0.05 ms** |

<sub>The hit rates were measured with µPi 0.1's index (Rust) on 60 doc questions and 60 function names with known
answers; the Go index is a port of it with the same ranking. grep was given each question's rarest word, which favours
it. The time is µPi 0.2's, warm.</sub>

<details>
<summary>Index, memory and updates</summary>

- The index is a mapped file in `~/.cache/docsearch`: the first process builds it, every later one maps it. A cache
  file that does not validate is rebuilt, not read.
- A changed file is searchable within a second without rewriting the index, scored on the same scale as the rest
  (an edited file holding one word of the query does not rank like a full match). The index is rebuilt once nothing
  has changed for 30 s and 20 files changed or 10 minutes passed.
- In a project of 150 files or fewer (all listed in the prompt) `project_search` is left out of the tools.
- `grep` stays the tool for exact text across all files, such as every call site.

</details>

</details>

<details>
<summary><b>Editing</b>: compiler diagnostics, fewer refused edits, edit matching, sed -i</summary>

### Editing

#### Compiler diagnostics after every edit

After `edit` or `write`, the compiler's diagnostics **the edit introduced** come as a message before the model's next
request. Errors that were there before are left out: they are collected in the background when the file is read. The
model sees its mistake in its next step instead of at the next build.

##### Background checks

Files are checked after the edit, in the background: `edit` and `write` (and a bash command that changed a file) return
at once, and only errors that are new against the state before come as a message before the model's next request. When
the model would end its run while a check of its edits still runs, µPi waits for it (`PI_CHECKS_WAIT`, default 30 s).
Edits in quick succession make one check, a unit runs one check at a time, and a check an edit overtook runs again
instead of reporting. A checker runs in its own process group and is stopped with everything it started.

| Language | Tool (the fastest found, each only when installed) | Checks |
|---|---|---|
| Go | `go build` and `go vet` at once; `go test -c` for the test files' errors | types, vet |
| C, C++, HIP | clang, with the flags of `compile_commands.json` | types, syntax |
| Rust | `cargo check` on the file's package, in `target/pi-check` | types |
| Python | ty; without it, Python's own parser | types, undefined names / syntax |
| TypeScript | `tsc` (TypeScript 7's native one before the project's) on the `tsconfig.json` project | types |
| JavaScript | `node --check` | syntax |
| Shell | `bash -n`, then shuck's errors | syntax, errors |
| Luau | luau-analyze | syntax; types under `--!strict` |
| JSON, TOML, YAML | parsers in the process | syntax |

An error is matched against the state before by file, kind and message, not by line: an edit above a pre-existing
error does not make it new, also where the message names its line (TOML, YAML, `go vet`). Every error of a file is
listed by the compiler (no error limit), so fixing one does not uncover an old one as new.

Linters run where the project configures them, and their findings wait: they are collected against the state before
the agent first changed the unit and reported once, when the model would end its run.

| Language | Linter (the fastest found) | Runs when the project has |
|---|---|---|
| Rust | clippy, in place of `cargo check` | `clippy.toml`, clippy lints in `Cargo.toml`, or `cargo clippy` in CI |
| Python | ruff | `ruff.toml` or `[tool.ruff]` |
| JS/TS | Biome; else oxlint | `biome.json`; an oxlint or ESLint configuration |
| Shell | shuck's warnings | `.shellcheckrc` or `shuck.toml` |
| Markdown | rumdl | a markdownlint or rumdl configuration |
| YAML | ryl (a Rust yamllint) | a yamllint or ryl configuration |
| Luau | selene | `selene.toml` |
| C/C++/HIP | clangd-tidy | `.clang-tidy` and `compile_commands.json` |

**HIP for AMD RDNA4 (gfx12)**: what the compiler accepts but the GPU does not. When a HIP unit is built for a `gfx12`
target (an `--offload-arch` or `-mcpu` in `compile_commands.json`, else `HIP_OFFLOAD_ARCH`) and
[radisa](https://github.com/Code000-211/radiance-rdna4-isa-suite) is found (`PI_RADISA`, or `tools/rdna4-isa-suite`
above the file), the unit is compiled for gfx1201 and its machine code checked against AMD's machine-readable RDNA4 ISA:
registers used before the `s_wait_*` their load needs, LDS stores outstanding at a barrier, WMMA and
`v_cmpx`/`v_permlane` hazards, VOPD pairs the hardware rejects, use after VGPR deallocation; spills, scratch arrays and
unclobbered inline-asm writes as lint findings. Only kernels whose optimised code changed are checked again.
`PI_RADISA_TIMEOUT` (seconds) raises the 300 s limit, `PI_RADISA_JOBS` sets fewer parallel compiles, and
`RADISA_COMPILE_COMMANDS` names a `compile_commands.json` outside the source tree.

On Moebius (443 Rust files), a type error the model wrote was reported 3 s after the edit, before the run ended, and
fixed; rust-analyzer, which Pi used before, reported nothing within 84 s and took 3.2 GB (measured with µPi 0.1).

#### Fewer refused edits

Pi refuses to edit a file the agent has not read, so it never overwrites what it did not see. That guard also turned
every edit of a file the agent had seen through `cat` or `grep` into a refusal, a read and a second edit: in real
sessions (23 Oct – 1 Nov 2026) that was **21% of all edits** (49 of 232), 3.7 s each (median).

- `edit` applies to an unread file when **every `oldText` matches it exactly once**: the file then counts as read, its
  diagnostics before the edit are taken, and the result says so. Inexact edits still refuse.
- `write` over an existing file that was not read is refused with the file's content (up to 30,000 characters) in the
  error, and the file then counts as read: the model sees what it would replace and writes again without a `read`.
- A bash command that prints files whole with `cat` (output not cut) counts as reading them.
- The same holds for a file that changed on disk since it was read: an exact, unique match applies without a re-read.
- Files of 1 MB or more read in pieces can still be edited; CRLF line endings and a BOM are kept.

#### Edit matching

- When `oldText` matches neither exactly nor by the fuzzy match, lines are compared ignoring leading and trailing
  whitespace (Codex's `seek_sequence`), and `newText` is re-indented to the file's indentation. More than one match is
  still refused.
- When `oldText` matches nowhere, the error shows **the closest text in the file** with line numbers (up to 20 lines)
  and the first line that differs, so the model fixes the edit without reading the file again.
- After an edit, the result shows the changed lines with 3 lines of context.
- When the agent reads a file, the `AGENTS.override.md`, `AGENTS.md` or `CLAUDE.md` of each folder between the project
  root and that file is added to the result, once per session (as Claude Code does).

#### No edits behind the edit tool's back

Models often change files through bash instead: Claude Opus changed files with bash 155 times and with `edit` 25 times
in real sessions. While the `edit` tool is there, `sed -i` and `perl -i` on one file, and text a command spells out
(`echo`, `printf`, a heredoc through `cat` or `tee`) written into a file that exists, run and then get what `edit` gets:
the changed lines, the file counted as read, and the background checks. The same replacement over many files, new files,
appends, files under `/tmp` and a program's output redirected into a file are left alone.

</details>

<details>
<summary><b>Running commands</b>: background jobs, wait, job hub, bash output</summary>

### Running commands

#### Background jobs instead of sleep

In real sessions, models waited for builds and tests with commands like `sleep 295; tail build.log`: 97 such calls waited
3.2 hours, either past the moment the job had ended or not long enough, which cost another turn. µPi gives the agent a
way to wait that ends exactly when the job does (as [oh-my-pi](https://github.com/can1357/oh-my-pi) does).

- **Automatic background.** A command still running after 120 s goes on in the background as a job (`bg1`, `bg2`, …);
  `background: true` starts it there right away. A command still running at its `timeout` goes there too instead of
  being ended. Only a command started with `background: true` is ended by its timeout.
- **The agent is woken.** When a job ends, the agent gets its exit code and the last 2 KB of its output: before its next
  request while it works, or as a new turn when it is idle. Jobs that end within 1.5 s come in one report.
- **`wait` tool.** Blocks until one of the session's jobs ends and returns its report; with nothing running it returns
  at once. It blocks up to 2 min, then 5 and 10, and from the second empty one on tells the model to do other work. A
  message from the user ends a wait.
- **No long fixed sleeps.** A command that sleeps longer than a command runs before it goes to the background (120 s)
  is not run; the error tells the model to call `wait`, or to loop on the condition itself.
- Jobs end with µPi and never keep `upi -p` running; `upi -p` waits for running jobs and gives their reports a turn first.

#### Job hub

`alt+j` or `/jobs` opens every background job of the session:

```text
Jobs  1 running · 1 ended
› ● bg2   running        2m 5s  cargo build --release
  ✗ bg1   exit 101         10s  cargo test
command cargo build --release
output  /tmp/pi-bash-….log
   Compiling …                      ← the end of its output, live
↑↓ select · enter output · x stop · escape close
```

Enter shows a job's whole output to scroll through, `x` stops the selected job. While jobs run, the status line shows
`● 2 jobs running (alt+j)`.

#### bash output and speed

- **Both ends of long output.** Output over 2000 lines or 32 KB keeps its first 40 lines and its end, so the first
  compiler error and the summary both reach the model. The full output is saved to a file.
- **Clean output.** Escape sequences, control characters and progress-bar redraws are removed, and commands run with
  `NO_COLOR=1` unless `NO_COLOR` or `FORCE_COLOR` is set.
- **Early start.** `read`, `ls`, `grep`, `find`, `project_search` and bash commands that only read (`git
  status/log/diff/show`, `cat`, `rg`, `grep`, `find` without `-exec`/`-delete`, …, without redirections or substitutions)
  start as soon as their arguments have streamed, before the model's message ends. The result is used only when the
  final arguments are the same. Quotes are read as bash reads them, so `echo "a\" "; touch x` is not taken as read-only.
- **In order where it matters.** The calls of one response run in order when one of them is `bash`, `edit` or `write`.

</details>

<details>
<summary><b>Working on another machine</b>: /remote over ssh</summary>

### Working on another machine

`/remote dev:~/Projects/Radiance` (or `upi --remote dev:~/Projects/Radiance`) moves `read`, `edit`, `write` and `bash`
to that folder on another machine over ssh; `/remote off` moves them back, `/remote` says where they work. The session
remembers it, and the status line shows `⇄ dev:/home/dev/Projects/Radiance`.

- **The same checks there.** Read before edit, exact-unique edits of unread files, the closest-text hint, background
  jobs, `wait` and the sleep guard all work on the remote files and commands. Compiler diagnostics after edits do not
  (the compilers run on this machine).
- **One login.** ssh keeps one master connection per host (ControlMaster). A key's passphrase or a password is asked
  for in µPi, not shown and not kept. In `upi -p` nothing is asked: a key without passphrase or an ssh agent is needed.
- **Commands end there too.** A command runs in its own process group on the host, and ends when the connection's input
  closes: an abort, a timeout, a stopped job or µPi's exit leaves nothing running there.
- **The prompt cache holds.** The tools keep their declarations; the system prompt says where they work. `grep`, `find`
  and `ls` search this machine, so they are off meanwhile.

Not measured on a real host yet: checked with an ssh stand-in that runs the remote side on this machine.

</details>

<details>
<summary><b>Context and compaction</b>: compaction cap, pruning, summaries ahead and from cache</summary>

### Context and compaction

- **Compaction at 128,000 tokens at most**, whatever the window (`compaction.maxContextTokens`). Every turn re-reads the
  context; on a 1M window it used to wait until 900,000.
- **Old tool results shortened.** Once the context passes 40,000 tokens, tool results older than the latest 8 with 2,000
  characters or more (or an image) go to the model as a one-line note; the session keeps them. The boundary moves in
  steps of 8, so the prompt cache is rebuilt once per step (`toolResultPruning`), and never reaches the results of the
  calls the model is still waiting for. On a resumed 100-turn session the request was 237 KB instead of Pi's 590 KB.
- **Summaries made ahead.** From 15% (at most 40,000 tokens) below the threshold, the summary is made in the background
  while the agent works, and the compaction at the threshold takes it. Until it is ready the agent goes on above the
  threshold, by at most half the threshold and half the way to the window's limit. Before, the agent stood still for
  each summary: 14, 56, 60 and 95 s in four real sessions. A manual or overflow compaction stops it.
- **The task stays word for word.** A summary drops requirements and constraints of the task (arXiv 2608.11242: about
  17% survive). After µPi's own compactions the user's messages from the compacted part stay next to the summary
  verbatim, oldest first, up to 16,000 characters: the first (the task), then the newest that fit.
- **Summaries from the prompt cache.** The compaction summary is asked for as a continuation of the session's own
  request, so the provider reads the conversation from its cache (as deepseek-harness does). Measured with µPi 0.1 and
  `deepseek-v4.1-flash`: 54k → **8.4k** new input tokens per manual compaction, median of 4.

A test checks that every request of a session starts with the previous one, so a change that would break the prompt
cache fails it.

</details>

<details>
<summary><b>Fewer turns</b>: project listing, batched calls, smaller prompt</summary>

### Fewer turns

Every turn costs a request (about 0.8 s with these models even when the answer is short), and in harnessbench 95% of a
run's time was the model's. So:

- **The project is in the prompt.** A `<project>` section, taken once per session, lists the working folder's files
  (git's, up to 150; a larger project by folders with file counts). Nearly every run used to begin by listing the
  folder. `PI_PROJECT_SNAPSHOT=off` leaves it out.
- **An edit and its test in one response.** The calls of a response run in order when one of them is `edit`, `write` or
  `bash`, and the prompt says that all edits for a fix and the command that tests them can go together.
- **A smaller prompt than Pi's**: shorter rules, no `grep`/`find` schemas by default, a two-line docs pointer.
- **Answers cut off at the output limit go on.** A response that hit the output limit with no tool call used to end the
  run: the model is told and goes on, each time thinking one level less, up to 3 times in a row.

</details>

<details>
<summary><b>Melious provider</b>: built-in provider with live prices</summary>

### Melious provider

Provider `melious` (`MELIOUS_API_KEY`, `https://api.melious.ai/v1`), with its models built in and default model
`glm-5.3`. With a key, context windows and prices are fetched from Melious when the model list is loaded, and models
Melious lists that are not built in are added. Without a connection, the built-in list is used.

</details>

<details>
<summary><b>Switches</b>: every setting and variable</summary>

### Switches

Everything above is on by default.

| Setting or variable | Effect |
|---|---|
| `compaction.maxContextTokens` | Cap for the compaction threshold (default 128000) |
| `toolResultPruning` | Shortening old tool results: `enabled`, `triggerTokens` (40000), `keepRecent` (8), `minChars` (2000) |
| `PI_PROJECT_SNAPSHOT=off` | No project listing in the system prompt |
| `--remote <host>[:<folder>]`, `PI_REMOTE` | Start working on that machine (as `/remote`) |
| `PI_SSH_COMMAND` | The ssh program (default `ssh`) |
| `PI_CHECKS` | `off`, or the background checks to keep (`go,cpp,rust,python,typescript,javascript,shell,luau,json,toml,yaml,rdna4-isa`, and the linters `js-lint,markdown,yaml-lint,luau-lint,cpp-lint`) |
| `PI_CHECKS_WAIT` | Seconds a run that would end waits for the checks of its edits (default 30) |
| `PI_CHECKS_CARGO_TARGET` | `shared` to let the background `cargo check` use the project's `target/` |
| `HCC_AMDGPU_TARGET`, `HIP_OFFLOAD_ARCH` | GPU target for HIP diagnostics (default `gfx1100`) |
| `PI_RADISA`, `PI_RADISA_TIMEOUT`, `PI_RADISA_JOBS`, `RADISA_COMPILE_COMMANDS` | The RDNA4 check (above) |
| `UPI_NODE_RUNTIME_SHARED=off` | Keep the extensions' Node side in the home folder instead of next to `upi` |
| `MICROPI_USE_PI_DIRS=1` | Use Pi's `~/.pi` folders instead of `~/.micropi` |
| `app.jobs.hub` keybinding | Opens the job hub (default `alt+j`) |

</details>

<details>
<summary><b>Tests</b>: how µPi is tested</summary>

### Tests

- **PiG's parity tests** check the harness against Pi's own behaviour (they need Pi's npm package and Node for the
  comparisons; `make parity-deps`).
- **µPi's features** are tested in Go next to their code: `internal/codingagent/checks`, `internal/codingagent/jobs`,
  `internal/codingagent/compaction`, `internal/docsearch`, `internal/codingagent/tools` (search, early start, bash
  writes, pruning), and the session tests in `coding/`. Tests added for a µPi fix are named in their comment.
- **End to end**: `tests/e2e` drives the built `upi` through every feature (`cd tests/e2e && GOWORK=off go test .`).

Run a package's tests with `go test ./internal/docsearch/`, for example. Tests that talk to a provider run only when
its keys are set.

</details>

## Credits

Ideas taken from [deepseek-harness](https://github.com/deepseek-ai/deepseek-harness) (compaction on the cached prefix,
moving commands to the background, no sleep polling, the request-prefix test), [oh-my-pi](https://github.com/can1357/oh-my-pi)
(background jobs, `wait`, the hub), Codex (`seek_sequence`) and Claude Code (nested instruction files). Built on
[PiG](https://github.com/MichaelKinsy/PiG) and [Pi](https://github.com/earendil-works/pi); µPi 0.1 was built on
[Pi-Bolt](https://github.com/opensec-git/Pi-Bolt). Harness costs measured with [Buffon](https://michaelkinsy.github.io/buffon/).
