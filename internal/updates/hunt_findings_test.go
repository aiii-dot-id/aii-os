package updates

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// http.Client.Timeout covers the body read, so a client-level timeout
// is always the binding one; at 30s it made downloadTimeout dead code.
func TestTheHTTPClientCarriesNoTimeoutOfItsOwn(t *testing.T) {
	c := newTestChecker(nil, t.TempDir(), nil)
	if c.httpClient.Timeout != 0 {
		t.Fatalf("http.Client.Timeout = %v — it overrides the %v download budget on every request", c.httpClient.Timeout, downloadTimeout)
	}
	if metadataTimeout >= downloadTimeout {
		t.Fatalf("metadataTimeout %v is not shorter than downloadTimeout %v", metadataTimeout, downloadTimeout)
	}
}

// "Cannot see the backup" is not "there is no backup". The parent-is-a-
// file case is a real fixture, not a synthetic errno: POSIX answers it
// ENOTDIR, Windows "path not found", and both must refuse.
func TestBackupStatRefusalDistinguishesAbsentFromUnseeable(t *testing.T) {
	notDir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	under := filepath.Join(notDir, "backup")
	_, underErr := os.Stat(under)
	if underErr == nil {
		t.Fatal("fixture: a path under a regular file must not stat")
	}
	absent := filepath.Join(t.TempDir(), "backup")
	_, absentErr := os.Stat(absent)
	for _, c := range []struct {
		path   string
		err    error
		refuse bool
	}{
		{"x", nil, false},
		{"x", os.ErrNotExist, false},
		{absent, absentErr, false},
		{under, underErr, true},
		{"x", os.ErrPermission, true},
		{"x", errors.New("input/output error"), true},
	} {
		got := statRefusal(c.path, c.err)
		if (got != nil) != c.refuse {
			t.Fatalf("statRefusal(%s, %v) = %v, want refuse=%v — an unseeable backup would be treated as absent and the tombstone deleted", c.path, c.err, got, c.refuse)
		}
	}
}

// An oversize body is refused, not truncated into a hash mismatch.
func TestReadBoundedRefusesRatherThanTruncates(t *testing.T) {
	if b, err := readBounded(strings.NewReader("abcdef"), 6, "x"); err != nil || string(b) != "abcdef" {
		t.Fatalf("exact fit: %q %v", b, err)
	}
	b, err := readBounded(strings.NewReader("abcdef"), 5, "release download")
	if err == nil {
		t.Fatalf("oversize body returned %q with no error — a truncated copy", b)
	}
	if !strings.Contains(err.Error(), "exceeds the 5 byte limit") {
		t.Fatalf("wrong refusal: %v", err)
	}
}
