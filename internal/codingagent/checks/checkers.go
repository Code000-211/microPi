package checks

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"go.yaml.in/yaml/v3"

	"github.com/MichaelKinsy/PiG/internal/codingagent/filetracking"
)

// DiagnosticSeverity is "error" or "warning" (linter finding).
type DiagnosticSeverity string

const (
	SeverityError   DiagnosticSeverity = "error"
	SeverityWarning DiagnosticSeverity = "warning"
)

// Diagnostic represents one finding from a checker or linter.
type Diagnostic struct {
	File     string
	Line     int
	Column   int
	Severity DiagnosticSeverity
	Message  string
	Code     string
}

// CheckUnit identifies the unit (file or project) checked by a Checker.
type CheckUnit struct {
	Key     string
	Checker Checker
	CWD     string
	File    string
}

// Checker validates a file or project unit and returns diagnostics.
type Checker interface {
	Name() string
	Matches(path string) bool
	Unit(path string) *CheckUnit
	Run(ctx context.Context, unit *CheckUnit) ([]Diagnostic, error)
}

const runTimeout = 300 * time.Second

type simpleChecker struct {
	name    string
	matches func(path string) bool
	unit    func(path string, self Checker) *CheckUnit
	run     func(ctx context.Context, unit *CheckUnit) ([]Diagnostic, error)
}

func (c *simpleChecker) Name() string             { return c.name }
func (c *simpleChecker) Matches(path string) bool { return c.matches(path) }
func (c *simpleChecker) Unit(path string) *CheckUnit {
	return c.unit(path, c)
}
func (c *simpleChecker) Run(ctx context.Context, unit *CheckUnit) ([]Diagnostic, error) {
	return c.run(ctx, unit)
}

// OnPath looks up executable name on PATH, caching positive and negative results.
var (
	pathCacheMu sync.Mutex
	pathCache   = make(map[string]string)
)

func OnPath(name string) string {
	pathCacheMu.Lock()
	if p, ok := pathCache[name]; ok {
		pathCacheMu.Unlock()
		return p
	}
	pathCacheMu.Unlock()

	found, err := exec.LookPath(name)
	if err != nil {
		found = ""
	}
	pathCacheMu.Lock()
	pathCache[name] = found
	pathCacheMu.Unlock()
	return found
}

// ResetPathCacheForTest clears the OnPath cache (for tests modifying PATH).
func ResetPathCacheForTest() {
	pathCacheMu.Lock()
	clear(pathCache)
	pathCacheMu.Unlock()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func findUp(dir string, names []string) string {
	current, err := filepath.Abs(dir)
	if err != nil {
		current = filepath.Clean(dir)
	}
	for {
		for _, name := range names {
			if pathExists(filepath.Join(current, name)) {
				return current
			}
		}
		up := filepath.Dir(current)
		if up == current {
			return ""
		}
		current = up
	}
}

func configured(dir string, names []string) bool {
	return findUp(dir, names) != ""
}

func mentions(dir, file string, re *regexp.Regexp) bool {
	found := findUp(dir, []string{file})
	if found == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(found, file))
	if err != nil {
		return false
	}
	return re.Match(data)
}

func extLower(path string) string {
	ext := filepath.Ext(path)
	if ext == "" {
		return ""
	}
	return strings.ToLower(ext[1:])
}

type cmdOutput struct {
	stdout string
	stderr string
	code   int
}

func runCommand(ctx context.Context, argv []string, cwd string, extraEnv map[string]string, timeout time.Duration) (cmdOutput, error) {
	if len(argv) == 0 {
		return cmdOutput{}, errors.New("empty command")
	}
	if timeout <= 0 {
		timeout = runTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
	cmd.Dir = cwd
	if len(extraEnv) > 0 {
		env := os.Environ()
		for k, v := range extraEnv {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	if runCtx.Err() != nil {
		return cmdOutput{}, runCtx.Err()
	}
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			return cmdOutput{}, err
		}
	}
	return cmdOutput{
		stdout: stdoutBuf.String(),
		stderr: stderrBuf.String(),
		code:   code,
	}, nil
}

func diag(file string, line, col int, msg, code string, sev DiagnosticSeverity) Diagnostic {
	if line < 1 {
		line = 1
	}
	if col < 1 {
		col = 1
	}
	if sev == "" {
		sev = SeverityError
	}
	return Diagnostic{
		File:     file,
		Line:     line,
		Column:   col,
		Severity: sev,
		Message:  strings.TrimSpace(msg),
		Code:     code,
	}
}

func resolveAbs(base, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(base, p))
}

var colonFindingRE = regexp.MustCompile(`^(.+?):(\d+):(\d+): (error|warning)\[([^\]]+)\]:? (.*)$`)

func parseColonFindings(text, cwd string, lint bool) []Diagnostic {
	var out []Diagnostic
	for _, line := range strings.Split(text, "\n") {
		m := colonFindingRE.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		if m[4] == "warning" && !lint {
			continue
		}
		ln, _ := strconv.Atoi(m[2])
		col, _ := strconv.Atoi(m[3])
		out = append(out, diag(resolveAbs(cwd, m[1]), ln, col, m[6], m[5], DiagnosticSeverity(m[4])))
	}
	return out
}

// ─── Go: go vet ───────────────────────────────────────────────────────────────

var goVetLineColRE = regexp.MustCompile(`^(.+?):(\d+):(\d+): (.*)$`)
var goVetLineRE = regexp.MustCompile(`^(.+?):(\d+): (.*)$`)

