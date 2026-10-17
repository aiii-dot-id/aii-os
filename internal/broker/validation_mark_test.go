package broker

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

func TestUnderTheValidationMarkNothingOfTheIdentityIsAnswered(t *testing.T) {
	st := newStore(t)
	h := newHost(t, st, Config{Grants: map[string]Grant{"org.example.checked": {Memory: true, KV: true, Interactions: true, Files: true, Embeddings: true}}})
	b := h.Bind("org.example.checked", packagefmt.TierT1, []string{"ring4.memory", "ring4.kv", capInteractionRead, capFSSandbox, capModelEmbeddings})
	b.SetFiles(t.TempDir(), 0)
	b.SetSettings(func() map[string]any { return map[string]any{"model": "the operator's"} })
	b.SetDeclaredSettings(func() map[string]any { return map[string]any{"model": "the package's"} })

	calls := map[string]string{
		"memory.recall":     memParams(opMemoryRecall, `{"query":"harbor"}`),
		"memory.remember":   memParams("memory.remember", `{"text":"a memory"}`),
		"kv.get":            `{"operation":"kv.get","target":{"key":"k"}}`,
		"kv.put":            `{"operation":"kv.put","target":{"key":"k"},"arguments":{"value":"v"}}`,
		"interaction.query": memParams(opInteractionQuery, `{"version":1}`),
		"fs.read":           `{"operation":"fs.read","target":{"root":"private","path":"a.txt"}}`,
		"fs.write":          `{"operation":"fs.write","target":{"root":"private","path":"a.txt"},"arguments":{"content":"x"}}`,
		"embeddings.create": `{"operation":"embeddings.create","arguments":{"input":["a"]}}`,
		"http.get":          `{"operation":"http.get","target":{"url":"https://api.example.test/"}}`,
	}

	// The control half: the grant answers an ordinary call.
	b.BeginOperation(OperationScope{Operation: "lookup", Effects: EffectsReadInternal})
	if out := dispatch(t, b, calls["memory.recall"]); out["status"] == nil || strings.Contains(string(out["status"]), "denied") {
		t.Fatalf("an ordinary call under the grant must be answered: %v", out)
	}
	if out := dispatch(t, b, `{"operation":"settings.get"}`); !strings.Contains(string(out["operation_result"]), "the operator's") {
		t.Fatalf("an ordinary call reads the operator's settings: %s", out["operation_result"])
	}
	b.EndOperation()

	b.BeginOperation(OperationScope{Operation: "lookup", Effects: EffectsReadInternal, Validation: true})
	defer b.EndOperation()
	for name, params := range calls {
		t.Run(name, func(t *testing.T) {
			wantErrorReason(t, dispatch(t, b, params), reasonValidationDeny)
		})
	}
	out := dispatch(t, b, `{"operation":"settings.get"}`)
	if got := string(out["operation_result"]); !strings.Contains(got, "the package's") || strings.Contains(got, "the operator's") {
		t.Fatalf("a check reads the package's own settings, never the operator's: %s", got)
	}
	reply, err := b.Dispatch(context.Background(), methodInvokeCall, []byte(calls["kv.put"]))
	if err != nil || !strings.Contains(string(reply), "publisher's checks") {
		t.Fatalf("the refusal names the check: %s %v", reply, err)
	}
}
