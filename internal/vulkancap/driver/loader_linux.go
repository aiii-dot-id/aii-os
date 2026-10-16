//go:build linux && !android && (amd64 || arm64)

package driver

import "github.com/ebitengine/purego"

func openLoader() (func(string) (uintptr, error), func(), error) {
	h, err := purego.Dlopen("libvulkan.so.1", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, nil, err
	}
	return func(name string) (uintptr, error) { return purego.Dlsym(h, name) }, func() { _ = purego.Dlclose(h) }, nil
}
