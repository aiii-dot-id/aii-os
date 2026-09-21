package install

import (
	"os"
	"path/filepath"
)

// .
// .
// .
// .
// .
// .
// .
// .
const ConfigDirName = "config"

// .
// .
// .
const (
	ConfigFileName    = "config.json"
	ProvidersFileName = "providers.json"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func ConfigPathIn(dir string) string {
	inDir := filepath.Join(dir, ConfigDirName, ConfigFileName)
	if _, err := os.Stat(inDir); err == nil {
		return inDir
	}
	if beside := filepath.Join(dir, ConfigFileName); statOK(beside) {
		return beside
	}
	return inDir
}

func statOK(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
