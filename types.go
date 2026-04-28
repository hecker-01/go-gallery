package gallery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// Message is a sealed sum type yielded by Client.Extract.
// Consumers type-switch over Directory, Media, and Queue.
type Message interface {
	isMessage()
}

// Directory signals that subsequent Media items should be written under Path.
type Directory struct {
	Path string
}

func (Directory) isMessage() {}

// Media carries a single downloadable item.
type Media struct {
	Info *MediaInfo
	// URL is the direct download URL (already resolved to best variant).
	URL string
}

func (Media) isMessage() {}

// Download streams the media to dest using the client's configured Downloader.
// It is a convenience method; callers may also use the URL directly.
func (m Media) Download(ctx context.Context, dest io.Writer, d Downloader, cfg DownloadConfig) error {
	return d.Download(ctx, m.URL, dest, cfg)
}

// Queue is a nested URL that Extract should recurse into.
type Queue struct {
	URL string
}

func (Queue) isMessage() {}

// AuthorInfo holds Twitter user fields embedded in MediaInfo.
type AuthorInfo struct {
	ID         string
	Name       string
	ScreenName string
}

// MediaInfo is the metadata record for a single downloaded item.
// Keywords() returns a flat map for use with Formatter and ExprFilter.
type MediaInfo struct {
	TweetID       string
	Author        AuthorInfo
	Date          time.Time
	Content       string
	MediaURL      string
	Extension     string
	Num           int
	Count         int
	FavoriteCount int
	RetweetCount  int
	ReplyCount    int
	QuoteCount    int
	Lang          string
	Hashtags      []string
	Mentions      []string
	IsRetweet     bool
	IsReply       bool
	IsQuote       bool
	Card          map[string]any
	Category      string
}

// Keywords returns a flat map of all MediaInfo fields for template evaluation
// and expression filters.
func (m *MediaInfo) Keywords() map[string]any {
	return map[string]any{
		"tweet_id":        m.TweetID,
		"author.name":     m.Author.Name,
		"author.id":       m.Author.ID,
		"author.screen_name": m.Author.ScreenName,
		"date":            m.Date,
		"content":         m.Content,
		"media_url":       m.MediaURL,
		"extension":       m.Extension,
		"num":             m.Num,
		"count":           m.Count,
		"favorite_count":  m.FavoriteCount,
		"retweet_count":   m.RetweetCount,
		"reply_count":     m.ReplyCount,
		"quote_count":     m.QuoteCount,
		"lang":            m.Lang,
		"hashtags":        m.Hashtags,
		"mentions":        m.Mentions,
		"is_retweet":      m.IsRetweet,
		"is_reply":        m.IsReply,
		"is_quote":        m.IsQuote,
		"category":        m.Category,
	}
}

