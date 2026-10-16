//go:build darwin && !ios

package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/uninstall"
)

type uninstallUI struct {
	aii func(args ...string) (stdout, stderr []byte, code int, err error)

	ask func(question, yes, no string) (answer, asked bool)

	tell func(title, message string)

	reveal func(path string) error
}

func runUninstallFlow(ui uninstallUI, bundle string) error {
	plan, err := ui.report("-plan")
	if err != nil {
		return err
	}
	if yes, _ := ui.ask(confirmText(plan, bundle), "Uninstall", "Cancel"); !yes {
		return nil
	}
	done, err := ui.report("-yes")
	if err != nil {
		return err
	}
	if done.Stopped != nil {
		ui.tell("AII OS stopped part way", stoppedText(done))
		return nil
	}
	if show, _ := ui.ask(sheetText(done, bundle), "Show in Finder", "Done"); show {
		return ui.reveal(bundle)
	}
	return nil
}

func (ui uninstallUI) report(mode string) (uninstall.Report, error) {
	var r uninstall.Report
	out, errOut, code, err := ui.aii("uninstall", mode, "-json")
	if err != nil {
		return r, fmt.Errorf("aii uninstall could not run: %w", err)
	}
	if code != 0 && code != 3 {
		return r, fmt.Errorf("aii uninstall %s: %s", mode, strings.TrimSpace(string(errOut)))
	}
	if err := json.Unmarshal(out, &r); err != nil || r.Format != uninstall.Format {
		return r, fmt.Errorf("aii uninstall %s answered with a report this app does not read (format %d): %v", mode, r.Format, err)
	}
	return r, nil
}

func confirmText(plan uninstall.Report, bundle string) string {
	var b strings.Builder
	b.WriteString("Uninstall AII OS from this account?\n\n")
	var stopping []string
	for _, id := range plan.Identities {
		if (id.Registered || id.Running) && id.Service == "" {
			stopping = append(stopping, "• "+identityLine(id))
		}
	}
	if len(stopping) > 0 {
		b.WriteString("These identities stop now, and do not start again at login:\n" + strings.Join(stopping, "\n") + "\n\n")
	}
	b.WriteString("Every identity's data stays exactly where it is — its record, its keys, everything in its folder. Nothing of any identity is removed.\n\n")
	b.WriteString("The machine service is removed, with the plugins' models and runtimes it keeps (" + sizeSaid(plan.FreedBytes) + ").\n\n")
	b.WriteString("Afterwards you drag " + filepath.Base(bundle) + " to the Trash.")
	return b.String()
}

func sheetText(r uninstall.Report, bundle string) string {
	var b strings.Builder
	b.WriteString("AII OS is uninstalled from this account.\n\n")
	var folders []string
	for _, s := range r.Stays {
		if s.What == uninstall.StayIdentity {
			line := "• " + nameOr(s.Name, filepath.Base(s.Path)) + ": " + s.Path
			if s.RealPath != "" && s.RealPath != s.Path {
				line += " (on " + s.RealPath + ")"
			}
			folders = append(folders, line)
		}
	}
	if len(folders) > 0 {
		b.WriteString("What stays — every identity's folder, untouched:\n" + strings.Join(folders, "\n") + "\n\n")
	}
	b.WriteString("Removed: the machine service, its directory and the plugins' store (" + sizeSaid(r.FreedBytes) + " freed).\n\n")
	for _, p := range r.Programs {
		if len(p.SharedWith) > 0 && samePathName(p.Path, bundle) {
			b.WriteString("Another account on this Mac runs this " + filepath.Base(bundle) + " now: if you move it to the Trash, theirs stops working too.\n\n")
		}
	}
	b.WriteString("Last step: drag " + filepath.Base(bundle) + " to the Trash. Show in Finder shows it.")
	return b.String()
}

func stoppedText(r uninstall.Report) string {
	return "The uninstall stopped at its " + r.Stopped.Step + " step: " + r.Stopped.Reason + "\n\nWhat to do: " + r.Stopped.Remedy + "\n\nNothing of any identity was removed."
}

func identityLine(id uninstall.Identity) string {
	return nameOr(id.Name, id.Slot) + " (" + id.Path + ")"
}

func nameOr(name, other string) string {
	if name == "" {
		return other + ", not yet born"
	}
	return name
}

func samePathName(a, b string) bool { return filepath.Clean(a) == filepath.Clean(b) }

func sizeSaid(n int64) string {
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
