package gallery

import (
	"archive/zip"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// PostProcessor runs after a successful download. Implementations must be
// safe for concurrent use.
type PostProcessor interface {
	// Name returns a human-readable identifier for logging.
	Name() string
	// OnPrepare is called before the download begins.
	OnPrepare(ctx context.Context, info *MediaInfo) error
	// OnFile is called with the path to the completed download file.
	OnFile(ctx context.Context, path string, info *MediaInfo) error
	// OnAfter is called after all post-processors have run OnFile.
	OnAfter(ctx context.Context, path string, info *MediaInfo) error
	// OnError is called when the download or a prior stage failed.
	OnError(ctx context.Context, err error, info *MediaInfo) error
}

// nopPostProcessor is an embeddable no-op implementation for partial overrides.
type nopPostProcessor struct{ name string }

func (n nopPostProcessor) Name() string                                            { return n.name }
func (n nopPostProcessor) OnPrepare(_ context.Context, _ *MediaInfo) error         { return nil }
func (n nopPostProcessor) OnFile(_ context.Context, _ string, _ *MediaInfo) error  { return nil }
func (n nopPostProcessor) OnAfter(_ context.Context, _ string, _ *MediaInfo) error { return nil }
func (n nopPostProcessor) OnError(_ context.Context, _ error, _ *MediaInfo) error  { return nil }

// ─── ExecPostProcessor ────────────────────────────────────────────────────────

// ExecPostProcessor runs an external command after each downloaded file.
// The argv elements are treated as Formatter patterns expanded against the
// media keywords map before execution.
type ExecPostProcessor struct {
	nopPostProcessor
	argv []string
}

// NewExecPostProcessor constructs a post-processor that runs the given command
// template. argv elements are Formatter patterns expanded per MediaInfo.
func NewExecPostProcessor(argv ...string) *ExecPostProcessor {
	return &ExecPostProcessor{
		nopPostProcessor: nopPostProcessor{name: "exec"},
		argv:             argv,
	}
}

func (p *ExecPostProcessor) OnFile(ctx context.Context, path string, info *MediaInfo) error {
	if len(p.argv) == 0 {
		return nil
	}
	kw := info.Keywords()
	kw["filepath"] = path

	expanded := make([]string, len(p.argv))
	for i, tmpl := range p.argv {
		f, err := NewFormatter(tmpl)
		if err != nil {
			expanded[i] = tmpl
			continue
		}
		expanded[i] = f.Format(kw)
	}

	cmd := exec.CommandContext(ctx, expanded[0], expanded[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ─── MtimePostProcessor ───────────────────────────────────────────────────────

// MtimePostProcessor sets the file modification time to the tweet date.
type MtimePostProcessor struct{ nopPostProcessor }

// NewMtimePostProcessor constructs a post-processor that sets file mtimes.
func NewMtimePostProcessor() *MtimePostProcessor {
	return &MtimePostProcessor{nopPostProcessor: nopPostProcessor{name: "mtime"}}
}

func (p *MtimePostProcessor) OnFile(_ context.Context, path string, info *MediaInfo) error {
	if info.Date.IsZero() {
		return nil
	}
	return os.Chtimes(path, info.Date, info.Date)
}

// ─── RenamePostProcessor ─────────────────────────────────────────────────────

// RenamePostProcessor renames each file using a Formatter pattern.
type RenamePostProcessor struct {
	nopPostProcessor
	pattern string
	fmt     *Formatter
}

// NewRenamePostProcessor constructs a rename post-processor.
// pattern is a Formatter template, e.g. "{author.screen_name}_{tweet_id}_{num}".
func NewRenamePostProcessor(pattern string) *RenamePostProcessor {
	f, _ := NewFormatter(pattern)
	return &RenamePostProcessor{
		nopPostProcessor: nopPostProcessor{name: "rename"},
		pattern:          pattern,
		fmt:              f,
	}
}

func (p *RenamePostProcessor) OnFile(_ context.Context, path string, info *MediaInfo) error {
	newName := p.fmt.Format(info.Keywords())
	if newName == "" || newName == filepath.Base(path) {
		return nil
	}
	newPath := filepath.Join(filepath.Dir(path), newName)
	return os.Rename(path, newPath)
}

// ─── ZipPostProcessor ────────────────────────────────────────────────────────

// ZipPostProcessor streams each downloaded file into a ZIP archive.
// The zip file is created lazily on the first OnFile call.
type ZipPostProcessor struct {
	nopPostProcessor
	zipPath string
	zw      *zip.Writer
	f       *os.File
}

// NewZipPostProcessor constructs a zip post-processor that writes to zipPath.
func NewZipPostProcessor(zipPath string) *ZipPostProcessor {
	return &ZipPostProcessor{
		nopPostProcessor: nopPostProcessor{name: "zip"},
		zipPath:          zipPath,
	}
}

func (p *ZipPostProcessor) OnFile(_ context.Context, path string, _ *MediaInfo) error {
	if p.zw == nil {
		f, err := os.Create(p.zipPath)
		if err != nil {
			return fmt.Errorf("zip: create archive: %w", err)
		}
		p.f = f
		p.zw = zip.NewWriter(f)
	}

	src, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("zip: open %s: %w", path, err)
	}
	defer src.Close()

	fi, err := src.Stat()
	if err != nil {
		return err
	}
	hdr, err := zip.FileInfoHeader(fi)
	if err != nil {
		return err
	}
	hdr.Name = filepath.Base(path)
	hdr.Method = zip.Deflate

	w, err := p.zw.CreateHeader(hdr)
	if err != nil {
		return fmt.Errorf("zip: create entry %s: %w", hdr.Name, err)
	}
	_, err = io.Copy(w, src)
	return err
}

func (p *ZipPostProcessor) OnAfter(_ context.Context, _ string, _ *MediaInfo) error {
	return nil // flush happens in Close
}

// Close finalises and closes the ZIP archive. Call when all files are done.
func (p *ZipPostProcessor) Close() error {
	if p.zw != nil {
		if err := p.zw.Close(); err != nil {
			return err
		}
		p.zw = nil
	}
	if p.f != nil {
		if err := p.f.Close(); err != nil {
			return err
		}
		p.f = nil
	}
	return nil
}

// ─── MetadataPostProcessor ───────────────────────────────────────────────────

// MetadataPostProcessor writes a sidecar JSON file for each downloaded item.
// The sidecar is named "<original>.<ext>.json" or "<original>.json".
type MetadataPostProcessor struct{ nopPostProcessor }

// NewMetadataPostProcessor constructs a metadata sidecar post-processor.
func NewMetadataPostProcessor() *MetadataPostProcessor {
	return &MetadataPostProcessor{nopPostProcessor: nopPostProcessor{name: "metadata"}}
}

func (p *MetadataPostProcessor) OnFile(_ context.Context, path string, info *MediaInfo) error {
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("metadata: marshal: %w", err)
	}
	sidecar := path + ".json"
	return os.WriteFile(sidecar, data, 0644)
}

// ─── HashPostProcessor ───────────────────────────────────────────────────────

// HashAlgorithm selects the hash function for HashPostProcessor.
type HashAlgorithm string

const (
	HashMD5    HashAlgorithm = "md5"
	HashSHA1   HashAlgorithm = "sha1"
	HashSHA256 HashAlgorithm = "sha256"
)

// HashPostProcessor writes a sidecar checksum file alongside each download.
// The sidecar is named "<original>.<ext>.<algo>" (e.g. "image.jpg.sha256").
type HashPostProcessor struct {
	nopPostProcessor
	algo HashAlgorithm
}

// NewHashPostProcessor constructs a hash post-processor.
func NewHashPostProcessor(algo HashAlgorithm) *HashPostProcessor {
	return &HashPostProcessor{
		nopPostProcessor: nopPostProcessor{name: "hash"},
		algo:             algo,
	}
}

func (p *HashPostProcessor) OnFile(_ context.Context, path string, _ *MediaInfo) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("hash: open %s: %w", path, err)
	}
	defer f.Close()

	var h hash.Hash
	switch strings.ToLower(string(p.algo)) {
	case "md5":
		h = md5.New()
	case "sha1":
		h = sha1.New()
	default:
		h = sha256.New()
	}

	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash: compute: %w", err)
	}
	sum := hex.EncodeToString(h.Sum(nil))

	sidecar := path + "." + string(p.algo)
	return os.WriteFile(sidecar, []byte(sum+"  "+filepath.Base(path)+"\n"), 0644)
}

// ─── Pipeline helper ─────────────────────────────────────────────────────────

// runPostProcessors executes the pipeline stages for a single downloaded file.
// onPrepare must be called before download; onFile after; onAfter after all
// processors' onFile calls; onError if any stage fails.
func runPostProcessors(ctx context.Context, processors []PostProcessor, path string, info *MediaInfo, downloadErr error) error {
	if downloadErr != nil {
		for _, pp := range processors {
			_ = pp.OnError(ctx, downloadErr, info)
		}
		return downloadErr
	}

	for _, pp := range processors {
		if err := pp.OnFile(ctx, path, info); err != nil {
			_ = pp.OnError(ctx, err, info)
			return err
		}
	}
	for _, pp := range processors {
		if err := pp.OnAfter(ctx, path, info); err != nil {
			return err
		}
	}
	return nil
}
