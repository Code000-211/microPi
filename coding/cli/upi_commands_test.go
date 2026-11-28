package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestUpiVersionOutputs(t *testing.T) {
	cliVer := cliVersionString()
	wantCLI := fmt.Sprintf("µPi %s (PiG %s)", MicroPiVersion, Version)
	if cliVer != wantCLI {
		t.Fatalf("cliVersionString() = %q, want %q", cliVer, wantCLI)
	}

	detailed := detailedVersionString()
	for _, want := range []string{
		"µPi: " + MicroPiVersion,
		"pig: " + PigVersion,
		"upstream pi: " + UpstreamVersion,
		"go: " + runtime.Version(),
		"platform: " + runtime.GOOS + "/" + runtime.GOARCH,
	} {
		if !strings.Contains(detailed, want) {
			t.Errorf("detailedVersionString() missing %q in:\n%s", want, detailed)
		}
	}
}

func TestUpiDoctorOfflineAndRedaction(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MICROPI_HOME", "")
	t.Setenv("PIG_HOME", "")
	t.Setenv("OPENAI_API_KEY", "sk-secret-1234567890")
	t.Setenv("TERM_PROGRAM", "herdr")
	t.Setenv("HERDR_ENV", "1")

	var buf bytes.Buffer
	runDoctor(&buf, "/tmp/upi", []string{"--offline"})
	out := buf.String()

	for _, want := range []string{
		"µPi doctor",
		"upi-version:       µPi " + MicroPiVersion,
		"pig-version:       " + Version,
		"offline:           true",
		"tools",
		"config & paths",
		".micropi",
		"auth (logged-in providers)",
		"models (registered providers)",
		"default model",
		"mcp",
		"herdr",
		"active:          true",
		"sk-sec…(masked)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("runDoctor output missing %q\nOutput:\n%s", want, out)
		}
	}
	if strings.Contains(out, "sk-secret-1234567890") {
		t.Fatalf("runDoctor leaked unmasked secret in output")
	}
}

func TestMicroPiUpdateReleaseManifestAndSemver(t *testing.T) {
	if ver, ok := codingagent.ParseMicroPiReleaseTag("upi-v0.2.0"); !ok || ver != "0.2.0" {
		t.Fatalf("ParseMicroPiReleaseTag(upi-v0.2.0) = (%q, %v), want (0.2.0, true)", ver, ok)
	}
	if _, ok := codingagent.ParseMicroPiReleaseTag("v0.2.0"); ok {
		t.Fatalf("ParseMicroPiReleaseTag(v0.2.0) = true, want false")
	}
	// A release after the current one, whatever it is.
	if !codingagent.IsNewerPackageVersion("999.0.0", MicroPiVersion) {
		t.Fatalf("expected 999.0.0 > %s", MicroPiVersion)
	}
	if codingagent.IsNewerPackageVersion(MicroPiVersion, MicroPiVersion) {
		t.Fatalf("expected %s not newer than itself", MicroPiVersion)
	}

	t.Setenv("PIG_UPDATE_ALLOW_LOOPBACK_HTTP", "1")
	binaryBytes := []byte("fake-upi-binary")
	sumBytes := sha256.Sum256(binaryBytes)
	sumHex := hex.EncodeToString(sumBytes[:])
	assetName := fmt.Sprintf("upi-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		assetName += ".exe"
	}

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/Code000-211/microPi/releases/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tag_name": "upi-v0.2.0",
				"body":     "Release notes",
				"assets": []map[string]string{
					{"name": assetName, "browser_download_url": srv.URL + "/download/" + assetName},
					{"name": "SHA256SUMS", "browser_download_url": srv.URL + "/download/SHA256SUMS"},
				},
			})
		case "/repos/Code000-211/microPi/releases":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"tag_name":   "upi-v0.3.0-beta.1",
					"prerelease": true,
					"assets": []map[string]string{
						{"name": assetName, "browser_download_url": srv.URL + "/download/" + assetName},
						{"name": "SHA256SUMS", "browser_download_url": srv.URL + "/download/SHA256SUMS"},
					},
				},
			})
		case "/download/SHA256SUMS":
			_, _ = fmt.Fprintf(w, "%s  %s\n", sumHex, assetName)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	prevBase := codingagent.MicroPiReleasesAPIBase
	codingagent.MicroPiReleasesAPIBase = srv.URL + "/repos/Code000-211/microPi"
	defer func() { codingagent.MicroPiReleasesAPIBase = prevBase }()

	t.Setenv("MICROPI_UPDATE_URL", "")
	t.Setenv("PIG_UPDATE_URL", "")
	if !strings.HasSuffix(codingagent.MicroPiReleasesAPIBase, "/repos/Code000-211/microPi") {
		t.Fatalf("unexpected MicroPiReleasesAPIBase: %s", codingagent.MicroPiReleasesAPIBase)
	}

	manifest, err := codingagent.FetchMicroPiReleaseManifest(context.Background(), srv.Client(), "stable")
	if err != nil {
		t.Fatalf("FetchMicroPiReleaseManifest(stable) error: %v", err)
	}
	if manifest.Version != "0.2.0" || manifest.PackageName != "upi" {
		t.Fatalf("stable manifest = %+v", manifest)
	}
	bin, ok := manifest.PlatformBinary()
	if !ok || bin.SHA256 != sumHex {
		t.Fatalf("PlatformBinary() = (%+v, %v), want sha256=%s", bin, ok, sumHex)
	}

	betaManifest, err := codingagent.FetchMicroPiReleaseManifest(context.Background(), srv.Client(), "beta")
	if err != nil {
		t.Fatalf("FetchMicroPiReleaseManifest(beta) error: %v", err)
	}
	if betaManifest.Version != "0.3.0-beta.1" {
		t.Fatalf("beta manifest version = %q, want 0.3.0-beta.1", betaManifest.Version)
	}
}
