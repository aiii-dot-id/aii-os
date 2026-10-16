package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

type WriteTool struct {
	deny func(ctx context.Context, path string) bool

	kept func(ctx context.Context, path string) error

	held func(ctx context.Context, path string, file os.FileInfo, by *os.File) error

	rename func(oldpath, newpath string) error

	measure func(ctx context.Context, path string) (os.FileInfo, int64, int, error)

	window func(at, path string)
}

func (t *WriteTool) Name() string { return "write" }
func (t *WriteTool) Description() string {
	return "Write, delete, move or copy a file; missing parent directories are made. Copying a directory, or deleting a full one, needs recursive."
}

func (t *WriteTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{"type": "string"},
			"content":   map[string]any{"type": "string"},
			"action":    map[string]any{"type": "string", "enum": []string{"write", "delete", "move", "copy"}},
			"to":        map[string]any{"type": "string", "description": "Full new path (move, copy)"},
			"recursive": map[string]any{"type": "boolean"},
		},
		"required": []string{"file_path"},
	}
}

func (t *WriteTool) Execute(ctx context.Context, args map[string]any) (Result, error) {
	path, _ := args["file_path"].(string)
	if path == "" {
		return Refusal(ReasonArgumentsRequired, "file_path is required"), nil
	}
	action, err := stringArg(args, "action")
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	recursive, err := boolArg(args, "recursive")
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	to, err := stringArg(args, "to")
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	switch action {
	case "", "write", "delete":
		if to != "" {
			return Refusal(ReasonArgumentsRequired, "to is the destination of a move or a copy; pass action=move or action=copy with it"), nil
		}
	case "move", "copy":
		if to == "" {
			return Refusal(ReasonArgumentsRequired, "malformed arguments: missing required to for "+action), nil
		}
	default:
		return Refusal(ReasonArgumentsRequired, fmt.Sprintf("action must be write, delete, move or copy, got %q", action)), nil
	}

	if err := ctx.Err(); err != nil {
		return Result{Error: err.Error()}, nil
	}
	switch action {
	case "delete":
		return t.delete(ctx, path, recursive)
	case "move":
		return t.move(ctx, path, to)
	case "copy":
		return t.copy(ctx, path, to, recursive)
	}

	content, ok := args["content"].(string)
	if !ok {
		return Refusal(ReasonArgumentsRequired, "malformed arguments: missing required content for write"), nil
	}

	if err := ctx.Err(); err != nil {
		return Result{Error: err.Error()}, nil
	}
	measure := t.measure
	if measure == nil {
		measure = previousFile
	}
	measured, prevBytes, prevLines, prevErr := measure(ctx, path)
	if errors.Is(prevErr, errReceiptNotRegular) {
		return Result{Error: prevErr.Error()}, nil
	}
	if err := ctx.Err(); err != nil {
		return Result{Error: err.Error()}, nil
	}

	parents, err := makeParents(path)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	if t.window != nil {
		t.window("parents", path)
	}
	same, made, err := writeFileNoFollowSame(path, []byte(content), 0644, measured, t.heldIn(ctx))
	if heldKept(err) {
		var undo error
		if stillMade(path, made) {
			undo = os.Remove(path)
		}
		return refuseMade("", err, path, errors.Join(undo, unmake(parents))), nil
	}

	if kept := t.madeKept(ctx, path, made != nil, parents); kept != nil {
		var undo error
		if stillMade(path, made) {
			undo = os.Remove(path)
		}
		return refuseMade("", kept, path, errors.Join(undo, unmake(parents))), nil
	}
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	if !same && (prevErr == nil || errors.Is(prevErr, fs.ErrNotExist)) {
		prevErr = errReceiptMoved
	}

	return Result{Output: fmt.Sprintf("Wrote %d bytes to %s %s", len(content), path,
		replacedNote(prevBytes, prevLines, prevErr, content)) + created(parents)}, nil
}

const writeReceiptMaxBytes int64 = 1 << 20

var errReceiptNotRegular = errors.New("write receipt requires a regular file")

