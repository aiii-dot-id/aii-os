package broker

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/tools"
)

const (
	DefaultMaxFilesBytes = 64 << 20

	MaxFileEntries = 4096

	MaxFilePathBytes = 512
	MaxFilePathDepth = 16

	MaxListEntries = 1024

	PrivateRoot = "private"
	SandboxRoot = "sandbox"

	capFSPrivate = PrivateFilesCapability
	capFSSandbox = "fs.sandbox"

	opFSList    = "fs.list"
	opFSRead    = "fs.read"
	opFSWrite   = "fs.write"
	opFSDelete  = "fs.delete"
	opFSPublish = "fs.publish"

	reasonFSPathInvalid    = "FS_PATH_INVALID"
	reasonFSSymlinkRefused = "FS_SYMLINK_REFUSED"
	reasonFSNotFound       = "FS_NOT_FOUND"
	reasonFSQuotaExceeded  = "FS_QUOTA_EXCEEDED"
	reasonFSRootRefused    = "FS_ROOT_REFUSED"
	reasonFSOutside        = "FS_OUTSIDE_SANDBOX"
	reasonFSProtected      = "FS_PROTECTED"
	reasonFSIOFailed       = "FS_IO_FAILED"

	reasonFSGenerationMismatch = "FS_GENERATION_MISMATCH"
	reasonFSDigestMismatch     = "FS_DIGEST_MISMATCH"
)

const PrivateFilesCapability = "fs.private"

type Sandbox interface {
	Admit(op, path string) (dir, rel string, err error)
}

func (b *Binding) SetFiles(privateDir string, capBytes int) {
	if b == nil {
		return
	}
	b.privateDir = privateDir
	if privateDir != "" && !b.tier.PublisherProven() {

		b.privateDir = filepath.Join(filepath.Dir(privateDir), tempFilesParent, b.activation)
	}
	b.filesCap = capBytes
}

func (b *Binding) filesCapBytes() int {
	if b.filesCap > 0 {
		return b.filesCap
	}
	if b.host.cfg.MaxFilesBytes > 0 {
		return b.host.cfg.MaxFilesBytes
	}
	return DefaultMaxFilesBytes
}

const tempFilesParent = ".activation-temp-v1"

func (b *Binding) clearTempFiles() error {
	if b.privateDir != "" && !b.tier.PublisherProven() {
		root, err := openTempFiles(filepath.Dir(filepath.Dir(b.privateDir)), false)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("broker: temporary files: %w", err)
		}
		defer root.Close()
		if err := root.RemoveAll(b.activation); err != nil {
			return fmt.Errorf("broker: removing activation files: %w", err)
		}
	}
	return nil
}

func openTempFiles(pluginDataDir string, create bool) (*os.Root, error) {
	if create {
		if err := os.MkdirAll(pluginDataDir, 0o700); err != nil {
			return nil, err
		}
	}
	base, err := os.OpenRoot(pluginDataDir)
	if err != nil {
		return nil, err
	}
	defer base.Close()
	if create {
		if err := base.Mkdir(tempFilesParent, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
	}
	info, err := base.Lstat(tempFilesParent)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("temporary parent %s must be a real directory", tempFilesParent)
	}
	return openCheckedDirectory(base, tempFilesParent, info)
}

func openCheckedDirectory(parent *os.Root, name string, checked os.FileInfo) (*os.Root, error) {
	if !checked.IsDir() || checked.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s must be a real directory", name)
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(checked, opened) {
		root.Close()
		return nil, errors.Join(err, fmt.Errorf("directory %s changed while opening", name))
	}
	return root, nil
}

