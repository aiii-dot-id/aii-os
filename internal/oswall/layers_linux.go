//go:build linux || android

package oswall

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Layer struct {
	Path   string
	Source string
	Write  bool
}

func Bind(path string, write bool) Layer { return Layer{Path: path, Source: path, Write: write} }

func Mask(path string) Layer { return Layer{Path: path} }

func Sort(layers []Layer) {
	sort.SliceStable(layers, func(i, j int) bool {
		a, b := layers[i], layers[j]
		if da, db := strings.Count(a.Path, string(filepath.Separator)), strings.Count(b.Path, string(filepath.Separator)); da != db {
			return da < db
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Source != "" && b.Source == ""
	})
}

func Args(layers []Layer, shown func(string) bool) []string {
	var args, readOnly []string
	for _, l := range layers {
		if l.Path == "" {
			continue
		}
		if l.Source != "" {
			if _, err := os.Stat(l.Source); err != nil {
				continue
			}
			op := "--ro-bind"
			if l.Write {
				op = "--bind"
			}
			args = append(args, op, l.Source, l.Path)
			continue
		}
		if shown != nil && !shown(l.Path) {
			continue
		}
		info, err := os.Lstat(l.Path)
		switch {
		case err != nil:
		case info.IsDir():
			args = append(args, "--tmpfs", l.Path)
			readOnly = append(readOnly, l.Path)
		case info.Mode().IsRegular():
			args = append(args, "--ro-bind", os.DevNull, l.Path)
		}
	}
	for _, dir := range readOnly {
		args = append(args, "--remount-ro", dir)
	}
	return args
}

func View(paths []string, write bool) []string {
	var args []string
	op := "--ro-bind"
	if write {
		op = "--bind"
	}
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			continue
		}
		p = filepath.Clean(p)
		target, err := filepath.EvalSymlinks(p)
		if err != nil {
			continue
		}
		args = append(args, op, target, target)
		if target != p {
			args = append(args, "--symlink", target, p)
		}
	}
	return args
}

func Within(dir, path string) bool {
	return dir == path || Below(dir, path)
}

func Shown(bound ...[]string) func(string) bool {
	var paths []string
	for _, set := range bound {
		for _, b := range set {
			if b == "" {
				continue
			}
			b = filepath.Clean(b)
			paths = append(paths, b)
			if target, err := filepath.EvalSymlinks(b); err == nil && target != b {
				paths = append(paths, target)
			}
		}
	}
	return func(p string) bool {
		for _, b := range paths {
			if Within(b, p) {
				return true
			}
		}
		return false
	}
}
