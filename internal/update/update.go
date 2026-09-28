// Package update checks GitHub Releases for a newer ReadGate build and
// installs it with stdlib-only code (no external updater dependency).
//
// Release assets (see .github/workflows/release.yml):
//   Windows  ReadGate-Windows-amd64.exe  (single .exe, also aliased ReadGate.exe)
//   macOS    ReadGate-macOS-universal.zip (contains ReadGate.app)
//   Linux    ReadGate-Linux-amd64.tar.gz  (contains ReadGate binary)
//
// Strategy per OS:
//   Windows/Linux: download the asset, then relaunch into a tiny detached
//     script that waits for this process to exit, swaps the binary, and
//     starts the new build. The app quits itself right after spawning it.
//   macOS: a running .app bundle cannot safely replace itself, so the update
//     is downloaded to ~/Downloads and revealed — the user drags it over
//     /Applications (standard unsigned-app flow).
package update

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	goruntime "runtime"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Repo is "owner/Repo". Forks can point elsewhere with READGATE_UPDATE_REPO.
func repo() string {
	if v := strings.TrimSpace(os.Getenv("READGATE_UPDATE_REPO")); v != "" {
		return strings.Trim(v, "/")
	}
	return "iMohamedSheta/ReadGate"
}

// APIURL is the GitHub "latest release" endpoint for the repo.
func APIURL() string { return "https://api.github.com/repos/" + repo() + "/releases/latest" }

// Asset is one file attached to a GitHub release.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	ContentType        string `json:"content_type"`
}

// Release is the subset of the GitHub release JSON we need.
type Release struct {
	TagName     string  `json:"tag_name"`
	Name        string  `json:"name"`
	Body        string  `json:"body"`
	HTMLURL     string  `json:"html_url"`
	PublishedAt string  `json:"published_at"`
	Assets      []Asset `json:"assets"`
}

// Info is the Wails binding payload: camelCase JSON, string timestamps
// (Wails cannot bind time.Time), matching the model package conventions.
type Info struct {
	CurrentVersion  string `json:"currentVersion"`
	LatestVersion   string `json:"latestVersion"`
	ReleaseName     string `json:"releaseName"`
	Notes           string `json:"notes"`
	PageURL         string `json:"pageUrl"`
	AssetName       string `json:"assetName"`
	DownloadURL     string `json:"downloadUrl"`
	Size            int64  `json:"size"`
	PublishedAt     string `json:"publishedAt"`
	UpdateAvailable bool   `json:"updateAvailable"`
	// CanInstall is false on macOS (manual drag-to-Applications flow).
	CanInstall bool   `json:"canInstall"`
	Platform   string `json:"platform"`
}

// AssetNameFor maps a GOOS/GOARCH pair to the release asset published by
// release.yml. Empty string = no asset for this platform.
func AssetNameFor(goos, arch string) string {
	switch goos {
	case "windows":
		return "ReadGate-Windows-amd64.exe"
	case "darwin":
		return "ReadGate-macOS-universal.zip"
	case "linux":
		if arch == "arm64" {
			return "" // release.yml ships amd64 only for now
		}
		return "ReadGate-Linux-amd64.tar.gz"
	default:
		return ""
	}
}

// CurrentAssetName reports the asset this running build updates from.
func CurrentAssetName() string { return AssetNameFor(goruntime.GOOS, goruntime.GOARCH) }

// IsNewer reports whether latest is strictly newer than current.
// "dev" (local builds) is older than any tagged release. Tags may carry a
// leading "v" and pre-release suffixes ("-rc.1"); a bare release beats its
// own pre-releases, otherwise compare dot-separated numeric fields.
func IsNewer(current, latest string) bool {
	c, lok := normalize(current)
	l, rok := normalize(latest)
	if !rok {
		return false
	}
	if !lok {
		return true // current is dev/empty: any real tag is newer
	}
	if c.core != l.core {
		return compareCore(c.nums, l.nums) < 0
	}
	// Same core: release > pre-release; else lexical pre-release compare.
	if c.pre == "" && l.pre != "" {
		return false
	}
	if c.pre != "" && l.pre == "" {
		return true
	}
	return c.pre < l.pre
}

type normVer struct {
	core string
	nums []int
	pre  string
}

