package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// runDoctor writes a Go-native diagnostic report for `upi doctor [--offline]`.
func runDoctor(w io.Writer, binaryPath string, args []string) {
	hr := strings.Repeat("─", 60)
	offline := os.Getenv("PI_OFFLINE") != "" || os.Getenv("MICROPI_OFFLINE") == "1"
	for _, arg := range args {
		if arg == "--offline" {
			offline = true
		}
	}

	// ─── µPi & Build Info ────────────────────────────────
	_, _ = fmt.Fprintf(w, "µPi doctor\n%s\n", hr)
	_, _ = fmt.Fprintf(w, "upi-version:       µPi %s\n", MicroPiVersion)
	_, _ = fmt.Fprintf(w, "pig-version:       %s\n", Version)
	_, _ = fmt.Fprintf(w, "build:             %s\n", buildIdentity())
	_, _ = fmt.Fprintf(w, "go runtime:        %s\n", runtime.Version())
	_, _ = fmt.Fprintf(w, "platform:          %s/%s\n", runtime.GOOS, runtime.GOARCH)
	_, _ = fmt.Fprintf(w, "binary:            %s\n", binaryPath)
	_, _ = fmt.Fprintf(w, "offline:           %t\n", offline)

	// ─── System & Go Tools ───────────────────────────────
	_, _ = fmt.Fprintf(w, "\ntools\n%s\n", hr)
	for _, tool := range []struct {
		name string
		args []string
	}{
		{name: "git", args: []string{"--version"}},
		{name: "rg", args: []string{"--version"}},
		{name: "ssh", args: []string{"-V"}},
		{name: "go", args: []string{"version"}},
	} {
		path, version := probeTool(tool.name, tool.args...)
		if path == "" {
			_, _ = fmt.Fprintf(w, "  %-10s not found\n", tool.name+":")
			continue
		}
		if version != "" {
			_, _ = fmt.Fprintf(w, "  %-10s %s (%s)\n", tool.name+":", version, path)
		} else {
			_, _ = fmt.Fprintf(w, "  %-10s %s\n", tool.name+":", path)
		}
	}

	// ─── Config & Paths ──────────────────────────────────
	_, _ = fmt.Fprintf(w, "\nconfig & paths\n%s\n", hr)
	configRoot := codingagent.ConfigRoot()
	agentDir := agentDirForModel()
	_, _ = fmt.Fprintf(w, "config root:       %s (%s)\n", configRoot, checkDirWritable(configRoot))
	_, _ = fmt.Fprintf(w, "agent dir:         %s (%s)\n", agentDir, checkDirWritable(agentDir))
	_, _ = fmt.Fprintf(w, "auth.json:         %s\n", filepath.Join(agentDir, "auth.json"))
	_, _ = fmt.Fprintf(w, "settings.json:     %s\n", filepath.Join(agentDir, "settings.json"))
	workDir, _ := os.Getwd()
	if workDir != "" {
		_, _ = fmt.Fprintf(w, "cwd:               %s\n", workDir)
	}
	trust := reportProjectTrust(workDir, agentDir)
	_, _ = fmt.Fprintf(w, "project trust:     %s (%s)\n", trust.State, trust.Reason)

	// ─── Auth Providers ──────────────────────────────────
	_, _ = fmt.Fprintf(w, "\nauth (logged-in providers)\n%s\n", hr)
	authPath := filepath.Join(agentDir, "auth.json")
	if _, err := os.Stat(authPath); err != nil {
		_, _ = fmt.Fprintf(w, "  (no auth.json: run `upi login`)\n")
	} else if storage, err := ai.NewAuthStorage(authPath); err != nil {
		_, _ = fmt.Fprintf(w, "  error opening auth: %v\n", err)
	} else if creds, err := storage.Load(); err != nil {
		_, _ = fmt.Fprintf(w, "  error loading auth: %v\n", err)
	} else if len(creds) == 0 {
		_, _ = fmt.Fprintf(w, "  (none)\n")
	} else {
		providers := make([]string, 0, len(creds))
		for p := range creds {
			providers = append(providers, p)
		}
		slices.Sort(providers)
		for _, p := range providers {
			_, _ = fmt.Fprintf(w, "  %s: %s\n", p, credentialKindLabel(creds[p]))
		}
	}

	// ─── Model Providers & Default Model ─────────────────
	_, _ = fmt.Fprintf(w, "\nmodels (registered providers)\n%s\n", hr)
	provs := map[string]int{}
	for _, m := range ai.ListModels("") {
		provs[m.Provider]++
	}
	provNames := make([]string, 0, len(provs))
	for p := range provs {
		provNames = append(provNames, p)
	}
	slices.Sort(provNames)
	for _, p := range provNames {
		_, _ = fmt.Fprintf(w, "  %s: %d models\n", p, provs[p])
	}
	if len(provNames) == 0 {
		_, _ = fmt.Fprintf(w, "  (registry empty)\n")
	}

	_, _ = fmt.Fprintf(w, "\ndefault model\n%s\n", hr)
	active := inspectActivePiglet(workDir)
	writeDiagnoseDefaultModel(w, workDir, agentDir, trust, active)

	// ─── MCP Status ──────────────────────────────────────
	_, _ = fmt.Fprintf(w, "\nmcp\n%s\n", hr)
	mcpCfg := mcpext.LoadMcpConfig(mcpext.LoadOptions{
		AgentDir:       agentDir,
		Cwd:            workDir,
		ProjectTrusted: trust.State == "trusted",
		ConfigDirName:  codingagent.ConfigDirName(),
	})
	_, _ = fmt.Fprintf(w, "  servers:         %d\n", len(mcpCfg.Servers))
	for _, srv := range mcpCfg.Servers {
		_, _ = fmt.Fprintf(w, "  - %s (scope=%s)\n", srv.Name, srv.Scope)
	}
	for _, errMsg := range mcpCfg.Errors {
		_, _ = fmt.Fprintf(w, "  error:           %s\n", errMsg)
	}

	// ─── Herdr Status ────────────────────────────────────
	_, _ = fmt.Fprintf(w, "\nherdr\n%s\n", hr)
	termProgram := os.Getenv("TERM_PROGRAM")
	herdrEnv := os.Getenv("HERDR_ENV")
	herdrKitty := os.Getenv("HERDR_KITTY_GRAPHICS")
	herdrAgent := os.Getenv("HERDR_AGENT")
	inHerdr := strings.EqualFold(termProgram, "herdr") || herdrEnv != ""
	_, _ = fmt.Fprintf(w, "  active:          %t\n", inHerdr)
	if termProgram != "" {
		_, _ = fmt.Fprintf(w, "  TERM_PROGRAM:    %s\n", termProgram)
	}
	if herdrEnv != "" {
		_, _ = fmt.Fprintf(w, "  HERDR_ENV:       %s\n", herdrEnv)
	}
	if herdrKitty != "" {
		_, _ = fmt.Fprintf(w, "  HERDR_KITTY_GRAPHICS: %s\n", herdrKitty)
	}
	if herdrAgent != "" {
		_, _ = fmt.Fprintf(w, "  HERDR_AGENT:     %s\n", herdrAgent)
	}

	// ─── Redacted Environment ────────────────────────────
	_, _ = fmt.Fprintf(w, "\nenvironment\n%s\n", hr)
	for _, k := range []string{
		"MICROPI_HOME", "MICROPI_CODING_AGENT_DIR", "MICROPI_USE_PI_DIRS",
		"MICROPI_UPDATE_URL", "MICROPI_CHANNEL",
		"PIG_HOME", "PIG_CODING_AGENT_DIR", "PIG_USE_PI_DIRS", "PIG_UPDATE_URL",
		"PI_CODING_AGENT_DIR", "PI_OFFLINE", "PI_SKIP_VERSION_CHECK",
		"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY",
		"GROQ_API_KEY", "CEREBRAS_API_KEY", "GITHUB_TOKEN",
		"PATH", "SHELL", "TERM", "COLORTERM", "NO_COLOR",
		"http_proxy", "https_proxy", "HTTP_PROXY", "HTTPS_PROXY",
	} {
		v := os.Getenv(k)
		if v == "" {
			continue
		}
		_, _ = fmt.Fprintf(w, "  %-26s %s\n", k+":", maskEnv(k, v))
	}
}

func probeTool(name string, args ...string) (string, string) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) == 0 {
		return path, ""
	}
	firstLine := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	return path, firstLine
}

func checkDirWritable(dir string) string {
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "not created yet"
		}
		return fmt.Sprintf("error: %v", err)
	}
	if !info.IsDir() {
		return "not a directory"
	}
	probe, err := os.CreateTemp(dir, ".upi-doctor-*")
	if err != nil {
		return fmt.Sprintf("not writable: %v", err)
	}
	_ = probe.Close()
	_ = os.Remove(probe.Name())
	return "writable"
}
