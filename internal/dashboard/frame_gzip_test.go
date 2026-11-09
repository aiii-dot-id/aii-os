package dashboard

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNegotiateGzip(t *testing.T) {
	const gz, id, refuse = "gzip", "identity", "406"
	for _, c := range []struct {
		fields []string
		want   string // with gzip offered
		alone  string // with identity alone offered
	}{
		{nil, id, id},
		{[]string{""}, id, id},
		{[]string{"gzip"}, gz, id},
		{[]string{"gzip, deflate, br, zstd"}, gz, id}, // what a browser sends
		{[]string{"GZip"}, gz, id},
		{[]string{"x-gzip"}, gz, id},
		{[]string{" gzip ;  q=0.5 , identity;q=0.4"}, gz, id},
		{[]string{"gzip;Q=1.000"}, gz, id},
		{[]string{"gzip;q=0"}, id, id},
		{[]string{"gzip;q=0.000"}, id, id},
		{[]string{"gzip;q=0.001"}, id, id}, // identity unlisted weighs 1
		{[]string{"gzip;q=0.001, identity;q=0.001"}, gz, id},
		{[]string{"gzip;q=0.5, identity"}, id, id},       // differing q: the higher wins
		{[]string{"gzip;q=0.5, identity;q=0.5"}, gz, id}, // a tie: the smaller
		{[]string{"gzip, gzip;q=0"}, id, id},             // a duplicate: its lower q
		{[]string{"gzip;q=0", "gzip"}, id, id},           // across fields too
		{[]string{"deflate"}, id, id},
		{[]string{"br;q=0, gzip"}, gz, id}, // another coding's weight is no one else's
		{[]string{"*"}, gz, id},
		{[]string{"*;q=0"}, refuse, refuse},
		{[]string{"*;q=0, gzip"}, gz, refuse},
		{[]string{"*, gzip;q=0"}, id, id}, // an exclusion outranks the wildcard
		{[]string{"*;q=0, identity"}, id, id},
		{[]string{"identity;q=0"}, refuse, refuse}, // gzip unlisted is not accepted
		{[]string{"identity;q=0, gzip"}, gz, refuse},
		{[]string{"identity;q=0, *"}, gz, refuse},
		{[]string{"gzip;q=1.5"}, id, id}, // an invalid q excludes its coding
		{[]string{"gzip;q=1.001"}, id, id},
		{[]string{"gzip;q=0.0001"}, id, id},
		{[]string{"gzip;q= 0.5"}, id, id},
		{[]string{"gzip;q=abc"}, id, id},
		{[]string{"gzip;"}, id, id},
		{[]string{"gzip;level=9"}, id, id},
		{[]string{"identity;q=x, gzip"}, gz, refuse},
		{[]string{"notgzip, gzipped"}, id, id}, // a name is matched whole, never as a substring
	} {
		for _, offered := range []bool{true, false} {
			want := c.want
			if !offered {
				want = c.alone
			}
			useGzip, acceptable := negotiateGzip(c.fields, offered)
			got := id
			switch {
			case !acceptable:
				got = refuse
			case useGzip:
				got = gz
			}
			if got != want || (useGzip && !offered) {
				t.Errorf("Accept-Encoding %q, gzip offered %v: %s, want %s", c.fields, offered, got, want)
			}
		}
	}
}

// rawClient leaves the body as it crossed the wire: no Accept-Encoding of its
// own and no decompression.
var rawClient = &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DisableCompression: true}}

// frameAsk sends one frame request to bound with the headers given, and the
// sign-in cookie when given.
func frameAsk(t *testing.T, bound, method, address, cookie string, header ...string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, "http://"+bound+address, nil)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: cookieName(bound), Value: cookie})
	}
	res, err := rawClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res, body
}

