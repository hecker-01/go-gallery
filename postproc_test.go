package gallery

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestMetadataOnExisting_BackfillsMissingSidecar verifies that OnExisting writes
// a sidecar when none is present (the refresh-backfill path).
func TestMetadataOnExisting_BackfillsMissingSidecar(t *testing.T) {
	dir := t.TempDir()
	media := filepath.Join(dir, "1_1.jpg")
	if err := os.WriteFile(media, []byte("img"), 0644); err != nil {
		t.Fatal(err)
	}
	sidecar := media + ".json"
	if _, err := os.Stat(sidecar); err == nil {
		t.Fatal("sidecar should not exist yet")
	}

	pp := NewMetadataPostProcessor()
	info := &MediaInfo{TweetID: "1", Num: 1, Extension: "jpg", Category: "twitter"}
	if err := pp.OnExisting(context.Background(), media, info); err != nil {
		t.Fatalf("OnExisting: %v", err)
	}
	if _, err := os.Stat(sidecar); err != nil {
		t.Fatalf("expected sidecar to be created: %v", err)
	}
}

// TestMetadataOnExisting_LeavesExistingSidecar verifies that OnExisting does not
// overwrite a sidecar that is already present.
func TestMetadataOnExisting_LeavesExistingSidecar(t *testing.T) {
	dir := t.TempDir()
	media := filepath.Join(dir, "1_1.jpg")
	if err := os.WriteFile(media, []byte("img"), 0644); err != nil {
		t.Fatal(err)
	}
	sidecar := media + ".json"
	if err := os.WriteFile(sidecar, []byte("PRESERVE"), 0644); err != nil {
		t.Fatal(err)
	}

	pp := NewMetadataPostProcessor()
	info := &MediaInfo{TweetID: "1", Num: 1, Extension: "jpg"}
	if err := pp.OnExisting(context.Background(), media, info); err != nil {
		t.Fatalf("OnExisting: %v", err)
	}
	got, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "PRESERVE" {
		t.Errorf("existing sidecar was overwritten: %q", got)
	}
}

// TestMetadataProcessorImplementsBackfill guards the interface wiring so the
// Download skip path keeps invoking backfill.
func TestMetadataProcessorImplementsBackfill(t *testing.T) {
	var _ BackfillProcessor = NewMetadataPostProcessor()
}
