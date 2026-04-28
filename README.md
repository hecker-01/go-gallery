# go-gallery

A Go library and CLI tool for downloading image and video galleries from Twitter/X. Uses the Twitter/X GraphQL and REST APIs — both guest-mode and authenticated — to extract media, then downloads files in parallel with deduplication, filtering, and post-processing support.

## Features

- **Multi-source extraction** — Single tweets, user timelines, likes, bookmarks, lists, search results, and home timeline
- **Guest & authenticated** — Works without login; provide cookies for protected endpoints (likes, bookmarks, home timeline)
- **Parallel downloads** — Configurable concurrency with resumable `.part` file support
- **Deduplication** — SQLite-backed archive prevents re-downloading already-seen files
- **Flexible filtering** — Filter by date range, content type, retweet/reply/quote flags, or arbitrary [`expr-lang`](https://github.com/expr-lang/expr) expressions
- **Filename templates** — Powerful `{key}`, `{key!l}`, `{key:layout}`, `{key/old/new}`, `{key|sep}`, `{key?true:false}` patterns
- **Post-processors** — `exec`, `mtime`, `rename`, `zip`, `hash`, `metadata` (JSON sidecar)
- **yt-dlp fallback** — Optional HLS/complex stream support via `yt-dlp`
- **Config file** — YAML, TOML, or JSON

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

**Requirements:** Go 1.22+. Pure-Go SQLite is used — no CGO required for basic usage.

## CLI Usage

```
go-gallery [flags] URL...
```

### Flags

| Flag                             | Default  | Description                                          |
| -------------------------------- | -------- | ---------------------------------------------------- |
| `-g`                             |          | Print direct media URLs and exit (no download)       |
| `-j`                             |          | Print per-item JSON to stdout and exit               |
| `-K`                             |          | Print available template keywords for the first item |
| `-simulate`                      |          | Run full pipeline but skip all I/O                   |
| `-o DIR`                         | `.`      | Output directory                                     |
| `-f PATTERN`                     | (config) | Filename template pattern                            |
| `--concurrency N`                | `4`      | Number of parallel downloads                         |
| `--cookies-from-browser BROWSER` |          | Import cookies from browser (`firefox`)              |
| `--cookies-from-file PATH`       |          | Import from Netscape `cookies.txt` file              |
| `--filter EXPR`                  |          | `expr-lang` expression to filter items               |
| `--config PATH`                  |          | Path to a YAML/TOML/JSON config file                 |

### Examples

```bash
# Download a user's media tab
go-gallery https://x.com/username/media

# Download a single tweet
go-gallery https://x.com/username/status/1234567890

# Download bookmarks (requires authentication)
go-gallery --cookies-from-browser firefox https://x.com/i/bookmarks

# Custom output directory and filename pattern
go-gallery -o ~/gallery \
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

## Configuration

```yaml
# config.yaml
output:
  dir: "~/gallery"
  filename_format: "{category}/{author.screen_name}/{tweet_id}_{num}.{extension}"
  skip_existing: true
  write_metadata: false

twitter:
  replies_enabled: false
  retweets_enabled: true
  video_max_bitrate: true # pick highest bitrate video variant

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

client := gallery.NewClient(
    gallery.WithConcurrency(4),
    gallery.WithOutputDir("./output"),
)

results, err := client.Download(ctx, "https://x.com/username/media")
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