func gunzip(t *testing.T, b []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// lengthFits reports whether h's Content-Length is n, or absent from a whole
// gzip response, which ServeContent sends chunked.
func lengthFits(h http.Header, n int) bool {
	cl := h.Get("Content-Length")
	return cl == strconv.Itoa(n) || (cl == "" && h.Get("Content-Encoding") == "gzip")
}

func etagOf(b []byte) string {
	sum := sha256.Sum256(b)
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

// varies reports whether h's Vary names every one of names.
func varies(h http.Header, names ...string) bool {
	all := strings.Join(h.Values("Vary"), ",")
	for _, n := range names {
		if !strings.Contains(all, n) {
			return false
		}
	}
	return true
}

func TestTheEmbeddedFrameGoesGzipToABrowserThatTakesIt(t *testing.T) {
	_, bound := keyServer(t, nil)
	var raw, sent, kept, files int
	err := fs.WalkDir(staticFS, "static", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		embedded, _ := staticFS.ReadFile(name)
		address := strings.TrimPrefix(name, "static")
		if strings.HasSuffix(name, ".png") || strings.HasSuffix(name, ".woff2") {
			res, body := frameAsk(t, bound, "GET", address, "", "Accept-Encoding", "gzip")
			if res.Header.Get("Content-Encoding") != "" || !bytes.Equal(body, embedded) || varies(res.Header, "Accept-Encoding") {
				t.Errorf("the icon or face %s changed: Content-Encoding %q, Vary %q", address, res.Header.Get("Content-Encoding"), res.Header.Values("Vary"))
			}
			return nil
		}
		smaller := gzipOf("/"+strings.TrimPrefix(name, "static/"), embedded) != nil
		for _, at := range []string{address, "/shipped" + address, "/plain" + address} {
			plain, plainBody := frameAsk(t, bound, "GET", at, "")
			if plain.StatusCode != 200 || plain.Header.Get("Content-Encoding") != "" || !bytes.Equal(plainBody, embedded) || plain.Header.Get("ETag") != etagOf(embedded) {
				t.Fatalf("GET %s with no Accept-Encoding = %d %q; want 200 and the embedded bytes as themselves", at, plain.StatusCode, plain.Header.Get("Content-Encoding"))
			}
			res, body := frameAsk(t, bound, "GET", at, "", "Accept-Encoding", "gzip, deflate, br, zstd")
			if res.StatusCode != 200 || !varies(res.Header, "Cookie", "Accept-Encoding") || !varies(plain.Header, "Cookie", "Accept-Encoding") {
				t.Fatalf("GET %s = %d, Vary %q; want 200 varying by Cookie and Accept-Encoding", at, res.StatusCode, res.Header.Values("Vary"))
			}
			for _, h := range []string{"Content-Type", "Content-Security-Policy", "X-Content-Type-Options", "Referrer-Policy", "Cache-Control"} {
				if res.Header.Get(h) != plain.Header.Get(h) || res.Header.Get(h) == "" {
					t.Fatalf("GET %s gzip %s = %q, as itself %q", at, h, res.Header.Get(h), plain.Header.Get(h))
				}
			}
			if res.Header.Get("ETag") != etagOf(body) || !lengthFits(res.Header, len(body)) {
				t.Fatalf("GET %s: ETag %s Content-Length %s; want the tag and length of the %d bytes sent", at, res.Header.Get("ETag"), res.Header.Get("Content-Length"), len(body))
			}
			if !smaller {
				if res.Header.Get("Content-Encoding") != "" || !bytes.Equal(body, embedded) {
					t.Fatalf("GET %s, which gzip does not make smaller: Content-Encoding %q; want it as itself", at, res.Header.Get("Content-Encoding"))
				}
				continue
			}
			if res.Header.Get("Content-Encoding") != "gzip" || !bytes.Equal(gunzip(t, body), embedded) || len(body) >= len(embedded) {
				t.Fatalf("GET %s: Content-Encoding %q, %d bytes for %d; want gzip that decodes to the embedded bytes", at, res.Header.Get("Content-Encoding"), len(body), len(embedded))
			}
		}
		_, body := frameAsk(t, bound, "GET", address, "", "Accept-Encoding", "gzip")
		raw, sent, kept, files = raw+len(embedded), sent+len(body), kept+len(gzipOf("/"+strings.TrimPrefix(name, "static/"), embedded)), files+1
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"custom.css", "custom.js"} {
		if gzipOf("/"+name, []byte(shipped(t, name))) != nil {
			t.Fatalf("the shipped %s is no longer smaller than its gzip: this test needs another such file", name)
		}
	}
	t.Logf("embedded .html/.js/.css: %d files, %d bytes as themselves, %d bytes sent to a browser that takes gzip (%.1f%%), %d bytes of gzip kept", files, raw, sent, 100*float64(sent)/float64(raw), kept)
}

// NEITHER OFFERED FORM ACCEPTABLE IS A 406, and only that. A request that
// refuses identity but takes gzip gets gzip; the same request for a file gzip
// does not make smaller is refused.
func TestAFrameFileNoAcceptedFormCanCarryIsRefused(t *testing.T) {
	_, bound := keyServer(t, nil)
	for _, c := range []struct {
		address, accept string
		want            int
	}{
		{"/app.js", "identity;q=0, gzip", 200},
		{"/app.js", "*;q=0", http.StatusNotAcceptable},
		{"/custom.css", "identity;q=0, gzip", http.StatusNotAcceptable},
		{"/custom.css", "gzip", 200},
	} {
		res, _ := frameAsk(t, bound, "GET", c.address, "", "Accept-Encoding", c.accept)
		if res.StatusCode != c.want || !varies(res.Header, "Cookie", "Accept-Encoding") {
			t.Errorf("GET %s Accept-Encoding %q = %d, Vary %q; want %d varying by both", c.address, c.accept, res.StatusCode, res.Header.Values("Vary"), c.want)
		}
	}
}

// EACH FORM IS VALIDATED, MEASURED AND RANGED ON ITS OWN BYTES. A tag is the
// hash of the form it came with: it revalidates that form to a 304 and
// answers the other form in full. HEAD gives no body, and no length but that
// of the form it would send. A failed If-Match is a 412 that claims no body.
// A range and an If-Range refer to the gzip bytes, not to offsets in the
// decompressed file.
func TestTheEncodedFrameRevalidatesAndRangesOnItsOwnBytes(t *testing.T) {
	_, bound := keyServer(t, nil)
	embedded := []byte(shipped(t, "app.js"))
	gz := gzipOf("/app.js", embedded)
	if gz == nil {
		t.Fatal("app.js is not offered as gzip, so this proves nothing")
	}
	forms := []struct {
		accept string
		bytes  []byte
		coding string
	}{{"gzip", gz, "gzip"}, {"identity", embedded, ""}}
	for _, f := range forms {
		for _, held := range forms {
			res, body := frameAsk(t, bound, "GET", "/app.js", "", "Accept-Encoding", f.accept, "If-None-Match", etagOf(held.bytes))
			if f.coding == held.coding {
				if res.StatusCode != http.StatusNotModified || len(body) != 0 || res.Header.Get("Content-Encoding") != "" || res.Header.Get("ETag") != etagOf(f.bytes) || !varies(res.Header, "Cookie", "Accept-Encoding") {
					t.Fatalf("%s revalidated with its own tag = %d, %d bytes, Content-Encoding %q; want a 304 with no body, its tag, varying by both", f.accept, res.StatusCode, len(body), res.Header.Get("Content-Encoding"))
				}
				continue
			}
			if res.StatusCode != 200 || !bytes.Equal(body, f.bytes) || res.Header.Get("Content-Encoding") != f.coding || res.Header.Get("ETag") != etagOf(f.bytes) {
				t.Fatalf("%s revalidated with the %s tag = %d %q; want 200, its own bytes and tag", f.accept, held.accept, res.StatusCode, res.Header.Get("Content-Encoding"))
			}
		}
		head, body := frameAsk(t, bound, "HEAD", "/app.js", "", "Accept-Encoding", f.accept)
		if head.StatusCode != 200 || len(body) != 0 || !lengthFits(head.Header, len(f.bytes)) || head.Header.Get("Content-Encoding") != f.coding || head.Header.Get("ETag") != etagOf(f.bytes) {
			t.Fatalf("HEAD %s = %d, %d bytes, Content-Length %s, Content-Encoding %q; want 200, no body, %d", f.accept, head.StatusCode, len(body), head.Header.Get("Content-Length"), head.Header.Get("Content-Encoding"), len(f.bytes))
		}
		failed, body := frameAsk(t, bound, "GET", "/app.js", "", "Accept-Encoding", f.accept, "If-Match", `"not-this-file"`)
		if cl := failed.Header.Get("Content-Length"); failed.StatusCode != http.StatusPreconditionFailed || len(body) != 0 || (cl != "" && cl != "0") {
			t.Fatalf("%s with a failed If-Match = %d, %d bytes, Content-Length %q; want 412 claiming no body", f.accept, failed.StatusCode, len(body), cl)
		}
	}
	part, body := frameAsk(t, bound, "GET", "/app.js", "", "Accept-Encoding", "gzip", "Range", "bytes=10-19")
	if part.StatusCode != http.StatusPartialContent || !bytes.Equal(body, gz[10:20]) || part.Header.Get("Content-Range") != "bytes 10-19/"+strconv.Itoa(len(gz)) || part.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("a range of the gzip form = %d %q, Content-Range %q; want 206 and bytes 10-19 of the gzip bytes", part.StatusCode, body, part.Header.Get("Content-Range"))
	}
	if part, body := frameAsk(t, bound, "GET", "/app.js", "", "Accept-Encoding", "gzip", "Range", "bytes=10-19", "If-Range", etagOf(gz)); part.StatusCode != http.StatusPartialContent || !bytes.Equal(body, gz[10:20]) {
		t.Fatalf("If-Range with the gzip tag = %d; want 206 of the gzip bytes", part.StatusCode)
	}
	if whole, body := frameAsk(t, bound, "GET", "/app.js", "", "Accept-Encoding", "gzip", "Range", "bytes=10-19", "If-Range", etagOf(embedded)); whole.StatusCode != 200 || !bytes.Equal(body, gz) {
		t.Fatalf("If-Range with the identity tag on the gzip form = %d, %d bytes; want 200 and the whole gzip form", whole.StatusCode, len(body))
	}
}

// THE OVERLAY IS NEVER ANSWERED FROM THE COMPRESSED EMBED. The compiled
// theme.css is compressed and kept once a request without the cookie asks for
// it; a signed-in request for the same address is then given the overlay's
// bytes as they are, then its edit, and on its removal the compiled file
// again, compressed. A request that is not signed in never sees the overlay.
func TestTheOverlayIsNeverAnsweredFromTheCompressedEmbed(t *testing.T) {
	s, bound := keyServer(t, nil)
	dir := t.TempDir()
	s.SetUIOverlay(dir)
	cookie, _ := signInAt(t, bound, pageKeyTestToken)
	if cookie == "" {
		t.Fatal("the sign-in gave no cookie, so this proves nothing")
	}
	embedded := []byte(shipped(t, "theme.css"))
	compiled := func(why, cookie string) {
		t.Helper()
		res, body := frameAsk(t, bound, "GET", "/theme.css", cookie, "Accept-Encoding", "gzip")
		if res.Header.Get("Content-Encoding") != "gzip" || !bytes.Equal(gunzip(t, body), embedded) {
			t.Fatalf("%s: Content-Encoding %q; want the compiled theme.css, gzip", why, res.Header.Get("Content-Encoding"))
		}
	}
	overlaid := func(why, want string) {
		t.Helper()
		res, body := frameAsk(t, bound, "GET", "/theme.css", cookie, "Accept-Encoding", "gzip")
		if res.StatusCode != 200 || res.Header.Get("Content-Encoding") != "" || string(body) != want || res.Header.Get("ETag") != etagOf(body) || !varies(res.Header, "Cookie") {
			t.Fatalf("%s: %d, Content-Encoding %q, %.60q; want the overlay's bytes as they are, %q", why, res.StatusCode, res.Header.Get("Content-Encoding"), body, want)
		}
	}
	compiled("no overlay yet, signed in", cookie)
	writeOverlay(t, dir, "theme.css", "/* the identity's theme */")
	compiled("an overlay, not signed in", "")
	overlaid("an overlay, signed in", "/* the identity's theme */")
	writeOverlay(t, dir, "theme.css", "/* the identity's theme, edited */")
	overlaid("the overlay edited", "/* the identity's theme, edited */")
	compiled("the overlay edited, not signed in", "")
	if err := os.Remove(filepath.Join(dir, "theme.css")); err != nil {
		t.Fatal(err)
	}
	compiled("the overlay removed, signed in", cookie)
}
