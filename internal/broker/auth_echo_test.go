package broker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// readable is everything a plugin can read in a reply or a receipt:
// every key and string decoded (so `>` is `>` again), and every
// number as it was written.
func readable(raw []byte) string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return string(raw)
	}
	var sb strings.Builder
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, e := range x {
				sb.WriteString(k + "\n")
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		default:
			fmt.Fprintf(&sb, "%v\n", x)
		}
	}
	walk(v)
	return string(raw) + "\n" + sb.String()
}

// noCredential fails when a form of the credential reaches the plugin
// anywhere in a reply, spelled any way JSON spells it.
func noCredential(t *testing.T, m map[string]json.RawMessage, forms ...string) {
	t.Helper()
	whole, _ := json.Marshal(m)
	text := readable(whole)
	for _, f := range forms {
		if strings.Contains(text, f) {
			t.Fatalf("the credential form %q reached the plugin:\n%s", f, whole)
		}
	}
}

// A remote that echoes the request's own credential hands the plugin
// nothing: in the body — raw, JSON-escaped, or as the service decoded
// it — in a Location, in a 4xx body, in the transport's error naming a
// refused redirect, in a stream head and in a stream read whole or a
// byte at a time, every form the broker sent (the bearer token; basic's
// user:password and its base64) comes back as the placeholder. The
// redacted JSON body is still the same JSON, the status and the error
// evidence stand, and a response that echoes nothing — even one that
// begins the credential and does not finish it — is untouched.
func TestTheRequestsOwnCredentialIsRedactedFromEveryEcho(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		_, cred, _ := strings.Cut(auth, " ")
		switch r.URL.Path {
		case "/body":
			decoded := []byte("null")
			if raw, err := base64.StdEncoding.DecodeString(cred); err == nil && strings.HasPrefix(auth, "Basic ") {
				decoded, _ = json.Marshal(string(raw)) // Go's encoder writes `>` as >
			}
			fmt.Fprintf(w, `{"escaped":"%s","raw":%q,"decoded":%s,"ordinary":"data","n":42}`,
				strings.ReplaceAll(auth, "/", `\/`), auth, decoded)
		case "/created":
			w.Header().Set("Location", "https://"+r.Host+"/credentials/"+cred)
			w.WriteHeader(http.StatusCreated)
		case "/denied":
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprintf(w, `{"error":"not accepted","token":%q}`, cred)
		case "/redirect":
			http.Redirect(w, r, "https://127.0.0.1:1/leak/"+cred, http.StatusFound)
		case "/stream":
			w.Header().Set("Location", "https://"+r.Host+"/s/"+cred)
			half := len(cred) / 2
			fmt.Fprint(w, "data: "+cred[:half])
			w.(http.Flusher).Flush()
			time.Sleep(20 * time.Millisecond)
			fmt.Fprint(w, cred[half:]+"\n\n")
		case "/prefix":
			fmt.Fprint(w, "data: "+cred[:len(cred)-1]+"!")
		default:
			fmt.Fprint(w, `{"ordinary":"data","n":42}`)
		}
	}))
	defer ts.Close()
	host, port := tsHostPort(t, ts)
	hostPort := fmt.Sprintf("%s:%d", host, port)
	guard := func(_ context.Context, raw string) error {
		if strings.HasPrefix(raw, ts.URL) {
			return nil
		}
		return firewall.ErrEgressBlocked // hermetic: nothing off the fixture is resolved or dialled
	}

	for _, tc := range []struct{ scheme, prefix, secret string }{
		{"bearer", "Bearer ", "fixture/bearer+token="},
		{"basic", "Basic ", "fixture-user:p>ss?w0rd"},
	} {
		t.Run(tc.scheme, func(t *testing.T) {
			wire := tc.secret
			forms := []string{tc.secret}
			if tc.scheme == "basic" {
				wire = base64.StdEncoding.EncodeToString([]byte(tc.secret))
				forms = append(forms, wire)
			}
			if !strings.Contains(wire, "/") {
				t.Fatalf("fixture %q must carry a '/' for the escaped echo to differ from the raw one", wire)
			}
			t.Setenv("AII_TEST_ECHO_SECRET", tc.secret)
			st := newStore(t)
			h := newHost(t, st, Config{
				Grants:       map[string]Grant{"p": {Hosts: []string{hostPort}, CredentialHandles: []string{"api"}}},
				AuthProfiles: map[string]AuthProfile{"api": {Scheme: tc.scheme, SecretEnv: "AII_TEST_ECHO_SECRET", Host: host, Port: port}},
				Guard:        guard, Transport: ts.Client().Transport,
			})
			b := h.Bind("p", packagefmt.TierT2, []string{"net.outbound:" + hostPort})
			type result struct {
				Status   int             `json:"http_status"`
				Body     json.RawMessage `json:"body"`
				Location string          `json:"location"`
			}
			call := func(path string) (map[string]json.RawMessage, result) {
				t.Helper()
				m := dispatch(t, b, netParams(ts.URL+path, `{"auth_profile":"api"}`))
				noCredential(t, m, forms...)
				var or result
				_ = json.Unmarshal(m["operation_result"], &or)
				return m, or
			}
			redacted := tc.prefix + CredentialPlaceholder

			// The body: every spelling redacted, still the same JSON.
			m, or := call("/body")
			wantResult(t, m, statusSucceeded, "")
			var body map[string]any
			if err := json.Unmarshal(or.Body, &body); err != nil {
				t.Fatalf("a redacted JSON body is still JSON: %v\n%s", err, or.Body)
			}
			if body["escaped"] != redacted || body["raw"] != redacted || body["ordinary"] != "data" || body["n"] != 42.0 {
				t.Fatalf("each echo is the placeholder and the rest is as sent: %s", or.Body)
			}
			if tc.scheme == "basic" && body["decoded"] != CredentialPlaceholder {
				t.Fatalf("user:password as the service decoded it is redacted: %s", or.Body)
			}

			// A Location.
			m, or = call("/created")
			wantResult(t, m, statusSucceeded, "")
			if or.Status != http.StatusCreated || or.Location != "https://"+hostPort+"/credentials/"+CredentialPlaceholder {
				t.Fatalf("the Location keeps its shape with the placeholder: %d %q", or.Status, or.Location)
			}

			// A remote failure: the status and the reason stand.
			m, or = call("/denied")
			rec := wantResult(t, m, statusFailed, reasonNetRemoteFailed)
			if or.Status != http.StatusForbidden || receiptField(t, rec, "effect") != EffectPerformed ||
				string(or.Body) != `{"error":"not accepted","token":"`+CredentialPlaceholder+`"}` {
				t.Fatalf("the failure's evidence is kept, the echo is not: %d %s", or.Status, or.Body)
			}

			// The transport's error names the refused hop's Location. The
			// first hop answered, so it is a remote outcome, not a policy
			// denial (C2: POLICY_DENY means nothing was sent).
			m, _ = call("/redirect")
			rec = wantResult(t, m, statusFailed, reasonNetRemoteFailed)
			if d := receiptField(t, rec, "detail"); !strings.Contains(d, "transport:") || !strings.Contains(d, "/leak/"+CredentialPlaceholder) {
				t.Fatalf("the refused hop is named with the placeholder: %q", d)
			}

			// A stream: the head, and the body read whole and a byte at a time.
			for _, args := range []string{"", `{"max_bytes":1}`} {
				id, head := openStream(t, b, "http.get", ts.URL+"/stream", `"auth_profile":"api"`)
				noCredential(t, head, forms...)
				if !strings.Contains(string(head["operation_result"]), `/s/`+CredentialPlaceholder) {
					t.Fatalf("the stream head's Location is redacted: %s", head["operation_result"])
				}
				if got := readStream(t, b, id, args, forms); got != "data: "+CredentialPlaceholder+"\n\n" {
					t.Fatalf("read %q: the stream carries the placeholder where the credential was: %q", args, got)
				}
			}

			// What echoes nothing is untouched: a body byte for byte, and
			// a stream that begins the credential without finishing it.
			m, or = call("/plain")
			wantResult(t, m, statusSucceeded, "")
			if string(or.Body) != `{"ordinary":"data","n":42}` {
				t.Fatalf("an ordinary body is as sent: %s", or.Body)
			}
			id, _ := openStream(t, b, "http.get", ts.URL+"/prefix", `"auth_profile":"api"`)
			if got := readStream(t, b, id, `{"max_bytes":1}`, forms); got != "data: "+wire[:len(wire)-1]+"!" {
				t.Fatalf("a tail held back is handed over once it is not the credential: %q", got)
			}

			recs, err := st.PluginReceipts("p")
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range recs {
				for _, f := range forms {
					if strings.Contains(readable(r.ReceiptJSON), f) {
						t.Fatalf("a stored receipt carries %q: %s", f, r.ReceiptJSON)
					}
				}
			}
		})
	}
}

