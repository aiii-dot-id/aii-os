package pluginhost

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker/wasmgen"
)

func TestDescriptorLoopCannotHoldActivation(t *testing.T) {
	const id = "org.example.looping-descriptor"
	account := []byte(`[{"id":"ping","summary":"answers"}]`)
	for _, supervised := range []bool{false, true} {
		name := "in-process"
		if supervised {
			name = "supervised"
		}
		t.Run(name, func(t *testing.T) {
			opts := &Options{ReadyTimeout: map[string]time.Duration{id: time.Second}}
			if supervised {
				opts.WorkerBinary = workerBin
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			pkg := describingPkgWith(t, id, account, wasmgen.LoopingDescriber())
			reg := newRegistry(t)
			done := make(chan error, 1)
			go func() {
				ap, err := Activate(ctx, pkg, reg, opts)
				if ap != nil {
					_ = ap.Deactivate(ctx)
				}
				done <- err
			}()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("looping descriptor activated")
				}
				var mismatch *DescriptorMismatchError
				if errors.As(err, &mismatch) && !mismatch.Unasked {
					t.Fatalf("deadline called a compared mismatch: %v", err)
				}
			case <-time.After(5 * time.Second):
				cancel()
				<-done
				t.Fatal("descriptor ignored the host startup allowance; only caller cancellation released activation")
			}

			corrected := *opts
			corrected.ReadyTimeout = map[string]time.Duration{id: 2 * time.Minute}
			ap, err := Activate(t.Context(), describingPkg(t, id, account, account), reg, &corrected)
			if err != nil {
				t.Fatal(err)
			}
			if err := ap.Deactivate(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWASMInitializationSharesTheStartupDeadline(t *testing.T) {
	const id = "org.example.looping-initializer"
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	pkg := describingPkgWith(t, id, []byte(`[]`), wasmgen.LoopingInitializer())
	done := make(chan error, 1)
	reg := newRegistry(t)
	go func() {
		_, err := Activate(ctx, pkg, reg, &Options{ReadyTimeout: map[string]time.Duration{id: 50 * time.Millisecond}})
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("initialization deadline cause lost: %v", err)
		}
	case <-time.After(5 * time.Second):
		cancel()
		<-done
		t.Fatal("initializer escaped startup deadline")
	}
}

func TestDescriptorProofRunsInWorkerBeforeReadiness(t *testing.T) {
	account := []byte(`[{"id":"ping","summary":"answers","effects":"read.internal"}]`)
	digest, err := pluginworker.DescriptorDigest(account)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name               string
		guest              []byte
		wantError, unasked bool
	}{
		{"match", wasmgen.DescribingResponder(account), false, false},
		{"equivalent", wasmgen.DescribingResponder([]byte(`[{"effects":"read.internal","summary":"answers","id":"ping"}]`)), false, false},
		{"absent", wasmgen.Responder(), false, false},
		{"mismatch", wasmgen.DescribingResponder([]byte(`[]`)), true, false},
		{"malformed", wasmgen.DescribingResponder([]byte(`broken JSON`)), true, false},
		{"trap", wasmgen.TrappingDescriber(), true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capture := logsink.CaptureForTest(t)
			reg := newRegistry(t)
			before := len(reg.Names())
			ap, err := Activate(t.Context(), describingPkgWith(t, "org.example.proof", account, tc.guest), reg,
				supervisedOpts(Options{}))
			if ap != nil {
				defer ap.Deactivate(t.Context())
			}
			if tc.wantError {
				var mismatch *DescriptorMismatchError
				if !errors.As(err, &mismatch) || mismatch.Unasked != tc.unasked || mismatch.WaitsOnPackage() == tc.unasked {
					t.Fatalf("wrong proof classification: %v", err)
				}
				if len(reg.Names()) != before {
					t.Fatal("refused descriptor published tools")
				}
				if strings.Contains(capture.String(), "event=ready") {
					t.Fatal("worker ready before failed descriptor proof")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(ap.sup.ReadyLine(), "descriptor_proof="+digest) {
				t.Fatal("supervised lane did not prove in worker")
			}
			res, err := reg.Execute(t.Context(), ap.ToolNames[0], nil)
			if err != nil || res.Error != "" || !strings.Contains(res.Output, "echoed") {
				t.Fatalf("valid plugin stopped working: %+v %v", res, err)
			}
		})
	}
}

func TestDescriptorProofCannotBorrowTheServingBroker(t *testing.T) {
	const id = "org.example.describe-no-grant"
	account := []byte(`[{"id":"ping"}]`)
	wasm := wasmgen.CallingDescriber(account, []byte(`{"operation":"kv.put","target":{"key":"during-proof"},"arguments":{"value":"forbidden"}}`))
	for _, worker := range []string{"", workerBin} {
		t.Run(worker, func(t *testing.T) {
			st := newBrokerStore(t)
			h, err := broker.New(broker.Config{Store: st, Grants: map[string]broker.Grant{id: {KV: true}}})
			if err != nil {
				t.Fatal(err)
			}

			ap, err := Activate(t.Context(), describingPkgWith(t, id, account, wasm, "ring4.kv"), newRegistry(t), &Options{Broker: h, WorkerBinary: worker, ReadyTimeout: map[string]time.Duration{id: 2 * time.Minute}})
			if err != nil {
				t.Fatal(err)
			}
			defer ap.Deactivate(t.Context())
			var n int
			if err := st.DB().QueryRow(`SELECT COUNT(*) FROM plugin_receipts WHERE plugin_id=?`, id).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Fatalf("proof reached serving broker: %d receipts", n)
			}
			if err := st.DB().QueryRow(`SELECT COUNT(*) FROM plugin_kv WHERE plugin_id=?`, id).Scan(&n); err != nil || n != 0 {
				t.Fatalf("proof mutated plugin storage: %d %v", n, err)
			}
			if status, _ := activationStorageCall(t, ap, `{"operation":"kv.put","target":{"key":"control"},"arguments":{"value":"works"}}`); status != "succeeded" {
				t.Fatal("serving write grant was not active")
			}
		})
	}
}
