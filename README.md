# go-gallery

A Go library and CLI tool for downloading image and video galleries from Twitter/X. Uses the Twitter/X GraphQL and REST APIs - both guest-mode and authenticated - to extract media, then downloads files in parallel with deduplication, filtering, and post-processing support.

## Features

- **Multi-source extraction** - Single tweets, user timelines, likes, bookmarks, lists, search results, and home timeline
- **Guest & authenticated** - Works without login; provide cookies for protected endpoints (likes, bookmarks, home timeline)
- **Parallel downloads** - Configurable concurrency with resumable `.part` file support
- **Deduplication** - SQLite-backed archive prevents re-downloading already-seen files
- **Flexible filtering** - Filter by date range, content type, retweet/reply/quote flags, or arbitrary [`expr-lang`](https://github.com/expr-lang/expr) expressions
- **Filename templates** - Powerful `{key}`, `{key!l}`, `{key:layout}`, `{key/old/new}`, `{key|sep}`, `{key?true:false}` patterns
- **Post-processors** - `exec`, `mtime`, `rename`, `zip`, `hash`, `metadata` (JSON sidecar)
- **yt-dlp fallback** - Optional HLS/complex stream support via `yt-dlp`
- **Config file** - YAML, TOML, or JSON
- **Graceful unavailability handling** - Deleted, DMCA-removed, suspended, and geo-blocked tweets are reported per-item and counted separately; the run never aborts because of them

## Installation

```bash
go install github.com/hecker-01/go-gallery/cmd/go-gallery@latest
```

Or build from source:

```bash
git clone https://github.com/hecker-01/go-gallery
cd go-gallery
go build ./cmd/go-gallery
```

**Requirements:** Go 1.22+. Pure-Go SQLite is used - no CGO required for basic usage.

## CLI Usage

```bash
go-gallery [flags] URL...
```

### Flags

| Flag                             | Default  | Description                                                                     |
| -------------------------------- | -------- | ------------------------------------------------------------------------------- |
| `-g`                             |          | Print direct media URLs and exit (no download)                                  |
| `-j`                             |          | Print per-item JSON to stdout and exit                                          |
| `-K`                             |          | Print available template keywords for the first item                            |
| `-simulate`                      |          | Run full pipeline but skip all I/O                                              |
| `-d DIR`                         | `.`      | Base output directory; `twitter/{username}/...` structure is created beneath it |
| `-D DIR`                         |          | Direct output directory; files are placed here with no subdirectory structure   |
| `-f PATTERN`                     | (config) | Filename template pattern                                                       |
| `--concurrency N`                | `4`      | Number of parallel downloads                                                    |
| `--cookies-from-browser BROWSER` |          | Import cookies from browser (`firefox`)                                         |
| `--cookies-from-file PATH`       |          | Import from Netscape `cookies.txt` file                                         |
| `--filter EXPR`                  |          | `expr-lang` expression to filter items                                          |
| `--config PATH`                  |          | Path to a YAML/TOML/JSON config file                                            |
| `-v` / `--verbose`               |          | Enable debug-level logging                                                      |
| `-q` / `--quiet`                 |          | Suppress all output                                                             |

**Output format** follows gallery-dl's style - `[source][level] message` with ANSI colors on terminals (auto-detected; set `NO_COLOR=1` to disable):

```log
[twitter][info] twitter/username/1234567890_1.jpg
[twitter][warning] unavailable [tombstone] tweet 9876543210
[twitter][warning] rate limited on UserMedia; waiting 15s (resets at 2026-01-01T12:00:00Z)
[go-gallery][info] 42 downloaded, 0 skipped, 1 unavailable, 0 failed (8.3s)
[go-gallery][warning] unavailable:
[go-gallery][warning]   unavailable (dmca): https://video.twimg.com/.../video.mp4
```

The summary line always reports four counters:

| Counter     | Meaning                                         |
| ----------- | ----------------------------------------------- |
| downloaded  | Files successfully saved to disk                |
| skipped     | Archive hits (already downloaded previously)    |
| unavailable | Deleted, DMCA, suspended, geo-blocked items     |
| failed      | Network errors, I/O errors, unexpected failures |

**Exit code** - exits `0` even when some items were unavailable (matching gallery-dl behaviour). Exits `1` only on a fatal auth/challenge failure, or when zero files downloaded and there were real failures.

> **`-d` vs `-D`** mirrors the convention from [gallery-dl](https://github.com/mikf/gallery-dl):
> `-d` sets the _base_ directory and the tool still creates `twitter/{username}/` beneath it;
> `-D` sets the _direct_ directory and files go there with no further subdirectories.

### Examples

```bash
# Download a user's media tab
go-gallery https://x.com/username/media

# Download a single tweet
go-gallery https://x.com/username/status/1234567890

# Download bookmarks (requires authentication)
go-gallery --cookies-from-browser firefox https://x.com/i/bookmarks

# Download to ~/gallery, keeping twitter/username/ subfolders
go-gallery -d ~/gallery https://x.com/username/media

# Download flat - all files directly into ~/flat, no subfolders
go-gallery -D ~/flat https://x.com/username/media

# Custom base directory and filename pattern
go-gallery -d ~/gallery \
  -f "{author.screen_name}/{date:2006-01}/{tweet_id}_{num}.{extension}" \
  https://x.com/username/media

# Filter to videos only
go-gallery --filter 'extension == "mp4"' https://x.com/username/media

# Print JSON metadata without downloading
go-gallery -j https://x.com/username/media

# Print available template keywords
go-gallery -K https://x.com/username/status/1234567890
```

## Supported URLs

Both `twitter.com` and `x.com` domains are supported.

| Source                | URL Pattern                          | Auth Required |
| --------------------- | ------------------------------------ | ------------- |
| Single tweet          | `/{username}/status/{id}`            | No            |
| User timeline / media | `/{username}` or `/{username}/media` | No            |
| User likes            | `/{username}/likes`                  | Yes           |
| Bookmarks             | `/i/bookmarks`                       | Yes           |
| List                  | `/i/lists/{id}`                      | No            |
| Search                | `/search?q=...`                      | No            |
| Home timeline         | `/home`                              | Yes           |

**Authentication:** Pass cookies via `--cookies-from-browser firefox` or `--cookies-from-file cookies.txt`. The tool will auto-fetch and cache a guest token for unauthenticated endpoints.

## Error Handling & Unavailable Content

go-gallery distinguishes between _permanent unavailability_ (content that is gone and won't come back) and _transient failures_ (network errors worth retrying).

### Unavailability reasons

When a tweet or media file cannot be retrieved, the reason is reported per-item and counted as `unavailable` in the summary:

| Reason             | Cause                                                   |
| ------------------ | ------------------------------------------------------- |
| `tombstone`        | Tweet removed (Twitter shows a placeholder in timeline) |
| `deleted`          | HTTP 404 - content does not exist                       |
| `gone`             | HTTP 410 - content permanently removed                  |
| `dmca`             | HTTP 451 - DMCA / legal takedown                        |
| `suspended`        | Tweet from a suspended account                          |
| `policy-violation` | Tweet removed for policy violation                      |
| `protected`        | Tweet from a protected account you cannot access        |

### Rate limiting

When a 429 response is received, go-gallery reads the `x-rate-limit-reset` header and waits until the window resets, adding a **10-second buffer** to account for clock skew between your machine and Twitter's servers. It retries up to 3 times before aborting the operation.

### Typed errors in the library

```go
import (
    "errors"
    gallery "github.com/hecker-01/go-gallery"
)

result, err := client.Download(ctx, url, opts...)

// Fatal extraction errors
var authnErr *gallery.AuthenticationError  // 401 / bad credentials
var challengeErr *gallery.ChallengeError   // CAPTCHA / account lock
if errors.As(err, &authnErr) { /* re-authenticate */ }

// Per-item unavailability (in result.Errors)
for _, e := range result.Errors {
    var nfe *gallery.NotFoundError
    if errors.As(e, &nfe) {
        fmt.Printf("unavailable (%s): %s\n", nfe.Reason, nfe.URL)
    }
}

// Summary counters
fmt.Printf("%d downloaded, %d unavailable, %d failed\n",
    result.TotalFiles, result.UnavailableFiles, result.FailedFiles)
```

`ClassifyHTTPStatus(status int, url string, body []byte) error` is also exported if you need to map raw HTTP codes to the same typed errors in your own code.

```yaml
# config.yaml
output:
  dir: "~/gallery"
  filename_format: "{category}/{author.screen_name}/{tweet_id}_{num}.{extension}"
  skip_existing: true
  write_metadata: false

twitter:
  retweets_enabled: false # include media from retweets on home/list/search timelines
  video_max_bitrate: true # pick highest bitrate video variant; false picks lowest
  user_agent: "" # override the browser User-Agent (empty = built-in default)
  auth_token: "" # authenticate via config instead of browser cookies
  csrf: "" # ct0 value, paired with auth_token

archive:
  enabled: true
  path: "" # defaults to XDG data dir
  key: "{tweet_id}_{num}"

cache:
  enabled: true
  path: "" # defaults to XDG cache dir
  ttl: 3600 # seconds

downloader:
  concurrency: 4
  retries: 4
  resume: true
  min_file_size: 0 # bytes, 0 = no limit
  max_file_size: 0 # bytes, 0 = no limit
```

Load with:

```bash
go-gallery --config config.yaml https://x.com/username/media
```

## Filename Templates

Templates use `{key}` placeholders. Available keywords:

| Keyword              | Description                                           |
| -------------------- | ----------------------------------------------------- |
| `tweet_id`           | Tweet ID                                              |
| `author.screen_name` | Username (e.g. `username`)                            |
| `author.name`        | Display name                                          |
| `author.id`          | Author's numeric ID                                   |
| `date`               | Tweet date (use `{date:2006-01-02}` for layout)       |
| `content`            | Tweet text                                            |
| `extension`          | File extension (`jpg`, `mp4`, etc.)                   |
| `num`                | Media index within tweet (1-based)                    |
| `count`              | Total media count in tweet                            |
| `favorite_count`     | Like count                                            |
| `retweet_count`      | Retweet count                                         |
| `reply_count`        | Reply count                                           |
| `quote_count`        | Quote count                                           |
| `lang`               | Tweet language                                        |
| `hashtags`           | Hashtags (use `{hashtags\|,}` to join with separator) |
| `mentions`           | Mentions                                              |
| `is_retweet`         | Boolean                                               |
| `is_reply`           | Boolean                                               |
| `is_quote`           | Boolean                                               |
| `category`           | Extractor category                                    |

**Modifiers:**

| Syntax             | Description                       |
| ------------------ | --------------------------------- |
| `{key!l}`          | Lowercase                         |
| `{key!u}`          | Uppercase                         |
| `{key:layout}`     | Format (dates use Go time layout) |
| `{key/old/new}`    | String replacement                |
| `{key\|sep}`       | Join slice with separator         |
| `{key?true:false}` | Conditional                       |

## Library Usage

```go
import "github.com/hecker-01/go-gallery"

client := gallery.NewClient(gallery.WithConcurrency(4))

// Base directory - twitter/username/ structure is created beneath ./output
result, err := client.Download(ctx, "https://x.com/username/media",
    gallery.WithOutputDir("./output"),
)
fmt.Printf("%d downloaded, %d skipped, %d unavailable, %d failed\n",
    result.TotalFiles, result.SkippedFiles, result.UnavailableFiles, result.FailedFiles)

// Direct directory - files go straight into ./flat with no subdirectories
result, err = client.Download(ctx, "https://x.com/username/media",
    gallery.WithDirectOutputDir("./flat"),
)

// Or compose manually: set base dir then enable flat mode
result, err = client.Download(ctx, "https://x.com/username/media",
    gallery.WithOutputDir("./flat"),
    gallery.WithFlatDir(),
)
```

### Streaming extraction

`Extract` returns a channel of `Message` values. Three variants exist:

```go
msgs, errs := client.Extract(ctx, "https://x.com/username/media")
for msg := range msgs {
    switch m := msg.(type) {
    case gallery.Directory:
        // Subsequent media items belong under m.Path
    case gallery.Media:
        // m.URL is the direct download URL; m.Info holds all metadata
    case gallery.Skipped:
        // Item was permanently unavailable
        // m.TweetID  - tweet ID (best-effort for timeline tombstones)
        // m.Reason   - "tombstone" | "deleted" | "suspended" | "dmca" | ...
        // m.Cause    - typed error (*NotFoundError etc.) if available
    }
}
if err := <-errs; err != nil {
    // Fatal extraction failure (auth error, challenge, network abort)
}
```

## Development

```bash
# Run tests
make test

# Run tests with race detector (requires CGO)
make test-race

# Lint
make lint

# Benchmarks
make bench

# Integration tests
make integration

# Clean
make clean
```

## License

See [LICENSE](LICENSE).


## Smart refresh and recovery

`WithStopAfterExisting(1)` stops extraction at the first nonempty regular destination file or archive hit, in timeline order. Zero (the default) scans normally. Already accepted downloads and metadata backfills finish; caller cancellation still cancels the entire operation. `Result.StoppedEarly` distinguishes this successful boundary from an error. This heuristic does not guarantee discovery of older gaps.

Use `WithDownloadObserver(func(DownloadEvent))` for progress instead of parsing logs. Events distinguish completed media, existing files, archive hits, unavailable media, and failures, with media identity, path, bytes, reason, and error where applicable. Callbacks are serialized per download, outside accounting locks, and must return promptly. Post-processing failures are reported separately from completed media transfers.

Construct a `NewRateLimitRegistry()` per authentication session and pass it to all related clients using `WithRateLimitRegistry`. GraphQL requests share per-endpoint budgets; media downloads and different endpoints remain concurrent. `Snapshots()` exposes the current budget and wait state. Do not share a registry between unrelated accounts. Omitting the option retains a private registry per client.

Every non-simulated media transfer writes a `RepairRecord` under the output directory's `.twdl/repairs` before starting. Successful finalization removes it. `LoadRepairRecords`, `SaveRepairRecord`, and `RemoveRepairRecord` support recovery tools; records contain media identity and the destination, not credentials or expiring CDN URLs. `WithRepair(record)` selects the recorded post/media index, preserves the destination, and bypasses archive skipping. Resolve it through the source post URL to obtain fresh media URLs. `WithRepairDestinations(paths)` bypasses archive hits only for those known repair paths during a fallback scan.

Library downloads retain normal `.part` resumption. HTTP 206 ranges are validated before append, a server ignoring Range triggers a fresh transfer, and 416 never promotes unverified partial bytes to a completed file. twdl adds stale-part deletion and targeted repair before refresh. Output and repair paths reject traversal and linked components.

`Extract` reports startup and pagination failures on its existing error channel. `Download` returns partial results alongside extraction errors and returns caller cancellation explicitly. `UserLookupError` identifies account-resolution failures while preserving typed causes for `errors.As`; `AccountUnavailableError` identifies explicit account-level API codes; a missing GraphQL endpoint, missing post, or malformed response must not be mistaken for a deleted account.
