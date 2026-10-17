package main

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

func TestDashboardLinkPrintsALinkThatSignsABrowserInOnce(t *testing.T) {
	const token = "a-token-of-honest-length-for-this-test-0123456789"
	s := dashboard.New("127.0.0.1", 0, &dashboard.WSHandler{})
	s.SetAccessToken(true, token)
	bound, err := s.Start("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(t.Context()) })
	_, portText, _ := net.SplitHostPort(bound)
	port, _ := strconv.Atoi(portText)
	cfg := filepath.Join(t.TempDir(), "config.json")
	write := func(offered string) {
		t.Helper()
		body := fmt.Sprintf(`{"dashboard":{"port":%d,"require_token":true,"access_token":%q}}`, port, offered)
		if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) (int, string, string) {
		t.Helper()
		var out, errs bytes.Buffer
		code := runDashboardLink(append([]string{"-config", cfg}, args...), &out, &errs)
		return code, strings.TrimSpace(out.String()), errs.String()
	}
	signIn := func(code string) (int, bool) {
		t.Helper()
		res, err := (&http.Client{Timeout: 10 * time.Second}).Post("http://"+bound+"/auth/token", "text/plain", strings.NewReader(code))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		for _, c := range res.Cookies() {
			if c.Name == "aii_token_"+portText && c.Value != "" {
				return res.StatusCode, true
			}
		}
		return res.StatusCode, false
	}

	write(token)
	code, out, errs := run()
	prefix := "http://" + bound + "/auth/link#"
	if code != 0 || !strings.HasPrefix(out, prefix) || strings.Contains(out+errs, token) {
		t.Fatalf("the verb did not print a sign-in link, or printed the token: %d %q %q", code, out, errs)
	}
	once, err := url.PathUnescape(strings.TrimPrefix(out, prefix))
	if err != nil {
		t.Fatal(err)
	}
	if status, cookie := signIn(once); status != http.StatusNoContent || !cookie {
		t.Fatalf("the link did not sign a browser in: %d", status)
	}
	if status, _ := signIn(once); status != http.StatusUnauthorized {
		t.Fatalf("the link signed a browser in twice: %d", status)
	}

	write("not-the-token-the-dashboard-asks-for")
	if code, out, errs := run(); code != 1 || out != "" || !strings.Contains(errs, "made no sign-in link (401)") {
		t.Fatalf("a refused token: %d %q %q", code, out, errs)
	}

	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	nobody := free.Addr().(*net.TCPAddr).Port
	free.Close()
	write(token)
	if code, out, errs := run("-port", strconv.Itoa(nobody)); code != 1 || out != "" || !strings.Contains(errs, "did not answer") {
		t.Fatalf("a port nobody answers on: %d %q %q", code, out, errs)
	}

	s.SetAccessToken(false, "")
	if code, out, _ := run(); code != 0 || out != "http://"+bound {
		t.Fatalf("a dashboard that asks for no token: %d %q, want its own address", code, out)
	}
}
