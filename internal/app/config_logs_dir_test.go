package app

import (
	"path/filepath"
	"testing"
)

// logsDirIn is the logs section of a configuration file as 0.1.23 reads it:
// whether the file holds one, and the dir it names there. 0.1.23 reads a
// present section into a blank one and gives the default "log" only to an
// absent section (config.go:1308, :1627 at c17bdeb4), so a section that
// names no dir is one that turns its log file off (logsink.go:248).
func logsDirIn(t *testing.T, path string) (present, named bool, dir string) {
	t.Helper()
	d, err := parseJSONDoc([]byte(readFile(t, path)))
	if err != nil {
		t.Fatal(err)
	}
	logs := d.member("logs")
	if logs == nil {
		return false, false, ""
	}
	v := logs.member("dir")
	if v == nil {
		return true, false, ""
	}
	return true, true, string(v.compact())
}

func TestALogsSectionThisVersionWritesNamesItsDir(t *testing.T) {
	t.Run("a save of the level", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.json")
		cfg, err := LoadConfig(path)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Logs.Level = "debug"
		if _, err := saveConfig(cfg); err != nil {
			t.Fatal(err)
		}
		if present, named, dir := logsDirIn(t, path); !present || !named || dir != `"log"` {
			t.Fatalf("the save wrote a logs section with dir named %v (%s):\n%s", named, dir, readFile(t, path))
		}
		if again, err := ReadConfig(path); err != nil || again.Logs.Dir != "log" || again.Logs.Level != "debug" {
			t.Fatalf("read back: %+v %v", again.Logs, err)
		}
	})
	t.Run("a save over a section the file holds without its dir", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.json")
		writeFile(t, path, "{\n  \"logs\": {\n    \"level\": \"debug\"\n  }\n}")
		cfg, err := ReadConfig(path)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Timezone = "Europe/Oslo"
		if _, err := saveConfig(cfg); err != nil {
			t.Fatal(err)
		}
		if present, named, dir := logsDirIn(t, path); !present || !named || dir != `"log"` {
			t.Fatalf("the save left a logs section with dir named %v (%s):\n%s", named, dir, readFile(t, path))
		}
	})
	t.Run("a deliberate off", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.json")
		writeFile(t, path, "{\n  \"logs\": {\n    \"dir\": \"\"\n  }\n}")
		cfg, err := ReadConfig(path)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Logs.Level = "warn"
		if _, err := saveConfig(cfg); err != nil {
			t.Fatal(err)
		}
		if _, named, dir := logsDirIn(t, path); !named || dir != `""` {
			t.Fatalf("a named empty dir did not stay off:\n%s", readFile(t, path))
		}
	})
	t.Run("the strip of a whole file whose level the operator changed", func(t *testing.T) {
		a, dir := wholeIdentity(t, func(m map[string]any) { section(m, "logs")["level"] = "debug" }, nil)
		a.stripShipped()
		path := filepath.Join(dir, "config.json")
		if present, named, d := logsDirIn(t, path); !present || !named || d != `"log"` {
			t.Fatalf("the strip left a logs section with dir named %v (%s):\n%s", named, d, readFile(t, path))
		}
		doc, _ := parseJSONDoc([]byte(readFile(t, path)))
		if logs := doc.member("logs"); logs.member("max_backups") != nil || logs.member("compress_days") != nil {
			t.Fatalf("the shipped rotation stayed in the file:\n%s", readFile(t, path))
		}
		if now := a.configSnapshot(); now.Logs.Dir != "log" || now.Logs.Level != "debug" {
			t.Fatalf("in force after the strip: %+v", now.Logs)
		}
	})
	t.Run("the strip of a whole file the operator left as shipped", func(t *testing.T) {
		a, dir := wholeIdentity(t, nil, nil)
		a.stripShipped()
		if present, _, _ := logsDirIn(t, filepath.Join(dir, "config.json")); present {
			t.Fatalf("a logs section no one changed stayed:\n%s", readFile(t, filepath.Join(dir, "config.json")))
		}
	})
}
