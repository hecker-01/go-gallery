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
//	--simulate            Run extraction and filtering but skip all I/O
//	-d DIR                Base output directory (default: current directory); {category}/{username}/... structure is created beneath it
//	-D DIR                Direct output directory; files are placed here with no subdirectory structure
//	-f PATTERN            Filename formatter pattern
//	--concurrency N       Number of parallel downloads (default: 4)
//	--cookies-from-browser BROWSER  Import cookies from browser profile (firefox)
//	--cookies-from-file PATH        Import cookies from Netscape cookies.txt
//	--filter EXPR         Expression filter (github.com/expr-lang/expr syntax)
//	--config PATH         Path to YAML/TOML/JSON config file
//	-v / --verbose        Enable debug-level logging
//	-q / --quiet          Suppress all output
//	--debug               Enable debug-level logging and trace all program activity
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	gallery "github.com/hecker-01/go-gallery"
)

func main() {
	os.Exit(run())
}

// reorderArgs moves flag tokens before positional arguments so that
// `go-gallery URL --flag value` works in addition to `go-gallery --flag value URL`.
// Go's flag package stops parsing at the first non-flag argument; this pre-pass
// fixes the most common case of users putting flags after URLs.
func reorderArgs() {
	args := os.Args[1:]

	// Flags that don't consume a following argument.
	boolFlags := map[string]bool{
		"g": true, "j": true, "K": true,
		"simulate": true,
		"v":        true, "verbose": true,
		"q": true, "quiet": true,
		"debug": true,
	}

	var flags, positional []string
	i := 0
	for i < len(args) {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
			i++
			continue
		}
		name := strings.TrimLeft(a, "-")
		// -flag=value is self-contained.
		if strings.Contains(name, "=") {
			flags = append(flags, a)
			i++
			continue
		}
		// Boolean flag: no following value token.
		if boolFlags[name] {
			flags = append(flags, a)
			i++
			continue
		}
		// Value flag: consume the next token as its value.
		flags = append(flags, a)
		if i+1 < len(args) {
			flags = append(flags, args[i+1])
			i += 2
		} else {
			i++
		}
	}

	os.Args = append([]string{os.Args[0]}, append(flags, positional...)...)
}

func run() int {
	reorderArgs()

	// ── Flags ─────────────────────────────────────────────────────────────────
	getURLs := flag.Bool("g", false, "print direct media URLs and exit")
	getJSON := flag.Bool("j", false, "print per-item JSON to stdout and exit")
	getKeywords := flag.Bool("K", false, "print template keywords for first item and exit")
	simulate := flag.Bool("simulate", false, "run full pipeline but skip network/filesystem I/O")
	var isVerbose, isQuiet, isDebug bool
	flag.BoolVar(&isVerbose, "v", false, "enable debug-level logging")
	flag.BoolVar(&isVerbose, "verbose", false, "enable debug-level logging")
	flag.BoolVar(&isQuiet, "q", false, "suppress all output")
	flag.BoolVar(&isQuiet, "quiet", false, "suppress all output")
	flag.BoolVar(&isDebug, "debug", false, "enable debug-level logging and trace all program activity")
	baseDir := flag.String("d", ".", "base output directory; {category}/{username}/... structure is created beneath it")
	directDir := flag.String("D", "", "direct output directory; files are placed here with no subdirectory structure")
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
	switch {
	case isQuiet:
		logLevel = levelQuiet
	case isDebug, isVerbose:
		logLevel = slog.LevelDebug
	}
	logger := slog.New(newGalleryHandler(os.Stderr, logLevel))

	if isDebug {
		logger.Debug(fmt.Sprintf("config: concurrency=%d baseDir=%q directDir=%q filenameFormat=%q simulate=%v filter=%q configPath=%q",
			*concurrency, *baseDir, *directDir, *filenameFormat, *simulate, *filterExpr, *configPath))
		logger.Debug(fmt.Sprintf("cookies: browser=%q file=%q", *cookiesBrowser, *cookiesFile))
		logger.Debug(fmt.Sprintf("processing %d URL(s)", len(urls)))
	}

	// ── Client options ────────────────────────────────────────────────────────
	opts := []gallery.Option{
		gallery.WithConcurrency(*concurrency),
		gallery.WithLogger(logger),
	}

	if *configPath != "" {
		cfg, err := gallery.LoadConfig(*configPath)
		if err != nil {
			logger.Error(fmt.Sprintf("failed to load config %s: %v", *configPath, err))
			return 1
		}
		opts = append(opts, gallery.WithConfig(cfg))
	}

	if *cookiesBrowser != "" {
		if *cookiesBrowser != "firefox" {
			logger.Error(fmt.Sprintf("unsupported browser %q; only 'firefox' is supported", *cookiesBrowser))
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
			logger.Error(fmt.Sprintf("invalid filter expression %q: %v", *filterExpr, err))
			return 1
		}
		filter = ef
	}

	// ── Download options ──────────────────────────────────────────────────────
	dlOpts := []gallery.DownloadOption{
		gallery.WithSimulate(*simulate),
	}
	// -D takes precedence over -d (mirrors gallery-dl behaviour).
	if *directDir != "" {
		dlOpts = append(dlOpts, gallery.WithDirectOutputDir(*directDir))
	} else {
		dlOpts = append(dlOpts, gallery.WithOutputDir(*baseDir))
	}
	if *filenameFormat != "" {
		dlOpts = append(dlOpts, gallery.WithFilenameFormat(*filenameFormat))
	}
	if filter != nil {
		dlOpts = append(dlOpts, gallery.WithFilter(filter))
	}
	if isDebug {
		dlOpts = append(dlOpts, gallery.WithPostProcessors(&debugPostProcessor{logger: logger}))
	}

	// ── Context with OS signal cancellation ───────────────────────────────────
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	client := gallery.NewClient(opts...)
	defer client.Close()

	// ── Mode dispatch ─────────────────────────────────────────────────────────
	exitCode := 0
	for _, rawURL := range urls {
		if isDebug {
			logger.Debug(fmt.Sprintf("processing URL: %s", rawURL))
		}
		switch {
		case *getURLs:
			if err := runGetURLs(ctx, client, rawURL, logger); err != nil {
				logger.Error(fmt.Sprintf("%v", err))
				exitCode = 1
			}

		case *getKeywords:
			if err := runGetKeywords(ctx, client, rawURL, logger); err != nil {
				logger.Error(fmt.Sprintf("%v", err))
				exitCode = 1
			}

		case *getJSON:
			if err := runGetJSON(ctx, client, rawURL, logger); err != nil {
				logger.Error(fmt.Sprintf("%v", err))
				exitCode = 1
			}

		default:
			if err := runDownload(ctx, client, rawURL, dlOpts, logger); err != nil {
				logger.Error(fmt.Sprintf("%v", err))
				exitCode = 1
			}
		}
	}
	return exitCode
}

