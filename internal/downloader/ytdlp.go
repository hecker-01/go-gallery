package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
)

// ErrYTDLPNotFound is returned when yt-dlp is not available in PATH.
var ErrYTDLPNotFound = errors.New("yt-dlp not found in PATH; install it or use HTTPDownloader")

// YTDLPDownloader wraps the yt-dlp CLI for HLS and complex video streams.
// It is feature-flagged: if yt-dlp is not installed at construction time,
// every Download call returns ErrYTDLPNotFound — no panic.
type YTDLPDownloader struct {
	path string // empty when not available
}

// NewYTDLP detects yt-dlp at construction time via exec.LookPath.
// A non-nil error is NOT returned when yt-dlp is missing; use IsAvailable to
// check before calling Download.
func NewYTDLP() *YTDLPDownloader {
	p, _ := exec.LookPath("yt-dlp")
	return &YTDLPDownloader{path: p}
}

// IsAvailable reports whether yt-dlp was found at construction time.
func (y *YTDLPDownloader) IsAvailable() bool { return y.path != "" }

// Download pipes url through yt-dlp and streams the output to dest.
// Returns ErrYTDLPNotFound when yt-dlp is not installed.
func (y *YTDLPDownloader) Download(ctx context.Context, url string, dest io.Writer, _ Config) error {
	if !y.IsAvailable() {
		return ErrYTDLPNotFound
	}
	cmd := exec.CommandContext(ctx, y.path,
		"--no-playlist",
		"--no-warnings",
		"-o", "-",
		url,
	)
	cmd.Stdout = dest
	var stderr errBuf
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := stderr.String()
		if msg != "" {
			return fmt.Errorf("yt-dlp: %s: %w", msg, err)
		}
		return fmt.Errorf("yt-dlp: %w", err)
	}
	return nil
}

// errBuf captures up to 4 KiB of yt-dlp stderr for error reporting.
type errBuf struct {
	buf []byte
}

func (b *errBuf) Write(p []byte) (int, error) {
	if len(b.buf) < 4096 {
		remain := 4096 - len(b.buf)
		if len(p) < remain {
			remain = len(p)
		}
		b.buf = append(b.buf, p[:remain]...)
	}
	return len(p), nil
}

func (b *errBuf) String() string { return string(b.buf) }
