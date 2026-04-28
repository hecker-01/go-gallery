package gallery

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Output.FilenameFormat == "" {
		t.Error("default FilenameFormat should not be empty")
	}
	if cfg.Downloader.Concurrency != 4 {
		t.Errorf("default Concurrency: got %d, want 4", cfg.Downloader.Concurrency)
	}
	if cfg.Downloader.Retries != 4 {
		t.Errorf("default Retries: got %d, want 4", cfg.Downloader.Retries)
	}
	if !cfg.Downloader.Resume {
		t.Error("default Resume should be true")
	}
	if !cfg.Cache.Enabled {
		t.Error("default Cache.Enabled should be true")
	}
}

func TestLoadConfig_JSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{"output":{"dir":"/tmp/output"},"downloader":{"concurrency":8}}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Output.Dir != "/tmp/output" {
		t.Errorf("Output.Dir: got %q, want %q", cfg.Output.Dir, "/tmp/output")
	}
	if cfg.Downloader.Concurrency != 8 {
		t.Errorf("Concurrency: got %d, want 8", cfg.Downloader.Concurrency)
	}
	// Fields not in the JSON file should keep defaults.
	if cfg.Downloader.Retries != 4 {
		t.Errorf("Retries should keep default 4, got %d", cfg.Downloader.Retries)
	}
}

func TestLoadConfig_YAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := "output:\n  dir: /yaml/output\ndownloader:\n  concurrency: 2\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Output.Dir != "/yaml/output" {
		t.Errorf("Output.Dir: got %q, want %q", cfg.Output.Dir, "/yaml/output")
	}
	if cfg.Downloader.Concurrency != 2 {
		t.Errorf("Concurrency: got %d, want 2", cfg.Downloader.Concurrency)
	}
}

func TestLoadConfig_TOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := "[output]\ndir = \"/toml/output\"\n[downloader]\nconcurrency = 6\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Output.Dir != "/toml/output" {
		t.Errorf("Output.Dir: got %q, want %q", cfg.Output.Dir, "/toml/output")
	}
	if cfg.Downloader.Concurrency != 6 {
		t.Errorf("Concurrency: got %d, want 6", cfg.Downloader.Concurrency)
	}
}

func TestLoadConfig_UnsupportedExtension(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.xml")
	if err := os.WriteFile(path, []byte("<root/>"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path)
	if err == nil {
		t.Error("expected error for unsupported extension, got nil")
	}
	var inputErr *InputError
	if !isInputError(err, &inputErr) {
		t.Errorf("expected *InputError, got %T: %v", err, err)
	}
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := LoadConfig("/nonexistent/path/config.json")
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func isInputError(err error, target **InputError) bool {
	if ie, ok := err.(*InputError); ok {
		*target = ie
		return true
	}
	return false
}
