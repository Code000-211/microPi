package codingagent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	semver "github.com/MichaelKinsy/PiG/third_party/semver"
)

// MicroPiReleasesAPIBase is the default GitHub API base URL for Code000-211/microPi releases.
var MicroPiReleasesAPIBase = "https://api.github.com/repos/Code000-211/microPi"

type githubRelease struct {
	TagName    string               `json:"tag_name"`
	Body       string               `json:"body"`
	Draft      bool                 `json:"draft"`
	Prerelease bool                 `json:"prerelease"`
	Assets     []githubReleaseAsset `json:"assets"`
}

type githubReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// ParseMicroPiReleaseTag extracts and validates the semver version from a `upi-v<X.Y.Z>` tag.
func ParseMicroPiReleaseTag(tag string) (string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(tag), "upi-v")
	if !ok || rest == "" {
		return "", false
	}
	if _, err := semver.StrictNewVersion(rest); err != nil {
		return "", false
	}
	return rest, true
}

// ResolveUpdateChannel returns "beta" or "stable" from explicit flag or MICROPI_CHANNEL env.
func ResolveUpdateChannel(explicit string) string {
	ch := strings.ToLower(strings.TrimSpace(explicit))
	if ch == "" {
		ch = strings.ToLower(strings.TrimSpace(os.Getenv("MICROPI_CHANNEL")))
	}
	if ch == "beta" {
		return "beta"
	}
	return "stable"
}

// FetchMicroPiReleaseManifest queries the configured GitHub Releases API (defaulting to
// Code000-211/microPi) for `upi-v*` releases and converts the selected release + SHA256SUMS
// into an UpdateManifest for SelfReplaceAtWithCommit.
func FetchMicroPiReleaseManifest(ctx context.Context, client *http.Client, channel string) (*UpdateManifest, error) {
	return FetchMicroPiReleaseManifestFrom(ctx, client, MicroPiReleasesAPIBase, channel)
}

// FetchMicroPiReleaseManifestFrom queries apiBase (`/releases/latest` or `/releases`) for `upi-v*` releases.
func FetchMicroPiReleaseManifestFrom(ctx context.Context, client *http.Client, apiBase, channel string) (*UpdateManifest, error) {
	base := strings.TrimRight(strings.TrimSpace(apiBase), "/")
	if base == "" {
		return nil, fmt.Errorf("empty microPi releases API base URL")
	}
	ch := ResolveUpdateChannel(channel)
	endpoint := base + "/releases/latest"
	if ch == "beta" {
		endpoint = base + "/releases?per_page=30"
	}

	var rel *githubRelease
	if ch == "beta" {
		var releases []githubRelease
		if err := fetchGitHubJSON(ctx, client, endpoint, &releases); err != nil {
			return nil, err
		}
		for i := range releases {
			r := &releases[i]
			if r.Draft {
				continue
			}
			if _, ok := ParseMicroPiReleaseTag(r.TagName); ok {
				rel = r
				break
			}
		}
	} else {
		var single githubRelease
		if err := fetchGitHubJSON(ctx, client, endpoint, &single); err != nil {
			return nil, err
		}
		if !single.Draft {
			if _, ok := ParseMicroPiReleaseTag(single.TagName); ok {
				rel = &single
			}
		}
	}
	if rel == nil {
		return nil, fmt.Errorf("no matching upi-v* release found at %s", endpoint)
	}

	version, _ := ParseMicroPiReleaseTag(rel.TagName)
	asset, sumsAsset, err := selectMicroPiAssets(rel.Assets, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return nil, err
	}
	sumsBody, err := fetchReleaseText(ctx, client, sumsAsset.BrowserDownloadURL)
	if err != nil {
		return nil, fmt.Errorf("fetch SHA256SUMS: %w", err)
	}
	sums := ParseSHA256SUMS(sumsBody)
	sha, ok := sums[asset.Name]
	if !ok || sha == "" {
		return nil, fmt.Errorf("SHA256SUMS has no entry for %s", asset.Name)
	}

	return &UpdateManifest{
		Version:     version,
		PackageName: AppName,
		Notes:       rel.Body,
		Binaries: map[string]UpdateBinary{
			PlatformKey(): {
				URL:    asset.BrowserDownloadURL,
				SHA256: sha,
			},
		},
	}, nil
}

func selectMicroPiAssets(assets []githubReleaseAsset, goos, goarch string) (githubReleaseAsset, githubReleaseAsset, error) {
	var sumsAsset githubReleaseAsset
	for _, a := range assets {
		if a.Name == "SHA256SUMS" {
			sumsAsset = a
			break
		}
	}
	if sumsAsset.BrowserDownloadURL == "" {
		return githubReleaseAsset{}, githubReleaseAsset{}, fmt.Errorf("release is missing SHA256SUMS asset")
	}

	archAliases := []string{goarch}
	if goarch == "amd64" {
		archAliases = append(archAliases, "x64")
	}
	var candidates []string
	for _, arch := range archAliases {
		ext := ""
		if goos == "windows" {
			ext = ".exe"
		}
		candidates = append(candidates,
			fmt.Sprintf("upi-%s-%s%s", goos, arch, ext),
			fmt.Sprintf("upi-%s-%s.tar.gz", goos, arch),
		)
	}
	for _, want := range candidates {
		for _, a := range assets {
			if a.Name == want && a.BrowserDownloadURL != "" {
				return a, sumsAsset, nil
			}
		}
	}
	return githubReleaseAsset{}, githubReleaseAsset{}, fmt.Errorf("release has no asset for %s/%s", goos, goarch)
}

// ParseSHA256SUMS parses standard `<hex>  <filename>` or `<hex> *<filename>` checksum files.
func ParseSHA256SUMS(body string) map[string]string {
	out := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		sum := strings.ToLower(fields[0])
		name := strings.TrimPrefix(fields[1], "*")
		name = strings.TrimPrefix(name, "./")
		out[name] = sum
	}
	return out
}

func fetchGitHubJSON(ctx context.Context, client *http.Client, rawURL string, target any) error {
	if _, err := validateUpdateURL(rawURL); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", ai.FormatUserAgent(nil))
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := doUpdateRequest(client, req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("update source %s: %s", rawURL, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("parse GitHub release response: %w", err)
	}
	return nil
}

func fetchReleaseText(ctx context.Context, client *http.Client, rawURL string) (string, error) {
	if _, err := validateUpdateURL(rawURL); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", ai.FormatUserAgent(nil))
	resp, err := doUpdateRequest(client, req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: %s", rawURL, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		return "", err
	}
	return string(data), nil
}
