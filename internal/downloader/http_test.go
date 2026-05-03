package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hecker-01/go-gallery/internal/galleryerrs"
)

func serveJPEG(t *testing.T, data []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Content-Length", itoa(len(data)))
		w.WriteHeader(http.StatusOK)
		w.Write(data)
	}))
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}

// jpegHeader is a minimal valid JPEG magic bytes prefix.
var jpegHeader = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 16, 'J', 'F', 'I', 'F',
	0, 1, 1, 0, 0, 1, 0, 1, 0, 0,
	// padding to make it a realistic sniff target
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
}

func makeJPEGPayload(size int) []byte {
	data := make([]byte, size)
	copy(data, jpegHeader)
	return data
}

func TestHTTPDownloader_Basic(t *testing.T) {
	payload := makeJPEGPayload(1024)
	srv := serveJPEG(t, payload)
	defer srv.Close()

	d := New(srv.Client())
	dest := filepath.Join(t.TempDir(), "out.jpg")
	cfg := Config{Retries: 0}

	if err := d.DownloadToFile(context.Background(), srv.URL, dest, cfg); err != nil {
		t.Fatalf("DownloadToFile: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(payload) {
		t.Errorf("file size: got %d, want %d", len(got), len(payload))
	}
}

func TestHTTPDownloader_HTTP404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	d := New(srv.Client())
	dest := filepath.Join(t.TempDir(), "out.jpg")
	err := d.DownloadToFile(context.Background(), srv.URL, dest, Config{Retries: 0})
	if err == nil {
		t.Error("expected error for HTTP 404, got nil")
	}
	var nfe *galleryerrs.NotFoundError
	if !errors.As(err, &nfe) {
		t.Errorf("expected *NotFoundError for 404, got %T: %v", err, err)
	}
	if nfe.Reason != "deleted" {
		t.Errorf("Reason = %q, want %q", nfe.Reason, "deleted")
	}
}

func TestHTTPDownloader_HTTP403(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	d := New(srv.Client())
	dest := filepath.Join(t.TempDir(), "out.jpg")
	err := d.DownloadToFile(context.Background(), srv.URL, dest, Config{Retries: 0})
	if err == nil {
		t.Error("expected error for HTTP 403, got nil")
	}
	var authzErr *galleryerrs.AuthorizationError
	if !errors.As(err, &authzErr) {
		t.Errorf("expected *AuthorizationError for 403, got %T: %v", err, err)
	}
}

func TestHTTPDownloader_HTTP410(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer srv.Close()

	d := New(srv.Client())
	dest := filepath.Join(t.TempDir(), "out.jpg")
	err := d.DownloadToFile(context.Background(), srv.URL, dest, Config{Retries: 0})
	if err == nil {
		t.Error("expected error for HTTP 410, got nil")
	}
	var nfe *galleryerrs.NotFoundError
	if !errors.As(err, &nfe) {
		t.Errorf("expected *NotFoundError for 410, got %T: %v", err, err)
	}
	if nfe.Reason != "gone" {
		t.Errorf("Reason = %q, want %q", nfe.Reason, "gone")
	}
}

func TestHTTPDownloader_HTTP451(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnavailableForLegalReasons)
	}))
	defer srv.Close()

	d := New(srv.Client())
	dest := filepath.Join(t.TempDir(), "out.jpg")
	err := d.DownloadToFile(context.Background(), srv.URL, dest, Config{Retries: 0})
	if err == nil {
		t.Error("expected error for HTTP 451, got nil")
	}
	var nfe *galleryerrs.NotFoundError
	if !errors.As(err, &nfe) {
		t.Errorf("expected *NotFoundError for 451, got %T: %v", err, err)
	}
	if nfe.Reason != "dmca" {
		t.Errorf("Reason = %q, want %q", nfe.Reason, "dmca")
	}
}

func TestHTTPDownloader_HTTP404_NoRetry(t *testing.T) {
	var callCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	d := New(srv.Client())
	dest := filepath.Join(t.TempDir(), "out.jpg")
	// With retries=3, a 404 should NOT retry because isTransient returns false.
	_ = d.DownloadToFile(context.Background(), srv.URL, dest, Config{Retries: 3})
	if callCount != 1 {
		t.Errorf("expected exactly 1 request for 404 (no retry), got %d", callCount)
	}
}

func TestHTTPDownloader_MIME_Reject(t *testing.T) {
	// Serve HTML — should be rejected.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "<html><body>error page</body></html>")
	}))
	defer srv.Close()

	d := New(srv.Client())
	dest := filepath.Join(t.TempDir(), "out.jpg")
	err := d.DownloadToFile(context.Background(), srv.URL, dest, Config{Retries: 0})
	if err == nil {
		t.Error("expected MIME rejection, got nil")
	}
	// .part file should be removed when Resume is false.
	if _, statErr := os.Stat(dest + ".part"); statErr == nil {
		t.Error("part file should be removed after failed non-resume download")
	}
}

func TestHTTPDownloader_Resume(t *testing.T) {
	const totalSize = 2048
	payload := makeJPEGPayload(totalSize)

	var rangeRequests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rangeHdr := r.Header.Get("Range")
		if rangeHdr == "bytes=1024-" {
			rangeRequests++
			w.Header().Set("Content-Type", "image/jpeg")
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 1024-%d/%d", totalSize-1, totalSize))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(payload[1024:])
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.WriteHeader(http.StatusOK)
		w.Write(payload)
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "out.jpg")
	partPath := dest + ".part"

	// Pre-create the .part file with the first half.
	if err := os.WriteFile(partPath, payload[:1024], 0644); err != nil {
		t.Fatal(err)
	}

	d := New(srv.Client())
	cfg := Config{Retries: 0, Resume: true}
	if err := d.DownloadToFile(context.Background(), srv.URL, dest, cfg); err != nil {
		t.Fatalf("DownloadToFile (resume): %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != totalSize {
		t.Errorf("resumed file size: got %d, want %d", len(got), totalSize)
	}
	if rangeRequests != 1 {
		t.Errorf("expected 1 Range request, got %d", rangeRequests)
	}
}

func TestHTTPDownloader_MinSize(t *testing.T) {
	payload := makeJPEGPayload(100)
	srv := serveJPEG(t, payload)
	defer srv.Close()

	d := New(srv.Client())
	dest := filepath.Join(t.TempDir(), "out.jpg")
	err := d.DownloadToFile(context.Background(), srv.URL, dest, Config{
		Retries:     0,
		MinFileSize: 10000, // much larger than payload
	})
	if err == nil {
		t.Error("expected too-small error, got nil")
	}
}

func TestYTDLP_NotAvailable(t *testing.T) {
	y := &YTDLPDownloader{path: ""}
	if y.IsAvailable() {
		t.Error("should not be available with empty path")
	}
	err := y.Download(context.Background(), "https://example.com/v", io.Discard, Config{})
	if err != ErrYTDLPNotFound {
		t.Errorf("expected ErrYTDLPNotFound, got %v", err)
	}
}
