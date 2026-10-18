package broker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// An exchange open when SAFE begins is refused its chunks and its close,
// like every effect: the plugin cannot read on, and cannot end it either. The
// refusal is the host's answer, not a lost stream: when SAFE ends the same
// stream id still closes.
func TestAnOpenExchangeIsRefusedItsChunksAndItsCloseInSAFE(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hello")
	}))
	defer ts.Close()
	host, port := tsHostPort(t, ts)
	hostPort := fmt.Sprintf("%s:%d", host, port)
	safe := false
	h := newHost(t, newStore(t), Config{Grants: map[string]Grant{"p": {Hosts: []string{hostPort}}}, Guard: guardFor(ts), Transport: ts.Client().Transport, InSAFE: func() bool { return safe }})
	b := h.Bind("p", packagefmt.TierT1, []string{"net.outbound:" + hostPort})
	id, _ := openStream(t, b, "http.get", ts.URL+"/x", "")
	if id == "" {
		t.Fatal("no stream opened")
	}

	safe = true
	for _, op := range []string{"http.read", "http.close"} {
		m := dispatch(t, b, fmt.Sprintf(`{"operation":%q,"target":{"stream_id":%q}}`, op, id))
		wantErrorReason(t, m, reasonPolicyDeny)
		if message := string(m["message"]); !strings.Contains(message, "in SAFE") || !strings.Contains(message, op) {
			t.Fatalf("%s is refused in SAFE, by name: %s", op, message)
		}
	}

	// The control: SAFE is what refused. Lifted, the same stream closes.
	safe = false
	wantResult(t, dispatch(t, b, fmt.Sprintf(`{"operation":"http.close","target":{"stream_id":%q}}`, id)), statusSucceeded, "")
}

// tools.publish and tools.withdraw are refused in SAFE: a plugin does not
// change what the identity may call while the identity cannot trust its
// record. Nothing is published, and lifted the same call lands.
func TestPublishingAndWithdrawingToolsAreRefusedInSAFE(t *testing.T) {
	safe := false
	h := newHost(t, newStore(t), Config{Grants: map[string]Grant{"p": {Tools: true}}, InSAFE: func() bool { return safe }})
	pub := &fakePublisher{}
	b := h.Bind("p", packagefmt.TierT1, []string{"tools.publish"})
	b.SetPublisher(pub)
	spec := `{"name":"server.list","summary":"List files on the server","input":{"type":"object","properties":{"path":{"type":"string"}}},"effects":"read.external","capabilities":[]}`
	publish := `{"operation":"tools.publish","arguments":` + spec + `}`
	withdraw := `{"operation":"tools.withdraw","target":{"name":"server.list"}}`

	safe = true
	for _, params := range []string{publish, withdraw} {
		reply, err := b.Dispatch(context.Background(), methodInvokeCall, []byte(params))
		if err != nil || !strings.Contains(string(reply), "in SAFE") || !strings.Contains(string(reply), reasonPolicyDeny) {
			t.Fatalf("refused in SAFE, by the host's own words: %s %v", reply, err)
		}
	}
	if len(pub.published) != 0 || len(pub.withdrawn) != 0 {
		t.Fatalf("SAFE refused, so nothing reached the publisher: %+v %+v", pub.published, pub.withdrawn)
	}

	safe = false
	wantResult(t, dispatch(t, b, publish), statusSucceeded, "")
	if len(pub.published) != 1 {
		t.Fatalf("the control: lifted, the same publish lands: %+v", pub.published)
	}
}
