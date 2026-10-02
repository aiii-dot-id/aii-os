//go:build windows && (amd64 || arm64)

package driver

import "golang.org/x/sys/windows"

func openLoader() (func(string) (uintptr, error), func(), error) {
	h, err := windows.LoadLibraryEx("vulkan-1.dll", 0, windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	if err != nil {
		return nil, nil, err
	}
	return func(name string) (uintptr, error) { return windows.GetProcAddress(h, name) }, func() { _ = windows.FreeLibrary(h) }, nil
}
