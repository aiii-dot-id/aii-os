//go:build windows

package machine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const Supported = true

func LinkPath(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", dir, err)
	}
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(abs))))
	return `\\.\pipe\aii-machine-` + hex.EncodeToString(sum[:8]), nil
}

const firstInstanceWait = 10 * time.Second

func openLink(path string) (net.Listener, bool, error) {
	sa, err := ownerOnlyPipeSecurity()
	if err != nil {
		return nil, false, err
	}
	deadline := time.Now().Add(firstInstanceWait)
	for {
		h, err := createPipe(path, sa, true)
		if err == nil {
			ev, err := windows.CreateEvent(nil, 1, 0, nil)
			if err != nil {
				windows.CloseHandle(h)
				return nil, false, err
			}
			return &pipeListener{name: path, sa: sa, next: h, closeEv: ev}, false, nil
		}
		if !errors.Is(err, windows.ERROR_ACCESS_DENIED) && !errors.Is(err, windows.ERROR_PIPE_BUSY) {
			return nil, false, fmt.Errorf("serve %s: %w", path, err)
		}
		if time.Now().After(deadline) {
			return nil, false, fmt.Errorf("serve %s: another process holds this pipe name and has for %s; the service does not share it: %w", path, firstInstanceWait, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func removeLink(string) error { return nil }

func ownerOnlyPipeSecurity() (*windows.SecurityAttributes, error) {
	tu, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("read this account: %w", err)
	}
	me := tu.User.Sid.String()
	sd, err := windows.SecurityDescriptorFromString("O:" + me + "D:P(A;;GA;;;" + me + ")(A;;GA;;;SY)")
	if err != nil {
		return nil, fmt.Errorf("build the link's security: %w", err)
	}
	sa := &windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(*sa))
	return sa, nil
}

func createPipe(name string, sa *windows.SecurityAttributes, first bool) (windows.Handle, error) {
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return windows.InvalidHandle, err
	}
	open := uint32(windows.PIPE_ACCESS_DUPLEX | windows.FILE_FLAG_OVERLAPPED)
	if first {
		open |= windows.FILE_FLAG_FIRST_PIPE_INSTANCE
	}
	mode := uint32(windows.PIPE_TYPE_BYTE | windows.PIPE_READMODE_BYTE | windows.PIPE_WAIT | windows.PIPE_REJECT_REMOTE_CLIENTS)
	return windows.CreateNamedPipe(n, open, mode, windows.PIPE_UNLIMITED_INSTANCES, 64<<10, 64<<10, 0, sa)
}

type pipeListener struct {
	name    string
	sa      *windows.SecurityAttributes
	closeEv windows.Handle

	mu        sync.Mutex
	next      windows.Handle
	accepting bool
	closed    bool
}

func (l *pipeListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil, net.ErrClosed
	}
	l.accepting = true
	h := l.next
	l.mu.Unlock()

	err := l.connect(h)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.accepting = false
	if err == nil && !l.closed {
		var next windows.Handle
		if next, err = createPipe(l.name, l.sa, false); err == nil {
			l.next = next
			return &pipeConn{File: os.NewFile(uintptr(h), l.name), name: l.name}, nil
		}
	}

	windows.CloseHandle(h)
	l.next = 0
	if l.closed {
		windows.CloseHandle(l.closeEv)
		return nil, net.ErrClosed
	}
	l.closed = true
	windows.CloseHandle(l.closeEv)
	return nil, fmt.Errorf("accept on %s: %w", l.name, err)
}

func (l *pipeListener) connect(h windows.Handle) error {
	ev, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(ev)
	ov := windows.Overlapped{HEvent: ev}
	err = windows.ConnectNamedPipe(h, &ov)

	for errors.Is(err, windows.ERROR_NO_DATA) {
		if derr := windows.DisconnectNamedPipe(h); derr != nil {
			return derr
		}
		ov = windows.Overlapped{HEvent: ev}
		err = windows.ConnectNamedPipe(h, &ov)
	}
	switch {
	case err == nil, errors.Is(err, windows.ERROR_PIPE_CONNECTED):
		return nil
	case !errors.Is(err, windows.ERROR_IO_PENDING):
		return err
	}
	which, err := windows.WaitForMultipleObjects([]windows.Handle{ev, l.closeEv}, false, windows.INFINITE)
	if err != nil {
		return err
	}
	if which == windows.WAIT_OBJECT_0+1 {

		_ = windows.CancelIoEx(h, &ov)
	}
	var n uint32
	if err := windows.GetOverlappedResult(h, &ov, &n, true); err != nil && !errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
		return err
	}
	if which == windows.WAIT_OBJECT_0+1 {
		return net.ErrClosed
	}
	return nil
}

func (l *pipeListener) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	windows.SetEvent(l.closeEv)
	if !l.accepting {
		windows.CloseHandle(l.next)
		l.next = 0
		windows.CloseHandle(l.closeEv)
	}
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr(l.name) }

type pipeConn struct {
	*os.File
	name string
}

func (c *pipeConn) LocalAddr() net.Addr  { return pipeAddr(c.name) }
func (c *pipeConn) RemoteAddr() net.Addr { return pipeAddr(c.name) }

type pipeAddr string

func (pipeAddr) Network() string  { return "pipe" }
func (a pipeAddr) String() string { return string(a) }

var ErrNotOurService = errors.New("the link is not served by this account's machine service")

func dialLink(ctx context.Context, path, image string) (net.Conn, error) {
	n, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(dialTimeout)
	for {
		h, err := windows.CreateFile(n, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING,
			windows.FILE_FLAG_OVERLAPPED|windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IDENTIFICATION, 0)
		if err == nil {
			if verr := verifyPipeServer(h, image); verr != nil {
				windows.CloseHandle(h)
				return nil, verr
			}
			return &pipeConn{File: os.NewFile(uintptr(h), path), name: path}, nil
		}
		if !errors.Is(err, windows.ERROR_PIPE_BUSY) || time.Now().After(deadline) || ctx.Err() != nil {
			return nil, &os.PathError{Op: "dial", Path: path, Err: err}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func verifyPipeServer(h windows.Handle, image string) error {
	var pid uint32
	if err := windows.GetNamedPipeServerProcessId(h, &pid); err != nil {
		return fmt.Errorf("%w: its server cannot be named: %v", ErrNotOurService, err)
	}
	p, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return fmt.Errorf("%w: its server (process %d) cannot be inspected: %v", ErrNotOurService, pid, err)
	}
	defer windows.CloseHandle(p)
	var tok windows.Token
	if err := windows.OpenProcessToken(p, windows.TOKEN_QUERY, &tok); err != nil {
		return fmt.Errorf("%w: its server's account cannot be read: %v", ErrNotOurService, err)
	}
	defer tok.Close()
	them, err := tok.GetTokenUser()
	if err != nil {
		return fmt.Errorf("%w: its server's account cannot be read: %v", ErrNotOurService, err)
	}
	me, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	if !them.User.Sid.Equals(me.User.Sid) {
		return fmt.Errorf("%w: it runs as %s", ErrNotOurService, them.User.Sid)
	}
	if image == "" {
		return nil
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(p, 0, &buf[0], &size); err != nil {
		return fmt.Errorf("%w: its server's program cannot be read: %v", ErrNotOurService, err)
	}
	got := filepath.Clean(windows.UTF16ToString(buf[:size]))
	want := filepath.Clean(image)
	if !strings.EqualFold(got, want) && !strings.EqualFold(got, want+".old") {
		return fmt.Errorf("%w: it is served by %s, not %s", ErrNotOurService, got, want)
	}
	return nil
}