var errReceiptMoved = errors.New("the file at this path changed between measuring it and writing; counts unavailable")

func previousFile(ctx context.Context, path string) (os.FileInfo, int64, int, error) {
	f, err := openWriteReceipt(path)
	if err != nil {
		return nil, 0, 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, 0, 0, err
	}
	if !st.Mode().IsRegular() {
		return nil, 0, 0, errReceiptNotRegular
	}
	if st.Size() > writeReceiptMaxBytes {
		return st, 0, 0, fmt.Errorf("receipt scan exceeds %d bytes; counts unavailable", writeReceiptMaxBytes)
	}
	reader := io.LimitReader(f, writeReceiptMaxBytes+1)
	buf := make([]byte, 32*1024)
	var size int64
	var lines int
	var tail byte
	for {
		if err := ctx.Err(); err != nil {
			return st, 0, 0, err
		}
		n, rerr := reader.Read(buf)
		size += int64(n)
		if size > writeReceiptMaxBytes {
			return st, 0, 0, fmt.Errorf("receipt scan exceeds %d bytes; counts unavailable", writeReceiptMaxBytes)
		}
		if n > 0 {
			lines += bytes.Count(buf[:n], []byte{'\n'})
			tail = buf[n-1]
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				break
			}
			return st, 0, 0, rerr
		}
	}
	if size > 0 && tail != '\n' {
		lines++
	}
	return st, size, lines, nil
}

func replacedNote(prevBytes int64, prevLines int, prevErr error, content string) string {
	switch {
	case errors.Is(prevErr, fs.ErrNotExist):
		return "(new file)"
	case prevErr != nil:
		return fmt.Sprintf("(replaced a file that could not be read: %v)", prevErr)
	}
	delta, lines := lineCount(content)-prevLines, "±0 lines"
	switch {
	case delta > 0:
		lines = fmt.Sprintf("+%d lines", delta)
	case delta < 0:
		lines = fmt.Sprintf("−%d lines", -delta)
	}
	return fmt.Sprintf("(replaced %d bytes; %s)", prevBytes, lines)
}

func lineCount(content string) int {
	if content == "" {
		return 0
	}
	n := strings.Count(content, "\n")
	if !strings.HasSuffix(content, "\n") {
		n++
	}
	return n
}

func makeParents(path string) ([]string, error) {
	return firewall.MakeParents(firewall.Local, path, 0o755)
}

func created(parents []string) string {
	if len(parents) == 0 {
		return ""
	}
	return " (created " + parents[0] + ")"
}

func unmake(parents []string) error {
	return firewall.Unmake(firewall.Local, parents)
}

func (t *WriteTool) madeKept(ctx context.Context, dst string, madeDst bool, parents []string) error {
	if t.kept == nil {
		return nil
	}
	return firewall.MadeKept(func(p string) error { return t.kept(ctx, p) }, dst, madeDst, len(parents) > 0)
}

func refuseMade(to string, kept error, dst string, undo error) Result {
	if undo != nil {
		return Result{Error: fmt.Sprintf("access denied: %s%v; taking back what this call made at %s failed, and some of it may be left: %v", to, kept, dst, undo)}
	}
	return Refusal(ReasonPathRefused, "access denied: "+to+kept.Error())
}

func (t *WriteTool) heldIn(ctx context.Context) func(string, os.FileInfo, *os.File) error {
	if t.held == nil {
		return nil
	}
	return func(path string, file os.FileInfo, by *os.File) error { return t.held(ctx, path, file, by) }
}

func (t *WriteTool) askAt(ctx context.Context, path string) error {
	held := t.heldIn(ctx)
	if held == nil {
		return nil
	}
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	return held(path, st, nil)
}

func stopped(nothing string, err, undo error) Result {
	if undo != nil {
		return Result{Error: fmt.Sprintf("%s: %v; taking back the directories this call made failed, and some may be left: %v", nothing, err, undo)}
	}
	return Result{Error: nothing + ": " + err.Error()}
}

