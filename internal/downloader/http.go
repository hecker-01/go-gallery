package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/hecker-01/go-gallery/internal/galleryerrs"
)

// Config carries the per-download options forwarded from DownloadConfig.
type Config struct {
	Retries     int
	Resume      bool
	MinFileSize int64
	MaxFileSize int64
	Headers     map[string]string
}

// HTTPDownloader downloads media over HTTP with streaming, .part-file resumption,
// size validation, and MIME checking. It satisfies the gallery.Downloader
// interface when used from the root package.
type HTTPDownloader struct {
	client *http.Client
}

// New returns an HTTPDownloader using the provided client. If client is nil a
// new one is constructed via NewHTTPClient.
func New(client *http.Client) *HTTPDownloader {
	if client == nil {
		client = NewHTTPClient(0)
	}
	return &HTTPDownloader{client: client}
}

// DownloadToFile streams url into destPath. On failure the .part file is kept
// when cfg.Resume is true so a future call can resume it.
func (d *HTTPDownloader) DownloadToFile(ctx context.Context, url, destPath string, cfg Config) error {
	partPath := destPath + ".part"
	retries := cfg.Retries
	if retries <= 0 {
		retries = 4
	}

	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 1s, 2s, 4s …
			backoff := time.Duration(1<<(attempt-1)) * time.Second
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}

		if err := d.attemptDownload(ctx, url, destPath, partPath, cfg); err != nil {
			lastErr = err
			if isTransient(err) {
				continue
			}
			break
		}
		return nil
	}
	return lastErr
}

func (d *HTTPDownloader) attemptDownload(ctx context.Context, url, destPath, partPath string, cfg Config) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}

	// Resumption: detect existing .part file and send Range header.
	var partSize int64
	if cfg.Resume {
		if fi, err := os.Stat(partPath); err == nil {
			partSize = fi.Size()
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", partSize))
		}
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	// 416 Range Not Satisfiable — the part file is already complete.
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		return os.Rename(partPath, destPath)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return galleryerrs.ClassifyHTTPStatus(resp.StatusCode, url, nil)
	}

	// Size validation using Content-Length (pre-download).
	if cl := resp.ContentLength; cl > 0 {
		total := partSize + cl
		if cfg.MinFileSize > 0 && total < cfg.MinFileSize {
			return fmt.Errorf("content too small: %d bytes (min %d)", total, cfg.MinFileSize)
		}
		if cfg.MaxFileSize > 0 && total > cfg.MaxFileSize {
			return fmt.Errorf("content too large: %d bytes (max %d)", total, cfg.MaxFileSize)
		}
	}

	// Open the .part file — append if resuming, create/truncate otherwise.
	flag := os.O_CREATE | os.O_WRONLY
	if resp.StatusCode == http.StatusPartialContent {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
		partSize = 0
	}

	if err := os.MkdirAll(filepath.Dir(partPath), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(partPath, flag, 0644)
	if err != nil {
		return err
	}

	// MIME check: read the first 512 bytes from the response and check them.
	sniff := make([]byte, 512)
	n, err := io.ReadFull(resp.Body, sniff)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		f.Close()
		if !cfg.Resume {
			os.Remove(partPath)
		}
		return fmt.Errorf("reading response for MIME sniff: %w", err)
	}
	sniff = sniff[:n]
	mime := http.DetectContentType(sniff)
	if !isAllowedMIME(mime) {
		f.Close()
		if !cfg.Resume {
			os.Remove(partPath)
		}
		return fmt.Errorf("unexpected content type %q for %s", mime, url)
	}

	// Write the sniffed bytes then stream the rest.
	if _, err := f.Write(sniff); err != nil {
		f.Close()
		if !cfg.Resume {
			os.Remove(partPath)
		}
		return err
	}

	written, copyErr := io.Copy(f, resp.Body)
	closeErr := f.Close()

	if copyErr != nil || closeErr != nil {
		if !cfg.Resume {
			os.Remove(partPath)
		}
		if copyErr != nil {
			return fmt.Errorf("streaming download: %w", copyErr)
		}
		return closeErr
	}

	// Post-download size validation.
	total := partSize + int64(n) + written
	if cfg.MinFileSize > 0 && total < cfg.MinFileSize {
		if !cfg.Resume {
			os.Remove(partPath)
		}
		return fmt.Errorf("downloaded file too small: %d bytes (min %d)", total, cfg.MinFileSize)
	}
	if cfg.MaxFileSize > 0 && total > cfg.MaxFileSize {
		if !cfg.Resume {
			os.Remove(partPath)
		}
		return fmt.Errorf("downloaded file too large: %d bytes (max %d)", total, cfg.MaxFileSize)
	}

	return os.Rename(partPath, destPath)
}

// ContentLength returns the Content-Length for url without downloading the body.
func (d *HTTPDownloader) ContentLength(ctx context.Context, url string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	if s := resp.Header.Get("Content-Length"); s != "" {
		n, _ := strconv.ParseInt(s, 10, 64)
		return n, nil
	}
	return resp.ContentLength, nil
}

// isTransient reports whether err should trigger a retry.
// Permanent failures (404, 403, 410, 451, 401) are not retried.
func isTransient(err error) bool {
	if err == nil {
		return false
	}
	// Permanent client-side errors — never retry these.
	var nfe *galleryerrs.NotFoundError
	if errors.As(err, &nfe) {
		return false
	}
	var authzErr *galleryerrs.AuthorizationError
	if errors.As(err, &authzErr) {
		return false
	}
	var authnErr *galleryerrs.AuthenticationError
	if errors.As(err, &authnErr) {
		return false
	}
	// Network-level errors that implement Temporary() are transient.
	var e interface{ Temporary() bool }
	if errors.As(err, &e) && e.Temporary() {
		return true
	}
	return false
}

// isAllowedMIME reports whether the MIME type is something we expect for
// image or video content. HTML / JSON indicates an error page.
func isAllowedMIME(mime string) bool {
	allowed := []string{
		"image/jpeg", "image/png", "image/gif", "image/webp",
		"video/mp4", "video/webm", "video/quicktime", "video/x-m4v",
		"application/octet-stream", // some CDNs serve binary with this
	}
	for _, a := range allowed {
		if mime == a {
			return true
		}
	}
	return false
}