var goChecker Checker = &simpleChecker{
	name: "go",
	matches: func(path string) bool {
		return extLower(path) == "go"
	},
	unit: func(path string, self Checker) *CheckUnit {
		if OnPath("go") == "" {
			return nil
		}
		modRoot := findUp(filepath.Dir(path), []string{"go.mod"})
		if modRoot == "" {
			return nil
		}
		pkgDir := filepath.Dir(path)
		return &CheckUnit{
			Key:     "go:" + pkgDir,
			Checker: self,
			CWD:     modRoot,
			File:    path,
		}
	},
	run: func(ctx context.Context, unit *CheckUnit) ([]Diagnostic, error) {
		relPkg, err := filepath.Rel(unit.CWD, filepath.Dir(unit.File))
		if err != nil || strings.HasPrefix(relPkg, "..") {
			relPkg = "."
		} else if relPkg == "." {
			relPkg = "."
		} else {
			relPkg = "./" + filepath.ToSlash(relPkg)
		}
		ran, err := runCommand(ctx, []string{"go", "build", "-gcflags=-e", "-o", os.DevNull, relPkg}, unit.CWD, nil, 0)
		if err != nil {
			return nil, err
		}
		if ran.code == 0 {
			ran, err = runCommand(ctx, []string{"go", "vet", relPkg}, unit.CWD, nil, 0)
			if err != nil {
				return nil, err
			}
		}
		var out []Diagnostic
		for _, line := range strings.Split(ran.stderr+"\n"+ran.stdout, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			trimmed = strings.TrimPrefix(trimmed, "vet: ")
			if m := goVetLineColRE.FindStringSubmatch(trimmed); m != nil {
				ln, _ := strconv.Atoi(m[2])
				col, _ := strconv.Atoi(m[3])
				out = append(out, diag(resolveAbs(unit.CWD, m[1]), ln, col, m[4], "", SeverityError))
			} else if m := goVetLineRE.FindStringSubmatch(trimmed); m != nil {
				ln, _ := strconv.Atoi(m[2])
				out = append(out, diag(resolveAbs(unit.CWD, m[1]), ln, 1, m[3], "", SeverityError))
			}
		}
		return out, nil
	},
}

// ─── Rust: cargo check / clippy ───────────────────────────────────────────────

var (
	cargoWorkspaceRE = regexp.MustCompile(`(?m)^\s*\[workspace\]`)
	clippyLintRE     = regexp.MustCompile(`lints\.clippy`)
	cargoClippyCIRE  = regexp.MustCompile(`cargo\s+clippy`)
	clippyCacheMu    sync.Mutex
	clippyCache      = make(map[string]bool)
)

func cargoProject(path string) (manifest, root string, ok bool) {
	pkg := findUp(filepath.Dir(path), []string{"Cargo.toml"})
	if pkg == "" {
		return "", "", false
	}
	root = pkg
	for dir := filepath.Dir(pkg); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		m := filepath.Join(dir, "Cargo.toml")
		if data, err := os.ReadFile(m); err == nil && cargoWorkspaceRE.Match(data) {
			root = dir
		}
	}
	return filepath.Join(pkg, "Cargo.toml"), root, true
}

func usesClippy(root string) bool {
	clippyCacheMu.Lock()
	if v, ok := clippyCache[root]; ok {
		clippyCacheMu.Unlock()
		return v
	}
	clippyCacheMu.Unlock()

	uses := fileExists(filepath.Join(root, "clippy.toml")) || fileExists(filepath.Join(root, ".clippy.toml"))
	if !uses {
		if data, err := os.ReadFile(filepath.Join(root, "Cargo.toml")); err == nil {
			uses = clippyLintRE.Match(data)
		}
	}
	if !uses {
		for _, ci := range []string{
			filepath.Join(root, ".github", "workflows"),
			filepath.Join(root, ".forgejo", "workflows"),
			filepath.Join(root, ".gitlab-ci.yml"),
		} {
			info, err := os.Stat(ci)
			if err != nil {
				continue
			}
			var files []string
			if info.IsDir() {
				entries, _ := os.ReadDir(ci)
				for _, e := range entries {
					files = append(files, filepath.Join(ci, e.Name()))
				}
			} else {
				files = []string{ci}
			}
			for _, f := range files {
				if data, err := os.ReadFile(f); err == nil && cargoClippyCIRE.Match(data) {
					uses = true
					break
				}
			}
			if uses {
				break
			}
		}
	}

	clippyCacheMu.Lock()
	clippyCache[root] = uses
	clippyCacheMu.Unlock()
	return uses
}

var rustChecker Checker = &simpleChecker{
	name: "rust",
	matches: func(path string) bool {
		return extLower(path) == "rs"
	},
	unit: func(path string, self Checker) *CheckUnit {
		if OnPath("cargo") == "" {
			return nil
		}
		manifest, root, ok := cargoProject(path)
		if !ok {
			return nil
		}
		return &CheckUnit{
			Key:     "rust:" + manifest,
			Checker: self,
			CWD:     root,
			File:    manifest,
		}
	},
	run: func(ctx context.Context, unit *CheckUnit) ([]Diagnostic, error) {
		var env map[string]string
		if os.Getenv("PI_CHECKS_CARGO_TARGET") != "shared" {
			env = map[string]string{"CARGO_TARGET_DIR": filepath.Join(unit.CWD, "target", "pi-check")}
		}
		clippy := usesClippy(unit.CWD)
		subcmd := "check"
		if clippy {
			subcmd = "clippy"
		}
		ran, err := runCommand(ctx, []string{"cargo", subcmd, "--message-format=json", "--quiet", "--manifest-path", unit.File}, unit.CWD, env, 0)
		if err != nil {
			return nil, err
		}
		var out []Diagnostic
		for _, line := range strings.Split(ran.stdout, "\n") {
			if !strings.HasPrefix(line, "{") {
				continue
			}
			var event struct {
				Reason  string `json:"reason"`
				Message *struct {
					Level   string `json:"level"`
					Message string `json:"message"`
					Code    *struct {
						Code string `json:"code"`
					} `json:"code"`
					Spans []struct {
						FileName    string `json:"file_name"`
						LineStart   int    `json:"line_start"`
						ColumnStart int    `json:"column_start"`
						IsPrimary   bool   `json:"is_primary"`
					} `json:"spans"`
				} `json:"message"`
			}
			if json.Unmarshal([]byte(line), &event) != nil || event.Reason != "compiler-message" || event.Message == nil {
				continue
			}
			var sev DiagnosticSeverity
			switch event.Message.Level {
			case "error":
				sev = SeverityError
			case "warning":
				if clippy {
					sev = SeverityWarning
				}
			}
			if sev == "" || len(event.Message.Spans) == 0 {
				continue
			}
			span := event.Message.Spans[0]
			for _, s := range event.Message.Spans {
				if s.IsPrimary {
					span = s
					break
				}
			}
			code := ""
			if event.Message.Code != nil {
				code = event.Message.Code.Code
			}
			out = append(out, diag(resolveAbs(unit.CWD, span.FileName), span.LineStart, span.ColumnStart, event.Message.Message, code, sev))
		}
		return out, nil
	},
}

