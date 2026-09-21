package app

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
// .
// .
// .
// .
const (
	LogControlDirName  = "log"
	LogControlFileName = "control.json"
)

// .
// .
// .
// .
type LogControl struct {
	Level  string              `json:"level,omitempty"`
	Detail map[string]string   `json:"detail,omitempty"`
	Group  map[string][]string `json:"group,omitempty"`
	// .
	// .
	// .
	// .
	Tap map[string]string `json:"tap,omitempty"`
}

// .
func (c LogControl) Empty() bool {
	return strings.TrimSpace(c.Level) == "" &&
		len(c.Detail) == 0 && len(c.Group) == 0 && len(c.Tap) == 0
}

// .
// .
// .
func LogControlPathIn(dir string) string {
	return filepath.Join(dir, LogControlDirName, LogControlFileName)
}

// .
// .
// .
// .
// .
// .
func ReadLogControl(path string) (LogControl, error) {
	var c LogControl
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return c, nil
		}
		return c, err
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return LogControl{}, err
	}
	return c, nil
}

// .
// .
// .
// .
// .
func WriteLogControl(path string, c LogControl) error {
	if c.Empty() {
		return ClearLogControl(path)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	_, err = writeFileAtomic(path, data)
	return err
}

// .
// .
// .
func ClearLogControl(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// .
// .
// .
// .
// .
// .
func identityHomeFromConfig(configPath string) string {
	dir := filepath.Dir(configPath)
	if filepath.Base(dir) == ConfigDirName {
		return filepath.Dir(dir)
	}
	return dir
}
