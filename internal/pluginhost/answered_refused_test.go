package pluginhost

import (
	"context"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker/wasmgen"
)

func TestAnAnswerThatSaysRefusedInsideASuccessIsAFailedCall(t *testing.T) {
	for _, c := range []struct {
		name, result, answer string
		failed               bool
	}{
		{"a store refused", `{"status":"succeeded","operation_result":{"stored":false,"refused":true,"reasonCode":"OPERATION_ARGUMENT_INVALID"}}`,
			`{"stored":false,"refused":true,"reasonCode":"OPERATION_ARGUMENT_INVALID"}`, true},
		{"no status is a success, and still says refused", `{"operation_result":{"refused":true}}`, `{"refused":true}`, true},
		{"a store kept", `{"status":"succeeded","operation_result":{"stored":true,"memories":[]}}`, `{"stored":true,"memories":[]}`, false},
		{"refused false", `{"status":"succeeded","operation_result":{"refused":false}}`, `{"refused":false}`, false},
		{"refused as a string", `{"status":"succeeded","operation_result":{"refused":"true"}}`, `{"refused":"true"}`, false},
		{"refused further down", `{"status":"succeeded","operation_result":{"detail":{"refused":true}}}`, `{"detail":{"refused":true}}`, false},
		{"an answer that is no object", `{"status":"succeeded","operation_result":["refused",true]}`, `["refused",true]`, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			const id = "org.example.refuser"
			guest := wasmgen.CannedResponder([]byte(`{"jsonrpc":"2.0","id":"h1","result":` + c.result + `}`))
			h, err := broker.New(broker.Config{Store: newBrokerStore(t), Grants: map[string]broker.Grant{id: {KV: true}}})
			if err != nil {
				t.Fatal(err)
			}
			reg := newRegistry(t)
			ap, err := Activate(context.Background(), writePkg(t, brokerPkgSpec(id, "0.1.0", []string{"store"}, guest, nil)), reg, &Options{Broker: h})
			if err != nil {
				t.Fatal(err)
			}
			defer ap.Deactivate(context.Background())

			res, err := reg.Execute(context.Background(), ap.ToolNames[0], nil)
			if err != nil {
				t.Fatal(err)
			}
			if res.Failed() != c.failed {
				t.Fatalf("the answer %s is recorded failed=%v, want failed=%v (%+v)", c.answer, res.Failed(), c.failed, res)
			}
			if res.Refused {
				t.Fatalf("the answer %s is recorded as a gate's refusal; the operation ran", c.answer)
			}
			if res.Error != "" || res.Text() != c.answer {
				t.Fatalf("the identity reads %q (error %q), want the plugin's answer as it gave it: %s", res.Text(), res.Error, c.answer)
			}
		})
	}
}
