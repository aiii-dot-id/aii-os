//go:build windows

package atomicfile

import "golang.org/x/sys/windows"

// Replace atomically publishes oldPath at newPath with write-through.
func Replace(oldPath, newPath string) (bool, error) {
	oldName, err := windows.UTF16PtrFromString(oldPath)
	if err != nil {
		return false, err
	}
	newName, err := windows.UTF16PtrFromString(newPath)
	if err != nil {
		return false, err
	}
	if err := windows.MoveFileEx(oldName, newName,
		windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return false, err
	}
	return true, nil
}

// PublishNew atomically publishes oldPath and refuses an existing newPath.
func PublishNew(oldPath, newPath string) (bool, error) {
	oldName, err := windows.UTF16PtrFromString(oldPath)
	if err != nil {
		return false, err
	}
	newName, err := windows.UTF16PtrFromString(newPath)
	if err != nil {
		return false, err
	}
	if err := windows.MoveFileEx(oldName, newName, windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return false, err
	}
	return true, nil
}