// ─── Python: ty + ruff, fallback python3 ast.parse ────────────────────────────

var (
	pythonRoots      = []string{"pyproject.toml", "setup.py", "setup.cfg", "ty.toml", ".git"}
	ruffToolRE       = regexp.MustCompile(`(?m)^\[tool\.ruff`)
	ruffLineRE       = regexp.MustCompile(`^(.+?):(\d+):(\d+): (\S+) (?:\[\*\] )?(.*)$`)
	pythonAstErrorRE = regexp.MustCompile(`(?m)line (\d+)[\s\S]*?(\w+Error: .*)$`)
)

func runRuff(ctx context.Context, unit *CheckUnit) ([]Diagnostic, error) {
	dir := filepath.Dir(unit.File)
	uses := configured(dir, []string{"ruff.toml", ".ruff.toml"}) || mentions(dir, "pyproject.toml", ruffToolRE)
	if !uses || OnPath("ruff") == "" {
		return nil, nil
	}
	ran, err := runCommand(ctx, []string{"ruff", "check", "--output-format", "concise", "--no-cache", "--quiet", unit.File}, unit.CWD, nil, 0)
	if err != nil {
		return nil, err
	}
	var out []Diagnostic
	for _, line := range strings.Split(ran.stdout, "\n") {
		if m := ruffLineRE.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			ln, _ := strconv.Atoi(m[2])
			col, _ := strconv.Atoi(m[3])
			out = append(out, diag(resolveAbs(unit.CWD, m[1]), ln, col, m[5], m[4], SeverityWarning))
		}
	}
	return out, nil
}

var pythonChecker Checker = &simpleChecker{
	name: "python",
	matches: func(path string) bool {
		ext := extLower(path)
		return ext == "py" || ext == "pyi"
	},
	unit: func(path string, self Checker) *CheckUnit {
		cwd := findUp(filepath.Dir(path), pythonRoots)
		if cwd == "" {
			cwd = filepath.Dir(path)
		}
		return &CheckUnit{
			Key:     "python:" + path,
			Checker: self,
			CWD:     cwd,
			File:    path,
		}
	},
	run: func(ctx context.Context, unit *CheckUnit) ([]Diagnostic, error) {
		if OnPath("ty") != "" {
			ran, err := runCommand(ctx, []string{"ty", "check", "--output-format", "concise", "--no-progress", unit.File}, unit.CWD, nil, 0)
			if err != nil {
				return nil, err
			}
			var found []Diagnostic
			for _, d := range parseColonFindings(ran.stdout, unit.CWD, false) {
				if d.File == filepath.Clean(unit.File) {
					found = append(found, d)
				}
			}
			ruffDiags, err := runRuff(ctx, unit)
			if err == nil {
				found = append(found, ruffDiags...)
			}
			return found, nil
		}
		py3 := OnPath("python3")
		if py3 == "" {
			return nil, nil
		}
		ran, err := runCommand(ctx, []string{
			py3, "-c", "import ast,sys; ast.parse(open(sys.argv[1], encoding='utf-8').read(), sys.argv[1])", unit.File,
		}, unit.CWD, nil, 0)
		if err != nil {
			return nil, err
		}
		if m := pythonAstErrorRE.FindStringSubmatch(ran.stderr); m != nil {
			ln, _ := strconv.Atoi(m[1])
			return []Diagnostic{diag(unit.File, ln, 1, m[2], "", SeverityError)}, nil
		}
		return nil, nil
	},
}

// ─── TypeScript: tsc; JavaScript: node ───────────────────────────────────────

var tscLineRE = regexp.MustCompile(`^(.+?)\((\d+),(\d+)\): error (TS\d+): (.*)$`)

func tscFor(projectDir string) string {
	if global := OnPath("tsc"); global != "" {
		return global
	}
	local := findUp(projectDir, []string{filepath.Join("node_modules", ".bin", "tsc")})
	if local != "" {
		return filepath.Join(local, "node_modules", ".bin", "tsc")
	}
	return ""
}

var typescriptChecker Checker = &simpleChecker{
	name: "typescript",
	matches: func(path string) bool {
		if strings.HasSuffix(path, ".d.ts") {
			return false
		}
		switch extLower(path) {
		case "ts", "tsx", "mts", "cts":
			return true
		}
		return false
	},
	unit: func(path string, self Checker) *CheckUnit {
		project := findUp(filepath.Dir(path), []string{"tsconfig.json"})
		if project == "" || tscFor(project) == "" {
			return nil
		}
		return &CheckUnit{
			Key:     "typescript:" + project,
			Checker: self,
			CWD:     project,
		}
	},
	run: func(ctx context.Context, unit *CheckUnit) ([]Diagnostic, error) {
		tsc := tscFor(unit.CWD)
		if tsc == "" {
			return nil, nil
		}
		h := sha1.Sum([]byte(unit.CWD))
		buildInfo := filepath.Join(os.TempDir(), "pi-checks", hex.EncodeToString(h[:8])+".tsbuildinfo")
		ran, err := runCommand(ctx, []string{
			tsc, "--noEmit", "--pretty", "false", "--incremental", "--tsBuildInfoFile", buildInfo,
			"-p", filepath.Join(unit.CWD, "tsconfig.json"),
		}, unit.CWD, nil, 0)
		if err != nil {
			return nil, err
		}
		var out []Diagnostic
		for _, line := range strings.Split(ran.stdout, "\n") {
			if m := tscLineRE.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
				ln, _ := strconv.Atoi(m[2])
				col, _ := strconv.Atoi(m[3])
				out = append(out, diag(resolveAbs(unit.CWD, m[1]), ln, col, m[5], m[4], SeverityError))
			}
		}
		return out, nil
	},
}

var (
	nodeLineRE    = regexp.MustCompile(`:(\d+)\n`)
	nodeMessageRE = regexp.MustCompile(`(?m)^(\w*Error: .*)$`)
)

