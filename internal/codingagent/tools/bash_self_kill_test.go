package tools

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// µPi: ports microPi's test/bash-self-kill.test.ts.
const selfKillAgentLine = "/opt/pi-harness/pi --mode json --no-session -- Save your results to /app/posterior_alpha_mean.txt and /app/analysis.R"

func killPatterns(command string) []string {
	var out []string
	for _, m := range fullMatchKillPatterns(command) {
		out = append(out, m.pattern)
	}
	return out
}

func TestFullMatchKillPatternsFindsPkillF(t *testing.T) {
	for _, command := range []string{`pkill -f "analysis.R"`, "pkill -9f analysis.R", "pkill -KILL --full -u root analysis.R", "kill $(pgrep -f analysis.R)"} {
		if got := killPatterns(command); !slices.Equal(got, []string{"analysis.R"}) {
			t.Errorf("%s: patterns = %q, want [analysis.R]", command, got)
		}
	}
}

func TestFullMatchKillPatternsLeavesPkillByNameAndListingPgrep(t *testing.T) {
	for _, command := range []string{"pkill Rscript", "pgrep -af analysis.R"} {
		if got := killPatterns(command); len(got) != 0 {
			t.Errorf("%s: patterns = %q, want none", command, got)
		}
	}
}

func TestSelfKillPatternRefusesTheTerminalBenchCommand(t *testing.T) {
	command := `sed -n '1,45p' /tmp/run.log; pkill -f "analysis.R"; sleep 1; pkill -9 -f "exec/R"; echo KILLED`
	if got, ok := selfKillPatternFor(command, selfKillAgentLine); !ok || got != "analysis.R" {
		t.Fatalf("selfKillPatternFor = %q, %v; want analysis.R", got, ok)
	}
}

func TestSelfKillPatternLetsOtherPatternsThrough(t *testing.T) {
	for _, command := range []string{`pkill -f "exec/R"`, "pkill -f 'Rscript /app/analysis'", "pkill -xf analysis.R"} {
		if got, ok := selfKillPatternFor(command, selfKillAgentLine); ok {
			t.Errorf("%s: matched %q, want none", command, got)
		}
	}
	if _, ok := selfKillPatternFor(`pkill -f "analysis.R"`, ""); ok {
		t.Error("an unknown command line must match nothing")
	}
}

// The bash tool refuses a pkill -f whose pattern is in the agent's own command line, before running anything.
func TestBashToolRefusesSelfKill(t *testing.T) {
	line, ok := agentCommandLine()
	if !ok || line == "" {
		t.Skip("no /proc/self/cmdline")
	}
	// The test binary's own path is in its command line.
	pattern := strings.Fields(line)[0]
	pattern = pattern[strings.LastIndexByte(pattern, '/')+1:]
	bt := &BashTool{CWD: t.TempDir()}
	args, _ := json.Marshal(map[string]any{"command": "pkill -f " + pattern})
	res, err := bt.Execute(context.Background(), "", args, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || res.Text() != selfKillRefusal(pattern) {
		t.Fatalf("res = %+v, want the self-kill refusal", res)
	}
}
