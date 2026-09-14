package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

func verbParams(op, rawURL, extraArgs string) string {
	args := "{}"
	if extraArgs != "" {
		args = extraArgs
	}
	return fmt.Sprintf(`{"operation":%q,"target":{"url":%q},"arguments":%s}`, op, rawURL, args)
}

func receiptField(t *testing.T, rec map[string]json.RawMessage, key string) string {
	t.Helper()
	var s string
	_ = json.Unmarshal(rec[key], &s)
	return s
}

// .
// .
// .
// .
// .
func TestMutationVerbsReachTheServerAndTheirArgumentsAreClosed(t *testing.T) {
	type seen struct{ method, body, ctype, accept string }
	var last atomic.Pointer[seen]
	var hits atomic.Int32
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		body, _ := io.ReadAll(r.Body)
		last.Store(&seen{method: r.Method, body: string(body), ctype: r.Header.Get("Content-Type"), accept: r.Header.Get("Accept")})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":7}`)
	}))
	defer ts.Close()
	host, port := tsHostPort(t, ts)
	hostPort := fmt.Sprintf("%s:%d", host, port)
	st := newStore(t)
	h := newHost(t, st, Config{Grants: map[string]Grant{"p": {Hosts: []string{hostPort}}}, Guard: guardFor(ts), Transport: ts.Client().Transport, MaxRequestBytes: 64})
	b := h.Bind("p", packagefmt.TierT1, []string{"net.outbound:" + hostPort})

	for _, tc := range []struct{ op, method string }{{"http.post", "POST"}, {"http.put", "PUT"}, {"http.patch", "PATCH"}} {
		m := dispatch(t, b, verbParams(tc.op, ts.URL+"/issues", `{"body":"{\"title\":\"x\"}","content_type":"application/json","headers":{"Accept":"application/vnd.github+json"}}`))
		rec := wantResult(t, m, statusSucceeded, "")
		got := last.Load()
		if got.method != tc.method || got.body != `{"title":"x"}` || got.ctype != "application/json" || got.accept != "application/vnd.github+json" {
			t.Fatalf("%s: server saw %+v", tc.op, got)
		}
		if receiptField(t, rec, "method") != tc.method || receiptField(t, rec, "effect") != EffectPerformed {
			t.Fatalf("%s: receipt method/effect = %s/%s", tc.op, rec["method"], rec["effect"])
		}
		var or map[string]interface{}
		_ = json.Unmarshal(m["operation_result"], &or)
		if or["http_status"] != 201.0 {
			t.Fatalf("%s: operation_result %v", tc.op, or)
		}
	}
	m := dispatch(t, b, verbParams("http.delete", ts.URL+"/issues/7", ""))
	wantResult(t, m, statusSucceeded, "")
	if last.Load().method != "DELETE" {
		t.Fatalf("delete: server saw %+v", last.Load())
	}
	before := hits.Load()
	for name, params := range map[string]string{
		"a body on a read":        verbParams("http.get", ts.URL, `{"body":"x","content_type":"text/plain"}`),
		"a body without its type": verbParams("http.post", ts.URL, `{"body":"x"}`),
		"a type without a body":   verbParams("http.post", ts.URL, `{"content_type":"text/plain"}`),
		"a forbidden header":      verbParams("http.post", ts.URL, `{"body":"x","content_type":"text/plain","headers":{"Authorization":"Bearer leak"}}`),
		"a cookie":                verbParams("http.post", ts.URL, `{"body":"x","content_type":"text/plain","headers":{"Cookie":"a=b"}}`),
		"content-type as header":  verbParams("http.post", ts.URL, `{"body":"x","content_type":"text/plain","headers":{"Content-Type":"text/html"}}`),
		"a bad header name":       verbParams("http.post", ts.URL, `{"body":"x","content_type":"text/plain","headers":{"X Space":"v"}}`),
		"a CRLF header value":     verbParams("http.post", ts.URL, `{"body":"x","content_type":"text/plain","headers":{"X-A":"v\r\nInjected: y"}}`),
		"a body over the ceiling": verbParams("http.post", ts.URL, `{"body":"`+strings.Repeat("x", 65)+`","content_type":"text/plain"}`),
		"an unknown argument":     verbParams("http.post", ts.URL, `{"retry":true}`),
		"a non-bool idempotent":   verbParams("http.post", ts.URL, `{"idempotent":"yes"}`),
	} {
		m := dispatch(t, b, params)
		var got struct {
			Status string `json:"status"`
		}
		raw, _ := json.Marshal(m)
		_ = json.Unmarshal(raw, &got)
		if got.Status != statusDenied {
			t.Fatalf("%s must be refused before sending, got %s", name, raw)
		}
	}
	hdrs := map[string]string{}
	for i := 0; i < MaxRequestHeaders+1; i++ {
		hdrs[fmt.Sprintf("X-H%d", i)] = "v"
	}
	hraw, _ := json.Marshal(hdrs)
	wantResult(t, dispatch(t, b, verbParams("http.post", ts.URL, `{"body":"x","content_type":"text/plain","headers":`+string(hraw)+`}`)), statusDenied, reasonNetHeaderInvalid)
	if hits.Load() != before {
		t.Fatal("a refused call reached the server")
	}
}

