//go:build !pig_strip_docs

package prompts

import (
	"path/filepath"
	"strings"
	"testing"
)

// upstream: packages/coding-agent/src/core/system-prompt.ts: the docs section names getReadmePath(), getDocsPath() and
// getExamplesPath(). µPi: microPi's short form; examples only when they are a local folder, not a URL.
func TestDocsSectionNamesTheGivenReadmeAndDocsPaths(t *testing.T) {
	docs := filepath.Join(t.TempDir(), "docs")
	readme := filepath.Join(t.TempDir(), "BUNDLE-README.md")
	section := docsSectionAt(docs, readme, "examples-here")
	if want := "read " + readme + ", the docs in " + docs + " and the examples in examples-here, following their links; docs/... and examples/... paths are under those folders, not the working folder."; !strings.HasSuffix(section, want) {
		t.Fatalf("docs section does not name the given paths:\n%s", section)
	}
	if section := docsSectionAt(docs, "", PigExamplesLocation); !strings.HasSuffix(section, "read "+filepath.Join(docs, "README.md")+" and the docs in "+docs+", following their links; docs/... paths are under that folder, not the working folder.") {
		t.Fatalf("an empty readme is not README.md inside the docs path, or a URL is named as examples:\n%s", section)
	}
}
