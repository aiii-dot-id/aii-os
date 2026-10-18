//go:build darwin && !ios

package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

func TestOnAMacTheSeededDocsAreTheIdentitysThroughTheWalledShell(t *testing.T) {
	if _, err := os.Stat("/usr/bin/sandbox-exec"); err != nil {
		t.Skipf("no sandbox-exec on this host: %v", err)
	}
	home := filepath.Join(t.TempDir(), "identity-0")
	data, configDir := filepath.Join(home, "data"), filepath.Join(home, "config")
	genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "SeededDocsTest",
		KeyPath:    filepath.Join(data, "identity.sec"),
		LedgerPath: filepath.Join(data, "ledger.jsonl"),
		DBPath:     filepath.Join(data, "aii.db"),
	}).Ledger.Close()
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(home)
	withProcessTable(t, "")
	cfg := defaultConfig()
	cfg.Identity.KeyPath, cfg.Identity.LedgerPath, cfg.Identity.DBPath = "data/identity.sec", "data/ledger.jsonl", "data/aii.db"
	cfg.LLM = withTestProvider(t, configDir, "test", "https://127.0.0.1:1", "m", "sk-x")
	cfg.SourcePath = filepath.Join(configDir, "config.json")
	cfg.Dashboard.Port = 0
	cfg.Dashboard.RequireToken = true // the sandbox is off only while the dashboard asks for its token
	cfg.Tools.CWD = "."
	cfg.Machine.Dir = filepath.Join(t.TempDir(), "machine")
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	app := New(cfg)
	app.seedSkillsDoc()
	app.seedMethodDoc()
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, app)

	shell := func(command string) tools.Result {
		t.Helper()
		res, err := app.toolReg.ExecuteModel(context.Background(), "shell", map[string]any{"command": command})
		if err != nil {
			t.Fatalf("shell %q: %v", command, err)
		}
		return res
	}
	quoted := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
	read := func(p string) string {
		t.Helper()
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	works := func(what, command string) string {
		t.Helper()
		res := shell(command)
		if res.Refused || res.ExitStatus == nil || *res.ExitStatus != 0 {
			exit := -1
			if res.ExitStatus != nil {
				exit = *res.ExitStatus
			}
			t.Fatalf("%s: %s did not work (refused %v, exit %d): %s%s", what, command, res.Refused, exit, res.Error, res.Output)
		}
		return res.Output
	}
	refused := func(what, command string) {
		t.Helper()
		res := shell(command)
		if res.Refused {
			t.Fatalf("%s: the word check refused %s (%q); the wall is what is under test", what, command, res.Error)
		}
		if res.ExitStatus != nil && *res.ExitStatus == 0 || !strings.Contains(res.Output, "Operation not permitted") || strings.Contains(res.Output, `"dashboard"`) {
			t.Fatalf("%s: %s was not refused by the wall:\n%s", what, command, res.Output)
		}
	}

	docs := map[string]string{
		"SKILLS.md": "# SKILLS.md — what this binary can do",
		"METHOD.md": "The Method — A Practice with First Principles",
	}
	config := cfg.SourcePath
	for _, mode := range []string{"on", "off"} {
		if mode == "off" {
			if err := app.setSandboxOff(true, true); err != nil || !app.toolReg.SandboxOff() {
				t.Fatalf("the operator's switch: %v", err)
			}
		}
		for doc, first := range docs {
			abs := filepath.Join(home, doc)
			// The read tool beside the shell opens it too.
			if out := toolRead(app, doc); !strings.Contains(out, first) {
				t.Fatalf("the read tool, sandbox %s, did not open %s: %q", mode, doc, out)
			}
			for _, name := range []string{doc, abs} {
				tag := mode + "-" + strings.TrimSuffix(doc, ".md") + "-" + map[bool]string{true: "relative", false: "absolute"}[name == doc]
				if out := works("cat "+tag, "cat "+quoted(name)); !strings.Contains(out, first) {
					t.Fatalf("cat %s: the doc's text is not what was read:\n%s", tag, out)
				}
				works("append "+tag, "printf '%s\\n' "+quoted("appended "+tag)+" >> "+quoted(name))
				if !strings.Contains(read(abs), "\nappended "+tag+"\n") {
					t.Fatalf("append %s: the line is not in the doc", tag)
				}
				works("sed -i "+tag, "sed -i '' "+quoted("s/^appended "+tag+"$/edited "+tag+"/")+" "+quoted(name))
				if got := read(abs); !strings.Contains(got, "\nedited "+tag+"\n") || strings.Contains(got, "appended "+tag) {
					t.Fatalf("sed -i %s: the doc was not edited in place", tag)
				}
			}
		}
		before := read(config)
		for _, spelled := range []string{`"config/$p$q"`, `"$h/config/$p$q"`} {
			at := "p=conf; q=ig.json; h=" + quoted(home) + "; "
			refused("the configuration, "+mode, at+"cat "+spelled)
			refused("an append to the configuration, "+mode, at+"printf x >> "+spelled)
			refused("sed -i on the configuration, "+mode, at+"sed -i '' 's/a/b/' "+spelled)
		}
		if read(config) != before {
			t.Fatalf("the configuration changed behind the wall with the sandbox %s", mode)
		}
	}
}
