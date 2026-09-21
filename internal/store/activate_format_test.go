package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/store/compressvfs"
)

func activationStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "identity.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	addPluginMemory(t, s, "saved", "lighthouse keeper")
	t.Cleanup(func() { s.Close() })
	return s, path
}

func assertActivatedData(t *testing.T, s *Store, format string) {
	t.Helper()
	got, err := s.DatabaseFormat(context.Background())
	if err != nil || got != format {
		t.Fatalf("format %q: %v", got, err)
	}
	var found int
	if err := s.DB().QueryRow(pmTri, "light").Scan(&found); err != nil || found != 1 {
		t.Fatalf("FTS: %d %v", found, err)
	}
	if err := s.QuickCheck(); err != nil {
		t.Fatal(err)
	}
	if err := s.ForeignKeyCheck(); err != nil {
		t.Fatal(err)
	}
}

func TestStartupFormatRoundtripUsesCurrentData(t *testing.T) {
	s, path := activationStore(t)
	for _, target := range []string{"", "sqlite", "zstd", "zstd", "sqlite"} {
		previous := s
		result, err := s.ActivateFormatAtStartup(context.Background(), path, target)
		if err != nil || result.Opened == nil || result.Recovery != "" {
			t.Fatalf("%q: %+v %v", target, result, err)
		}
		s = result.Opened
		if s != previous {
			t.Cleanup(func() { s.Close() })
		}
		want := target
		if want == "" {
			want = "sqlite"
		}
		assertActivatedData(t, s, want)
		// .
		// .
		if target == "zstd" {
			if _, err := s.DB().Exec("UPDATE plugin_memories SET text='lighthouse keeper changed' WHERE id='saved'"); err != nil {
				t.Fatal(err)
			}
		}
	}
	var text string
	if err := s.DB().QueryRow("SELECT text FROM plugin_memories WHERE id='saved'").Scan(&text); err != nil || text != "lighthouse keeper changed" {
		t.Fatalf("new history lost: %q %v", text, err)
	}
	paths, err := FormatRecoveryDirectories(path)
	if err != nil || len(paths) != 0 {
		t.Fatalf("working material: %v %v", paths, err)
	}
}

func TestStartupFormatRefusalKeepsOriginalUsable(t *testing.T) {
	for _, refusal := range []string{"bad-format", "cancelled", "borrowed-connection", "different-path", "external-reader", "read-only"} {
		t.Run(refusal, func(t *testing.T) {
			s, path := activationStore(t)
			ctx := context.Background()
			target := compressvfs.FormatZstd
			switch refusal {
			case "bad-format":
				target = "other"
			case "read-only":
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				var err error
				s, err = OpenReadOnly(path)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "borrowed-connection":
				c, err := s.DB().Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
			case "different-path":
				path = filepath.Join(t.TempDir(), "other.db")
				if err := os.WriteFile(path, []byte("other"), 0600); err != nil {
					t.Fatal(err)
				}
			case "external-reader":
				reader, err := OpenReadOnly(path)
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				pin, err := reader.DB().BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer pin.Rollback()
				var n int
				if err := pin.QueryRow("SELECT count(*) FROM plugin_memories").Scan(&n); err != nil {
					t.Fatal(err)
				}
				if _, err := s.DB().Exec("UPDATE plugin_memories SET text='lighthouse keeper changed' WHERE id='saved'"); err != nil {
					t.Fatal(err)
				}
				if _, err := s.DB().Exec("PRAGMA busy_timeout=1"); err != nil {
					t.Fatal(err)
				}
			}
			result, err := s.ActivateFormatAtStartup(ctx, path, target)
			if err == nil || result.Published || result.Opened != s {
				t.Fatalf("refusal: %+v %v", result, err)
			}
			assertActivatedData(t, s, "sqlite")
		})
	}
}

func TestStartupFormatReopenFailureRetainsRecovery(t *testing.T) {
	s, path := activationStore(t)
	result, err := s.activateFormat(context.Background(), path, "zstd", func(from, to string) (bool, error) {
		// .
		published, err := atomicfile.Replace(from, to)
		if err != nil {
			return published, err
		}
		return published, os.WriteFile(to, []byte("damaged after publication"), 0600)
	})
	if err == nil || !result.Published || result.Opened != nil || result.Recovery == "" {
		t.Fatalf("unverified file admitted: %+v %v", result, err)
	}
	if err := checkDatabaseImage(context.Background(), result.Recovery); err != nil {
		t.Fatalf("recovery not usable: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "damaged after publication" {
		t.Fatalf("failed new file was silently replaced: %q %v", b, err)
	}
}

func TestStartupFormatPublicationFailures(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(map[bool]string{false: "before", true: "after"}[after], func(t *testing.T) {
			s, path := activationStore(t)
			fault := syscall.EIO
			result, err := s.activateFormat(context.Background(), path, "zstd", func(from, to string) (bool, error) {
				if !after {
					return false, fault
				}
				published, err := atomicfile.Replace(from, to)
				return published, errors.Join(err, fault)
			})
			if !errors.Is(err, fault) || result.Published != after {
				t.Fatalf("result: %+v %v", result, err)
			}
			if !after {
				if result.Opened == nil || result.Recovery != "" {
					t.Fatalf("lost original: %+v", result)
				}
				defer result.Opened.Close()
				assertActivatedData(t, result.Opened, "sqlite")
				return
			}
			if result.Opened != nil || result.Recovery == "" {
				t.Fatalf("uncertain publication resumed writers: %+v", result)
			}
			if err := checkDatabaseImage(context.Background(), result.Recovery); err != nil {
				t.Fatalf("recovery: %v", err)
			}
			// .
			next, err := New(path)
			if err != nil {
				t.Fatal(err)
			}
			defer next.Close()
			assertActivatedData(t, next, "zstd")
			paths, err := FormatRecoveryDirectories(path)
			if err != nil || len(paths) != 1 || !strings.HasPrefix(result.Recovery, paths[0]) {
				t.Fatalf("recovery discovery: %v %v", paths, err)
			}
		})
	}
}
