//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// The declaration is bounded; a fetch verifies every byte, resumes a
// broken download, refuses a body that does not hash or overruns its
// size, keeps a preinstalled file that hashes right, and offline names
// what is missing instead of inventing it.
func TestModelsAreFetchedVerifiedResumedAndHonestOffline(t *testing.T) {
	good := `[{"name":"stt-int8.bin","url":"https://models.example.test/stt.bin","sha256":"` + strings.Repeat("a", 64) + `","size":1024}]`
	if _, err := ParseModels([]byte(good)); err != nil {
		t.Fatal(err)
	}
	// A hub-materialized model directory carries its .gitattributes; an
	// engine that catalogs the directory declares it, hash and all.
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
		// The path is bounded and portable, or refused.
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

	// What the plugin store's writer does with a declaration: bodies by
	// path, each its own bytes, behind an address the declaration names as
	// https (the parser's rule) and the test serves over plain HTTP.
	bodies := map[string][]byte{"/stt": []byte(strings.Repeat("W", 4096)), "/cut": []byte(strings.Repeat("T", 4096)),
		"/wrong": []byte(strings.Repeat("V", 4096)), "/long": []byte(strings.Repeat("L", 4096))}
	var ranges []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ranges = append(ranges, r.Header.Get("Range"))
		body := bodies[r.URL.Path]
		if r.URL.Path == "/wrong" {
			body = []byte(strings.Repeat("X", 4096))
		}
		if r.URL.Path == "/long" {
			body = append(append([]byte{}, body...), []byte("extra")...)
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
	fetch := servedAt(ts.URL)
	decl := func(name, path string) ModelDecl {
		b := bodies[path]
		if b == nil {
			b = bodies["/stt"]
		}
		return ModelDecl{Name: name, URL: modelsHost + path, SHA256: sha(b), Size: int64(len(b))}
	}
	w := openStore(t)
	var logs []string
	logf := func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }
	ensure := func(fetch ModelFetcher, adopt Adoption, models ...ModelDecl) error {
		names := []string{"stt.bin", "tts.bin", "vad.bin", "turn.bin"}
		return EnsureEntry(context.Background(), w, viewWork(names, models...), fetch, adopt, logf)
	}
	statuses := func(models ...ModelDecl) []ModelStatus {
		m := modelMaterial(w, "p", "1", models...)
		m.view.Names = []string{"stt.bin", "tts.bin", "vad.bin", "turn.bin"}
		m.ModelsDir, _ = w.View(m.viewKey())
		return m.modelStatuses(models)
	}

	stt := decl("stt.bin", "/stt")
	if err := ensure(fetch, Adoption{}, stt); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if fi, err := os.Stat(blobOf(w, stt)); err != nil || fi.Size() != 4096 || fi.Mode().Perm()&0o111 != 0 {
		t.Fatalf("verified, never executable: %v %v", fi, err)
	}
	if st := statuses(stt); !st[0].Present {
		t.Fatalf("status: %+v", st)
	}
	before := len(ranges)
	if err := ensure(fetch, Adoption{}, stt); err != nil || len(ranges) != before {
		t.Fatalf("a present model is not refetched: %v %d", err, len(ranges)-before)
	}

	// A broken download resumes from the partial with a Range request.
	cut := decl("tts.bin", "/cut")
	err := ensure(fetch, Adoption{}, cut)
	if err == nil || !strings.Contains(err.Error(), "will resume") {
		t.Fatalf("a short body is not the model: %v", err)
	}
	if st := statuses(cut); st[0].Present || st[0].Partial != 1000 {
		t.Fatalf("the partial is kept: %+v", st)
	}
	if err := ensure(fetch, Adoption{}, cut); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if ranges[len(ranges)-1] != "bytes=1000-" {
		t.Fatalf("resumed with a Range: %v", ranges)
	}
	if sum, _, _ := hashFile(blobOf(w, cut)); sum != cut.SHA256 {
		t.Fatal("the resumed file hashes right")
	}

	wrong := decl("vad.bin", "/wrong")
	if err := ensure(fetch, Adoption{}, wrong); err == nil || !strings.Contains(err.Error(), "do not hash") {
		t.Fatalf("a body that does not hash is discarded: %v", err)
	}
	if _, err := os.Stat(blobOf(w, wrong)); !os.IsNotExist(err) {
		t.Fatal("nothing under the model's name")
	}
	long := decl("turn.bin", "/long")
	if err := ensure(fetch, Adoption{}, long); err == nil || !strings.Contains(err.Error(), "more than the declared") {
		t.Fatalf("a body over its size is cut off: %v", err)
	}

	w = openStore(t)
	pre := t.TempDir()
	if err := os.WriteFile(filepath.Join(pre, "stt.bin"), bodies["/stt"], 0o644); err != nil {
		t.Fatal(err)
	}
	slot := Adoption{ModelDirs: []string{pre}}
	never := decl("stt.bin", "/never")
	if err := ensure(nil, slot, never); err != nil {
		t.Fatalf("a slot's file that hashes right needs no fetch: %v", err)
	}
	tts := decl("tts.bin", "/cut")
	err = ensure(nil, slot, never, tts)
	var missing *ModelsMissingError
	if !errorsAs(err, &missing) || len(missing.Missing) != 1 || missing.Missing[0] != "tts.bin" {
		t.Fatalf("offline names what is missing: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pre, "tts.bin"), bytes.Repeat([]byte("c"), int(tts.Size)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensure(nil, slot, tts); err == nil {
		t.Fatal("a slot's file that does not hash is not the model")
	}
	if _, err := os.Stat(blobOf(w, tts)); !os.IsNotExist(err) {
		t.Fatal("a slot's wrong file was published")
	}
	if _, err := os.Stat(filepath.Join(pre, "tts.bin")); err != nil {
		t.Fatalf("the slot's own file is the slot's, and is left: %v", err)
	}
}

// modelsHost is the https address the tests' declarations name.
const modelsHost = "https://models.example.test"

// servedAt fetches a declaration's address from the test's own server.
func servedAt(server string) ModelFetcher {
	return func(ctx context.Context, url string, offset int64, w io.Writer) (int64, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server+strings.TrimPrefix(url, modelsHost), nil)
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

// A multi-file model keeps its directory shape: each file lands at its
// declared path, is resumed and verified there, and is never reached
// through a link an operator left in the way.
func TestAModelLandsAtItsDeclaredPathAndNeverThroughALink(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Each file's bytes are its last path segment, repeated.
		weights := []byte(strings.Repeat(path.Base(r.URL.Path), 4096))
		start := int64(0)
		if rg := r.Header.Get("Range"); strings.HasPrefix(rg, "bytes=") {
			start, _ = strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(rg, "bytes="), "-"), 10, 64)
			w.WriteHeader(http.StatusPartialContent)
		}
		if strings.HasPrefix(r.URL.Path, "/cut/") && start == 0 {
			w.Write(weights[:1000])
			return
		}
		w.Write(weights[start:])
	}))
	defer ts.Close()
	fetch := servedAt(ts.URL)
	declOf := func(name, path, url string, fill string) ModelDecl {
		b := []byte(strings.Repeat(fill, 4096))
		return ModelDecl{Name: name, Path: path, URL: modelsHost + url + "/" + fill, SHA256: sha(b), Size: int64(len(b))}
	}
	w := openStore(t)
	names := []string{"stt.config", "tts.weights", "vad.weights"}
	view, _ := w.View(plugstore.ViewKey(modelsDigest, names))
	ensure := func(models ...ModelDecl) error {
		return EnsureEntry(context.Background(), w, viewWork(names, models...), fetch, Adoption{}, nil)
	}
	cfg := declOf("stt.config", "stt/tokenizer/config.json", "/w", "C")
	if err := ensure(cfg); err != nil {
		t.Fatalf("fetch into a directory shape: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(view, "stt", "tokenizer", "config.json")); err != nil || fi.Size() != 4096 || fi.Mode().Perm()&0o111 != 0 {
		t.Fatalf("the file lands at its path, never executable: %v %v", fi, err)
	}
	if fi, err := os.Stat(filepath.Join(view, "stt")); err != nil || !fi.IsDir() || (fi.Mode().Perm()&0o077 != 0 && !isWindows()) {
		t.Fatalf("its directories are the store's, 0700: %v %v", fi, err)
	}
	// A broken download resumes from its blob's partial.
	cut := declOf("tts.weights", "tts/model.safetensors", "/cut", "T")
	if err := ensure(cut); err == nil || !strings.Contains(err.Error(), "will resume") {
		t.Fatalf("a short body is not the model: %v", err)
	}
	if fi, err := os.Stat(blobPartial(w, cut)); err != nil || fi.Size() != 1000 {
		t.Fatalf("the partial is kept: %v %v", fi, err)
	}
	if err := ensure(cut); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if sum, _, _ := hashFile(filepath.Join(view, "tts", "model.safetensors")); sum != cut.SHA256 {
		t.Fatal("the resumed file hashes right")
	}
	// A link in the way is refused, and nothing is written through it.
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(view, "vad")); err != nil {
		t.Skipf("this host cannot create a link here: %v", err)
	}
	err := ensure(declOf("vad.weights", "vad/model.bin", "/w", "V"))
	if err == nil || !strings.Contains(err.Error(), "is a link") {
		t.Fatalf("a link is not the store's directory: %v", err)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Fatalf("written through the link: %v", entries)
	}
}

func isWindows() bool { return filepath.Separator == '\\' }

// The running platform's components, nothing else: with a profile, the
// host acquires exactly the models it names; without one, everything.
func TestOnlyTheSelectedVariantsModelsAreAcquired(t *testing.T) {
	decls := []ModelDecl{{Name: "stt-cuda"}, {Name: "stt-metal"}, {Name: "tts"}, {Name: "vad"}}
	got, err := selectModels(decls, &AcceleratorProfile{Models: []string{"tts", "stt-metal"}}, nil)
	if err != nil || len(got) != 2 || got[0].Name != "stt-metal" || got[1].Name != "tts" {
		t.Fatalf("the profile's set, in declaration order: %v %v", got, err)
	}
	if _, err := selectModels(decls, &AcceleratorProfile{Models: []string{"tts", "stt-rocm"}}, nil); err == nil || !strings.Contains(err.Error(), "does not declare") {
		t.Fatalf("an undeclared model is refused: %v", err)
	}
	if got, err := selectModels(decls, nil, nil); err != nil || len(got) != 4 {
		t.Fatalf("without a profile, everything declared: %v %v", got, err)
	}
}
