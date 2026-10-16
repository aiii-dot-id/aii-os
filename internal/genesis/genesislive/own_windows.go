//go:build windows

package genesislive

import (
	"os"
	"os/user"
)

func accountTag() string {
	if u, err := user.Current(); err == nil && u.Uid != "" {
		return u.Uid
	}
	return "user"
}

func privateDir(d string) bool {
	if err := os.MkdirAll(d, 0o700); err != nil {
		return false
	}
	st, err := os.Lstat(d)
	return err == nil && st.IsDir()
}

func openOwn(path string) (*os.File, os.FileInfo, bool) {
	if lst, err := os.Lstat(path); err != nil || !lst.Mode().IsRegular() {
		return nil, nil, false
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, false
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		_ = f.Close()
		return nil, nil, false
	}
	return f, st, true
}
