// Package gallery provides a pluggable library for downloading image and video
// galleries from Twitter/X and other sites. The primary entry point is Client.
package gallery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/hecker-01/go-gallery/internal/extractor"
	"github.com/hecker-01/go-gallery/internal/ratelimit"

	// Blank-import Twitter extractors so their init() registers them.
	_ "github.com/hecker-01/go-gallery/internal/extractor/twitter"
)

// Client is the primary entry point. Construct it with NewClient.
// A zero-value Client is not valid; always use NewClient.
type Client struct {
	cfg         Config
	httpClient  *http.Client
	jar         http.CookieJar
	archive     Archive
	cache       Cache
	logger      *slog.Logger
	concurrency int
	rateLimitCb func(endpoint string, resetAt time.Time)
	downloader  Downloader
	rlRegistry  *ratelimit.Registry
	proxyURL    string
}

// Option configures a Client during construction.
type Option func(*Client)

// NewClient constructs a Client, applying all provided options.
// Unset fields fall back to DefaultConfig() values.
func NewClient(opts ...Option) *Client {
	c := &Client{
		cfg:         DefaultConfig(),
		concurrency: 4,
	}
	c.logger = slog.Default()

	for _, o := range opts {
		o(c)
	}

	// Build rate-limit registry after options so the callback is already set.
	origCb := c.rateLimitCb
	rlReg := ratelimit.New(func(endpoint string, resetAt time.Time) {
		if origCb != nil {
			origCb(endpoint, resetAt)
		}
	})
	c.rlRegistry = rlReg
	// Wrap rateLimitCb so 429 events also update the local registry.
	c.rateLimitCb = func(endpoint string, resetAt time.Time) {
		rlReg.On429(endpoint, resetAt)
		if origCb != nil {
			origCb(endpoint, resetAt)
		}
	}

	if c.httpClient == nil {
		transport := &http.Transport{
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   20,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ForceAttemptHTTP2:     true,
		}
		if c.proxyURL != "" {
			if pu, err := url.Parse(c.proxyURL); err == nil {
				transport.Proxy = http.ProxyURL(pu)
			}
		}
		c.httpClient = &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		}
	}
	if c.jar != nil {
		c.httpClient.Jar = c.jar
	}

	return c
}

// WithConfig sets the library Config. Option values applied after this call
// override the Config fields.
func WithConfig(cfg Config) Option {
	return func(c *Client) { c.cfg = cfg }
}

// WithCookies sets the cookie jar used for authenticated requests.
func WithCookies(jar http.CookieJar) Option {
	return func(c *Client) { c.jar = jar }
}

// WithCookiesFromBrowser extracts cookies from the named browser profile and
// injects them into the client. Supported values: "firefox".
// Construction fails silently and logs a warning if extraction fails; the
// client still works in guest (unauthenticated) mode.
func WithCookiesFromBrowser(browser string) Option {
	return func(c *Client) {
		jar, err := CookiesFromBrowser(browser)
		if err != nil {
			c.logger.Warn(fmt.Sprintf("could not extract browser cookies from %s: %v", browser, err))
			return
		}
		c.jar = jar
	}
}

// WithCookiesFromFile reads a Netscape cookies.txt file and injects the
// cookies into the client.
func WithCookiesFromFile(path string) Option {
	return func(c *Client) {
		jar, err := CookiesFromFile(path)
		if err != nil {
			c.logger.Warn(fmt.Sprintf("could not load cookies from %s: %v", path, err))
			return
		}
		c.jar = jar
	}
}

// WithProxy configures an HTTP proxy URL (e.g. "http://localhost:8080").
func WithProxy(rawURL string) Option {
	return func(c *Client) {
		c.proxyURL = rawURL
	}
}

// WithConcurrency sets the number of parallel media downloads.
func WithConcurrency(n int) Option {
	return func(c *Client) {
		if n > 0 {
			c.concurrency = n
			c.cfg.Downloader.Concurrency = n
		}
	}
}

// WithArchive injects an archive backend for skip-already-downloaded logic.
func WithArchive(a Archive) Option {
	return func(c *Client) { c.archive = a }
}

// WithLogger sets the structured logger. The library never calls log.Fatal or
// writes to stdout/stderr directly; all output goes through this logger.
func WithLogger(l *slog.Logger) Option {
	return func(c *Client) {
		if l != nil {
			c.logger = l
		}
	}
}

// WithHTTPClient replaces the underlying HTTP client entirely. The provided
// client's transport and timeout settings take precedence.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc != nil {
			c.httpClient = hc
		}
	}
}

// WithRateLimitCallback registers a callback that fires when a per-endpoint
// rate limit is hit or updated. fn is called from a background goroutine so
// it must be safe for concurrent use.
func WithRateLimitCallback(fn func(endpoint string, resetAt time.Time)) Option {
	return func(c *Client) { c.rateLimitCb = fn }
}

// WithDownloader replaces the media downloader. Consumers implement Downloader
// to intercept or entirely replace download behaviour.
func WithDownloader(d Downloader) Option {
	return func(c *Client) { c.downloader = d }
}