func normalize(v string) (normVer, bool) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	if v == "" || strings.EqualFold(v, "dev") {
		return normVer{}, false
	}
	core, pre, _ := strings.Cut(v, "-")
	parts := strings.Split(core, ".")
	nums := make([]int, 0, len(parts))
	for _, p := range parts {
		digits := ""
		for _, r := range p {
			if r < '0' || r > '9' {
				break
			}
			digits += string(r)
		}
		if digits == "" {
			return normVer{}, false
		}
		n, err := strconv.Atoi(digits)
		if err != nil {
			return normVer{}, false
		}
		nums = append(nums, n)
	}
	return normVer{core: core, nums: nums, pre: pre}, true
}

func compareCore(a, b []int) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}

// httpClient is shared: GitHub API + asset downloads both need sane timeouts.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// FetchLatest downloads the latest-release metadata from the GitHub API.
func FetchLatest(ctx context.Context) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, APIURL(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ReadGate-Updater")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == 429 {
		return nil, fmt.Errorf("github releases: rate limited (HTTP %d) — try again in a few minutes", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github releases: HTTP %d", resp.StatusCode)
	}
	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("github releases: bad response: %w", err)
	}
	if rel.TagName == "" {
		return nil, fmt.Errorf("github releases: no releases published yet")
	}
	return &rel, nil
}

// Check compares the running version against the latest GitHub release and
// picks the asset for this platform. Never fails for "no update": it returns
// UpdateAvailable=false instead.
func Check(ctx context.Context, current string) (*Info, error) {
	rel, err := FetchLatest(ctx)
	if err != nil {
		return nil, err
	}
	want := AssetNameFor(goruntime.GOOS, goruntime.GOARCH)
	info := &Info{
		CurrentVersion: current,
		LatestVersion:  rel.TagName,
		ReleaseName:    rel.Name,
		Notes:          rel.Body,
		PageURL:        rel.HTMLURL,
		PublishedAt:    rel.PublishedAt,
		Platform:       goruntime.GOOS + "/" + goruntime.GOARCH,
		CanInstall:     goruntime.GOOS == "windows" || goruntime.GOOS == "linux",
	}
	if info.ReleaseName == "" {
		info.ReleaseName = rel.TagName
	}
	info.UpdateAvailable = IsNewer(current, rel.TagName)
	if want == "" {
		return info, nil // platform ships no asset: page link is the path
	}
	for _, a := range rel.Assets {
		if a.Name == want {
			info.AssetName = a.Name
			info.DownloadURL = a.BrowserDownloadURL
			info.Size = a.Size
			break
		}
	}
	return info, nil
}

// allowedDownloadHost guards Download: the URL must be a GitHub release host
// so a tampered API payload cannot turn the updater into an open downloader.
func allowedDownloadHost(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return false
	}
	h := strings.ToLower(u.Host)
	switch h {
	case "github.com", "api.github.com", "objects.githubusercontent.com",
		"release-assets.githubusercontent.com", "codeload.github.com":
		return true
	}
	return strings.HasSuffix(h, ".githubusercontent.com")
}

// Download fetches downloadURL into dir (created if needed) as assetName and
// returns the full path. Progress reports bytes written when non-nil.
func Download(ctx context.Context, downloadURL, dir, assetName string, progress func(written, total int64)) (string, error) {
	if downloadURL == "" || assetName == "" {
		return "", fmt.Errorf("no update asset for this platform — open the release page instead")
	}
	if !allowedDownloadHost(downloadURL) {
		return "", fmt.Errorf("refusing to download from untrusted host")
	}
	if strings.Contains(assetName, "/") || strings.Contains(assetName, "\\") || strings.Contains(assetName, "..") {
		return "", fmt.Errorf("bad asset name")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "ReadGate-Updater")
	req.Header.Set("Accept", "application/octet-stream")
	// Asset downloads are big binaries: allow longer than the API default.
	client := &http.Client{Timeout: 15 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	dest := filepath.Join(dir, assetName)
	tmp := dest + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", err
	}
	var written int64
	total := resp.ContentLength
	buf := make([]byte, 256*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				_ = f.Close()
				_ = os.Remove(tmp)
				return "", werr
			}
			written += int64(n)
			if progress != nil {
				progress(written, total)
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				break
			}
			_ = f.Close()
			_ = os.Remove(tmp)
			return "", fmt.Errorf("download: %w", rerr)
		}
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return dest, nil
}

// DownloadsDir is ~/Downloads when resolvable, else os.TempDir().
func DownloadsDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dl := filepath.Join(home, "Downloads")
		if st, err := os.Stat(dl); err == nil && st.IsDir() {
			return dl
		}
	}
	return os.TempDir()
}

