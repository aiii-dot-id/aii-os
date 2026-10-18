//go:build darwin && !ios

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func openURL(url string) error { return exec.Command("open", url).Run() }

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

func uninstallFromApp(aii string) error {
	bundle := filepath.Dir(filepath.Dir(filepath.Dir(aii)))
	if !strings.HasSuffix(bundle, ".app") {
		return fmt.Errorf("the uninstall runs from AII OS.app; this is %s — run: aii uninstall", aii)
	}
	return runUninstallFlow(uninstallUI{
		aii: func(args ...string) ([]byte, []byte, int, error) {
			var out, errOut bytes.Buffer
			c := exec.Command(aii, args...)
			c.Stdout, c.Stderr = &out, &errOut
			err := c.Run()
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				return out.Bytes(), errOut.Bytes(), exit.ExitCode(), nil
			}
			return out.Bytes(), errOut.Bytes(), 0, err
		},
		ask:    ask,
		tell:   tell,
		reveal: func(path string) error { return exec.Command("open", "-R", path).Run() },
	}, bundle)
}

const terminalName = "Terminal"

func tell(title, msg string) {
	fmt.Fprintln(os.Stderr, title+": "+msg)
	script := fmt.Sprintf(`display alert %q message %q giving up after 600`, title, msg)
	_ = exec.Command("osascript", "-e", script).Run()
}