var javascriptChecker Checker = &simpleChecker{
	name: "javascript",
	matches: func(path string) bool {
		switch extLower(path) {
		case "js", "mjs", "cjs":
			return true
		}
		return false
	},
	unit: func(path string, self Checker) *CheckUnit {
		if OnPath("node") == "" {
			return nil
		}
		return &CheckUnit{
			Key:     "javascript:" + path,
			Checker: self,
			CWD:     filepath.Dir(path),
			File:    path,
		}
	},
	run: func(ctx context.Context, unit *CheckUnit) ([]Diagnostic, error) {
		ran, err := runCommand(ctx, []string{"node", "--check", unit.File}, unit.CWD, nil, 0)
		if err != nil {
			return nil, err
		}
		if ran.code == 0 {
			return nil, nil
		}
		ln := 1
		if m := nodeLineRE.FindStringSubmatch(ran.stderr); m != nil {
			ln, _ = strconv.Atoi(m[1])
		}
		if m := nodeMessageRE.FindStringSubmatch(ran.stderr); m != nil {
			return []Diagnostic{diag(unit.File, ln, 1, m[1], "", SeverityError)}, nil
		}
		return nil, nil
	},
}

// ─── Shell: bash -n, shuck ────────────────────────────────────────────────────

var (
	bashLineRE   = regexp.MustCompile(`: line (\d+): (.*)$`)
	bashQuotedRE = regexp.MustCompile("^`.*'$")
)

var shellChecker Checker = &simpleChecker{
	name: "shell",
	matches: func(path string) bool {
		ext := extLower(path)
		return ext == "sh" || ext == "bash"
	},
	unit: func(path string, self Checker) *CheckUnit {
		if OnPath("bash") == "" && OnPath("shuck") == "" {
			return nil
		}
		return &CheckUnit{
			Key:     "shell:" + path,
			Checker: self,
			CWD:     filepath.Dir(path),
			File:    path,
		}
	},
	run: func(ctx context.Context, unit *CheckUnit) ([]Diagnostic, error) {
		var out []Diagnostic
		if OnPath("bash") != "" {
			ran, err := runCommand(ctx, []string{"bash", "-n", unit.File}, unit.CWD, map[string]string{"LC_ALL": "C"}, 0)
			if err != nil {
				return nil, err
			}
			for _, line := range strings.Split(ran.stderr, "\n") {
				if m := bashLineRE.FindStringSubmatch(line); m != nil && !bashQuotedRE.MatchString(m[2]) {
					ln, _ := strconv.Atoi(m[1])
					out = append(out, diag(unit.File, ln, 1, m[2], "", SeverityError))
				}
			}
		}
		if len(out) == 0 && OnPath("shuck") != "" {
			lint := configured(unit.CWD, []string{".shellcheckrc", "shuck.toml", ".shuck.toml"})
			ran, err := runCommand(ctx, []string{"shuck", "check", "--output-format", "concise", unit.File}, unit.CWD, nil, 0)
			if err != nil {
				return nil, err
			}
			out = append(out, parseColonFindings(ran.stdout, unit.CWD, lint)...)
		}
		return out, nil
	},
}

// ─── Luau: luau-analyze ───────────────────────────────────────────────────────

var (
	luauStrictRE = regexp.MustCompile(`(?m)^--!strict`)
	luauLineRE   = regexp.MustCompile(`^(.+?)\((\d+),(\d+)\): (\w+): (.*)$`)
)

var luauChecker Checker = &simpleChecker{
	name: "luau",
	matches: func(path string) bool {
		return extLower(path) == "luau"
	},
	unit: func(path string, self Checker) *CheckUnit {
		if OnPath("luau-analyze") == "" {
			return nil
		}
		return &CheckUnit{
			Key:     "luau:" + path,
			Checker: self,
			CWD:     filepath.Dir(path),
			File:    path,
		}
	},
	run: func(ctx context.Context, unit *CheckUnit) ([]Diagnostic, error) {
		ran, err := runCommand(ctx, []string{"luau-analyze", unit.File}, unit.CWD, nil, 0)
		if err != nil {
			return nil, err
		}
		raw, _ := os.ReadFile(unit.File)
		strict := luauStrictRE.Match(raw)
		var out []Diagnostic
		for _, line := range strings.Split(ran.stdout+"\n"+ran.stderr, "\n") {
			m := luauLineRE.FindStringSubmatch(strings.TrimSpace(line))
			if m == nil {
				continue
			}
			if m[4] == "SyntaxError" || (strict && m[4] == "TypeError") {
				ln, _ := strconv.Atoi(m[2])
				col, _ := strconv.Atoi(m[3])
				out = append(out, diag(unit.File, ln, col, m[5], m[4], SeverityError))
			}
		}
		return out, nil
	},
}

// ─── JSON, TOML, YAML: pure Go in-process parsers ─────────────────────────────

func offsetToLineCol(data []byte, offset int64) (int, int) {
	if offset <= 0 {
		return 1, 1
	}
	if offset > int64(len(data)) {
		offset = int64(len(data))
	}
	prefix := data[:offset]
	line := bytes.Count(prefix, []byte{'\n'}) + 1
	lastNL := bytes.LastIndexByte(prefix, '\n')
	col := int(offset) - lastNL
	if col < 1 {
		col = 1
	}
	return line, col
}

var jsonChecker Checker = &simpleChecker{
	name: "json",
	matches: func(path string) bool {
		return extLower(path) == "json" && !strings.HasSuffix(path, "tsconfig.json")
	},
	unit: func(path string, self Checker) *CheckUnit {
		return &CheckUnit{Key: "json:" + path, Checker: self, CWD: filepath.Dir(path), File: path}
	},
	run: func(_ context.Context, unit *CheckUnit) ([]Diagnostic, error) {
		data, err := os.ReadFile(unit.File)
		if err != nil {
			return nil, err
		}
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			var syn *json.SyntaxError
			ln, col := 1, 1
			if errors.As(err, &syn) {
				ln, col = offsetToLineCol(data, syn.Offset)
			}
			return []Diagnostic{diag(unit.File, ln, col, err.Error(), "", SeverityError)}, nil
		}
		return nil, nil
	},
}

var yamlLineRE = regexp.MustCompile(`line (\d+)`)