func ClearOrphanTempFiles(pluginDataDir string) error {
	if pluginDataDir == "" {
		return nil
	}
	root, err := openTempFiles(pluginDataDir, false)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("broker: opening orphan temporary files: %w", err)
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	entries, readErr := dir.ReadDir(-1)
	closeErr := dir.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return err
	}
	var errs []error
	for _, entry := range entries {
		name := entry.Name()
		if len(name) < 26 || strings.Trim(name, "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567") != "" || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			errs = append(errs, fmt.Errorf("broker: refusing unrecognized temporary entry %q", name))
			continue
		}
		if err := root.RemoveAll(name); err != nil {
			errs = append(errs, fmt.Errorf("broker: removing orphan activation %s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

type fsTarget struct {
	Root string `json:"root"`
	Path string `json:"path"`
}

func cleanRel(p string) (string, error) {
	if len(p) > MaxFilePathBytes {
		return "", fmt.Errorf("path over %d bytes", MaxFilePathBytes)
	}
	if strings.ContainsAny(p, "\x00\\") {
		return "", fmt.Errorf("path carries a NUL or a backslash")
	}
	if p == "" || p == "." {
		return ".", nil
	}
	if strings.HasPrefix(p, "/") || filepath.IsAbs(p) || filepath.VolumeName(p) != "" {
		return "", fmt.Errorf("path must be relative to the root")
	}
	parts := strings.Split(p, "/")
	if len(parts) > MaxFilePathDepth {
		return "", fmt.Errorf("path deeper than %d", MaxFilePathDepth)
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("path carries an empty, . or .. component")
		}
	}
	return filepath.Clean(filepath.FromSlash(p)), nil
}

type resolvedRoot struct {
	name    string
	dir     string
	shown   string
	private bool
	opened  *os.Root
}

func (b *Binding) openPrivate() (resolvedRoot, *outcome) {
	if !b.envelopeHas(capFSPrivate) {
		return resolvedRoot{}, &outcome{status: statusDenied, reason: reasonNotInEnvelope, detail: capFSPrivate + " is not in the signed capability envelope"}
	}
	if b.privateDir == "" {
		return resolvedRoot{}, &outcome{status: statusDenied, reason: reasonFSRootRefused, detail: "this activation has no private directory"}
	}
	if !b.tier.PublisherProven() {
		root, err := openTempFiles(filepath.Dir(filepath.Dir(b.privateDir)), true)
		if err != nil {
			return resolvedRoot{}, &outcome{status: statusFailed, reason: reasonFSIOFailed, detail: "the temporary parent cannot be opened: " + err.Error()}
		}
		defer root.Close()
		if err := root.Mkdir(b.activation, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return resolvedRoot{}, &outcome{status: statusFailed, reason: reasonFSIOFailed, detail: "the temporary private directory cannot be created"}
		}
		info, err := root.Lstat(b.activation)
		if err != nil {
			return resolvedRoot{}, &outcome{status: statusFailed, reason: reasonFSIOFailed, detail: "the temporary private directory cannot be checked"}
		}
		opened, err := openCheckedDirectory(root, b.activation, info)
		if err != nil {
			return resolvedRoot{}, &outcome{status: statusFailed, reason: reasonFSIOFailed, detail: "the temporary private directory cannot be opened: " + err.Error()}
		}
		return resolvedRoot{name: PrivateRoot, dir: b.privateDir, private: true, opened: opened}, nil
	}
	if err := os.MkdirAll(b.privateDir, 0o700); err != nil {
		return resolvedRoot{}, &outcome{status: statusFailed, reason: reasonFSIOFailed, detail: "the private directory cannot be created"}
	}
	real, err := filepath.EvalSymlinks(b.privateDir)
	if err != nil {
		return resolvedRoot{}, &outcome{status: statusFailed, reason: reasonFSIOFailed, detail: "the private directory cannot be resolved"}
	}
	return resolvedRoot{name: PrivateRoot, dir: real, private: true}, nil
}

func (b *Binding) admitSandbox(pol policySnapshot, op, path string) (resolvedRoot, string, *outcome) {
	if !b.tier.PublisherProven() {
		return resolvedRoot{}, "", &outcome{status: statusDenied, reason: reasonTierDenied,
			detail: fmt.Sprintf("tier %s cannot work in the identity's sandbox (the trust contract backs it only from a publisher-proven tier)", b.tier)}
	}
	if !b.envelopeHas(capFSSandbox) {
		return resolvedRoot{}, "", &outcome{status: statusDenied, reason: reasonNotInEnvelope, detail: capFSSandbox + " is not in the signed capability envelope"}
	}
	if !pol.grant(b.pluginID).Files {
		return resolvedRoot{}, "", &outcome{status: statusDenied, reason: reasonPolicyDeny,
			detail: fmt.Sprintf("the operator has not granted this plugin files (plugins.grants.%s.files)", b.pluginID)}
	}
	if b.host.cfg.Sandbox == nil {
		return resolvedRoot{}, "", &outcome{status: statusDenied, reason: reasonFSOutside, detail: "this host has no sandbox to work in"}
	}
	dir, rel, err := b.host.cfg.Sandbox.Admit(b.pluginID+" "+op, path)
	if err != nil {
		reason := reasonFSProtected
		if errors.Is(err, tools.ErrOutsideSandbox) {
			reason = reasonFSOutside
		}
		return resolvedRoot{}, "", &outcome{status: statusDenied, reason: reason, detail: path + ": " + err.Error()}
	}
	if rel != "." && len(strings.Split(filepath.ToSlash(rel), "/")) > MaxFilePathDepth {
		return resolvedRoot{}, "", &outcome{status: statusDenied, reason: reasonFSPathInvalid, detail: fmt.Sprintf("%s: deeper than %d below the directory that holds it", path, MaxFilePathDepth)}
	}
	return resolvedRoot{name: SandboxRoot, dir: dir, shown: path}, rel, nil
}

func (b *Binding) envelopeHas(capability string) bool {
	for _, c := range b.envelope {
		if c == capability {
			return true
		}
	}
	return false
}

func noSymlinkOnPath(root *os.Root, rel string) error {
	if rel == "." {
		return nil
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for i := 1; i <= len(parts); i++ {
		prefix := filepath.FromSlash(strings.Join(parts[:i], "/"))
		fi, err := root.Lstat(prefix)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink", prefix)
		}
	}
	return nil
}

func treeSize(root *os.Root) (bytes int64, entries int, over bool) {
	_ = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == "." {
			return nil
		}
		entries++
		if entries > MaxFileEntries {
			over = true
			return fs.SkipAll
		}
		if info, ierr := d.Info(); ierr == nil && info.Mode().IsRegular() {
			bytes += info.Size()
		}
		return nil
	})
	return bytes, entries, over
}

