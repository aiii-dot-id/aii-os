//go:build !windows

package firewall

import "path/filepath"

func evalLinks(path string) (string, error) { return filepath.EvalSymlinks(path) }

func linkTarget(string) (target string, linked bool, err error) { return "", false, nil }
