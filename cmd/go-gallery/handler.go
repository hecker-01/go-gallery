package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
)

// ANSI color codes, matching gallery-dl's defaults.
const (
	colorReset  = "\033[0m"
	colorGray   = "\033[0;37m"  // debug
	colorWhite  = "\033[1;37m"  // info
	colorYellow = "\033[1;33m"  // warning
	colorRed    = "\033[1;31m"  // error
)

// levelQuiet is a sentinel level above all real log levels; nothing is emitted.
const levelQuiet = slog.Level(100)

// galleryHandler is a slog.Handler that formats records in gallery-dl's style:
//
//	[source][level] message
//
// Colors are auto-detected (enabled when the output is a terminal and NO_COLOR
// is not set). The source name defaults to "go-gallery" but can be overridden
// per-logger via logger.With("source", "twitter").
type galleryHandler struct {
	mu    sync.Mutex
	out   io.Writer
	level slog.Leveler
	color bool
	attrs []slog.Attr
}

func newGalleryHandler(out io.Writer, level slog.Leveler) *galleryHandler {
	color := false
	if _, noColor := os.LookupEnv("NO_COLOR"); !noColor {
		if f, ok := out.(*os.File); ok {
			color = isTerminal(f)
		}
	}
	return &galleryHandler{out: out, level: level, color: color}
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func (h *galleryHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

func (h *galleryHandler) Handle(_ context.Context, r slog.Record) error {
	// Merge pre-attached attrs with per-record attrs.
	allAttrs := make([]slog.Attr, 0, len(h.attrs)+r.NumAttrs())
	allAttrs = append(allAttrs, h.attrs...)
	r.Attrs(func(a slog.Attr) bool {
		allAttrs = append(allAttrs, a)
		return true
	})

	// Extract source name; default to "go-gallery".
	source := "go-gallery"
	remaining := allAttrs[:0]
	for _, a := range allAttrs {
		if a.Key == "source" {
			source = a.Value.String()
		} else {
			remaining = append(remaining, a)
		}
	}

	levelStr := levelName(r.Level)
	var buf bytes.Buffer
	if h.color {
		fmt.Fprintf(&buf, "%s[%s][%s]%s %s", levelColor(r.Level), source, levelStr, colorReset, r.Message)
	} else {
		fmt.Fprintf(&buf, "[%s][%s] %s", source, levelStr, r.Message)
	}
	for _, a := range remaining {
		fmt.Fprintf(&buf, " %s=%v", a.Key, a.Value.Any())
	}
	buf.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.out.Write(buf.Bytes())
	return err
}

func (h *galleryHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	merged := make([]slog.Attr, len(h.attrs)+len(attrs))
	copy(merged, h.attrs)
	copy(merged[len(h.attrs):], attrs)
	return &galleryHandler{out: h.out, level: h.level, color: h.color, attrs: merged}
}

func (h *galleryHandler) WithGroup(_ string) slog.Handler { return h }

func levelName(level slog.Level) string {
	switch {
	case level < slog.LevelInfo:
		return "debug"
	case level < slog.LevelWarn:
		return "info"
	case level < slog.LevelError:
		return "warning"
	default:
		return "error"
	}
}

func levelColor(level slog.Level) string {
	switch {
	case level < slog.LevelInfo:
		return colorGray
	case level < slog.LevelWarn:
		return colorWhite
	case level < slog.LevelError:
		return colorYellow
	default:
		return colorRed
	}
}
