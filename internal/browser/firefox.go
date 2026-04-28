// Package browser provides cookie extraction helpers for locally-installed
// web browsers. Only Firefox is supported in v0.
package browser

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	_ "modernc.org/sqlite"
)

// Firefox reads Twitter/X cookies from the default Firefox profile on the
// current machine and returns them in an http.CookieJar.
//
// Firefox stores cookies in plain SQLite (no encryption), so no password is
// required. The function copies the live cookies.sqlite to a temporary file
// before reading it, because Firefox holds an exclusive write lock on the
// live database.
func Firefox() (*cookiejar.Jar, error) {
	profileDir, err := findDefaultFirefoxProfile()
	if err != nil {
		return nil, fmt.Errorf("browser: firefox: locate profile: %w", err)
	}
	return ReadFirefoxCookies(filepath.Join(profileDir, "cookies.sqlite"))
}

// ReadFirefoxCookies opens a Firefox cookies.sqlite file (or a copy of one)
// and returns the Twitter/X cookies in a jar.
func ReadFirefoxCookies(dbPath string) (*cookiejar.Jar, error) {
	// Copy to a temp file so we never conflict with a running Firefox.
	tmp, err := copyToTemp(dbPath)
	if err != nil {
		return nil, fmt.Errorf("browser: firefox: copy db: %w", err)
	}
	defer os.Remove(tmp)

	db, err := sql.Open("sqlite", tmp)
	if err != nil {
		return nil, fmt.Errorf("browser: firefox: open db: %w", err)
	}
	defer db.Close()

	rows, err := db.QueryContext(context.Background(),
		`SELECT host, path, name, value, expiry, isSecure, isHttpOnly
		 FROM moz_cookies
		 WHERE host LIKE '%twitter.com' OR host LIKE '%x.com'
		    OR host LIKE '.twitter.com' OR host LIKE '.x.com'`,
	)
	if err != nil {
		return nil, fmt.Errorf("browser: firefox: query cookies: %w", err)
	}
	defer rows.Close()

	jar, _ := cookiejar.New(nil)
	var cookies []*http.Cookie

	for rows.Next() {
		var host, path, name, value string
		var expiry int64
		var isSecure, isHTTPOnly bool

		if err := rows.Scan(&host, &path, &name, &value, &expiry, &isSecure, &isHTTPOnly); err != nil {
			continue
		}

		cookies = append(cookies, &http.Cookie{
			Name:     name,
			Value:    value,
			Path:     path,
			Domain:   host,
			Secure:   isSecure,
			HttpOnly: isHTTPOnly,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("browser: firefox: scan cookies: %w", err)
	}

	// Set cookies for both twitter.com and x.com domains.
	for _, domain := range []string{"https://twitter.com/", "https://x.com/"} {
		u, _ := url.Parse(domain)
		jar.SetCookies(u, cookies)
	}
	return jar, nil
}

// findDefaultFirefoxProfile locates the default Firefox profile directory on
// the current operating system by reading profiles.ini.
func findDefaultFirefoxProfile() (string, error) {
	configDir, err := firefoxConfigDir()
	if err != nil {
		return "", err
	}

	iniPath := filepath.Join(configDir, "profiles.ini")
	data, err := os.ReadFile(iniPath)
	if err != nil {
		return "", fmt.Errorf("read profiles.ini at %s: %w", iniPath, err)
	}

	profile, err := parseProfilesIni(string(data), configDir)
	if err != nil {
		return "", err
	}
	return profile, nil
}

// firefoxConfigDir returns the platform-appropriate Firefox config directory.
func firefoxConfigDir() (string, error) {
	switch runtime.GOOS {
	case "windows":
		appdata := os.Getenv("APPDATA")
		if appdata == "" {
			return "", fmt.Errorf("APPDATA not set")
		}
		return filepath.Join(appdata, "Mozilla", "Firefox"), nil
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", "Firefox"), nil
	default: // Linux and others
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".mozilla", "firefox"), nil
	}
}

// parseProfilesIni finds the default profile path from a profiles.ini string.
// configDir is prepended for relative paths.
func parseProfilesIni(ini, configDir string) (string, error) {
	type section struct {
		name      string
		fields    map[string]string
	}

	var sections []section
	var cur *section

	for _, line := range strings.Split(ini, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			sections = append(sections, section{
				name:   line[1 : len(line)-1],
				fields: make(map[string]string),
			})
			cur = &sections[len(sections)-1]
			continue
		}
		if cur != nil {
			if idx := strings.IndexByte(line, '='); idx > 0 {
				k := strings.TrimSpace(line[:idx])
				v := strings.TrimSpace(line[idx+1:])
				cur.fields[k] = v
			}
		}
	}

	// Prefer the profile with Default=1 in an [Install...] section.
	// Fall back to the first [Profile...] section with Default=1.
	// Then fall back to the first [Profile...] section.
	var fallback string
	for i := range sections {
		s := &sections[i]
		if strings.HasPrefix(s.name, "Install") {
			if def, ok := s.fields["Default"]; ok {
				return absProfilePath(def, configDir), nil
			}
		}
	}
	for i := range sections {
		s := &sections[i]
		if !strings.HasPrefix(s.name, "Profile") {
			continue
		}
		pathVal := s.fields["Path"]
		if pathVal == "" {
			continue
		}
		abs := absProfilePath(pathVal, configDir)
		if s.fields["Default"] == "1" {
			return abs, nil
		}
		if fallback == "" {
			fallback = abs
		}
	}
	if fallback != "" {
		return fallback, nil
	}
	return "", fmt.Errorf("no Firefox profile found in profiles.ini")
}

func absProfilePath(path, configDir string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(configDir, path)
}

// copyToTemp creates a temporary copy of src and returns its path.
func copyToTemp(src string) (string, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp("", "go-gallery-firefox-*.sqlite")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}