// MarshalJSON produces the full JSON representation of the info, used by
// MetadataPostProcessor and GetJSON.
func (m *MediaInfo) MarshalJSON() ([]byte, error) {
	type alias struct {
		TweetID       string            `json:"tweet_id"`
		Author        AuthorInfo        `json:"author"`
		Date          string            `json:"date"`
		Content       string            `json:"content"`
		MediaURL      string            `json:"media_url"`
		Extension     string            `json:"extension"`
		Num           int               `json:"num"`
		Count         int               `json:"count"`
		FavoriteCount int               `json:"favorite_count"`
		RetweetCount  int               `json:"retweet_count"`
		ReplyCount    int               `json:"reply_count"`
		QuoteCount    int               `json:"quote_count"`
		Lang          string            `json:"lang"`
		Hashtags      []string          `json:"hashtags"`
		Mentions      []string          `json:"mentions"`
		IsRetweet     bool              `json:"is_retweet"`
		IsReply       bool              `json:"is_reply"`
		IsQuote       bool              `json:"is_quote"`
		Card          map[string]any    `json:"card,omitempty"`
		Category      string            `json:"category"`
	}
	a := alias{
		TweetID:       m.TweetID,
		Author:        m.Author,
		Date:          m.Date.UTC().Format(time.RFC3339),
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
	if a.Hashtags == nil {
		a.Hashtags = []string{}
	}
	if a.Mentions == nil {
		a.Mentions = []string{}
	}
	return json.Marshal(a)
}

// Result is returned by Client.Download summarising the completed operation.
type Result struct {
	TotalFiles   int
	SkippedFiles int // archive hits
	FailedFiles  int
	Errors       []error
	Duration     time.Duration
}

// Range selects a subset of items by 1-based index (e.g. "1-5,7,10-20").
type Range struct {
	raw string
}

// ParseRange parses a comma-separated range expression like "1-5,7,10-20".
func ParseRange(s string) (Range, error) {
	r := Range{raw: strings.TrimSpace(s)}
	// validate via Contains to surface parse errors eagerly
	_, err := r.segments()
	return r, err
}

type rangeSegment struct{ lo, hi int }

func (r Range) segments() ([]rangeSegment, error) {
	if r.raw == "" {
		return nil, nil
	}
	var segs []rangeSegment
	for _, part := range strings.Split(r.raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if idx := strings.Index(part, "-"); idx > 0 {
			lo, err := strconv.Atoi(part[:idx])
			if err != nil {
				return nil, fmt.Errorf("invalid range %q: %w", part, err)
			}
			hi, err := strconv.Atoi(part[idx+1:])
			if err != nil {
				return nil, fmt.Errorf("invalid range %q: %w", part, err)
			}
			segs = append(segs, rangeSegment{lo, hi})
		} else {
			n, err := strconv.Atoi(part)
			if err != nil {
				return nil, fmt.Errorf("invalid range %q: %w", part, err)
			}
			segs = append(segs, rangeSegment{n, n})
		}
	}
	return segs, nil
}

// Contains reports whether 1-based index n falls within the range.
func (r Range) Contains(n int) bool {
	segs, err := r.segments()
	if err != nil || segs == nil {
		return true // empty range means "all"
	}
	for _, s := range segs {
		if n >= s.lo && n <= s.hi {
			return true
		}
	}
	return false
}

// String returns the raw range expression.
func (r Range) String() string { return r.raw }

// DownloadConfig holds the resolved configuration for a single Download call.
type DownloadConfig struct {
	OutputDir      string
	// FlatDir, when true, strips any directory components from the formatted
	// filename so files land directly in OutputDir with no subdirectories.
	// Equivalent to gallery-dl's -D flag.
	FlatDir        bool
	FilenameFormat string
	Filter         Filter
	PostProcessors []PostProcessor
	Simulate       bool
	Range          *Range
	Downloader     Downloader
	// MinFileSize / MaxFileSize in bytes; 0 means no limit.
	MinFileSize int64
	MaxFileSize int64
}

// DownloadOption mutates a DownloadConfig.
type DownloadOption func(*DownloadConfig)

// WithOutputDir sets the base output directory. The filename format's
// directory components (e.g. {category}/{author.screen_name}/) are still
// created beneath it. Equivalent to gallery-dl's -d flag.
func WithOutputDir(dir string) DownloadOption {
	return func(c *DownloadConfig) { c.OutputDir = dir }
}

// WithFlatDir disables subdirectory creation — files are placed directly in
// OutputDir regardless of the filename format's directory components.
// Equivalent to gallery-dl's -D flag.
func WithFlatDir() DownloadOption {
	return func(c *DownloadConfig) { c.FlatDir = true }
}

// WithDirectOutputDir sets an exact output directory with no subdirectory
// structure. Equivalent to combining WithOutputDir and WithFlatDir.
func WithDirectOutputDir(dir string) DownloadOption {
	return func(c *DownloadConfig) {
		c.OutputDir = dir
		c.FlatDir = true
	}
}

// WithFilenameFormat overrides the filename formatter pattern.
func WithFilenameFormat(pattern string) DownloadOption {
	return func(c *DownloadConfig) { c.FilenameFormat = pattern }
}

// WithFilter sets the filter applied to each Media item.
func WithFilter(f Filter) DownloadOption {
	return func(c *DownloadConfig) { c.Filter = f }
}

// WithPostProcessors replaces the post-processor list.
func WithPostProcessors(pp ...PostProcessor) DownloadOption {
	return func(c *DownloadConfig) { c.PostProcessors = pp }
}

// WithSimulate when true drives the full extraction and filter pipeline but
// skips all network I/O and filesystem writes.
func WithSimulate(s bool) DownloadOption {
	return func(c *DownloadConfig) { c.Simulate = s }
}

// WithRange restricts which items are downloaded by 1-based index.
func WithRange(rv Range) DownloadOption {
	return func(c *DownloadConfig) { c.Range = &rv }
}

// WithDownloaderOpt injects a custom Downloader into this Download call.
func WithDownloaderOpt(d Downloader) DownloadOption {
	return func(c *DownloadConfig) { c.Downloader = d }
}

// Downloader is the interface that wraps media download behaviour.
// The root implementation (HTTPDownloader) lives in internal/downloader but
// the interface is public so consumers can substitute their own.
type Downloader interface {
	Download(ctx context.Context, url string, dest io.Writer, opts DownloadConfig) error
}
