package tools

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestQuotedGrantedFolderWorksThroughTheShellTool(t *testing.T) {
	r, _, file := spacedGrant(t)
	command := "cat '" + file + "'"
	if shellDialect == "PowerShell" {
		command = "Get-Content -LiteralPath '" + file + "'"
	}
	result, err := r.Execute(context.Background(), "shell", map[string]any{"command": command})
	if err != nil || result.Error != "" || !strings.Contains(result.Output, "package config") {
		t.Fatalf("granted quoted path cannot be read through the real tool: %+v %v", result, err)
	}
}

// The wall reads a command as its shell does: an argument at a time,
// quotes honoured, operators ending a word — and, where the shell is
// bash, a backslash escaping the character after it.
func TestShellWordsReadACommandAsItsShellDoes(t *testing.T) {
	cases := []struct {
		cmd  string
		want []string
	}{
		{`cat notes.md`, []string{"cat", "notes.md"}},
		{`cat 'My Projects/notes.md'`, []string{"cat", "My Projects/notes.md"}},
		{`cat "My Projects/notes.md"`, []string{"cat", "My Projects/notes.md"}},
		{`cat x|head -1`, []string{"cat", "x", "head", "-1"}},
		{`(cat x)`, []string{"cat", "x"}},
		{`cat x;`, []string{"cat", "x"}},
		{`cat a&&cat b`, []string{"cat", "a", "cat", "b"}},
		{`pro"vi"ders.json`, []string{"providers.json"}},
		{`echo "a; cat b"`, []string{"echo", "a; cat b"}},
		{`echo ''`, []string{"echo"}},
		{`cat 'unterminated`, []string{"cat", "unterminated"}},
	}
	if shellDialect == "bash" {
		cases = append(cases,
			struct {
				cmd  string
				want []string
			}{`cat My\ Projects/notes.md`, []string{"cat", "My Projects/notes.md"}},
			struct {
				cmd  string
				want []string
			}{`cat pro\viders.json`, []string{"cat", "providers.json"}},
			struct {
				cmd  string
				want []string
			}{`echo "a \"b\" \$c \n"`, []string{"echo", `a "b" $c \n`}},
		)
	} else {
		cases = append(cases,
			struct {
				cmd  string
				want []string
			}{`type 'C:\dir with space\x.txt'`, []string{"type", `C:\dir with space\x.txt`}},
			struct {
				cmd  string
				want []string
			}{`type C:\dir\x.txt`, []string{"type", `C:\dir\x.txt`}},
		)
	}
	for _, c := range cases {
		for _, v := range []bashVersion{bash3, bash5} {
			if got := shellWords(c.cmd, v); !reflect.DeepEqual(got, c.want) {
				t.Errorf("shellWords(%q, %d) = %q, want %q", c.cmd, v, got, c.want)
			}
		}
	}
}

func spacedGrant(t *testing.T) (r *Registry, granted, file string) {
	t.Helper()
	home := t.TempDir()
	granted = filepath.Join(t.TempDir(), "My Projects")
	deep := filepath.Join(granted, "aii-os", "config")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	file = filepath.Join(deep, "config.go")
	for name, body := range map[string]string{
		file:                                     "package config",
		filepath.Join(granted, "providers.json"): "{}",
	} {
		if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "ledger.jsonl"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = NewRegistry(home, nil, Timeouts{})
	r.SetExtraRoots([]string{granted})
	return r, granted, file
}

// A granted folder with a space in its name — "/srv/Shared Models/work",
// "~/My Projects" — was refused in every quoting: the wall cut on
// whitespace, so the name's head was an absolute path outside every
// root and its tail a relative one matched on its bare name. Quoted as
// the shell needs it, the name is one argument and the grant holds, in
// every spelling the shell accepts and one level of nesting down.
func TestAGrantedFolderWithASpaceIsReachableWhenQuoted(t *testing.T) {
	r, granted, file := spacedGrant(t)
	cmds := []string{
		"cat '" + file + "'",
		`cat "` + file + `"`,
		"cd '" + granted + "' && git status",
		"cat '" + filepath.Join(granted, "providers.json") + "'",
		"cp '" + file + "' '" + filepath.Join(granted, "copy.go") + "'",
		"grep -n config '" + filepath.Join(granted, "aii-os", "config") + "'/*.go",
		"bash -c 'cat \"" + file + "\"'",
	}
	if shellDialect == "bash" {
		cmds = append(cmds, "cat "+strings.ReplaceAll(file, " ", `\ `))
	}
	for _, cmd := range cmds {
		if why := r.shellRefusal(cmd); why != "" {
			t.Errorf("a granted folder with a space was refused: %q -> %s", cmd, why)
		}
	}
}

// The home itself may carry the space (a Mac whose data drive does),
// and a grant may be nested in it; both spellings of a name agree.
func TestASpacedHomeAndANestedSpacedGrantAgreeInBothSpellings(t *testing.T) {
	home := filepath.Join(t.TempDir(), "with space")
	work := filepath.Join(home, "My Projects")
	deep := filepath.Join(work, "aii-os", "config")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	// config.json: a name the floor keeps in the home, the grant's to open.
	if err := os.WriteFile(filepath.Join(deep, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(home, nil, Timeouts{})
	r.SetExtraRoots([]string{work})
	for _, cmd := range []string{
		"cat notes/todo.md",
		"cat '" + filepath.Join(home, "notes", "todo.md") + "'",
		"cat 'My Projects/aii-os/config/config.json'",
		"cat '" + filepath.Join(deep, "config.json") + "'",
		"cd 'My Projects' && git status",
		"cd '" + work + "' && git status",
	} {
		if why := r.shellRefusal(cmd); why != "" {
			t.Errorf("a name in a spaced home was refused: %q -> %s", cmd, why)
		}
	}
}

// The control half: a space hides nothing. Unquoted, the shell hands a
// spaced name over as two arguments and the wall reads it so; a nested
// command is read as the nested shell reads it; a rooted piece after a
// name's head is a reference of its own; and a name split by quotes or
// a backslash now arrives whole.
func TestASpaceDoesNotHideAnEscape(t *testing.T) {
	r, granted, file := spacedGrant(t)
	refused := []string{
		"cat " + file,
		"bash -c 'cat /etc/passwd'",
		"bash -c \"cd '" + granted + "'; cat /etc/passwd\"",
		"bash -c 'cat data/ledger.jsonl'",
		"bash -c 'cat ../../../etc/passwd'",
		"bash -c '" + file + " /etc/passwd'",
		"cat '" + file + " /etc/passwd'",
		`cat pro"vi"ders.json`,
		"cat 'data/ledger.jsonl'",
		"cat 'with space/providers.json'",
	}
	if shellDialect == "bash" {
		refused = append(refused, `cat pro\viders.json`, `cat /etc/pass\wd`)
	}
	for _, cmd := range refused {
		if why := r.shellRefusal(cmd); why == "" {
			t.Errorf("an escape hid behind a space or a quote: %q was allowed", cmd)
		}
	}
	// The refusal names what the shell would hand over, not a fragment.
	if why := r.shellRefusal("cat 'with space/providers.json'"); !strings.Contains(why, "with space/providers.json") {
		t.Errorf("the refusal did not name the argument: %s", why)
	}
}
