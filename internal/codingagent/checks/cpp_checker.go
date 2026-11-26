package checks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/internal/codingagent/filetracking"
)

var (
	cppSourceExts = map[string]bool{
		".cpp": true, ".cc": true, ".cxx": true, ".c": true, ".cu": true, ".hip": true,
	}
	cppHeaderExts = map[string]bool{
		".h": true, ".hh": true, ".hpp": true, ".hxx": true, ".cuh": true, ".hiph": true,
	}
	gccOnlyFlags = map[string]bool{
		"-fno-canonical-system-headers": true,
		"-Wno-maybe-uninitialized":      true,
		"-fext-numeric-literals":        true,
		"-frounding-math":               true,
		"-fabi-version=0":               true,
	}
	makefileIncludeRE = regexp.MustCompile(`(?:^|[\s=])-I\s*([^\s$()'"]+)`)
	clangDiagRE       = regexp.MustCompile(`^(.+?):(\d+):(\d+): (fatal error|error|warning|note): (.*?)(?: \[([^\]]+)\])?$`)
)

// IsCppOrHipSource reports whether filePath has a C/C++/CUDA/HIP source extension.
func IsCppOrHipSource(filePath string) bool {
	return cppSourceExts[strings.ToLower(filepath.Ext(filePath))]
}

// IsCppOrHipHeader reports whether filePath has a C/C++/CUDA/HIP header extension.
func IsCppOrHipHeader(filePath string) bool {
	return cppHeaderExts[strings.ToLower(filepath.Ext(filePath))]
}

// IsCppOrHipFile reports whether filePath is a C/C++/CUDA/HIP source or header file.
func IsCppOrHipFile(filePath string) bool {
	return IsCppOrHipSource(filePath) || IsCppOrHipHeader(filePath)
}

type compileCommandEntry struct {
	Directory string   `json:"directory"`
	File      string   `json:"file"`
	Arguments []string `json:"arguments"`
	Command   string   `json:"command"`
}

type cachedCompileDB struct {
	dbPath               string
	mtimeNano            int64
	lastStatCheck        time.Time
	entriesByFile        map[string]compileCommandEntry
	entriesByDir         map[string]compileCommandEntry
	sanitizedFlagsByFile map[string][]string
	firstHipEntry        *compileCommandEntry
	firstCppEntry        *compileCommandEntry
}

type cachedDirLookup struct {
	dbPath    string
	expiresAt time.Time
}

type cachedIncludes struct {
	flags     []string
	expiresAt time.Time
}

const dbLookupCacheTTL = 5 * time.Second

func getRocmInfo() (rocmPath, rocmInclude string) {
	if envPath := os.Getenv("ROCM_PATH"); envPath != "" {
		inc := filepath.Join(envPath, "include")
		if dirExists(inc) {
			return envPath, inc
		}
		return envPath, ""
	}
	if envPath := os.Getenv("HIP_PATH"); envPath != "" {
		inc := filepath.Join(envPath, "include")
		if dirExists(inc) {
			return envPath, inc
		}
		return envPath, ""
	}
	if dirExists("/opt/rocm") {
		if dirExists("/opt/rocm/include") {
			return "/opt/rocm", "/opt/rocm/include"
		}
		return "/opt/rocm", ""
	}
	return "", ""
}

