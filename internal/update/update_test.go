package update

import "testing"

func TestIsNewer(t *testing.T) {
	cases := []struct {
		cur, latest string
		want        bool
	}{
		{"v1.0.0", "v1.0.1", true},
		{"v1.0.1", "v1.0.1", false},
		{"v1.2.0", "v1.1.9", false},
		{"v1.9.9", "v2.0.0", true},
		{"v1.0", "v1.0.1", true},
		{"1.0.0", "v1.0.1", true},   // missing v prefix tolerated
		{"dev", "v0.0.1", true},     // local builds always update
		{"", "v0.0.1", true},        // empty version always updates
		{"v1.0.0", "bad-tag", false}, // unparseable latest: no update
		{"v1.0.0-rc.1", "v1.0.0", true},
		{"v1.0.0", "v1.0.0-rc.1", false},
		{"v2.10.2", "v2.9.9", false}, // numeric, not lexical
	}
	for _, c := range cases {
		if got := IsNewer(c.cur, c.latest); got != c.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", c.cur, c.latest, got, c.want)
		}
	}
}

func TestAssetNameFor(t *testing.T) {
	if got := AssetNameFor("windows", "amd64"); got != "ReadGate-Windows-amd64.exe" {
		t.Errorf("windows asset = %q", got)
	}
	if got := AssetNameFor("darwin", "arm64"); got != "ReadGate-macOS-universal.zip" {
		t.Errorf("darwin asset = %q", got)
	}
	if got := AssetNameFor("linux", "amd64"); got != "ReadGate-Linux-amd64.tar.gz" {
		t.Errorf("linux asset = %q", got)
	}
	if got := AssetNameFor("plan9", "amd64"); got != "" {
		t.Errorf("plan9 asset should be empty, got %q", got)
	}
}

func TestAllowedDownloadHost(t *testing.T) {
	good := []string{
		"https://github.com/iMohamedSheta/ReadGate/releases/download/v1.0/ReadGate.exe",
		"https://objects.githubusercontent.com/abc",
		"https://release-assets.githubusercontent.com/abc",
		"https://foo.githubusercontent.com/abc",
	}
	for _, u := range good {
		if !allowedDownloadHost(u) {
			t.Errorf("should allow %s", u)
		}
	}
	bad := []string{
		"http://github.com/x/y.exe",
		"https://evil.com/ReadGate.exe",
		"https://github.com.evil.com/x",
		"",
		"not a url",
	}
	for _, u := range bad {
		if allowedDownloadHost(u) {
			t.Errorf("should refuse %q", u)
		}
	}
}
