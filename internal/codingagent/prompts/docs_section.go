//go:build !pig_strip_docs

package prompts

// docs_section.go holds the system prompt's docs section. It is the boundary a
// Piglet Binary compiles out with the pig_strip_docs tag together with
// `pig docs` and the docs bundle (internal/pigdocs); docs_section_stripped.go
// replaces it so the model is not pointed at files the Binary never writes.

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/configroot"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// docsSections is the docs section after tools and rules, or none when a
// Piglet strips docs, as its Binary does. An empty root names this binary's
// bundle, and the section is absent when there is none: the binary embeds no
// bundle, or startup could not write it to a config root that held none of
// this build.
func docsSections(root, readme, examples string) []section {
	if pigstrip.Has(pigstrip.ListFeatures, pigstrip.Docs) {
		return nil
	}
	if root == "" {
		// pig additive (D22): a bundle that is not on disk is not named.
		dir, ok := configroot.DocsDir()
		if !ok || !configroot.DocsPresent() {
			return nil
		}
		root = dir
	}
	return []section{{"docs", docsSectionAt(root, readme, examples)}}
}

// docsTopic is one subject the docs section names, with the strip entry that owns it, if any.
type docsTopic struct {
	text     string
	list, id string
}

// docsTopics are the docs section's topics in microPi's order (system-prompt.ts).
var docsTopics = []docsTopic{
	{text: "extensions"},
	{text: "SDK"},
	{text: "themes", list: pigstrip.ListFeatures, id: pigstrip.Themes},
	{text: "skills", list: pigstrip.ListFeatures, id: pigstrip.Skills},
	{text: "TUI"},
	{text: "models"},
	{text: "packages"},
	{text: "MCP", list: pigstrip.ListExtensions, id: "mcp"},
}

// pigletTopic is the topic a Piglet adds. Like every topic it goes with the docs section: the `docs` feature owns it, so
// stripping the `/piglet` command, which is only the inspection UI, does not remove the Piglet docs.
// pig additive (D23, D92): Pi has no Piglets.
var pigletTopic = docsTopic{text: "Piglets"}

// docsSectionAt is microPi's docs section: short, since every request carries it and it matters only for questions
// about pi itself. An empty readme is README.md inside root. µPi: the examples are named only when they are a local
// folder: PiG publishes its examples in its repository, and the section points only at the bundled documentation.
// pig additive (D92): a topic the process strips is not named.
func docsSectionAt(root, readme, examples string) string {
	if readme == "" {
		readme = filepath.Join(root, "README.md")
	}
	all := docsTopics
	if pigletDocsTopic.Load() {
		all = append(slices.Clone(docsTopics), pigletTopic)
	}
	var topics []string
	for _, topic := range all {
		if topic.list != "" && pigstrip.Has(topic.list, topic.id) {
			continue
		}
		topics = append(topics, topic.text)
	}
	read := "read " + readme + " and the docs in " + root
	paths := "docs/... paths are under that folder"
	if examples != "" && !strings.Contains(examples, "://") {
		read = "read " + readme + ", the docs in " + root + " and the examples in " + examples
		paths = "docs/... and examples/... paths are under those folders"
	}
	return "Only for questions about pi itself (" + strings.Join(topics, ", ") + "): " + read + ", following their links; " + paths + ", not the working folder."
}
