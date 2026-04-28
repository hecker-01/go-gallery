package browser

import (
	"os"
	"path/filepath"
	"testing"
)

// TestParseProfilesIni_DefaultFlag verifies that a profile with Default=1
// is selected over a profile that appears first in the file.
func TestParseProfilesIni_DefaultFlag(t *testing.T) {
	ini := `
[Profile0]
Name=default-release
IsRelative=1
Path=profiles/abc123.default-release

[Profile1]
Name=dev
IsRelative=1
Path=profiles/xyz789.dev
Default=1
`
	configDir := "/home/user/.mozilla/firefox"
	got, err := parseProfilesIni(ini, configDir)
	if err != nil {
		t.Fatalf("parseProfilesIni: %v", err)
	}
	want := filepath.Join(configDir, "profiles/xyz789.dev")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestParseProfilesIni_FallbackToFirst verifies fallback to the first profile
// when no Default=1 is set.
func TestParseProfilesIni_FallbackToFirst(t *testing.T) {
	ini := `
[Profile0]
Name=default
IsRelative=1
Path=profiles/aaa111.default

[Profile1]
Name=secondary
IsRelative=1
Path=profiles/bbb222.secondary
`
	configDir := "/home/user/.mozilla/firefox"
	got, err := parseProfilesIni(ini, configDir)
	if err != nil {
		t.Fatalf("parseProfilesIni: %v", err)
	}
	want := filepath.Join(configDir, "profiles/aaa111.default")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestParseProfilesIni_InstallSection verifies that an [Install...] section's
// Default key is preferred over [Profile...] Default=1.
func TestParseProfilesIni_InstallSection(t *testing.T) {
	ini := `
[Install4F96D1932A9F858E]
Default=profiles/install-default.default-release
Locked=1

[Profile0]
Name=default-release
IsRelative=1
Path=profiles/something-else
Default=1
`
	configDir := "/home/user/.mozilla/firefox"
	got, err := parseProfilesIni(ini, configDir)
	if err != nil {
		t.Fatalf("parseProfilesIni: %v", err)
	}
	want := filepath.Join(configDir, "profiles/install-default.default-release")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestParseProfilesIni_NoProfiles verifies that an error is returned when no
// profiles are found.
func TestParseProfilesIni_NoProfiles(t *testing.T) {
	ini := `[General]
StartWithLastProfile=1
`
	_, err := parseProfilesIni(ini, "/some/dir")
	if err == nil {
		t.Error("expected error for empty profile list, got nil")
	}
}

// TestReadFirefoxCookies_RealDB tests against a small hand-crafted SQLite db
// committed in testdata/. This verifies the SQL query and jar population.
func TestReadFirefoxCookies_RealDB(t *testing.T) {
	dbPath := filepath.Join("testdata", "cookies.sqlite")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Skip("testdata/cookies.sqlite not present; skipping live db test")
	}

	jar, err := ReadFirefoxCookies(dbPath)
	if err != nil {
		t.Fatalf("ReadFirefoxCookies: %v", err)
	}
	if jar == nil {
		t.Error("expected non-nil jar")
	}
}

// TestCopyToTemp verifies that copyToTemp creates a readable copy.
func TestCopyToTemp(t *testing.T) {
	src := filepath.Join(t.TempDir(), "source.txt")
	if err := os.WriteFile(src, []byte("hello test"), 0644); err != nil {
		t.Fatal(err)
	}

	tmp, err := copyToTemp(src)
	if err != nil {
		t.Fatalf("copyToTemp: %v", err)
	}
	defer os.Remove(tmp)

	data, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatalf("read copy: %v", err)
	}
	if string(data) != "hello test" {
		t.Errorf("copy content: got %q, want %q", string(data), "hello test")
	}
}