var tomlChecker Checker = &simpleChecker{
	name: "toml",
	matches: func(path string) bool {
		return extLower(path) == "toml"
	},
	unit: func(path string, self Checker) *CheckUnit {
		return &CheckUnit{Key: "toml:" + path, Checker: self, CWD: filepath.Dir(path), File: path}
	},
	run: func(_ context.Context, unit *CheckUnit) ([]Diagnostic, error) {
		data, err := os.ReadFile(unit.File)
		if err != nil {
			return nil, err
		}
		var v any
		if _, err := toml.Decode(string(data), &v); err != nil {
			ln := 1
			var parseErr toml.ParseError
			if errors.As(err, &parseErr) && parseErr.Position.Line > 0 {
				ln = parseErr.Position.Line
			} else if m := yamlLineRE.FindStringSubmatch(err.Error()); m != nil {
				ln, _ = strconv.Atoi(m[1])
			}
			return []Diagnostic{diag(unit.File, ln, 1, err.Error(), "", SeverityError)}, nil
		}
		return nil, nil
	},
}

var yamlChecker Checker = &simpleChecker{
	name: "yaml",
	matches: func(path string) bool {
		ext := extLower(path)
		return ext == "yaml" || ext == "yml"
	},
	unit: func(path string, self Checker) *CheckUnit {
		return &CheckUnit{Key: "yaml:" + path, Checker: self, CWD: filepath.Dir(path), File: path}
	},
	run: func(_ context.Context, unit *CheckUnit) ([]Diagnostic, error) {
		data, err := os.ReadFile(unit.File)
		if err != nil {
			return nil, err
		}
		var v any
		if err := yaml.Unmarshal(data, &v); err != nil {
			ln := 1
			if m := yamlLineRE.FindStringSubmatch(err.Error()); m != nil {
				ln, _ = strconv.Atoi(m[1])
			}
			return []Diagnostic{diag(unit.File, ln, 1, err.Error(), "", SeverityError)}, nil
		}
		return nil, nil
	},
}

// ─── AMD RDNA4 (gfx12) machine code: radisa ───────────────────────────────────

var (
	amdgpuAsmRE    = regexp.MustCompile(`\.amdgcn_target|\.amdhsa_kernel|\bs_endpgm\b|\bs_wait_\w+|\bv_\w+_[bfiu]\d+`)
	offloadArchRE  = regexp.MustCompile(`(?:--offload-arch|--cuda-gpu-arch|-mcpu)[= ](gfx\w+)`)
	clangMajorRE   = regexp.MustCompile(`clang version (\d+)`)
	radisaErrWhyRE = regexp.MustCompile(`(?m)\berror: (.*)$`)
	radisaAsmRE    = regexp.MustCompile(`(?m)^(.+?):(\d+):(\d+): error: (.*)$`)
	radisaLocRE    = regexp.MustCompile(`\(([^\s():]+):(\d+)\)`)
	radisaPcRE     = regexp.MustCompile(`\s*\(pc 0x[0-9a-f]+\)`)
)

func radisaSuite(dir string) string {
	if env := os.Getenv("PI_RADISA"); env != "" {
		if fileExists(filepath.Join(env, "radisa", "__main__.py")) {
			abs, err := filepath.Abs(env)
			if err == nil {
				return abs
			}
			return env
		}
		return ""
	}
	for _, sub := range []string{filepath.Join("tools", "rdna4-isa-suite"), "rdna4-isa-suite"} {
		if found := findUp(dir, []string{filepath.Join(sub, "radisa", "__main__.py")}); found != "" {
			return filepath.Join(found, sub)
		}
	}
	return ""
}

func compileDbForRadisa(file string) string {
	if env := os.Getenv("RADISA_COMPILE_COMMANDS"); env != "" && fileExists(env) {
		absEnv, _ := filepath.Abs(env)
		absFile, _ := filepath.Abs(file)
		if _, ok := offloadArchsForDb(absEnv)[absFile]; ok {
			return absEnv
		}
	}
	names := []string{
		"compile_commands.json",
		filepath.Join("build", "compile_commands.json"),
		filepath.Join("out", "compile_commands.json"),
		filepath.Join("cmake-build-release", "compile_commands.json"),
		filepath.Join("cmake-build-debug", "compile_commands.json"),
	}
	found := findUp(filepath.Dir(file), names)
	if found == "" {
		return ""
	}
	for _, n := range names {
		cand := filepath.Join(found, n)
		if fileExists(cand) {
			return cand
		}
	}
	return ""
}

func offloadArchsForDb(db string) map[string][]string {
	raw, err := os.ReadFile(db)
	if err != nil {
		return nil
	}
	var entries []struct {
		Directory string   `json:"directory"`
		File      string   `json:"file"`
		Command   string   `json:"command"`
		Arguments []string `json:"arguments"`
	}
	if json.Unmarshal(raw, &entries) != nil {
		return nil
	}
	out := make(map[string][]string)
	for _, e := range entries {
		text := e.Command
		if len(e.Arguments) > 0 {
			text = strings.Join(e.Arguments, " ")
		}
		matches := offloadArchRE.FindAllStringSubmatch(text, -1)
		base := e.Directory
		if base == "" {
			base = filepath.Dir(db)
		}
		abs := resolveAbs(base, e.File)
		for _, m := range matches {
			out[abs] = append(out[abs], m[1])
		}
	}
	return out
}

func buildsForGfx12(path, db string) bool {
	hasGfx12 := func(archs []string) bool {
		for _, a := range archs {
			if strings.HasPrefix(a, "gfx12") {
				return true
			}
		}
		return false
	}
	if db != "" {
		abs, _ := filepath.Abs(path)
		if archs := offloadArchsForDb(db)[abs]; len(archs) > 0 {
			return hasGfx12(archs)
		}
	}
	env := os.Getenv("HIP_OFFLOAD_ARCH")
	if env == "" {
		env = os.Getenv("HCC_AMDGPU_TARGET")
	}
	if env == "" {
		return true
	}
	parts := strings.FieldsFunc(env, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\t' })
	return hasGfx12(parts)
}

func amdgpuAssembly(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, 65536)
	n, _ := f.Read(buf)
	return amdgpuAsmRE.Match(buf[:n])
}

type radisaFinding struct {
	Rule        string `json:"rule"`
	Severity    string `json:"severity"`
	Message     string `json:"message"`
	Instruction string `json:"instruction"`
	Loc         *struct {
		File   string `json:"file"`
		Line   int    `json:"line"`
		Column int    `json:"column"`
	} `json:"loc"`
}