func (b *Binding) dispatchFS(p invokeParams, pol policySnapshot) ([]byte, error) {
	var target fsTarget
	if len(p.Target) > 0 {
		if err := json.Unmarshal(p.Target, &target); err != nil {
			return b.resultReply(p.Operation, p.PluginOperation, "", outcome{status: statusDenied, reason: reasonTargetInvalid, detail: "target must be an object"})
		}
	}
	capability := capFSSandbox
	switch target.Root {
	case PrivateRoot:
		capability = capFSPrivate
	case SandboxRoot:
	default:
		return b.resultReply(p.Operation, p.PluginOperation, "", outcome{status: statusDenied, reason: reasonTargetInvalid,
			detail: fmt.Sprintf("%s requires target.root: %q (the plugin's own directory) or %q (the identity's sandbox, by the path its own tools would take)", p.Operation, PrivateRoot, SandboxRoot)})
	}
	label := target.Root + ":" + target.Path
	reply := func(o outcome) ([]byte, error) { return b.resultReply(p.Operation, p.PluginOperation, label, o) }

	if r, denied := b.scopeDenies(p.Operation, capability); denied {
		return r, nil
	}

	if b.host.cfg.InSAFE != nil && b.host.cfg.InSAFE() && (target.Root != PrivateRoot || (p.Operation != opFSRead && p.Operation != opFSList)) {
		return errorReply(-32000, fmt.Sprintf("this identity is in SAFE; %s on %s is refused while it holds", p.Operation, target.Root),
			&errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
	}
	var root resolvedRoot
	var rel string
	if target.Root == PrivateRoot {
		var denied *outcome
		if root, denied = b.openPrivate(); denied != nil {
			return reply(*denied)
		}
		if root.opened != nil {
			defer root.opened.Close()
		}
		var err error
		if rel, err = cleanRel(target.Path); err != nil {
			return reply(outcome{status: statusDenied, reason: reasonFSPathInvalid, detail: err.Error()})
		}
		root.shown = filepath.ToSlash(rel)
	} else {
		if err := boundedPath(target.Path); err != nil {
			return reply(outcome{status: statusDenied, reason: reasonFSPathInvalid, detail: err.Error()})
		}
		var denied *outcome
		if root, rel, denied = b.admitSandbox(pol, p.Operation, target.Path); denied != nil {
			return reply(*denied)
		}
	}
	if rel == "." && p.Operation != opFSList {
		return reply(outcome{status: statusDenied, reason: reasonFSPathInvalid, detail: p.Operation + " requires target.path"})
	}
	osRoot := root.opened
	if osRoot == nil {
		var err error
		osRoot, err = os.OpenRoot(root.dir)
		if err != nil {
			return reply(outcome{status: statusFailed, reason: reasonFSIOFailed, detail: "the root cannot be opened"})
		}
		defer osRoot.Close()
	}
	if err := noSymlinkOnPath(osRoot, rel); err != nil {
		return reply(outcome{status: statusDenied, reason: reasonFSSymlinkRefused, detail: "real paths only: " + err.Error()})
	}

	switch p.Operation {
	case opFSList:
		return b.fsList(reply, osRoot, root, rel)
	case opFSRead:
		return b.fsRead(reply, p, osRoot, root, rel)
	case opFSWrite:
		return b.fsWrite(reply, p, osRoot, root, rel)
	case opFSPublish:
		return b.fsPublish(reply, p, pol, osRoot, root, rel)
	default:
		return b.fsDelete(reply, osRoot, root, rel)
	}
}

func boundedPath(p string) error {
	if len(p) > MaxFilePathBytes {
		return fmt.Errorf("path over %d bytes", MaxFilePathBytes)
	}
	if strings.ContainsRune(p, 0) {
		return fmt.Errorf("path carries a NUL")
	}
	return nil
}

type replyFn func(outcome) ([]byte, error)

func (b *Binding) fsList(reply replyFn, osRoot *os.Root, root resolvedRoot, rel string) ([]byte, error) {
	f, err := osRoot.Open(rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return reply(outcome{status: statusFailed, reason: reasonFSNotFound, transportOK: true, detail: "no such directory"})
		}
		return reply(outcome{status: statusFailed, reason: reasonFSIOFailed, detail: "open: " + err.Error()})
	}
	defer f.Close()
	entries, err := f.ReadDir(-1)
	if err != nil {
		return reply(outcome{status: statusFailed, reason: reasonFSIOFailed, detail: "read directory: " + err.Error()})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	truncated := false
	if len(entries) > MaxListEntries {
		entries, truncated = entries[:MaxListEntries], true
	}
	list := make([]map[string]interface{}, 0, len(entries))
	for _, e := range entries {
		entry := map[string]interface{}{"name": e.Name(), "dir": e.IsDir()}
		if info, ierr := e.Info(); ierr == nil {
			entry["size"] = info.Size()
			entry["modified"] = info.ModTime().UTC().Format(time.RFC3339)
			if info.Mode()&os.ModeSymlink != 0 {
				entry["symlink"] = true
			}
		}
		list = append(list, entry)
	}
	or, _ := json.Marshal(map[string]interface{}{"root": root.name, "path": root.shown, "entries": list, "truncated": truncated})
	return reply(outcome{status: statusSucceeded, transportOK: true, operationResult: or})
}

func (b *Binding) fsRead(reply replyFn, p invokeParams, osRoot *os.Root, root resolvedRoot, rel string) ([]byte, error) {
	var offset int64
	length := b.host.cfg.maxResponseBytes()
	wantDigest := false
	if len(p.Arguments) > 0 {
		var args map[string]json.RawMessage
		if err := json.Unmarshal(p.Arguments, &args); err != nil {
			return reply(outcome{status: statusDenied, reason: reasonArgumentInvalid, detail: "arguments must be an object"})
		}
		for name, raw := range args {
			switch name {
			case "digest":
				if err := json.Unmarshal(raw, &wantDigest); err != nil {
					return reply(outcome{status: statusDenied, reason: reasonArgumentInvalid, detail: "digest must be true or false"})
				}
			case "offset":
				if err := json.Unmarshal(raw, &offset); err != nil || offset < 0 {
					return reply(outcome{status: statusDenied, reason: reasonArgumentInvalid, detail: "offset must be a non-negative integer"})
				}
			case "length":
				var n int
				if err := json.Unmarshal(raw, &n); err != nil || n <= 0 || n > b.host.cfg.maxResponseBytes() {
					return reply(outcome{status: statusDenied, reason: reasonArgumentInvalid, detail: fmt.Sprintf("length must be 1..%d", b.host.cfg.maxResponseBytes())})
				}
				length = n
			default:
				return reply(outcome{status: statusDenied, reason: reasonNetUnknownArgument, detail: fmt.Sprintf("argument %q is not supported by fs.read (offset, length, digest)", name)})
			}
		}
	}
	f, err := osRoot.Open(rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return reply(outcome{status: statusFailed, reason: reasonFSNotFound, transportOK: true, detail: "no such file"})
		}
		return reply(outcome{status: statusFailed, reason: reasonFSIOFailed, detail: "open: " + err.Error()})
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return reply(outcome{status: statusFailed, reason: reasonFSNotFound, transportOK: true, detail: "not a regular file"})
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return reply(outcome{status: statusFailed, reason: reasonFSIOFailed, detail: "seek: " + err.Error()})
	}

	result := map[string]interface{}{"root": root.name, "path": root.shown, "data_b64": "", "bytes": length, "offset": offset, "size": fi.Size(), "eof": false, "sha256": strings.Repeat("0", sha256.Size*2)}
	metadata, err := json.Marshal(result)
	if err != nil {
		return reply(outcome{status: statusFailed, reason: reasonFSIOFailed, detail: "encode: " + err.Error()})
	}
	available := maxOperationResultBytes - len(metadata)
	if available < 4 {
		return reply(outcome{status: statusFailed, reason: reasonNetResponseTooBig, detail: "file metadata exceeds the plugin frame budget"})
	}
	if maxData := available / 4 * 3; length > maxData {
		length = maxData
	}
	delete(result, "sha256")
	data, err := io.ReadAll(io.LimitReader(f, int64(length)))
	if err != nil {
		return reply(outcome{status: statusFailed, reason: reasonFSIOFailed, detail: "read: " + err.Error()})
	}
	result["data_b64"], result["bytes"], result["eof"] = base64.StdEncoding.EncodeToString(data), len(data), offset+int64(len(data)) >= fi.Size()

	if offset == 0 && offset+int64(len(data)) >= fi.Size() {
		result["sha256"] = hex.EncodeToString(sha256Of(data))
	} else if wantDigest {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return reply(outcome{status: statusFailed, reason: reasonFSIOFailed, detail: "seek: " + err.Error()})
		}
		sum, err := digestOf(f)
		if err != nil {
			return reply(outcome{status: statusFailed, reason: reasonFSIOFailed, detail: "digest: " + err.Error()})
		}
		result["sha256"] = sum
	}
	or, _ := json.Marshal(result)
	return reply(outcome{status: statusSucceeded, transportOK: true, operationResult: or})
}

