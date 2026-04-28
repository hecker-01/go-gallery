package gallery

import (
	"bufio"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hecker-01/go-gallery/internal/browser"
)

// CookiesFromFile parses a Netscape-format cookies.txt file and returns a
// cookie jar containing its entries. Lines beginning with '#' are comments.
// Returns an InputError for unknown formats.
func CookiesFromFile(path string) (http.CookieJar, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, &InputError{Field: "path", Message: fmt.Sprintf("open cookies file: %v", err)}
	}
	defer f.Close()

	jar, _ := cookiejar.New(nil)
	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 7 {
			continue
		}
		domain := fields[0]
		// fields[1] is domain flag (TRUE/FALSE — include subdomains)
		path := fields[2]
		secure := strings.EqualFold(fields[3], "true")
		var expires time.Time
		if exp, err := strconv.ParseInt(fields[4], 10, 64); err == nil && exp > 0 {
			expires = time.Unix(exp, 0)
		}
		name := fields[5]
		value := fields[6]

		// Determine the scheme from the secure flag.
		scheme := "http"
		if secure {
			scheme = "https"
		}

		// Build the URL for SetCookies. Use the domain (strip leading dot).
		host := strings.TrimPrefix(domain, ".")
		u := &url.URL{
			Scheme: scheme,
			Host:   host,
			Path:   path,
		}

		ck := &http.Cookie{
			Name:    name,
			Value:   value,
			Path:    path,
			Domain:  domain,
			Expires: expires,
			Secure:  secure,
		}
		jar.SetCookies(u, []*http.Cookie{ck})
	}
	if err := scanner.Err(); err != nil {
		return nil, &InputError{Field: "path", Message: fmt.Sprintf("read cookies file: %v", err)}
	}
	return jar, nil
}

// CookiesFromBrowser extracts Twitter/X cookies from a locally-installed
// browser's profile database.
//
// Supported browsers: "firefox". All other values return an InputError listing
// supported options. Consumers should fall back to CookiesFromFile for other
// browsers.
func CookiesFromBrowser(browserName string) (http.CookieJar, error) {
	switch strings.ToLower(strings.TrimSpace(browserName)) {
	case "firefox":
		jar, err := browser.Firefox()
		if err != nil {
			return nil, &InputError{Field: "browser", Message: fmt.Sprintf("extract Firefox cookies: %v", err)}
		}
		return jar, nil
	default:
		return nil, &InputError{
			Field: "browser",
			Message: fmt.Sprintf(
				"unsupported browser %q — only \"firefox\" is supported in v0; "+
					"use WithCookiesFromFile for other browsers",
				browserName,
			),
		}
	}
}

