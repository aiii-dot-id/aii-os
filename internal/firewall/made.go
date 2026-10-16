package firewall

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

type Dirs interface {
	Lstat(name string) (fs.FileInfo, error)
	Mkdir(name string, perm fs.FileMode) error
	Remove(name string) error
}

var Local Dirs = local{}

type local struct{}

func (local) Lstat(name string) (fs.FileInfo, error)    { return os.Lstat(name) }
func (local) Mkdir(name string, perm fs.FileMode) error { return os.Mkdir(name, perm) }
func (local) Remove(name string) error                  { return os.Remove(name) }

func MakeParents(d Dirs, path string, perm fs.FileMode) ([]string, error) {
	var missing []string
	for p := filepath.Dir(path); p != "." && filepath.Dir(p) != p; p = filepath.Dir(p) {
		if _, err := d.Lstat(p); err == nil {
			break
		}
		missing = append(missing, p)
	}
	var made []string
	for i := len(missing) - 1; i >= 0; i-- {
		p := missing[i]
		err := d.Mkdir(p, perm)
		if err == nil {
			made = append(made, p)
			continue
		}
		if errors.Is(err, fs.ErrExist) {
			if info, lerr := d.Lstat(p); lerr == nil && info.IsDir() {
				continue
			}
		}
		return nil, errors.Join(err, Unmake(d, made))
	}
	return made, nil
}

func Unmake(d Dirs, made []string) error {
	for i := len(made) - 1; i >= 0; i-- {
		err := d.Remove(made[i])
		if err == nil || errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%s is left: it holds what this call did not make", made[i])
		}
		return err
	}
	return nil
}

func MadeKept(kept func(path string) error, dst string, madeDst, madeDirs bool) error {
	if kept == nil {
		return nil
	}
	if !madeDst {
		if !madeDirs {
			return nil
		}
		return kept(filepath.Dir(dst))
	}
	if err := kept(dst); err != nil {
		return err
	}

	var refused error
	filepath.WalkDir(dst, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {

			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			refused = fmt.Errorf("%s, in what this call made, could not be read, so what lies under it could not be checked: %w", p, err)
			return filepath.SkipAll
		}
		if p == dst {
			return nil
		}
		if refused = kept(p); refused != nil {
			return filepath.SkipAll
		}
		return nil
	})
	return refused
}
