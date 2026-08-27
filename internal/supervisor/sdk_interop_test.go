package supervisor

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// The SDK's native lane, run by the REAL supervisor — the one place
// that proves the two repositories still agree on the wire.
//
// The plugin SDK is a separate module, so this cannot import it and
// cannot build its own fixture. It takes a path instead, the way the
// live substrate tests take AII_OAUTH_LIVE, and skips with the build
// command when it is absent. A framing change on either side breaks
// this and nothing else would.
//
//	cd ../aii-plugin-sdk && go build -o /tmp/native-skel ./examples/native-skel/
//	AII_SDK_NATIVE=/tmp/native-skel go test -run TestSDKNativePlugin ./internal/supervisor/
type kvDispatcher struct{ calls []string }

func (d *kvDispatcher) Dispatch(ctx context.Context, method string, params []byte) ([]byte, error) {
	d.calls = append(d.calls, method+" "+string(params))
	return []byte(`{"status":"succeeded","value":{"stored":true}}`), nil
}

func TestSDKNativePluginRunsUnderTheRealSupervisor(t *testing.T) {
	bin := os.Getenv("AII_SDK_NATIVE")
	if bin == "" {
		t.Skip("set AII_SDK_NATIVE to an SDK-built native plugin (see the comment above)")
	}
	_, lg := newCapture()
	d := &kvDispatcher{}
	s, err := Start(Spec{
		PluginID:  "org.example.native-skel",
		Argv:      []string{bin},
		Env:       []string{"SEV_PLUGIN_SOCKET=stdio:"},
		ReadyMark: "child-ready",
		Backoff:   Backoff{Initial: 20 * time.Millisecond, Max: 100 * time.Millisecond, MaxRestarts: 1},
		Log:       lg,
	}, d)
	if err != nil {
		t.Fatalf("the supervisor could not start an SDK-built native plugin: %v", err)
	}
	defer s.Close()

	// 1. A plain invoke.
	reply, err := s.Invoke(context.Background(),
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"invoke.call","params":{"operation":"describe","arguments":{}}}`))
	if err != nil {
		t.Fatalf("invoke failed: %v", err)
	}
	if !strings.Contains(string(reply), "native_t3_component") {
		t.Fatalf("the handler's value did not come back: %s", reply)
	}

	// 2. A nested hostcall: the plugin calls the host WHILE the host
	//    waits for the plugin. This is the deadlock case.
	reply, err = s.Invoke(context.Background(),
		[]byte(`{"jsonrpc":"2.0","id":2,"method":"invoke.call","params":{"operation":"remember","arguments":{"key":"model_path","value":"/models/stt"}}}`))
	if err != nil {
		t.Fatalf("nested hostcall invoke failed: %v", err)
	}
	var env map[string]json.RawMessage
	if err := json.Unmarshal(reply, &env); err != nil {
		t.Fatal(err)
	}
	if _, isErr := env["error"]; isErr {
		t.Fatalf("the nested hostcall errored: %s", reply)
	}
	if len(d.calls) == 0 {
		t.Fatal("the host never saw the plugin's upstream call")
	}
	if !strings.Contains(d.calls[0], "kv.put") {
		t.Fatalf("unexpected upstream call: %v", d.calls)
	}
	t.Logf("INTEROP OK: describe answered, upstream %s, nested reply delivered", strings.Fields(d.calls[0])[0])
}