func splitCompileCommand(command string) []string {
	var args []string
	var cur strings.Builder
	var quote byte
	for i := 0; i < len(command); i++ {
		c := command[i]
		if quote != 0 {
			if c == '\\' && i+1 < len(command) && quote == '"' {
				i++
				cur.WriteByte(command[i])
			} else if c == quote {
				quote = 0
			} else {
				cur.WriteByte(c)
			}
		} else if c == '"' || c == '\'' {
			quote = c
		} else if c == ' ' || c == '\t' {
			if cur.Len() > 0 {
				args = append(args, cur.String())
				cur.Reset()
			}
		} else if c == '\\' && i+1 < len(command) && runtime.GOOS != "windows" {
			i++
			cur.WriteByte(command[i])
		} else {
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 {
		args = append(args, cur.String())
	}
	return args
}

// SanitizeCompileFlags strips driver/output/dependency flags and normalizes language, standard,
// HIP offload-arch, and include paths for clang -fsyntax-only.
func SanitizeCompileFlags(rawArgs []string, targetFilePath, workingDir, contentHint string) []string {
	var result []string
	targetNorm := filetracking.PathKey(targetFilePath)
	ext := strings.ToLower(filepath.Ext(targetFilePath))
	isHipByExt := ext == ".hip" || ext == ".cuh" || ext == ".hiph"
	isHipByContent := !isHipByExt && contentHint != "" &&
		(strings.Contains(contentHint, "<hip/") ||
			strings.Contains(contentHint, "__global__") ||
			strings.Contains(contentHint, "hipLaunchKernelGGL"))

	seenOffloadArch := false
	hasExplicitLanguage := false
	hasStdFlag := false
	hasCStdFlag := false
	entrySourceIsC := false

	startIndex := 0
	if len(rawArgs) > 0 {
		first := strings.ToLower(rawArgs[0])
		if !strings.HasPrefix(first, "-") &&
			(strings.Contains(first, "clang") ||
				strings.Contains(first, "gcc") ||
				strings.Contains(first, "g++") ||
				strings.Contains(first, "c++") ||
				strings.Contains(first, "cc") ||
				strings.Contains(first, "hipcc") ||
				strings.Contains(first, "nvcc") ||
				strings.HasSuffix(first, ".exe")) {
			startIndex = 1
		}
	}

	for i := startIndex; i < len(rawArgs); i++ {
		arg := rawArgs[i]
		if arg == "" {
			continue
		}
		if arg == "-o" || arg == "-MF" || arg == "-MT" || arg == "-MQ" {
			i++
			continue
		}
		if arg == "-c" || arg == "-MD" || arg == "-MMD" || arg == "-MP" ||
			strings.HasPrefix(arg, "-o") || strings.HasPrefix(arg, "-MF") || strings.HasPrefix(arg, "-MT") {
			continue
		}
		if gccOnlyFlags[arg] || strings.HasPrefix(arg, "-fconcepts-diagnostics-depth=") {
			continue
		}
		if strings.HasPrefix(arg, "--offload-arch=") || strings.HasPrefix(arg, "--cuda-gpu-arch=") {
			if seenOffloadArch {
				continue
			}
			seenOffloadArch = true
			result = append(result, arg)
			continue
		}
		if arg == "--offload-arch" || arg == "--cuda-gpu-arch" {
			var nextArch string
			if i+1 < len(rawArgs) {
				nextArch = rawArgs[i+1]
			}
			i++
			if seenOffloadArch || nextArch == "" {
				continue
			}
			seenOffloadArch = true
			result = append(result, arg+"="+nextArch)
			continue
		}
		if arg == "-x" && i+1 < len(rawArgs) {
			hasExplicitLanguage = true
			i++
			result = append(result, arg, rawArgs[i])
			continue
		}
		if strings.HasPrefix(arg, "-std=") {
			hasStdFlag = true
			hasCStdFlag = !strings.Contains(arg, "++")
		}
		if !strings.HasPrefix(arg, "-") {
			resolvedArg := arg
			if workingDir != "" {
				resolvedArg = resolveAbs(workingDir, arg)
			} else if abs, err := filepath.Abs(arg); err == nil {
				resolvedArg = abs
			}
			if filetracking.PathKey(resolvedArg) == targetNorm {
				continue
			}
			argExt := strings.ToLower(filepath.Ext(arg))
			if argExt == ".c" {
				entrySourceIsC = true
			}
			if cppSourceExts[argExt] {
				continue
			}
		}
		result = append(result, arg)
	}

	isHip := isHipByExt || isHipByContent || seenOffloadArch
	isHeader := IsCppOrHipHeader(targetFilePath)
	isC := !isHip && (ext == ".c" || (isHeader && (hasCStdFlag || entrySourceIsC)))

	if !hasExplicitLanguage {
		var lang string
		switch {
		case isHip && isHeader:
			lang = "hip-header"
		case isHip:
			lang = "hip"
		case isC && isHeader:
			lang = "c-header"
		case isC:
			lang = "c"
		case isHeader:
			lang = "c++-header"
		default:
			lang = "c++"
		}
		result = append([]string{"-x", lang}, result...)
	}

	if !hasStdFlag && !isC {
		result = append(result, "-std=c++20")
	}

	if isHip {
		if !seenOffloadArch {
			defArch := os.Getenv("HCC_AMDGPU_TARGET")
			if defArch == "" {
				defArch = os.Getenv("HIP_OFFLOAD_ARCH")
			}
			if defArch == "" {
				defArch = "gfx1100"
			}
			firstArch := strings.FieldsFunc(defArch, func(r rune) bool { return r == ',' || r == ';' })[0]
			result = append(result, "--offload-arch="+strings.TrimSpace(firstArch))
		}
		rocmPath, rocmInclude := getRocmInfo()
		if rocmPath != "" && !slices.ContainsFunc(result, func(a string) bool { return strings.HasPrefix(a, "--rocm-path=") }) {
			result = append(result, "--rocm-path="+rocmPath)
		}
		if rocmInclude != "" && !slices.Contains(result, "-I"+rocmInclude) {
			result = append(result, "-I"+rocmInclude)
		}
	}

	absTarget, err := filepath.Abs(targetFilePath)
	if err != nil {
		absTarget = targetFilePath
	}
	fileDir := filepath.Dir(absTarget)
	if !slices.Contains(result, "-I"+fileDir) {
		result = append(result, "-I"+fileDir)
	}
	if !slices.ContainsFunc(result, func(a string) bool { return strings.HasPrefix(a, "-ferror-limit=") }) {
		result = append(result, "-ferror-limit=20")
	}
	return result
}

func projectIncludeFlags(workspaceCwd string) []string {
	seen := make(map[string]struct{})
	var dirs []string
	addDir := func(d string) {
		if _, ok := seen[d]; ok {
			return
		}
		seen[d] = struct{}{}
		dirs = append(dirs, d)
	}

	for _, name := range []string{"Makefile", "makefile", "GNUmakefile"} {
		p := filepath.Join(workspaceCwd, name)
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, m := range makefileIncludeRE.FindAllStringSubmatch(string(data), -1) {
			if len(m) >= 2 {
				d := resolveAbs(workspaceCwd, m[1])
				if pathExists(d) {
					addDir(d)
				}
			}
		}
	}
	if len(dirs) == 0 {
		for _, name := range []string{"include", "inc"} {
			d := filepath.Join(workspaceCwd, name)
			if dirExists(d) {
				addDir(d)
			}
		}
	}
	flags := make([]string, len(dirs))
	for i, d := range dirs {
		flags[i] = "-I" + d
	}
	return flags
}

// CompileCommandsResolver locates compile_commands.json and resolves sanitized flags per file.
type CompileCommandsResolver struct {
	mu              sync.Mutex
	projectIncludes map[string]cachedIncludes
	cache           map[string]*cachedCompileDB
	dirLookupCache  map[string]cachedDirLookup
}

// NewCompileCommandsResolver creates a CompileCommandsResolver.
func NewCompileCommandsResolver() *CompileCommandsResolver {
	return &CompileCommandsResolver{
		projectIncludes: make(map[string]cachedIncludes),
		cache:           make(map[string]*cachedCompileDB),
		dirLookupCache:  make(map[string]cachedDirLookup),
	}
}

// GetFlagsForFile returns sanitized clang flags for filePath within workspaceCwd.
func (r *CompileCommandsResolver) GetFlagsForFile(filePath, workspaceCwd, contentHint string) []string {
	absPath := resolveAbs(workspaceCwd, filePath)
	exactKey := filetracking.PathKey(absPath)

	r.mu.Lock()
	defer r.mu.Unlock()

	if db := r.findAndLoadCompileDBLocked(absPath, workspaceCwd); db != nil {
		if cached, ok := db.sanitizedFlagsByFile[exactKey]; ok {
			return slices.Clone(cached)
		}
		entry, ok := db.entriesByFile[exactKey]
		if !ok {
			dirKey := filetracking.PathKey(filepath.Dir(absPath))
			if dirEntry, found := db.entriesByDir[dirKey]; found {
				entry = dirEntry
				ok = true
			} else {
				ext := strings.ToLower(filepath.Ext(absPath))
				if ext == ".hip" || ext == ".cuh" {
					if db.firstHipEntry != nil {
						entry = *db.firstHipEntry
						ok = true
					} else if db.firstCppEntry != nil {
						entry = *db.firstCppEntry
						ok = true
					}
				} else {
					if db.firstCppEntry != nil {
						entry = *db.firstCppEntry
						ok = true
					} else if db.firstHipEntry != nil {
						entry = *db.firstHipEntry
						ok = true
					}
				}
			}
		}
		if ok {
			rawArgs := entry.Arguments
			if len(rawArgs) == 0 && entry.Command != "" {
				rawArgs = splitCompileCommand(entry.Command)
			}
			sanitized := SanitizeCompileFlags(rawArgs, absPath, entry.Directory, contentHint)
			if entry.Directory != "" && !slices.ContainsFunc(sanitized, func(a string) bool { return strings.HasPrefix(a, "-working-directory=") }) {
				sanitized = append([]string{"-working-directory=" + entry.Directory}, sanitized...)
			}
			db.sanitizedFlagsByFile[exactKey] = sanitized
			return slices.Clone(sanitized)
		}
	}

	now := time.Now()
	inc, ok := r.projectIncludes[workspaceCwd]
	if !ok || !inc.expiresAt.After(now) {
		inc = cachedIncludes{
			flags:     projectIncludeFlags(workspaceCwd),
			expiresAt: now.Add(dbLookupCacheTTL),
		}
		r.projectIncludes[workspaceCwd] = inc
	}
	return SanitizeCompileFlags(inc.flags, absPath, workspaceCwd, contentHint)
}

func (r *CompileCommandsResolver) findAndLoadCompileDBLocked(filePath, workspaceCwd string) *cachedCompileDB {
	now := time.Now()
	dirKey := filetracking.PathKey(filepath.Dir(filePath))
	if cached, ok := r.dirLookupCache[dirKey]; ok && cached.expiresAt.After(now) {
		if cached.dbPath == "" {
			return nil
		}
		return r.loadCompileDBLocked(cached.dbPath, now)
	}

	var candidateDirs []string
	cur := filepath.Dir(filePath)
	for depth := 0; depth < 5; depth++ {
		candidateDirs = append(candidateDirs, cur)
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	resolvedWS, err := filepath.Abs(workspaceCwd)
	if err != nil {
		resolvedWS = workspaceCwd
	}
	if !slices.Contains(candidateDirs, resolvedWS) {
		candidateDirs = append([]string{resolvedWS}, candidateDirs...)
	}

	subdirs := []string{"", "build", "out", "cmake-build-release", "cmake-build-debug"}
	for _, baseDir := range candidateDirs {
		for _, sub := range subdirs {
			var cand string
			if sub == "" {
				cand = filepath.Join(baseDir, "compile_commands.json")
			} else {
				cand = filepath.Join(baseDir, sub, "compile_commands.json")
			}
			if fileExists(cand) {
				r.dirLookupCache[dirKey] = cachedDirLookup{dbPath: cand, expiresAt: now.Add(dbLookupCacheTTL)}
				return r.loadCompileDBLocked(cand, now)
			}
		}
	}
	r.dirLookupCache[dirKey] = cachedDirLookup{dbPath: "", expiresAt: now.Add(dbLookupCacheTTL)}
	return nil
}

func (r *CompileCommandsResolver) loadCompileDBLocked(dbPath string, now time.Time) *cachedCompileDB {
	cached := r.cache[dbPath]
	if cached != nil && now.Sub(cached.lastStatCheck) < dbLookupCacheTTL {
		return cached
	}
	info, err := os.Stat(dbPath)
	if err != nil {
		return nil
	}
	mtimeNano := info.ModTime().UnixNano()
	if cached != nil && cached.mtimeNano == mtimeNano {
		cached.lastStatCheck = now
		return cached
	}
	raw, err := os.ReadFile(dbPath)
	if err != nil {
		return nil
	}
	var entries []compileCommandEntry
	if json.Unmarshal(raw, &entries) != nil {
		return nil
	}

	db := &cachedCompileDB{
		dbPath:               dbPath,
		mtimeNano:            mtimeNano,
		lastStatCheck:        now,
		entriesByFile:        make(map[string]compileCommandEntry),
		entriesByDir:         make(map[string]compileCommandEntry),
		sanitizedFlagsByFile: make(map[string][]string),
	}
	for _, entry := range entries {
		if entry.File == "" {
			continue
		}
		baseDir := entry.Directory
		if baseDir == "" {
			baseDir = filepath.Dir(dbPath)
		}
		absFile := resolveAbs(baseDir, entry.File)
		fileKey := filetracking.PathKey(absFile)
		dirKey := filetracking.PathKey(filepath.Dir(absFile))

		db.entriesByFile[fileKey] = entry
		if _, ok := db.entriesByDir[dirKey]; !ok {
			db.entriesByDir[dirKey] = entry
		}
		ext := strings.ToLower(filepath.Ext(absFile))
		eCopy := entry
		if db.firstHipEntry == nil && (ext == ".hip" || ext == ".cu" || strings.Contains(entry.Command, "hip")) {
			db.firstHipEntry = &eCopy
		} else if db.firstCppEntry == nil {
			db.firstCppEntry = &eCopy
		}
	}
	r.cache[dbPath] = db
	return db
}

func includeDirsFromFlags(flags []string, baseDir string) []string {
	base := baseDir
	for _, a := range flags {
		if strings.HasPrefix(a, "-working-directory=") {
			base = resolveAbs(baseDir, strings.TrimPrefix(a, "-working-directory="))
			break
		}
	}
	var dirs []string
	for i := 0; i < len(flags); i++ {
		for _, flag := range []string{"-I", "-iquote", "-isystem"} {
			if flags[i] == flag && i+1 < len(flags) {
				i++
				dirs = append(dirs, resolveAbs(base, flags[i]))
				break
			}
			if strings.HasPrefix(flags[i], flag) && len(flags[i]) > len(flag) {
				dirs = append(dirs, resolveAbs(base, flags[i][len(flag):]))
				break
			}
		}
	}
	return dirs
}

func clangBinary() string {
	if p := OnPath("clang++"); p != "" {
		return p
	}
	return OnPath("clang")
}

// CppChecker returns a CGO-free C/C++/HIP Checker backed by external clang++/clang -fsyntax-only
// and CompileCommandsResolver. Returns nil if no clang binary is on PATH.
func CppChecker(stateTracker *filetracking.FileStateTracker, workspaceCwd string) Checker {
	if clangBinary() == "" {
		return nil
	}
	resolver := NewCompileCommandsResolver()
	return &cppSyntaxChecker{
		stateTracker: stateTracker,
		workspaceCwd: workspaceCwd,
		resolver:     resolver,
	}
}

type cppSyntaxChecker struct {
	stateTracker *filetracking.FileStateTracker
	workspaceCwd string
	resolver     *CompileCommandsResolver
}

func (c *cppSyntaxChecker) Name() string             { return "cpp" }
func (c *cppSyntaxChecker) Matches(path string) bool { return IsCppOrHipFile(path) }

func (c *cppSyntaxChecker) Unit(path string) *CheckUnit {
	if clangBinary() == "" {
		return nil
	}
	return &CheckUnit{
		Key:     "cpp:" + path,
		Checker: c,
		CWD:     c.workspaceCwd,
		File:    path,
	}
}

func (c *cppSyntaxChecker) resolveTranslationUnit(resolvedTarget string) string {
	if !IsCppOrHipHeader(resolvedTarget) || c.stateTracker == nil {
		return resolvedTarget
	}
	if direct, ok := c.stateTracker.FindHostSourceForHeader(resolvedTarget); ok {
		if st, ok := c.stateTracker.GetTrackedState(direct); ok && st.ContentComplete {
			return direct
		}
	}
	targetKey := filetracking.PathKey(resolvedTarget)
	for _, m := range c.stateTracker.FindSourcesIncludingByPath(resolvedTarget) {
		st, ok := c.stateTracker.GetTrackedState(m.Source)
		if !ok || !st.ContentComplete {
			continue
		}
		flags := c.resolver.GetFlagsForFile(m.Source, c.workspaceCwd, st.Content)
		for _, dir := range includeDirsFromFlags(flags, filepath.Dir(m.Source)) {
			if filetracking.PathKey(filepath.Join(dir, m.Include)) == targetKey {
				return m.Source
			}
		}
	}
	return resolvedTarget
}

func (c *cppSyntaxChecker) Run(ctx context.Context, unit *CheckUnit) ([]Diagnostic, error) {
	bin := clangBinary()
	if bin == "" {
		return nil, nil
	}
	resolvedTarget := resolveAbs(c.workspaceCwd, unit.File)
	targetKey := filetracking.PathKey(resolvedTarget)
	tuFilePath := c.resolveTranslationUnit(resolvedTarget)

	var contentHint string
	if data, err := os.ReadFile(tuFilePath); err == nil {
		contentHint = string(data)
	}
	flags := c.resolver.GetFlagsForFile(tuFilePath, c.workspaceCwd, contentHint)

	// Remove -working-directory=... from CLI args and pass it as cmd.Dir instead.
	cmdDir := c.workspaceCwd
	var cliFlags []string
	for _, f := range flags {
		if strings.HasPrefix(f, "-working-directory=") {
			cmdDir = strings.TrimPrefix(f, "-working-directory=")
			continue
		}
		cliFlags = append(cliFlags, f)
	}

	argv := append([]string{bin, "-fsyntax-only", "-fno-caret-diagnostics", "-fno-color-diagnostics"}, cliFlags...)
	argv = append(argv, tuFilePath)

	ran, err := runCommand(ctx, argv, cmdDir, map[string]string{"LC_ALL": "C"}, 0)
	if err != nil {
		return nil, err
	}

	var relevant []Diagnostic
	var missing []Diagnostic
	for _, line := range strings.Split(ran.stderr+"\n"+ran.stdout, "\n") {
		m := clangDiagRE.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		if m[4] == "note" {
			continue
		}
		diagFile := resolveAbs(cmdDir, m[1])
		if filetracking.PathKey(diagFile) != targetKey {
			continue
		}
		ln, _ := strconv.Atoi(m[2])
		col, _ := strconv.Atoi(m[3])
		sev := SeverityWarning
		if m[4] == "error" || m[4] == "fatal error" {
			sev = SeverityError
		}
		d := diag(diagFile, ln, col, m[5], m[6], sev)
		relevant = append(relevant, d)
		if strings.HasSuffix(d.Message, "file not found") {
			missing = append(missing, d)
		}
	}
	if len(missing) > 0 {
		return missing, nil
	}
	return relevant, nil
}