// readStream pulls a stream to its end and returns what the plugin was
// handed; every chunk says truly how many bytes it carries, and no
// reply carries a form.
func readStream(t *testing.T, b *Binding, id, args string, forms []string) string {
	t.Helper()
	var text string
	for i := 0; i < 500; i++ {
		c, m := readChunk(t, b, id, args)
		noCredential(t, m, forms...)
		data := mustB64(t, c.DataB64)
		if c.Bytes != len(data) {
			t.Fatalf("chunk %d says %d bytes and carries %d", c.Seq, c.Bytes, len(data))
		}
		text += string(data)
		if c.Done {
			for _, f := range forms {
				if strings.Contains(text, f) {
					t.Fatalf("the stream handed over %q across its chunks: %q", f, text)
				}
			}
			return text
		}
	}
	t.Fatal("the stream never ended")
	return ""
}

// The scrub at the byte level. A JSON body keeps its shape and every
// byte no form was in: an escaped echo (`\/`, `>`) is still the
// value, a key is a string like any other, and a string that escapes
// but carries no form is left as written. A form outside any string is
// replaced where it stands, and what is left is no longer JSON, so it
// rides as text. Fed a byte at a time, a stream hands over exactly what
// the whole would — no piece of a form is out before the read that
// decides it.
func TestACredentialEchoIsScrubbedWhateverItsJSONSpellingAndSplit(t *testing.T) {
	var e credentialEcho
	e.add("1234567890", "fix/ture>tok", "1234567890", "")
	if len(e) != 2 || e[0] != "fix/ture>tok" {
		t.Fatalf("forms are distinct, non-empty, longest first: %q", e)
	}
	for _, tc := range []struct {
		in, want string
		json     bool
	}{
		{`{"a":"fix\/ture>tok","b":[1,2],"c":"plain"}`, `{"a":"{credential}","b":[1,2],"c":"plain"}`, true},
		{`{"fix/ture>tok":true}`, `{"{credential}":true}`, true},
		{`{"ordinary":"da\/ta","n":42}`, `{"ordinary":"da\/ta","n":42}`, true},
		{`not json: fix/ture>tok`, `not json: {credential}`, false},
		{`{"n":1234567890}`, `{"n":{credential}}`, false},
	} {
		got := e.body([]byte(tc.in))
		if string(got) != tc.want || json.Valid(got) != tc.json {
			t.Fatalf("body(%s) = %s (JSON %v), want %s (JSON %v)", tc.in, got, json.Valid(got), tc.want, tc.json)
		}
	}

	whole := "x fix/ture>tok y fix/ture>to! 123456789 1234567890"
	want := e.scrub(whole)
	if want != "x {credential} y fix/ture>to! 123456789 {credential}" {
		t.Fatalf("whole: %q", want)
	}
	s := &httpStream{echo: e}
	var got string
	for i := 0; i < len(whole); i++ {
		got += string(s.pass([]byte{whole[i]}, i == len(whole)-1))
	}
	if got != want {
		t.Fatalf("a byte at a time: %q, want %q", got, want)
	}
	if out := (&httpStream{}).pass([]byte("as sent"), false); string(out) != "as sent" {
		t.Fatalf("a stream with no credential passes as sent: %q", out)
	}
}
