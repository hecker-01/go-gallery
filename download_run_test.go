package gallery

import (
	"context"
	"errors"
	"fmt"
	"github.com/hecker-01/go-gallery/internal/extractor"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type fixtureExtractor struct{ items []extractor.Item }

func (f fixtureExtractor) Name() string     { return "fixture" }
func (f fixtureExtractor) Category() string { return "fixture" }
func (f fixtureExtractor) Items(ctx context.Context) <-chan extractor.Item {
	out := make(chan extractor.Item)
	go func() {
		defer close(out)
		for _, i := range f.items {
			select {
			case out <- i:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

var fixtureID atomic.Int64

func fixtureURL(items ...extractor.Item) string {
	u := fmt.Sprintf("fixture://%d", fixtureID.Add(1))
	extractor.Register("^"+u+"$", func(string, extractor.ClientParams) extractor.Extractor { return fixtureExtractor{items} })
	return u
}
func fixtureMedia(id string) extractor.Item {
	return extractor.Item{Kind: extractor.KindMedia, URL: id, Meta: &extractor.ItemMeta{TweetID: id, Num: 1, Extension: "jpg"}}
}

type downloadFunc func(context.Context, string, io.Writer, DownloadConfig) error

func (f downloadFunc) Download(ctx context.Context, u string, w io.Writer, c DownloadConfig) error {
	return f(ctx, u, w, c)
}
func TestSmartStopDrainsTransfersAndIgnoresDuplicates(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "2_1.jpg"), []byte("old"), 0600)
	u := fixtureURL(fixtureMedia("4"), fixtureMedia("4"), fixtureMedia("3"), fixtureMedia("2"), fixtureMedia("1"))
	release := make(chan struct{})
	second := make(chan struct{})
	dl := downloadFunc(func(ctx context.Context, u string, w io.Writer, _ DownloadConfig) error {
		if u == "4" {
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		} else if u == "3" {
			close(second)
		}
		_, err := io.WriteString(w, "new")
		return err
	})
	go func() { <-second; close(release) }()
	var events []DownloadEvent
	r, err := NewClient(WithConcurrency(2)).Download(context.Background(), u, WithDirectOutputDir(dir), WithFilenameFormat("{tweet_id}_{num}.{extension}"), WithDownloaderOpt(dl), WithStopAfterExisting(1), WithDownloadObserver(func(e DownloadEvent) { events = append(events, e) }))
	if err != nil || !r.StoppedEarly || r.TotalFiles != 2 || r.SkippedFiles != 1 {
		t.Fatalf("result=%+v err=%v", r, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "1_1.jpg")); !os.IsNotExist(err) {
		t.Fatal("downloaded past boundary")
	}
	completed := 0
	for _, e := range events {
		if e.Kind == EventCompleted {
			completed++
		}
	}
	if completed != 2 {
		t.Fatalf("completion events=%d", completed)
	}
	records, err := LoadRepairRecords(dir)
	if err != nil || len(records) != 0 {
		t.Fatalf("records=%v err=%v", records, err)
	}
}
func TestExtractFailurePreservesPartialResults(t *testing.T) {
	want := errors.New("page failed")
	for _, items := range [][]extractor.Item{{{Kind: extractor.KindError, Err: want}}, {fixtureMedia("1"), {Kind: extractor.KindError, Err: want}}} {
		r, err := NewClient().Download(context.Background(), fixtureURL(items...), WithDirectOutputDir(t.TempDir()), WithDownloaderOpt(downloadFunc(func(_ context.Context, _ string, w io.Writer, _ DownloadConfig) error {
			_, err := io.WriteString(w, "ok")
			return err
		})))
		if !errors.Is(err, want) || r.TotalFiles != len(items)-1 || r.Duration < 0 {
			t.Fatalf("%+v %v", r, err)
		}
	}
}
func TestRepairBypassesArchiveAndKeepsDestination(t *testing.T) {
	dir := t.TempDir()
	a := NewMemoryArchive()
	a.Put(context.Background(), "1:1")
	record := RepairRecord{Destination: filepath.Join(dir, "custom.jpg"), TweetID: "1", MediaIndex: 1}
	r, err := NewClient(WithArchive(a)).Download(context.Background(), fixtureURL(fixtureMedia("1"), fixtureMedia("2")), WithDirectOutputDir(dir), WithRepair(record), WithDownloaderOpt(downloadFunc(func(_ context.Context, _ string, w io.Writer, _ DownloadConfig) error {
		_, err := io.WriteString(w, "ok")
		return err
	})))
	if err != nil || r.TotalFiles != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err = os.Stat(record.Destination); err != nil {
		t.Fatal(err)
	}
}
func TestCancelledDownloadReturnsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, err := NewClient().Download(ctx, fixtureURL(fixtureMedia("1")), WithDirectOutputDir(t.TempDir()))
	if !errors.Is(err, context.Canceled) || r.Duration < 0 {
		t.Fatalf("%+v %v", r, err)
	}
}
func TestRepairRecordAndAtomicReplacement(t *testing.T) {
	dir := t.TempDir()
	r := RepairRecord{Destination: filepath.Join(dir, "1.jpg"), TweetID: "1", MediaIndex: 1}
	if err := SaveRepairRecord(dir, r); err != nil {
		t.Fatal(err)
	}
	r.MediaIndex = 2
	if err := SaveRepairRecord(dir, r); err != nil {
		t.Fatal(err)
	}
	records, err := LoadRepairRecords(dir)
	if err != nil || len(records) != 1 || records[0].MediaIndex != 2 {
		t.Fatalf("%v %v", records, err)
	}
	if err := SaveRepairRecord(dir, RepairRecord{Destination: filepath.Join(dir, "..", "escape")}); err == nil {
		t.Fatal("accepted escape")
	}
	blocked := filepath.Join(dir, "directory")
	os.Mkdir(blocked, 0700)
	if err := WriteFileAtomic(blocked, []byte("no"), 0600); err == nil {
		t.Fatal("replaced directory")
	}
}
func TestSharedRegistryInjected(t *testing.T) {
	r := NewRateLimitRegistry()
	a := NewClient(WithRateLimitRegistry(r))
	b := NewClient(WithRateLimitRegistry(r))
	if a.rlRegistry != b.rlRegistry {
		t.Fatal("not shared")
	}
	a.rlRegistry.On429("test", time.Now().Add(time.Minute))
	if !r.Snapshots()[0].Waiting {
		t.Fatal("missing wait status")
	}
}

func TestSmartStopIgnoresEmptyAndPartialFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "3_1.jpg.part"), []byte("partial"), 0600)
	os.WriteFile(filepath.Join(dir, "2_1.jpg"), nil, 0600)
	os.WriteFile(filepath.Join(dir, "1_1.jpg"), []byte("existing"), 0600)
	r, err := NewClient().Download(context.Background(), fixtureURL(fixtureMedia("3"), fixtureMedia("2"), fixtureMedia("1")), WithDirectOutputDir(dir), WithFilenameFormat("{tweet_id}_{num}.{extension}"), WithStopAfterExisting(1), WithDownloaderOpt(downloadFunc(func(_ context.Context, _ string, w io.Writer, _ DownloadConfig) error {
		_, err := io.WriteString(w, "new")
		return err
	})))
	if err != nil || r.TotalFiles != 2 || !r.StoppedEarly {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestFallbackRepairsOnlyKnownArchivePaths(t *testing.T) {
	dir := t.TempDir()
	a := NewMemoryArchive()
	a.Put(context.Background(), "2:1")
	a.Put(context.Background(), "1:1")
	target := filepath.Join(dir, "2_1.jpg")
	r, err := NewClient(WithArchive(a)).Download(context.Background(), fixtureURL(fixtureMedia("2"), fixtureMedia("1")), WithDirectOutputDir(dir), WithFilenameFormat("{tweet_id}_{num}.{extension}"), WithRepairDestinations([]string{target}), WithDownloaderOpt(downloadFunc(func(_ context.Context, _ string, w io.Writer, _ DownloadConfig) error {
		_, err := io.WriteString(w, "repaired")
		return err
	})))
	if err != nil || r.TotalFiles != 1 || r.SkippedFiles != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	if !fileExists(target) || fileExists(filepath.Join(dir, "1_1.jpg")) {
		t.Fatal("archive repair scope incorrect")
	}
}
