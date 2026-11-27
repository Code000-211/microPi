package docsearch

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
)

// Ports microPi packages/docsearch/extension/index.ts (Config, loadConfig, saveConfig, expand).

// Settings is ~/.pi/agent/docsearch.json (the µPi agent directory's docsearch.json).
type Settings struct {
	// Project indexes the project Pi runs in (its git root, else the working directory).
	Project bool `json:"project"`
	// Roots are more folders, indexed in every project. `~` is expanded.
	Roots []string `json:"roots"`
	// Code indexes source code as well as docs.
	Code bool `json:"code"`
	// Extensions are the file extensions to index, replacing the defaults (DocExtensions, plus CodeExtensions with Code).
	Extensions []string `json:"extensions,omitempty"`
	// ResultChars are the characters of text shown for the best results, in rank order; the results after them come
	// as `path:lines · heading` only.
	ResultChars []int `json:"resultChars"`
	// Results is how many results come in all, shown in full or as pointers (the tool's `k` overrides it).
	Results int `json:"results"`
	// AutoRecall adds the best-matching doc sections to a typed prompt before the model runs.
	AutoRecall      bool `json:"autoRecall"`
	AutoRecallHits  int  `json:"autoRecallHits"`
	AutoRecallChars int  `json:"autoRecallChars"`
	// AutoRecallMinCoverage is the share of the prompt's terms, weighted by rarity, a section must contain to be added...
	AutoRecallMinCoverage float64 `json:"autoRecallMinCoverage"`
	// ...or AutoRecallNamedCoverage, when the section mentions a name the prompt names.
	AutoRecallNamedCoverage float64 `json:"autoRecallNamedCoverage"`
}

// DocExtensions and CodeExtensions are the extensions indexed by default.
var (
	DocExtensions  = []string{"md", "mdx", "markdown", "txt", "rst", "adoc", "org"}
	CodeExtensions = []string{"c", "cc", "cpp", "cxx", "h", "hh", "hpp", "hip", "cu", "cuh", "rs", "py", "go", "sh", "bash", "ts", "tsx", "mts", "cts", "js", "jsx", "mjs", "cjs"}
)

// DefaultSettings are the settings without a docsearch.json.
func DefaultSettings() Settings {
	return Settings{
		Project:                 true,
		Roots:                   []string{},
		Code:                    true,
		ResultChars:             []int{1500, 1000, 600},
		Results:                 6,
		AutoRecall:              true,
		AutoRecallHits:          3,
		AutoRecallChars:         2400,
		AutoRecallMinCoverage:   0.85,
		AutoRecallNamedCoverage: 0.3,
	}
}

// IndexedExtensions are the extensions the index takes: Extensions, else the doc extensions plus the code ones with
// Code.
func (s Settings) IndexedExtensions() []string {
	if s.Extensions != nil {
		return s.Extensions
	}
	if s.Code {
		return append(append([]string{}, DocExtensions...), CodeExtensions...)
	}
	return append([]string{}, DocExtensions...)
}

var agentDirFunc atomic.Pointer[func() (string, error)]

// SetAgentDir names the agent directory docsearch.json lives in (index.ts getAgentDir()); the coding agent sets it.
func SetAgentDir(dir func() (string, error)) { agentDirFunc.Store(&dir) }

// SettingsPath returns the path to docsearch.json.
func SettingsPath() string {
	if dir := agentDirFunc.Load(); dir != nil {
		if resolved, err := (*dir)(); err == nil && resolved != "" {
			return filepath.Join(resolved, "docsearch.json")
		}
	}
	if dir := os.Getenv("PI_CODING_AGENT_DIR"); dir != "" {
		return filepath.Join(dir, "docsearch.json")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".", ".pi", "agent", "docsearch.json")
	}
	return filepath.Join(home, ".pi", "agent", "docsearch.json")
}

// LoadSettings reads docsearch.json over the defaults (`{ ...DEFAULTS, ...JSON.parse(file) }`); a broken file gives
// the defaults.
func LoadSettings() Settings {
	settings := DefaultSettings()
	data, err := os.ReadFile(SettingsPath())
	if err != nil {
		return settings
	}
	if json.Unmarshal(data, &settings) != nil {
		return DefaultSettings()
	}
	if settings.Roots == nil {
		settings.Roots = []string{}
	}
	return settings
}

// SaveSettingsPatch writes the patch's members over the file's (`{ ...current, ...patch }`), tab-indented, keeping
// the file's member order.
func SaveSettingsPatch(patch map[string]any) error {
	path := SettingsPath()
	type member struct {
		key   string
		value json.RawMessage
	}
	var members []member
	if data, err := os.ReadFile(path); err == nil {
		decoder := json.NewDecoder(bytes.NewReader(data))
		if token, err := decoder.Token(); err == nil && token == json.Delim('{') {
			for decoder.More() {
				key, err := decoder.Token()
				var value json.RawMessage
				if err != nil || decoder.Decode(&value) != nil {
					members = nil
					break
				}
				members = append(members, member{key.(string), value})
			}
		}
	}
	for key, value := range patch {
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		replaced := false
		for i := range members {
			if members[i].key == key {
				members[i].value, replaced = encoded, true
			}
		}
		if !replaced {
			members = append(members, member{key, encoded})
		}
	}
	var out bytes.Buffer
	out.WriteString("{")
	for i, m := range members {
		if i > 0 {
			out.WriteString(",")
		}
		key, _ := json.Marshal(m.key)
		out.WriteString("\n\t")
		out.Write(key)
		out.WriteString(": ")
		var indented bytes.Buffer
		if json.Indent(&indented, m.value, "\t", "\t") != nil {
			indented.Reset()
			indented.Write(m.value)
		}
		out.Write(indented.Bytes())
	}
	if len(members) > 0 {
		out.WriteString("\n")
	}
	out.WriteString("}\n")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, out.Bytes(), 0o644)
}

// expandHome is index.ts expand: `~` and `~/...` start at the home directory.
func expandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, p[1:])
}
