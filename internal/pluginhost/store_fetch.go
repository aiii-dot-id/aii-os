//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

func fetchVerified(ctx context.Context, pluginID, what, url, partial string, size int64, sha string, fetch ModelFetcher, logf func(string, ...any)) error {
	reasked := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var offset int64

		if fi, err := os.Lstat(partial); err == nil {
			if !fi.Mode().IsRegular() {
				return fmt.Errorf("the partial %s is not a regular file", what)
			}
			offset = fi.Size()
			if offset > size {
				if err := os.Remove(partial); err != nil {
					return fmt.Errorf("discard the oversized partial %s: %w", what, err)
				}
				offset = 0
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect the partial %s: %w", what, err)
		}
		var n int64

		if offset < size {
			if fetch == nil {

				return fmt.Errorf("%s has %d of %d bytes: %w", what, offset, size, errNoDownloadPath)
			}
			f, err := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
			if err != nil {
				return fmt.Errorf("open the partial %s: %w", what, err)
			}
			if logf != nil {
				logf("plugin %s: fetching %s (%d of %d bytes present)", pluginID, what, offset, size)
			}

			limited := &limitedWriter{w: f, remaining: size - offset}
			var ferr error
			n, ferr = fetch(ctx, url, offset, limited)
			cerr := f.Close()
			if cancelled := ctx.Err(); cancelled != nil {
				return cancelled
			}
			if limited.overflow {
				if offset > 0 {

					if err := discardPartial(partial, what); err != nil {
						return err
					}
					sayTo(logf, "plugin %s: %s: the server sent more than the declared %d bytes after a resumed download — the part kept from before is discarded and the file is fetched once whole", pluginID, what, size)
					continue
				}
				_ = os.Remove(partial)
				return fmt.Errorf("fetch %s: the server sent more than the declared %d bytes: %w", what, size, errNotDeclaredBytes)
			}
			if ferr != nil {
				if offset > 0 && errors.Is(ferr, ErrNoResume) {
					if err := discardPartial(partial, what); err != nil {
						return err
					}
					sayTo(logf, "plugin %s: %s: the server will not resume — the partial is discarded and the whole file is asked for now", pluginID, what)
					continue
				}
				if accessRefused(ferr) && !reasked {
					reasked = true
					sayTo(logf, "plugin %s: %s: %v — asked again, once, of the package's own address (an address it was sent on to may have expired)", pluginID, what, ferr)
					continue
				}
				return fmt.Errorf("fetch %s: %w", what, ferr)
			}
			if cerr != nil {
				return fmt.Errorf("write %s: %w", what, cerr)
			}
		}
		sum, got, err := hashFileContext(ctx, partial)
		if err != nil {
			return err
		}
		if got < size {
			return fmt.Errorf("fetch %s: %d of %d bytes after %d more; the download will resume: %w", what, got, size, n, errShortTransfer)
		}
		if got > size || sum != sha {
			if offset > 0 {
				if err := discardPartial(partial, what); err != nil {
					return err
				}
				sayTo(logf, "plugin %s: %s: the bytes of a resumed download do not hash to the declared sha256 digest — the part kept from before is discarded and the file is fetched once whole", pluginID, what)
				continue
			}
			_ = os.Remove(partial)
			return fmt.Errorf("fetch %s: the bytes do not hash to the declared sha256 digest — discarded: %w", what, errNotDeclaredBytes)
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		if err := syncDownload(partial); err != nil {
			return fmt.Errorf("make %s durable before it takes its name: %w", what, err)
		}
		if logf != nil {
			logf("plugin %s: %s verified (%d bytes)", pluginID, what, got)
		}
		return nil
	}
}

var syncDownload = func(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if errors.Is(err, fs.ErrPermission) {
		if cerr := os.Chmod(path, 0o600); cerr != nil {
			return err
		}
		f, err = os.OpenFile(path, os.O_WRONLY, 0)
	}
	if err != nil {
		return err
	}
	err = f.Sync()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func discardPartial(partial, what string) error {
	if err := os.Remove(partial); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("discard the partial %s: %w", what, err)
	}
	return nil
}

func sayTo(logf func(string, ...any), format string, args ...any) {
	if logf != nil {
		logf(format, args...)
	}
}

var destinations = struct {
	mu   sync.Mutex
	held map[string]chan struct{}
}{held: map[string]chan struct{}{}}

func ownDestination(ctx context.Context, final string) (func(), error) {
	key := filepath.Clean(final)
	for {
		destinations.mu.Lock()
		held, busy := destinations.held[key]
		if !busy {
			mine := make(chan struct{})
			destinations.held[key] = mine
			destinations.mu.Unlock()
			var once sync.Once
			return func() {
				once.Do(func() {
					destinations.mu.Lock()
					delete(destinations.held, key)
					destinations.mu.Unlock()
					close(mine)
				})
			}, nil
		}
		destinations.mu.Unlock()
		select {
		case <-held:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

type limitedWriter struct {
	w         io.Writer
	remaining int64
	overflow  bool
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > l.remaining {
		l.overflow = true
		if l.remaining > 0 {
			_, _ = l.w.Write(p[:l.remaining])
			l.remaining = 0
		}
		return 0, errors.New("declared size exceeded")
	}
	n, err := l.w.Write(p)
	l.remaining -= int64(n)
	return n, err
}

func placeEntrypoint(pluginID, root string, inv *packagefmt.Inventory, entry entrypointSpec) error {
	for _, e := range inv.Files {
		if e.Path == entry.Name || strings.HasPrefix(e.Path, entry.Name+"/") {
			return &RuntimeError{PluginID: pluginID, Detail: fmt.Sprintf("the runtime tree already holds %s; the carrier is the bundle's, not the archive's", e.Path), Declared: true}
		}
	}
	target := filepath.Join(root, entry.Name)
	if _, err := os.Lstat(target); err == nil {
		return fmt.Errorf("the runtime tree already holds %s; the carrier is the bundle's, not the archive's", entry.Name)
	}
	sum := sha256.Sum256(entry.Bytes)
	if got := "sha256:" + hex.EncodeToString(sum[:]); got != entry.Digest {
		return fmt.Errorf("the carrier's bytes do not match the bundle's verified digest")
	}
	return os.WriteFile(target, entry.Bytes, 0o700)
}

func removeIfPresent(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func ceilingWords(over []runtimeCeiling) []string {
	words := make([]string, len(over))
	for i, c := range over {
		words[i] = c.String()
	}
	return words
}

func accessRefused(err error) bool {
	var se *FetchStatusError
	return errors.As(err, &se) && (se.Status == 401 || se.Status == 403)
}
