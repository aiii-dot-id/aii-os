package packagefmt

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// .
// .
// .
// .
// .
// .
// .

// .
// .
// .
// .
// .
func WriteTree(w io.Writer, dir, root string, limits TreeLimits) (inventory []byte, archiveSHA string, err error) {
	limits = limits.filled()
	if err := validateTreeRoot(root); err != nil {
		return nil, "", err
	}
	var files []treeSource
	var dirs []string
	var total int64
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		rel, rerr := filepath.Rel(dir, path)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink; a runtime tree carries no links", rel)
		}
		if d.IsDir() {
			dirs = append(dirs, rel)
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", rel)
		}
		if rel == InventoryFile {
			return fmt.Errorf("%s is the archive's own member; the tree must not carry one", InventoryFile)
		}
		if strings.Count(rel, "/")+1 > limits.MaxDepth {
			return fmt.Errorf("%s is deeper than %d segments", rel, limits.MaxDepth)
		}
		for _, seg := range strings.Split(rel, "/") {
			if componentForbiddenUnder(seg, true) {
				return fmt.Errorf("%s: segment %q is not admitted by the grammar", rel, seg)
			}
		}
		if len(root)+1+len(rel) > maxMemberPathBytes {
			return fmt.Errorf("%s: the member path exceeds %d bytes", rel, maxMemberPathBytes)
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		if info.Size() > limits.MaxFileBytes {
			return fmt.Errorf("%s is %d bytes; at most %d", rel, info.Size(), limits.MaxFileBytes)
		}
		sum, herr := hashPath(path)
		if herr != nil {
			return herr
		}
		mode := "file"
		if info.Mode().Perm()&0o111 != 0 {
			mode = "exec"
		}
		files = append(files, treeSource{rel: rel, abs: path, size: info.Size(), sha: sum, mode: mode})
		total += info.Size()
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	if len(files) == 0 {
		return nil, "", fmt.Errorf("%s holds no files", dir)
	}
	if len(files) > limits.MaxFiles {
		return nil, "", fmt.Errorf("%d files; at most %d", len(files), limits.MaxFiles)
	}
	if total > limits.MaxInstalledBytes {
		return nil, "", fmt.Errorf("%d installed bytes; at most %d", total, limits.MaxInstalledBytes)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	inv := Inventory{InstalledBytes: total}
	for _, f := range files {
		inv.Files = append(inv.Files, InventoryEntry{Path: f.rel, Size: f.size, SHA256: f.sha, Mode: f.mode})
	}
	inventory, err = json.Marshal(inv)
	if err != nil {
		return nil, "", err
	}
	if int64(len(inventory)) > limits.MaxInventoryBytes {
		return nil, "", fmt.Errorf("the inventory is %d bytes; at most %d", len(inventory), limits.MaxInventoryBytes)
	}
	// .
	// .
	type member struct {
		path  string
		isDir bool
		src   *treeSource
	}
	members := []member{{path: root, isDir: true}, {path: root + "/" + InventoryFile}}
	var rest []member
	seenDir := map[string]bool{}
	for _, f := range files {
		parts := strings.Split(f.rel, "/")
		for i := 1; i < len(parts); i++ {
			d := strings.Join(parts[:i], "/")
			if !seenDir[d] {
				seenDir[d] = true
				rest = append(rest, member{path: root + "/" + d, isDir: true})
			}
		}
		f := f
		rest = append(rest, member{path: root + "/" + f.rel, src: &f})
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i].path < rest[j].path })
	members = append(members, rest...)

	hw := sha256.New()
	zw, err := gzip.NewWriterLevel(io.MultiWriter(w, hw), gzip.BestCompression)
	if err != nil {
		return nil, "", err
	}
	// .
	// .
	zw.Header.OS = 255
	for _, m := range members {
		typ := byte(tarTypeRegular)
		mode := int64(tarModeRegular)
		var size int64
		if m.isDir {
			typ, mode = tarTypeDirectory, tarModeDir
		} else if m.src != nil {
			size = m.src.size
			if m.src.mode == "exec" {
				mode = tarModeExecutable
			}
		} else {
			size = int64(len(inventory))
		}
		if err := writeTarMemberHeader(zw, m.path, size, mode, typ); err != nil {
			return nil, "", err
		}
		if m.isDir {
			continue
		}
		if m.src == nil {
			if _, err := zw.Write(inventory); err != nil {
				return nil, "", err
			}
		} else {
			f, err := os.Open(m.src.abs)
			if err != nil {
				return nil, "", err
			}
			n, cerr := io.Copy(zw, f)
			f.Close()
			if cerr != nil {
				return nil, "", cerr
			}
			if n != m.src.size {
				return nil, "", fmt.Errorf("%s changed size while packing", m.src.rel)
			}
		}
		if pad := size % tarBlockBytes; pad != 0 {
			if _, err := zw.Write(make([]byte, tarBlockBytes-pad)); err != nil {
				return nil, "", err
			}
		}
	}
	if _, err := zw.Write(make([]byte, 2*tarBlockBytes)); err != nil {
		return nil, "", err
	}
	if err := zw.Close(); err != nil {
		return nil, "", err
	}
	return inventory, "sha256:" + hex.EncodeToString(hw.Sum(nil)), nil
}

type treeSource struct {
	rel, abs, sha, mode string
	size                int64
}

func validateTreeRoot(root string) error {
	if root == "" || strings.Contains(root, "/") || componentForbidden(root) {
		return fmt.Errorf("root %q must be one admitted path component", root)
	}
	return nil
}

func hashPath(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// .
// .
// .
func writeTarMemberHeader(w io.Writer, path string, size, mode int64, typ byte) error {
	stored := tarStoredPath(path, typ)
	if tarPathFitsUSTAR(path, typ) {
		hdr, err := buildTarHeader(stored, size, mode, typ)
		if err != nil {
			return fmt.Errorf("header %q: %v", path, err)
		}
		_, err = w.Write(hdr[:])
		return err
	}
	record, err := paxBuildPathRecord(path)
	if err != nil {
		return fmt.Errorf("pax record %q: %v", path, err)
	}
	paxHdr, err := buildTarHeader(paxHeaderPath, int64(len(record)), tarModeRegular, tarTypePAXLocal)
	if err != nil {
		return err
	}
	if _, err := w.Write(paxHdr[:]); err != nil {
		return err
	}
	if _, err := io.WriteString(w, record); err != nil {
		return err
	}
	if pad := len(record) % tarBlockBytes; pad != 0 {
		if _, err := w.Write(make([]byte, tarBlockBytes-pad)); err != nil {
			return err
		}
	}
	hdr, err := buildTarHeader(paxMemberPlaceholder, size, mode, typ)
	if err != nil {
		return err
	}
	_, err = w.Write(hdr[:])
	return err
}
