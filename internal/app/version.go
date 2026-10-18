package app

import "github.com/aiii-dot-id/aii-os/internal/version"

func BuildIdentity() string { return version.Commit() }

func VersionString() string {
	return Current()
}

func Current() string {
	return version.Authored()
}