// .
// .
func TestAMutationDoesNotFollowARedirectUnlessAsked(t *testing.T) {
	var methods []string
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/moved" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		fmt.Fprint(w, `{"final":true}`)
	}))
	defer ts.Close()
	host, port := tsHostPort(t, ts)
	hostPort := fmt.Sprintf("%s:%d", host, port)
	h := newHost(t, newStore(t), Config{Grants: map[string]Grant{"p": {Hosts: []string{hostPort}}}, Guard: guardFor(ts), Transport: ts.Client().Transport})
	b := h.Bind("p", packagefmt.TierT1, []string{"net.outbound:" + hostPort})

	m := dispatch(t, b, verbParams("http.post", ts.URL+"/moved", `{"body":"x","content_type":"text/plain"}`))
	rec := wantResult(t, m, statusSucceeded, "")
	var or map[string]interface{}
	_ = json.Unmarshal(m["operation_result"], &or)
	if or["http_status"] != 302.0 || or["location"] != "/final" || receiptField(t, rec, "effect") != EffectPerformed {
		t.Fatalf("a mutation's redirect is the answer: %v %s", or, rec["effect"])
	}
	if len(methods) != 1 || methods[0] != "POST /moved" {
		t.Fatalf("the redirect was followed: %v", methods)
	}
	methods = nil
	m = dispatch(t, b, verbParams("http.post", ts.URL+"/moved", `{"body":"x","content_type":"text/plain","follow_redirects":true}`))
	wantResult(t, m, statusSucceeded, "")
	if len(methods) != 2 || methods[1] != "GET /final" {
		t.Fatalf("asked, the redirect is followed (302 turns the method into GET, as browsers do): %v", methods)
	}
	methods = nil
	wantResult(t, dispatch(t, b, netParams(ts.URL+"/moved", "")), statusSucceeded, "")
	if len(methods) != 2 {
		t.Fatalf("a read follows by default: %v", methods)
	}
}

