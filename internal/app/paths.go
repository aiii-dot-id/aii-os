package app

import "github.com/aiii-dot-id/aii-os/internal/install"

// .
// .
// .
const (
	ConfigDirName     = install.ConfigDirName
	ConfigFileName    = install.ConfigFileName
	ProvidersFileName = install.ProvidersFileName
)

// .
// .
func DefaultConfigPath() string { return install.ConfigPathIn("") }

// .
// .
func ConfigPathIn(dir string) string { return install.ConfigPathIn(dir) }
