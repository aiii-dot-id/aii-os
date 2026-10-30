//go:build darwin && !ios

package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOnAMacTheShellReadsTheLogAndCannotChangeItButForItsSwitch(t *testing.T) {
	if _, err := os.Stat("/usr/bin/sandbox-exec"); err != nil {
		t.Skipf("no sandbox-exec on this host: %v", err)
	}
	app, home := logWallApp(t)
	run := func(command string) (out string, exit int) {
		t.Helper()
		res, err := app.toolReg.Execute(context.Background(), "shell", map[string]any{"command": command})
		if err != nil {
			t.Fatalf("shell %q: %v", command, err)
		}
		if res.Refused {
			t.Fatalf("the word check refused %q (%s); the wall is what is under test", command, res.Error)
		}
		exit = -1
		if res.ExitStatus != nil {
			exit = *res.ExitStatus
		}
		return res.Output, exit
	}
	const dir = `d=lo; e=g; `
	for _, rel := range []string{"aii-rolled.log", "llm/a-capture.json"} {
		if out, exit := run(dir + `cat "$d$e/` + rel + `"`); exit != 0 || !strings.Contains(out, "MARK") {
			t.Errorf("the shell reads log/%s (exit %d):\n%s", rel, exit, out)
		}
		for what, command := range map[string]string{
			"append":   `echo SPRAY >> "$d$e/` + rel + `"`,
			"remove":   `rm -f "$d$e/` + rel + `"`,
			"new file": `echo SPRAY > "$d$e/` + filepath.Dir(rel) + `/forged.log"`,
		} {
			if out, exit := run(dir + command); exit == 0 || !strings.Contains(out, "Operation not permitted") {
				t.Errorf("%s of log/%s is refused by the wall (exit %d):\n%s", what, rel, exit, out)
			}
		}
		if b, err := os.ReadFile(filepath.Join(home, "log", rel)); err != nil || !strings.HasSuffix(string(b), "MARK\n") {
			t.Fatalf("log/%s was changed: %q %v", rel, b, err)
		}
	}
	if out, exit := run(dir + `echo '{"level":"debug"}' > "$d$e/control.json"`); exit != 0 {
		t.Fatalf("the shell writes the identity's switch (exit %d):\n%s", exit, out)
	}
	if ctl, err := ReadLogControl(filepath.Join(home, "log", LogControlFileName)); err != nil || ctl.Level != "debug" {
		t.Fatalf("the switch the shell wrote: %+v %v", ctl, err)
	}
}
