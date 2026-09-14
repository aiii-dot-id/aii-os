package dashboard

import (
	"context"
	"io"
	"io/fs"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
// .
func TestStaticServingModuleLayout(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())

	get := func(urlPath string) (int, string, []byte) {
		t.Helper()
		resp, err := testClient.Get("https://" + addr + urlPath)
		if err != nil {
			t.Fatalf("GET %s: %v", urlPath, err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("GET %s read: %v", urlPath, err)
		}
		return resp.StatusCode, resp.Header.Get("Content-Type"), body
	}

	wantType := map[string]string{
		".html": "text/html; charset=utf-8",
		".js":   "text/javascript; charset=utf-8",
		".css":  "text/css; charset=utf-8",
	}

	// .
	// .
	for _, p := range []string{"/", "/index.html"} {
		code, ctype, body := get(p)
		if code != 200 || ctype != wantType[".html"] {
			t.Fatalf("GET %s = %d %q, want 200 %q", p, code, ctype, wantType[".html"])
		}
		for _, ref := range []string{
			`<script type="module" src="./app.js"></script>`,
			`<link rel="stylesheet" href="./theme.css">`,
			`<link rel="stylesheet" href="./layout.css">`,
		} {
			if !strings.Contains(string(body), ref) {
				t.Errorf("GET %s: shell does not reference %s", p, ref)
			}
		}
	}

	// .
	for _, p := range []string{"/app.js", "/ws.js", "/views/chat.js", "/theme.css"} {
		ext := p[strings.LastIndex(p, "."):]
		code, ctype, body := get(p)
		if code != 200 || ctype != wantType[ext] || len(body) == 0 {
			t.Errorf("GET %s = %d %q (%d bytes), want 200 %q non-empty", p, code, ctype, len(body), wantType[ext])
		}
	}

	// .
	// .
	// .
	err = fs.WalkDir(staticFS, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		urlPath := strings.TrimPrefix(p, "static")
		dot := strings.LastIndex(p, ".")
		if dot < 0 {
			t.Errorf("embedded file %s has no extension — the handler cannot type it", p)
			return nil
		}
		want, ok := wantType[p[dot:]]
		if !ok {
			t.Errorf("embedded file %s has extension %q outside the servable set", p, p[dot:])
			return nil
		}
		code, ctype, body := get(urlPath)
		if code != 200 || ctype != want || len(body) == 0 {
			t.Errorf("GET %s = %d %q (%d bytes), want 200 %q non-empty", urlPath, code, ctype, len(body), want)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// .
	// .
	// .
	for _, p := range []string{"/nope.js", "/nope.css", "/views", "/views/", "/server.go", "/static/../server.go"} {
		if code, _, _ := get(p); code != 404 {
			t.Errorf("GET %s = %d, want 404", p, code)
		}
	}
}
