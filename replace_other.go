//go:build !windows

package gallery

import "os"

func replaceFile(old, new string) error { return os.Rename(old, new) }
