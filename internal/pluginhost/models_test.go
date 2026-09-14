package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// .
// .
// .
// .
func TestModelsAreFetchedVerifiedResumedAndHonestOffline(t *testing.T) {
	good := `[{"name":"stt-int8.bin","url":"https://models.example.test/stt.bin","sha256":"` + strings.Repeat("a", 64) + `","size":1024}]`
	if _, err := ParseModels([]byte(good)); err != nil {
		t.Fatal(err)
	}
	// .
	// .
	dotted := `[{"name":"stt-gitattributes","path":"stt/.gitattributes","url":"https://models.example.test/g","sha256":"` + strings.Repeat("a", 64) + `","size":1519}]`
	if decls, err := ParseModels([]byte(dotted)); err != nil || decls[0].Dest() != "stt/.gitattributes" {
		t.Fatalf("a leading dot is a portable file name: %v %+v", err, decls)
	}
	for name, tc := range map[string]struct{ raw, want string }{
		"http url":      {`[{"name":"m","url":"http://x/m","sha256":"` + strings.Repeat("a", 64) + `","size":1}]`, "https"},
		"bad hash":      {`[{"name":"m","url":"https://x/m","sha256":"abc","size":1}]`, "sha256"},
		"bad name":      {`[{"name":"../m","url":"https://x/m","sha256":"` + strings.Repeat("a", 64) + `","size":1}]`, "name"},
		"zero size":     {`[{"name":"m","url":"https://x/m","sha256":"` + strings.Repeat("a", 64) + `","size":0}]`, "size"},
		"twice":         {`[{"name":"m","url":"https://x/m","sha256":"` + strings.Repeat("a", 64) + `","size":1},{"name":"m","url":"https://x/n","sha256":"` + strings.Repeat("b", 64) + `","size":1}]`, "twice"},
		"unknown field": {`[{"name":"m","url":"https://x/m","sha256":"` + strings.Repeat("a", 64) + `","size":1,"exec":true}]`, "unknown field"},
		// .
		"path traversal": {`[{"name":"m","path":"../m","url":"https://x/m","sha256":"` + strings.Repeat("a", 64) + `","size":1}]`, "portable file name"},
		"path absolute":  {`[{"name":"m","path":"/etc/m","url":"https://x/m","sha256":"` + strings.Repeat("a", 64) + `","size":1}]`, "portable file name"},
		"path backslash": {`[{"name":"m","path":"a\\b","url":"https://x/m","sha256":"` + strings.Repeat("a", 64) + `","size":1}]`, "portable file name"},
		"path drive":     {`[{"name":"m","path":"C:/m","url":"https://x/m","sha256":"` + strings.Repeat("a", 64) + `","size":1}]`, "portable file name"},
		"path stream":    {`[{"name":"m","path":"m:zone","url":"https://x/m","sha256":"` + strings.Repeat("a", 64) + `","size":1}]`, "portable file name"},
		"path dot end":   {`[{"name":"m","path":"stt./m","url":"https://x/m","sha256":"` + strings.Repeat("a", 64) + `","size":1}]`, "portable file name"},
		"path dot only":  {`[{"name":"m","path":"stt/./m","url":"https://x/m","sha256":"` + strings.Repeat("a", 64) + `","size":1}]`, "portable file name"},
		"path device":    {`[{"name":"m","path":"stt/NUL.bin","url":"https://x/m","sha256":"` + strings.Repeat("a", 64) + `","size":1}]`, "device name"},
		"path partial":   {`[{"name":"m","path":"m.partial","url":"https://x/m","sha256":"` + strings.Repeat("a", 64) + `","size":1}]`, "suffix is the host's"},
		"path deep":      {`[{"name":"m","path":"a/b/c/d/e/f/g/h/i","url":"https://x/m","sha256":"` + strings.Repeat("a", 64) + `","size":1}]`, "deeper than 8"},
		"path case":      {`[{"name":"m","path":"STT/config.json","url":"https://x/m","sha256":"` + strings.Repeat("a", 64) + `","size":1},{"name":"n","path":"stt/Config.json","url":"https://x/n","sha256":"` + strings.Repeat("b", 64) + `","size":1}]`, "case-insensitive"},
		"path directory": {`[{"name":"m","path":"stt","url":"https://x/m","sha256":"` + strings.Repeat("a", 64) + `","size":1},{"name":"n","path":"stt/config.json","url":"https://x/n","sha256":"` + strings.Repeat("b", 64) + `","size":1}]`, "is a directory of"},
	} {
		if _, err := ParseModels([]byte(tc.raw)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v (want %q)", name, err, tc.want)
		}
	}

	weights := []byte(strings.Repeat("W", 4096))
	var ranges []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ranges = append(ranges, r.Header.Get("Range"))
		body := weights
		if r.URL.Path == "/wrong" {
			body = []byte(strings.Repeat("X", 4096))
		}
		if r.URL.Path == "/long" {
			body = append(append([]byte{}, weights...), []byte("extra")...)
		}
		start := int64(0)
		if rg := r.Header.Get("Range"); strings.HasPrefix(rg, "bytes=") {
			start, _ = strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(rg, "bytes="), "-"), 10, 64)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(body)-1, len(body)))
			w.WriteHeader(http.StatusPartialContent)
		}
		if r.URL.Path == "/cut" && start == 0 {
			w.Write(body[:1000])
			return
		}
		w.Write(body[start:])
	}))
	defer ts.Close()
	fetch := func(ctx context.Context, url string, offset int64, w io.Writer) (int64, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if offset > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		return io.Copy(w, resp.Body)
	}
	decl := func(name, path string) ModelDecl {
		return ModelDecl{Name: name, URL: ts.URL + path, SHA256: sha(weights), Size: int64(len(weights))}
	}
	dir := t.TempDir()
	var logs []string
	logf := func(f string, a ...interface{}) { logs = append(logs, fmt.Sprintf(f, a...)) }

	if err := EnsureModels(context.Background(), "p", []ModelDecl{decl("stt.bin", "/stt")}, dir, fetch, logf); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(dir, "stt.bin")); err != nil || fi.Size() != 4096 || fi.Mode().Perm()&0o111 != 0 {
		t.Fatalf("verified, never executable: %v %v", fi, err)
	}
	st := ModelStatuses([]ModelDecl{decl("stt.bin", "/stt")}, dir)
	if !st[0].Present {
		t.Fatalf("status: %+v", st)
	}
	before := len(ranges)
	if err := EnsureModels(context.Background(), "p", []ModelDecl{decl("stt.bin", "/stt")}, dir, fetch, logf); err != nil || len(ranges) != before {
		t.Fatalf("a present verified file is not refetched: %v %d", err, len(ranges)-before)
	}

	// .
	cut := decl("tts.bin", "/cut")
	err := EnsureModels(context.Background(), "p", []ModelDecl{cut}, dir, fetch, logf)
	if err == nil || !strings.Contains(err.Error(), "will resume") {
		t.Fatalf("a short body is not the model: %v", err)
	}
	if st := ModelStatuses([]ModelDecl{cut}, dir); st[0].Present || st[0].Partial != 1000 {
		t.Fatalf("the partial is kept: %+v", st)
	}
	if err := EnsureModels(context.Background(), "p", []ModelDecl{cut}, dir, fetch, logf); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if ranges[len(ranges)-1] != "bytes=1000-" {
		t.Fatalf("resumed with a Range: %v", ranges)
	}
	if sum, _, _ := hashFile(filepath.Join(dir, "tts.bin")); sum != sha(weights) {
		t.Fatal("the resumed file hashes right")
	}

	wrong := decl("vad.bin", "/wrong")
	if err := EnsureModels(context.Background(), "p", []ModelDecl{wrong}, dir, fetch, logf); err == nil || !strings.Contains(err.Error(), "do not hash") {
		t.Fatalf("a body that does not hash is discarded: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "vad.bin")); !os.IsNotExist(err) {
		t.Fatal("nothing under the model's name")
	}
	long := decl("turn.bin", "/long")
	if err := EnsureModels(context.Background(), "p", []ModelDecl{long}, dir, fetch, logf); err == nil || !strings.Contains(err.Error(), "more than the declared") {
		t.Fatalf("a body over its size is cut off: %v", err)
	}

	// .
	pre := t.TempDir()
	if err := os.WriteFile(filepath.Join(pre, "stt.bin"), weights, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureModels(context.Background(), "p", []ModelDecl{decl("stt.bin", "/never")}, pre, nil, logf); err != nil {
		t.Fatalf("a preinstalled file that hashes right needs no fetch: %v", err)
	}
	err = EnsureModels(context.Background(), "p", []ModelDecl{decl("stt.bin", "/never"), decl("tts.bin", "/never")}, pre, nil, logf)
	var missing *ModelsMissingError
	if !errorsAs(err, &missing) || len(missing.Missing) != 1 || missing.Missing[0] != "tts.bin" {
		t.Fatalf("offline names what is missing: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pre, "tts.bin"), []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureModels(context.Background(), "p", []ModelDecl{decl("tts.bin", "/never")}, pre, nil, logf); err == nil {
		t.Fatal("a preinstalled file that does not hash is not the model")
	}
	if _, err := os.Stat(filepath.Join(pre, "tts.bin")); !os.IsNotExist(err) {
		t.Fatal("the wrong file is removed, not handed to the engine")
	}
}

func errorsAs(err error, target **ModelsMissingError) bool {
	for e := err; e != nil; {
		if m, ok := e.(*ModelsMissingError); ok {
			*target = m
			return true
		}
		u, ok := e.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		e = u.Unwrap()
	}
	return false
}

// .
// .
// .
func TestAModelLandsAtItsDeclaredPathAndNeverThroughALink(t *testing.T) {
	weights := []byte(strings.Repeat("W", 4096))
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := int64(0)
		if rg := r.Header.Get("Range"); strings.HasPrefix(rg, "bytes=") {
			start, _ = strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(rg, "bytes="), "-"), 10, 64)
			w.WriteHeader(http.StatusPartialContent)
		}
		if r.URL.Path == "/cut" && start == 0 {
			w.Write(weights[:1000])
			return
		}
		w.Write(weights[start:])
	}))
	defer ts.Close()
	fetch := func(ctx context.Context, url string, offset int64, w io.Writer) (int64, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if offset > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		return io.Copy(w, resp.Body)
	}
	decl := func(name, path, url string) ModelDecl {
		return ModelDecl{Name: name, Path: path, URL: ts.URL + url, SHA256: sha(weights), Size: int64(len(weights))}
	}
	dir := t.TempDir()
	cfg := decl("stt.config", "stt/tokenizer/config.json", "/w")
	if err := EnsureModels(context.Background(), "p", []ModelDecl{cfg}, dir, fetch, nil); err != nil {
		t.Fatalf("fetch into a directory shape: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(dir, "stt", "tokenizer", "config.json")); err != nil || fi.Size() != 4096 || fi.Mode().Perm()&0o111 != 0 {
		t.Fatalf("the file lands at its path, never executable: %v %v", fi, err)
	}
	if fi, err := os.Stat(filepath.Join(dir, "stt")); err != nil || !fi.IsDir() || (fi.Mode().Perm()&0o077 != 0 && !isWindows()) {
		t.Fatalf("its directories are the host's, 0700: %v %v", fi, err)
	}
	if st := ModelStatuses([]ModelDecl{cfg}, dir); !st[0].Present || st[0].Path != "stt/tokenizer/config.json" {
		t.Fatalf("status names the path: %+v", st)
	}
	// .
	cut := decl("tts.weights", "tts/model.safetensors", "/cut")
	if err := EnsureModels(context.Background(), "p", []ModelDecl{cut}, dir, fetch, nil); err == nil || !strings.Contains(err.Error(), "will resume") {
		t.Fatalf("a short body is not the model: %v", err)
	}
	if st := ModelStatuses([]ModelDecl{cut}, dir); st[0].Present || st[0].Partial != 1000 {
		t.Fatalf("the partial is kept at the path: %+v", st)
	}
	if err := EnsureModels(context.Background(), "p", []ModelDecl{cut}, dir, fetch, nil); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if sum, _, _ := hashFile(filepath.Join(dir, "tts", "model.safetensors")); sum != sha(weights) {
		t.Fatal("the resumed file hashes right")
	}
	// .
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(dir, "vad")); err != nil {
		t.Skipf("this host cannot create a link here: %v", err)
	}
	err := EnsureModels(context.Background(), "p", []ModelDecl{decl("vad.weights", "vad/model.bin", "/w")}, dir, fetch, nil)
	var missing *ModelsMissingError
	if !errorsAs(err, &missing) || missing.Cause == nil || !strings.Contains(missing.Cause.Error(), "is a link") {
		t.Fatalf("a link is not the host's directory: %v", err)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Fatalf("written through the link: %v", entries)
	}
}

func isWindows() bool { return filepath.Separator == '\\' }

// .
// .
func TestOnlyTheSelectedVariantsModelsAreAcquired(t *testing.T) {
	decls := []ModelDecl{{Name: "stt-cuda"}, {Name: "stt-metal"}, {Name: "tts"}, {Name: "vad"}}
	got, err := selectModels(decls, &AcceleratorProfile{Models: []string{"tts", "stt-metal"}})
	if err != nil || len(got) != 2 || got[0].Name != "stt-metal" || got[1].Name != "tts" {
		t.Fatalf("the profile's set, in declaration order: %v %v", got, err)
	}
	if _, err := selectModels(decls, &AcceleratorProfile{Models: []string{"tts", "stt-rocm"}}); err == nil || !strings.Contains(err.Error(), "does not declare") {
		t.Fatalf("an undeclared model is refused: %v", err)
	}
	if got, err := selectModels(decls, nil); err != nil || len(got) != 4 {
		t.Fatalf("without a profile, everything declared: %v %v", got, err)
	}
}
