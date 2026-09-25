//go:build windows

package gallery

import "golang.org/x/sys/windows"

func replaceFile(old, new string) error {
	a, err := windows.UTF16PtrFromString(old)
	if err != nil {
		return err
	}
	b, err := windows.UTF16PtrFromString(new)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(a, b, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