// Install swaps the running binary for the downloaded asset and restarts.
// It spawns a detached helper, so the caller must quit immediately after a
// nil return. macOS returns an error by design (manual .app install).
func Install(downloadedPath string) error {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return fmt.Errorf("cannot locate running binary")
	}
	switch goruntime.GOOS {
	case "windows":
		return installWindows(exe, downloadedPath)
	case "linux":
		return installLinux(exe, downloadedPath)
	default:
		return fmt.Errorf("automatic install is not supported on macOS — the update was saved to %s; unzip it and drag ReadGate.app to Applications", downloadedPath)
	}
}

func installWindows(exe, downloaded string) error {
	if !strings.HasSuffix(strings.ToLower(downloaded), ".exe") {
		return fmt.Errorf("expected a .exe update, got %s", filepath.Base(downloaded))
	}
	pid := os.Getpid()
	bat := filepath.Join(os.TempDir(), fmt.Sprintf("readgate-update-%d.bat", pid))
	script := "@echo off\r\n" +
		"rem ReadGate self-updater: waits for the old process, swaps the exe, relaunches.\r\n" +
		fmt.Sprintf(":wait\r\ntasklist /FI \"PID eq %d\" 2>NUL | find \"%d\" >NUL\r\nif not errorlevel 1 ( ping -n 2 127.0.0.1 >NUL & goto wait )\r\n", pid, pid) +
		fmt.Sprintf("copy /Y %q %q >NUL\r\n", downloaded, exe) +
		fmt.Sprintf("start \"\" %q\r\n", exe) +
		"del %~f0\r\n"
	if err := os.WriteFile(bat, []byte(script), 0o644); err != nil {
		return err
	}
	cmd := exec.Command("cmd", "/C", "start", "/B", "", bat)
	cmd.Dir = os.TempDir()
	if err := cmd.Start(); err != nil {
		_ = os.Remove(bat)
		return err
	}
	_ = cmd.Process.Release()
	return nil
}

func installLinux(exe, downloaded string) error {
	lower := strings.ToLower(downloaded)
	var newBin string
	if strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz") {
		dir, err := os.MkdirTemp("", "readgate-update-*")
		if err != nil {
			return err
		}
		bin, err := extractFirstExecutable(downloaded, dir)
		if err != nil {
			return err
		}
		newBin = bin
	} else {
		newBin = downloaded
		if err := os.Chmod(newBin, 0o755); err != nil {
			return err
		}
	}
	pid := os.Getpid()
	sh := filepath.Join(os.TempDir(), fmt.Sprintf("readgate-update-%d.sh", pid))
	script := "#!/bin/sh\n" +
		"# ReadGate self-updater: waits for the old process, swaps the binary, relaunches.\n" +
		fmt.Sprintf("while kill -0 %d 2>/dev/null; do sleep 1; done\n", pid) +
		fmt.Sprintf("cp -f %q %q\n", newBin, exe) +
		fmt.Sprintf("chmod +x %q\n", exe) +
		fmt.Sprintf("nohup %q >/dev/null 2>&1 &\n", exe) +
		"rm -f \"$0\"\n"
	if err := os.WriteFile(sh, []byte(script), 0o755); err != nil {
		return err
	}
	cmd := exec.Command("/bin/sh", sh)
	cmd.Dir = os.TempDir()
	if err := cmd.Start(); err != nil {
		_ = os.Remove(sh)
		return err
	}
	_ = cmd.Process.Release()
	return nil
}

// extractFirstExecutable unpacks a .tar.gz and returns the largest regular
// file inside (the ReadGate binary), marked executable.
func extractFirstExecutable(archive, dir string) (string, error) {
	f, err := os.Open(archive)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("not a gzip archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var best string
	var bestSize int64 = -1
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if !hdr.FileInfo().Mode().IsRegular() {
			continue
		}
		base := filepath.Base(hdr.Name)
		if base == "" || base == "." {
			continue
		}
		dest := filepath.Join(dir, base)
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return "", err
		}
		n, err := io.Copy(out, tr)
		_ = out.Close()
		if err != nil {
			return "", err
		}
		if n > bestSize {
			best, bestSize = dest, n
		}
	}
	if best == "" {
		return "", fmt.Errorf("archive holds no binary")
	}
	_ = os.Chmod(best, 0o755)
	return best, nil
}
