package gallery

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// Config is the top-level library configuration. All fields have sane defaults
// returned by DefaultConfig(); no file is required.
type Config struct {
	Output     OutputConfig              `json:"output"     yaml:"output"     toml:"output"`
	Twitter    TwitterConfig             `json:"twitter"    yaml:"twitter"    toml:"twitter"`
	Archive    ArchiveConfig             `json:"archive"    yaml:"archive"    toml:"archive"`
	Cache      CacheConfig               `json:"cache"      yaml:"cache"      toml:"cache"`
	Downloader DownloaderConfig          `json:"downloader" yaml:"downloader" toml:"downloader"`
	Extractors map[string]map[string]any `json:"extractors" yaml:"extractors" toml:"extractors"`
}

// OutputConfig controls where and how files are saved.
type OutputConfig struct {
	// Dir is the base directory for downloads. Defaults to the current directory.
	Dir string `json:"dir" yaml:"dir" toml:"dir"`
	// FilenameFormat is the formatter pattern. See NewFormatter for syntax.
	FilenameFormat string `json:"filename_format" yaml:"filename_format" toml:"filename_format"`
	// SkipExisting skips download when the destination file already exists.
	SkipExisting bool `json:"skip_existing" yaml:"skip_existing" toml:"skip_existing"`
	// WriteMetadata writes a JSON sidecar next to each downloaded file.
	WriteMetadata bool `json:"write_metadata" yaml:"write_metadata" toml:"write_metadata"`
}

// TwitterConfig holds Twitter-specific extractor settings.
type TwitterConfig struct {
	// GuestToken overrides the dynamically-fetched guest token.
	GuestToken string `json:"guest_token" yaml:"guest_token" toml:"guest_token"`
	// AuthToken is the auth_token cookie value for authenticated requests.
	AuthToken string `json:"auth_token" yaml:"auth_token" toml:"auth_token"`
	// CSRF is the ct0 cookie / x-csrf-token value.
	CSRF string `json:"csrf" yaml:"csrf" toml:"csrf"`
	// UserAgent overrides the default browser User-Agent sent to Twitter.
	UserAgent string `json:"user_agent" yaml:"user_agent" toml:"user_agent"`
	// RepliesEnabled includes reply tweets when extracting a user timeline.
	RepliesEnabled bool `json:"replies_enabled" yaml:"replies_enabled" toml:"replies_enabled"`
	// RetweetsEnabled includes retweets when extracting a user timeline.
	RetweetsEnabled bool `json:"retweets_enabled" yaml:"retweets_enabled" toml:"retweets_enabled"`
	// VideoMaxBitrate picks the highest bitrate variant; false picks lowest.
	VideoMaxBitrate bool `json:"video_max_bitrate" yaml:"video_max_bitrate" toml:"video_max_bitrate"`
}

// ArchiveConfig controls the download archive (deduplication database).
type ArchiveConfig struct {
	// Path is the SQLite file path. Defaults to XDG data home.
	Path string `json:"path" yaml:"path" toml:"path"`
	// Key is the archive key pattern. Defaults to "{tweet_id}_{num}".
	Key string `json:"key" yaml:"key" toml:"key"`
	// Enabled toggles archive checking. Defaults to false so it is opt-in.
	Enabled bool `json:"enabled" yaml:"enabled" toml:"enabled"`
}

// CacheConfig controls the SQLite session cache.
type CacheConfig struct {
	// Path overrides the XDG cache path.
	Path string `json:"path" yaml:"path" toml:"path"`
	// Enabled toggles the cache. Defaults to true.
	Enabled bool `json:"enabled" yaml:"enabled" toml:"enabled"`
	// TTL is the default cache entry lifetime in seconds.
	TTL int `json:"ttl" yaml:"ttl" toml:"ttl"`
}

// DownloaderConfig controls the HTTP downloader behaviour.
type DownloaderConfig struct {
	// Concurrency is the number of parallel media downloads. Defaults to 4.
	Concurrency int `json:"concurrency" yaml:"concurrency" toml:"concurrency"`
	// Retries is the number of retry attempts on transient failures. Default 4.
	Retries int `json:"retries" yaml:"retries" toml:"retries"`
	// Resume enables .part file resumption via Range headers. Default true.
	Resume bool `json:"resume" yaml:"resume" toml:"resume"`
	// MinFileSize rejects files smaller than this many bytes. 0 = no limit.
	MinFileSize int64 `json:"min_file_size" yaml:"min_file_size" toml:"min_file_size"`
	// MaxFileSize rejects files larger than this many bytes. 0 = no limit.
	MaxFileSize int64 `json:"max_file_size" yaml:"max_file_size" toml:"max_file_size"`
	// ChunkSize is the streaming read buffer size in bytes. Default 32 KiB.
	ChunkSize int `json:"chunk_size" yaml:"chunk_size" toml:"chunk_size"`
	// RateLimitSleep is the seconds to wait when rate-limited in HTTP 429.
	// 0 means use the server-supplied reset header.
	RateLimitSleep int `json:"rate_limit_sleep" yaml:"rate_limit_sleep" toml:"rate_limit_sleep"`
}

// DefaultConfig returns a Config populated with sensible defaults.
// No fields reference external resources so it works out of the box.
func DefaultConfig() Config {
	return Config{
		Output: OutputConfig{
			Dir:            ".",
			FilenameFormat: "{category}/{author.screen_name}/{tweet_id}_{num}.{extension}",
			SkipExisting:   true,
		},
		Twitter: TwitterConfig{
			UserAgent:       "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
			VideoMaxBitrate: true,
			RetweetsEnabled: true,
		},
		Archive: ArchiveConfig{
			Key:     "{tweet_id}_{num}",
			Enabled: false,
		},
		Cache: CacheConfig{
			Enabled: true,
			TTL:     3600,
		},
		Downloader: DownloaderConfig{
			Concurrency: 4,
			Retries:     4,
			Resume:      true,
			ChunkSize:   32 * 1024,
		},
		Extractors: map[string]map[string]any{},
	}
}

// LoadConfig reads a config file at path. Format is detected by file extension:
// .json → JSON, .yaml / .yml → YAML, .toml → TOML.
// The returned Config is populated over DefaultConfig() so missing fields get
// sane defaults.
func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("reading config %s: %w", path, err)
	}

	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".json":
		if err := json.Unmarshal(data, &cfg); err != nil {
			return cfg, fmt.Errorf("parsing JSON config: %w", err)
		}
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return cfg, fmt.Errorf("parsing YAML config: %w", err)
		}
	case ".toml":
		if err := toml.Unmarshal(data, &cfg); err != nil {
			return cfg, fmt.Errorf("parsing TOML config: %w", err)
		}
	default:
		return cfg, &InputError{
			Field:   "path",
			Message: fmt.Sprintf("unsupported config format %q (use .json, .yaml, or .toml)", ext),
		}
	}

	return cfg, nil
}