// Extract starts extraction of url and returns a channel of Messages and an
// error channel. The caller ranges over the message channel and decides what
// to do with each item (e.g. calling Download on Media items).
//
// Both channels are closed when extraction finishes or ctx is cancelled.
// A non-nil value on the error channel indicates a fatal extraction failure.
func (c *Client) Extract(ctx context.Context, url string) (<-chan Message, <-chan error) {
	msgs := make(chan Message)
	errs := make(chan error, 1)

	params := extractor.ClientParams{
		HTTP:        c.httpClient,
		Cookies:     c.jar,
		Cache:       c.cache,
		Logger:      c.logger,
		RateLimitCB: c.rateLimitCb,
		Concurrency: c.concurrency,
	}

	ex, ok := extractor.Dispatch(url, params)
	if !ok {
		go func() {
			defer close(msgs)
			defer close(errs)
			errs <- &InputError{Message: "no extractor found for URL: " + url}
		}()
		return msgs, errs
	}

	go func() {
		defer close(msgs)
		defer close(errs)
		for item := range ex.Items(ctx) {
			select {
			case msgs <- convertItem(item):
			case <-ctx.Done():
				return
			}
		}
	}()
	return msgs, errs
}

// convertItem translates an internal extractor.Item to a public gallery.Message.
func convertItem(item extractor.Item) Message {
	switch item.Kind {
	case extractor.KindDirectory:
		return Directory{Path: item.DirPath}
	case extractor.KindMedia:
		return Media{Info: convertMeta(item.Meta), URL: item.URL}
	case extractor.KindQueue:
		return Queue{URL: item.QueueURL}
	default:
		return Queue{URL: item.QueueURL}
	}
}

// convertMeta maps extractor.ItemMeta to the public gallery.MediaInfo.
func convertMeta(m *extractor.ItemMeta) *MediaInfo {
	if m == nil {
		return nil
	}
	return &MediaInfo{
		TweetID: m.TweetID,
		Author: AuthorInfo{
			ID:         m.Author.ID,
			Name:       m.Author.Name,
			ScreenName: m.Author.ScreenName,
		},
		Date:          m.Date,
		Content:       m.Content,
		MediaURL:      m.MediaURL,
		Extension:     m.Extension,
		Num:           m.Num,
		Count:         m.Count,
		FavoriteCount: m.FavoriteCount,
		RetweetCount:  m.RetweetCount,
		ReplyCount:    m.ReplyCount,
		QuoteCount:    m.QuoteCount,
		Lang:          m.Lang,
		Hashtags:      m.Hashtags,
		Mentions:      m.Mentions,
		IsRetweet:     m.IsRetweet,
		IsReply:       m.IsReply,
		IsQuote:       m.IsQuote,
		Card:          m.Card,
		Category:      m.Category,
	}
}

// Download is the batteries-included path. It runs extraction, creates
// directories, downloads files, checks the archive, runs post-processors, and
// returns a Result summary.
func (c *Client) Download(ctx context.Context, url string, opts ...DownloadOption) (Result, error) {
	cfg := DownloadConfig{
		OutputDir:      c.cfg.Output.Dir,
		FilenameFormat: c.cfg.Output.FilenameFormat,
		Downloader:     c.downloader,
	}
	for _, o := range opts {
		o(&cfg)
	}

	if cfg.FilenameFormat == "" {
		cfg.FilenameFormat = DefaultConfig().Output.FilenameFormat
	}
	if cfg.OutputDir == "" {
		cfg.OutputDir = "."
	}

	dl := cfg.Downloader
	if dl == nil {
		dl = &httpDownloader{client: c.httpClient}
	}

	start := time.Now()
	var (
		result Result
		mu     sync.Mutex
		wg     sync.WaitGroup
		sem    = make(chan struct{}, c.concurrency)
	)

	msgs, errs := c.Extract(ctx, url)

	for msg := range msgs {
		media, ok := msg.(Media)
		if !ok {
			continue
		}
		info := media.Info

		// Range filter.
		if cfg.Range != nil && !cfg.Range.Contains(info.Num) {
			continue
		}

		// User-supplied filter.
		if cfg.Filter != nil && !cfg.Filter.Accept(info) {
			continue
		}

		// Archive check.
		if c.archive != nil {
			archKey := info.TweetID + ":" + strconv.Itoa(info.Num)
			if has, _ := c.archive.Has(ctx, archKey); has {
				mu.Lock()
				result.SkippedFiles++
				mu.Unlock()
				continue
			}
		}

		mu.Lock()
		result.TotalFiles++
		mu.Unlock()

		if cfg.Simulate {
			c.logger.Info("[simulate] " + info.MediaURL)
			continue
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(mi *MediaInfo, mediaURL string) {
			defer wg.Done()
			defer func() { <-sem }()

			// Format destination path.
			kw := mi.Keywords()
			fname, err := NewFormatter(cfg.FilenameFormat)
			if err != nil {
				mu.Lock()
				result.FailedFiles++
				result.Errors = append(result.Errors, err)
				mu.Unlock()
				return
			}
			name := fname.Format(kw)
			if name == "" || name == "." {
				name = mi.TweetID + "_" + strconv.Itoa(mi.Num) + "." + mi.Extension
			}
			if cfg.FlatDir {
				name = filepath.Base(name)
			}

			destPath := filepath.Join(cfg.OutputDir, name)
			if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
				mu.Lock()
				result.FailedFiles++
				result.Errors = append(result.Errors, err)
				mu.Unlock()
				return
			}

			f, err := os.Create(destPath)
			if err != nil {
				mu.Lock()
				result.FailedFiles++
				result.Errors = append(result.Errors, err)
				mu.Unlock()
				return
			}

			dlErr := dl.Download(ctx, mediaURL, f, cfg)
			f.Close()
			if dlErr != nil {
				os.Remove(destPath)
				mu.Lock()
				result.FailedFiles++
				result.Errors = append(result.Errors, dlErr)
				mu.Unlock()
				_ = runPostProcessors(ctx, cfg.PostProcessors, destPath, mi, dlErr)
				return
			}

			if ppErr := runPostProcessors(ctx, cfg.PostProcessors, destPath, mi, nil); ppErr != nil {
				mu.Lock()
				result.Errors = append(result.Errors, ppErr)
				mu.Unlock()
			}

			if c.archive != nil {
				_ = c.archive.Put(ctx, mi.TweetID+":"+strconv.Itoa(mi.Num))
			}

			c.logger.Info(destPath)
		}(info, media.URL)
	}

	wg.Wait()

	// Drain the error channel (buffered size 1, closed after msgs closes).
	if extractErr := <-errs; extractErr != nil {
		return result, extractErr
	}

	result.Duration = time.Since(start)
	return result, nil
}