type radisaReport struct {
	Findings []radisaFinding `json:"findings"`
	Kernels  []struct {
		Name     string          `json:"name"`
		Callee   bool            `json:"callee"`
		Findings []radisaFinding `json:"findings"`
	} `json:"kernels"`
	Incremental *struct {
		Compiler string `json:"compiler"`
	} `json:"incremental"`
}

func radisaDiagnostics(report radisaReport, file string) []Diagnostic {
	var out []Diagnostic
	assembly := extLower(file) == "s"
	major := 0
	if report.Incremental != nil {
		if m := clangMajorRE.FindStringSubmatch(report.Incremental.Compiler); m != nil {
			major, _ = strconv.Atoi(m[1])
		}
	}
	oldCompiler := !assembly && major > 0 && major < 20
	leftOut := false

	add := func(f radisaFinding, kernelName string, isCallee bool) {
		if f.Rule == "VOPD_ANTIDEP" || f.Rule == "COMPILER_DIAGNOSTICS" {
			return
		}
		if oldCompiler && (f.Rule == "MISSING_ALU_WAIT" || f.Rule == "ALU_WAIT_TTMP") {
			leftOut = true
			return
		}
		var sev DiagnosticSeverity
		switch f.Severity {
		case "error":
			sev = SeverityError
		case "review", "incomplete":
			sev = SeverityWarning
		default:
			return
		}
		msg := radisaPcRE.ReplaceAllString(f.Message, "")
		loc := f.Loc
		if f.Rule == "COMPILE" {
			why := "see `radisa check`"
			if m := radisaErrWhyRE.FindStringSubmatch(msg); m != nil {
				why = m[1]
			}
			sev = SeverityWarning
			msg = fmt.Sprintf("not checked: the unit did not compile for gfx1201 (%s)", why)
		}
		if f.Rule == "ASSEMBLER" {
			if m := radisaAsmRE.FindStringSubmatch(msg); assembly && m != nil {
				ln, _ := strconv.Atoi(m[2])
				col, _ := strconv.Atoi(m[3])
				loc = &struct {
					File   string `json:"file"`
					Line   int    `json:"line"`
					Column int    `json:"column"`
				}{File: m[1], Line: ln, Column: col}
				msg = m[4]
			} else if !assembly {
				sev = SeverityWarning
				msg = strings.SplitN(msg, "\n", 2)[0]
			}
		}
		at := file
		lineNo := 1
		colNo := 1
		if loc != nil {
			at = resolveAbs(filepath.Dir(file), loc.File)
			if loc.Line > 0 {
				lineNo = loc.Line
			}
			if loc.Column > 0 {
				colNo = loc.Column
			}
			baseName := filepath.Base(at)
			msg = radisaLocRE.ReplaceAllStringFunc(msg, func(whole string) string {
				sub := radisaLocRE.FindStringSubmatch(whole)
				if len(sub) < 3 || sub[1] != baseName {
					return whole
				}
				n, _ := strconv.Atoi(sub[2])
				d := n - lineNo
				if d == 0 {
					return "(same line)"
				}
				absD := d
				dir := "below"
				if d < 0 {
					absD = -d
					dir = "above"
				}
				plural := "s"
				if absD == 1 {
					plural = ""
				}
				return fmt.Sprintf("(%d line%s %s)", absD, plural, dir)
			})
		}
		if f.Severity == "incomplete" && f.Rule != "COMPILE" {
			msg = "not checked: " + msg
		}
		if f.Instruction != "" {
			msg += fmt.Sprintf(" [%s]", f.Instruction)
		}
		if loc == nil && kernelName != "" {
			kind := "kernel"
			if isCallee {
				kind = "function"
			}
			msg = fmt.Sprintf("in %s %s: %s", kind, kernelName, msg)
		}
		out = append(out, diag(at, lineNo, colNo, msg, f.Rule, sev))
	}

	for _, f := range report.Findings {
		add(f, "", false)
	}
	for _, k := range report.Kernels {
		for _, f := range k.Findings {
			add(f, k.Name, k.Callee)
		}
	}
	if leftOut {
		out = append(out, diag(
			file, 1, 1,
			fmt.Sprintf("radisa's s_wait_alu checks are left out: the device compiler is LLVM %d, which inserts no s_wait_alu (LLVM 20 and later do), so they would report every carry chain it emits. Check with the compiler the code is built with (ROCm's clang) to have them.", major),
			"RADISA_OLD_COMPILER",
			SeverityWarning,
		))
	}
	return out
}

var rdna4Checker Checker = &simpleChecker{
	name: "rdna4-isa",
	matches: func(path string) bool {
		ext := extLower(path)
		return ext == "hip" || ext == "cu" || (ext == "s" && amdgpuAssembly(path))
	},
	unit: func(path string, self Checker) *CheckUnit {
		if OnPath("python3") == "" || radisaSuite(filepath.Dir(path)) == "" {
			return nil
		}
		if extLower(path) != "s" && !buildsForGfx12(path, compileDbForRadisa(path)) {
			return nil
		}
		return &CheckUnit{Key: "rdna4-isa:" + path, Checker: self, CWD: filepath.Dir(path), File: path}
	},
	run: func(ctx context.Context, unit *CheckUnit) ([]Diagnostic, error) {
		suite := radisaSuite(unit.CWD)
		if suite == "" {
			return nil, errors.New("radisa is gone")
		}
		work, err := os.MkdirTemp("", "pi-radisa-")
		if err != nil {
			return nil, err
		}
		defer func() { _ = os.RemoveAll(work) }()

		outPath := filepath.Join(work, "report.json")
		argv := []string{
			"python3", "-I", "-c",
			"import sys; sys.path.insert(0, sys.argv.pop(1)); from radisa.cli import main; sys.exit(main(sys.argv[1:]))",
			suite, "check", unit.File, "--json", outPath,
		}
		if extLower(unit.File) != "s" {
			if db := compileDbForRadisa(unit.File); db != "" {
				argv = append(argv, "--compile-commands", db)
			}
		}
		if jobs, err := strconv.Atoi(os.Getenv("PI_RADISA_JOBS")); err == nil && jobs >= 1 {
			argv = append(argv, "--jobs", strconv.Itoa(jobs))
		}
		timeout := runTimeout
		if secs, err := strconv.ParseFloat(os.Getenv("PI_RADISA_TIMEOUT"), 64); err == nil && secs > 0 {
			timeout = time.Duration(secs * float64(time.Second))
		}
		ran, err := runCommand(ctx, argv, unit.CWD, nil, timeout)
		if err != nil {
			return nil, err
		}
		if ran.code == 2 {
			lines := strings.Split(strings.TrimSpace(ran.stderr), "\n")
			why := lines[len(lines)-1]
			return []Diagnostic{diag(unit.File, 1, 1, "radisa did not check this file: "+why, "RADISA_USAGE", SeverityWarning)}, nil
		}
		if ran.code != 0 && ran.code != 1 && ran.code != 3 {
			lines := strings.Split(strings.TrimSpace(ran.stderr), "\n")
			return nil, fmt.Errorf("radisa ended with %d: %s", ran.code, lines[len(lines)-1])
		}
		raw, err := os.ReadFile(outPath)
		if err != nil {
			return nil, err
		}
		var rep radisaReport
		if err := json.Unmarshal(raw, &rep); err != nil {
			return nil, err
		}
		return radisaDiagnostics(rep, unit.File), nil
	},
}

