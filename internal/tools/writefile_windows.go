//go:build windows

package tools

import "os"

// .
// .
// .
// .

// .
// .
// .
// .
func writeFileNoFollowSame(path string, data []byte, perm os.FileMode, measured os.FileInfo) (bool, error) {
	if measured == nil {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
		if err == nil {
			_, werr := f.Write(data)
			cerr := f.Close()
			if werr != nil {
				return true, werr
			}
			return true, cerr
		}
		if !os.IsExist(err) {
			return false, err
		}
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, perm)
	if err != nil {
		return false, err
	}
	st, serr := f.Stat()
	same := measured != nil && serr == nil && os.SameFile(measured, st)
	if terr := f.Truncate(0); terr != nil {
		f.Close()
		return false, terr
	}
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil {
		return same, werr
	}
	return same, cerr
}

// .
// .
// .
func rewriteSameFile(path string, data []byte, measured os.FileInfo) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	if !st.Mode().IsRegular() || !os.SameFile(measured, st) {
		f.Close()
		return errNotTheFileRead(path)
	}
	if err := f.Truncate(0); err != nil {
		f.Close()
		return err
	}
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	return cerr
}

// .
// .
func openWriteReceipt(path string) (*os.File, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errReceiptNotRegular
	}
	f, _, err := openRegular(path)
	return f, err
}