// .
// .
// .
// .
// .
func TestTheEffectIsNotPerformedUnknownOrRetriedByDeclaration(t *testing.T) {
	var attempts atomic.Int32
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		io.Copy(io.Discard, r.Body)
		if n == 1 || r.URL.Path == "/always-cut" {
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("no hijacker")
			}
			conn, _, _ := hj.Hijack()
			conn.Close()
			return
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer ts.Close()
	host, port := tsHostPort(t, ts)
	hostPort := fmt.Sprintf("%s:%d", host, port)

	// .
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadPort := l.Addr().(*net.TCPAddr).Port
	l.Close()
	deadURL := fmt.Sprintf("http://127.0.0.1:%d/x", deadPort)
	deadHostPort := fmt.Sprintf("127.0.0.1:%d", deadPort)
	guard := func(_ context.Context, rawURL string) error {
		if strings.HasPrefix(rawURL, ts.URL) || strings.HasPrefix(rawURL, deadURL) {
			return nil
		}
		return guardFor(ts)(context.Background(), rawURL)
	}
	h := newHost(t, newStore(t), Config{Grants: map[string]Grant{"p": {Hosts: []string{hostPort, deadHostPort}}}, Guard: guard, Transport: ts.Client().Transport})
	b := h.Bind("p", packagefmt.TierT1, []string{"net.outbound:" + hostPort, "net.outbound:" + deadHostPort})

	rec := wantResult(t, dispatch(t, b, verbParams("http.post", deadURL, `{"body":"x","content_type":"text/plain"}`)), statusFailed, reasonNetRemoteFailed)
	if receiptField(t, rec, "effect") != EffectNotPerformed {
		t.Fatalf("nothing written: effect %s", rec["effect"])
	}

	rec = wantResult(t, dispatch(t, b, verbParams("http.post", ts.URL+"/always-cut", `{"body":"x","content_type":"text/plain"}`)), statusFailed, reasonNetEffectUnknown)
	if receiptField(t, rec, "effect") != EffectUnknown {
		t.Fatalf("written, no response: effect %s", rec["effect"])
	}
	if attempts.Load() != 1 {
		t.Fatalf("a non-idempotent mutation is never retried: %d attempts", attempts.Load())
	}

	attempts.Store(0)
	m := dispatch(t, b, verbParams("http.post", ts.URL+"/once", `{"body":"x","content_type":"text/plain","idempotent":true}`))
	rec = wantResult(t, m, statusSucceeded, "")
	if attempts.Load() != 2 || receiptField(t, rec, "effect") != EffectPerformed {
		t.Fatalf("an idempotent mutation is retried once and the retry answers: %d attempts, effect %s", attempts.Load(), rec["effect"])
	}
	attempts.Store(0)
	wantResult(t, dispatch(t, b, verbParams("http.post", ts.URL+"/always-cut", `{"body":"x","content_type":"text/plain","idempotent":true}`)), statusFailed, reasonNetEffectUnknown)
	if attempts.Load() != 2 {
		t.Fatalf("once, not forever: %d attempts", attempts.Load())
	}
}

// .
// .
func TestAMutationNeedsAWriteClassAndAProvenTier(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "{}") }))
	defer ts.Close()
	host, port := tsHostPort(t, ts)
	hostPort := fmt.Sprintf("%s:%d", host, port)
	h := newHost(t, newStore(t), Config{Grants: map[string]Grant{"p": {Hosts: []string{hostPort}}}, Guard: guardFor(ts), Transport: ts.Client().Transport})
	b := h.Bind("p", packagefmt.TierT1, []string{"net.outbound:" + hostPort})
	b.BeginOperation(OperationScope{Operation: "lookup", Effects: EffectsReadExternal, Capabilities: []string{"net.outbound:" + hostPort}, Declared: true})
	reply, _ := b.Dispatch(context.Background(), "invoke-call", []byte(verbParams("http.post", ts.URL, `{"body":"x","content_type":"text/plain"}`)))
	if !strings.Contains(string(reply), "declares effects read.external") || !strings.Contains(string(reply), reasonPolicyDeny) {
		t.Fatalf("read.external cannot post: %s", reply)
	}
	wantResult(t, dispatch(t, b, netParams(ts.URL, "")), statusSucceeded, "")
	b.EndOperation()
	b.BeginOperation(OperationScope{Operation: "create", Effects: EffectsWriteExternal, Capabilities: []string{"net.outbound:" + hostPort}, Declared: true})
	wantResult(t, dispatch(t, b, verbParams("http.post", ts.URL, `{"body":"x","content_type":"text/plain"}`)), statusSucceeded, "")
	b.EndOperation()

	t0 := h.Bind("p", packagefmt.TierT0, []string{"net.outbound:" + hostPort})
	reply, _ = t0.Dispatch(context.Background(), "invoke-call", []byte(verbParams("http.delete", ts.URL, "")))
	if !strings.Contains(string(reply), reasonTierDenied) {
		t.Fatalf("T0 holds no network: %s", reply)
	}
}