// ─── Linters ──────────────────────────────────────────────────────────────────

func makeLinter(
	name string,
	exts []string,
	setup func(path string) (cwd string, argv []string, ok bool),
	parse func(output, cwd string) []Diagnostic,
) Checker {
	extSet := make(map[string]bool, len(exts))
	for _, e := range exts {
		extSet[e] = true
	}
	return &simpleChecker{
		name:    name,
		matches: func(path string) bool { return extSet[extLower(path)] },
		unit: func(path string, self Checker) *CheckUnit {
			cwd, _, ok := setup(path)
			if !ok {
				return nil
			}
			return &CheckUnit{Key: name + ":" + path, Checker: self, CWD: cwd, File: path}
		},
		run: func(ctx context.Context, unit *CheckUnit) ([]Diagnostic, error) {
			cwd, argv, ok := setup(unit.File)
			if !ok {
				return nil, nil
			}
			ran, err := runCommand(ctx, argv, cwd, nil, 0)
			if err != nil {
				return nil, err
			}
			diags := parse(ran.stdout+"\n"+ran.stderr, cwd)
			for i := range diags {
				diags[i].Severity = SeverityWarning
			}
			return diags, nil
		},
	}
}

func nodeTool(dir, tool string) string {
	if local := findUp(dir, []string{filepath.Join("node_modules", ".bin", tool)}); local != "" {
		return filepath.Join(local, "node_modules", ".bin", tool)
	}
	return OnPath(tool)
}

var (
	biomeConfigs  = []string{"biome.json", "biome.jsonc"}
	oxlintConfigs = []string{".oxlintrc.json", "oxlintrc.json"}
	eslintConfigs = []string{
		"eslint.config.js", "eslint.config.mjs", "eslint.config.cjs", "eslint.config.ts",
		".eslintrc", ".eslintrc.json", ".eslintrc.js", ".eslintrc.cjs", ".eslintrc.yml", ".eslintrc.yaml",
	}
	githubLintRE = regexp.MustCompile(`^::(error|warning|notice) title=([^,]+),file=([^,]+),line=(\d+),(?:endLine=\d+,)?col=(\d+)[^:]*::(.*)$`)
	unixLintRE   = regexp.MustCompile(`^(.+?):(\d+):(\d+): (.*) \[(?:Warning|Error)/([^\]]+)\]$`)
	rumdlLineRE  = regexp.MustCompile(`^(.+?):(\d+):(\d+): \[(\w+)\] (.*?)(?: \[\*\])?$`)
	rumdlPyRE    = regexp.MustCompile(`(?m)^\[tool\.rumdl`)
	rylLineRE    = regexp.MustCompile(`^(.+?):(\d+):(\d+): \[(?:error|warning)\] (.*?)(?: \(([\w-]+)\))?$`)
	clangdTidyRE = regexp.MustCompile(`^(.+?):(\d+):(\d+): (?:Warning|Error|Information|Hint): (.*?)(?: \[([\w.,-]+)\])?$`)
)

var jsLintChecker = makeLinter(
	"js-lint",
	[]string{"js", "jsx", "mjs", "cjs", "ts", "tsx", "mts", "cts"},
	func(path string) (string, []string, bool) {
		dir := filepath.Dir(path)
		if biomeDir := findUp(dir, biomeConfigs); biomeDir != "" {
			if biome := nodeTool(biomeDir, "biome"); biome != "" {
				return biomeDir, []string{biome, "lint", "--reporter=github", "--max-diagnostics=50", path}, true
			}
		}
		oxConfigs := append(slices.Clone(oxlintConfigs), eslintConfigs...)
		if oxDir := findUp(dir, oxConfigs); oxDir != "" {
			if oxlint := nodeTool(oxDir, "oxlint"); oxlint != "" {
				return oxDir, []string{oxlint, "--format=unix", path}, true
			}
		}
		return "", nil, false
	},
	func(output, cwd string) []Diagnostic {
		var out []Diagnostic
		for _, line := range strings.Split(output, "\n") {
			trimmed := strings.TrimSpace(line)
			if m := githubLintRE.FindStringSubmatch(trimmed); m != nil {
				ln, _ := strconv.Atoi(m[4])
				col, _ := strconv.Atoi(m[5])
				out = append(out, diag(resolveAbs(cwd, m[3]), ln, col, m[6], m[2], SeverityWarning))
				continue
			}
			if m := unixLintRE.FindStringSubmatch(trimmed); m != nil {
				ln, _ := strconv.Atoi(m[2])
				col, _ := strconv.Atoi(m[3])
				out = append(out, diag(resolveAbs(cwd, m[1]), ln, col, m[4], m[5], SeverityWarning))
			}
		}
		return out
	},
)

