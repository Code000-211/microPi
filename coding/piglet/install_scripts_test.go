package piglet

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/installresolver"
)

// Every materialization a Piglet makes carries the install-scripts choice: off unless the caller opted in through
// ResolveOptions (validate) or the add source.
func TestPigletMaterializationCarriesTheInstallScriptsChoice(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scripted.yaml")
	if err := os.WriteFile(path, []byte("name: scripted\npackages:\n  s: npm:scripts-pkg@1.0.0\nextensions:\n  - name: runner\n    origins: [package:s]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, allow := range []bool{false, true} {
		var got []installresolver.MaterializeOptions
		installresolver.SetMaterializer(func(_, _, _ string, options installresolver.MaterializeOptions, _, _ io.Writer) (string, error) {
			got = append(got, options)
			return dir, nil
		})
		t.Cleanup(func() { installresolver.SetMaterializer(nil) })
		resolution, err := ResolveEffectiveWithOptions(path, ResolveOptions{AllowInstallScripts: allow})
		if err != nil {
			t.Fatal(err)
		}
		ResolveExtensions(resolution.Piglet)
		if len(got) != 1 || got[0].AllowInstallScripts != allow {
			t.Errorf("allow=%v: materializations = %+v", allow, got)
		}
	}
}
