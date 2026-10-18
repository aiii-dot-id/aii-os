//go:build (linux && !android) || (darwin && !ios) || windows

package install

import (
	"fmt"
	"os"
	"os/user"
	"runtime"
)

var accountHome = func() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("read this account's home: %w", err)
	}
	return u.HomeDir, nil
}

type ForeignAccountError struct {
	Sudo    string
	Home    string
	Account string
}

func (e *ForeignAccountError) Error() string {
	if e.Sudo != "" {
		return fmt.Sprintf("this acts on the registrations of the account that runs it, and under sudo that is not %s's — run it as %s, without sudo", e.Sudo, e.Sudo)
	}
	return fmt.Sprintf("this acts on this account's own registrations, as its home holds them: %s is %s, and the account's home is %s — run it from the account's own session (an identity's shell has a home of its own)", homeVariable(), e.Home, e.Account)
}

func homeVariable() string {
	if runtime.GOOS == "windows" {
		return "USERPROFILE"
	}
	return "HOME"
}

func OwnAccount() error {
	if su := os.Getenv("SUDO_USER"); su != "" && su != "root" {
		cur, err := user.Current()
		if err != nil || cur.Username != su {
			return &ForeignAccountError{Sudo: su}
		}
	}
	home, err := operatorHome()
	if err != nil {
		return err
	}
	want, err := accountHome()
	if err != nil {
		return err
	}
	if !samePath(home, want) {
		return &ForeignAccountError{Home: home, Account: want}
	}
	return nil
}

func AccountHomeForTest(home string) (restore func()) {
	saved := accountHome
	accountHome = func() (string, error) { return home, nil }
	return func() { accountHome = saved }
}
