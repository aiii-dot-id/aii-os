//go:build linux && !android

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

func openURL(url string) error { return exec.Command("xdg-open", url).Run() }

func alert(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	_ = exec.Command("notify-send", "--app-name=AII OS", "AII OS could not start", msg).Run()
}

func ask(question, yes, no string) (answer, asked bool) {
	var cmd *exec.Cmd
	if zenity, err := exec.LookPath("zenity"); err == nil {
		cmd = exec.Command(zenity, "--question", "--no-markup", "--title=AII OS", "--text="+question, "--ok-label="+yes, "--cancel-label="+no)
	} else if kdialog, err := exec.LookPath("kdialog"); err == nil {
		cmd = exec.Command(kdialog, "--title", "AII OS", "--yes-label", yes, "--no-label", no, "--yesno", question)
	} else {
		return false, false
	}
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return true, true
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return false, true
	}
	return false, false
}
