//go:build darwin && !ios

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func openURL(url string) error { return exec.Command("open", url).Run() }

func startService(string) bool { return false }

func alert(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	script := fmt.Sprintf(`display alert "AII OS could not start" message %q as critical giving up after 120`, msg)
	_ = exec.Command("osascript", "-e", script).Run()
}

func ask(question, yes, no string) (answer, asked bool) {
	script := fmt.Sprintf(`display dialog %q with title "AII OS" buttons {%q, %q} default button %q cancel button %q with icon caution giving up after 300`,
		question, no, yes, yes, no)
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	if err != nil {
		return false, strings.Contains(string(out), "-128")
	}
	return strings.Contains(string(out), "button returned:"+yes), true
}
