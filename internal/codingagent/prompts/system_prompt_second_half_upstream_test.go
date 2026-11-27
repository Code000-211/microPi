//go:build !pig_strip_docs

package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigdocs"
)

// Cases 8-14 of packages/coding-agent/test/system-prompt.test.ts (111-209). Keep their original inputs and strengthen substring/count checks to complete emitted-section equality. D22 owns only the PiG product and documentation-location literals.
func TestUpstreamSystemPromptSecondHalfExactSections(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	docsRoot := pigdocs.DocsDir(home)
	// µPi: microPi's short docs section, without the examples PiG publishes in its repository.
	docs := "<docs>\nOnly for questions about pi itself (extensions, SDK, themes, skills, TUI, models, packages, MCP): read " + filepath.Join(docsRoot, "README.md") +
		" and the docs in " + docsRoot + ", following their links; docs/... paths are under that folder, not the working folder.\n</docs>"
	const toolsTail = "\n\nAdditional tools may be available.\n</tools>"
	// µPi: microPi's ordered-calls rule comes before the closing rules.
	const rulesTail = "\n- " + orderedCallsRule + "\n- Be concise in your responses\n- Show file paths clearly when working with files\n</rules>"
	const skills = `<skills>
The following skills provide specialized instructions for specific tasks.
Use bash to load a skill's file when the task matches its description.
Resolve relative paths against the skill's directory (parent of SKILL.md). Use absolute paths in tool calls.

<available_skills>
  <skill>
    <name>test-skill</name>
    <description>A test skill.</description>
    <location>/skills/test-skill/SKILL.md</location>
  </skill>
</available_skills>
</skills>`
	skill := Skill{Name: "test-skill", Description: "A test skill.", Path: "/skills/test-skill/SKILL.md"}
	for _, tc := range []struct {
		name, section, want string
		opts                Options
	}{
		{"111 docs absolute bases", "docs", docs, Options{Cwd: cwd}},
		{"126 custom tool snippet", "tools", "<tools>\n- dynamic_tool: Run dynamic test behavior" + toolsTail, Options{Cwd: cwd, Tools: []string{"read", "dynamic_tool"}, ToolHints: map[string]string{"dynamic_tool": "Run dynamic test behavior"}}},
		{"140 omitted custom snippet", "tools", "<tools>\n(none)" + toolsTail, Options{Cwd: cwd, Tools: []string{"read", "dynamic_tool"}}},
		{"153 appended guideline", "rules", "<rules>\n- Use dynamic_tool for project summaries." + rulesTail, Options{Cwd: cwd, Tools: []string{"read", "dynamic_tool"}, PromptGuidelines: []string{"Use dynamic_tool for project summaries."}}},
		{"165 trimmed unique guideline", "rules", "<rules>\n- Use dynamic_tool for summaries." + rulesTail, Options{Cwd: cwd, Tools: []string{"read", "dynamic_tool"}, PromptGuidelines: []string{"Use dynamic_tool for summaries.", "  Use dynamic_tool for summaries.  ", "   "}}},
		{"179 bash skills default prompt", "skills", skills, Options{Cwd: cwd, Tools: []string{"bash"}, Skills: []Skill{skill}}},
		{"179 bash skills custom prompt", "skills", skills, Options{Cwd: cwd, CustomPrompt: "Custom system prompt", AppendMode: "replace", Tools: []string{"bash"}, Skills: []Skill{skill}}},
		{"197 no skill reader", "skills", "", Options{Cwd: cwd, Tools: []string{"write"}, Skills: []Skill{skill}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prompt := BuildDefaultPrompt(tc.opts)
			start, end := "<"+tc.section+">", "</"+tc.section+">"
			_, rest, found := strings.Cut(prompt, start)
			if tc.want == "" {
				if found {
					t.Fatalf("unexpected %s section in %q", tc.section, prompt)
				}
				return
			}
			content, _, closed := strings.Cut(rest, end)
			if !found || !closed {
				t.Fatalf("missing complete %s section in %q", tc.section, prompt)
			}
			if got := start + content + end; got != tc.want {
				t.Errorf("%s section:\ngot  %q\nwant %q", tc.section, got, tc.want)
			}
		})
	}
}
