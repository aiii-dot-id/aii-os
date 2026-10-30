package tools

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

func TestTheIdentitysOwnLogCommandIsNotReadAsTheLog(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	logs := filepath.Join(home, "log")
	if err := os.MkdirAll(logs, 0o700); err != nil {
		t.Fatal(err)
	}
	policy := firewall.DefaultPolicy()
	policy.SetPlaces([]*firewall.Rule{{ID: "sub.logs", Kind: firewall.KindSubstrate, Path: logs, ReadOpen: true,
		Except: []string{filepath.Join(logs, "control.json")}, Reason: "kept", Enforced: true}})
	r := NewRegistry(home, policy, Timeouts{ShellSeconds: 30})
	for _, cmd := range []string{
		"aii log", "aii log level debug", "aii log level voice=debug llm=", "aii log tap dream.return on --for 20m",
		"aii log clear", "aii log details",
	} {
		if why, _ := r.shellWallFrom(policy, home, cmd); why != "" {
			t.Errorf("%q is the identity's own log command, and is refused: %s", cmd, why)
		}
	}
	for _, cmd := range []string{
		"rm -r log", "ls log", "aii log; rm -r log", "aii log && rm -r log", "aii log || rm -r log", "aii log & rm -r log",
		"aii log level debug > log/aii.log", "aii log $(rm -r log)", "aii log `rm -r log`", "aii log level x\nrm -r log",
		"echo aii log | sh", "aii log level debug log/aii.log", "./aii log", "aiii log", "rm -r log aii log",
	} {
		if why, _ := r.shellWallFrom(policy, home, cmd); why == "" {
			t.Errorf("%q names the log as more than the command's word, and is let through", cmd)
		}
	}
}