var markdownChecker = makeLinter(
	"markdown",
	[]string{"md", "markdown"},
	func(path string) (string, []string, bool) {
		names := []string{
			".markdownlint.json", ".markdownlint.jsonc", ".markdownlint.yaml", ".markdownlint.yml",
			".markdownlint-cli2.jsonc", ".markdownlint-cli2.yaml", ".rumdl.toml", "rumdl.toml",
		}
		cwd := findUp(filepath.Dir(path), names)
		if cwd == "" && mentions(filepath.Dir(path), "pyproject.toml", rumdlPyRE) {
			cwd = findUp(filepath.Dir(path), []string{"pyproject.toml"})
		}
		if cwd == "" || OnPath("rumdl") == "" {
			return "", nil, false
		}
		return cwd, []string{"rumdl", "check", path}, true
	},
	func(output, cwd string) []Diagnostic {
		var out []Diagnostic
		for _, line := range strings.Split(output, "\n") {
			if m := rumdlLineRE.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
				ln, _ := strconv.Atoi(m[2])
				col, _ := strconv.Atoi(m[3])
				out = append(out, diag(resolveAbs(cwd, m[1]), ln, col, m[5], m[4], SeverityWarning))
			}
		}
		return out
	},
)

var yamlLintChecker = makeLinter(
	"yaml-lint",
	[]string{"yaml", "yml"},
	func(path string) (string, []string, bool) {
		cwd := findUp(filepath.Dir(path), []string{".yamllint", ".yamllint.yaml", ".yamllint.yml", ".ryl.toml"})
		if cwd == "" || OnPath("ryl") == "" {
			return "", nil, false
		}
		return cwd, []string{"ryl", "check", "--format", "parsable", path}, true
	},
	func(output, cwd string) []Diagnostic {
		var out []Diagnostic
		for _, line := range strings.Split(output, "\n") {
			if m := rylLineRE.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
				ln, _ := strconv.Atoi(m[2])
				col, _ := strconv.Atoi(m[3])
				out = append(out, diag(resolveAbs(cwd, m[1]), ln, col, m[4], m[5], SeverityWarning))
			}
		}
		return out
	},
)

var luauLintChecker = makeLinter(
	"luau-lint",
	[]string{"luau", "lua"},
	func(path string) (string, []string, bool) {
		cwd := findUp(filepath.Dir(path), []string{"selene.toml"})
		if cwd == "" || OnPath("selene") == "" {
			return "", nil, false
		}
		return cwd, []string{"selene", "--display-style", "quiet", path}, true
	},
	func(output, cwd string) []Diagnostic {
		var out []Diagnostic
		hasLuauAnalyze := OnPath("luau-analyze") != ""
		for _, d := range parseColonFindings(output, cwd, true) {
			if d.Code == "parse_error" && extLower(d.File) == "luau" && hasLuauAnalyze {
				continue
			}
			out = append(out, d)
		}
		return out
	},
)

var cppLintChecker = makeLinter(
	"cpp-lint",
	[]string{"c", "cc", "cpp", "cxx", "h", "hh", "hpp", "hip", "cu"},
	func(path string) (string, []string, bool) {
		cwd := findUp(filepath.Dir(path), []string{".clang-tidy"})
		db := findUp(filepath.Dir(path), []string{"compile_commands.json", filepath.Join("build", "compile_commands.json")})
		if cwd == "" || db == "" || OnPath("clangd-tidy") == "" {
			return "", nil, false
		}
		dbDir := db
		if !fileExists(filepath.Join(db, "compile_commands.json")) {
			dbDir = filepath.Join(db, "build")
		}
		return cwd, []string{"clangd-tidy", "-p", dbDir, path}, true
	},
	func(output, cwd string) []Diagnostic {
		var out []Diagnostic
		for _, line := range strings.Split(output, "\n") {
			if m := clangdTidyRE.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
				ln, _ := strconv.Atoi(m[2])
				col, _ := strconv.Atoi(m[3])
				out = append(out, diag(resolveAbs(cwd, m[1]), ln, col, m[4], m[5], SeverityWarning))
			}
		}
		return out
	},
)

// DefaultCheckers returns all built-in language checkers and linters, in the order of checkers.ts CHECKERS (with Go first), without the
// C/C++/HIP checker, which a session adds (SessionCheckers).
func DefaultCheckers() []Checker {
	return []Checker{
		goChecker,
		rustChecker,
		pythonChecker,
		typescriptChecker,
		javascriptChecker,
		shellChecker,
		luauChecker,
		jsonChecker,
		tomlChecker,
		yamlChecker,
		jsLintChecker,
		markdownChecker,
		yamlLintChecker,
		luauLintChecker,
		cppLintChecker,
		rdna4Checker,
	}
}

// EnabledCheckersFromEnv returns the checkers from DefaultCheckers allowed by PI_CHECKS.
func EnabledCheckersFromEnv() []Checker {
	val, _ := os.LookupEnv("PI_CHECKS")
	return EnabledCheckers(val, DefaultCheckers())
}

// SessionCheckers returns the checkers a session runs, as PI_CHECKS allows them: DefaultCheckers, then C, C++ and HIP (CppChecker) where
// clang is there, as agent-session.ts registers `[...CHECKERS, cpp]`. state is the session's file state, which finds the source a header is
// checked in.
//
// µPi: the C/C++/HIP check runs the clang binary (-fsyntax-only) instead of libclang through a native bridge.
func SessionCheckers(state *filetracking.FileStateTracker, workspaceCwd string) []Checker {
	all := DefaultCheckers()
	if cpp := CppChecker(state, workspaceCwd); cpp != nil {
		all = append(all, cpp)
	}
	val, _ := os.LookupEnv("PI_CHECKS")
	return EnabledCheckers(val, all)
}

// EnabledCheckers filters all by a PI_CHECKS setting:
// empty/"on" enables all; "off"/"0"/"false" disables all; comma-separated names act as an allowlist.
func EnabledCheckers(setting string, all []Checker) []Checker {
	trimmed := strings.TrimSpace(setting)
	if trimmed == "" || trimmed == "on" {
		return slices.Clone(all)
	}
	if trimmed == "off" || trimmed == "0" || trimmed == "false" {
		return nil
	}
	allowed := make(map[string]struct{})
	for _, part := range strings.Split(trimmed, ",") {
		if name := strings.TrimSpace(part); name != "" {
			allowed[name] = struct{}{}
		}
	}
	var out []Checker
	for _, c := range all {
		if _, ok := allowed[c.Name()]; ok {
			out = append(out, c)
		}
	}
	return out
}

// ShownPath returns path relative to cwd when it is under cwd, otherwise path.
func ShownPath(path, cwd string) string {
	if cwd == "" {
		return path
	}
	rel, err := filepath.Rel(cwd, path)
	if err != nil || rel == "" || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return path
	}
	return rel
}