// debugPostProcessor logs each media item before download via OnPrepare.
type debugPostProcessor struct{ logger *slog.Logger }

func (d *debugPostProcessor) Name() string { return "debug" }
func (d *debugPostProcessor) OnPrepare(_ context.Context, info *gallery.MediaInfo) error {
	d.logger.Debug(fmt.Sprintf("processing %s", info.MediaURL))
	return nil
}
func (d *debugPostProcessor) OnFile(_ context.Context, _ string, _ *gallery.MediaInfo) error {
	return nil
}
func (d *debugPostProcessor) OnAfter(_ context.Context, _ string, _ *gallery.MediaInfo) error {
	return nil
}
func (d *debugPostProcessor) OnError(_ context.Context, _ error, _ *gallery.MediaInfo) error {
	return nil
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
			logger.Warn(fmt.Sprintf("JSON encode error: %v", err))
		}
	}
	return <-errs
}

func runDownload(ctx context.Context, client *gallery.Client, rawURL string, opts []gallery.DownloadOption, logger *slog.Logger) error {
	result, err := client.Download(ctx, rawURL, opts...)

	logger.Info(fmt.Sprintf("%d downloaded, %d skipped, %d unavailable, %d failed (%s)",
		result.TotalFiles, result.SkippedFiles, result.UnavailableFiles, result.FailedFiles, result.Duration))

	// Print per-item details grouped by category.
	var unavailErrs, failedErrs []error
	for _, e := range result.Errors {
		var nfe *gallery.NotFoundError
		var authzErr *gallery.AuthorizationError
		if errors.As(e, &nfe) || errors.As(e, &authzErr) {
			unavailErrs = append(unavailErrs, e)
		} else {
			failedErrs = append(failedErrs, e)
		}
	}
	if len(unavailErrs) > 0 {
		logger.Warn("unavailable:")
		for _, e := range unavailErrs {
			logger.Warn("  " + e.Error())
		}
	}
	if len(failedErrs) > 0 {
		logger.Warn("failed:")
		for _, e := range failedErrs {
			logger.Warn("  " + e.Error())
		}
	}

	// Fatal extraction error (auth failure, challenge, network abort).
	if err != nil {
		return err
	}

	// Exit 1 only when nothing downloaded AND there were actual failures
	// (not just unavailables - those are expected and exit 0 per gallery-dl behaviour).
	if result.TotalFiles == 0 && result.FailedFiles > 0 {
		return fmt.Errorf("%d download(s) failed, none succeeded", result.FailedFiles)
	}

	return nil
}
