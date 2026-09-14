package broker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

type fakeEmbedder struct {
	calls [][]string
	fail  error
}

func (f *fakeEmbedder) Embed(_ context.Context, inputs []string) (string, [][]float32, error) {
	f.calls = append(f.calls, inputs)
	if f.fail != nil {
		return "fake-embed", nil, f.fail
	}
	out := make([][]float32, len(inputs))
	for i, s := range inputs {
		out[i] = []float32{float32(len(s)), 0.5, -1}
	}
	return "fake-embed", out, nil
}

func embedParams(inputs ...string) string {
	q := make([]string, len(inputs))
	for i, s := range inputs {
		q[i] = fmt.Sprintf("%q", s)
	}
	return `{"operation":"embeddings.create","arguments":{"input":[` + strings.Join(q, ",") + `]}}`
}

// .
// .
// .
// .
func TestEmbeddingsRunUnderTheRingsAndThroughTheSeam(t *testing.T) {
	st := newStore(t)
	fe := &fakeEmbedder{}
	h := newHost(t, st, Config{Grants: map[string]Grant{"p": {Embeddings: true}}, Embed: fe})
	envelope := []string{"model.embeddings"}

	// .
	wantErrorReason(t, dispatch(t, h.Bind("p", packagefmt.TierT0, envelope), embedParams("a")), reasonTierDenied)
	// .
	wantErrorReason(t, dispatch(t, h.Bind("p", packagefmt.TierT1, []string{"ring4.kv"}), embedParams("a")), reasonNotInEnvelope)
	// .
	wantErrorReason(t, dispatch(t, h.Bind("q", packagefmt.TierT1, envelope), embedParams("a")), reasonPolicyDeny)

	b := h.Bind("p", packagefmt.TierT1, envelope)
	rec := wantResult(t, dispatch(t, b, embedParams("hello", "world!")), statusSucceeded, "")
	if string(rec["target"]) != `"fake-embed"` {
		t.Fatalf("the receipt names the model that answered, got %s", rec["target"])
	}
	m := dispatch(t, b, embedParams("hello", "world!"))
	if !strings.Contains(string(m["operation_result"]), `"dimensions":3`) || !strings.Contains(string(m["operation_result"]), `"vectors":[[5,0.5,-1],[6,0.5,-1]]`) {
		t.Fatalf("the vectors come back in order with their dimension, got %s", m["operation_result"])
	}
	if len(fe.calls) != 2 || fe.calls[0][1] != "world!" {
		t.Fatalf("the seam saw the inputs in order: %v", fe.calls)
	}
}

// .
// .
// .
func TestEmbeddingsBoundsSAFEAndSeam(t *testing.T) {
	st := newStore(t)
	fe := &fakeEmbedder{}
	safe := false
	h := newHost(t, st, Config{Grants: map[string]Grant{"p": {Embeddings: true}}, Embed: fe, InSAFE: func() bool { return safe }})
	b := h.Bind("p", packagefmt.TierT1, []string{"model.embeddings"})

	wantResult(t, dispatch(t, b, `{"operation":"embeddings.create","arguments":{}}`), statusDenied, reasonArgumentInvalid)
	many := make([]string, maxEmbedInputs+1)
	for i := range many {
		many[i] = "x"
	}
	wantResult(t, dispatch(t, b, embedParams(many...)), statusDenied, reasonArgumentInvalid)
	wantResult(t, dispatch(t, b, embedParams(strings.Repeat("y", maxEmbedInputBytes+1))), statusDenied, reasonArgumentInvalid)
	if len(fe.calls) != 0 {
		t.Fatal("nothing leaves before the bounds pass")
	}

	fe.fail = errors.New("connection refused")
	rec := wantResult(t, dispatch(t, b, embedParams("a")), statusFailed, reasonNetRemoteFailed)
	if string(rec["target"]) != `"fake-embed"` {
		t.Fatalf("a failed call still names the model, got %s", rec["target"])
	}
	fe.fail = nil

	safe = true
	wantErrorReason(t, dispatch(t, b, embedParams("a")), reasonPolicyDeny)
	safe = false

	none := newHost(t, st, Config{Grants: map[string]Grant{"p": {Embeddings: true}}})
	wantErrorReason(t, dispatch(t, none.Bind("p", packagefmt.TierT1, []string{"model.embeddings"}), embedParams("a")), reasonPolicyDeny)
}

// .
// .
func TestKVListIsABoundedPrefixedRead(t *testing.T) {
	st := newStore(t)
	safe := false
	h := newHost(t, st, Config{Grants: map[string]Grant{"p": {KV: true}}, InSAFE: func() bool { return safe }})
	b := h.Bind("p", packagefmt.TierT1, []string{"ring4.kv"})
	for _, k := range []string{"mem:b", "mem:a", "note:1"} {
		wantResult(t, dispatch(t, b, `{"operation":"kv.put","target":{"key":"`+k+`"},"arguments":{"value":"v"}}`), statusSucceeded, "")
	}
	m := dispatch(t, b, `{"operation":"kv.list","target":{"prefix":"mem:"}}`)
	wantResult(t, m, statusSucceeded, "")
	if !strings.Contains(string(m["operation_result"]), `"keys":["mem:a","mem:b"]`) || !strings.Contains(string(m["operation_result"]), `"truncated":false`) {
		t.Fatalf("kv.list returns the prefixed keys sorted, got %s", m["operation_result"])
	}
	m = dispatch(t, b, `{"operation":"kv.list","target":{"prefix":""},"arguments":{"limit":2}}`)
	if !strings.Contains(string(m["operation_result"]), `"truncated":true`) {
		t.Fatalf("a limit below the count says so, got %s", m["operation_result"])
	}
	safe = true
	wantResult(t, dispatch(t, b, `{"operation":"kv.list","target":{"prefix":"mem:"}}`), statusSucceeded, "")
	wantErrorReason(t, dispatch(t, b, `{"operation":"kv.put","target":{"key":"mem:c"},"arguments":{"value":"v"}}`), reasonPolicyDeny)
}