// GetURLs returns MediaInfo slices containing direct download URLs and metadata
// for all items reachable from url.
func (c *Client) GetURLs(ctx context.Context, url string) ([]*MediaInfo, error) {
	msgs, errs := c.Extract(ctx, url)
	var items []*MediaInfo
	for msg := range msgs {
		if m, ok := msg.(Media); ok {
			items = append(items, m.Info)
		}
	}
	if err := <-errs; err != nil {
		return items, err
	}
	return items, nil
}

// GetKeywords returns a flat map of template variables for the first Media item
// yielded by url. Equivalent to the -K / --get-keywords flag in gallery-dl.
func (c *Client) GetKeywords(ctx context.Context, url string) (map[string]any, error) {
	msgs, errs := c.Extract(ctx, url)
	// Drain remaining messages after finding first media.
	defer func() {
		for range msgs {
		}
		<-errs
	}()
	for msg := range msgs {
		if m, ok := msg.(Media); ok {
			return m.Info.Keywords(), nil
		}
	}
	if err := <-errs; err != nil {
		return nil, err
	}
	return nil, &InputError{Message: "no media items found for URL: " + url}
}

// GetJSON returns a channel of json.RawMessage — one per tweet's full metadata
// object. Equivalent to the -j flag.
func (c *Client) GetJSON(ctx context.Context, url string) (<-chan json.RawMessage, <-chan error) {
	out := make(chan json.RawMessage)
	outErrs := make(chan error, 1)

	go func() {
		defer close(out)
		defer close(outErrs)

		msgs, errs := c.Extract(ctx, url)
		for msg := range msgs {
			m, ok := msg.(Media)
			if !ok {
				continue
			}
			data, err := json.Marshal(m.Info)
			if err != nil {
				outErrs <- err
				// Drain msgs to let Extract goroutine finish.
				for range msgs {
				}
				<-errs
				return
			}
			select {
			case out <- json.RawMessage(data):
			case <-ctx.Done():
				for range msgs {
				}
				<-errs
				return
			}
		}
		if err := <-errs; err != nil {
			outErrs <- err
		}
	}()

	return out, outErrs
}

// RateLimitStatus returns the current rate-limit state for the named endpoint.
// endpoint is a GraphQL operation name, e.g. "UserTweets".
// Returns zeroed fields if the endpoint has not been seen yet.
func (c *Client) RateLimitStatus(endpoint string) RateLimitInfo {
	s := c.rlRegistry.Status(endpoint)
	return RateLimitInfo{
		Endpoint:  s.Endpoint,
		Limit:     s.Limit,
		Remaining: s.Remaining,
		ResetAt:   s.ResetAt,
	}
}

// httpDownloader is the default Downloader implementation used when none is
// injected. It performs a simple streaming HTTP GET.
type httpDownloader struct {
	client *http.Client
}

func (d *httpDownloader) Download(ctx context.Context, rawURL string, dest io.Writer, _ DownloadConfig) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &HttpError{StatusCode: resp.StatusCode, URL: rawURL}
	}
	_, err = io.Copy(dest, resp.Body)
	return err
}

// RateLimitInfo is a snapshot of a single endpoint's rate-limit state.
type RateLimitInfo struct {
	Endpoint  string
	Limit     int
	Remaining int
	ResetAt   time.Time
}

// Close shuts down worker pools, flushes the archive, and releases resources.
// It should be called via defer after NewClient.
func (c *Client) Close() error {
	if c.archive != nil {
		return c.archive.Close()
	}
	return nil
}