func refuseOr(err error) Result {
	if heldKept(err) {
		return Refusal(ReasonPathRefused, "access denied: "+err.Error())
	}
	return Result{Error: err.Error()}
}

func refuseMadeOr(to string, err error, dst string, undo error) Result {
	if heldKept(err) {
		return refuseMade(to, err, dst, undo)
	}
	if undo != nil {
		return Result{Error: fmt.Sprintf("%v; taking back what this call made at %s failed, and some of it may be left: %v", err, dst, undo)}
	}
	return Result{Error: err.Error()}
}

type sourceKept struct{ error }

func (s *sourceKept) Unwrap() error { return s.error }

func side(err error) string {
	var source *sourceKept
	if errors.As(err, &source) {
		return ""
	}
	return "to "
}

func heldKept(err error) bool {
	var kept *SubstrateError
	return errors.As(err, &kept)
}

func stillMade(path string, made fs.FileInfo) bool {
	if made == nil {
		return false
	}
	var now fs.FileInfo
	var err error
	if made.IsDir() {
		now, err = handleStat(path)
	} else {
		now, err = os.Lstat(path)
	}
	return err == nil && os.SameFile(made, now)
}

type tree struct {
	files, links, dirs int
	other              int
	denied             int
	unreadable         int
}

func (t *WriteTool) survey(ctx context.Context, dir, dst string) (tree, error) {
	var c tree
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			c.unreadable++
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		if t.deny != nil && (t.deny(ctx, p) || dst != "" && t.deny(ctx, filepath.Join(dst, walkRel(dir, p)))) {
			c.denied++
		}
		switch {
		case d.IsDir():
			c.dirs++
		case d.Type()&fs.ModeSymlink != 0:
			c.links++
		case d.Type().IsRegular():
			c.files++
		default:
			c.other++
		}
		return nil
	})
	return c, err
}

func (c tree) refusal(dir, done string) string {
	switch {
	case c.denied > 0:
		return fmt.Sprintf("%d path(s) inside %s are protected by the substrate floor; nothing was %s", c.denied, dir, done)
	case c.unreadable > 0:
		return fmt.Sprintf("%d path(s) inside %s could not be read; nothing was %s", c.unreadable, dir, done)
	}
	return ""
}

func (c tree) empty() bool { return c.files+c.links+c.dirs+c.other == 0 }

