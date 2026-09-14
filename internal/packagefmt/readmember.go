package packagefmt

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

import (
	"bytes"
	"io"
	"os"
)

// .
// .
// .
// .
// .
// .
const maxMemberReadBytes = 64 << 20

// .
// .
// .
// .
// .
// .
func ReadMember(pkgPath, installRelPath string) ([]byte, error) {
	f, err := os.Open(pkgPath)
	if err != nil {
		return nil, fail(ReasonEnvelopeMalformed, "open", "%v", err)
	}
	defer f.Close()
	raw, verr := readMember(f, installRelPath)
	if verr != nil {
		return nil, verr
	}
	return raw, nil
}

func readMember(r io.Reader, installRelPath string) ([]byte, *Error) {
	gz, verr := newGzipStream(r)
	if verr != nil {
		return nil, verr
	}
	walker := newTarWalker(gz)

	root := ""
	for {
		member, done, verr := walker.next()
		if verr != nil {
			return nil, verr
		}
		if done {
			return nil, fail(ReasonEnvelopeMalformed, "read-member", "install-root member %q is not in the package", installRelPath)
		}
		if root == "" {
			// .
			// .
			root = member.path
			continue
		}
		if member.isDir {
			continue
		}
		if member.path == root+"/install-root/"+installRelPath {
			if member.size > maxMemberReadBytes {
				return nil, fail(ReasonCeilingExceeded, "read-member", "member %q is %d bytes, above the %d-byte materialization ceiling", member.path, member.size, int64(maxMemberReadBytes))
			}
			var buf bytes.Buffer
			buf.Grow(int(member.size))
			if verr := walker.readPayload(member, &buf); verr != nil {
				return nil, verr
			}
			return buf.Bytes(), nil
		}
		// .
		// .
		if verr := walker.readPayload(member, nil); verr != nil {
			return nil, verr
		}
	}
}
