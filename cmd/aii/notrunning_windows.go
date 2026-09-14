//go:build windows

package main

import "github.com/aiii-dot-id/aii-os/internal/install"

// .
// .
// .
func isNotRunning(err error) bool { return err == install.ErrNotRunning }
