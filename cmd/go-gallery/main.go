// Command go-gallery is a reference CLI for the go-gallery library.
//
// Usage:
//
//	go-gallery [flags] URL...
//
// Flags:
//
//	-g                    Print direct media URLs and exit (GetURLs mode)
//	-j                    Print per-item JSON and exit (GetJSON mode)
//	-K                    Print template keywords for first item and exit
//	-simulate             Run extraction and filtering but skip all I/O
//	-o DIR                Output directory (default: current directory)
//	-f PATTERN            Filename formatter pattern
//	--concurrency N       Number of parallel downloads (default: 4)
//	--cookies-from-browser BROWSER  Import cookies from browser profile (firefox)
//	--cookies-from-file PATH        Import cookies from Netscape cookies.txt
//	--filter EXPR         Expression filter (github.com/expr-lang/expr syntax)
//	--config PATH         Path to YAML/TOML/JSON config file
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	gallery "github.com/hecker-01/go-gallery"
)

func main() {
	os.Exit(run())
}

func run() int {
	// ── Flags ─────────────────────────────────────────────────────────────────
	getURLs := flag.Bool("g", false, "print direct media URLs and exit")
	getJSON := flag.Bool("j", false, "print per-item JSON to stdout and exit")
	getKeywords := flag.Bool("K", false, "print template keywords for first item and exit")
	simulate := flag.Bool("simulate", false, "run full pipeline but skip network/filesystem I/O")
	verbose := flag.Bool("verbose", false, "enable debug-level logging")
	outputDir := flag.String("o", ".", "output directory")
	filenameFormat := flag.String("f", "", "filename formatter pattern (overrides config)")
	concurrency := flag.Int("concurrency", 4, "number of parallel downloads")
	cookiesBrowser := flag.String("cookies-from-browser", "", "import cookies from browser profile (firefox)")
	cookiesFile := flag.String("cookies-from-file", "", "import cookies from Netscape cookies.txt")
	filterExpr := flag.String("filter", "", "expression filter (expr-lang syntax)")
	configPath := flag.String("config", "", "path to YAML/TOML/JSON config file")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: go-gallery [flags] URL...\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	urls := flag.Args()
	if len(urls) == 0 {
		flag.Usage()
		return 1
	}

	// ── Logger ────────────────────────────────────────────────────────────────
	logLevel := slog.LevelInfo
	if *verbose {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel,
	}))

	// ── Client options ────────────────────────────────────────────────────────
	opts := []gallery.Option{
		gallery.WithConcurrency(*concurrency),
		gallery.WithLogger(logger),
	}

	if *configPath != "" {
		cfg, err := gallery.LoadConfig(*configPath)
		if err != nil {
			logger.Error("failed to load config", "path", *configPath, "error", err)
			return 1
		}
		opts = append(opts, gallery.WithConfig(cfg))
	}

	if *cookiesBrowser != "" {
		if *cookiesBrowser != "firefox" {
			logger.Error("unsupported browser for cookie extraction; only 'firefox' is supported",
				"browser", *cookiesBrowser)
			return 1
		}
		opts = append(opts, gallery.WithCookiesFromBrowser(*cookiesBrowser))
	}
	if *cookiesFile != "" {
		opts = append(opts, gallery.WithCookiesFromFile(*cookiesFile))
	}

	// ── Filter ────────────────────────────────────────────────────────────────
	var filter gallery.Filter
	if *filterExpr != "" {
		ef, err := gallery.NewExprFilter(*filterExpr)
		if err != nil {
			logger.Error("invalid filter expression", "expr", *filterExpr, "error", err)
			return 1
		}
		filter = ef
	}

	// ── Download options ──────────────────────────────────────────────────────
	dlOpts := []gallery.DownloadOption{
		gallery.WithOutputDir(*outputDir),
		gallery.WithSimulate(*simulate),
	}
	if *filenameFormat != "" {
		dlOpts = append(dlOpts, gallery.WithFilenameFormat(*filenameFormat))
	}
	if filter != nil {
		dlOpts = append(dlOpts, gallery.WithFilter(filter))
	}

	// ── Context with OS signal cancellation ───────────────────────────────────
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	client := gallery.NewClient(opts...)
	defer client.Close()

	// ── Mode dispatch ─────────────────────────────────────────────────────────
	exitCode := 0
	for _, rawURL := range urls {
		switch {
		case *getURLs:
			if err := runGetURLs(ctx, client, rawURL, logger); err != nil {
				logger.Error("GetURLs failed", "url", rawURL, "error", err)
				exitCode = 1
			}

		case *getKeywords:
			if err := runGetKeywords(ctx, client, rawURL, logger); err != nil {
				logger.Error("GetKeywords failed", "url", rawURL, "error", err)
				exitCode = 1
			}

		case *getJSON:
			if err := runGetJSON(ctx, client, rawURL, logger); err != nil {
				logger.Error("GetJSON failed", "url", rawURL, "error", err)
				exitCode = 1
			}

		default:
			if err := runDownload(ctx, client, rawURL, dlOpts, logger); err != nil {
				logger.Error("Download failed", "url", rawURL, "error", err)
				exitCode = 1
			}
		}
	}
	return exitCode
}

func runGetURLs(ctx context.Context, client *gallery.Client, rawURL string, logger *slog.Logger) error {
	items, err := client.GetURLs(ctx, rawURL)
	for _, info := range items {
		fmt.Println(info.MediaURL)
	}
	return err
}

func runGetKeywords(ctx context.Context, client *gallery.Client, rawURL string, logger *slog.Logger) error {
	kw, err := client.GetKeywords(ctx, rawURL)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(kw)
}

func runGetJSON(ctx context.Context, client *gallery.Client, rawURL string, logger *slog.Logger) error {
	msgs, errs := client.GetJSON(ctx, rawURL)
	enc := json.NewEncoder(os.Stdout)
	for msg := range msgs {
		if err := enc.Encode(msg); err != nil {
			logger.Warn("JSON encode error", "error", err)
		}
	}
	return <-errs
}

func runDownload(ctx context.Context, client *gallery.Client, rawURL string, opts []gallery.DownloadOption, logger *slog.Logger) error {
	result, err := client.Download(ctx, rawURL, opts...)
	logger.Info("download complete",
		"total", result.TotalFiles,
		"skipped", result.SkippedFiles,
		"failed", result.FailedFiles,
		"duration", result.Duration,
	)
	if len(result.Errors) > 0 {
		for _, e := range result.Errors {
			logger.Warn("download error", "error", e)
		}
	}
	return err
}
