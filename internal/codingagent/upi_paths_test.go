package codingagent_test

import (
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestResolveDefaultAgentDirPrecedence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("MICROPI_HOME", "")
	t.Setenv("PIG_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("MICROPI_USE_PI_DIRS", "")
	t.Setenv("PIG_USE_PI_DIRS", "")
	t.Setenv("MICROPI_CODING_AGENT_DIR", "")
	t.Setenv("PIG_CODING_AGENT_DIR", "")
	t.Setenv("PI_CODING_AGENT_DIR", "")

	// Default: ~/.micropi/agent
	got, err := codingagent.ResolveDefaultAgentDir()
	if err != nil {
		t.Fatalf("ResolveDefaultAgentDir() error: %v", err)
	}
	if want := filepath.Join(home, ".micropi", "agent"); got != want {
		t.Fatalf("default agent dir = %q, want %q", got, want)
	}

	// PIG_CODING_AGENT_DIR overrides default
	t.Setenv("PIG_CODING_AGENT_DIR", "~/pig-agent")
	got, err = codingagent.ResolveDefaultAgentDir()
	if err != nil {
		t.Fatalf("ResolveDefaultAgentDir() error: %v", err)
	}
	if want := filepath.Join(home, "pig-agent"); got != want {
		t.Fatalf("PIG_CODING_AGENT_DIR = %q, want %q", got, want)
	}

	// MICROPI_CODING_AGENT_DIR beats PIG_CODING_AGENT_DIR
	t.Setenv("MICROPI_CODING_AGENT_DIR", "~/micropi-agent")
	got, err = codingagent.ResolveDefaultAgentDir()
	if err != nil {
		t.Fatalf("ResolveDefaultAgentDir() error: %v", err)
	}
	if want := filepath.Join(home, "micropi-agent"); got != want {
		t.Fatalf("MICROPI_CODING_AGENT_DIR = %q, want %q", got, want)
	}

	// MICROPI_USE_PI_DIRS=1 uses ~/.pi/agent and PI_CODING_AGENT_DIR
	t.Setenv("MICROPI_USE_PI_DIRS", "1")
	got, err = codingagent.ResolveDefaultAgentDir()
	if err != nil {
		t.Fatalf("ResolveDefaultAgentDir() error: %v", err)
	}
	if want := filepath.Join(home, ".pi", "agent"); got != want {
		t.Fatalf("MICROPI_USE_PI_DIRS=1 = %q, want %q", got, want)
	}

	t.Setenv("PI_CODING_AGENT_DIR", "~/custom-pi-agent")
	got, err = codingagent.ResolveDefaultAgentDir()
	if err != nil {
		t.Fatalf("ResolveDefaultAgentDir() error: %v", err)
	}
	if want := filepath.Join(home, "custom-pi-agent"); got != want {
		t.Fatalf("PI_CODING_AGENT_DIR = %q, want %q", got, want)
	}
}
