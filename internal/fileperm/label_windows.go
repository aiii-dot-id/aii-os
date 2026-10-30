//go:build windows

package fileperm

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

type Label int

const (
	LabelNone Label = iota

	LabelKept

	LabelReadable

	LabelIdentity
)

var labelSDDL = map[Label]string{
	LabelKept:     "S:(ML;OICI;NWNR;;;ME)",
	LabelReadable: "S:(ML;OICI;NW;;;ME)",
	LabelIdentity: "S:(ML;OICI;NW;;;LW)",
}

func labelInheritedSDDL(l Label, dir bool) string {
	flags := "ID"
	if dir {
		flags = "OICIID"
	}
	switch l {
	case LabelKept:
		return "S:(ML;" + flags + ";NWNR;;;ME)"
	case LabelReadable:
		return "S:(ML;" + flags + ";NW;;;ME)"
	case LabelIdentity:
		return "S:(ML;" + flags + ";NW;;;LW)"
	}
	return ""
}

func openForLabel(path string, access uint32) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(longPath(path))
	if err != nil {
		return 0, err
	}
	return windows.CreateFile(p, access,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
}

func longPath(path string) string {
	if strings.HasPrefix(path, `\\?\`) || !filepath.IsAbs(path) {
		return path
	}
	if strings.HasPrefix(path, `\\`) {
		return `\\?\UNC\` + path[2:]
	}
	return `\\?\` + path
}

func LabelOf(path string) (string, error) {
	h, err := openForLabel(path, windows.READ_CONTROL)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	return labelOfHandle(h)
}

func labelOfHandle(h windows.Handle) (string, error) {
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.LABEL_SECURITY_INFORMATION)
	if err != nil {
		return "", err
	}
	s := sd.String()
	i := strings.Index(s, "S:")
	if i < 0 {
		return "", nil
	}

	s = s[i:]
	j := strings.IndexByte(s, '(')
	if j < 0 {
		return "", nil
	}
	return "S:" + s[j:], nil
}

func HasLabel(path string, l Label) (bool, error) {
	got, err := LabelOf(path)
	if err != nil {
		return false, err
	}
	return got == labelSDDL[l], nil
}

func SetLabel(path string, l Label) error {
	h, err := openForLabel(path, windows.READ_CONTROL|windows.WRITE_OWNER)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return setLabelHandle(h, labelSDDL[l])
}

func setLabelHandle(h windows.Handle, sddl string) error {
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	return windows.SetKernelObjectSecurity(h, windows.LABEL_SECURITY_INFORMATION, sd)
}

var programExt = map[string]bool{".exe": true, ".com": true, ".scr": true}

var ErrSeveralNames = errors.New("the file has more than one name")

func LabelTree(root string, l Label, skipped func(path string, why error)) error {
	if err := SetLabel(root, l); err != nil {
		return fmt.Errorf("label %s: %w", root, err)
	}
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if path == root {
			return err
		}
		if err != nil {
			if skipped != nil {
				skipped(path, err)
			}
			return nil
		}
		h, oerr := openForLabel(path, windows.READ_CONTROL|windows.WRITE_OWNER)
		if oerr != nil {
			if skipped != nil {
				skipped(path, oerr)
			}
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		defer windows.CloseHandle(h)
		var info windows.ByHandleFileInformation
		if gerr := windows.GetFileInformationByHandle(h, &info); gerr == nil {
			if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {

				_ = setLabelHandle(h, labelInheritedSDDL(l, false))
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if !d.IsDir() && l == LabelIdentity && programExt[strings.ToLower(filepath.Ext(path))] {
				return nil
			}
			if !d.IsDir() && info.NumberOfLinks > 1 {
				if skipped != nil {
					skipped(path, ErrSeveralNames)
				}
				return nil
			}
		}
		if cur, lerr := labelOfHandle(h); lerr == nil && cur != "" && !strings.Contains(cur, "ID;") {

			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if serr := setLabelHandle(h, labelInheritedSDDL(l, d.IsDir())); serr != nil && skipped != nil {
			skipped(path, serr)
		}
		return nil
	})
}

func UnlabelTree(root string, l Label) error {
	if ok, err := HasLabel(root, l); err != nil || !ok {
		return err
	}
	if err := setLabelPath(root, "S:"); err != nil {
		return fmt.Errorf("unlabel %s: %w", root, err)
	}
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if path == root || err != nil {
			return nil
		}
		h, oerr := openForLabel(path, windows.READ_CONTROL|windows.WRITE_OWNER)
		if oerr != nil {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		defer windows.CloseHandle(h)
		cur, lerr := labelOfHandle(h)
		if lerr != nil {
			return nil
		}
		if cur == labelInheritedSDDL(l, d.IsDir()) {
			_ = setLabelHandle(h, "S:")
		}
		if d.IsDir() && (d.Type()&fs.ModeSymlink != 0 || (cur != "" && !strings.Contains(cur, "ID;"))) {
			return fs.SkipDir
		}
		return nil
	})
}

func setLabelPath(path, sddl string) error {
	h, err := openForLabel(path, windows.READ_CONTROL|windows.WRITE_OWNER)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return setLabelHandle(h, sddl)
}

func Census(root string, l Label) ([]string, error) {
	var stray []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		h, err := openForLabel(path, windows.READ_CONTROL)
		if err != nil {
			return err
		}
		defer windows.CloseHandle(h)
		cur, err := labelOfHandle(h)
		if err != nil {
			return err
		}
		switch {
		case path == root:
		case cur == labelSDDL[l], cur == labelInheritedSDDL(l, d.IsDir()):
		case cur != "" && !strings.Contains(cur, "ID;"):
			if d.IsDir() {
				return fs.SkipDir
			}
		default:
			stray = append(stray, path)
		}
		if d.Type()&fs.ModeSymlink != 0 && d.IsDir() {
			return fs.SkipDir
		}
		return nil
	})
	return stray, err
}

func Hold(path string) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(longPath(path))
	if err != nil {
		return 0, err
	}
	return windows.CreateFile(p, windows.FILE_LIST_DIRECTORY,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
}

func IsDirNotLink(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.IsDir() && fi.Mode()&(os.ModeSymlink|os.ModeIrregular) == 0
}
