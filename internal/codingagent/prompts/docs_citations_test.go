//go:build !pig_strip_docs

package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigdocs"
)

// The system prompt points the model at the documentation the binary materializes. µPi: microPi's short docs section
// cites no pages: it names the README and the docs folder, which must both be on disk, and links to no repository.
func TestDocsSectionCitesOnlyShippedPages(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)
	docsDir := pigdocs.DocsDir(root)
	if _, err := pigdocs.Sync(root); err != nil {
		t.Fatal(err)
	}
	section := docsSectionAt(docsDir, "", "")
	readme := filepath.Join(docsDir, "README.md")
	if !strings.Contains(section, "read "+readme+" and the docs in "+docsDir+",") {
		t.Fatalf("the section does not name the main documentation under %s:\n%s", docsDir, section)
	}
	if info, err := os.Stat(readme); err != nil || info.Size() == 0 {
		t.Errorf("README.md is not materialized under %s: %v", docsDir, err)
	}
	if strings.Contains(section, "://") {
		t.Errorf("the docs section links to a URL:\n%s", section)
	}
	if !strings.Contains(section, "MCP") {
		t.Error("the documentation section no longer names MCP")
	}
}