func sha256Of(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}

func digestOf(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (b *Binding) fsPublish(reply replyFn, p invokeParams, pol policySnapshot, osRoot *os.Root, root resolvedRoot, rel string) ([]byte, error) {
	var data []byte
	haveData := false
	from, declared, expected := "", "", ""
	expectAbsent := false
	if len(p.Arguments) > 0 {
		var args map[string]json.RawMessage
		if err := json.Unmarshal(p.Arguments, &args); err != nil {
			return reply(outcome{status: statusDenied, reason: reasonArgumentInvalid, detail: "arguments must be an object"})
		}
		for name, raw := range args {
			switch name {
			case "data":
				var s string
				if err := json.Unmarshal(raw, &s); err != nil {
					return reply(outcome{status: statusDenied, reason: reasonArgumentInvalid, detail: "data must be a string"})
				}
				data, haveData = []byte(s), true
			case "data_b64":
				var s string
				if err := json.Unmarshal(raw, &s); err != nil {
					return reply(outcome{status: statusDenied, reason: reasonArgumentInvalid, detail: "data_b64 must be a string"})
				}
				dec, err := base64.StdEncoding.DecodeString(s)
				if err != nil {
					return reply(outcome{status: statusDenied, reason: reasonArgumentInvalid, detail: "data_b64 is not base64"})
				}
				data, haveData = dec, true
			case "from":
				if err := json.Unmarshal(raw, &from); err != nil || from == "" {
					return reply(outcome{status: statusDenied, reason: reasonArgumentInvalid, detail: "from must be the staged file's path in the same root"})
				}
			case "sha256":
				if err := json.Unmarshal(raw, &declared); err != nil || !isHexDigest(declared) {
					return reply(outcome{status: statusDenied, reason: reasonArgumentInvalid, detail: "sha256 must be 64 hex characters"})
				}
			case "expected_sha256":
				if err := json.Unmarshal(raw, &expected); err != nil || !isHexDigest(expected) {
					return reply(outcome{status: statusDenied, reason: reasonArgumentInvalid, detail: "expected_sha256 must be 64 hex characters (the digest a read carried)"})
				}
			case "expected_absent":
				if err := json.Unmarshal(raw, &expectAbsent); err != nil {
					return reply(outcome{status: statusDenied, reason: reasonArgumentInvalid, detail: "expected_absent must be true or false"})
				}
			default:
				return reply(outcome{status: statusDenied, reason: reasonNetUnknownArgument, detail: fmt.Sprintf("argument %q is not supported by fs.publish (data, data_b64, from, sha256, expected_sha256, expected_absent)", name)})
			}
		}
	}
	switch {
	case haveData && from != "":
		return reply(outcome{status: statusDenied, reason: reasonArgumentInvalid, detail: "fs.publish takes inline data or a staged file, not both"})
	case !haveData && from == "":
		return reply(outcome{status: statusDenied, reason: reasonArgumentInvalid, detail: "fs.publish requires data, data_b64 or from"})
	case from != "" && declared == "":
		return reply(outcome{status: statusDenied, reason: reasonArgumentInvalid, detail: "publishing a staged file requires sha256, the digest the whole content must measure to"})
	case expected != "" && expectAbsent:
		return reply(outcome{status: statusDenied, reason: reasonArgumentInvalid, detail: "expected_sha256 and expected_absent exclude each other"})
	}
	if haveData && len(data) > b.host.cfg.maxRequestBytes() {
		return reply(outcome{status: statusDenied, reason: reasonNetRequestTooBig, detail: fmt.Sprintf("%d bytes exceed the %d-byte request ceiling — stage the content in appended pieces with fs.write and publish it with from", len(data), b.host.cfg.maxRequestBytes())})
	}
	var fromRel string
	if from != "" {
		if root.private {
			var err error
			if fromRel, err = cleanRel(from); err != nil || fromRel == "." {
				return reply(outcome{status: statusDenied, reason: reasonFSPathInvalid, detail: "from: " + errString(err, "a path is required")})
			}
		} else {

			if err := boundedPath(from); err != nil {
				return reply(outcome{status: statusDenied, reason: reasonFSPathInvalid, detail: "from: " + err.Error()})
			}
			staged, srel, denied := b.admitSandbox(pol, p.Operation, from)
			if denied != nil {
				return reply(*denied)
			}
			if staged.dir != root.dir || srel == "." {
				return reply(outcome{status: statusDenied, reason: reasonFSPathInvalid, detail: "from must name a staged file in the same directory of the sandbox as the target"})
			}
			fromRel = srel
		}
		if fromRel == rel {
			return reply(outcome{status: statusDenied, reason: reasonFSPathInvalid, detail: "from names the target itself"})
		}
		if err := noSymlinkOnPath(osRoot, fromRel); err != nil {
			return reply(outcome{status: statusDenied, reason: reasonFSSymlinkRefused, detail: "real paths only: " + err.Error()})
		}
	}

	unlock := b.host.publishLock(b.pluginID, root.dir, rel)
	defer unlock()

	current, exists, err := currentDigest(osRoot, rel)
	if err != nil {
		return reply(outcome{status: statusFailed, reason: reasonFSIOFailed, detail: "the published file cannot be read: " + err.Error()})
	}
	switch {
	case expectAbsent && exists:
		return reply(outcome{status: statusFailed, reason: reasonFSGenerationMismatch, transportOK: true, detail: fmt.Sprintf("the file exists (sha256 %s); expected_absent asked for none — nothing replaced", current)})
	case expected != "" && !exists:
		return reply(outcome{status: statusFailed, reason: reasonFSGenerationMismatch, transportOK: true, detail: fmt.Sprintf("no file is published; expected sha256 %s — nothing replaced", expected)})
	case expected != "" && current != expected:
		return reply(outcome{status: statusFailed, reason: reasonFSGenerationMismatch, transportOK: true, detail: fmt.Sprintf("the published file is sha256 %s, not the expected %s — read it again before replacing it; nothing replaced", current, expected)})
	}

	if dir := filepath.Dir(rel); dir != "." {
		if err := osRoot.MkdirAll(dir, 0o700); err != nil {
			return reply(outcome{status: statusFailed, reason: reasonFSIOFailed, detail: "mkdir: " + err.Error()})
		}
	}

	var source string
	var digest string
	var size int64
	if haveData {
		capBytes := b.filesCapBytes()
		if root.private {
			used, entries, over := treeSize(osRoot)
			if over || entries >= MaxFileEntries {
				return reply(outcome{status: statusFailed, reason: reasonFSQuotaExceeded, transportOK: true, detail: fmt.Sprintf("the private directory holds %d entries; the ceiling is %d — nothing replaced", entries, MaxFileEntries)})
			}
			if used+int64(len(data)) > int64(capBytes) {
				return reply(outcome{status: statusFailed, reason: reasonFSQuotaExceeded, transportOK: true, detail: fmt.Sprintf("the private directory holds %d bytes; publishing %d more would pass the %d-byte ceiling — nothing replaced", used, len(data), capBytes)})
			}
		} else {
			b.filesMu.Lock()
			budgeted := b.sandboxWritten + len(data)
			b.filesMu.Unlock()
			if budgeted > capBytes {
				return reply(outcome{status: statusFailed, reason: reasonFSQuotaExceeded, transportOK: true, detail: fmt.Sprintf("this activation has written %d bytes into the identity's sandbox; %d more would pass the %d-byte ceiling — nothing replaced", budgeted-len(data), len(data), capBytes)})
			}
		}
		digest = hex.EncodeToString(sha256Of(data))
		if declared != "" && declared != digest {
			return reply(outcome{status: statusFailed, reason: reasonFSDigestMismatch, transportOK: true, detail: fmt.Sprintf("the inline content measures sha256 %s, not the declared %s — nothing replaced", digest, declared)})
		}
		tmp, err := writeTemp(osRoot, rel, data)
		if err != nil {
			return reply(outcome{status: statusFailed, reason: reasonFSIOFailed, transportOK: true, detail: "the new content could not be written and synced: " + err.Error() + " — the published file is untouched"})
		}
		source, size = tmp, int64(len(data))
		if !root.private {
			b.filesMu.Lock()
			b.sandboxWritten += len(data)
			b.filesMu.Unlock()
		}
	} else {
		f, err := osRoot.OpenFile(fromRel, os.O_RDWR, 0)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return reply(outcome{status: statusFailed, reason: reasonFSNotFound, transportOK: true, detail: "no staged file at from — nothing replaced"})
			}
			return reply(outcome{status: statusFailed, reason: reasonFSIOFailed, detail: "open staged: " + err.Error()})
		}
		fi, err := f.Stat()
		if err != nil || !fi.Mode().IsRegular() {
			f.Close()
			return reply(outcome{status: statusFailed, reason: reasonFSNotFound, transportOK: true, detail: "from is not a regular file — nothing replaced"})
		}
		sum, err := digestOf(f)
		if err != nil {
			f.Close()
			return reply(outcome{status: statusFailed, reason: reasonFSIOFailed, detail: "staged read: " + err.Error()})
		}
		if sum != declared {
			f.Close()
			return reply(outcome{status: statusFailed, reason: reasonFSDigestMismatch, transportOK: true, detail: fmt.Sprintf("the staged file measures sha256 %s, not the declared %s — the upload is incomplete or not what you meant; nothing replaced, the staged file is left for you", sum, declared)})
		}
		if err := f.Sync(); err != nil {
			f.Close()
			return reply(outcome{status: statusFailed, reason: reasonFSIOFailed, transportOK: true, detail: "the staged file could not be synced: " + err.Error() + " — nothing replaced"})
		}
		f.Close()
		source, digest, size = fromRel, sum, fi.Size()
	}

	replaced := exists
	if err := osRoot.Rename(source, rel); err != nil {
		if haveData {
			_ = osRoot.Remove(source)
		}
		return reply(outcome{status: statusFailed, reason: reasonFSIOFailed, transportOK: true, detail: "rename failed: " + err.Error() + " — the published file is untouched"})
	}

	_ = osRoot.Chmod(rel, 0o600)
	durability := syncDir(osRoot, filepath.Dir(rel))
	or, _ := json.Marshal(map[string]interface{}{"root": root.name, "path": root.shown, "size": size, "sha256": digest, "replaced": replaced, "durable": durability != "unknown", "durability": durability})
	return reply(outcome{status: statusSucceeded, transportOK: true, operationResult: or})
}

