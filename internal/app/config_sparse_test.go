package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A FIRST BOOT NAMES NOTHING BUT AN EMPTY ADDRESS BOOK, AND A SAVE WRITES
// ONLY WHAT IT CHANGED. A file written by the save that changed one setting
// holds that setting and no default, so a default a later release corrects
// reaches it. The address book is stated because its absence reads
// otherwise (stateContacts).
func TestASaveWritesNoDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != "{\n  \"contacts\": []\n}" {
		t.Fatalf("a first boot wrote %s, want a file that names nothing but an empty address book", got)
	}
	cfg.Agency.MaxParallelSubagents = 5
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"contacts\": [],\n  \"agency\": {\n    \"max_parallel_subagents\": 5\n  }\n}"
	if got := readFile(t, path); got != want {
		t.Fatalf("a save of one setting wrote:\n%s\nwant only that setting:\n%s", got, want)
	}
	// What is in force is computed at read: the worker count follows the
	// setting the file names, as for a file the operator wrote by hand.
	again, err := ReadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.Agency.MaxParallelSubagents != 5 || again.Agency.QueueWorkers != 5 || again.Agency.MaxToolRounds != defaultConfig().Agency.MaxToolRounds {
		t.Fatalf("read back: parallel %d, workers %d, rounds %s", again.Agency.MaxParallelSubagents, again.Agency.QueueWorkers, again.Agency.MaxToolRounds)
	}
}

func TestASaveWritesOnlyTheLineItChanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	writeFile(t, path, `{
  "timezone": "Europe/Oslo",
  "agency": {
    "subagent_max_tool_rounds": 50,
    "max_tool_rounds": 40
  }
}`)
	cfg, err := ReadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	// The operator edits the file by hand; the running identity has not
	// read it again when a door saves another setting.
	writeFile(t, path, `{
  "timezone": "Europe/Oslo",
  "agency": {
    "subagent_max_tool_rounds": 50,
    "max_tool_rounds": 40
  },
  "prompt": {
    "recent_turns": 7
  }
}`)
	cfg.Timezone = "America/New_York"
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	want := `{
  "timezone": "America/New_York",
  "agency": {
    "subagent_max_tool_rounds": 50,
    "max_tool_rounds": 40
  },
  "prompt": {
    "recent_turns": 7
  }
}`
	if got := readFile(t, path); got != want {
		t.Fatalf("the save wrote:\n%s\nwant the file as it stood with the one line changed:\n%s", got, want)
	}
}

// A SAVE OVER A FILE THAT DOES NOT READ IS REFUSED, by the file's name and
// its fault: what the operator wrote there is not written over.
func TestASaveOverAFileThatDoesNotReadIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	broken := `{"plugins": {"grants": {},}}`
	writeFile(t, path, broken)
	cfg.Timezone = "UTC"
	_, err = saveConfig(cfg)
	var unreadable *unreadableFileError
	if !errors.As(err, &unreadable) || !strings.Contains(err.Error(), "config.json does not read as it stands") || !strings.Contains(err.Error(), "nothing was saved") {
		t.Fatalf("a save over a file that does not read: %v", err)
	}
	if got := readFile(t, path); got != broken {
		t.Fatalf("the operator's file was written over: %s", got)
	}
}

func TestASaveDropsRetiredKeysAndKeepsWhatTheyCarried(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	writeFile(t, path, `{"tools": {"bash_timeout_seconds": 300}, "agency": {"heuristic_nudges": true}}`)
	cfg, err := ReadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Timezone = "UTC"
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, path)
	if strings.Contains(got, "bash_timeout_seconds") || strings.Contains(got, "heuristic_nudges") {
		t.Fatalf("a retired key was kept: %s", got)
	}
	again, err := ReadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.Tools.ShellTimeoutSeconds != 300 {
		t.Fatalf("the shell timeout the old name carried was lost: %d\n%s", again.Tools.ShellTimeoutSeconds, got)
	}
}

// EVERY LINE A FILE LEAVES OUT READS AS ITS DEFAULT, whatever else the
// file names beside it: a file that names one line of a section keeps the
// section's other defaults. The logs section, read into a blank one, lost
// its directory and its rotation to a file naming only its level.
func TestEveryLineLeftOutReadsAsItsDefault(t *testing.T) {
	whole, err := json.Marshal(defaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	d, err := parseJSONDoc(whole)
	if err != nil {
		t.Fatal(err)
	}
	docLeaves(d, nil, func(p []string, v *jsonDoc) { want[pathKey(p)] = string(canonicalJSON(v.compact())) })
	docLeaves(d, nil, func(p []string, _ *jsonDoc) {
		if slices.ContainsFunc(keptConfigPaths, func(k []string) bool { return slices.Equal(k, p[:min(len(k), len(p))]) }) {
			return // the address book: absent is not empty
		}
		trial, _ := parseJSONDoc(whole)
		removeAt(trial, p, 0)
		cfg, _, err := decodeConfigSaying("config.json", trial.encode())
		if err != nil {
			t.Errorf("without %s: %v", configPathName(p), err)
			return
		}
		raw, _ := json.Marshal(cfg)
		got, _ := parseJSONDoc(raw)
		docLeaves(got, nil, func(q []string, v *jsonDoc) {
			if w, ok := want[pathKey(q)]; ok && w != string(canonicalJSON(v.compact())) {
				t.Errorf("a file without %s reads %s as %s, not its default %s", configPathName(p), configPathName(q), v.compact(), w)
			}
		})
	})
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
