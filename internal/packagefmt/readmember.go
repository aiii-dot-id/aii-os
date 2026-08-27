package packagefmt

// ReadMember — the bounded second pass that turns a verified bundle
// into loadable bytes. Verify streams install-root artifacts through
// the digest and never materializes them (§1 streaming-bounded); a
// loader that wants ONE member's bytes walks the archive again with the
// SAME canonical walker. No trust logic lives here: the caller compares
// sha256 of the returned bytes against the verified Result.FileDigests
// entry — that comparison, not this read, is what makes the bytes
// trustworthy (the verified-bytes-are-loaded-bytes invariant; a file
// swapped on disk between the two passes fails the caller's digest
// check). The walk re-enforces the canonical grammar only as far as it
// goes: it returns as soon as the member is materialized.

import (
	"bytes"
	"io"
	"os"
)

// maxMemberReadBytes caps a single materialized install-root member:
// 64 MiB, adopted from the C host's component-file ceiling
// (SEV_WASM_HOST_COMPONENT_FILE_LIMIT_BYTES, sev_wasm_host.h:68 — the
// same source pluginworker.MaxArtifactBytes mirrors; one C owner, two
// Go citations). The archive-wide ceilings (tar.go) still bound the
// walk itself.
const maxMemberReadBytes = 64 << 20

// ReadMember returns the bytes of exactly one install-root member of
// the .aiiospkg at path. installRelPath is normalized relative to
// install-root/ — the same key form as Result.FileDigests and a
// manifest variant's entrypoint. Grammar violations reject with the
// usual typed *Error; a member the package does not contain is a typed
// rejection too, never a nil result.
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
			// The walker guarantees the first member is the sole
			// top-level directory.
			root = member.path
			continue
		}
		if member.isDir {
			continue // directories carry no payload
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
		// Not the one: stream its payload past (the walker requires
		// consumption before the next header).
		if verr := walker.readPayload(member, nil); verr != nil {
			return nil, verr
		}
	}
}
