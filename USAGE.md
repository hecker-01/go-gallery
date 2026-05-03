# Usage Guide

## Authentication

Some endpoints (likes, bookmarks, home timeline) require authentication. Even for public endpoints, authenticated requests have much higher rate limits.

**Extract cookies from Firefox:**

```bash
go-gallery --cookies-from-browser firefox https://x.com/username/media
```

**Use a cookies.txt file** (Netscape format, exported by browser extensions):

```bash
go-gallery --cookies-from-file ~/cookies.txt https://x.com/username/media
```

Without cookies the tool runs in guest mode. Twitter's guest quota is low - you may hit rate limits after a single page of results.

---

## Common Workflows

**Download a user's entire media archive:**

```bash
go-gallery --cookies-from-browser firefox https://x.com/username/media
```

Files land in `./twitter/username/`.

**Download into a custom base directory:**

```bash
go-gallery -d ~/gallery --cookies-from-browser firefox https://x.com/username/media
# Files → ~/gallery/twitter/username/
```

**Download flat - no subdirectories:**

```bash
go-gallery -D ~/downloads --cookies-from-browser firefox https://x.com/username/media
# Files → ~/downloads/
```

**Download a single tweet:**

```bash
go-gallery https://x.com/username/status/1234567890
```

**Download bookmarks or likes** (auth required):

```bash
go-gallery --cookies-from-browser firefox https://x.com/i/bookmarks
go-gallery --cookies-from-browser firefox https://x.com/username/likes
```

**Download multiple accounts in one run:**

```bash
go-gallery --cookies-from-browser firefox \
  https://x.com/user1/media \
  https://x.com/user2/media
```

---

## Avoiding Re-Downloads (Archive)

Without an archive every run re-downloads everything. Enable deduplication via config:

```yaml
# config.yaml
archive:
  enabled: true
  path: "" # defaults to XDG data dir (~/.local/share/go-gallery/)
```

```bash
go-gallery --config config.yaml --cookies-from-browser firefox https://x.com/username/media
```

Files already recorded in the archive are counted as `skipped` and not downloaded again.

---

## Filtering

Filters use [`expr-lang`](https://github.com/expr-lang/expr) syntax over the item's metadata keywords.

**Videos only:**

```bash
go-gallery --filter 'extension == "mp4"' https://x.com/username/media
```

**Images only:**

```bash
go-gallery --filter 'extension != "mp4"' https://x.com/username/media
```

**Only highly-liked posts:**

```bash
go-gallery --filter 'favorite_count > 1000' https://x.com/username/media
```

**Posts from 2024 onwards:**

```bash
go-gallery --filter 'date >= "2024-01-01"' https://x.com/username/media
```

**Only original tweets (no replies, no quotes):**

```bash
go-gallery --filter '!is_reply && !is_quote' https://x.com/username/media
```

Print available keywords for any URL with `-K`:

```bash
go-gallery -K https://x.com/username/status/1234567890
```

---

## Filename Templates

The default pattern is `{category}/{author.screen_name}/{tweet_id}_{num}.{extension}`.

**Date-organised subfolders:**

```bash
go-gallery -f "{author.screen_name}/{date:2006-01}/{tweet_id}_{num}.{extension}" \
  https://x.com/username/media
```

**Include like count in filename:**

```bash
go-gallery -f "{author.screen_name}/{tweet_id}_{favorite_count}_{num}.{extension}" \
  https://x.com/username/media
```

**Lowercase screen name:**

```bash
go-gallery -f "{author.screen_name!l}/{tweet_id}_{num}.{extension}" \
  https://x.com/username/media
```

---

## Rate Limits

Twitter rate-limits by API endpoint. When a limit is hit the tool logs the window and waits automatically:

```log
[twitter][info] rate-limit window exhausted for UserMedia; sleeping 7m1s
```

The run will resume after the window resets - you do not need to restart. With authenticated cookies the per-window quota is much larger than guest mode.

Near-limit warnings appear before exhaustion so you can see it coming:

```log
[twitter][warning] twitter UserMedia near rate limit: 3/500 remaining (resets 2026-05-03T17:41:20Z)
```

---

## DMCA and Unavailable Content

Unavailable media is reported per-item and counted separately - the run never aborts:

```log
[go-gallery][warning] unavailable [dmca] tweet 1456420886458814466
[go-gallery][info] 138 downloaded, 0 skipped, 1 unavailable, 0 failed (41.2s)
```

Unavailability reasons:

| Reason             | Cause                                   |
| ------------------ | --------------------------------------- |
| `dmca`             | DMCA / legal takedown                   |
| `tombstone`        | Tweet removed (placeholder in timeline) |
| `suspended`        | Account suspended                       |
| `policy-violation` | Removed for policy violation            |
| `deleted`          | HTTP 404                                |
| `gone`             | HTTP 410                                |

---

## Debugging

`--debug` logs every API request and response with timing and a correlation ID:

```bash
go-gallery --debug --cookies-from-browser firefox https://x.com/username/media 2>&1 | tee latest.log
```

Key debug lines:

```log
[twitter][debug] → #0001 UserByScreenName (attempt 1/4): https://x.com/...
[twitter][debug] ← #0001 UserByScreenName: 200 in 318ms
[twitter][debug] UserMedia page: cursor_in=first → 41 items, cursor_out=DAABCgABHH...
[twitter][debug] UserMedia page: cursor_in=DAABCgABHH... → 35 items, cursor_out=DAABCgABC...
```

Each `→`/`←` pair shares a `#NNNN` ID so you can match requests to their responses. The page summary lines show how many items each pagination page yielded and what cursor was returned.

To isolate just pagination progress:

```powershell
./go-gallery.exe ... --debug 2>&1 | Select-String "UserMedia page:"
```

```bash
./go-gallery ... --debug 2>&1 | grep "UserMedia page:"
```
