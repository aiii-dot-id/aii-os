package packagefmt

import (
	"bytes"
	"io"
	"os"
	"strings"
)

const maxMemberReadBytes = 64 << 20

func ReadMember(pkgPath, installRelPath string) ([]byte, error) {
	var raw []byte
	if err := ReadMembers(pkgPath, []string{installRelPath}, func(_ string, b []byte) error {
		raw = b
		return nil
	}); err != nil {
		return nil, err
	}
	return raw, nil
}

func ReadMembers(pkgPath string, installRelPaths []string, fn func(installRelPath string, raw []byte) error) error {
	if len(installRelPaths) == 0 {
		return nil
	}
	f, err := os.Open(pkgPath)
	if err != nil {
		return fail(ReasonEnvelopeMalformed, "open", "%v", err)
	}
	defer f.Close()
	return readMembers(f, installRelPaths, fn)
}

func readMembers(r io.Reader, installRelPaths []string, fn func(string, []byte) error) error {
	wanted := make(map[string]bool, len(installRelPaths))
	for _, rel := range installRelPaths {
		wanted[rel] = true
	}
	gz, verr := newGzipStream(r)
	if verr != nil {
		return verr
	}
	walker := newTarWalker(gz)

	root := ""
	for len(wanted) > 0 {
		member, done, verr := walker.next()
		if verr != nil {
			return verr
		}
		if done {
			absent := ""
			for _, rel := range installRelPaths {
				if wanted[rel] {
					absent = rel
					break
				}
			}
			return fail(ReasonEnvelopeMalformed, "read-member", "install-root member %q is not in the package", absent)
		}
		if root == "" {

			root = member.path
			continue
		}
		if member.isDir {
			continue
		}
		rel, inRoot := strings.CutPrefix(member.path, root+"/install-root/")
		if !inRoot || !wanted[rel] {

			if verr := walker.readPayload(member, nil); verr != nil {
				return verr
			}
			continue
		}
		if member.size > maxMemberReadBytes {
			return fail(ReasonCeilingExceeded, "read-member", "member %q is %d bytes, above the %d-byte materialization ceiling", member.path, member.size, int64(maxMemberReadBytes))
		}
		var buf bytes.Buffer
		buf.Grow(int(member.size))
		if verr := walker.readPayload(member, &buf); verr != nil {
			return verr
		}
		delete(wanted, rel)
		if err := fn(rel, buf.Bytes()); err != nil {
			return err
		}
	}
	return nil
}