func (c tree) String() string {
	var parts []string
	if n := c.files + c.other; n > 0 {
		parts = append(parts, counted(n, "file", "files"))
	}
	if c.links > 0 {
		parts = append(parts, counted(c.links, "link", "links"))
	}
	if c.dirs > 0 {
		parts = append(parts, counted(c.dirs, "directory", "directories"))
	}
	switch len(parts) {
	case 0:
		return "nothing"
	case 1:
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

func (t *WriteTool) delete(ctx context.Context, path string, recursive bool) (Result, error) {
	if t.window != nil {
		t.window("gate", path)
	}
	st, err := os.Lstat(path)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	if !st.IsDir() {
		if err := ctx.Err(); err != nil {
			return Result{Error: err.Error()}, nil
		}
		if err := t.askAt(ctx, path); err != nil {
			return refuseOr(err), nil
		}
		if err := os.Remove(path); err != nil {
			return Result{Error: err.Error()}, nil
		}
		what := "file"
		if st.Mode()&fs.ModeSymlink != 0 {
			what = "link"
		}
		return Result{Output: "deleted the " + what + " " + path}, nil
	}
	c, err := t.survey(ctx, path, "")
	if err != nil {
		return Result{Error: "nothing was deleted: " + err.Error()}, nil
	}
	if why := c.refusal(path, "deleted"); why != "" {
		return Result{Error: why}, nil
	}
	if err := ctx.Err(); err != nil {
		return Result{Error: "nothing was deleted: " + err.Error()}, nil
	}
	if c.empty() {
		if err := t.askAt(ctx, path); err != nil {
			return refuseOr(err), nil
		}
		if err := os.Remove(path); err != nil {
			return Result{Error: err.Error()}, nil
		}
		return Result{Output: "deleted the empty directory " + path}, nil
	}
	if !recursive {
		return Result{Error: fmt.Sprintf("%s is a directory holding %s; pass recursive=true to delete it and everything in it", path, c)}, nil
	}
	if err := t.askAt(ctx, path); err != nil {
		return refuseOr(err), nil
	}
	if err := os.RemoveAll(path); err != nil {
		return Result{Error: fmt.Sprintf("deleting %s failed part-way, and some of it may be gone: %v", path, err)}, nil
	}
	return Result{Output: fmt.Sprintf("deleted %s and the %s under it", path, c)}, nil
}

func (t *WriteTool) move(ctx context.Context, from, to string) (Result, error) {
	if t.window != nil {
		t.window("gate", from)
	}
	st, err := os.Lstat(from)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	if filepath.Clean(from) == filepath.Clean(to) {
		return Result{Error: "file_path and to are the same path; nothing was moved"}, nil
	}
	var c tree
	dst, dstErr := os.Lstat(to)
	if st.IsDir() {
		if within(resolveForContainment(from), resolveForContainment(to)) {
			return Result{Error: fmt.Sprintf("%s is inside %s; a directory cannot move into itself", to, from)}, nil
		}
		if dstErr == nil {
			return Result{Error: fmt.Sprintf("%s already exists; a directory moves to a new path", to)}, nil
		}
		if c, err = t.survey(ctx, from, to); err != nil {
			return Result{Error: "nothing was moved: " + err.Error()}, nil
		}
		if why := c.refusal(from, "moved"); why != "" {
			return Result{Error: why}, nil
		}
	} else if dstErr == nil && dst.IsDir() {
		return Result{Error: fmt.Sprintf("%s is a directory; to names the new path itself, for example %s", to, filepath.Join(to, filepath.Base(from)))}, nil
	}
	parents, err := makeParents(to)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	if t.window != nil {
		t.window("parents", to)
	}
	rename := t.rename
	if rename == nil {
		rename = os.Rename
	}
	if err := ctx.Err(); err != nil {
		return stopped("nothing was moved", err, unmake(parents)), nil
	}
	if err := t.askAt(ctx, from); err != nil {
		return refuseMadeOr("", err, to, unmake(parents)), nil
	}
	across := ""
	if err := rename(from, to); err != nil {
		if !errors.Is(err, errCrossDevice) {
			if kept := t.madeKept(ctx, to, false, parents); kept != nil {
				return refuseMade("to ", kept, to, unmake(parents)), nil
			}
			return Result{Error: err.Error()}, nil
		}

		made, cerr := carry(ctx, from, to, st, c, dstErr != nil, t.heldIn(ctx))
		if kept := t.madeKept(ctx, to, made != nil, parents); kept != nil {
			var undo error
			if stillMade(to, made) {
				undo = os.RemoveAll(to)
			}
			return refuseMade("to ", kept, to, errors.Join(undo, unmake(parents))), nil
		}
		if heldKept(cerr) {
			var undo error
			if stillMade(to, made) {
				undo = os.RemoveAll(to)
			}
			return refuseMade(side(cerr), cerr, to, errors.Join(undo, unmake(parents))), nil
		}
		if cerr != nil {
			return Result{Error: fmt.Sprintf("%s and %s are on different filesystems, and copying failed: %v; %s is unchanged", from, to, cerr, from)}, nil
		}

		if err := t.askAt(ctx, from); err != nil {
			var undo error
			if stillMade(to, made) {
				undo = os.RemoveAll(to)
			}
			return refuseMadeOr("", err, to, errors.Join(undo, unmake(parents))), nil
		}
		if err := os.RemoveAll(from); err != nil {
			return Result{Error: fmt.Sprintf("copied %s to %s across filesystems, but could not remove %s: %v", from, to, from, err)}, nil
		}
		across = " (across filesystems: copied, then removed)"
	} else if dstErr != nil {

		if kept := t.madeKept(ctx, to, true, parents); kept != nil {
			undo := fmt.Errorf("%s is no longer what this call moved there, and nothing was moved back", to)
			if stillMade(to, st) {
				undo = rename(to, from)
			}
			return refuseMade("to ", kept, to, errors.Join(undo, unmake(parents))), nil
		}
	}
	made := created(parents)
	switch {
	case st.IsDir() && c.empty():
		return Result{Output: fmt.Sprintf("moved the empty directory %s to %s", from, to) + across + made}, nil
	case st.IsDir():
		return Result{Output: fmt.Sprintf("moved %s to %s with the %s under it", from, to, c) + across + made}, nil
	case dstErr == nil:
		return Result{Output: fmt.Sprintf("moved %s to %s, replacing the file there", from, to) + across + made}, nil
	}
	return Result{Output: fmt.Sprintf("moved %s to %s", from, to) + across + made}, nil
}

func carry(ctx context.Context, from, to string, st fs.FileInfo, c tree, fresh bool, held func(string, os.FileInfo, *os.File) error) (fs.FileInfo, error) {
	switch {
	case st.IsDir():
		if c.other > 0 {
			return nil, fmt.Errorf("%s holds %d FIFO(s), socket(s) or device(s), which no copy carries", from, c.other)
		}
		return copyTree(ctx, from, to, held)
	case st.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(from)
		if err != nil {
			return nil, err
		}
		if err := os.Symlink(target, to); err != nil {
			return nil, err
		}
		return os.Lstat(to)
	case st.Mode().IsRegular():
		_, made, err := copyFile(ctx, from, to, st.Mode().Perm(), fresh, held)
		return made, err
	}
	return nil, fmt.Errorf("%s is not a file, a directory or a link", from)
}

func (t *WriteTool) copy(ctx context.Context, from, to string, recursive bool) (Result, error) {
	if t.window != nil {
		t.window("gate", from)
	}
	st, err := os.Stat(from)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	if filepath.Clean(from) == filepath.Clean(to) {
		return Result{Error: "file_path and to are the same path; nothing was copied"}, nil
	}
	dst, dstErr := os.Lstat(to)
	if st.IsDir() {
		root := resolveForContainment(from)
		switch {
		case !recursive:
			return Result{Error: fmt.Sprintf("%s is a directory; pass recursive=true to copy it and everything in it", from)}, nil
		case within(root, resolveForContainment(to)):
			return Result{Error: fmt.Sprintf("%s is inside %s; a directory cannot be copied into itself", to, from)}, nil
		case dstErr == nil:
			return Result{Error: fmt.Sprintf("%s already exists; a directory is copied to a new path", to)}, nil
		}
		c, err := t.survey(ctx, root, to)
		if err != nil {
			return Result{Error: "nothing was copied: " + err.Error()}, nil
		}
		if why := c.refusal(from, "copied"); why != "" {
			return Result{Error: why}, nil
		}
		if c.other > 0 {
			return Result{Error: fmt.Sprintf("%s holds %d FIFO(s), socket(s) or device(s), which no copy carries; nothing was copied", from, c.other)}, nil
		}
		parents, err := makeParents(to)
		if err != nil {
			return Result{Error: err.Error()}, nil
		}
		made, cerr := copyTree(ctx, root, to, t.heldIn(ctx))
		if kept := t.madeKept(ctx, to, made != nil, parents); kept != nil {
			var undo error
			if stillMade(to, made) {
				undo = os.RemoveAll(to)
			}
			return refuseMade("to ", kept, to, errors.Join(undo, unmake(parents))), nil
		}
		if heldKept(cerr) {
			var undo error
			if stillMade(to, made) {
				undo = os.RemoveAll(to)
			}
			return refuseMade(side(cerr), cerr, to, errors.Join(undo, unmake(parents))), nil
		}
		if cerr != nil {
			return Result{Error: fmt.Sprintf("copying %s failed: %v", from, cerr)}, nil
		}
		return Result{Output: fmt.Sprintf("copied %s to %s with the %s under it", from, to, c) + created(parents)}, nil
	}
	if !st.Mode().IsRegular() {
		return Result{Error: fmt.Sprintf("%s is not a regular file; nothing was copied", from)}, nil
	}
	if dstErr == nil && dst.IsDir() {
		return Result{Error: fmt.Sprintf("%s is a directory; to names the new path itself, for example %s", to, filepath.Join(to, filepath.Base(from)))}, nil
	}
	if dstErr == nil && os.SameFile(st, dst) {
		return Result{Error: "file_path and to are the same file; nothing was copied"}, nil
	}
	parents, err := makeParents(to)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	if t.window != nil {
		t.window("parents", to)
	}
	if err := ctx.Err(); err != nil {
		return stopped("nothing was copied", err, unmake(parents)), nil
	}
	n, made, err := copyFile(ctx, from, to, st.Mode().Perm(), dstErr != nil, t.heldIn(ctx))
	if heldKept(err) {
		var undo error
		if stillMade(to, made) {
			undo = os.Remove(to)
		}
		return refuseMade(side(err), err, to, errors.Join(undo, unmake(parents))), nil
	}
	if kept := t.madeKept(ctx, to, made != nil, parents); kept != nil {
		var undo error
		if stillMade(to, made) {
			undo = os.Remove(to)
		}
		return refuseMade("to ", kept, to, errors.Join(undo, unmake(parents))), nil
	}
	if err != nil {
		return Result{Error: err.Error()}, nil
	}
	was := "new file"
	if made == nil {
		was = "replaced a file"
	}
	return Result{Output: fmt.Sprintf("copied %s to %s (%d bytes; %s)", from, to, n, was) + created(parents)}, nil
}

func copyFile(ctx context.Context, from, to string, perm os.FileMode, fresh bool, held func(string, os.FileInfo, *os.File) error) (int64, fs.FileInfo, error) {
	in, src, err := openRegular(from)
	if err != nil {
		return 0, nil, err
	}
	defer in.Close()

	if held != nil {
		if kept := held(from, src, in); kept != nil {
			return 0, nil, &sourceKept{kept}
		}
	}
	if err := ctx.Err(); err != nil {
		return 0, nil, err
	}
	out, made, err := openForCopy(to, perm, fresh, held)
	if err != nil {
		return 0, made, err
	}
	effect := "was emptied for the copy"
	if made != nil {
		effect = "was created for the copy"
	}
	n, err := io.Copy(out, ctxReader{ctx, in})
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return n, made, fmt.Errorf("%w; %s %s and holds %d of the %d bytes of %s", err, to, effect, n, src.Size(), from)
	}
	return n, made, nil
}

func copyTree(ctx context.Context, from, to string, held func(string, os.FileInfo, *os.File) error) (made fs.FileInfo, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Lstat(from)
	if err != nil {
		return nil, err
	}
	if err := os.Mkdir(to, info.Mode().Perm()|0o700); err != nil {
		return nil, err
	}
	made, err = handleStat(to)
	if err != nil {
		return nil, fmt.Errorf("%w; %s was created and is left as it is", err, to)
	}
	defer func() {
		if err == nil {
			return
		}
		if now, serr := handleStat(to); serr != nil || !os.SameFile(made, now) {
			made = nil
			err = fmt.Errorf("%w; the partial copy at %s was not removed: it is no longer the directory this copy made", err, to)
		} else if rerr := os.RemoveAll(to); rerr != nil {
			err = fmt.Errorf("%w; removing the partial copy at %s failed: %v", err, to, rerr)
		} else {
			made = nil
			err = fmt.Errorf("%w; the partial copy was removed", err)
		}
	}()
	return made, filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(from, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		dst := filepath.Join(to, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.Mkdir(dst, info.Mode().Perm()|0o700)
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(target, dst)
		}
		_, _, err = copyFile(ctx, p, dst, info.Mode().Perm(), true, held)
		return err
	})
}

func handleStat(path string) (fs.FileInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Stat()
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