func currentDigest(osRoot *os.Root, rel string) (digest string, exists bool, err error) {
	f, err := osRoot.Open(rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", false, err
	}
	if !fi.Mode().IsRegular() {
		return "", false, fmt.Errorf("not a regular file")
	}
	sum, err := digestOf(f)
	if err != nil {
		return "", false, err
	}
	return sum, true, nil
}

func writeTemp(osRoot *os.Root, rel string, data []byte) (string, error) {
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	tmp := rel + ".publish-" + hex.EncodeToString(nonce[:])
	f, err := osRoot.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	if n, err := f.Write(data); err != nil || n != len(data) {
		f.Close()
		_ = osRoot.Remove(tmp)
		if err == nil {
			err = io.ErrShortWrite
		}
		return "", err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = osRoot.Remove(tmp)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = osRoot.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

func syncDir(osRoot *os.Root, dir string) string {
	if runtime.GOOS == "windows" {
		return "file-synced"
	}
	d, err := osRoot.Open(dir)
	if err != nil {
		return "unknown"
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return "unknown"
	}
	return "synced"
}

func isHexDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func errString(err error, otherwise string) string {
	if err != nil {
		return err.Error()
	}
	return otherwise
}

func (b *Binding) fsWrite(reply replyFn, p invokeParams, osRoot *os.Root, root resolvedRoot, rel string) ([]byte, error) {
	var data []byte
	haveData := false
	appendMode := false
	if len(p.Arguments) > 0 {
		var args map[string]json.RawMessage
		if err := json.Unmarshal(p.Arguments, &args); err != nil {
			return reply(outcome{status: statusDenied, reason: reasonArgumentInvalid, detail: "arguments must be an object"})
		}
		for name, raw := range args {
			switch name {
			case "data":
				var s string
				if err := json.Unmarshal(raw, &s); err != nil {
					return reply(outcome{status: statusDenied, reason: reasonArgumentInvalid, detail: "data must be a string"})
				}
				data, haveData = []byte(s), true
			case "data_b64":
				var s string
				if err := json.Unmarshal(raw, &s); err != nil {
					return reply(outcome{status: statusDenied, reason: reasonArgumentInvalid, detail: "data_b64 must be a string"})
				}
				dec, err := base64.StdEncoding.DecodeString(s)
				if err != nil {
					return reply(outcome{status: statusDenied, reason: reasonArgumentInvalid, detail: "data_b64 is not base64"})
				}
				data, haveData = dec, true
			case "append":
				if err := json.Unmarshal(raw, &appendMode); err != nil {
					return reply(outcome{status: statusDenied, reason: reasonArgumentInvalid, detail: "append must be true or false"})
				}
			default:
				return reply(outcome{status: statusDenied, reason: reasonNetUnknownArgument, detail: fmt.Sprintf("argument %q is not supported by fs.write (data, data_b64, append)", name)})
			}
		}
	}
	if !haveData {
		return reply(outcome{status: statusDenied, reason: reasonArgumentInvalid, detail: "fs.write requires data or data_b64"})
	}
	if len(data) > b.host.cfg.maxRequestBytes() {
		return reply(outcome{status: statusDenied, reason: reasonNetRequestTooBig, detail: fmt.Sprintf("%d bytes exceed the %d-byte write ceiling (append in pieces)", len(data), b.host.cfg.maxRequestBytes())})
	}

	capBytes := b.filesCapBytes()
	if root.private {
		used, entries, over := treeSize(osRoot)
		if over || entries >= MaxFileEntries {
			return reply(outcome{status: statusFailed, reason: reasonFSQuotaExceeded, transportOK: true, detail: fmt.Sprintf("the private directory holds %d entries; the ceiling is %d", entries, MaxFileEntries)})
		}
		if used+int64(len(data)) > int64(capBytes) {
			return reply(outcome{status: statusFailed, reason: reasonFSQuotaExceeded, transportOK: true, detail: fmt.Sprintf("the private directory holds %d bytes; writing %d would pass the %d-byte ceiling", used, len(data), capBytes)})
		}
	} else {
		b.filesMu.Lock()
		budgeted := b.sandboxWritten + len(data)
		b.filesMu.Unlock()
		if budgeted > capBytes {
			return reply(outcome{status: statusFailed, reason: reasonFSQuotaExceeded, transportOK: true, detail: fmt.Sprintf("this activation has written %d bytes into the identity's sandbox; %d more would pass the %d-byte ceiling", budgeted-len(data), len(data), capBytes)})
		}
	}
	if dir := filepath.Dir(rel); dir != "." {
		if err := osRoot.MkdirAll(dir, 0o700); err != nil {
			return reply(outcome{status: statusFailed, reason: reasonFSIOFailed, detail: "mkdir: " + err.Error()})
		}
	}
	flags := os.O_WRONLY | os.O_CREATE
	if appendMode {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	f, err := osRoot.OpenFile(rel, flags, 0o600)
	if err != nil {
		return reply(outcome{status: statusFailed, reason: reasonFSIOFailed, detail: "open for writing: " + err.Error()})
	}
	n, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		return reply(outcome{status: statusFailed, reason: reasonFSIOFailed, detail: "write failed"})
	}

	_ = osRoot.Chmod(rel, 0o600)
	if !root.private {
		b.filesMu.Lock()
		b.sandboxWritten += n
		b.filesMu.Unlock()
	}
	size := int64(n)
	if fi, err := osRoot.Stat(rel); err == nil {
		size = fi.Size()
	}
	or, _ := json.Marshal(map[string]interface{}{"root": root.name, "path": root.shown, "bytes": n, "size": size, "appended": appendMode})
	return reply(outcome{status: statusSucceeded, transportOK: true, operationResult: or})
}

func (b *Binding) fsDelete(reply replyFn, osRoot *os.Root, root resolvedRoot, rel string) ([]byte, error) {
	fi, err := osRoot.Lstat(rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			or, _ := json.Marshal(map[string]interface{}{"root": root.name, "path": root.shown, "deleted": false})
			return reply(outcome{status: statusSucceeded, transportOK: true, operationResult: or})
		}
		return reply(outcome{status: statusFailed, reason: reasonFSIOFailed, detail: "stat: " + err.Error()})
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return reply(outcome{status: statusDenied, reason: reasonFSSymlinkRefused, detail: "real paths only: the target is a symlink"})
	}
	if err := osRoot.Remove(rel); err != nil {
		return reply(outcome{status: statusFailed, reason: reasonFSIOFailed, detail: "remove: " + err.Error() + " (a directory must be empty)"})
	}
	or, _ := json.Marshal(map[string]interface{}{"root": root.name, "path": root.shown, "deleted": true})
	return reply(outcome{status: statusSucceeded, transportOK: true, operationResult: or})
}

func EnvelopeOnly(capability string) bool { return capability == capFSPrivate }
