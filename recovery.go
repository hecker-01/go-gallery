package gallery

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RepairRecord persists the identity of an unfinished media transfer. Empty
// TweetID denotes a legacy partial whose identity must be rediscovered by scanning.
type RepairRecord struct {
	Destination string `json:"destination"`
	TweetID     string `json:"tweet_id,omitempty"`
	MediaIndex  int    `json:"media_index,omitempty"`
	SourceURL   string `json:"source_url,omitempty"`
}

// CheckOutputPath rejects traversal and symlinks/junctions in existing components.
func CheckOutputPath(root, path string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path outside output directory: %s", path)
	}
	for current := path; ; current = filepath.Dir(current) {
		fi, err := os.Lstat(current)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return fmt.Errorf("refusing linked output path: %s", current)
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	return nil
}

func repairPath(root string, r RepairRecord) string {
	dest, _ := filepath.Abs(r.Destination)
	return filepath.Join(root, ".twdl", "repairs", fmt.Sprintf("%x.json", sha256.Sum256([]byte(filepath.Clean(dest)))))
}
func SaveRepairRecord(root string, r RepairRecord) error {
	var err error
	r.Destination, err = filepath.Abs(r.Destination)
	if err != nil {
		return err
	}
	if err = CheckOutputPath(root, r.Destination); err != nil {
		return err
	}
	path := repairPath(root, r)
	if err = CheckOutputPath(root, path); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, data, 0600)
}
func RemoveRepairRecord(root string, r RepairRecord) error {
	path := repairPath(root, r)
	if err := CheckOutputPath(root, path); err != nil {
		return err
	}
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
func LoadRepairRecords(root string) ([]RepairRecord, error) {
	dir := filepath.Join(root, ".twdl", "repairs")
	if err := CheckOutputPath(root, dir); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []RepairRecord
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if err := CheckOutputPath(root, path); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var r RepairRecord
		if err = json.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("repair record %s: %w", path, err)
		}
		if err = CheckOutputPath(root, r.Destination); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// WriteFileAtomic flushes a same-directory temporary file before replacement.
func WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".write-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return replaceFile(tmp, path)
}
