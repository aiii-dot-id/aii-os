//go:build (linux && !android) || (darwin && !ios) || windows

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/app"
	"github.com/aiii-dot-id/aii-os/internal/install"
	"github.com/aiii-dot-id/aii-os/internal/uninstall"
)

const uninstallUsage = `Usage: aii uninstall [-plan] [-yes] [-json]

Uninstalls AII OS from this account, in one order: every identity is
unregistered and stopped, then the machine service, then the account's
plugin stores and the service's directory are removed. Every identity's
slot stays exactly as it is — its record, its key, its data — and is named
at the end with everything else that stays. The program itself is yours to
remove, as the end says.

  -plan   say what would be done, and do nothing
  -yes    do it without asking
  -json   write the report as JSON (format 1)

Exit status: 0 done, 3 stopped part way (do what it says, then run it
again), 1 an error or a refusal, 2 a usage error.
`

var uninstallEnv = func() uninstall.Env { return uninstall.DefaultEnv(slotKey, slotService) }

var uninstallInput io.Reader = os.Stdin

func runUninstall(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, uninstallUsage) }
	planOnly := fs.Bool("plan", false, "say what would be done, and do nothing")
	yes := fs.Bool("yes", false, "do it without asking")
	asJSON := fs.Bool("json", false, "write the report as JSON")
	if code, done := parseVerbFlags(fs, args); done {
		return code
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return 2
	}
	env := uninstallEnv()
	plan, err := uninstall.Run(env, true)
	if err != nil {
		fmt.Fprintln(stderr, "uninstall:", err)
		return 1
	}
	if *planOnly {
		writeReport(stdout, plan, *asJSON)
		return 0
	}
	if !*yes {

		ask := stdout
		if *asJSON {
			ask = stderr
		}
		writeReport(ask, plan, false)
		fmt.Fprint(ask, "\nType yes to uninstall: ")
		line, _ := bufio.NewReader(uninstallInput).ReadString('\n')
		if strings.TrimSpace(line) != "yes" {
			fmt.Fprintln(stderr, "uninstall: nothing was changed")
			return 1
		}
	}
	r, err := uninstall.Run(env, false)
	if err != nil {
		fmt.Fprintln(stderr, "uninstall:", err)
		return 1
	}
	writeReport(stdout, r, *asJSON)
	return r.ExitCode()
}

func writeReport(w io.Writer, r uninstall.Report, asJSON bool) {
	if asJSON {
		raw, _ := json.MarshalIndent(r, "", "  ")
		fmt.Fprintf(w, "%s\n", raw)
		return
	}
	if len(r.Identities) > 0 {
		fmt.Fprintln(w, "Identities (each one's slot stays exactly as it is):")
		for _, id := range r.Identities {
			state := ""
			if id.Running {
				state = " (running)"
			}
			fmt.Fprintf(w, "  %-12s %-20s %s%s\n", id.Slot, nameOrUnborn(id.Name), id.Path, state)
		}
	}
	if r.Plan {
		fmt.Fprintln(w, "This would:")
		for _, s := range r.Steps {
			fmt.Fprintln(w, "  -", s)
		}
		fmt.Fprintf(w, "It frees about %s.\n", bytesSaid(r.FreedBytes))
		return
	}
	for _, d := range r.Done {
		fmt.Fprintf(w, "Done (%s): %s\n", d.Step, d.Did)
	}
	if r.Stopped != nil {
		fmt.Fprintf(w, "Stopped (%s): %s\n  What to do: %s\n", r.Stopped.Step, r.Stopped.Reason, r.Stopped.Remedy)
	}
	for _, n := range r.Notes {
		fmt.Fprintln(w, "Note:", n)
	}
	if len(r.Stays) > 0 {
		fmt.Fprintln(w, "What stays:")
		for _, s := range r.Stays {
			where := s.Path
			if s.RealPath != "" && s.RealPath != s.Path {
				where += " -> " + s.RealPath
			}
			if s.Name != "" {
				where += " (" + s.Name + ")"
			}
			if where != "" {
				where += ": "
			}
			fmt.Fprintf(w, "  %s: %s%s\n", s.What, where, s.Why)
		}
	}
	for _, p := range r.Programs {
		line := "To finish, " + p.Remove + "."
		if len(p.SharedWith) > 0 {
			line += fmt.Sprintf(" Another account (uid %v) runs it now: removing it stops theirs too.", p.SharedWith)
		}
		fmt.Fprintln(w, line)
	}
	if r.FreedBytes > 0 {
		fmt.Fprintf(w, "Freed %s.\n", bytesSaid(r.FreedBytes))
	}
}

func nameOrUnborn(name string) string {
	if name == "" {
		return "(not yet born)"
	}
	return name
}

func bytesSaid(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}

func slotService(dir string) (string, error) {
	cfg, err := app.ReadConfig(install.ConfigPathIn(dir))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	switch d := cfg.Machine.Dir; {
	case d == "":
		return "", nil
	case filepath.IsAbs(d):
		return filepath.Clean(d), nil
	default:
		return filepath.Join(dir, d), nil
	}
}
