package updates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"runtime"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`

	PublishedAt string `json:"published_at,omitempty"`

	source *neturl.URL
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func (c *Checker) fetchLatestRelease(ctx context.Context) (*githubRelease, error) {
	return c.fetchRelease(ctx, "latest")
}

func (c *Checker) fetchRelease(ctx context.Context, which string) (*githubRelease, error) {
	ctx, cancel := context.WithTimeout(ctx, metadataTimeout)
	defer cancel()
	base, err := c.apiBase()
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/repos/%s/releases/%s", base, c.resolveRepo(), which)
	req, err := http.NewRequestWithContext(withSource(ctx, base), "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "aii-os/"+c.currentVer())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, errNoRelease
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("release API %s returned %d: %s", base, resp.StatusCode, string(body))
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadSize+1))
	if err != nil {
		return nil, fmt.Errorf("read release: %w", err)
	}
	if int64(len(raw)) > maxDownloadSize {
		return nil, fmt.Errorf("the release metadata from %s exceeds the %d byte limit", base, int64(maxDownloadSize))
	}
	var release githubRelease
	if err := json.Unmarshal(raw, &release); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}
	release.source = base
	return &release, nil
}

func gitHubReleaseHost(raw string) error {
	u, err := neturl.Parse(raw)
	if err != nil {
		return fmt.Errorf("unparseable URL: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("refusing a non-https release URL (%s)", u.Scheme)
	}
	h := u.Hostname()
	ok := h == "github.com" || h == "api.github.com" || h == "codeload.github.com" ||
		h == "githubusercontent.com" || strings.HasSuffix(h, ".githubusercontent.com")
	if !ok {
		return fmt.Errorf("refusing a release URL off GitHub (%s)", h)
	}
	return nil
}

func releaseHost(base *neturl.URL, raw string) error {
	if base == nil {
		return errors.New("refusing a release URL with no recorded release source")
	}
	if def, _ := APIBase(""); strings.EqualFold(base.Hostname(), def.Hostname()) {
		return gitHubReleaseHost(raw)
	}
	u, err := neturl.Parse(raw)
	if err != nil {
		return fmt.Errorf("unparseable URL: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("refusing a non-https release URL (%s)", u.Scheme)
	}
	if !strings.EqualFold(u.Hostname(), base.Hostname()) || httpsPort(u) != httpsPort(base) {
		return fmt.Errorf("refusing a release URL off the release source %s (%s)", base.Host, u.Host)
	}
	return nil
}

func httpsPort(u *neturl.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	return "443"
}

func (c *Checker) download(ctx context.Context, source *neturl.URL, url string) ([]byte, error) {
	if err := c.hostAllowlist(source, url); err != nil {
		return nil, fmt.Errorf("download egress: %w", err)
	}
	dlCtx, cancel := context.WithTimeout(withSource(ctx, source), downloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(dlCtx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "aii-os/"+c.currentVer())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download returned %d", resp.StatusCode)
	}

	return readBounded(resp.Body, maxDownloadSize, "release download "+url)
}

func findAssets(release *githubRelease, archiveName, sigName string) (string, string, error) {
	var archiveURL, sigURL string
	for _, a := range release.Assets {
		if a.Name == archiveName {
			archiveURL = a.BrowserDownloadURL
		}
		if a.Name == sigName {
			sigURL = a.BrowserDownloadURL
		}
	}
	if archiveURL == "" {
		return "", "", fmt.Errorf("archive asset %q not found in release %s", archiveName, release.TagName)
	}
	if sigURL == "" {
		return "", "", fmt.Errorf("signature asset %q not found in release %s", sigName, release.TagName)
	}
	return archiveURL, sigURL, nil
}

func assetName(version string) string {
	platform, arch := hostTarget()
	return AssetName(version, platform, arch)
}

func hostTarget() (platform, arch string) {
	return packagefmt.HostPlatform(), runtime.GOARCH
}
