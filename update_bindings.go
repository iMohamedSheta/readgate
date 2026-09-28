package main

import (
	"context"
	"fmt"
	"time"

	"readgate/internal/update"
	"readgate/internal/version"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// update setting keys (persisted via store settings, mirrored in localStorage).
const (
	settingUpdateAutoCheck = "update.autoCheck" // "on" (default) | "off"
	settingUpdateSkip      = "update.skipVersion"
	settingUpdateLastCheck = "update.lastCheck"
)

// CheckForUpdates compares the baked-in version against the latest GitHub
// release. Manual checks always hit the network; the frontend throttles
// automatic ones to once per 24h via GetSettings()[update.lastCheck].
func (a *App) CheckForUpdates() (*update.Info, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	info, err := update.Check(ctx, version.Version)
	if err != nil {
		return nil, err
	}
	a.store.SetSetting(settingUpdateLastCheck, time.Now().UTC().Format(time.RFC3339))
	return info, nil
}

// SkipUpdateVersion hides a release ("Skip this version" in the banner).
func (a *App) SkipUpdateVersion(v string) {
	if v == "" {
		return
	}
	a.store.SetSetting(settingUpdateSkip, v)
}

// OpenReleasePage opens the GitHub release page in the default browser.
// Only https github.com URLs are allowed.
func (a *App) OpenReleasePage(pageURL string) string {
	if pageURL == "" {
		if info, err := a.CheckForUpdates(); err == nil && info.PageURL != "" {
			pageURL = info.PageURL
		}
	}
	if len(pageURL) < 9 || pageURL[:8] != "https://" {
		return "not a valid release URL"
	}
	host := pageURL[8:]
	for i, c := range host {
		if c == '/' || c == ':' {
			host = host[:i]
			break
		}
	}
	if host != "github.com" {
		return "refusing to open a non-github.com URL"
	}
	runtime.BrowserOpenURL(a.ctx, pageURL)
	return ""
}

// DownloadAndInstallUpdate is the one-click updater: re-checks (so the
// install never uses a stale URL), downloads the platform asset to
// ~/Downloads, then — Windows/Linux — spawns the self-updater and quits the
// app so the binary can be swapped. Returns a human message for the UI.
// On macOS it downloads the zip and returns instructions (manual install).
func (a *App) DownloadAndInstallUpdate() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	info, err := update.Check(ctx, version.Version)
	if err != nil {
		return "", err
	}
	if !info.UpdateAvailable {
		return "already on the latest version (" + version.Version + ")", nil
	}
	if info.DownloadURL == "" {
		runtime.BrowserOpenURL(a.ctx, info.PageURL)
		return "no automatic package for " + info.Platform + " — the release page was opened instead", nil
	}
	dlCtx, dlCancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer dlCancel()
	path, err := update.Download(dlCtx, info.DownloadURL, update.DownloadsDir(), info.AssetName, func(written, total int64) {
		runtime.EventsEmit(a.ctx, "update:progress", map[string]any{
			"written": written, "total": total, "asset": info.AssetName,
		})
	})
	if err != nil {
		return "", err
	}
	if err := update.Install(path); err != nil {
		// macOS lands here by design: file is downloaded, user finishes it.
		return fmt.Sprintf("downloaded %s — %s", info.AssetName, err.Error()), nil
	}
	// Detached swap script is running: quit now so it can replace us.
	go func() {
		time.Sleep(400 * time.Millisecond)
		runtime.Quit(a.ctx)
	}()
	return fmt.Sprintf("installing %s — ReadGate will restart on the new version…", info.LatestVersion), nil
}
